-- Copyright (c) 2025 Silver Blueprints LLC
-- SPDX-License-Identifier: MIT

-- name: CreateBusinessInvite :one
INSERT INTO business_invite (business_id, email, role, token_hash, invited_by_user_id, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetBusinessInvite :one
SELECT * FROM business_invite WHERE id = $1;

-- name: GetPendingBusinessInviteByTokenHash :one
SELECT * FROM business_invite
WHERE token_hash = $1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > NOW();

-- name: ListBusinessInvitesForBusiness :many
SELECT * FROM business_invite WHERE business_id = $1 ORDER BY created_at DESC;

-- name: AcceptBusinessInvite :one
UPDATE business_invite SET accepted_at = NOW(), accepted_by_user_id = $2
WHERE id = $1
RETURNING *;

-- name: RevokeBusinessInvite :one
UPDATE business_invite SET revoked_at = NOW()
WHERE id = $1 AND accepted_at IS NULL AND revoked_at IS NULL
RETURNING *;
