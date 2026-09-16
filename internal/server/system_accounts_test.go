// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"testing"

	"google.golang.org/grpc/codes"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
)

// TestSystemAccounts_PersistOnBusiness checks that all four system account
// ids (AR, AP, Income Summary, Retained Earnings) get resolved once and
// stored on the business row itself (business.ar_account_id and friends),
// not just re-derived by a ledger_account.code lookup on every call - see
// resolveSystemAccount in system_accounts.go/periodclose/provision.go.
// newTenant bypasses the CreateBusiness RPC (so none of these start out
// set), which doubles as coverage for the self-heal path any pre-existing
// business (prod's business 1 included) takes on its first touch.
func TestSystemAccounts_PersistOnBusiness(t *testing.T) {
	defer failOnPanic(t)
	tc := newTenant(t)
	ctx := tc.ctx
	contacts := newContactService(tc.store)
	closes := newPeriodCloseService(tc.store)

	biz, err := tc.q.GetBusiness(ctx, tc.businessID)
	if err != nil {
		t.Fatalf("GetBusiness: %v", err)
	}
	if biz.ArAccountID != nil || biz.ApAccountID != nil || biz.IncomeSummaryAccountID != nil || biz.RetainedEarningsAccountID != nil {
		t.Fatalf("expected a freshly-provisioned tenant to start with no system accounts stored, got %+v", biz)
	}

	customer := must(contacts.CreateContact(ctx, &denarixv1.CreateContactRequest{
		BusinessId: tc.businessID, ContactNumber: "C-1", Name: "Acme Co", IsCustomer: true,
	})).GetContact()
	vendor := must(contacts.CreateContact(ctx, &denarixv1.CreateContactRequest{
		BusinessId: tc.businessID, ContactNumber: "V-1", Name: "Supplies Co", IsVendor: true,
	})).GetContact()

	biz, err = tc.q.GetBusiness(ctx, tc.businessID)
	if err != nil {
		t.Fatalf("GetBusiness: %v", err)
	}
	if biz.ArAccountID == nil {
		t.Fatalf("business.ar_account_id still unset after the first customer")
	}
	custAcct := must(newLedgerAccountService(tc.store).GetLedgerAccount(ctx, &denarixv1.GetLedgerAccountRequest{Id: customer.GetCustomer().GetLedgerAccountId()})).GetAccount()
	if custAcct.GetParentAccountId() != *biz.ArAccountID {
		t.Fatalf("customer's AR sub-account parent %d != business.ar_account_id %d", custAcct.GetParentAccountId(), *biz.ArAccountID)
	}
	if biz.ApAccountID == nil {
		t.Fatalf("business.ap_account_id still unset after the first vendor")
	}
	vendAcct := must(newLedgerAccountService(tc.store).GetLedgerAccount(ctx, &denarixv1.GetLedgerAccountRequest{Id: vendor.GetVendor().GetLedgerAccountId()})).GetAccount()
	if vendAcct.GetParentAccountId() != *biz.ApAccountID {
		t.Fatalf("vendor's AP sub-account parent %d != business.ap_account_id %d", vendAcct.GetParentAccountId(), *biz.ApAccountID)
	}

	// A second customer reuses the same stored container - the fast path,
	// no new container created.
	other := must(contacts.CreateContact(ctx, &denarixv1.CreateContactRequest{
		BusinessId: tc.businessID, ContactNumber: "C-2", Name: "Beta Inc", IsCustomer: true,
	})).GetContact()
	otherAcct := must(newLedgerAccountService(tc.store).GetLedgerAccount(ctx, &denarixv1.GetLedgerAccountRequest{Id: other.GetCustomer().GetLedgerAccountId()})).GetAccount()
	if otherAcct.GetParentAccountId() != *biz.ArAccountID {
		t.Fatalf("second customer's AR sub-account parent %d != stored business.ar_account_id %d", otherAcct.GetParentAccountId(), *biz.ArAccountID)
	}

	// Triggering a close resolves (and persists) Income Summary/Retained
	// Earnings the same way.
	must(closes.TriggerClose(ctx, &denarixv1.TriggerCloseRequest{BusinessId: tc.businessID, PeriodEnd: dateOf(2026, 1, 31)}))
	biz, err = tc.q.GetBusiness(ctx, tc.businessID)
	if err != nil {
		t.Fatalf("GetBusiness: %v", err)
	}
	if biz.IncomeSummaryAccountID == nil || biz.RetainedEarningsAccountID == nil {
		t.Fatalf("business.income_summary_account_id/retained_earnings_account_id still unset after a close: %+v", biz)
	}
}

