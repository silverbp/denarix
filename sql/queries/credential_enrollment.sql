-- Copyright (c) 2025 Silver Blueprints LLC
-- SPDX-License-Identifier: MIT

-- name: CreateCredentialEnrollment :one
-- Upsert on the one-live-token-per-user rule (credential_enrollment_user_pending_uindex):
-- issuing a fresh enrollment for a user who already has a pending one invalidates the old one
-- by replacing its hash, so only the newest token ever works.
INSERT INTO credential_enrollment (user_id, token_hash, revoke_existing, purpose, created_by_user_id, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (user_id) WHERE consumed_at IS NULL
DO UPDATE SET
    token_hash = EXCLUDED.token_hash,
    revoke_existing = EXCLUDED.revoke_existing,
    purpose = EXCLUDED.purpose,
    created_by_user_id = EXCLUDED.created_by_user_id,
    created_at = NOW(),
    expires_at = EXCLUDED.expires_at
RETURNING *;

-- name: GetPendingCredentialEnrollmentByTokenHash :one
SELECT * FROM credential_enrollment
WHERE token_hash = $1 AND consumed_at IS NULL AND expires_at > NOW();

-- name: GetPendingCredentialEnrollmentForUser :one
SELECT * FROM credential_enrollment
WHERE user_id = $1 AND consumed_at IS NULL AND expires_at > NOW();

-- name: ConsumeCredentialEnrollment :exec
UPDATE credential_enrollment SET consumed_at = NOW()
WHERE id = $1 AND consumed_at IS NULL;
