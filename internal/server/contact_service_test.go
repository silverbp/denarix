// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"testing"

	"google.golang.org/grpc/codes"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
)

// TestContact_CustomerAndVendorRoles checks a contact can carry either role,
// both at once, or neither, and that update/deactivate work regardless of
// which roles are attached.
func TestContact_CustomerAndVendorRoles(t *testing.T) {
	defer failOnPanic(t)
	tc := newTenant(t)
	ctx := tc.ctx
	contacts := newContactService(tc.store)

	both := must(contacts.CreateContact(ctx, &denarixv1.CreateContactRequest{
		BusinessId: tc.businessID, ContactNumber: "C-BOTH", Name: "Acme Co",
		IsCustomer: true, IsVendor: true,
	})).GetContact()
	ar := both.GetCustomer().GetLedgerAccountId()
	ap := both.GetVendor().GetLedgerAccountId()
	if ar == 0 {
		t.Fatalf("customer created with no auto-provisioned ledger account")
	}
	if ap == 0 {
		t.Fatalf("vendor created with no auto-provisioned ledger account")
	}

	neither := must(contacts.CreateContact(ctx, &denarixv1.CreateContactRequest{
		BusinessId: tc.businessID, ContactNumber: "C-NEITHER", Name: "Just A Contact",
	})).GetContact()
	if neither.GetCustomer() != nil || neither.GetVendor() != nil {
		t.Fatalf("plain contact should carry neither role, got customer=%v vendor=%v", neither.GetCustomer(), neither.GetVendor())
	}

	// Update touches plain fields and leaves both roles (and their ledger
	// accounts) untouched - update never edits customer/vendor state.
	updated := must(contacts.UpdateContact(ctx, &denarixv1.UpdateContactRequest{
		Id: both.GetId(), ResourceVersion: both.GetResourceVersion(),
		Name: ptr("Acme Company"), Phone: ptr("555-0100"),
	})).GetContact()
	if updated.GetName() != "Acme Company" || updated.GetPhone() != "555-0100" {
		t.Fatalf("update did not persist: got name=%q phone=%q", updated.GetName(), updated.GetPhone())
	}
	if updated.GetResourceVersion() != both.GetResourceVersion()+1 {
		t.Fatalf("resource_version = %d, want %d", updated.GetResourceVersion(), both.GetResourceVersion()+1)
	}
	if updated.GetCustomer().GetLedgerAccountId() != ar || updated.GetVendor().GetLedgerAccountId() != ap {
		t.Fatalf("update disturbed customer/vendor roles: %+v / %+v", updated.GetCustomer(), updated.GetVendor())
	}

	// Deactivate soft-deletes: the row stays readable (both roles still
	// attached) but flips inactive, and a stale-version deactivate on the
	// now-current row is rejected rather than silently reapplied.
	deactivated := must(contacts.DeactivateContact(ctx, &denarixv1.DeactivateContactRequest{
		Id: both.GetId(), ResourceVersion: updated.GetResourceVersion(),
	})).GetContact()
	if deactivated.GetIsActive() {
		t.Fatalf("contact still active after deactivate")
	}
	got := must(contacts.GetContact(ctx, &denarixv1.GetContactRequest{Id: both.GetId()})).GetContact()
	if got.GetIsActive() {
		t.Fatalf("deactivated contact reads back active")
	}
	if got.GetName() != "Acme Company" || got.GetCustomer().GetLedgerAccountId() != ar || got.GetVendor().GetLedgerAccountId() != ap {
		t.Fatalf("deactivate should not touch other fields, got %+v", got)
	}
	_, err := contacts.DeactivateContact(ctx, &denarixv1.DeactivateContactRequest{
		Id: both.GetId(), ResourceVersion: updated.GetResourceVersion(),
	})
	wantCode(t, err, codes.Aborted)
}

