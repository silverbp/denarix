// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/db/sqlcgen"
	"github.com/silverbp/denarix/internal/ledgermath"
)

// testItem is a catalog row with every defaultable field populated, so each test can see
// exactly which of them a line falls back to.
func testItem(t *testing.T) sqlcgen.Item {
	t.Helper()
	price, err := ledgermath.DecimalToNumeric(decimal.RequireFromString("150.00"))
	if err != nil {
		t.Fatal(err)
	}
	acct := int32(40)
	taxRate := int64(7)
	return sqlcgen.Item{
		ID:                     71,
		BusinessID:             1,
		ItemCode:               "CONSULT",
		Name:                   "Consulting",
		RetailPrice:            price,
		IsTaxable:              true,
		DefaultTaxRateID:       &taxRate,
		DefaultLedgerAccountID: &acct,
		IsActive:               true,
	}
}

func ptr[T any](v T) *T { return &v }

// sameDecimal compares numerically - moneypb.ToProto normalizes trailing zeros, so
// "150.00" round-trips as "150".
func sameDecimal(t *testing.T, field string, got *denarixv1.Decimal, want string) {
	t.Helper()
	if got == nil {
		t.Errorf("%s = nil, want %s", field, want)
		return
	}
	if !decimal.RequireFromString(got.GetValue()).Equal(decimal.RequireFromString(want)) {
		t.Errorf("%s = %q, want %s", field, got.GetValue(), want)
	}
}

