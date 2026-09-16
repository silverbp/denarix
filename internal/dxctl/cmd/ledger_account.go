// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package cmd

import (
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/dxctl/resource"
)

var ledgerAccountNoun = resource.Noun{
	Singular: "ledger-account",
	Plural:   "ledger accounts",
	Aliases:  []string{"ledger-accounts", "la"},
	Columns: []resource.Column{
		resource.Int("ID", (*denarixv1.LedgerAccount).GetId),
		resource.Str("CODE", (*denarixv1.LedgerAccount).GetCode),
		resource.Str("NAME", (*denarixv1.LedgerAccount).GetName),
		resource.Int("TYPE", (*denarixv1.LedgerAccount).GetAccountTypeId),
		resource.Bool("SYSTEM", (*denarixv1.LedgerAccount).GetIsSystem),
		resource.OptInt("PARENT", func(a *denarixv1.LedgerAccount) *int32 { return a.ParentAccountId }),
		resource.Bool("ACTIVE", (*denarixv1.LedgerAccount).GetIsActive),
		resource.Int("VERSION", (*denarixv1.LedgerAccount).GetResourceVersion),
	},
}

// ledger_account ids are int32 (the one resource whose id isn't a bigint);
// the generic verb builders parse an int64, so verbs narrow it here.

func newLedgerAccountCmd() *cobra.Command {
	root := newGroupCmd(ledgerAccountNoun, "Manage the chart of accounts")

	var includeSubaccounts bool
	listCmd := newListCmd(ledgerAccountNoun, func(r run) ([]proto.Message, error) {
		resp, err := denarixv1.NewLedgerAccountServiceClient(r.conn).ListLedgerAccounts(r.ctx, &denarixv1.ListLedgerAccountsRequest{BusinessId: r.businessID})
		if err != nil {
			return nil, err
		}
		items := make([]proto.Message, 0, len(resp.GetAccounts()))
		for _, a := range resp.GetAccounts() {
			if !includeSubaccounts && a.ParentAccountId != nil {
				continue
			}
			items = append(items, a)
		}
		return items, nil
	})
	listCmd.Flags().BoolVar(&includeSubaccounts, "all", false, "also include customer/vendor sub-accounts (any account with a parent_account_id) - excluded by default")

	root.AddCommand(
		listCmd,
		newGetCmd(ledgerAccountNoun, func(r run, id int64) (proto.Message, error) {
			resp, err := denarixv1.NewLedgerAccountServiceClient(r.conn).GetLedgerAccount(r.ctx, &denarixv1.GetLedgerAccountRequest{Id: int32(id)})
			return resp.GetAccount(), err
		}),
		newLedgerAccountCreateCmd(),
		newLedgerAccountUpdateCmd(),
		newVersionedMutateCmd(ledgerAccountNoun, "deactivate", resource.Doc{Summary: "Deactivate a ledger account"}, func(r run, id, resourceVersion int64) (proto.Message, error) {
			resp, err := denarixv1.NewLedgerAccountServiceClient(r.conn).DeactivateLedgerAccount(r.ctx, &denarixv1.DeactivateLedgerAccountRequest{Id: int32(id), ResourceVersion: resourceVersion})
			return resp.GetAccount(), err
		}),
	)
	return root
}

