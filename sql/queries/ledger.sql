-- Copyright (c) 2025 Silver Blueprints LLC
-- SPDX-License-Identifier: MIT

-- name: CreateLedgerAccount :one
INSERT INTO ledger_account (
    business_id, account_type_id, parent_account_id, code, name, description,
    is_system, is_reconcilable, is_container, cash_flow_category_id,
    balance_sheet_category_id, income_statement_category_id, created_by_user_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
)
RETURNING *;

-- name: GetLedgerAccount :one
SELECT * FROM ledger_account WHERE id = $1;

-- name: GetLedgerAccountByCode :one
SELECT * FROM ledger_account WHERE business_id = $1 AND code = $2;

-- name: ListLedgerAccounts :many
SELECT * FROM ledger_account WHERE business_id = $1 ORDER BY code;

-- name: UpdateLedgerAccount :one
UPDATE ledger_account SET
    name = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    is_reconcilable = COALESCE(sqlc.narg('is_reconcilable'), is_reconcilable),
    is_container = COALESCE(sqlc.narg('is_container'), is_container),
    cash_flow_category_id = COALESCE(sqlc.narg('cash_flow_category_id'), cash_flow_category_id),
    balance_sheet_category_id = COALESCE(sqlc.narg('balance_sheet_category_id'), balance_sheet_category_id),
    income_statement_category_id = COALESCE(sqlc.narg('income_statement_category_id'), income_statement_category_id),
    updated_at = NOW()
WHERE id = sqlc.arg('id')
    AND (sqlc.narg('resource_version')::bigint IS NULL OR resource_version = sqlc.narg('resource_version'))
RETURNING *;

-- name: DeactivateLedgerAccount :one
UPDATE ledger_account SET is_active = FALSE, updated_at = NOW()
WHERE id = sqlc.arg('id')
    AND (sqlc.narg('resource_version')::bigint IS NULL OR resource_version = sqlc.narg('resource_version'))
RETURNING *;

-- name: CreateLedgerTransaction :one
INSERT INTO ledger_transaction (
    business_id, transaction_date, description, reference_number, created_by_user_id,
    reverses_ledger_transaction_id
) VALUES (
    $1, $2, $3, $4, $5, sqlc.narg('reverses_ledger_transaction_id')
)
RETURNING *;

-- name: GetReversalOfLedgerTransaction :one
-- The transaction that reverses $1, if one has been posted. The partial
-- unique index on reverses_ledger_transaction_id guarantees at most one.
SELECT * FROM ledger_transaction
WHERE reverses_ledger_transaction_id = sqlc.arg('original_id')::bigint AND deleted_at IS NULL;

-- name: GetLedgerTransaction :one
SELECT * FROM ledger_transaction WHERE id = $1 AND deleted_at IS NULL;

-- name: ListLedgerTransactions :many
-- Keyset-paged on (transaction_date, id) DESC — a compound cursor, not id
-- alone, so pagination reflects the chronological order clients want to
-- show rather than posting order. (transaction_date, id) is unique per
-- row, which is what keyset pagination needs. Also takes optional
-- AND-combined filters: a date range on transaction_date, a
-- case-insensitive substring of the transaction description, and "has a
-- live entry against this account".
SELECT lt.* FROM ledger_transaction lt
WHERE lt.business_id = $1 AND lt.deleted_at IS NULL
  AND (lt.transaction_date, lt.id) < (sqlc.arg('before_date')::date, sqlc.arg('before_id')::bigint)
  AND (sqlc.narg('start_date')::date IS NULL OR lt.transaction_date >= sqlc.narg('start_date'))
  AND (sqlc.narg('end_date')::date IS NULL OR lt.transaction_date <= sqlc.narg('end_date'))
  AND (sqlc.narg('description_contains')::text IS NULL
       OR lt.description ILIKE '%' || sqlc.narg('description_contains') || '%')
  AND (sqlc.narg('account_id')::int IS NULL OR EXISTS (
        SELECT 1 FROM ledger_entry le
        WHERE le.ledger_transaction_id = lt.id AND le.account_id = sqlc.narg('account_id')
          AND le.deleted_at IS NULL))
ORDER BY lt.transaction_date DESC, lt.id DESC
LIMIT sqlc.arg('page_limit');

-- name: CreateLedgerEntry :one
INSERT INTO ledger_entry (
    business_id, ledger_transaction_id, account_id, debit_amount, credit_amount, description
) VALUES (
    $1, $2, $3, $4, $5, $6
)
RETURNING *;

-- name: ListLedgerEntriesByTransaction :many
SELECT * FROM ledger_entry WHERE ledger_transaction_id = $1 AND deleted_at IS NULL ORDER BY id;

-- name: ListLedgerEntriesByTransactionIDs :many
SELECT * FROM ledger_entry WHERE ledger_transaction_id = ANY(sqlc.arg('transaction_ids')::bigint[]) AND deleted_at IS NULL ORDER BY id;

-- name: SoftDeleteLedgerEntriesByTransaction :exec
UPDATE ledger_entry SET deleted_at = NOW()
WHERE ledger_transaction_id = $1 AND deleted_at IS NULL;

-- name: GetLedgerAccountType :one
SELECT * FROM ledger_account_type WHERE id = $1;
