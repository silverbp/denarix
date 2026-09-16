// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package reporting

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"github.com/silverbp/denarix/internal/db/sqlcgen"
	"github.com/silverbp/denarix/internal/ledgermath"
)

// TrialBalance sums every account's debit/credit activity from inception
// through asOf, omitting accounts with no activity in that window.
// SUM(TotalDebit) == SUM(TotalCredit) is a checkable property of the
// underlying ledger_entry data (the debit-or-credit CHECK constraint
// guarantees it per row), so a trial balance that doesn't balance would
// indicate a bug in this query, not bad input data.
func TrialBalance(ctx context.Context, q *sqlcgen.Queries, businessID int64, asOf time.Time) (*TrialBalanceResult, error) {
	rows, err := q.AccountBalancesAsOf(ctx, sqlcgen.AccountBalancesAsOfParams{
		BusinessID:  businessID,
		PeriodStart: ledgermath.PgDate(ledgermath.InceptionDate),
		PeriodEnd:   ledgermath.PgDate(asOf),
	})
	if err != nil {
		return nil, err
	}

	result := &TrialBalanceResult{TotalDebit: decimal.Zero, TotalCredit: decimal.Zero}
	for _, r := range rows {
		debit, err := ledgermath.NumericToDecimal(r.TotalDebit)
		if err != nil {
			return nil, err
		}
		credit, err := ledgermath.NumericToDecimal(r.TotalCredit)
		if err != nil {
			return nil, err
		}
		if debit.IsZero() && credit.IsZero() {
			continue
		}

		result.Lines = append(result.Lines, TrialBalanceLine{
			AccountID: r.AccountID,
			Code:      r.Code,
			Name:      r.Name,
			Debit:     debit,
			Credit:    credit,
		})
		result.TotalDebit = result.TotalDebit.Add(debit)
		result.TotalCredit = result.TotalCredit.Add(credit)
	}
	return result, nil
}
