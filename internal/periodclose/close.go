// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package periodclose

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/silverbp/denarix/internal/db/sqlcgen"
	"github.com/silverbp/denarix/internal/ledgermath"
	"github.com/silverbp/denarix/internal/ledgerpost"
)

// account_type_id values seeded by migrations/00001_initial.up.sql.
const (
	revenueTypeID  = 4
	expensesTypeID = 5
)

// CloseResult summarizes a completed close.
type CloseResult struct {
	PeriodClose          sqlcgen.PeriodClose
	LedgerTransactionIDs []int64
}

// Close runs the 5-step period-close algorithm from
// docs/architecture.md#period-close for businessID through periodEnd. Must
// run inside a single transaction — q should be bound via store.ExecTx.
// The lock trigger only sees already-committed period_close rows, so this
// close's own postings (steps 1-4 below) must land before its period_close
// row is inserted (step 5), which is exactly the order this function
// follows.
func Close(ctx context.Context, q *sqlcgen.Queries, businessID int64, periodEnd time.Time, createdByUserID *int64) (*CloseResult, error) {
	periodStart, err := resolvePeriodStart(ctx, q, businessID, periodEnd)
	if err != nil {
		return nil, err
	}

	incomeSummary, retainedEarnings, err := ProvisionSystemAccounts(ctx, q, businessID, createdByUserID)
	if err != nil {
		return nil, err
	}

	rows, err := q.AccountBalancesAsOf(ctx, sqlcgen.AccountBalancesAsOfParams{
		BusinessID:  businessID,
		PeriodStart: ledgermath.PgDate(periodStart),
		PeriodEnd:   ledgermath.PgDate(periodEnd),
	})
	if err != nil {
		return nil, err
	}

	var (
		transactionIDs   []int64
		sourceAccountIDs []int32
	)
	incomeSummaryDelta := decimal.Zero

	for _, r := range rows {
		if r.AccountTypeID != revenueTypeID && r.AccountTypeID != expensesTypeID {
			continue
		}
		debit, err := ledgermath.NumericToDecimal(r.TotalDebit)
		if err != nil {
			return nil, err
		}
		credit, err := ledgermath.NumericToDecimal(r.TotalCredit)
		if err != nil {
			return nil, err
		}
		net := ledgermath.NetBalance(r.NormalBalance, debit, credit)
		if net.IsZero() {
			continue
		}

		txnID, delta, err := postZeroingTransaction(ctx, q, businessID, periodEnd, r.AccountID, r.NormalBalance, net, incomeSummary, createdByUserID)
		if err != nil {
			return nil, err
		}
		transactionIDs = append(transactionIDs, txnID)
		sourceAccountIDs = append(sourceAccountIDs, r.AccountID)
		incomeSummaryDelta = incomeSummaryDelta.Add(delta)
	}

	if !incomeSummaryDelta.IsZero() {
		sweepTxnID, err := postSweepTransaction(ctx, q, businessID, periodEnd, incomeSummary, retainedEarnings, incomeSummaryDelta, createdByUserID)
		if err != nil {
			return nil, err
		}
		transactionIDs = append(transactionIDs, sweepTxnID)
		sourceAccountIDs = append(sourceAccountIDs, incomeSummary)
	}

	// Balance sanity (docs/architecture.md guard rail #4): defense-in-depth
	// check that every posting this close made balances. Should be
	// structurally guaranteed by the mirrored-entry construction below; a
	// mismatch here means a bug in this function, not bad input data.
	if err := ledgerpost.VerifyBalanced(ctx, q, transactionIDs...); err != nil {
		return nil, err
	}

	pc, err := q.CreatePeriodClose(ctx, sqlcgen.CreatePeriodCloseParams{
		BusinessID:                businessID,
		PeriodStart:               ledgermath.PgDate(periodStart),
		PeriodEnd:                 ledgermath.PgDate(periodEnd),
		IncomeSummaryAccountID:    incomeSummary,
		RetainedEarningsAccountID: retainedEarnings,
		CreatedByUserID:           createdByUserID,
	})
	if err != nil {
		return nil, err
	}

	for i, txnID := range transactionIDs {
		if _, err := q.CreatePeriodCloseEntry(ctx, sqlcgen.CreatePeriodCloseEntryParams{
			PeriodCloseID:       pc.ID,
			LedgerTransactionID: txnID,
			SourceAccountID:     sourceAccountIDs[i],
		}); err != nil {
			return nil, err
		}
	}

	return &CloseResult{PeriodClose: pc, LedgerTransactionIDs: transactionIDs}, nil
}