// TestContact_DuplicateNamesAllowed checks that two customers (or vendors)
// with the same display name can both be created - their auto-provisioned
// sub-accounts share a name and a parent (the AR/AP container), which used
// to collide on ledger_account_business_parent_name_uindex until that
// index was made partial (WHERE NOT is_system).
func TestContact_DuplicateNamesAllowed(t *testing.T) {
	defer failOnPanic(t)
	tc := newTenant(t)
	ctx := tc.ctx
	contacts := newContactService(tc.store)

	first := must(contacts.CreateContact(ctx, &denarixv1.CreateContactRequest{
		BusinessId: tc.businessID, ContactNumber: "C-1", Name: "John Smith", IsCustomer: true,
	})).GetContact()
	second := must(contacts.CreateContact(ctx, &denarixv1.CreateContactRequest{
		BusinessId: tc.businessID, ContactNumber: "C-2", Name: "John Smith", IsCustomer: true,
	})).GetContact()

	if first.GetCustomer().GetLedgerAccountId() == second.GetCustomer().GetLedgerAccountId() {
		t.Fatalf("both contacts resolved to the same ledger account: %d", first.GetCustomer().GetLedgerAccountId())
	}

	// Deactivating and re-creating a contact under the same name (the
	// account itself is never deleted, just deactivated) must also work.
	must(contacts.DeactivateContact(ctx, &denarixv1.DeactivateContactRequest{Id: first.GetId()}))
	must(contacts.CreateContact(ctx, &denarixv1.CreateContactRequest{
		BusinessId: tc.businessID, ContactNumber: "C-3", Name: "John Smith", IsCustomer: true,
	}))
}

// TestContact_VendorCreateDoesNotTouchAR checks that creating a vendor-only
// contact never resolves (or provisions) the AR container - getOrCreateContactAccount
// used to call resolveARContainer unconditionally before overwriting the
// result for the vendor side.
func TestContact_VendorCreateDoesNotTouchAR(t *testing.T) {
	defer failOnPanic(t)
	tc := newTenant(t)
	ctx := tc.ctx
	contacts := newContactService(tc.store)

	must(contacts.CreateContact(ctx, &denarixv1.CreateContactRequest{
		BusinessId: tc.businessID, ContactNumber: "V-1", Name: "Supplies Co", IsVendor: true,
	}))

	biz, err := tc.q.GetBusiness(ctx, tc.businessID)
	if err != nil {
		t.Fatalf("GetBusiness: %v", err)
	}
	if biz.ArAccountID != nil {
		t.Fatalf("vendor-only contact creation resolved business.ar_account_id: %d", *biz.ArAccountID)
	}
	if biz.ApAccountID == nil {
		t.Fatalf("business.ap_account_id still unset after a vendor create")
	}
}

// TestContact_SelfHealRejectsMismatchedContainer checks that self-healing
// (a legacy business with no business.ar_account_id stored yet) refuses to
// silently adopt a pre-existing ledger_account at the AR code (1100) that
// isn't actually a usable container - a postable non-container account, an
// inactive one, or one of the wrong account type - since that id would
// otherwise get pinned to the business row forever with no way to reset
// it.
func TestContact_SelfHealRejectsMismatchedContainer(t *testing.T) {
	defer failOnPanic(t)
	tc := newTenant(t)
	ctx := tc.ctx
	contacts := newContactService(tc.store)

	// A postable (non-container) account at the reserved AR code - exactly
	// what CLAUDE.md's chart-of-accounts convention would have produced
	// before AR/AP became auto-provisioned system accounts.
	tc.account(1, accountsReceivableCode, "Accounts Receivable")

	_, err := contacts.CreateContact(ctx, &denarixv1.CreateContactRequest{
		BusinessId: tc.businessID, ContactNumber: "C-1", Name: "Acme Co", IsCustomer: true,
	})
	wantCode(t, err, codes.FailedPrecondition)
}
