-- Copyright (c) 2025 Silver Blueprints LLC
-- SPDX-License-Identifier: MIT

-- name: AccountBalancesAsOf :many
-- One row per ledger_account, with total debit/credit activity from
-- ledger_entry within [period_start, period_end]. Used, with different
-- date ranges, for trial balance and balance sheet ("as of": period_start
-- = business inception) and income statement (an arbitrary range).
--
-- Both joins are LEFT and date-unconditional (so every account survives
-- even with zero activity); the date range is applied inside the SUM's
-- CASE instead of in a JOIN...ON clause. Filtering by date in a LEFT JOIN's
-- ON clause does NOT exclude out-of-range ledger_entry rows from the sum -
-- it only nulls out the unmatched ledger_transaction columns, while
-- ledger_entry's own debit/credit columns (already bound by the prior
-- join) stay non-null and still get summed.
SELECT
    la.id AS account_id,
    la.code,
    la.name,
    la.account_type_id,
    la.parent_account_id,
    la.is_container,
    la.balance_sheet_category_id,
    la.income_statement_category_id,
    lat.normal_balance,
    COALESCE(SUM(CASE WHEN lt.transaction_date BETWEEN sqlc.arg('period_start') AND sqlc.arg('period_end') THEN le.debit_amount ELSE 0 END), 0)::numeric AS total_debit,
    COALESCE(SUM(CASE WHEN lt.transaction_date BETWEEN sqlc.arg('period_start') AND sqlc.arg('period_end') THEN le.credit_amount ELSE 0 END), 0)::numeric AS total_credit
FROM ledger_account la
JOIN ledger_account_type lat ON lat.id = la.account_type_id
LEFT JOIN ledger_entry le ON le.account_id = la.id AND le.deleted_at IS NULL
LEFT JOIN ledger_transaction lt ON lt.id = le.ledger_transaction_id AND lt.deleted_at IS NULL
WHERE la.business_id = sqlc.arg('business_id')
GROUP BY la.id, la.code, la.name, la.account_type_id, la.parent_account_id, la.is_container, la.balance_sheet_category_id, la.income_statement_category_id, lat.normal_balance
ORDER BY la.code;

-- name: GetLastPeriodClose :one
SELECT * FROM period_close
WHERE business_id = $1 AND reversed_at IS NULL
ORDER BY period_end DESC
LIMIT 1;

-- name: ListLedgerEntriesForAccount :many
SELECT
    le.*,
    lt.transaction_date,
    lt.description AS transaction_description,
    lt.reference_number
FROM ledger_entry le
JOIN ledger_transaction lt ON lt.id = le.ledger_transaction_id AND lt.deleted_at IS NULL
WHERE le.account_id = sqlc.arg('account_id')
    AND le.deleted_at IS NULL
    AND lt.transaction_date BETWEEN sqlc.arg('period_start') AND sqlc.arg('period_end')
ORDER BY lt.transaction_date, le.id;
