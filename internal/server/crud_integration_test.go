// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"testing"

	typepb "google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/grpc/codes"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
)

// crudCase drives one resource through the verbs every resource shares:
// create, get (as the owner, as an outsider, by an unknown id), update
// (stale resource_version, current version, as an outsider) and
// deactivate. The per-resource closures are the only thing that differs,
// so adding a resource here is a dozen lines - and every handler the
// generic resource table / loadForBusiness helpers back is exercised.
type crudCase struct {
	name string
	// create returns the new row's id and resource_version.
	create func(tc *testTenant) (id, version int64)
	get    func(ctx context.Context, id int64) (version int64, err error)
	update func(ctx context.Context, id, version int64) (newVersion int64, err error)
	// deactivate is the resource's terminal verb (deactivate/send/cancel).
	deactivate func(ctx context.Context, id, version int64) error
	// deactivateAgain is what a second deactivate with the now-stale
	// version reports: Aborted when the row is still readable, NotFound when
	// deactivate soft-deleted it, FailedPrecondition when a status guard
	// fires first.
	deactivateAgain codes.Code
}

func dateOf(y, m, d int) *typepb.Date {
	return &typepb.Date{Year: int32(y), Month: int32(m), Day: int32(d)}
}
func dec(v string) *denarixv1.Decimal { return &denarixv1.Decimal{Value: v} }

