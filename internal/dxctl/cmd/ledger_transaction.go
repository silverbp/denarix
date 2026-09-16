// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package cmd

import (
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/dxctl/resource"
)

var ledgerTransactionNoun = resource.Noun{
	Singular: "ledger-transaction",
	Plural:   "ledger transactions",
	Aliases:  []string{"ledger-transactions", "lt"},
	Columns: []resource.Column{
		resource.Int("ID", (*denarixv1.LedgerTransaction).GetId),
		resource.Date("DATE", (*denarixv1.LedgerTransaction).GetTransactionDate),
		resource.Str("DESCRIPTION", (*denarixv1.LedgerTransaction).GetDescription),
		resource.Int("ENTRIES", func(t *denarixv1.LedgerTransaction) int { return len(t.GetEntries()) }),
		resource.OptInt("REVERSES", func(t *denarixv1.LedgerTransaction) *int64 { return t.ReversesLedgerTransactionId }),
	},
}

func newLedgerTransactionCmd() *cobra.Command {
	root := newGroupCmd(ledgerTransactionNoun, "Read and post the double-entry ledger")
	root.AddCommand(
		newLedgerTransactionListCmd(),
		newGetCmd(ledgerTransactionNoun, func(r run, id int64) (proto.Message, error) {
			resp, err := denarixv1.NewLedgerTransactionServiceClient(r.conn).GetLedgerTransaction(r.ctx, &denarixv1.GetLedgerTransactionRequest{Id: id})
			return resp.GetTransaction(), err
		}),
		newLedgerTransactionPostCmd(),
		newLedgerTransactionReverseCmd(),
	)
	return root
}

// listPageSize caps a single ListLedgerTransactions call; the list command
// pages through as many calls as --limit needs.
const listPageSize = 200

func newLedgerTransactionListCmd() *cobra.Command {
	var (
		account              int32
		start, end, contains string
		limit                int
	)
	cmd := newTableCmd(ledgerTransactionNoun, "list", resource.Doc{
		Summary: "List ledger transactions, newest first",
		Detail: "Filters combine with AND. --description-contains is a case-insensitive substring " +
			"match on the transaction description (entry descriptions aren't searched). " +
			"The default --limit is 50; pass --limit 0 to fetch every match.",
		Examples: []resource.Example{
			{Cmd: "dxctl ledger-transaction list --account 81 --start 2024-01-01 --end 2024-12-31"},
			{Cmd: "dxctl ledger-transaction list --description-contains \"sales tax\" --limit 0"},
		},
	}, ledgerTransactionNoun.Columns, func(r run) ([]proto.Message, error) {
		req := &denarixv1.ListLedgerTransactionsRequest{
			BusinessId:          r.businessID,
			AccountId:           r.optInt32("account", &account),
			DescriptionContains: r.optString("description-contains", &contains),
		}
		var err error
		if req.StartDate, err = r.optDate("start", &start); err != nil {
			return nil, err
		}
		if req.EndDate, err = r.optDate("end", &end); err != nil {
			return nil, err
		}
		client := denarixv1.NewLedgerTransactionServiceClient(r.conn)
		var all []proto.Message
		for {
			req.PageSize = listPageSize
			if limit > 0 && limit-len(all) < listPageSize {
				req.PageSize = int32(limit - len(all))
			}
			resp, err := client.ListLedgerTransactions(r.ctx, req)
			if err != nil {
				return nil, err
			}
			all = append(all, toMessages(resp.GetTransactions())...)
			if resp.GetNextPageToken() == "" || (limit > 0 && len(all) >= limit) {
				return all, nil
			}
			req.PageToken = resp.GetNextPageToken()
		}
	})
	cmd.Flags().Int32Var(&account, "account", 0, "only transactions with an entry against this ledger account id")
	cmd.Flags().StringVar(&start, "start", "", "only transactions dated on or after this date, YYYY-MM-DD")
	cmd.Flags().StringVar(&end, "end", "", "only transactions dated on or before this date, YYYY-MM-DD")
	cmd.Flags().StringVar(&contains, "description-contains", "", "only transactions whose description contains this text (case-insensitive)")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum transactions to return; 0 = all")
	return cmd
}

func newLedgerTransactionReverseCmd() *cobra.Command {
	var date string
	cmd := newMutateCmd(ledgerTransactionNoun, "reverse", resource.Doc{
		Summary: "Post a new transaction reversing an existing one",
		Detail: "Mirrors every entry of the original transaction with debit and credit swapped; the " +
			"original is never touched. A transaction can be reversed once, and a reversal can't itself " +
			"be reversed. Rejected if the transaction is linked from an invoice or " +
			"payment - correct those through `invoice cancel` / `payment void` instead, which keep " +
			"paid_amount/balance_due in sync.",
		Examples: []resource.Example{
			{Cmd: "dxctl ledger-transaction reverse 42"},
			{Cmd: "dxctl ledger-transaction reverse 42 --date 2026-02-01", Desc: "original is in a closed period - post the reversal in the open one"},
		},
	}, func(r run, id int64) (proto.Message, error) {
		reversalDate, err := r.optDate("date", &date)
		if err != nil {
			return nil, err
		}
		resp, err := denarixv1.NewLedgerTransactionServiceClient(r.conn).ReverseLedgerTransaction(r.ctx, &denarixv1.ReverseLedgerTransactionRequest{Id: id, ReversalDate: reversalDate})
		return resp.GetTransaction(), err
	})
	cmd.Flags().StringVar(&date, "date", "", "date to post the reversal on (YYYY-MM-DD); defaults to the original's date - set it when the original falls in a closed period")
	return cmd
}

func newLedgerTransactionPostCmd() *cobra.Command {
	var date, description, reference string
	var rawEntries []string

	cmd := newNoArgCmd(ledgerTransactionNoun, "post", resource.Doc{
		Summary: "Post a balanced double-entry transaction",
		Detail: "Repeat --entry once per posting line. Posting is atomic - the API " +
			"never produces an unbalanced or partially-posted transaction - and " +
			"permanent: there is no edit or delete, so correcting a mistake means " +
			"`ledger-transaction reverse` rather than undoing this one.",
		Examples: []resource.Example{{Cmd: "dxctl ledger-transaction post --date 2026-01-15 " +
			"--entry account=101,debit=500.00 --entry account=400,credit=500.00"}},
	}, func(r run) (proto.Message, error) {
		entries, err := parseEntryFlags(rawEntries)
		if err != nil {
			return nil, err
		}
		txnDate, err := parseDateFlag("date", date)
		if err != nil {
			return nil, err
		}
		resp, err := denarixv1.NewLedgerTransactionServiceClient(r.conn).CreateLedgerTransaction(r.ctx, &denarixv1.CreateLedgerTransactionRequest{
			BusinessId:      r.businessID,
			TransactionDate: txnDate,
			Entries:         entries,
			Description:     r.optString("description", &description),
			ReferenceNumber: r.optString("reference", &reference),
		})
		return resp.GetTransaction(), err
	})
	cmd.Flags().StringVar(&date, "date", "", "transaction date, YYYY-MM-DD (required)")
	cmd.Flags().StringVar(&description, "description", "", "transaction description")
	cmd.Flags().StringVar(&reference, "reference", "", "reference number")
	cmd.Flags().StringArrayVar(&rawEntries, "entry", nil, "account=<id>,debit=<amt> or account=<id>,credit=<amt> (repeatable, at least 2 required)")
	_ = cmd.MarkFlagRequired("date")
	_ = cmd.MarkFlagRequired("entry")
	return cmd
}
