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
)

func decimalPB(v string) *denarixv1.Decimal { return &denarixv1.Decimal{Value: v} }

func TestComputeLines_RejectsNegativeTotal(t *testing.T) {
	// Both lines are non-taxable, so computeLines never touches q — nil is safe,
	// same pattern as TestLookupLineItem_ZeroItemIDIsInvalidArgument.
	inputs := []resolvedLine{
		{Quantity: decimalPB("1"), UnitPrice: decimalPB("100.00")},
		{Quantity: decimalPB("1"), UnitPrice: decimalPB("-200.00")},
	}
	_, _, _, _, err := computeLines(context.Background(), nil, 1, inputs)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v (%v), want InvalidArgument", status.Code(err), err)
	}
}

func TestComputeLines_AllowsNegativeLineWithinPositiveTotal(t *testing.T) {
	inputs := []resolvedLine{
		{Quantity: decimalPB("1"), UnitPrice: decimalPB("1000.00")},
		{Quantity: decimalPB("1"), UnitPrice: decimalPB("-100.00")},
	}
	_, subtotal, _, total, err := computeLines(context.Background(), nil, 1, inputs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !subtotal.Equal(decimal.RequireFromString("900.00")) {
		t.Errorf("subtotal = %s, want 900.00", subtotal)
	}
	if !total.Equal(decimal.RequireFromString("900.00")) {
		t.Errorf("total = %s, want 900.00", total)
	}
}
