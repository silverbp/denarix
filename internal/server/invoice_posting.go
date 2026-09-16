// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"github.com/silverbp/denarix/internal/db/sqlcgen"
	"github.com/silverbp/denarix/internal/ledgermath"
	"github.com/silverbp/denarix/internal/ledgerpost"
)

// Posting an invoice to the ledger: the AR/AP leg against the contact's
// own account, a revenue/expense leg per line, and sales tax split out
// per tax rate. Payment posting lives in payment_service.go.

// resolveContactLedgerAccountID looks up a contact's own AR sub-ledger account (customer side)
// or AP sub-ledger account (vendor side) - the role tables' ledger_account_id, not a column on
// contact itself (see customer/vendor in migrations/00001_initial.up.sql). Returns (nil, nil),
// not an error, if the contact has no row in that role's table at all.
func resolveContactLedgerAccountID(ctx context.Context, q *sqlcgen.Queries, contactID int64, isCustomerSide bool) (*int32, error) {
	if isCustomerSide {
		customer, err := q.GetCustomerByContactID(ctx, contactID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil
			}
			return nil, err
		}
		return customer.LedgerAccountID, nil
	}
	vendor, err := q.GetVendorByContactID(ctx, contactID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return vendor.LedgerAccountID, nil
}

// postInvoiceLedger posts an invoice to the ledger: creates a new
// ledger_transaction and its balanced entries. CreateInvoice/
// UpdateInvoiceLineItems require every line item's ledger_account_id up
// front, so the nil checks here (and in writeInvoiceLedgerEntries) are a
// safety net, not the primary enforcement.
func postInvoiceLedger(ctx context.Context, q *sqlcgen.Queries, businessID int64, invoice sqlcgen.Invoice, lineItems []sqlcgen.InvoiceLineItem, createdByUserID *int64) (int64, error) {
	contactLedgerAccountID, err := resolveContactLedgerAccountID(ctx, q, invoice.ContactID, invoice.InvoiceType == "SALES")
	if err != nil {
		return 0, err
	}
	if contactLedgerAccountID == nil {
		return 0, fmt.Errorf("cannot post invoice: contact %d has no customer/vendor ledger_account_id set", invoice.ContactID)
	}

	description := fmt.Sprintf("Invoice %s", invoice.InvoiceNumber)
	txn, err := q.CreateLedgerTransaction(ctx, sqlcgen.CreateLedgerTransactionParams{
		BusinessID:      businessID,
		TransactionDate: invoice.InvoiceDate,
		Description:     &description,
		CreatedByUserID: createdByUserID,
	})
	if err != nil {
		return 0, err
	}
	if err := writeInvoiceLedgerEntries(ctx, q, businessID, txn.ID, *contactLedgerAccountID, invoice, lineItems); err != nil {
		return 0, err
	}
	return txn.ID, nil
}

// repostInvoiceLedger regenerates an already-posted invoice's ledger entries
// in place after its line items change, rather than rejecting the edit or
// posting a reversal: the existing entries under ledgerTransactionID are
// soft-deleted and replaced with entries built from the invoice's current
// state, so the ledger transaction itself (and its id) stays put. The
// ledger_entry period-lock trigger fires on both the soft-delete (an UPDATE
// of deleted_at) and the inserts, so editing a transaction dated in a closed
// period is still rejected.
func repostInvoiceLedger(ctx context.Context, q *sqlcgen.Queries, businessID, ledgerTransactionID int64, invoice sqlcgen.Invoice, lineItems []sqlcgen.InvoiceLineItem) error {
	contactLedgerAccountID, err := resolveContactLedgerAccountID(ctx, q, invoice.ContactID, invoice.InvoiceType == "SALES")
	if err != nil {
		return err
	}
	if contactLedgerAccountID == nil {
		return fmt.Errorf("cannot repost invoice: contact %d has no customer/vendor ledger_account_id set", invoice.ContactID)
	}
	if err := q.SoftDeleteLedgerEntriesByTransaction(ctx, ledgerTransactionID); err != nil {
		return err
	}
	return writeInvoiceLedgerEntries(ctx, q, businessID, ledgerTransactionID, *contactLedgerAccountID, invoice, lineItems)
}

