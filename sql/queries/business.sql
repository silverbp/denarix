-- Copyright (c) 2025 Silver Blueprints LLC
-- SPDX-License-Identifier: MIT

-- name: CreateBusiness :one
INSERT INTO business (
    name, tax_id, address_line1, address_line2, city, state, postal_code,
    country, phone, email, created_by_user_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
)
RETURNING *;

-- name: GetBusiness :one
SELECT * FROM business WHERE id = $1 AND deleted_at IS NULL;

-- name: UpdateBusiness :one
UPDATE business SET
    name = COALESCE(sqlc.narg('name'), name),
    tax_id = COALESCE(sqlc.narg('tax_id'), tax_id),
    address_line1 = COALESCE(sqlc.narg('address_line1'), address_line1),
    address_line2 = COALESCE(sqlc.narg('address_line2'), address_line2),
    city = COALESCE(sqlc.narg('city'), city),
    state = COALESCE(sqlc.narg('state'), state),
    postal_code = COALESCE(sqlc.narg('postal_code'), postal_code),
    country = COALESCE(sqlc.narg('country'), country),
    phone = COALESCE(sqlc.narg('phone'), phone),
    email = COALESCE(sqlc.narg('email'), email),
    updated_at = NOW()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
    AND (sqlc.narg('resource_version')::bigint IS NULL OR resource_version = sqlc.narg('resource_version'))
RETURNING *;

-- name: DeactivateBusiness :one
UPDATE business SET is_active = FALSE, updated_at = NOW()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
    AND (sqlc.narg('resource_version')::bigint IS NULL OR resource_version = sqlc.narg('resource_version'))
RETURNING *;

-- name: ListBusinessesForUser :many
SELECT b.*, bu.role AS membership_role
FROM business b
JOIN business_user bu ON bu.business_id = b.id
WHERE bu.user_id = $1 AND b.deleted_at IS NULL
ORDER BY b.name;

-- name: ConsumeNextEstimateNumber :one
-- Atomically claims and increments business.next_estimate_number, returning
-- the prefix + the number just claimed (the pre-increment value).
UPDATE business SET next_estimate_number = next_estimate_number + 1, updated_at = NOW()
WHERE id = $1
RETURNING estimate_number_prefix AS prefix, (next_estimate_number - 1)::int AS claimed_number;

-- name: ConsumeNextInvoiceNumber :one
UPDATE business SET next_invoice_number = next_invoice_number + 1, updated_at = NOW()
WHERE id = $1
RETURNING invoice_number_prefix AS prefix, (next_invoice_number - 1)::int AS claimed_number;

-- The four queries below persist a business's system ledger_account ids the first time each
-- is provisioned or self-healed (see internal/server/system_accounts.go,
-- internal/periodclose/provision.go) - guarded by "column IS NULL" so a later call is a true
-- no-op (0 rows, no resource_version bump) once set, never overwriting an already-resolved id.

-- name: SetBusinessARAccountID :exec
UPDATE business SET ar_account_id = $2, updated_at = NOW() WHERE id = $1 AND ar_account_id IS NULL;

-- name: SetBusinessAPAccountID :exec
UPDATE business SET ap_account_id = $2, updated_at = NOW() WHERE id = $1 AND ap_account_id IS NULL;

-- name: SetBusinessIncomeSummaryAccountID :exec
UPDATE business SET income_summary_account_id = $2, updated_at = NOW() WHERE id = $1 AND income_summary_account_id IS NULL;

-- name: SetBusinessRetainedEarningsAccountID :exec
UPDATE business SET retained_earnings_account_id = $2, updated_at = NOW() WHERE id = $1 AND retained_earnings_account_id IS NULL;
