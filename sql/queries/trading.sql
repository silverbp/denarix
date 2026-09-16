-- Copyright (c) 2025 Silver Blueprints LLC
-- SPDX-License-Identifier: MIT

-- name: CreateEstimate :one
INSERT INTO estimate (
    business_id, customer_id, estimate_number, estimate_date, expiration_date,
    subtotal, total_tax_amount, total_amount, notes, terms, created_by_user_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
)
RETURNING *;

-- name: CreateEstimateLineItem :one
INSERT INTO estimate_line_item (
    estimate_id, item_id, line_number, description, quantity, unit_price,
    line_subtotal, is_taxable, tax_rate_id, tax_rate, tax_amount, line_total
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
)
RETURNING *;

-- name: GetEstimate :one
SELECT * FROM estimate WHERE id = $1 AND deleted_at IS NULL;

-- name: ListEstimateLineItems :many
SELECT * FROM estimate_line_item WHERE estimate_id = $1 AND deleted_at IS NULL ORDER BY line_number;

-- name: ListEstimateLineItemsByEstimateIDs :many
-- Batch form for list handlers: every line of every estimate in one round trip.
SELECT * FROM estimate_line_item
WHERE estimate_id = ANY(sqlc.arg('estimate_ids')::bigint[]) AND deleted_at IS NULL
ORDER BY estimate_id, line_number;

-- name: ListEstimates :many
SELECT * FROM estimate
WHERE business_id = sqlc.arg('business_id') AND deleted_at IS NULL
    AND (sqlc.arg('include_all')::bool OR status IN ('DRAFT', 'SENT'))
ORDER BY estimate_date DESC;

-- name: UpdateEstimateStatus :one
UPDATE estimate SET status = sqlc.arg('status'), updated_at = NOW()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
    AND (sqlc.narg('resource_version')::bigint IS NULL OR resource_version = sqlc.narg('resource_version'))
RETURNING *;

-- name: UpdateEstimateHeader :one
-- Header fields only - see EstimateService.UpdateEstimate for what's
-- deliberately excluded (estimate_date, customer_id, estimate_number).
UPDATE estimate SET
    notes = COALESCE(sqlc.narg('notes'), notes),
    terms = COALESCE(sqlc.narg('terms'), terms),
    expiration_date = COALESCE(sqlc.narg('expiration_date'), expiration_date),
    updated_at = NOW()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
    AND (sqlc.narg('resource_version')::bigint IS NULL OR resource_version = sqlc.narg('resource_version'))
RETURNING *;

-- name: DeleteEstimateLineItems :exec
UPDATE estimate_line_item SET deleted_at = NOW()
WHERE estimate_id = $1 AND deleted_at IS NULL;

-- name: UpdateEstimateTotals :one
-- Carries the resource_version precondition for UpdateEstimateLineItems as a whole: it's
-- the first statement in that transaction to touch the estimate row, so it both checks
-- the version and takes the row lock everything after it (line-item replace) runs under.
UPDATE estimate SET
    subtotal = sqlc.arg('subtotal'), total_tax_amount = sqlc.arg('total_tax_amount'),
    total_amount = sqlc.arg('total_amount'), updated_at = NOW()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
    AND (sqlc.narg('resource_version')::bigint IS NULL OR resource_version = sqlc.narg('resource_version'))
RETURNING *;

-- name: CreateInvoice :one
INSERT INTO invoice (
    business_id, contact_id, invoice_type, estimate_id, invoice_number, invoice_date,
    due_date, subtotal, total_tax_amount, total_amount, balance_due, notes, terms,
    created_by_user_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
)
RETURNING *;

-- name: CreateInvoiceLineItem :one
INSERT INTO invoice_line_item (
    invoice_id, item_id, ledger_account_id, line_number, description, quantity,
    unit_price, line_subtotal, is_taxable, tax_rate_id, tax_rate, tax_amount, line_total
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
)
RETURNING *;

-- name: GetInvoice :one
SELECT * FROM invoice WHERE id = $1 AND deleted_at IS NULL;