func TestResolveLine_InvoiceDefaultsFromItem(t *testing.T) {
	item := testItem(t)
	got, err := resolveLine(0, &denarixv1.NewDocumentLineItem{ItemId: 71, LineNumber: 1}, item, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.ItemID == nil || *got.ItemID != 71 {
		t.Errorf("ItemID = %v, want 71", got.ItemID)
	}
	if got.LedgerAccountID == nil || *got.LedgerAccountID != 40 {
		t.Errorf("LedgerAccountID = %v, want item's 40", got.LedgerAccountID)
	}
	if got.Description != "Consulting" {
		t.Errorf("Description = %q, want item name", got.Description)
	}
	sameDecimal(t, "UnitPrice", got.UnitPrice, "150.00")
	if !got.IsTaxable {
		t.Error("IsTaxable should default from item (true)")
	}
	if got.TaxRateID == nil || *got.TaxRateID != 7 {
		t.Errorf("TaxRateID = %v, want item default 7", got.TaxRateID)
	}
}

func TestResolveLine_LineOverridesWin(t *testing.T) {
	item := testItem(t)
	got, err := resolveLine(0, &denarixv1.NewDocumentLineItem{
		ItemId:      71,
		Description: "Consulting (March)",
		UnitPrice:   &denarixv1.Decimal{Value: "99.00"},
		IsTaxable:   ptr(true),
		TaxRateId:   ptr(int64(9)),
	}, item, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "Consulting (March)" {
		t.Errorf("Description = %q, want the line's own", got.Description)
	}
	sameDecimal(t, "UnitPrice", got.UnitPrice, "99.00")
	if got.TaxRateID == nil || *got.TaxRateID != 9 {
		t.Errorf("TaxRateID = %v, want the line's own 9", got.TaxRateID)
	}
	// The one thing a line can never override: the account is always the item's.
	if got.LedgerAccountID == nil || *got.LedgerAccountID != 40 {
		t.Errorf("LedgerAccountID = %v, want item's 40", got.LedgerAccountID)
	}
}

func TestResolveLine_ExplicitNotTaxableSuppressesItemTaxRate(t *testing.T) {
	item := testItem(t)
	got, err := resolveLine(0, &denarixv1.NewDocumentLineItem{ItemId: 71, IsTaxable: ptr(false)}, item, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.IsTaxable {
		t.Error("explicit is_taxable=false must win over the item's true")
	}
	if got.TaxRateID != nil {
		t.Errorf("TaxRateID = %v, want nil on a non-taxable line", *got.TaxRateID)
	}
}

func TestResolveLine_InvoiceItemWithoutAccountIsRejected(t *testing.T) {
	item := testItem(t)
	item.DefaultLedgerAccountID = nil
	_, err := resolveLine(3, &denarixv1.NewDocumentLineItem{ItemId: 71}, item, true)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("code = %v (%v), want FailedPrecondition", status.Code(err), err)
	}
	// An estimate line doesn't need one, and never carries an account.
	got, err := resolveLine(3, &denarixv1.NewDocumentLineItem{ItemId: 71}, item, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.LedgerAccountID != nil {
		t.Errorf("estimate line LedgerAccountID = %v, want nil", *got.LedgerAccountID)
	}
}

func TestResolveLine_EstimateDefaultsAndOverrides(t *testing.T) {
	item := testItem(t)
	got, err := resolveLine(0, &denarixv1.NewDocumentLineItem{ItemId: 71}, item, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.ItemID == nil || *got.ItemID != 71 || got.Description != "Consulting" ||
		!got.IsTaxable || got.TaxRateID == nil || *got.TaxRateID != 7 {
		t.Errorf("defaults not applied: %+v", got)
	}
	sameDecimal(t, "UnitPrice", got.UnitPrice, "150.00")

	got, err = resolveLine(0, &denarixv1.NewDocumentLineItem{ItemId: 71, Description: "Custom", UnitPrice: &denarixv1.Decimal{Value: "1.00"}}, item, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "Custom" {
		t.Errorf("line description override lost: %+v", got)
	}
	sameDecimal(t, "UnitPrice", got.UnitPrice, "1.00")
}

func TestCheckLineItemUsable_Inactive(t *testing.T) {
	item := testItem(t)
	if err := checkLineItemUsable(0, item); err != nil {
		t.Fatalf("active item should be usable, got %v", err)
	}
	item.IsActive = false
	if err := checkLineItemUsable(2, item); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("code = %v (%v), want FailedPrecondition", status.Code(err), err)
	}
}

func TestLookupLineItem_ZeroItemIDIsInvalidArgument(t *testing.T) {
	// The item_id == 0 check precedes the query, so a nil Queries never gets touched.
	_, err := lookupLineItem(context.Background(), nil, 1, 4, 0)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v (%v), want InvalidArgument", status.Code(err), err)
	}
}

func TestNewDocumentLineItemsFromEstimate_CarriesItemAndNeverAccount(t *testing.T) {
	qty, _ := ledgermath.DecimalToNumeric(decimal.RequireFromString("2"))
	price, _ := ledgermath.DecimalToNumeric(decimal.RequireFromString("10.00"))
	lines := []sqlcgen.EstimateLineItem{
		{ItemID: ptr(int64(71)), LineNumber: 1, Description: "A", Quantity: qty, UnitPrice: price, IsTaxable: true, TaxRateID: ptr(int64(7))},
		{ItemID: nil, LineNumber: 2, Description: "legacy", Quantity: qty, UnitPrice: price},
	}
	got := newDocumentLineItemsFromEstimate(lines)
	if got[0].GetItemId() != 71 || got[0].GetDescription() != "A" || !got[0].GetIsTaxable() || got[0].GetTaxRateId() != 7 {
		t.Errorf("line 0 not carried over: %+v", got[0])
	}
	sameDecimal(t, "Quantity", got[0].GetQuantity(), "2")
	sameDecimal(t, "UnitPrice", got[0].GetUnitPrice(), "10.00")
	// A pre-catalog estimate line has no item; it becomes 0 so lookupLineItem rejects it.
	if got[1].GetItemId() != 0 {
		t.Errorf("legacy line ItemId = %d, want 0", got[1].GetItemId())
	}
}

func TestPrefixStatus_KeepsCode(t *testing.T) {
	err := prefixStatus(status.Error(codes.InvalidArgument, "item 71 not found in business 1"), "line %d", 2)
	if status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != "line 2: item 71 not found in business 1" {
		t.Fatalf("got %v", err)
	}
}
