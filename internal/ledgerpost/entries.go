// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package ledgerpost

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/silverbp/denarix/internal/db/sqlcgen"
	"github.com/silverbp/denarix/internal/ledgermath"
)

// CreateDecimalEntry inserts one ledger_entry row for txnID, converting the
// decimal amounts to NUMERIC. Exactly one of debit/credit should be
// positive (ledger_entry_debit_or_credit) - see DebitCreditFor.
func CreateDecimalEntry(ctx context.Context, q *sqlcgen.Queries, businessID, txnID int64, accountID int32, debit, credit decimal.Decimal) error {
	debitNum, err := ledgermath.DecimalToNumeric(debit)
	if err != nil {
		return err
	}
	creditNum, err := ledgermath.DecimalToNumeric(credit)
	if err != nil {
		return err
	}
	_, err = q.CreateLedgerEntry(ctx, sqlcgen.CreateLedgerEntryParams{
		BusinessID:          businessID,
		LedgerTransactionID: txnID,
		AccountID:           accountID,
		DebitAmount:         debitNum,
		CreditAmount:        creditNum,
	})
	return err
}

// DebitCreditFor maps a signed amount onto the (debit, credit) pair
// ledger_entry requires — exactly one side strictly positive
// (ledger_entry_debit_or_credit, migrations/00001_initial.up.sql). naturalDebit
// says which side a positive amount belongs on for this leg (e.g. true for an
// AR leg, false for a revenue leg); a negative amount flips to the opposite
// side as its absolute value instead of going negative on its natural side.
// That's exactly how a discount line posts: a negative revenue line becomes a
// debit to that same revenue (or contra-revenue) account. A zero amount
// returns (0, 0) — callers must still skip it, since the XOR constraint
// rejects a zero entry on either side.
func DebitCreditFor(amount decimal.Decimal, naturalDebit bool) (debit, credit decimal.Decimal) {
	abs := amount.Abs()
	if amount.IsNegative() {
		naturalDebit = !naturalDebit
	}
	if naturalDebit {
		return abs, decimal.Zero
	}
	return decimal.Zero, abs
}

// VerifyBalanced is the defense-in-depth check every posting path runs
// after writing its entries: SUM(debit) == SUM(credit) across txnIDs. The
// DB CHECK on ledger_entry only guarantees each row has exactly one side
// populated; nothing in the schema enforces that a transaction as a whole
// balances. A mismatch here means a bug in the caller's arithmetic, never
// bad input.
func VerifyBalanced(ctx context.Context, q *sqlcgen.Queries, txnIDs ...int64) error {
	if len(txnIDs) == 0 {
		return nil
	}
	entries, err := q.ListLedgerEntriesByTransactionIDs(ctx, txnIDs)
	if err != nil {
		return err
	}
	totalDebit, totalCredit := decimal.Zero, decimal.Zero
	for _, e := range entries {
		d, err := ledgermath.NumericToDecimal(e.DebitAmount)
		if err != nil {
			return err
		}
		c, err := ledgermath.NumericToDecimal(e.CreditAmount)
		if err != nil {
			return err
		}
		totalDebit = totalDebit.Add(d)
		totalCredit = totalCredit.Add(c)
	}
	if !totalDebit.Equal(totalCredit) {
		return fmt.Errorf("posting produced unbalanced entries: total debit %s != total credit %s (this is a bug, not bad input)", totalDebit, totalCredit)
	}
	return nil
}
