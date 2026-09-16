// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"testing"

	"google.golang.org/grpc/codes"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
)

// TestGetCustomerStatement_RequiresCustomerRole checks that a statement
// can't be pulled for a contact that was never marked --customer - a plain
// contact (or a vendor-only one) has no invoice/payment activity by
// definition, so silently returning an empty statement would hide a typo'd
// contact id rather than reporting it.
func TestGetCustomerStatement_RequiresCustomerRole(t *testing.T) {
	defer failOnPanic(t)
	tc := newTenant(t)
	ctx := tc.ctx
	contacts := newContactService(tc.store)
	reports := newReportingService(tc.store)

	plain := must(contacts.CreateContact(ctx, &denarixv1.CreateContactRequest{
		BusinessId: tc.businessID, ContactNumber: "C-1", Name: "Not A Customer",
	})).GetContact()

	_, err := reports.GetCustomerStatement(ctx, &denarixv1.GetCustomerStatementRequest{
		ContactId: plain.GetId(), PeriodStart: dateOf(2026, 1, 1), PeriodEnd: dateOf(2026, 12, 31),
	})
	wantCode(t, err, codes.FailedPrecondition)

	_, err = reports.GetCustomerStatementPdf(ctx, &denarixv1.GetCustomerStatementPdfRequest{
		ContactId: plain.GetId(), PeriodStart: dateOf(2026, 1, 1), PeriodEnd: dateOf(2026, 12, 31),
	})
	wantCode(t, err, codes.FailedPrecondition)
}

// TestGetCustomerStatement_ReflectsManualLedgerEntry checks that the
// statement's activity/running-balance are sourced from the customer's
// ledger account directly (see loadStatementActivity in
// internal/reporting/statement.go), not just the invoice/payment tables -
// a manual adjustment posted straight to the account shows up too, while
// aging still comes from the invoice.
func TestGetCustomerStatement_ReflectsManualLedgerEntry(t *testing.T) {
	defer failOnPanic(t)
	tc := newTenant(t)
	ctx := tc.ctx

	contacts := newContactService(tc.store)
	invoices := newInvoiceService(tc.store)
	txns := newLedgerTransactionService(tc.store)
	reports := newReportingService(tc.store)

	contactID, itemID := tc.customerAndItem()
	custAR := must(contacts.GetContact(ctx, &denarixv1.GetContactRequest{Id: contactID})).GetContact().GetCustomer().GetLedgerAccountId()
	writeOff := tc.account(5, "6900", "Bad Debt Expense")

	must(invoices.CreateInvoice(ctx, &denarixv1.CreateInvoiceRequest{
		BusinessId: tc.businessID, ContactId: contactID, InvoiceType: "SALES", InvoiceDate: dateOf(2026, 1, 1), DueDate: dateOf(2026, 1, 31),
		LineItems: []*denarixv1.NewDocumentLineItem{{ItemId: itemID, LineNumber: 1}},
	}))

	desc := "Write off uncollectible balance"
	must(txns.CreateLedgerTransaction(ctx, &denarixv1.CreateLedgerTransactionRequest{
		BusinessId: tc.businessID, TransactionDate: dateOf(2026, 1, 15), Description: &desc,
		Entries: []*denarixv1.NewLedgerEntry{
			{AccountId: writeOff, DebitAmount: dec("25.00")},
			{AccountId: custAR, CreditAmount: dec("25.00")},
		},
	}))

	stmt := must(reports.GetCustomerStatement(ctx, &denarixv1.GetCustomerStatementRequest{
		ContactId: contactID, PeriodStart: dateOf(2026, 1, 1), PeriodEnd: dateOf(2026, 1, 31),
	})).GetStatement()

	if len(stmt.GetActivity()) != 2 {
		t.Fatalf("activity = %d lines, want 2 (invoice + manual write-off): %+v", len(stmt.GetActivity()), stmt.GetActivity())
	}
	last := stmt.GetActivity()[len(stmt.GetActivity())-1]
	if last.GetDescription() != desc {
		t.Fatalf("last activity line description = %q, want %q", last.GetDescription(), desc)
	}
	eqAmount(t, "ending balance", stmt.GetEndingBalance(), "75.00") // 100.00 invoice - 25.00 write-off
	if len(stmt.GetInvoices()) != 1 {
		t.Fatalf("aging/invoices should still come from the invoice table: got %d invoices", len(stmt.GetInvoices()))
	}
}

