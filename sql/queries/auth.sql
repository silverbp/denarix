-- Copyright (c) 2025 Silver Blueprints LLC
-- SPDX-License-Identifier: MIT

-- name: GetAppUser :one
SELECT * FROM app_user WHERE id = $1 AND deleted_at IS NULL;

-- name: GetAppUserByEmail :one
SELECT * FROM app_user WHERE email = $1 AND deleted_at IS NULL;

-- name: CreateAppUser :one
INSERT INTO app_user (email, display_name)
VALUES ($1, $2)
RETURNING *;

-- name: ClearGlobalAdmin :exec
-- Only ever one row (the single-admin index), but not scoped to a specific id - called before
-- granting a new admin, to transfer rather than add a second one. See auth.SetGlobalAdmin.
UPDATE app_user SET is_global_admin = FALSE, updated_at = NOW() WHERE is_global_admin = TRUE;

-- name: SetGlobalAdmin :one
UPDATE app_user SET is_global_admin = $2, updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: GetGlobalAdmin :one
SELECT * FROM app_user WHERE is_global_admin = TRUE AND deleted_at IS NULL;

-- name: GetBusinessUser :one
SELECT * FROM business_user WHERE business_id = $1 AND user_id = $2;

-- name: CreateBusinessUser :one
INSERT INTO business_user (business_id, user_id, role)
VALUES ($1, $2, $3)
RETURNING *;

-- name: CreateUserSession :one
INSERT INTO user_session (user_id, refresh_token_hash, client_name, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetUserSessionByHash :one
SELECT * FROM user_session
WHERE refresh_token_hash = $1 AND revoked_at IS NULL AND expires_at > NOW();

-- name: RevokeUserSession :one
UPDATE user_session SET revoked_at = NOW(), replaced_by_session_id = sqlc.narg('replaced_by_session_id')
WHERE id = sqlc.arg('id') AND revoked_at IS NULL
RETURNING *;

-- name: CreateWebAuthnCredential :one
INSERT INTO webauthn_credential (
    user_id, credential_id, public_key, attestation_type, transports,
    aaguid, sign_count, backup_eligible, backup_state, name
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
)
RETURNING *;

-- name: ListWebAuthnCredentialsForUser :many
SELECT * FROM webauthn_credential WHERE user_id = $1 ORDER BY id;

-- name: GetWebAuthnCredentialByCredentialID :one
SELECT * FROM webauthn_credential WHERE credential_id = $1;

-- name: DeleteWebAuthnCredentialsForUser :exec
-- Hard-removes every passkey on an account — used when a credential_enrollment token with
-- revoke_existing is redeemed (a "reset"), so a lost/compromised device can no longer sign in.
DELETE FROM webauthn_credential WHERE user_id = $1;

-- name: UpdateWebAuthnCredentialAfterLogin :exec
-- sign_count backs clone-detection; backup_state can legitimately change post-registration
-- (e.g. a passkey newly synced to iCloud Keychain) and go-webauthn's docs call out that it MUST
-- be written back whenever it changes, so both are refreshed together after every login.
UPDATE webauthn_credential SET sign_count = $2, backup_state = $3, last_used_at = NOW() WHERE id = $1;
