-- Copyright (c) 2025 Silver Blueprints LLC
-- SPDX-License-Identifier: MIT

-- name: ListLedgerAccountTypes :many
SELECT * FROM ledger_account_type ORDER BY id;

-- name: ListCashFlowCategories :many
SELECT * FROM cash_flow_category ORDER BY display_sequence;

-- name: ListBalanceSheetCategories :many
SELECT * FROM balance_sheet_category ORDER BY display_sequence;

-- name: ListIncomeStatementCategories :many
SELECT * FROM income_statement_category ORDER BY display_sequence;