// TestContact_AutoProvisionsLedgerAccount checks that a customer/vendor
// created without an explicit ledger_account_id gets its own postable
// sub-account under the business's AR/AP container automatically (see
// getOrCreateContactAccount in system_accounts.go), rather than being left
// unset - and that a second contact shares the same container.
func TestContact_AutoProvisionsLedgerAccount(t *testing.T) {
	defer failOnPanic(t)
	tc := newTenant(t)
	ctx := tc.ctx
	contacts := newContactService(tc.store)
	accounts := newLedgerAccountService(tc.store)

	customer := must(contacts.CreateContact(ctx, &denarixv1.CreateContactRequest{
		BusinessId: tc.businessID, ContactNumber: "C-1", Name: "Acme Co", IsCustomer: true,
	})).GetContact()
	custAR := customer.GetCustomer().GetLedgerAccountId()
	if custAR == 0 {
		t.Fatalf("customer created with no ledger account")
	}
	arAcct := must(accounts.GetLedgerAccount(ctx, &denarixv1.GetLedgerAccountRequest{Id: custAR})).GetAccount()
	if arAcct.GetName() != "Acme Co" || arAcct.GetAccountTypeId() != 1 || arAcct.GetIsContainer() {
		t.Fatalf("unexpected auto-provisioned customer account: %+v", arAcct)
	}
	arContainer := must(accounts.GetLedgerAccount(ctx, &denarixv1.GetLedgerAccountRequest{Id: arAcct.GetParentAccountId()})).GetAccount()
	if arContainer.GetCode() != "1100" || !arContainer.GetIsContainer() || !arContainer.GetIsSystem() {
		t.Fatalf("customer account not parented under a system AR container: %+v", arContainer)
	}

	vendor := must(contacts.CreateContact(ctx, &denarixv1.CreateContactRequest{
		BusinessId: tc.businessID, ContactNumber: "V-1", Name: "Supplies Co", IsVendor: true,
	})).GetContact()
	vendAP := vendor.GetVendor().GetLedgerAccountId()
	apAcct := must(accounts.GetLedgerAccount(ctx, &denarixv1.GetLedgerAccountRequest{Id: vendAP})).GetAccount()
	if apAcct.GetName() != "Supplies Co" || apAcct.GetAccountTypeId() != 2 {
		t.Fatalf("unexpected auto-provisioned vendor account: %+v", apAcct)
	}
	apContainer := must(accounts.GetLedgerAccount(ctx, &denarixv1.GetLedgerAccountRequest{Id: apAcct.GetParentAccountId()})).GetAccount()
	if apContainer.GetCode() != "2000" || !apContainer.GetIsContainer() {
		t.Fatalf("vendor account not parented under a system AP container: %+v", apContainer)
	}

	other := must(contacts.CreateContact(ctx, &denarixv1.CreateContactRequest{
		BusinessId: tc.businessID, ContactNumber: "C-2", Name: "Beta Inc", IsCustomer: true,
	})).GetContact()
	otherAcct := must(accounts.GetLedgerAccount(ctx, &denarixv1.GetLedgerAccountRequest{Id: other.GetCustomer().GetLedgerAccountId()})).GetAccount()
	if otherAcct.GetParentAccountId() != arAcct.GetParentAccountId() {
		t.Fatalf("second customer did not share the first's AR container")
	}
}

