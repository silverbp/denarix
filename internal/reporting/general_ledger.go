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

// GeneralLedger lists every posting to accountID within [start, end], with
// a running balance in the account's normal-balance direction.
func GeneralLedger(ctx context.Context, q *sqlcgen.Queries, accountID int32, start, end time.Time) (*GeneralLedgerResult, error) {
	account, err := q.GetLedgerAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	accountType, err := q.GetLedgerAccountType(ctx, account.AccountTypeID)
	if err != nil {
		return nil, err
	}

	rows, err := q.ListLedgerEntriesForAccount(ctx, sqlcgen.ListLedgerEntriesForAccountParams{
		AccountID:   accountID,
		PeriodStart: ledgermath.PgDate(start),
		PeriodEnd:   ledgermath.PgDate(end),
	})
	if err != nil {
		return nil, err
	}

	result := &GeneralLedgerResult{AccountID: account.ID, Code: account.Code, Name: account.Name}
	running := decimal.Zero
	for _, r := range rows {
		debit, err := ledgermath.NumericToDecimal(r.DebitAmount)
		if err != nil {
			return nil, err
		}
		credit, err := ledgermath.NumericToDecimal(r.CreditAmount)
		if err != nil {
			return nil, err
		}
		running = running.Add(ledgermath.NetBalance(accountType.NormalBalance, debit, credit))

		result.Lines = append(result.Lines, GeneralLedgerLine{
			LedgerTransactionID: r.LedgerTransactionID,
			TransactionDate:     r.TransactionDate.Time,
			Description:         r.TransactionDescription,
			Debit:               debit,
			Credit:              credit,
			RunningBalance:      running,
		})
	}
	result.EndingBalance = running
	return result, nil
}