-- name: ListInvoiceLineItems :many
SELECT * FROM invoice_line_item WHERE invoice_id = $1 AND deleted_at IS NULL ORDER BY line_number;

-- name: ListInvoiceLineItemsByInvoiceIDs :many
-- Batch form for list handlers: every line of every invoice in one round trip.
SELECT * FROM invoice_line_item
WHERE invoice_id = ANY(sqlc.arg('invoice_ids')::bigint[]) AND deleted_at IS NULL
ORDER BY invoice_id, line_number;

-- name: ListInvoices :many
SELECT * FROM invoice
WHERE business_id = sqlc.arg('business_id') AND deleted_at IS NULL
    AND (sqlc.arg('include_all')::bool OR status NOT IN ('PAID', 'CANCELLED'))
ORDER BY invoice_date DESC;

-- name: UpdateInvoiceStatus :one
-- from_status is the status the caller validated the transition against; the
-- predicate makes the transition atomic so two concurrent callers can't both
-- pass the check and both act on it (e.g. both post a cancellation reversal).
-- Zero rows means the row is gone, its version moved, or its status moved.
UPDATE invoice SET status = sqlc.arg('status'), updated_at = NOW()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
    AND status = sqlc.arg('from_status')
    AND (sqlc.narg('resource_version')::bigint IS NULL OR resource_version = sqlc.narg('resource_version'))
RETURNING *;

-- name: UpdateInvoiceHeader :one
-- Fields with no ledger impact only - see InvoiceService.UpdateInvoice for
-- what's deliberately excluded (invoice_date, contact_id, invoice_number)
-- and why.
UPDATE invoice SET
    notes = COALESCE(sqlc.narg('notes'), notes),
    terms = COALESCE(sqlc.narg('terms'), terms),
    due_date = COALESCE(sqlc.narg('due_date'), due_date),
    updated_at = NOW()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
    AND (sqlc.narg('resource_version')::bigint IS NULL OR resource_version = sqlc.narg('resource_version'))
RETURNING *;

-- name: DeleteInvoiceLineItems :exec
UPDATE invoice_line_item SET deleted_at = NOW()
WHERE invoice_id = $1 AND deleted_at IS NULL;

-- name: UpdateInvoiceTotals :one
-- Same role as UpdateEstimateTotals: the version check + row lock for UpdateInvoiceLineItems.
-- SetInvoiceLedgerTransaction/ApplyPaymentToInvoice below stay unconditional - they run
-- either later in this same transaction (lock already held) or as a side effect of another
-- resource's write (a payment), where there's no client-held invoice version to check.
UPDATE invoice SET
    subtotal = sqlc.arg('subtotal'), total_tax_amount = sqlc.arg('total_tax_amount'),
    total_amount = sqlc.arg('total_amount'), balance_due = sqlc.arg('balance_due'), updated_at = NOW()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
    AND (sqlc.narg('resource_version')::bigint IS NULL OR resource_version = sqlc.narg('resource_version'))
RETURNING *;

-- name: SetInvoiceLedgerTransaction :one
UPDATE invoice SET ledger_transaction_id = $2, updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: ApplyPaymentToInvoice :one
-- The `balance_due >= amount` guard makes over-application impossible even
-- under concurrent CreatePayment calls against the same invoice - a
-- pre-check in the server can give a friendlier error, but this is what
-- actually enforces it: :one on zero rows fails with pgx.ErrNoRows, which
-- the caller treats as "would over-apply / no longer payable", not "not
-- found" (the caller already holds the row from an earlier GetInvoice in
-- the same request). Only a SENT or OVERDUE invoice is payable: a DRAFT
-- must be sent first (so UnapplyPaymentFromInvoice can always restore SENT),
-- and a CANCELLED one has nothing to pay.
UPDATE invoice SET
    paid_amount = paid_amount + sqlc.arg('amount'),
    balance_due = balance_due - sqlc.arg('amount'),
    status = CASE WHEN balance_due - sqlc.arg('amount') <= 0 THEN 'PAID' ELSE status END,
    updated_at = NOW()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
    AND status IN ('SENT', 'OVERDUE', 'PAID')
    AND balance_due >= sqlc.arg('amount')