// writeInvoiceLedgerEntries generates and inserts the balanced ledger_entry
// rows for an invoice against an existing ledger transaction: an AR/AP leg
// against the contact's own account, a revenue/expense leg per line, and
// sales tax split out to each tax rate's own liability account. Shared by
// postInvoiceLedger (new transaction) and repostInvoiceLedger (existing
// transaction, entries replaced).
func writeInvoiceLedgerEntries(ctx context.Context, q *sqlcgen.Queries, businessID, txnID int64, contactLedgerAccountID int32, invoice sqlcgen.Invoice, lineItems []sqlcgen.InvoiceLineItem) error {
	totalAmount, err := ledgermath.NumericToDecimal(invoice.TotalAmount)
	if err != nil {
		return err
	}
	isSales := invoice.InvoiceType == "SALES"

	// AR/AP leg, against the contact's own ledger account. Skipped when the
	// invoice totals zero — ledger_entry's debit_or_credit check constraint
	// requires exactly one side strictly positive, which a zero amount on
	// either side can never satisfy. computeLines rejects a negative document
	// total, so totalAmount is never negative here in practice; debitCreditFor
	// is used anyway to keep this leg consistent with the per-line legs below.
	if !totalAmount.IsZero() {
		debit, credit := ledgerpost.DebitCreditFor(totalAmount, isSales)
		if err := ledgerpost.CreateDecimalEntry(ctx, q, businessID, txnID, contactLedgerAccountID, debit, credit); err != nil {
			return err
		}
	}

	// Per-line revenue/expense legs. SALES tax is split out to each tax
	// rate's own liability account (grouped, in case multiple lines share
	// one); PURCHASE tax is rolled into the line's own account instead of a
	// liability account — tax_liability_account_id models tax the business
	// COLLECTED and owes to a government, which fits sales tax, not tax
	// paid to a vendor.
	taxByLiabilityAccount := map[int32]decimal.Decimal{}
	for _, li := range lineItems {
		if li.LedgerAccountID == nil {
			return fmt.Errorf("line %d is missing ledger_account_id", li.LineNumber)
		}
		lineSubtotal, err := ledgermath.NumericToDecimal(li.LineSubtotal)
		if err != nil {
			return err
		}
		taxAmount, err := ledgermath.NumericToDecimal(li.TaxAmount)
		if err != nil {
			return err
		}

		if isSales {
			// Revenue leg: a positive subtotal credits the item's account, as
			// usual. A negative subtotal — a discount item's line — debits
			// the same account instead, which is exactly a contra-revenue
			// posting; no separate discount machinery needed.
			if !lineSubtotal.IsZero() {
				debit, credit := ledgerpost.DebitCreditFor(lineSubtotal, false)
				if err := ledgerpost.CreateDecimalEntry(ctx, q, businessID, txnID, *li.LedgerAccountID, debit, credit); err != nil {
					return err
				}
			}
			if li.TaxRateID != nil && !taxAmount.IsZero() {
				tr, err := q.GetTaxRate(ctx, *li.TaxRateID)
				if err != nil {
					return err
				}
				taxByLiabilityAccount[tr.TaxLiabilityAccountID] = taxByLiabilityAccount[tr.TaxLiabilityAccountID].Add(taxAmount)
			}
		} else {
			// Expense leg: symmetric with the revenue leg above — a negative
			// lineTotal (a discount on a PURCHASE) credits the account
			// instead of debiting it.
			lineTotal := lineSubtotal.Add(taxAmount)
			if !lineTotal.IsZero() {
				debit, credit := ledgerpost.DebitCreditFor(lineTotal, true)
				if err := ledgerpost.CreateDecimalEntry(ctx, q, businessID, txnID, *li.LedgerAccountID, debit, credit); err != nil {
					return err
				}
			}
		}
	}
	for accountID, amount := range taxByLiabilityAccount {
		// A discount line can net a rate's bucket to exactly zero (e.g. a
		// taxable line and its equal-and-opposite discount sharing a tax
		// rate); skip it the same way the per-line legs above do — the XOR
		// constraint on ledger_entry rejects a zero-amount entry regardless
		// of which side it's on.
		if amount.IsZero() {
			continue
		}
		debit, credit := ledgerpost.DebitCreditFor(amount, false)
		if err := ledgerpost.CreateDecimalEntry(ctx, q, businessID, txnID, accountID, debit, credit); err != nil {
			return err
		}
	}

	return ledgerpost.VerifyBalanced(ctx, q, txnID)
}
