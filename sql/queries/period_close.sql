-- Copyright (c) 2025 Silver Blueprints LLC
-- SPDX-License-Identifier: MIT

-- name: CreatePeriodClose :one
INSERT INTO period_close (
    business_id, period_start, period_end, income_summary_account_id,
    retained_earnings_account_id, created_by_user_id
) VALUES (
    $1, $2, $3, $4, $5, $6
)
RETURNING *;

-- name: CreatePeriodCloseEntry :one
INSERT INTO period_close_entry (period_close_id, ledger_transaction_id, source_account_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetPeriodClose :one
SELECT * FROM period_close WHERE id = $1;

-- name: ListPeriodCloses :many
SELECT * FROM period_close WHERE business_id = $1 ORDER BY period_end DESC;

-- name: ListPeriodCloseEntries :many
SELECT * FROM period_close_entry WHERE period_close_id = $1 ORDER BY id;

-- name: ListPeriodCloseEntriesByCloseIDs :many
-- Batch form for list handlers.
SELECT * FROM period_close_entry
WHERE period_close_id = ANY(sqlc.arg('period_close_ids')::bigint[])
ORDER BY period_close_id, id;

-- name: ReversePeriodClose :one
UPDATE period_close SET reversed_at = NOW()
WHERE id = $1 AND reversed_at IS NULL
RETURNING *;
