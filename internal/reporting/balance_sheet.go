// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package reporting

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/silverbp/denarix/internal/db/sqlcgen"
	"github.com/silverbp/denarix/internal/ledgermath"
)

// currentEarningsLineCode labels the synthetic equity line below — not a
// real ledger_account, so it needs a code distinct from any real one
// (AccountID 0, which no real ledger_account uses, since ids start at 1).
const currentEarningsLineCode = "CURRENT-EARNINGS"

// uncategorizedSectionTitle groups any account with no
// balance_sheet_category_id (e.g. one created through the API before a
// caller picks a category). Appended last, and only if non-empty, so a
// missing category never silently drops a balance from the report instead
// of just showing up in an obviously-named catch-all.
const uncategorizedSectionTitle = "Uncategorized"

// balanceSheetSectionOrder is the fixed display order of every known
// balance_sheet_category - always rendered, even with zero balances (see
// the screenshot this report format was modeled on: "Long-term Liabilities
// (total) 0.00" prints with no line items under it).
var balanceSheetSectionOrder = []struct {
	id    int32
	title string
}{
	{longTermAssetsCategoryID, "Long-term Assets"},
	{currentCategoryID, "Current Assets & Liabilities"},
	{longTermLiabilitiesCategoryID, "Long-term Liabilities"},
	{capitalAndReservesCategoryID, "Capital & Reserves"},
	{openingBalancesCategoryID, "Opening Balances"},
}

// BalanceSheet reports every ledger_account balance as of asOf, grouped by
// balance_sheet_category into the sections a classic UK-style statutory
// balance sheet uses (Long-term Assets / Current Assets & Liabilities /
// Long-term Liabilities / Capital & Reserves), plus the derived subtotal
// rows between them (Net current assets, Total assets less current
// liabilities, Total net assets). Grouping is presentation only - which
// column a line prints in (asset vs. liability) is driven by the account's
// normal_balance, not its category, since "Current Assets & Liabilities"
// deliberately mixes both in one section.
//
// If asOf falls after the business's latest unreversed period close (or no
// close exists yet), REVENUE/EXPENSE activity since that close hasn't been
// swept into Retained Earnings, so it's reported as a synthetic "Current
// Period Earnings" line under Capital & Reserves - the standard accounting
// treatment for an as-of date within a still-open period
// (docs/architecture.md#period-close, "Reporting integration").
func BalanceSheet(ctx context.Context, q *sqlcgen.Queries, businessID int64, asOf time.Time) (*BalanceSheetResult, error) {
	rows, err := q.AccountBalancesAsOf(ctx, sqlcgen.AccountBalancesAsOfParams{
		BusinessID:  businessID,
		PeriodStart: ledgermath.PgDate(ledgermath.InceptionDate),
		PeriodEnd:   ledgermath.PgDate(asOf),
	})
	if err != nil {
		return nil, err
	}

	sections := make(map[int32]*BalanceSheetSection, len(balanceSheetSectionOrder))
	for _, s := range balanceSheetSectionOrder {
		sections[s.id] = &BalanceSheetSection{Title: s.title, TotalAssets: decimal.Zero, TotalLiabilities: decimal.Zero}
	}
	uncategorized := &BalanceSheetSection{Title: uncategorizedSectionTitle, TotalAssets: decimal.Zero, TotalLiabilities: decimal.Zero}

	byID, childrenOf, err := indexBalanceSheetRows(rows)
	if err != nil {
		return nil, err
	}
	rolledUp := make(map[int32]decimal.Decimal, len(byID))
	for id := range byID {
		rollUpAccountBalance(id, byID, childrenOf, rolledUp)
	}

	// Iterate rows (not byID) to keep the SQL's ORDER BY code as the display order - map
	// iteration order is random in Go.
	for _, r := range rows {
		acct, ok := byID[r.AccountID]
		if !ok || acct.row.ParentAccountID != nil {
			// Not a balance-sheet account (REVENUE/EXPENSES, filtered out by
			// indexBalanceSheetRows), or not printed on its own - its balance is already
			// folded into an ancestor's rolled-up total (see rollUpAccountBalance), which is
			// the only one of the two that ever prints as a line.
			continue
		}
		net := rolledUp[r.AccountID]
		if net.IsZero() {
			continue
		}

		line := AccountLine{AccountID: acct.row.AccountID, Code: acct.row.Code, Name: acct.row.Name, Amount: net}

		section := uncategorized
		if acct.row.BalanceSheetCategoryID != nil {
			if s, ok := sections[*acct.row.BalanceSheetCategoryID]; ok {
				section = s
			}
		}

		// A DEBIT-normal account (ASSETS) prints in the Asset column; every
		// CREDIT-normal type (LIABILITIES, EQUITY, TAX_LIABILITY) prints in the
		// Liability column - the same two-column split the source report uses.
		if acct.row.NormalBalance == "DEBIT" {
			section.AssetLines = append(section.AssetLines, line)
			section.TotalAssets = section.TotalAssets.Add(net)
		} else {
			section.LiabilityLines = append(section.LiabilityLines, line)
			section.TotalLiabilities = section.TotalLiabilities.Add(net)
		}
	}

	lastClose, err := q.GetLastPeriodClose(ctx, businessID)
	periodStart := ledgermath.InceptionDate
	withinOpenPeriod := true
	switch {
	case err == nil:
		periodStart = lastClose.PeriodEnd.Time.AddDate(0, 0, 1)
		withinOpenPeriod = asOf.After(lastClose.PeriodEnd.Time)
	case errors.Is(err, pgx.ErrNoRows):
		// No close yet: the whole ledger-to-date is "the open period".
	default:
		return nil, err
	}

	if withinOpenPeriod {
		earnings, err := currentPeriodEarnings(ctx, q, businessID, periodStart, asOf)
		if err != nil {
			return nil, err
		}
		if !earnings.IsZero() {
			capital := sections[capitalAndReservesCategoryID]
			capital.LiabilityLines = append(capital.LiabilityLines, AccountLine{
				AccountID: 0,
				Code:      currentEarningsLineCode,
				Name:      "Current Period Earnings",
				Amount:    earnings,
			})
			capital.TotalLiabilities = capital.TotalLiabilities.Add(earnings)
		}
	}

	result := &BalanceSheetResult{TotalAssets: decimal.Zero, TotalLiabilities: decimal.Zero}
	for _, s := range balanceSheetSectionOrder {
		result.Sections = append(result.Sections, *sections[s.id])
	}
	if len(uncategorized.AssetLines) > 0 || len(uncategorized.LiabilityLines) > 0 {
		result.Sections = append(result.Sections, *uncategorized)
	}
	for _, s := range result.Sections {
		result.TotalAssets = result.TotalAssets.Add(s.TotalAssets)
		result.TotalLiabilities = result.TotalLiabilities.Add(s.TotalLiabilities)
	}

	current := sections[currentCategoryID]
	result.NetCurrentAssets = current.TotalAssets.Sub(current.TotalLiabilities)
	result.TotalAssetsLessCurrentLiabilities = sections[longTermAssetsCategoryID].TotalAssets.Add(result.NetCurrentAssets)
	result.TotalNetAssets = result.TotalAssetsLessCurrentLiabilities.Sub(sections[longTermLiabilitiesCategoryID].TotalLiabilities)

	return result, nil
}