func crudCases() []crudCase {
	businesses := newBusinessService(testStore)
	accounts := newLedgerAccountService(testStore)
	contacts := newContactService(testStore)
	items := newItemService(testStore)
	taxRates := newTaxRateService(testStore)
	statements := newBankStatementService(testStore)
	estimates := newEstimateService(testStore)
	invoices := newInvoiceService(testStore)

	return []crudCase{
		{
			name: "business",
			create: func(tc *testTenant) (int64, int64) {
				b := must(businesses.GetBusiness(tc.ctx, &denarixv1.GetBusinessRequest{Id: tc.businessID}))
				return b.GetBusiness().GetId(), b.GetBusiness().GetResourceVersion()
			},
			get: func(ctx context.Context, id int64) (int64, error) {
				r, err := businesses.GetBusiness(ctx, &denarixv1.GetBusinessRequest{Id: id})
				return r.GetBusiness().GetResourceVersion(), err
			},
			update: func(ctx context.Context, id, v int64) (int64, error) {
				r, err := businesses.UpdateBusiness(ctx, &denarixv1.UpdateBusinessRequest{Id: id, ResourceVersion: v, Name: ptr("Renamed")})
				return r.GetBusiness().GetResourceVersion(), err
			},
			deactivate: func(ctx context.Context, id, v int64) error {
				_, err := businesses.DeactivateBusiness(ctx, &denarixv1.DeactivateBusinessRequest{Id: id, ResourceVersion: v})
				return err
			},
			deactivateAgain: codes.Aborted,
		},
		{
			name: "ledger-account",
			create: func(tc *testTenant) (int64, int64) {
				a := must(accounts.CreateLedgerAccount(tc.ctx, &denarixv1.CreateLedgerAccountRequest{BusinessId: tc.businessID, AccountTypeId: 1, Code: "1900", Name: "Misc"}))
				return int64(a.GetAccount().GetId()), a.GetAccount().GetResourceVersion()
			},
			get: func(ctx context.Context, id int64) (int64, error) {
				r, err := accounts.GetLedgerAccount(ctx, &denarixv1.GetLedgerAccountRequest{Id: int32(id)})
				return r.GetAccount().GetResourceVersion(), err
			},
			update: func(ctx context.Context, id, v int64) (int64, error) {
				r, err := accounts.UpdateLedgerAccount(ctx, &denarixv1.UpdateLedgerAccountRequest{Id: int32(id), ResourceVersion: v, Name: ptr("Renamed")})
				return r.GetAccount().GetResourceVersion(), err
			},
			deactivate: func(ctx context.Context, id, v int64) error {
				_, err := accounts.DeactivateLedgerAccount(ctx, &denarixv1.DeactivateLedgerAccountRequest{Id: int32(id), ResourceVersion: v})
				return err
			},
			deactivateAgain: codes.Aborted,
		},
		{
			name: "contact",
			create: func(tc *testTenant) (int64, int64) {
				c := must(contacts.CreateContact(tc.ctx, &denarixv1.CreateContactRequest{BusinessId: tc.businessID, ContactNumber: "C-1", Name: "Acme", IsCustomer: true}))
				return c.GetContact().GetId(), c.GetContact().GetResourceVersion()
			},
			get: func(ctx context.Context, id int64) (int64, error) {
				r, err := contacts.GetContact(ctx, &denarixv1.GetContactRequest{Id: id})
				return r.GetContact().GetResourceVersion(), err
			},
			update: func(ctx context.Context, id, v int64) (int64, error) {
				r, err := contacts.UpdateContact(ctx, &denarixv1.UpdateContactRequest{Id: id, ResourceVersion: v, Phone: ptr("555-0100")})
				return r.GetContact().GetResourceVersion(), err
			},
			deactivate: func(ctx context.Context, id, v int64) error {
				_, err := contacts.DeactivateContact(ctx, &denarixv1.DeactivateContactRequest{Id: id, ResourceVersion: v})
				return err
			},
			deactivateAgain: codes.Aborted,
		},
		{
			name: "item",
			create: func(tc *testTenant) (int64, int64) {
				rev := tc.account(4, "4000", "Revenue")
				it := must(items.CreateItem(tc.ctx, &denarixv1.CreateItemRequest{BusinessId: tc.businessID, ItemCode: "SVC", Name: "Service", RetailPrice: dec("10.00"), DefaultLedgerAccountId: &rev}))
				return it.GetItem().GetId(), it.GetItem().GetResourceVersion()
			},
			get: func(ctx context.Context, id int64) (int64, error) {
				r, err := items.GetItem(ctx, &denarixv1.GetItemRequest{Id: id})
				return r.GetItem().GetResourceVersion(), err
			},
			update: func(ctx context.Context, id, v int64) (int64, error) {
				r, err := items.UpdateItem(ctx, &denarixv1.UpdateItemRequest{Id: id, ResourceVersion: v, RetailPrice: dec("12.50")})
				return r.GetItem().GetResourceVersion(), err
			},
			deactivate: func(ctx context.Context, id, v int64) error {
				_, err := items.DeactivateItem(ctx, &denarixv1.DeactivateItemRequest{Id: id, ResourceVersion: v})
				return err
			},
			deactivateAgain: codes.Aborted,
		},
		{
			name: "tax-rate",
			create: func(tc *testTenant) (int64, int64) {
				liab := tc.account(6, "2100", "Sales Tax Payable")
				tr := must(taxRates.CreateTaxRate(tc.ctx, &denarixv1.CreateTaxRateRequest{BusinessId: tc.businessID, Name: "Sales Tax", Rate: dec("0.0825"), TaxLiabilityAccountId: liab}))
				return tr.GetTaxRate().GetId(), tr.GetTaxRate().GetResourceVersion()
			},
			get: func(ctx context.Context, id int64) (int64, error) {
				r, err := taxRates.GetTaxRate(ctx, &denarixv1.GetTaxRateRequest{Id: id})
				return r.GetTaxRate().GetResourceVersion(), err
			},
			update: func(ctx context.Context, id, v int64) (int64, error) {
				r, err := taxRates.UpdateTaxRate(ctx, &denarixv1.UpdateTaxRateRequest{Id: id, ResourceVersion: v, Rate: dec("0.09")})
				return r.GetTaxRate().GetResourceVersion(), err
			},
			deactivate: func(ctx context.Context, id, v int64) error {
				_, err := taxRates.DeactivateTaxRate(ctx, &denarixv1.DeactivateTaxRateRequest{Id: id, ResourceVersion: v})
				return err
			},
			deactivateAgain: codes.Aborted,
		},
		{
			name: "bank-statement",
			create: func(tc *testTenant) (int64, int64) {
				cash := tc.reconcilableAccount("1000", "Cash")
				bs := must(statements.CreateBankStatement(tc.ctx, &denarixv1.CreateBankStatementRequest{BusinessId: tc.businessID, LedgerAccountId: cash, StatementName: "Jan", StatementDate: dateOf(2026, 1, 31), OpeningBalance: dec("0"), ClosingBalance: dec("0")}))
				return bs.GetBankStatement().GetId(), bs.GetBankStatement().GetResourceVersion()
			},
			get: func(ctx context.Context, id int64) (int64, error) {
				r, err := statements.GetBankStatement(ctx, &denarixv1.GetBankStatementRequest{Id: id})
				return r.GetBankStatement().GetResourceVersion(), err
			},
			update: func(ctx context.Context, id, v int64) (int64, error) {
				r, err := statements.UpdateBankStatement(ctx, &denarixv1.UpdateBankStatementRequest{Id: id, ResourceVersion: v, StatementName: ptr("January")})
				return r.GetBankStatement().GetResourceVersion(), err
			},
			deactivate: func(ctx context.Context, id, v int64) error {
				_, err := statements.DeactivateBankStatement(ctx, &denarixv1.DeactivateBankStatementRequest{Id: id, ResourceVersion: v})
				return err
			},
			deactivateAgain: codes.NotFound, // deactivate soft-deletes
		},
		{
			name: "estimate",
			create: func(tc *testTenant) (int64, int64) {
				cust, item := tc.customerAndItem()
				e := must(estimates.CreateEstimate(tc.ctx, &denarixv1.CreateEstimateRequest{
					BusinessId: tc.businessID, CustomerId: cust, EstimateDate: dateOf(2026, 1, 1), ExpirationDate: dateOf(2026, 2, 1),
					LineItems: []*denarixv1.NewDocumentLineItem{{ItemId: item, LineNumber: 1, Quantity: dec("2")}},
				}))
				return e.GetEstimate().GetId(), e.GetEstimate().GetResourceVersion()
			},
			get: func(ctx context.Context, id int64) (int64, error) {
				r, err := estimates.GetEstimate(ctx, &denarixv1.GetEstimateRequest{Id: id})
				return r.GetEstimate().GetResourceVersion(), err
			},
			update: func(ctx context.Context, id, v int64) (int64, error) {
				e, err := estimates.GetEstimate(ctx, &denarixv1.GetEstimateRequest{Id: id})
				if err != nil {
					return 0, err
				}
				item := e.GetEstimate().GetLineItems()[0].GetItemId()
				r, err := estimates.UpdateEstimateLineItems(ctx, &denarixv1.UpdateEstimateLineItemsRequest{Id: id, ResourceVersion: v,
					LineItems: []*denarixv1.NewDocumentLineItem{{ItemId: item, LineNumber: 1, Quantity: dec("3")}}})
				return r.GetEstimate().GetResourceVersion(), err
			},
			deactivate: func(ctx context.Context, id, v int64) error {
				_, err := estimates.UpdateEstimateStatus(ctx, &denarixv1.UpdateEstimateStatusRequest{Id: id, ResourceVersion: v, Status: "SENT"})
				return err
			},
			deactivateAgain: codes.Aborted,
		},
		{
			name: "invoice",
			create: func(tc *testTenant) (int64, int64) {
				cust, item := tc.customerAndItem()
				inv := must(invoices.CreateInvoice(tc.ctx, &denarixv1.CreateInvoiceRequest{
					BusinessId: tc.businessID, ContactId: cust, InvoiceType: "SALES", InvoiceDate: dateOf(2026, 1, 1), DueDate: dateOf(2026, 1, 31),
					LineItems: []*denarixv1.NewDocumentLineItem{{ItemId: item, LineNumber: 1, Quantity: dec("2")}},
				}))
				return inv.GetInvoice().GetId(), inv.GetInvoice().GetResourceVersion()
			},
			get: func(ctx context.Context, id int64) (int64, error) {
				r, err := invoices.GetInvoice(ctx, &denarixv1.GetInvoiceRequest{Id: id})
				return r.GetInvoice().GetResourceVersion(), err
			},
			update: func(ctx context.Context, id, v int64) (int64, error) {
				r, err := invoices.UpdateInvoice(ctx, &denarixv1.UpdateInvoiceRequest{Id: id, ResourceVersion: v, Notes: ptr("thanks")})
				return r.GetInvoice().GetResourceVersion(), err
			},
			deactivate: func(ctx context.Context, id, v int64) error {
				_, err := invoices.UpdateInvoiceStatus(ctx, &denarixv1.UpdateInvoiceStatusRequest{Id: id, ResourceVersion: v, Status: "CANCELLED"})
				return err
			},
			deactivateAgain: codes.FailedPrecondition, // CANCELLED is terminal
		},
	}
}

