// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package auth

import (
	"context"

	"github.com/silverbp/denarix/internal/db"
	"github.com/silverbp/denarix/internal/db/sqlcgen"
)

// GrantGlobalAdmin transfers global-admin status to userID, atomically clearing whoever
// currently holds it (if anyone) first — there is only ever one global admin at a time (see the
// app_user_single_global_admin_uindex constraint this keeps satisfied), so granting it to a new
// user is a transfer, not an addition.
func GrantGlobalAdmin(ctx context.Context, store *db.Store, userID int64) (sqlcgen.AppUser, error) {
	var updated sqlcgen.AppUser
	err := store.ExecTx(ctx, func(q *sqlcgen.Queries) error {
		if err := q.ClearGlobalAdmin(ctx); err != nil {
			return err
		}
		var err error
		updated, err = q.SetGlobalAdmin(ctx, sqlcgen.SetGlobalAdminParams{ID: userID, IsGlobalAdmin: true})
		return err
	})
	return updated, err
}

// RevokeGlobalAdmin clears userID's global-admin status without granting it to anyone else,
// leaving zero global admins until someone is granted it again (via GrantGlobalAdmin, or the
// bootstrap admin at the next server start — see EnsureBootstrapAdmin).
func RevokeGlobalAdmin(ctx context.Context, store *db.Store, userID int64) (sqlcgen.AppUser, error) {
	return store.Queries.SetGlobalAdmin(ctx, sqlcgen.SetGlobalAdminParams{ID: userID, IsGlobalAdmin: false})
}
