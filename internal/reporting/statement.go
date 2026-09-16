// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package reporting

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/silverbp/denarix/internal/db/sqlcgen"
	"github.com/silverbp/denarix/internal/ledgermath"
)

// ErrNotACustomer is returned by CustomerStatement for a contact with no
// customer row - the caller (reporting_service.go / reporting_pdf.go) maps
// it to FailedPrecondition rather than silently returning an empty
// statement for a contact that was never onboarded as a customer.
var ErrNotACustomer = errors.New("contact is not a customer")

type StatementInvoiceLine struct {
	InvoiceID     int64
	InvoiceNumber string
	InvoiceDate   time.Time
	DueDate       time.Time
	TotalAmount   decimal.Decimal
	BalanceDue    decimal.Decimal
	Status        string
}

type StatementPaymentLine struct {
	PaymentID     int64
	PaymentNumber string
	PaymentDate   time.Time
	Amount        decimal.Decimal
}

// ErrNoLedgerAccount is returned by CustomerStatement for a customer with
// no ledger_account_id - only possible for a contact whose customer role
// predates auto-provisioning (see getOrCreateContactAccount in
// internal/server/system_accounts.go) and was explicitly created without
// one.
var ErrNoLedgerAccount = errors.New("customer has no ledger account")

// StatementActivityLine is one row of the customer's own AR sub-ledger
// account activity, date-ordered with a running balance: a debit increases
// the balance owed, a credit decreases it. Sourced from ledger_entry
// directly (not the invoice/payment tables), so a manual adjustment posted
// straight to the account - a write-off, a correction - shows up here too,
// not just invoices and payments. A consequence of that: a business that
// chooses to share one AR account across multiple customers (a valid
// topology, see docs/quickstart.md) will see every one of those customers'
// activity pooled into each other's statement, since the account itself -
// not the contact - is what this is scoped to. Auto-provisioned customers
// (the default - see internal/server/system_accounts.go) each get their
// own account, so this only affects a business that deliberately opted
// into sharing one.
type StatementActivityLine struct {
	Date           time.Time
	Description    string
	Debit          decimal.Decimal
	Credit         decimal.Decimal
	RunningBalance decimal.Decimal
}

type AgingBucket struct {
	Label  string // "Current", "1-30", "31-60", "61-90", "90+"
	Amount decimal.Decimal
}

type CustomerStatementResult struct {
	ContactID     int64
	ContactName   string
	PeriodStart   time.Time
	PeriodEnd     time.Time
	Invoices      []StatementInvoiceLine
	Payments      []StatementPaymentLine
	Activity      []StatementActivityLine
	EndingBalance decimal.Decimal
	AgingBuckets  []AgingBucket
}

// CustomerStatement reports one customer's own AR sub-ledger activity over
// [start, end] with a running balance, plus an AR aging snapshot (as of
// end) over every still-open invoice through end — independent of the
// [start, end] window, since aging is conventionally a point-in-time
// snapshot, not scoped to an activity range. Aging stays invoice-due-date
// based (a GL entry has no due date), even though activity/balance now
// come from the ledger, not the invoice/payment tables — see
// StatementActivityLine.
func CustomerStatement(ctx context.Context, q *sqlcgen.Queries, contactID int64, start, end time.Time) (*CustomerStatementResult, error) {
	contact, err := q.GetContact(ctx, contactID)
	if err != nil {
		return nil, err
	}
	customer, err := q.GetCustomerByContactID(ctx, contactID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%d: %w", contactID, ErrNotACustomer)
		}
		return nil, err
	}
	if customer.LedgerAccountID == nil {
		return nil, fmt.Errorf("%d: %w", contactID, ErrNoLedgerAccount)
	}

	result := &CustomerStatementResult{
		ContactID:   contactID,
		ContactName: contact.Name,
		PeriodStart: start,
		PeriodEnd:   end,
	}

	paymentRows, err := q.ListPaymentsForContact(ctx, sqlcgen.ListPaymentsForContactParams{
		ContactID:   contactID,
		PeriodStart: ledgermath.PgDate(start),
		PeriodEnd:   ledgermath.PgDate(end),
	})
	if err != nil {
		return nil, err
	}

	if err := loadStatementActivity(ctx, q, result, *customer.LedgerAccountID, start, end, paymentRows); err != nil {
		return nil, err
	}

	invoiceRows, err := q.ListInvoicesForContact(ctx, sqlcgen.ListInvoicesForContactParams{
		ContactID:   contactID,
		PeriodStart: ledgermath.PgDate(start),
		PeriodEnd:   ledgermath.PgDate(end),
	})
	if err != nil {
		return nil, err
	}
	for _, inv := range invoiceRows {
		total, err := ledgermath.NumericToDecimal(inv.TotalAmount)
		if err != nil {
			return nil, err
		}
		balanceDue, err := ledgermath.NumericToDecimal(inv.BalanceDue)
		if err != nil {
			return nil, err
		}
		result.Invoices = append(result.Invoices, StatementInvoiceLine{
			InvoiceID:     inv.ID,
			InvoiceNumber: inv.InvoiceNumber,
			InvoiceDate:   inv.InvoiceDate.Time,
			DueDate:       inv.DueDate.Time,
			TotalAmount:   total,
			BalanceDue:    balanceDue,
			Status:        inv.Status,
		})
	}
	for _, p := range paymentRows {
		amount, err := ledgermath.NumericToDecimal(p.Amount)
		if err != nil {
			return nil, err
		}
		result.Payments = append(result.Payments, StatementPaymentLine{
			PaymentID:     p.ID,
			PaymentNumber: p.PaymentNumber,
			PaymentDate:   p.PaymentDate.Time,
			Amount:        amount,
		})
	}

	agingRows, err := q.ListInvoicesForContact(ctx, sqlcgen.ListInvoicesForContactParams{
		ContactID:   contactID,
		PeriodStart: ledgermath.PgDate(ledgermath.InceptionDate),
		PeriodEnd:   ledgermath.PgDate(end),
	})
	if err != nil {
		return nil, err
	}
	result.AgingBuckets, err = computeAgingBuckets(agingRows, end)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// loadStatementActivity fills result.Activity/EndingBalance from