// resolvePeriodStart implements docs/architecture.md step 1 plus guard rail
// #1 (contiguity): the day after the business's last unreversed close, or
// business inception if none, and rejects a periodEnd that doesn't move the
// lock forward (idempotency guard #2 — a clear error instead of a raw
// partial-unique-index violation).
func resolvePeriodStart(ctx context.Context, q *sqlcgen.Queries, businessID int64, periodEnd time.Time) (time.Time, error) {
	lastClose, err := q.GetLastPeriodClose(ctx, businessID)
	switch {
	case err == nil:
		if !periodEnd.After(lastClose.PeriodEnd.Time) {
			return time.Time{}, fmt.Errorf("business %d is already closed through %s; period_end must be after that date",
				businessID, lastClose.PeriodEnd.Time.Format("2006-01-02"))
		}
		return lastClose.PeriodEnd.Time.AddDate(0, 0, 1), nil
	case errors.Is(err, pgx.ErrNoRows):
		return ledgermath.InceptionDate, nil
	default:
		return time.Time{}, err
	}
}

// postZeroingTransaction posts one ledger_transaction with two entries:
// zeroing accountID (posting the opposite of its net in its own normal
// direction), and the mirror image to Income Summary. Swapping the zeroing
// entry's own debit/credit for the Income Summary entry both trivially
// balances the transaction and gives Income Summary exactly the right
// signed contribution: REVENUE's net (CREDIT-normal) sweeps in as +net in
// Income Summary's own CREDIT-normal direction; EXPENSES's net
// (DEBIT-normal) sweeps in as -net there — so net income = revenue -
// expenses falls out of summing these deltas directly. Returns the
// transaction id and Income Summary's signed delta from this posting.
func postZeroingTransaction(ctx context.Context, q *sqlcgen.Queries, businessID int64, periodEnd time.Time, accountID int32, normalBalance string, net decimal.Decimal, incomeSummaryID int32, createdByUserID *int64) (txnID int64, incomeSummaryDelta decimal.Decimal, err error) {
	description := fmt.Sprintf("Period close %s: zero account %d", periodEnd.Format("2006-01-02"), accountID)
	txn, err := q.CreateLedgerTransaction(ctx, sqlcgen.CreateLedgerTransactionParams{
		BusinessID:      businessID,
		TransactionDate: ledgermath.PgDate(periodEnd),
		Description:     &description,
		CreatedByUserID: createdByUserID,
	})
	if err != nil {
		return 0, decimal.Zero, err
	}

	zeroDebit, zeroCredit := ledgermath.EntryForNormalDelta(normalBalance, net.Neg())
	if err := ledgerpost.CreateDecimalEntry(ctx, q, businessID, txn.ID, accountID, zeroDebit, zeroCredit); err != nil {
		return 0, decimal.Zero, err
	}
	if err := ledgerpost.CreateDecimalEntry(ctx, q, businessID, txn.ID, incomeSummaryID, zeroCredit, zeroDebit); err != nil {
		return 0, decimal.Zero, err
	}

	// Income Summary's own normal_balance is CREDIT (provisioned as
	// EQUITY — see ProvisionSystemAccounts), so its signed delta from this
	// posting is (what it was actually posted) credit - debit. Income
	// Summary's entry above is the SWAP of the source account's entry
	// (debit=zeroCredit, credit=zeroDebit), so the delta is
	// zeroDebit - zeroCredit, not the other way around.
	incomeSummaryDelta = zeroDebit.Sub(zeroCredit)
	return txn.ID, incomeSummaryDelta, nil
}

// postSweepTransaction implements step 4: zero Income Summary's resulting
// delta into Retained Earnings, again via the mirrored-entry trick.
func postSweepTransaction(ctx context.Context, q *sqlcgen.Queries, businessID int64, periodEnd time.Time, incomeSummaryID, retainedEarningsID int32, incomeSummaryDelta decimal.Decimal, createdByUserID *int64) (int64, error) {
	description := fmt.Sprintf("Period close %s: sweep Income Summary to Retained Earnings", periodEnd.Format("2006-01-02"))
	txn, err := q.CreateLedgerTransaction(ctx, sqlcgen.CreateLedgerTransactionParams{
		BusinessID:      businessID,
		TransactionDate: ledgermath.PgDate(periodEnd),
		Description:     &description,
		CreatedByUserID: createdByUserID,
	})
	if err != nil {
		return 0, err
	}

	zeroDebit, zeroCredit := ledgermath.EntryForNormalDelta("CREDIT", incomeSummaryDelta.Neg())
	if err := ledgerpost.CreateDecimalEntry(ctx, q, businessID, txn.ID, incomeSummaryID, zeroDebit, zeroCredit); err != nil {
		return 0, err
	}
	if err := ledgerpost.CreateDecimalEntry(ctx, q, businessID, txn.ID, retainedEarningsID, zeroCredit, zeroDebit); err != nil {
		return 0, err
	}
	return txn.ID, nil
}
