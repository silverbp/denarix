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

// IncomeStatement sums REVENUE/EXPENSE activity over [start, end] into the standard US
// multi-step shape (Revenue - Cost of Goods Sold = Gross Profit; Gross Profit - Operating
// Expenses = Net Income), splitting EXPENSES-type accounts via
// ledger_account.income_statement_category_id. Reads ledger_entry directly regardless of period-close
// state — a P&L for a range doesn't care whether that range has since been closed
// (docs/architecture.md#period-close, "Reporting integration").
func IncomeStatement(ctx context.Context, q *sqlcgen.Queries, businessID int64, start, end time.Time) (*IncomeStatementResult, error) {
	rows, err := q.AccountBalancesAsOf(ctx, sqlcgen.AccountBalancesAsOfParams{
		BusinessID:  businessID,
		PeriodStart: ledgermath.PgDate(start),
		PeriodEnd:   ledgermath.PgDate(end),
	})
	if err != nil {
		return nil, err
	}

	result := &IncomeStatementResult{
		TotalRevenue:           decimal.Zero,
		TotalCostOfGoodsSold:   decimal.Zero,
		TotalOperatingExpenses: decimal.Zero,
	}
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
		if debit.IsZero() && credit.IsZero() {
			continue
		}

		net := ledgermath.NetBalance(r.NormalBalance, debit, credit)
		line := AccountLine{AccountID: r.AccountID, Code: r.Code, Name: r.Name, Amount: net}

		switch {
		case r.AccountTypeID == revenueTypeID:
			result.Revenue = append(result.Revenue, line)
			result.TotalRevenue = result.TotalRevenue.Add(net)
		case r.IncomeStatementCategoryID != nil && *r.IncomeStatementCategoryID == costOfGoodsSoldCategoryID:
			result.CostOfGoodsSold = append(result.CostOfGoodsSold, line)
			result.TotalCostOfGoodsSold = result.TotalCostOfGoodsSold.Add(net)
		default:
			result.OperatingExpenses = append(result.OperatingExpenses, line)
			result.TotalOperatingExpenses = result.TotalOperatingExpenses.Add(net)
		}
	}
	result.GrossProfit = result.TotalRevenue.Sub(result.TotalCostOfGoodsSold)
	result.TotalExpenses = result.TotalCostOfGoodsSold.Add(result.TotalOperatingExpenses)
	result.NetIncome = result.GrossProfit.Sub(result.TotalOperatingExpenses)
	return result, nil
}