// TestGetCustomerStatement_UnpostedPaymentReflected checks that a payment
// applied to an invoice without ever posting to the ledger (CreatePayment
// doesn't require --account/ledger_account_id) still lowers the
// ledger-sourced statement balance - not just the invoice's own
// balance_due - since otherwise the statement would show the customer
// still owing the full invoice amount while the invoice itself reads PAID.
func TestGetCustomerStatement_UnpostedPaymentReflected(t *testing.T) {
	defer failOnPanic(t)
	tc := newTenant(t)
	ctx := tc.ctx

	invoices := newInvoiceService(tc.store)
	payments := newPaymentService(tc.store)
	reports := newReportingService(tc.store)

	contactID, itemID := tc.customerAndItem()
	inv := must(invoices.CreateInvoice(ctx, &denarixv1.CreateInvoiceRequest{
		BusinessId: tc.businessID, ContactId: contactID, InvoiceType: "SALES", InvoiceDate: dateOf(2026, 1, 1), DueDate: dateOf(2026, 1, 31),
		LineItems: []*denarixv1.NewDocumentLineItem{{ItemId: itemID, LineNumber: 1}},
	})).GetInvoice()
	inv = must(invoices.UpdateInvoiceStatus(ctx, &denarixv1.UpdateInvoiceStatusRequest{Id: inv.GetId(), Status: "SENT"})).GetInvoice()

	// No LedgerAccountId - this payment never posts to the ledger, but
	// still fully applies to (and pays off) the invoice.
	pay := must(payments.CreatePayment(ctx, &denarixv1.CreatePaymentRequest{
		BusinessId: tc.businessID, ContactId: contactID, PaymentType: "RECEIVED", PaymentNumber: "P-1", PaymentDate: dateOf(2026, 1, 15),
		Amount: dec("100.00"), PaymentMethod: "CASH",
		Applications: []*denarixv1.PaymentApplicationInput{{InvoiceId: inv.GetId(), AppliedAmount: dec("100.00")}},
	})).GetPayment()
	if pay.LedgerTransactionId != nil {
		t.Fatalf("expected an unposted payment (no ledger_account_id given), got ledger_transaction_id=%d", pay.GetLedgerTransactionId())
	}
	inv = must(invoices.GetInvoice(ctx, &denarixv1.GetInvoiceRequest{Id: inv.GetId()})).GetInvoice()
	if inv.GetStatus() != "PAID" {
		t.Fatalf("invoice status = %q, want PAID", inv.GetStatus())
	}

	stmt := must(reports.GetCustomerStatement(ctx, &denarixv1.GetCustomerStatementRequest{
		ContactId: contactID, PeriodStart: dateOf(2026, 1, 1), PeriodEnd: dateOf(2026, 1, 31),
	})).GetStatement()
	eqAmount(t, "ending balance", stmt.GetEndingBalance(), "0.00")
	if len(stmt.GetActivity()) != 2 {
		t.Fatalf("activity = %d lines, want 2 (invoice debit + unposted payment credit): %+v", len(stmt.GetActivity()), stmt.GetActivity())
	}
	last := stmt.GetActivity()[len(stmt.GetActivity())-1]
	if last.GetDescription() != "Payment P-1 (not posted to ledger)" {
		t.Fatalf("last activity line description = %q", last.GetDescription())
	}
}
