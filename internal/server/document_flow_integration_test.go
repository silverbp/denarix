// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"testing"

	"github.com/shopspring/decimal"
	typepb "google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/grpc/codes"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
)

// eqAmount compares a wire Decimal numerically ("220" == "220.00").
func eqAmount(t *testing.T, field string, got *denarixv1.Decimal, want string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %s", field, want)
	}
	if !decimal.RequireFromString(got.GetValue()).Equal(decimal.RequireFromString(want)) {
		t.Fatalf("%s = %s, want %s", field, got.GetValue(), want)
	}
}

// balanced checks a transaction's entries sum to the same debit and credit.
func balanced(t *testing.T, txn *denarixv1.LedgerTransaction) decimal.Decimal {
	t.Helper()
	dr, cr := decimal.Zero, decimal.Zero
	for _, e := range txn.GetEntries() {
		dr = dr.Add(decimal.RequireFromString(e.GetDebitAmount().GetValue()))
		cr = cr.Add(decimal.RequireFromString(e.GetCreditAmount().GetValue()))
	}
	if !dr.Equal(cr) {
		t.Fatalf("transaction %d unbalanced: debit %s credit %s", txn.GetId(), dr, cr)
	}
	return dr
}

// TestDocumentFlow walks the whole trading cycle through the real handlers:
// chart of accounts, customer and vendor, catalog, estimate -> invoice ->
// payment -> void -> cancel, a purchase invoice, raw postings and their
// reversal, bank reconciliation, every report, a period close and its
// reversal, and entity context. It is the end-to-end guard behind the
// server refactors.
func TestDocumentFlow(t *testing.T) {
	defer failOnPanic(t)
	tc := newTenant(t)
	ctx := tc.ctx
	q := tc.store.Queries

	accounts := newLedgerAccountService(tc.store)
	contacts := newContactService(tc.store)
	items := newItemService(tc.store)
	taxRates := newTaxRateService(tc.store)
	estimates := newEstimateService(tc.store)
	invoices := newInvoiceService(tc.store)
	payments := newPaymentService(tc.store)
	txns := newLedgerTransactionService(tc.store)
	statements := newBankStatementService(tc.store)
	reports := newReportingService(tc.store)
	closes := newPeriodCloseService(tc.store)
	notes := newEntityContextService(tc.store)

	// --- chart of accounts ------------------------------------------------
	cash := tc.reconcilableAccount("1000", "Cash")
	arContainer := must(accounts.CreateLedgerAccount(ctx, &denarixv1.CreateLedgerAccountRequest{BusinessId: tc.businessID, AccountTypeId: 1, Code: "1100", Name: "Accounts Receivable", IsContainer: true})).GetAccount().GetId()
	taxLiab := tc.account(6, "2100", "Sales Tax Payable")
	equity := tc.account(3, "3000", "Opening Balance Equity")
	revenue := tc.account(4, "4000", "Consulting Revenue")
	expense := tc.account(5, "6000", "Job Supplies")

	// An item may not point at a container account.
	_, err := items.CreateItem(ctx, &denarixv1.CreateItemRequest{BusinessId: tc.businessID, ItemCode: "BAD", Name: "Bad", RetailPrice: dec("1"), DefaultLedgerAccountId: &arContainer})
	wantCode(t, err, codes.InvalidArgument)

	// --- parties, catalog, tax ------------------------------------------
	// Neither contact is given a ledger account explicitly - CreateContact
	// always provisions the customer/vendor's own AR/AP sub-account itself
	// (see getOrCreateContactAccount), parented under arContainer above
	// (found by its code, "1100") for the customer side.
	customer := must(contacts.CreateContact(ctx, &denarixv1.CreateContactRequest{BusinessId: tc.businessID, ContactNumber: "C-1", Name: "Acme", IsCustomer: true})).GetContact()
	custAR := customer.GetCustomer().GetLedgerAccountId()
	if customer.GetCustomer() == nil || custAR == 0 {
		t.Fatalf("customer role not attached: %+v", customer)
	}
	if acct := must(accounts.GetLedgerAccount(ctx, &denarixv1.GetLedgerAccountRequest{Id: custAR})).GetAccount(); acct.GetParentAccountId() != arContainer {
		t.Fatalf("customer AR account not parented under arContainer: %+v", acct)
	}
	vendor := must(contacts.CreateContact(ctx, &denarixv1.CreateContactRequest{BusinessId: tc.businessID, ContactNumber: "V-1", Name: "Supplies Co", IsCustomer: false, IsVendor: true})).GetContact()
	if vendor.GetVendor().GetLedgerAccountId() == 0 {
		t.Fatalf("vendor created with no auto-provisioned ledger account")
	}
	salesTax := must(taxRates.CreateTaxRate(ctx, &denarixv1.CreateTaxRateRequest{BusinessId: tc.businessID, Name: "Sales Tax", Rate: dec("0.10"), TaxLiabilityAccountId: taxLiab})).GetTaxRate()
	taxID := salesTax.GetId()
	consult := must(items.CreateItem(ctx, &denarixv1.CreateItemRequest{BusinessId: tc.businessID, ItemCode: "CONSULT", Name: "Consulting", RetailPrice: dec("100.00"), IsTaxable: true, DefaultTaxRateId: &taxID, DefaultLedgerAccountId: &revenue})).GetItem()
	supplies := must(items.CreateItem(ctx, &denarixv1.CreateItemRequest{BusinessId: tc.businessID, ItemCode: "SUPPLIES", Name: "Supplies", RetailPrice: dec("50.00"), DefaultLedgerAccountId: &expense})).GetItem()

	// A line may not reference another business's item (or a nonexistent one).
	_, err = estimates.CreateEstimate(ctx, &denarixv1.CreateEstimateRequest{BusinessId: tc.businessID, CustomerId: customer.GetId(), EstimateDate: dateOf(2026, 1, 1), ExpirationDate: dateOf(2026, 2, 1),
		LineItems: []*denarixv1.NewDocumentLineItem{{ItemId: 1 << 40, LineNumber: 1}}})
	wantCode(t, err, codes.InvalidArgument)

	// --- estimate -> invoice ---------------------------------------------
	est := must(estimates.CreateEstimate(ctx, &denarixv1.CreateEstimateRequest{
		BusinessId: tc.businessID, CustomerId: customer.GetId(), EstimateDate: dateOf(2026, 1, 1), ExpirationDate: dateOf(2026, 2, 1),
		LineItems: []*denarixv1.NewDocumentLineItem{{ItemId: consult.GetId(), LineNumber: 1, Quantity: dec("2")}},
	})).GetEstimate()
	eqAmount(t, "estimate subtotal", est.GetSubtotal(), "200.00")
	eqAmount(t, "estimate tax", est.GetTotalTaxAmount(), "20.00")
	eqAmount(t, "estimate total", est.GetTotalAmount(), "220.00")
	if got := est.GetLineItems()[0].GetDescription(); got != "Consulting" {
		t.Fatalf("line description should default from the item, got %q", got)
	}
	est = must(estimates.UpdateEstimate(ctx, &denarixv1.UpdateEstimateRequest{Id: est.GetId(), ResourceVersion: est.GetResourceVersion(), Notes: ptr("net 30"), ExpirationDate: dateOf(2026, 3, 1)})).GetEstimate()
	if est.GetNotes() != "net 30" || est.GetExpirationDate().GetMonth() != 3 || est.GetTerms() != "" {
		t.Fatalf("estimate header update should change only the passed fields: %+v", est)
	}
	_, err = estimates.UpdateEstimate(ctx, &denarixv1.UpdateEstimateRequest{Id: est.GetId(), ResourceVersion: est.GetResourceVersion() - 1, Terms: ptr("stale")})
	wantCode(t, err, codes.Aborted) // stale resource_version
	est = must(estimates.UpdateEstimateStatus(ctx, &denarixv1.UpdateEstimateStatusRequest{Id: est.GetId(), Status: "SENT"})).GetEstimate()
	est = must(estimates.UpdateEstimateStatus(ctx, &denarixv1.UpdateEstimateStatusRequest{Id: est.GetId(), Status: "ACCEPTED"})).GetEstimate()
	if pdfBytes := must(estimates.GetEstimatePdf(ctx, &denarixv1.GetEstimatePdfRequest{Id: est.GetId()})).GetContent(); !bytes.HasPrefix(pdfBytes, []byte("%PDF")) {
		t.Fatal("estimate pdf did not render")
	}
	listed := must(estimates.ListEstimates(ctx, &denarixv1.ListEstimatesRequest{BusinessId: tc.businessID, IncludeAll: true})).GetEstimates()
	if len(listed) != 1 || len(listed[0].GetLineItems()) != 1 {
		t.Fatalf("list estimates should carry line items: %+v", listed)
	}

	estID := est.GetId()
	inv := must(invoices.CreateInvoice(ctx, &denarixv1.CreateInvoiceRequest{
		BusinessId: tc.businessID, ContactId: customer.GetId(), InvoiceType: "SALES", EstimateId: &estID,
		InvoiceDate: dateOf(2026, 1, 5), DueDate: dateOf(2026, 2, 5),
	})).GetInvoice()
	eqAmount(t, "invoice total", inv.GetTotalAmount(), "220.00")
	eqAmount(t, "invoice balance", inv.GetBalanceDue(), "220.00")
	if inv.LedgerTransactionId == nil {
		t.Fatal("invoice should be posted on create")
	}
	if got := inv.GetLineItems()[0].GetLedgerAccountId(); got != revenue {
		t.Fatalf("invoice line account = %d, want the item's %d", got, revenue)
	}
	posting := must(txns.GetLedgerTransaction(ctx, &denarixv1.GetLedgerTransactionRequest{Id: inv.GetLedgerTransactionId()})).GetTransaction()
	if total := balanced(t, posting); !total.Equal(decimal.RequireFromString("220")) {
		t.Fatalf("invoice posting total = %s, want 220", total)
	}
	if len(posting.GetEntries()) != 3 { // AR, revenue, tax liability
		t.Fatalf("invoice posting has %d entries, want 3", len(posting.GetEntries()))
	}
	// A document-linked transaction can't be reversed directly.
	_, err = txns.ReverseLedgerTransaction(ctx, &denarixv1.ReverseLedgerTransactionRequest{Id: posting.GetId()})
	wantCode(t, err, codes.FailedPrecondition)

	// --- payment: apply, post, void --------------------------------------
	_, err = payments.CreatePayment(ctx, &denarixv1.CreatePaymentRequest{BusinessId: tc.businessID, ContactId: customer.GetId(), PaymentType: "RECEIVED", PaymentNumber: "P-0", PaymentDate: dateOf(2026, 1, 10), Amount: dec("220.00"), PaymentMethod: "CASH",
		Applications: []*denarixv1.PaymentApplicationInput{{InvoiceId: inv.GetId(), AppliedAmount: dec("220.00")}}})
	wantCode(t, err, codes.FailedPrecondition) // still DRAFT
	inv = must(invoices.UpdateInvoiceStatus(ctx, &denarixv1.UpdateInvoiceStatusRequest{Id: inv.GetId(), Status: "SENT"})).GetInvoice()

	pay := must(payments.CreatePayment(ctx, &denarixv1.CreatePaymentRequest{
		BusinessId: tc.businessID, ContactId: customer.GetId(), PaymentType: "RECEIVED", PaymentNumber: "P-1", PaymentDate: dateOf(2026, 1, 10),
		Amount: dec("220.00"), PaymentMethod: "CASH", LedgerAccountId: &cash,
		Applications: []*denarixv1.PaymentApplicationInput{{InvoiceId: inv.GetId(), AppliedAmount: dec("220.00")}},
	})).GetPayment()
	if pay.LedgerTransactionId == nil || len(pay.GetApplications()) != 1 {
		t.Fatalf("payment not posted/applied: %+v", pay)
	}
	balanced(t, must(txns.GetLedgerTransaction(ctx, &denarixv1.GetLedgerTransactionRequest{Id: pay.GetLedgerTransactionId()})).GetTransaction())
	inv = must(invoices.GetInvoice(ctx, &denarixv1.GetInvoiceRequest{Id: inv.GetId()})).GetInvoice()
	if inv.GetStatus() != "PAID" {
		t.Fatalf("invoice status = %s, want PAID", inv.GetStatus())
	}
	eqAmount(t, "paid invoice balance", inv.GetBalanceDue(), "0")
	if listedPays := must(payments.ListPayments(ctx, &denarixv1.ListPaymentsRequest{BusinessId: tc.businessID})).GetPayments(); len(listedPays) != 1 || len(listedPays[0].GetApplications()) != 1 {
		t.Fatalf("list payments should carry applications: %+v", listedPays)
	}

	// Lines can't change on a PAID invoice, nor can it be cancelled while applied.
	_, err = invoices.UpdateInvoiceLineItems(ctx, &denarixv1.UpdateInvoiceLineItemsRequest{Id: inv.GetId(), LineItems: []*denarixv1.NewDocumentLineItem{{ItemId: consult.GetId(), LineNumber: 1}}})
	wantCode(t, err, codes.FailedPrecondition)
	_, err = invoices.UpdateInvoiceStatus(ctx, &denarixv1.UpdateInvoiceStatusRequest{Id: inv.GetId(), Status: "CANCELLED"})
	wantCode(t, err, codes.FailedPrecondition)

	voided := must(payments.VoidPayment(ctx, &denarixv1.VoidPaymentRequest{Id: pay.GetId()})).GetPayment()
	if voided.GetId() != pay.GetId() {
		t.Fatal("void should echo the payment")
	}
	_, err = payments.GetPayment(ctx, &denarixv1.GetPaymentRequest{Id: pay.GetId()})
	wantCode(t, err, codes.NotFound)
	inv = must(invoices.GetInvoice(ctx, &denarixv1.GetInvoiceRequest{Id: inv.GetId()})).GetInvoice()
	if inv.GetStatus() != "SENT" {
		t.Fatalf("invoice status after void = %s, want SENT", inv.GetStatus())
	}
	eqAmount(t, "balance after void", inv.GetBalanceDue(), "220.00")

	// Replace the lines on the now-unpaid invoice: the posting is regenerated in place.
	inv = must(invoices.UpdateInvoiceLineItems(ctx, &denarixv1.UpdateInvoiceLineItemsRequest{Id: inv.GetId(), ResourceVersion: inv.GetResourceVersion(),
		LineItems: []*denarixv1.NewDocumentLineItem{{ItemId: consult.GetId(), LineNumber: 1, Quantity: dec("3")}}})).GetInvoice()
	eqAmount(t, "invoice total after update-lines", inv.GetTotalAmount(), "330.00")
	if inv.GetLedgerTransactionId() != posting.GetId() {
		t.Fatal("update-lines should keep the same ledger transaction id")
	}
	reposted := must(txns.GetLedgerTransaction(ctx, &denarixv1.GetLedgerTransactionRequest{Id: posting.GetId()})).GetTransaction()
	if total := balanced(t, reposted); !total.Equal(decimal.RequireFromString("330")) {
		t.Fatalf("reposted total = %s, want 330", total)
	}

	cancelled := must(invoices.UpdateInvoiceStatus(ctx, &denarixv1.UpdateInvoiceStatusRequest{Id: inv.GetId(), Status: "CANCELLED", ReversalDate: dateOf(2026, 1, 20)})).GetInvoice()
	if cancelled.GetStatus() != "CANCELLED" {
		t.Fatalf("status = %s", cancelled.GetStatus())
	}
	eqAmount(t, "cancelled balance", cancelled.GetBalanceDue(), "0")
	_, err = invoices.UpdateInvoice(ctx, &denarixv1.UpdateInvoiceRequest{Id: inv.GetId(), Notes: ptr("x")})
	wantCode(t, err, codes.FailedPrecondition)
	if pdfBytes := must(invoices.GetInvoicePdf(ctx, &denarixv1.GetInvoicePdfRequest{Id: inv.GetId()})).GetContent(); !bytes.HasPrefix(pdfBytes, []byte("%PDF")) {
		t.Fatal("invoice pdf did not render")
	}

	// --- purchase invoice against the vendor --------------------------------
	purchase := must(invoices.CreateInvoice(ctx, &denarixv1.CreateInvoiceRequest{
		BusinessId: tc.businessID, ContactId: vendor.GetId(), InvoiceType: "PURCHASE", InvoiceNumber: ptr("SUP-77"),
		InvoiceDate: dateOf(2026, 1, 6), DueDate: dateOf(2026, 2, 6),
		LineItems: []*denarixv1.NewDocumentLineItem{{ItemId: supplies.GetId(), LineNumber: 1, Quantity: dec("4")}},
	})).GetInvoice()
	eqAmount(t, "purchase total", purchase.GetTotalAmount(), "200.00")
	balanced(t, must(txns.GetLedgerTransaction(ctx, &denarixv1.GetLedgerTransactionRequest{Id: purchase.GetLedgerTransactionId()})).GetTransaction())
	// Default list hides the cancelled one; --all shows both.
	if open := must(invoices.ListInvoices(ctx, &denarixv1.ListInvoicesRequest{BusinessId: tc.businessID})).GetInvoices(); len(open) != 1 {
		t.Fatalf("open invoices = %d, want 1", len(open))
	}
	if all := must(invoices.ListInvoices(ctx, &denarixv1.ListInvoicesRequest{BusinessId: tc.businessID, IncludeAll: true})).GetInvoices(); len(all) != 2 || len(all[0].GetLineItems()) != 1 {
		t.Fatalf("all invoices = %+v", all)
	}

	// --- raw posting and reversal -----------------------------------------
	opening := must(txns.CreateLedgerTransaction(ctx, &denarixv1.CreateLedgerTransactionRequest{
		BusinessId: tc.businessID, TransactionDate: dateOf(2026, 1, 2), Description: ptr("Opening balance"),
		Entries: []*denarixv1.NewLedgerEntry{{AccountId: cash, DebitAmount: dec("1000.00")}, {AccountId: equity, CreditAmount: dec("1000.00")}},
	})).GetTransaction()
	_, err = txns.CreateLedgerTransaction(ctx, &denarixv1.CreateLedgerTransactionRequest{BusinessId: tc.businessID, TransactionDate: dateOf(2026, 1, 2),
		Entries: []*denarixv1.NewLedgerEntry{{AccountId: cash, DebitAmount: dec("1")}, {AccountId: equity, CreditAmount: dec("2")}}})
	wantCode(t, err, codes.InvalidArgument) // unbalanced
	mistake := must(txns.CreateLedgerTransaction(ctx, &denarixv1.CreateLedgerTransactionRequest{
		BusinessId: tc.businessID, TransactionDate: dateOf(2026, 1, 3),
		Entries: []*denarixv1.NewLedgerEntry{{AccountId: expense, DebitAmount: dec("5.00")}, {AccountId: cash, CreditAmount: dec("5.00")}},
	})).GetTransaction()
	reversal := must(txns.ReverseLedgerTransaction(ctx, &denarixv1.ReverseLedgerTransactionRequest{Id: mistake.GetId()})).GetTransaction()
	if reversal.GetReversesLedgerTransactionId() != mistake.GetId() {
		t.Fatal("reversal should link back to the original")
	}
	_, err = txns.ReverseLedgerTransaction(ctx, &denarixv1.ReverseLedgerTransactionRequest{Id: mistake.GetId()})
	wantCode(t, err, codes.FailedPrecondition) // already reversed
	_, err = txns.ReverseLedgerTransaction(ctx, &denarixv1.ReverseLedgerTransactionRequest{Id: reversal.GetId()})
	wantCode(t, err, codes.FailedPrecondition) // a reversal can't be reversed
	if page := must(txns.ListLedgerTransactions(ctx, &denarixv1.ListLedgerTransactionsRequest{BusinessId: tc.businessID, PageSize: 2})).GetTransactions(); len(page) != 2 || len(page[0].GetEntries()) == 0 {
		t.Fatalf("list transactions should page and carry entries: %+v", page)
	}

	// --- list filters ----------------------------------------------------
	byDescription := must(txns.ListLedgerTransactions(ctx, &denarixv1.ListLedgerTransactionsRequest{BusinessId: tc.businessID, DescriptionContains: ptr("OPENING BAL")})).GetTransactions()
	if len(byDescription) != 1 || byDescription[0].GetId() != opening.GetId() {
		t.Fatalf("description filter should be a case-insensitive substring match: %+v", byDescription)
	}
	byDate := must(txns.ListLedgerTransactions(ctx, &denarixv1.ListLedgerTransactionsRequest{BusinessId: tc.businessID, StartDate: dateOf(2026, 1, 2), EndDate: dateOf(2026, 1, 2)})).GetTransactions()
	if len(byDate) == 0 {
		t.Fatal("date filter should include the opening transaction")
	}
	for _, txn := range byDate {
		if txn.GetTransactionDate().GetDay() != 2 {
			t.Fatalf("date filter returned a transaction outside the range: %+v", txn)
		}
	}
	_, err = txns.ListLedgerTransactions(ctx, &denarixv1.ListLedgerTransactionsRequest{BusinessId: tc.businessID, StartDate: dateOf(2026, 1, 3), EndDate: dateOf(2026, 1, 2)})
	wantCode(t, err, codes.InvalidArgument) // start after end
	_, err = txns.ListLedgerTransactions(ctx, &denarixv1.ListLedgerTransactionsRequest{BusinessId: tc.businessID, AccountId: ptr(int32(1 << 30))})
	wantCode(t, err, codes.InvalidArgument) // unknown account
	var (
		byAccount []*denarixv1.LedgerTransaction
		pageToken string
		pages     int
	)
	for {
		resp := must(txns.ListLedgerTransactions(ctx, &denarixv1.ListLedgerTransactionsRequest{BusinessId: tc.businessID, AccountId: ptr(expense), PageSize: 1, PageToken: pageToken}))
		byAccount = append(byAccount, resp.GetTransactions()...)
		pages++
		if pageToken = resp.GetNextPageToken(); pageToken == "" {
			break
		}
	}
	if pages < 2 || len(byAccount) < 2 {
		t.Fatalf("account filter should page through at least the mistake and its reversal, got %d over %d pages", len(byAccount), pages)
	}
	seen := map[int64]bool{}
	for _, txn := range byAccount {
		seen[txn.GetId()] = true
		var hit bool
		for _, e := range txn.GetEntries() {
			hit = hit || e.GetAccountId() == expense
		}
		if !hit {
			t.Fatalf("account filter returned a transaction with no expense entry: %+v", txn)
		}
	}
	if !seen[mistake.GetId()] || !seen[reversal.GetId()] {
		t.Fatal("account filter should include both the mistake and its reversal")
	}

	// --- list ordering by transaction_date, not posting order -------------
	// Posted in this id order (ascending) but with an out-of-order date on
	// the last one (a back-dated correction), so id DESC and
	// (transaction_date, id) DESC disagree about the order.
	orderAcct := tc.account(4, "4900", "Ordering Test Revenue")
	postOrdered := func(date *typepb.Date) *denarixv1.LedgerTransaction {
		return must(txns.CreateLedgerTransaction(ctx, &denarixv1.CreateLedgerTransactionRequest{
			BusinessId: tc.businessID, TransactionDate: date,
			Entries: []*denarixv1.NewLedgerEntry{{AccountId: orderAcct, CreditAmount: dec("1.00")}, {AccountId: equity, DebitAmount: dec("1.00")}},
		})).GetTransaction()
	}
	oMid := postOrdered(dateOf(2026, 1, 15))
	oLatest := postOrdered(dateOf(2026, 1, 20))
	oBackdated := postOrdered(dateOf(2026, 1, 1)) // highest id, earliest date

	var ordered []*denarixv1.LedgerTransaction
	var token string
	for {
		resp := must(txns.ListLedgerTransactions(ctx, &denarixv1.ListLedgerTransactionsRequest{
			BusinessId: tc.businessID, AccountId: ptr(orderAcct), PageSize: 1, PageToken: token,
		}))
		ordered = append(ordered, resp.GetTransactions()...)
		if token = resp.GetNextPageToken(); token == "" {
			break
		}
	}
	if len(ordered) != 3 {
		t.Fatalf("expected 3 ordering-test transactions across pages, got %d", len(ordered))
	}
	wantOrder := []int64{oLatest.GetId(), oMid.GetId(), oBackdated.GetId()}
	for i, txn := range ordered {
		if txn.GetId() != wantOrder[i] {
			t.Fatalf("page %d: pagination should follow transaction_date DESC (not posting order); got id %d, want %d",
				i, txn.GetId(), wantOrder[i])
		}
	}

	// --- bank reconciliation ----------------------------------------------
	stmt := must(statements.CreateBankStatement(ctx, &denarixv1.CreateBankStatementRequest{BusinessId: tc.businessID, LedgerAccountId: cash, StatementName: "Jan 2026", StatementDate: dateOf(2026, 1, 31), OpeningBalance: dec("0"), ClosingBalance: dec("1000.00")})).GetBankStatement()
	eqAmount(t, "reconciled before", stmt.GetReconciledBalance(), "0")
	eqAmount(t, "difference before", stmt.GetDifference(), "1000.00")
	candidates := must(statements.ListUnreconciledLedgerTransactions(ctx, &denarixv1.ListUnreconciledLedgerTransactionsRequest{LedgerAccountId: cash, ThroughDate: dateOf(2026, 1, 31)})).GetTransactions()
	if len(candidates) < 3 { // opening, mistake, reversal, payment, void
		t.Fatalf("unreconciled = %d", len(candidates))
	}
	stmt = must(statements.ReconcileLedgerTransactions(ctx, &denarixv1.ReconcileLedgerTransactionsRequest{BankStatementId: stmt.GetId(), LedgerTransactionIds: []int64{opening.GetId()}})).GetBankStatement()
	eqAmount(t, "reconciled after", stmt.GetReconciledBalance(), "1000.00")
	eqAmount(t, "difference after", stmt.GetDifference(), "0")
	_, err = statements.ReconcileLedgerTransactions(ctx, &denarixv1.ReconcileLedgerTransactionsRequest{BankStatementId: stmt.GetId(), LedgerTransactionIds: []int64{est.GetId() + 1<<40}})
	wantCode(t, err, codes.InvalidArgument) // not this business's transaction
	_, err = statements.DeactivateBankStatement(ctx, &denarixv1.DeactivateBankStatementRequest{Id: stmt.GetId()})
	wantCode(t, err, codes.FailedPrecondition) // still has a line
	// A second statement must chain from the first's closing balance.
	_, err = statements.CreateBankStatement(ctx, &denarixv1.CreateBankStatementRequest{BusinessId: tc.businessID, LedgerAccountId: cash, StatementName: "Feb", StatementDate: dateOf(2026, 2, 28), OpeningBalance: dec("999"), ClosingBalance: dec("999")})
	wantCode(t, err, codes.InvalidArgument)
	if listed := must(statements.ListBankStatements(ctx, &denarixv1.ListBankStatementsRequest{BusinessId: tc.businessID})).GetBankStatements(); len(listed) != 1 || len(listed[0].GetLines()) != 1 {
		t.Fatalf("list statements should carry lines and balances: %+v", listed)
	}
	stmt = must(statements.UnreconcileLedgerTransactions(ctx, &denarixv1.UnreconcileLedgerTransactionsRequest{BankStatementId: stmt.GetId(), LedgerTransactionIds: []int64{opening.GetId()}})).GetBankStatement()
	must(statements.DeactivateBankStatement(ctx, &denarixv1.DeactivateBankStatementRequest{Id: stmt.GetId()}))

	// --- reports -----------------------------------------------------------
	tb := must(reports.GetTrialBalance(ctx, &denarixv1.GetTrialBalanceRequest{BusinessId: tc.businessID, AsOf: dateOf(2026, 12, 31)})).GetTrialBalance()
	if tb.GetTotalDebit().GetValue() != tb.GetTotalCredit().GetValue() {
		t.Fatalf("trial balance out of balance: %s vs %s", tb.GetTotalDebit().GetValue(), tb.GetTotalCredit().GetValue())
	}
	is := must(reports.GetIncomeStatement(ctx, &denarixv1.GetIncomeStatementRequest{BusinessId: tc.businessID, PeriodStart: dateOf(2026, 1, 1), PeriodEnd: dateOf(2026, 12, 31)})).GetIncomeStatement()
	eqAmount(t, "total expenses", is.GetTotalExpenses(), "200.00") // the purchase; the 5.00 mistake was reversed
	gl := must(reports.GetGeneralLedger(ctx, &denarixv1.GetGeneralLedgerRequest{BusinessId: tc.businessID, AccountId: cash, PeriodStart: dateOf(2026, 1, 1), PeriodEnd: dateOf(2026, 12, 31)})).GetGeneralLedger()
	eqAmount(t, "cash ending balance", gl.GetEndingBalance(), "1000.00")
	must(reports.GetBalanceSheet(ctx, &denarixv1.GetBalanceSheetRequest{BusinessId: tc.businessID, AsOf: dateOf(2026, 12, 31)}))
	stmtReport := must(reports.GetCustomerStatement(ctx, &denarixv1.GetCustomerStatementRequest{ContactId: customer.GetId(), PeriodStart: dateOf(2026, 1, 1), PeriodEnd: dateOf(2026, 12, 31)})).GetStatement()
	if stmtReport.GetContactName() != "Acme" {
		t.Fatalf("customer statement for %q", stmtReport.GetContactName())
	}
	_, err = reports.GetCustomerStatement(tc.outsider, &denarixv1.GetCustomerStatementRequest{ContactId: customer.GetId(), PeriodStart: dateOf(2026, 1, 1), PeriodEnd: dateOf(2026, 12, 31)})
	wantCode(t, err, codes.NotFound)
	_, err = reports.GetTrialBalance(ctx, &denarixv1.GetTrialBalanceRequest{BusinessId: tc.businessID})
	wantCode(t, err, codes.InvalidArgument) // as_of required
	for name, content := range map[string][]byte{
		"trial balance":      must(reports.GetTrialBalancePdf(ctx, &denarixv1.GetTrialBalancePdfRequest{BusinessId: tc.businessID, AsOf: dateOf(2026, 12, 31)})).GetContent(),
		"balance sheet":      must(reports.GetBalanceSheetPdf(ctx, &denarixv1.GetBalanceSheetPdfRequest{BusinessId: tc.businessID, AsOf: dateOf(2026, 12, 31)})).GetContent(),
		"income statement":   must(reports.GetIncomeStatementPdf(ctx, &denarixv1.GetIncomeStatementPdfRequest{BusinessId: tc.businessID, PeriodStart: dateOf(2026, 1, 1), PeriodEnd: dateOf(2026, 12, 31)})).GetContent(),
		"general ledger":     must(reports.GetGeneralLedgerPdf(ctx, &denarixv1.GetGeneralLedgerPdfRequest{BusinessId: tc.businessID, AccountId: cash, PeriodStart: dateOf(2026, 1, 1), PeriodEnd: dateOf(2026, 12, 31)})).GetContent(),
		"customer statement": must(reports.GetCustomerStatementPdf(ctx, &denarixv1.GetCustomerStatementPdfRequest{ContactId: customer.GetId(), PeriodStart: dateOf(2026, 1, 1), PeriodEnd: dateOf(2026, 12, 31)})).GetContent(),
	} {
		if !bytes.HasPrefix(content, []byte("%PDF")) {
			t.Fatalf("%s pdf did not render", name)
		}
	}

	// --- period close --------------------------------------------------------
	pc := must(closes.TriggerClose(ctx, &denarixv1.TriggerCloseRequest{BusinessId: tc.businessID, PeriodEnd: dateOf(2026, 1, 31)})).GetPeriodClose()
	if len(pc.GetGeneratedLedgerTransactionIds()) == 0 {
		t.Fatal("close should have generated closing entries")
	}
	_, err = txns.CreateLedgerTransaction(ctx, &denarixv1.CreateLedgerTransactionRequest{BusinessId: tc.businessID, TransactionDate: dateOf(2026, 1, 15),
		Entries: []*denarixv1.NewLedgerEntry{{AccountId: cash, DebitAmount: dec("1")}, {AccountId: equity, CreditAmount: dec("1")}}})
	wantCode(t, err, codes.FailedPrecondition) // period locked
	if listed := must(closes.ListPeriodCloses(ctx, &denarixv1.ListPeriodClosesRequest{BusinessId: tc.businessID})).GetPeriodCloses(); len(listed) != 1 || len(listed[0].GetGeneratedLedgerTransactionIds()) != len(pc.GetGeneratedLedgerTransactionIds()) {
		t.Fatalf("list closes should carry generated transactions: %+v", listed)
	}
	reversedClose := must(closes.ReverseClose(ctx, &denarixv1.ReverseCloseRequest{Id: pc.GetId()})).GetPeriodClose()
	if reversedClose.GetReversedAt() == nil {
		t.Fatal("close not marked reversed")
	}
	must(txns.CreateLedgerTransaction(ctx, &denarixv1.CreateLedgerTransactionRequest{BusinessId: tc.businessID, TransactionDate: dateOf(2026, 1, 15),
		Entries: []*denarixv1.NewLedgerEntry{{AccountId: cash, DebitAmount: dec("1")}, {AccountId: equity, CreditAmount: dec("1")}}}))

	// --- stacked closes: reverse newest-first, no cascade -----------------
	c1 := must(closes.TriggerClose(ctx, &denarixv1.TriggerCloseRequest{BusinessId: tc.businessID, PeriodEnd: dateOf(2026, 1, 31)})).GetPeriodClose()
	c2 := must(closes.TriggerClose(ctx, &denarixv1.TriggerCloseRequest{BusinessId: tc.businessID, PeriodEnd: dateOf(2026, 2, 28)})).GetPeriodClose()
	_, err = closes.ReverseClose(ctx, &denarixv1.ReverseCloseRequest{Id: c1.GetId()})
	wantCode(t, err, codes.FailedPrecondition) // a later close still stands
	if got := must(closes.GetPeriodClose(ctx, &denarixv1.GetPeriodCloseRequest{Id: c1.GetId()})).GetPeriodClose(); got.GetReversedAt() != nil {
		t.Fatal("refused reverse must leave the older close unreversed")
	}
	must(closes.ReverseClose(ctx, &denarixv1.ReverseCloseRequest{Id: c2.GetId()}))
	must(closes.ReverseClose(ctx, &denarixv1.ReverseCloseRequest{Id: c1.GetId()}))
	_, err = closes.ReverseClose(ctx, &denarixv1.ReverseCloseRequest{Id: c1.GetId()})
	wantCode(t, err, codes.FailedPrecondition) // already reversed
	if tb := must(reports.GetTrialBalance(ctx, &denarixv1.GetTrialBalanceRequest{BusinessId: tc.businessID, AsOf: dateOf(2026, 12, 31)})).GetTrialBalance(); tb.GetTotalDebit().GetValue() != tb.GetTotalCredit().GetValue() {
		t.Fatalf("trial balance should balance after unwinding stacked closes: %s != %s", tb.GetTotalDebit().GetValue(), tb.GetTotalCredit().GetValue())
	}

	// --- entity context ---------------------------------------------------
	note := must(notes.CreateEntityContext(ctx, &denarixv1.CreateEntityContextRequest{BusinessId: tc.businessID, EntityType: "invoice", EntityId: inv.GetId(), ContextType: "user_note", Content: "customer asked for a discount"})).GetEntityContext()
	_, err = notes.CreateEntityContext(ctx, &denarixv1.CreateEntityContextRequest{BusinessId: tc.businessID, EntityType: "invoice", EntityId: 1 << 40, ContextType: "user_note", Content: "x"})
	wantCode(t, err, codes.InvalidArgument)
	_, err = notes.CreateEntityContext(ctx, &denarixv1.CreateEntityContextRequest{BusinessId: tc.businessID, EntityType: "spaceship", EntityId: 1, ContextType: "user_note", Content: "x"})
	wantCode(t, err, codes.InvalidArgument)
	newer := must(notes.CreateEntityContext(ctx, &denarixv1.CreateEntityContextRequest{BusinessId: tc.businessID, EntityType: "invoice", EntityId: inv.GetId(), ContextType: "user_note", Content: "discount declined", SupersedesIds: []int64{note.GetId()}})).GetEntityContext()
	if current := must(notes.ListEntityContext(ctx, &denarixv1.ListEntityContextRequest{BusinessId: tc.businessID, EntityType: "invoice", EntityId: inv.GetId()})).GetEntityContexts(); len(current) != 1 || current[0].GetId() != newer.GetId() {
		t.Fatalf("superseded note should be hidden: %+v", current)
	}
	must(notes.DeleteEntityContext(ctx, &denarixv1.DeleteEntityContextRequest{Id: newer.GetId()}))
	if current := must(notes.ListEntityContext(ctx, &denarixv1.ListEntityContextRequest{BusinessId: tc.businessID, EntityType: "invoice", EntityId: inv.GetId()})).GetEntityContexts(); len(current) != 1 || current[0].GetId() != note.GetId() {
		t.Fatalf("deleting the newer note should un-hide the older: %+v", current)
	}

	// The contact list carries both roles from the batch load.
	all := must(contacts.ListContacts(ctx, &denarixv1.ListContactsRequest{BusinessId: tc.businessID})).GetContacts()
	roles := 0
	for _, c := range all {
		if c.GetCustomer() != nil {
			roles++
		}
		if c.GetVendor() != nil {
			roles++
		}
	}
	if len(all) != 2 || roles != 2 {
		t.Fatalf("contacts = %+v", all)
	}
	_ = q
}
