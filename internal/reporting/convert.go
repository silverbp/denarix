// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package reporting

// account_type_id values seeded by migrations/00001_initial.up.sql. Sign
// arithmetic (net balances, decimal/date conversion) lives in
// internal/ledgermath, shared with internal/periodclose.
const (
	assetsTypeID      = 1
	liabilitiesTypeID = 2
	equityTypeID      = 3
	revenueTypeID     = 4
	expensesTypeID    = 5
)

// balance_sheet_category_id values seeded by migrations/00001_initial.up.sql -
// the balance sheet report's section grouping (see BalanceSheet in
// balance_sheet.go).
const (
	longTermAssetsCategoryID      = 1
	currentCategoryID             = 2
	longTermLiabilitiesCategoryID = 3
	capitalAndReservesCategoryID  = 4
	openingBalancesCategoryID     = 5
)

// income_statement_category_id values seeded by migrations/00001_initial.up.sql - the income
// statement report's section grouping (see IncomeStatement in income_statement.go).
const (
	revenueCategoryID           = 1
	costOfGoodsSoldCategoryID   = 2
	operatingExpensesCategoryID = 3
)
