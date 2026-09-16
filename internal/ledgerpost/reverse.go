// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

// Package ledgerpost holds the ledger-posting primitives shared by more than
// one caller, so there's exactly one place that generates a reversing
// transaction rather than one copy per caller.
package ledgerpost

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/silverbp/denarix/internal/db/sqlcgen"
)

// ReverseTransaction posts a new ledger_transaction dated `date`, with one
// entry per entry of originalTxnID, debit and credit swapped — the guided
// correction for a transaction posted in error, and the shared primitive
// behind both period-close reversal (internal/periodclose.Reverse) and
// LedgerTransactionService.ReverseLedgerTransaction. The original
// transaction and its entries are never touched, consistent with the
// schema's soft-delete-over-mutation convention elsewhere. The new
// transaction records originalTxnID in reverses_ledger_transaction_id, and
// the partial unique index on that column means a second reversal of the
// same transaction fails at INSERT (unique_violation) rather than silently
// doubling the correction. Must run inside a single transaction — q should
// be bound via store.ExecTx, so the reversal either posts in full or not at
// all.
func ReverseTransaction(ctx context.Context, q *sqlcgen.Queries, businessID, originalTxnID int64, date pgtype.Date, description string, createdByUserID *int64) (sqlcgen.LedgerTransaction, error) {
	originalEntries, err := q.ListLedgerEntriesByTransaction(ctx, originalTxnID)
	if err != nil {
		return sqlcgen.LedgerTransaction{}, err
	}
	if len(originalEntries) == 0 {
		return sqlcgen.LedgerTransaction{}, fmt.Errorf("ledger transaction %d has no entries to reverse", originalTxnID)
	}

	txn, err := q.CreateLedgerTransaction(ctx, sqlcgen.CreateLedgerTransactionParams{
		BusinessID:                  businessID,
		TransactionDate:             date,
		Description:                 &description,
		CreatedByUserID:             createdByUserID,
		ReversesLedgerTransactionID: &originalTxnID,
	})
	if err != nil {
		return sqlcgen.LedgerTransaction{}, err
	}

	for _, e := range originalEntries {
		if _, err := q.CreateLedgerEntry(ctx, sqlcgen.CreateLedgerEntryParams{
			BusinessID:          businessID,
			LedgerTransactionID: txn.ID,
			AccountID:           e.AccountID,
			// Swapped: what was a debit on the original entry becomes a
			// credit here, and vice versa.
			DebitAmount:  e.CreditAmount,
			CreditAmount: e.DebitAmount,
		}); err != nil {
			return sqlcgen.LedgerTransaction{}, err
		}
	}
	return txn, nil
}
