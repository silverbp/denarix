// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

// Package reporting computes the financial reports over the ledger — trial
// balance, balance sheet, income statement, general ledger — as plain Go
// (native time.Time/decimal.Decimal, no proto dependency), so it's directly
// unit-testable against a real Postgres, matching internal/periodclose.
package reporting

import (
	"time"

	"github.com/shopspring/decimal"
)

// AccountLine is one row of a report keyed to a single ledger_account
// (Amount's sign convention depends on the report: a balance-sheet net
// balance, a revenue/expense total, ...).
type AccountLine struct {
	AccountID int32
	Code      string
	Name      string
	Amount    decimal.Decimal
}

type TrialBalanceLine struct {
	AccountID int32
	Code      string
	Name      string
	Debit     decimal.Decimal
	Credit    decimal.Decimal
}

type TrialBalanceResult struct {
	Lines       []TrialBalanceLine
	TotalDebit  decimal.Decimal
	TotalCredit decimal.Decimal
}

// BalanceSheetSection groups ledger_account balances into one
// balance_sheet_category ("Long-term Assets", "Current Assets &
// Liabilities", "Long-term Liabilities", "Capital & Reserves"). AssetLines
// and LiabilityLines are which column a line prints in (its
// ledger_account_type, not its category) - a section can populate both,
// e.g. "Current Assets & Liabilities" mixing a bank account with a credit
// card balance, matching a classic UK-style statutory balance sheet.
type BalanceSheetSection struct {
	Title            string
	AssetLines       []AccountLine
	LiabilityLines   []AccountLine
	TotalAssets      decimal.Decimal
	TotalLiabilities decimal.Decimal
}

type BalanceSheetResult struct {
	Sections []BalanceSheetSection
	// NetCurrentAssets is TotalAssets - TotalLiabilities within the
	// "Current Assets & Liabilities" section only.
	NetCurrentAssets decimal.Decimal
	// TotalAssetsLessCurrentLiabilities is Long-term Assets' TotalAssets +
	// NetCurrentAssets.
	TotalAssetsLessCurrentLiabilities decimal.Decimal
	// TotalNetAssets is TotalAssetsLessCurrentLiabilities - Long-term
	// Liabilities' TotalLiabilities.
	TotalNetAssets decimal.Decimal
	// TotalAssets and TotalLiabilities are grand totals across every
	// section (TotalLiabilities includes Capital & Reserves) - the
	// double-entry identity that the two must be equal is the same check
	// the old flat assets/liabilities/equity shape made, now summed across
	// sections instead.
	TotalAssets      decimal.Decimal
	TotalLiabilities decimal.Decimal
}

// IncomeStatementResult is the standard US multi-step income statement shape: Revenue - Cost of
// Goods Sold = Gross Profit; Gross Profit - Operating Expenses = Net Income.
// CostOfGoodsSold/OperatingExpenses are split via ledger_account.income_statement_category_id -
// only EXPENSES-type accounts ever populate either list.
type IncomeStatementResult struct {
	Revenue                []AccountLine
	TotalRevenue           decimal.Decimal
	CostOfGoodsSold        []AccountLine
	TotalCostOfGoodsSold   decimal.Decimal
	GrossProfit            decimal.Decimal
	OperatingExpenses      []AccountLine
	TotalOperatingExpenses decimal.Decimal
	TotalExpenses          decimal.Decimal
	NetIncome              decimal.Decimal
}

type GeneralLedgerLine struct {
	LedgerTransactionID int64
	TransactionDate     time.Time
	Description         *string
	Debit               decimal.Decimal
	Credit              decimal.Decimal
	RunningBalance      decimal.Decimal
}

type GeneralLedgerResult struct {
	AccountID     int32
	Code          string
	Name          string
	Lines         []GeneralLedgerLine
	EndingBalance decimal.Decimal
}
