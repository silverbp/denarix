// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/silverbp/denarix/internal/db"
	"github.com/silverbp/denarix/internal/db/sqlcgen"
)

// EnsureBootstrapAdmin get-or-creates an app_user for email and grants them global-admin
// status, but only if no global admin currently exists — there's no existing admin to grant the
// very first one otherwise. This is purely a first-time bootstrap, not a standing override: once
// any admin exists (this one, or one legitimately transferred via UserService.SetGlobalAdmin),
// later server restarts leave it alone even if DENARIX_BOOTSTRAP_ADMIN_EMAIL is still set — a
// transfer sticks rather than being fought by the env var on every deploy. Returns (nil, nil) if
// an admin already exists.
func EnsureBootstrapAdmin(ctx context.Context, store *db.Store, email string) (*User, error) {
	if _, err := store.Queries.GetGlobalAdmin(ctx); err == nil {
		return nil, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	existing, err := store.Queries.GetAppUserByEmail(ctx, email)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		existing, err = store.Queries.CreateAppUser(ctx, sqlcgen.CreateAppUserParams{Email: email})
		if err != nil {
			return nil, err
		}
	}

	if _, err := GrantGlobalAdmin(ctx, store, existing.ID); err != nil {
		return nil, err
	}
	return &User{ID: existing.ID, Email: existing.Email}, nil
}
