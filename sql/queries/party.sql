-- Copyright (c) 2025 Silver Blueprints LLC
-- SPDX-License-Identifier: MIT

-- name: CreateContact :one
INSERT INTO contact (
    business_id, contact_number, name,
    email, phone, payment_terms_days, credit_limit, created_by_user_id,
    billing_address_line1, billing_address_line2, billing_city, billing_state,
    billing_postal_code, billing_country
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
)
RETURNING *;

-- name: GetContact :one
SELECT * FROM contact WHERE id = $1 AND deleted_at IS NULL;

-- name: ListContacts :many
SELECT * FROM contact
WHERE business_id = sqlc.arg('business_id') AND deleted_at IS NULL
    AND (is_active OR sqlc.arg('include_inactive')::bool)
ORDER BY name;

-- name: UpdateContact :one
UPDATE contact SET
    name = COALESCE(sqlc.narg('name'), name),
    email = COALESCE(sqlc.narg('email'), email),
    phone = COALESCE(sqlc.narg('phone'), phone),
    payment_terms_days = COALESCE(sqlc.narg('payment_terms_days'), payment_terms_days),
    credit_limit = COALESCE(sqlc.narg('credit_limit'), credit_limit),
    billing_address_line1 = COALESCE(sqlc.narg('billing_address_line1'), billing_address_line1),
    billing_address_line2 = COALESCE(sqlc.narg('billing_address_line2'), billing_address_line2),
    billing_city = COALESCE(sqlc.narg('billing_city'), billing_city),
    billing_state = COALESCE(sqlc.narg('billing_state'), billing_state),
    billing_postal_code = COALESCE(sqlc.narg('billing_postal_code'), billing_postal_code),
    billing_country = COALESCE(sqlc.narg('billing_country'), billing_country),
    updated_at = NOW()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
    AND (sqlc.narg('resource_version')::bigint IS NULL OR resource_version = sqlc.narg('resource_version'))
RETURNING *;

-- name: DeactivateContact :one
UPDATE contact SET is_active = FALSE, updated_at = NOW()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
    AND (sqlc.narg('resource_version')::bigint IS NULL OR resource_version = sqlc.narg('resource_version'))
RETURNING *;

-- name: CreateCustomer :one
INSERT INTO customer (contact_id, ledger_account_id) VALUES ($1, $2)
RETURNING *;

-- name: GetCustomerByContactID :one
SELECT * FROM customer WHERE contact_id = $1;

-- name: CreateVendor :one
INSERT INTO vendor (contact_id, ledger_account_id) VALUES ($1, $2)
RETURNING *;

-- name: GetVendorByContactID :one
SELECT * FROM vendor WHERE contact_id = $1;

-- name: ListCustomersByContactIDs :many
-- Batch form for contact list handlers.
SELECT * FROM customer WHERE contact_id = ANY(sqlc.arg('contact_ids')::bigint[]);

-- name: ListVendorsByContactIDs :many
SELECT * FROM vendor WHERE contact_id = ANY(sqlc.arg('contact_ids')::bigint[]);

-- name: CreateItem :one
INSERT INTO item (
    business_id, item_code, item_type, name, description, unit_of_measure, cost_price,
    retail_price, is_taxable, default_tax_rate_id, default_ledger_account_id, created_by_user_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
)
RETURNING *;

-- name: GetItem :one
SELECT * FROM item WHERE id = $1 AND deleted_at IS NULL;

-- name: ListItems :many
SELECT * FROM item
WHERE business_id = sqlc.arg('business_id') AND deleted_at IS NULL
    AND (is_active OR sqlc.arg('include_inactive')::bool)
ORDER BY item_code;

-- name: UpdateItem :one
UPDATE item SET
    item_type = COALESCE(sqlc.narg('item_type'), item_type),
    name = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    retail_price = COALESCE(sqlc.narg('retail_price'), retail_price),
    cost_price = COALESCE(sqlc.narg('cost_price'), cost_price),
    is_taxable = COALESCE(sqlc.narg('is_taxable'), is_taxable),
    default_tax_rate_id = COALESCE(sqlc.narg('default_tax_rate_id'), default_tax_rate_id),
    default_ledger_account_id = COALESCE(sqlc.narg('default_ledger_account_id'), default_ledger_account_id),
    updated_at = NOW()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
    AND (sqlc.narg('resource_version')::bigint IS NULL OR resource_version = sqlc.narg('resource_version'))
RETURNING *;

-- name: DeactivateItem :one
UPDATE item SET is_active = FALSE, updated_at = NOW()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
    AND (sqlc.narg('resource_version')::bigint IS NULL OR resource_version = sqlc.narg('resource_version'))
RETURNING *;

-- name: CreateTaxRate :one
INSERT INTO tax_rate (
    business_id, name, rate, tax_liability_account_id, created_by_user_id
) VALUES (
    $1, $2, $3, $4, $5
)
RETURNING *;

-- name: GetTaxRate :one
SELECT * FROM tax_rate WHERE id = $1;

-- name: ListTaxRates :many
SELECT * FROM tax_rate WHERE business_id = $1 ORDER BY name;

-- name: UpdateTaxRate :one
UPDATE tax_rate SET
    name = COALESCE(sqlc.narg('name'), name),
    rate = COALESCE(sqlc.narg('rate'), rate),
    updated_at = NOW()
WHERE id = sqlc.arg('id')
    AND (sqlc.narg('resource_version')::bigint IS NULL OR resource_version = sqlc.narg('resource_version'))
RETURNING *;

-- name: DeactivateTaxRate :one
UPDATE tax_rate SET is_active = FALSE, updated_at = NOW()
WHERE id = sqlc.arg('id')
    AND (sqlc.narg('resource_version')::bigint IS NULL OR resource_version = sqlc.narg('resource_version'))
RETURNING *;