// TestDeactivateContact_VendorLedgerAccount mirrors
// TestDeactivateContact_CustomerLedgerAccount for the AP side.
func TestDeactivateContact_VendorLedgerAccount(t *testing.T) {
	defer failOnPanic(t)
	tc := newTenant(t)
	ctx := tc.ctx

	contacts := newContactService(tc.store)
	invoices := newInvoiceService(tc.store)
	items := newItemService(tc.store)
	accounts := newLedgerAccountService(tc.store)

	expense := tc.account(5, "6000", "Job Supplies")
	vendor := must(contacts.CreateContact(ctx, &denarixv1.CreateContactRequest{
		BusinessId: tc.businessID, ContactNumber: "V-1", Name: "Supplies Co", IsVendor: true,
	})).GetContact()
	vendAP := vendor.GetVendor().GetLedgerAccountId()
	item := must(items.CreateItem(ctx, &denarixv1.CreateItemRequest{
		BusinessId: tc.businessID, ItemCode: "SUP", Name: "Supplies", RetailPrice: dec("50.00"), DefaultLedgerAccountId: &expense,
	})).GetItem()

	inv := must(invoices.CreateInvoice(ctx, &denarixv1.CreateInvoiceRequest{
		BusinessId: tc.businessID, ContactId: vendor.GetId(), InvoiceType: "PURCHASE", InvoiceNumber: ptr("SUP-1"),
		InvoiceDate: dateOf(2026, 1, 1), DueDate: dateOf(2026, 1, 31),
		LineItems: []*denarixv1.NewDocumentLineItem{{ItemId: item.GetId(), LineNumber: 1}},
	})).GetInvoice()

	must(contacts.DeactivateContact(ctx, &denarixv1.DeactivateContactRequest{Id: vendor.GetId()}))
	acct := must(accounts.GetLedgerAccount(ctx, &denarixv1.GetLedgerAccountRequest{Id: vendAP})).GetAccount()
	if !acct.GetIsActive() {
		t.Fatalf("vendor AP account went inactive with a non-zero balance")
	}

	must(invoices.UpdateInvoiceStatus(ctx, &denarixv1.UpdateInvoiceStatusRequest{Id: inv.GetId(), Status: "CANCELLED"}))
	must(contacts.DeactivateContact(ctx, &denarixv1.DeactivateContactRequest{Id: vendor.GetId()}))
	acct = must(accounts.GetLedgerAccount(ctx, &denarixv1.GetLedgerAccountRequest{Id: vendAP})).GetAccount()
	if acct.GetIsActive() {
		t.Fatalf("vendor AP account should have gone inactive at zero balance")
	}
}

// TestDeactivateContact_CustomerLedgerAccount checks the conditional side
// effect on contact deactivation: a customer's own AR sub-account follows
// it into inactive only once that account carries no balance.
func TestDeactivateContact_CustomerLedgerAccount(t *testing.T) {
	defer failOnPanic(t)
	tc := newTenant(t)
	ctx := tc.ctx

	contacts := newContactService(tc.store)
	invoices := newInvoiceService(tc.store)
	accounts := newLedgerAccountService(tc.store)

	contactID, itemID := tc.customerAndItem()
	custAR := must(contacts.GetContact(ctx, &denarixv1.GetContactRequest{Id: contactID})).GetContact().GetCustomer().GetLedgerAccountId()

	inv := must(invoices.CreateInvoice(ctx, &denarixv1.CreateInvoiceRequest{
		BusinessId: tc.businessID, ContactId: contactID, InvoiceType: "SALES", InvoiceDate: dateOf(2026, 1, 1), DueDate: dateOf(2026, 1, 31),
		LineItems: []*denarixv1.NewDocumentLineItem{{ItemId: itemID, LineNumber: 1}},
	})).GetInvoice()

	// A customer with an outstanding invoice balance keeps its AR account
	// active even once the contact is deactivated.
	must(contacts.DeactivateContact(ctx, &denarixv1.DeactivateContactRequest{Id: contactID}))
	acct := must(accounts.GetLedgerAccount(ctx, &denarixv1.GetLedgerAccountRequest{Id: custAR})).GetAccount()
	if !acct.GetIsActive() {
		t.Fatalf("customer AR account went inactive with a non-zero balance")
	}

	// Once the invoice is cancelled (balance back to zero), the same
	// deactivate call also deactivates the now-zero-balance account.
	must(invoices.UpdateInvoiceStatus(ctx, &denarixv1.UpdateInvoiceStatusRequest{Id: inv.GetId(), Status: "CANCELLED"}))
	must(contacts.DeactivateContact(ctx, &denarixv1.DeactivateContactRequest{Id: contactID}))
	acct = must(accounts.GetLedgerAccount(ctx, &denarixv1.GetLedgerAccountRequest{Id: custAR})).GetAccount()
	if acct.GetIsActive() {
		t.Fatalf("customer AR account should have gone inactive at zero balance")
	}
}
