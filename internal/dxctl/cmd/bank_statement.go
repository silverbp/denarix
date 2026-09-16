// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package cmd

import (
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/dxctl/resource"
)

var bankStatementNoun = resource.Noun{
	Singular: "bank-statement",
	Plural:   "bank statements",
	Aliases:  []string{"bank-statements", "bs"},
	Columns: []resource.Column{
		resource.Int("ID", (*denarixv1.BankStatement).GetId),
		resource.Str("NAME", (*denarixv1.BankStatement).GetStatementName),
		resource.Int("ACCOUNT", (*denarixv1.BankStatement).GetLedgerAccountId),
		resource.Money("CLOSING", (*denarixv1.BankStatement).GetClosingBalance),
		resource.Money("RECONCILED", (*denarixv1.BankStatement).GetReconciledBalance),
		resource.Money("DIFFERENCE", (*denarixv1.BankStatement).GetDifference),
		resource.Int("LINES", func(bs *denarixv1.BankStatement) int { return len(bs.GetLines()) }),
		resource.Int("VERSION", (*denarixv1.BankStatement).GetResourceVersion),
	},
}

func newBankStatementCmd() *cobra.Command {
	root := newGroupCmd(bankStatementNoun, "Manage bank statements and reconciliation")
	root.AddCommand(
		newListCmd(bankStatementNoun, func(r run) ([]proto.Message, error) {
			resp, err := denarixv1.NewBankStatementServiceClient(r.conn).ListBankStatements(r.ctx, &denarixv1.ListBankStatementsRequest{BusinessId: r.businessID})
			return toMessages(resp.GetBankStatements()), err
		}),
		newGetCmd(bankStatementNoun, func(r run, id int64) (proto.Message, error) {
			resp, err := denarixv1.NewBankStatementServiceClient(r.conn).GetBankStatement(r.ctx, &denarixv1.GetBankStatementRequest{Id: id})
			return resp.GetBankStatement(), err
		}),
		newBankStatementCreateCmd(),
		newBankStatementUpdateCmd(),
		newVersionedMutateCmd(bankStatementNoun, "deactivate", resource.Doc{Summary: "Deactivate a bank statement"}, func(r run, id, resourceVersion int64) (proto.Message, error) {
			resp, err := denarixv1.NewBankStatementServiceClient(r.conn).DeactivateBankStatement(r.ctx, &denarixv1.DeactivateBankStatementRequest{Id: id, ResourceVersion: resourceVersion})
			return resp.GetBankStatement(), err
		}),
		newBankStatementReconcileCmd(),
		newBankStatementUnreconcileCmd(),
		newBankStatementUnreconciledCmd(),
	)
	return root
}

func newBankStatementCreateCmd() *cobra.Command {
	var account int32
	var name, date, opening, closing string
	var allowMismatch bool

	cmd := newCreateCmd(bankStatementNoun, resource.Doc{
		Summary: "Create a bank statement to reconcile against",
		Detail: "Rejects an opening balance that doesn't match the prior statement's closing " +
			"balance for the same account - pass --allow-opening-mismatch to override.",
		Examples: []resource.Example{{Cmd: "dxctl bank-statement create --account 10 --name \"Jan 2026\" --date 2026-01-31 --opening 1000.00 --closing 1500.00"}},
	}, func(r run) (proto.Message, error) {
		dateArg, err := parseDateFlag("date", date)
		if err != nil {
			return nil, err
		}
		resp, err := denarixv1.NewBankStatementServiceClient(r.conn).CreateBankStatement(r.ctx, &denarixv1.CreateBankStatementRequest{
			BusinessId:           r.businessID,
			LedgerAccountId:      account,
			StatementName:        name,
			StatementDate:        dateArg,
			OpeningBalance:       &denarixv1.Decimal{Value: opening},
			ClosingBalance:       &denarixv1.Decimal{Value: closing},
			AllowOpeningMismatch: allowMismatch,
		})
		return resp.GetBankStatement(), err
	})
	cmd.Flags().Int32Var(&account, "account", 0, "is_reconcilable ledger account id (required)")
	cmd.Flags().StringVar(&name, "name", "", "statement name/label (required)")
	cmd.Flags().StringVar(&date, "date", "", "statement date, YYYY-MM-DD (required)")
	cmd.Flags().StringVar(&opening, "opening", "0", "opening balance per the bank statement")
	cmd.Flags().StringVar(&closing, "closing", "0", "closing balance per the bank statement")
	cmd.Flags().BoolVar(&allowMismatch, "allow-opening-mismatch", false,
		"skip the check that opening must equal the prior statement's closing balance for this account (first statement on the account, or a mid-history import)")
	_ = cmd.MarkFlagRequired("account")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("date")
	return cmd
}