func TestResourceCRUD(t *testing.T) {
	if testStore == nil {
		t.Skip("integration test: needs a database (run without -short)")
	}
	const unknownID = int64(1) << 40

	for _, c := range crudCases() {
		t.Run(c.name, func(t *testing.T) {
			defer failOnPanic(t)
			tc := newTenant(t)
			id, v1 := c.create(tc)

			got := must(c.get(tc.ctx, id))
			if got != v1 {
				t.Fatalf("get: resource_version = %d, want %d from create", got, v1)
			}
			_, err := c.get(tc.outsider, id)
			wantCode(t, err, codes.NotFound)
			_, err = c.get(tc.ctx, unknownID)
			wantCode(t, err, codes.NotFound)

			_, err = c.update(tc.ctx, id, v1+100)
			wantCode(t, err, codes.Aborted)
			_, err = c.update(tc.outsider, id, v1)
			wantCode(t, err, codes.NotFound)
			v2 := must(c.update(tc.ctx, id, v1))
			if v2 <= v1 {
				t.Fatalf("update: resource_version %d did not advance past %d", v2, v1)
			}
			if got := must(c.get(tc.ctx, id)); got != v2 {
				t.Fatalf("get after update: version %d, want %d", got, v2)
			}

			wantCode(t, c.deactivate(tc.outsider, id, v2), codes.NotFound)
			if err := c.deactivate(tc.ctx, id, v2); err != nil {
				t.Fatalf("deactivate: %v", err)
			}
			wantCode(t, c.deactivate(tc.ctx, id, v2), c.deactivateAgain)
		})
	}
}