// ledgerAccountID's own GL activity over [start, end] (built on
// GeneralLedger - every posting against the account, document-generated or
// manual, in the account's own normal-balance direction), merged with any
// payment in paymentRows that's been applied to an invoice but never
// posted to the ledger (payment.ledger_transaction_id NULL - CreatePayment
// doesn't require --account, see maybePostPayment). Without this merge the
// statement's balance would silently disagree with the invoice(s) it was
// applied to: balance_due already reflects an unposted payment, the GL
// does not. A posted payment is skipped here - it already has its own
// ledger_entry, counted once via GeneralLedger.
func loadStatementActivity(ctx context.Context, q *sqlcgen.Queries, result *CustomerStatementResult, ledgerAccountID int32, start, end time.Time, paymentRows []sqlcgen.Payment) error {
	gl, err := GeneralLedger(ctx, q, ledgerAccountID, start, end)
	if err != nil {
		return err
	}
	account, err := q.GetLedgerAccount(ctx, ledgerAccountID)
	if err != nil {
		return err
	}
	accountType, err := q.GetLedgerAccountType(ctx, account.AccountTypeID)
	if err != nil {
		return err
	}

	type rawLine struct {
		date          time.Time
		description   string
		debit, credit decimal.Decimal
	}
	raw := make([]rawLine, 0, len(gl.Lines)+len(paymentRows))
	for _, l := range gl.Lines {
		description := ""
		if l.Description != nil {
			description = *l.Description
		}
		raw = append(raw, rawLine{date: l.TransactionDate, description: description, debit: l.Debit, credit: l.Credit})
	}
	for _, p := range paymentRows {
		if p.LedgerTransactionID != nil {
			continue // already reflected via its own ledger_entry, above
		}
		amount, err := ledgermath.NumericToDecimal(p.Amount)
		if err != nil {
			return err
		}
		raw = append(raw, rawLine{date: p.PaymentDate.Time, description: fmt.Sprintf("Payment %s (not posted to ledger)", p.PaymentNumber), credit: amount})
	}
	sort.SliceStable(raw, func(i, j int) bool { return raw[i].date.Before(raw[j].date) })

	running := decimal.Zero
	for _, r := range raw {
		running = running.Add(ledgermath.NetBalance(accountType.NormalBalance, r.debit, r.credit))
		result.Activity = append(result.Activity, StatementActivityLine{
			Date:           r.date,
			Description:    r.description,
			Debit:          r.debit,
			Credit:         r.credit,
			RunningBalance: running,
		})
	}
	result.EndingBalance = running
	return nil
}

func computeAgingBuckets(invoices []sqlcgen.Invoice, asOf time.Time) ([]AgingBucket, error) {
	buckets := []AgingBucket{
		{Label: "Current", Amount: decimal.Zero},
		{Label: "1-30", Amount: decimal.Zero},
		{Label: "31-60", Amount: decimal.Zero},
		{Label: "61-90", Amount: decimal.Zero},
		{Label: "90+", Amount: decimal.Zero},
	}

	for _, inv := range invoices {
		balanceDue, err := ledgermath.NumericToDecimal(inv.BalanceDue)
		if err != nil {
			return nil, err
		}
		if !balanceDue.IsPositive() {
			continue
		}

		daysOverdue := int(asOf.Sub(inv.DueDate.Time).Hours() / 24)
		idx := 0
		switch {
		case daysOverdue <= 0:
			idx = 0
		case daysOverdue <= 30:
			idx = 1
		case daysOverdue <= 60:
			idx = 2
		case daysOverdue <= 90:
			idx = 3
		default:
			idx = 4
		}
		buckets[idx].Amount = buckets[idx].Amount.Add(balanceDue)
	}
	return buckets, nil
}
