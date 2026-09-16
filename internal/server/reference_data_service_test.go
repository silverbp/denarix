// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
)

// TestListLedgerReferenceData checks that all four seeded lookup tables
// come back non-empty and carry the ids ledger_account.account_type_id
// etc. actually reference (migrations/00001_initial.up.sql), and that the
// RPC is global - any authenticated user gets the same data regardless of
// business membership, but an unauthenticated caller is rejected.
func TestListLedgerReferenceData(t *testing.T) {
	tc := newTenant(t)
	svc := newReferenceDataService(testStore)

	resp := must(svc.ListLedgerReferenceData(tc.ctx, &denarixv1.ListLedgerReferenceDataRequest{}))

	if len(resp.GetAccountTypes()) != 6 {
		t.Fatalf("account_types = %d, want 6", len(resp.GetAccountTypes()))
	}
	wantIDs := map[int32]string{1: "ASSETS", 2: "LIABILITIES", 3: "EQUITY", 4: "REVENUE", 5: "EXPENSES", 6: "TAX_LIABILITY"}
	for _, at := range resp.GetAccountTypes() {
		if want, ok := wantIDs[at.GetId()]; !ok || want != at.GetName() {
			t.Fatalf("unexpected account type %+v", at)
		}
		if at.GetNormalBalance() != "DEBIT" && at.GetNormalBalance() != "CREDIT" {
			t.Fatalf("account type %d has bad normal_balance %q", at.GetId(), at.GetNormalBalance())
		}
	}

	if len(resp.GetCashFlowCategories()) != 3 {
		t.Fatalf("cash_flow_categories = %d, want 3", len(resp.GetCashFlowCategories()))
	}
	if len(resp.GetBalanceSheetCategories()) != 5 {
		t.Fatalf("balance_sheet_categories = %d, want 5", len(resp.GetBalanceSheetCategories()))
	}
	if len(resp.GetIncomeStatementCategories()) != 3 {
		t.Fatalf("income_statement_categories = %d, want 3", len(resp.GetIncomeStatementCategories()))
	}

	// Same response for an outsider - the data is global, not business-scoped.
	outsiderResp, err := svc.ListLedgerReferenceData(tc.outsider, &denarixv1.ListLedgerReferenceDataRequest{})
	if err != nil {
		t.Fatalf("outsider ListLedgerReferenceData: %v", err)
	}
	if len(outsiderResp.GetAccountTypes()) != len(resp.GetAccountTypes()) {
		t.Fatalf("outsider saw %d account types, want %d", len(outsiderResp.GetAccountTypes()), len(resp.GetAccountTypes()))
	}

	_, err = svc.ListLedgerReferenceData(context.Background(), &denarixv1.ListLedgerReferenceDataRequest{})
	wantCode(t, err, codes.Unauthenticated)
}