// --- tenant fixtures shared by the integration tests ---------------------

// account creates a postable ledger account of the given type.
func (tc *testTenant) account(accountType int32, code, name string) int32 {
	tc.t.Helper()
	r := must(newLedgerAccountService(tc.store).CreateLedgerAccount(tc.ctx, &denarixv1.CreateLedgerAccountRequest{
		BusinessId: tc.businessID, AccountTypeId: accountType, Code: code, Name: name,
	}))
	return r.GetAccount().GetId()
}

func (tc *testTenant) reconcilableAccount(code, name string) int32 {
	tc.t.Helper()
	r := must(newLedgerAccountService(tc.store).CreateLedgerAccount(tc.ctx, &denarixv1.CreateLedgerAccountRequest{
		BusinessId: tc.businessID, AccountTypeId: 1, Code: code, Name: name, IsReconcilable: true,
	}))
	return r.GetAccount().GetId()
}

// customerAndItem provisions the minimum for a sales document: a customer
// contact with its own AR sub-account, and a catalog item posting to a
// revenue account.
func (tc *testTenant) customerAndItem() (contactID, itemID int64) {
	tc.t.Helper()
	rev := tc.account(4, "4000", "Revenue")
	c := must(newContactService(tc.store).CreateContact(tc.ctx, &denarixv1.CreateContactRequest{
		BusinessId: tc.businessID, ContactNumber: "C-1", Name: "Acme", IsCustomer: true,
	}))
	it := must(newItemService(tc.store).CreateItem(tc.ctx, &denarixv1.CreateItemRequest{
		BusinessId: tc.businessID, ItemCode: "SVC", Name: "Service", RetailPrice: dec("100.00"), DefaultLedgerAccountId: &rev,
	}))
	return c.GetContact().GetId(), it.GetItem().GetId()
}
