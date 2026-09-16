// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/silverbp/denarix/internal/db/sqlcgen"
)

var roleRank = map[string]int{
	"VIEWER": 0,
	"MEMBER": 1,
	"ADMIN":  2,
	"OWNER":  3,
}

// RequireBusinessRole checks that the calling user (resolved onto ctx by
// UnaryInterceptor) is a member of businessID with at least minRole.
// Returns Unauthenticated if no user was resolved, NotFound if the caller
// has no membership at all (avoids leaking whether a business id exists to
// non-members), or PermissionDenied if their role is too low.
func RequireBusinessRole(ctx context.Context, q *sqlcgen.Queries, businessID int64, minRole string) error {
	u, ok := UserFromContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "no authenticated user")
	}

	membership, err := q.GetBusinessUser(ctx, sqlcgen.GetBusinessUserParams{
		BusinessID: businessID,
		UserID:     u.ID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return status.Errorf(codes.NotFound, "business %d not found", businessID)
		}
		return status.Errorf(codes.Internal, "checking business membership: %v", err)
	}

	if roleRank[membership.Role] < roleRank[minRole] {
		return status.Errorf(codes.PermissionDenied, "role %s does not meet required role %s for business %d", membership.Role, minRole, businessID)
	}
	return nil
}

// RequireGlobalAdmin checks that the calling user is a global admin
// (app_user.is_global_admin) — a system-wide capability, not scoped to any
// one business. Used to gate creating a business at all, and (alongside
// RequireBusinessRole) inviting a user into one — see
// BusinessService/UserService in proto/denarix/v1.
func RequireGlobalAdmin(ctx context.Context, q *sqlcgen.Queries) error {
	u, ok := UserFromContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "no authenticated user")
	}

	appUser, err := q.GetAppUser(ctx, u.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return status.Error(codes.Unauthenticated, "no authenticated user")
		}
		return status.Errorf(codes.Internal, "checking admin status: %v", err)
	}
	if !appUser.IsGlobalAdmin {
		return status.Error(codes.PermissionDenied, "global admin required")
	}
	return nil
}

// RequireGlobalAdminOrBusinessRole allows either a global admin (any
// business) or a member of businessID with at least minRole — the
// permission shape CreateBusinessInvite uses: a global admin can invite
// into any business, and that business's own OWNER/ADMIN can invite into
// their own.
func RequireGlobalAdminOrBusinessRole(ctx context.Context, q *sqlcgen.Queries, businessID int64, minRole string) error {
	if err := RequireGlobalAdmin(ctx, q); err == nil {
		return nil
	}
	return RequireBusinessRole(ctx, q, businessID, minRole)
}