type balanceSheetAccount struct {
	row    sqlcgen.AccountBalancesAsOfRow
	ownNet decimal.Decimal
}

// indexBalanceSheetRows filters rows down to balance-sheet accounts (excluding REVENUE/EXPENSES,
// which never appear here - see BalanceSheet), computes each one's own net balance, and builds a
// parent -> children index for rollUpAccountBalance.
func indexBalanceSheetRows(rows []sqlcgen.AccountBalancesAsOfRow) (map[int32]*balanceSheetAccount, map[int32][]int32, error) {
	byID := make(map[int32]*balanceSheetAccount, len(rows))
	for _, r := range rows {
		if r.AccountTypeID == revenueTypeID || r.AccountTypeID == expensesTypeID {
			continue
		}
		debit, err := ledgermath.NumericToDecimal(r.TotalDebit)
		if err != nil {
			return nil, nil, err
		}
		credit, err := ledgermath.NumericToDecimal(r.TotalCredit)
		if err != nil {
			return nil, nil, err
		}
		byID[r.AccountID] = &balanceSheetAccount{row: r, ownNet: ledgermath.NetBalance(r.NormalBalance, debit, credit)}
	}

	childrenOf := make(map[int32][]int32)
	for id, acct := range byID {
		if acct.row.ParentAccountID != nil {
			childrenOf[*acct.row.ParentAccountID] = append(childrenOf[*acct.row.ParentAccountID], id)
		}
	}
	return byID, childrenOf, nil
}

// rollUpAccountBalance sums an account's own balance with every descendant's (walking
// parent_account_id, e.g. every customer ledger_account rolling up into Accounts Receivable),
// memoized in rolledUp so a container's total is computed once even if this is called for
// several of its children.
func rollUpAccountBalance(id int32, byID map[int32]*balanceSheetAccount, childrenOf map[int32][]int32, rolledUp map[int32]decimal.Decimal) decimal.Decimal {
	if total, ok := rolledUp[id]; ok {
		return total
	}
	acct, ok := byID[id]
	if !ok {
		return decimal.Zero
	}
	total := acct.ownNet
	for _, childID := range childrenOf[id] {
		total = total.Add(rollUpAccountBalance(childID, byID, childrenOf, rolledUp))
	}
	rolledUp[id] = total
	return total
}

func currentPeriodEarnings(ctx context.Context, q *sqlcgen.Queries, businessID int64, start, end time.Time) (decimal.Decimal, error) {
	rows, err := q.AccountBalancesAsOf(ctx, sqlcgen.AccountBalancesAsOfParams{
		BusinessID:  businessID,
		PeriodStart: ledgermath.PgDate(start),
		PeriodEnd:   ledgermath.PgDate(end),
	})
	if err != nil {
		return decimal.Zero, err
	}

	total := decimal.Zero
	for _, r := range rows {
		if r.AccountTypeID != revenueTypeID && r.AccountTypeID != expensesTypeID {
			continue
		}
		debit, err := ledgermath.NumericToDecimal(r.TotalDebit)
		if err != nil {
			return decimal.Zero, err
		}
		credit, err := ledgermath.NumericToDecimal(r.TotalCredit)
		if err != nil {
			return decimal.Zero, err
		}
		// Net income = revenue - expenses. REVENUE's normal_balance is
		// CREDIT, so its net-income contribution is credit - debit;
		// EXPENSES's is DEBIT, and an expense must SUBTRACT from net
		// income, which is exactly credit - debit for a DEBIT-normal
		// account too (a debit posting is negative here). So summing
		// credit - debit across both account types in one pass gives net
		// income directly.
		total = total.Add(credit.Sub(debit))
	}
	return total, nil
}