func newBankStatementUpdateCmd() *cobra.Command {
	var name, date, opening, closing string
	var allowMismatch bool

	cmd := newVersionedMutateCmd(bankStatementNoun, "update", resource.Doc{
		Summary:  "Fix a bank statement's own fields",
		Detail:   "Only flags you pass are sent - omit a flag to leave that field unchanged.",
		Examples: []resource.Example{{Cmd: "dxctl bank-statement update 3 --opening 10000.00"}},
	}, func(r run, id, resourceVersion int64) (proto.Message, error) {
		dateArg, err := r.optDate("date", &date)
		if err != nil {
			return nil, err
		}
		resp, err := denarixv1.NewBankStatementServiceClient(r.conn).UpdateBankStatement(r.ctx, &denarixv1.UpdateBankStatementRequest{
			Id:                   id,
			ResourceVersion:      resourceVersion,
			AllowOpeningMismatch: allowMismatch,
			StatementName:        r.optString("name", &name),
			StatementDate:        dateArg,
			OpeningBalance:       r.optDecimal("opening", &opening),
			ClosingBalance:       r.optDecimal("closing", &closing),
		})
		return resp.GetBankStatement(), err
	})
	cmd.Flags().StringVar(&name, "name", "", "new statement name/label")
	cmd.Flags().StringVar(&date, "date", "", "new statement date, YYYY-MM-DD")
	cmd.Flags().StringVar(&opening, "opening", "", "new opening balance")
	cmd.Flags().StringVar(&closing, "closing", "", "new closing balance")
	cmd.Flags().BoolVar(&allowMismatch, "allow-opening-mismatch", false,
		"skip the check that opening must equal the prior statement's closing balance for this account")
	return cmd
}

func newBankStatementReconcileCmd() *cobra.Command {
	var transactionIDs []int64

	cmd := newMutateCmd(bankStatementNoun, "reconcile", resource.Doc{
		Summary:  "Link ledger transactions to a bank statement",
		Detail:   "Each transaction must already post to the statement's own ledger account.",
		Examples: []resource.Example{{Cmd: "dxctl bank-statement reconcile 3 --transaction 12 --transaction 13"}},
	}, func(r run, id int64) (proto.Message, error) {
		resp, err := denarixv1.NewBankStatementServiceClient(r.conn).ReconcileLedgerTransactions(r.ctx, &denarixv1.ReconcileLedgerTransactionsRequest{
			BankStatementId:      id,
			LedgerTransactionIds: transactionIDs,
		})
		return resp.GetBankStatement(), err
	})
	cmd.Flags().Int64SliceVar(&transactionIDs, "transaction", nil, "ledger transaction id to reconcile (repeatable, required)")
	_ = cmd.MarkFlagRequired("transaction")
	return cmd
}

func newBankStatementUnreconcileCmd() *cobra.Command {
	var transactionIDs []int64

	cmd := newMutateCmd(bankStatementNoun, "unreconcile", resource.Doc{
		Summary:  "Unlink ledger transactions from a bank statement",
		Detail:   "Undoes a wrong `reconcile` call, or clears lines before `deactivate`. The transactions reappear in `unreconciled`.",
		Examples: []resource.Example{{Cmd: "dxctl bank-statement unreconcile 3 --transaction 12"}},
	}, func(r run, id int64) (proto.Message, error) {
		resp, err := denarixv1.NewBankStatementServiceClient(r.conn).UnreconcileLedgerTransactions(r.ctx, &denarixv1.UnreconcileLedgerTransactionsRequest{
			BankStatementId:      id,
			LedgerTransactionIds: transactionIDs,
		})
		return resp.GetBankStatement(), err
	})
	cmd.Flags().Int64SliceVar(&transactionIDs, "transaction", nil, "ledger transaction id to unreconcile (repeatable, required)")
	_ = cmd.MarkFlagRequired("transaction")
	return cmd
}

func newBankStatementUnreconciledCmd() *cobra.Command {
	var account int32
	var through string

	cmd := newTableCmd(bankStatementNoun, "unreconciled", resource.Doc{
		Summary:  "List ledger transactions not yet linked to any bank statement",
		Detail:   "Candidates for `bank-statement reconcile` on the given account, through the given date.",
		Examples: []resource.Example{{Cmd: "dxctl bank-statement unreconciled --account 10 --through 2026-01-31"}},
	}, ledgerTransactionNoun.Columns, func(r run) ([]proto.Message, error) {
		throughArg, err := parseDateFlag("through", through)
		if err != nil {
			return nil, err
		}
		resp, err := denarixv1.NewBankStatementServiceClient(r.conn).ListUnreconciledLedgerTransactions(r.ctx, &denarixv1.ListUnreconciledLedgerTransactionsRequest{
			LedgerAccountId: account,
			ThroughDate:     throughArg,
		})
		return toMessages(resp.GetTransactions()), err
	})
	cmd.Flags().Int32Var(&account, "account", 0, "ledger account id (required)")
	cmd.Flags().StringVar(&through, "through", "", "list transactions posted through this date, YYYY-MM-DD (required)")
	_ = cmd.MarkFlagRequired("account")
	_ = cmd.MarkFlagRequired("through")
	return cmd
}