func newLedgerAccountCreateCmd() *cobra.Command {
	var code, name, description string
	var accountTypeID, parentID, cashFlowCategoryID, balanceSheetCategoryID, incomeStatementCategoryID int32
	var reconcilable, container bool

	cmd := newCreateCmd(ledgerAccountNoun, resource.Doc{
		Summary:  "Create a chart-of-accounts entry",
		Examples: []resource.Example{{Cmd: "dxctl ledger-account create --code 1000 --name Cash --account-type 1"}},
	}, func(r run) (proto.Message, error) {
		resp, err := denarixv1.NewLedgerAccountServiceClient(r.conn).CreateLedgerAccount(r.ctx, &denarixv1.CreateLedgerAccountRequest{
			BusinessId:                r.businessID,
			AccountTypeId:             accountTypeID,
			Code:                      code,
			Name:                      name,
			IsReconcilable:            reconcilable,
			IsContainer:               container,
			Description:               r.optString("description", &description),
			ParentAccountId:           r.optInt32("parent", &parentID),
			CashFlowCategoryId:        r.optInt32("cash-flow-category", &cashFlowCategoryID),
			BalanceSheetCategoryId:    r.optInt32("balance-sheet-category", &balanceSheetCategoryID),
			IncomeStatementCategoryId: r.optInt32("income-statement-category", &incomeStatementCategoryID),
		})
		return resp.GetAccount(), err
	})
	cmd.Flags().StringVar(&code, "code", "", "account code, e.g. 1000 (required)")
	cmd.Flags().StringVar(&name, "name", "", "account name (required)")
	cmd.Flags().StringVar(&description, "description", "", "account description")
	cmd.Flags().Int32Var(&accountTypeID, "account-type", 0, "ledger_account_type id: 1=ASSETS 2=LIABILITIES 3=EQUITY 4=REVENUE 5=EXPENSES 6=TAX_LIABILITY (required)")
	cmd.Flags().Int32Var(&parentID, "parent", 0, "parent ledger account id (rolls this account up under a container, e.g. Accounts Receivable)")
	cmd.Flags().Int32Var(&cashFlowCategoryID, "cash-flow-category", 0, "cash_flow_category id: 1=Operating 2=Investing 3=Financing")
	cmd.Flags().Int32Var(&balanceSheetCategoryID, "balance-sheet-category", 0, "balance_sheet_category id (for ASSETS/LIABILITIES/EQUITY/TAX_LIABILITY accounts)")
	cmd.Flags().Int32Var(&incomeStatementCategoryID, "income-statement-category", 0, "income_statement_category id: 1=Revenue 2=Cost of Goods Sold 3=Operating Expenses (for REVENUE/EXPENSES accounts)")
	cmd.Flags().BoolVar(&reconcilable, "reconcilable", false, "mark this account eligible for bank-statement reconciliation")
	cmd.Flags().BoolVar(&container, "container", false, "mark this account as a non-postable roll-up node (e.g. Accounts Receivable)")
	_ = cmd.MarkFlagRequired("code")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("account-type")
	return cmd
}

func newLedgerAccountUpdateCmd() *cobra.Command {
	var name, description string
	var cashFlowCategoryID, balanceSheetCategoryID, incomeStatementCategoryID int32
	var reconcilable, container bool

	cmd := newVersionedMutateCmd(ledgerAccountNoun, "update", resource.Doc{
		Summary:  "Update a ledger account",
		Detail:   "Only flags you pass are sent - omit a flag to leave that field unchanged.",
		Examples: []resource.Example{{Cmd: "dxctl ledger-account update 40 --name \"Consulting Revenue\""}},
	}, func(r run, id, resourceVersion int64) (proto.Message, error) {
		resp, err := denarixv1.NewLedgerAccountServiceClient(r.conn).UpdateLedgerAccount(r.ctx, &denarixv1.UpdateLedgerAccountRequest{
			Id:                        int32(id),
			ResourceVersion:           resourceVersion,
			Name:                      r.optString("name", &name),
			Description:               r.optString("description", &description),
			IsReconcilable:            r.optBool("reconcilable", &reconcilable),
			IsContainer:               r.optBool("container", &container),
			CashFlowCategoryId:        r.optInt32("cash-flow-category", &cashFlowCategoryID),
			BalanceSheetCategoryId:    r.optInt32("balance-sheet-category", &balanceSheetCategoryID),
			IncomeStatementCategoryId: r.optInt32("income-statement-category", &incomeStatementCategoryID),
		})
		return resp.GetAccount(), err
	})
	cmd.Flags().StringVar(&name, "name", "", "new account name")
	cmd.Flags().StringVar(&description, "description", "", "new account description")
	cmd.Flags().Int32Var(&cashFlowCategoryID, "cash-flow-category", 0, "new cash_flow_category id: 1=Operating 2=Investing 3=Financing")
	cmd.Flags().Int32Var(&balanceSheetCategoryID, "balance-sheet-category", 0, "new balance_sheet_category id")
	cmd.Flags().Int32Var(&incomeStatementCategoryID, "income-statement-category", 0, "new income_statement_category id: 1=Revenue 2=Cost of Goods Sold 3=Operating Expenses")
	cmd.Flags().BoolVar(&reconcilable, "reconcilable", false, "eligible for bank-statement reconciliation")
	cmd.Flags().BoolVar(&container, "container", false, "a non-postable roll-up node")
	return cmd
}