RETURNING *;

-- name: CreatePayment :one
INSERT INTO payment (
    business_id, contact_id, payment_type, payment_number, payment_date,
    amount, payment_method, ledger_account_id, reference_number, notes, created_by_user_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
)
RETURNING *;

-- name: GetPayment :one
SELECT * FROM payment WHERE id = $1 AND deleted_at IS NULL;

-- name: ListPayments :many
SELECT * FROM payment WHERE business_id = $1 AND deleted_at IS NULL ORDER BY payment_date DESC;

-- name: SetPaymentLedgerTransaction :one
UPDATE payment SET ledger_transaction_id = $2, updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: CreatePaymentApplication :one
INSERT INTO payment_application (payment_id, invoice_id, applied_amount)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListPaymentApplicationsForPayment :many
SELECT * FROM payment_application WHERE payment_id = $1 ORDER BY id;

-- name: ListPaymentApplicationsByPaymentIDs :many
-- Batch form for list handlers.
SELECT * FROM payment_application
WHERE payment_id = ANY(sqlc.arg('payment_ids')::bigint[])
ORDER BY payment_id, id;

-- name: CountPaymentApplicationsForInvoice :one
-- Used by InvoiceService.UpdateInvoiceStatus to refuse cancelling an
-- invoice that still has a payment applied - void the payment(s) first, so
-- the payment side (paid_amount/balance_due/payment_application) never
-- drifts from a cancelled invoice's ledger reversal.
SELECT COUNT(*) FROM payment_application WHERE invoice_id = $1;

-- name: ListInvoicesForContact :many
SELECT * FROM invoice
WHERE contact_id = sqlc.arg('contact_id') AND deleted_at IS NULL
    AND invoice_date BETWEEN sqlc.arg('period_start') AND sqlc.arg('period_end')
ORDER BY invoice_date;

-- name: ListPaymentsForContact :many
SELECT * FROM payment
WHERE contact_id = sqlc.arg('contact_id') AND deleted_at IS NULL
    AND payment_date BETWEEN sqlc.arg('period_start') AND sqlc.arg('period_end')
ORDER BY payment_date;

-- name: CountDocumentsForLedgerTransaction :one
-- Whether any invoice or payment references this ledger transaction -
-- LedgerTransactionService.ReverseLedgerTransaction refuses a direct
-- reverse when this is nonzero, since correcting a document-linked
-- transaction through the document (invoice cancel / payment void) is what
-- keeps paid_amount/balance_due/payment_application in sync; a raw reverse
-- would fix the GL while leaving those stale. Deliberately no deleted_at
-- filter: a voided payment's original posting is still a document posting
-- (and already reversed by the void), never a raw one.
SELECT
    (SELECT COUNT(*) FROM invoice WHERE ledger_transaction_id = sqlc.arg('ledger_transaction_id')::bigint)
    + (SELECT COUNT(*) FROM payment WHERE ledger_transaction_id = sqlc.arg('ledger_transaction_id')::bigint) AS document_count;

-- name: UnapplyPaymentFromInvoice :one
-- Inverse of ApplyPaymentToInvoice: restores paid_amount/balance_due, and
-- moves a PAID invoice back to SENT since ApplyPaymentToInvoice's CASE only
-- ever sets PAID and can't be run backwards. Any other status is left alone
-- (a partial payment never changed it). OVERDUE is deliberately not derived
-- here from due_date - it's only ever set explicitly via UpdateInvoiceStatus
-- (`invoice mark-overdue`), and this query mustn't be the one place that
-- disagrees.
UPDATE invoice SET
    paid_amount = paid_amount - sqlc.arg('amount'),
    balance_due = balance_due + sqlc.arg('amount'),
    status = CASE WHEN status = 'PAID' THEN 'SENT' ELSE status END,
    updated_at = NOW()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- name: DeletePaymentApplicationsForPayment :exec
DELETE FROM payment_application WHERE payment_id = $1;

-- name: SoftDeletePayment :one
UPDATE payment SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;
