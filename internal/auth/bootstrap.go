// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/silverbp/denarix/internal/db"
	"github.com/silverbp/denarix/internal/db/sqlcgen"
)

// bootstrapEnrollmentTTL is short on purpose: the operator minting it is
// standing at the server console, so a token that expires quickly limits
// exposure. A lost token clears on its own within the hour, after which the
// next start mints a fresh one.
const bootstrapEnrollmentTTL = time.Hour

// BootstrapResult reports what EnsureBootstrapAdmin did. EnrollmentToken is
// the one-time token the admin uses to register their first (or, on a
// break-glass reset, replacement) passkey — see credential_enrollment and
// internal/auth/webauthn.go. It is empty when nothing was minted (an admin
// already exists and this was not a reset, or a pending token was already
// outstanding).
type BootstrapResult struct {
	User            *User
	EnrollmentToken string
	Reset           bool
}

// EnsureBootstrapAdmin get-or-creates the global admin identified by email and mints a one-time
// enrollment token so they can register a passkey — the very first admin has no inviter above
// them, so this is the only way they get in.
//
// Behaviour:
//
//   - No global admin yet (first run): grant email global-admin and mint a BOOTSTRAP enrollment
//     token. Once an admin exists this branch never fires again, even if the env var stays set —
//     a legitimate later transfer via UserService.SetGlobalAdmin sticks.
//   - An admin already exists and reset is false: no-op (returns an empty result). This is the
//     steady state; leaving DENARIX_BOOTSTRAP_ADMIN_EMAIL set does nothing.
//   - An admin already exists and reset is true (break-glass): ensure email is the global admin
//     (transferring if needed) and mint a reset enrollment token that will revoke their existing
//     passkeys when redeemed. This is the recovery path for the one account nobody else can
//     reset. Minting is non-destructive — existing passkeys are only revoked when the token is
//     actually redeemed — so a restart before redemption never locks the admin out.
//
// If a pending (unconsumed, unexpired) enrollment already exists for the target, no new token is
// minted and EnrollmentToken is empty, so a restart loop can't invalidate a token mid-use; to
// force a fresh one, delete the outstanding row or let it expire.
func EnsureBootstrapAdmin(ctx context.Context, store *db.Store, email string, reset bool) (*BootstrapResult, error) {
	admin, err := store.Queries.GetGlobalAdmin(ctx)
	adminExists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	if adminExists && !reset {
		return &BootstrapResult{}, nil
	}

	user, err := getOrCreateAppUser(ctx, store.Queries, email)
	if err != nil {
		return nil, err
	}

	// Make sure the named email actually holds global-admin (grant on first run; transfer on a
	// break-glass reset aimed at a different account than the current holder).
	if !adminExists || admin.ID != user.ID {
		if _, err := GrantGlobalAdmin(ctx, store, user.ID); err != nil {
			return nil, err
		}
	}

	result := &BootstrapResult{User: &User{ID: user.ID, Email: user.Email}, Reset: reset}

	// Don't re-mint over an outstanding token.
	if _, err := store.Queries.GetPendingCredentialEnrollmentForUser(ctx, user.ID); err == nil {
		return result, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	rawToken, err := NewEnrollmentToken()
	if err != nil {
		return nil, err
	}
	if _, err := store.Queries.CreateCredentialEnrollment(ctx, sqlcgen.CreateCredentialEnrollmentParams{
		UserID:         user.ID,
		TokenHash:      HashEnrollmentToken(rawToken),
		RevokeExisting: reset, // first run: additive; reset: wipe the lost device's passkeys
		Purpose:        "BOOTSTRAP",
		ExpiresAt:      pgtype.Timestamp{Time: time.Now().Add(bootstrapEnrollmentTTL), Valid: true},
	}); err != nil {
		return nil, err
	}
	result.EnrollmentToken = rawToken
	return result, nil
}

func getOrCreateAppUser(ctx context.Context, q *sqlcgen.Queries, email string) (sqlcgen.AppUser, error) {
	existing, err := q.GetAppUserByEmail(ctx, email)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return sqlcgen.AppUser{}, err
	}
	return q.CreateAppUser(ctx, sqlcgen.CreateAppUserParams{Email: email})
}
