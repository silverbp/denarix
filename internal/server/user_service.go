// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/auth"
	"github.com/silverbp/denarix/internal/db"
	"github.com/silverbp/denarix/internal/db/sqlcgen"
)

// resetEnrollmentTTL mirrors businessInviteTTL: a reset token is
// hand-delivered to a user who then has to actually see it and register, so
// a week is enough while still being short-lived.
const resetEnrollmentTTL = 7 * 24 * time.Hour

type userService struct {
	denarixv1.UnimplementedUserServiceServer
	store *db.Store
}

func newUserService(store *db.Store) *userService {
	return &userService{store: store}
}

func (s *userService) GetMe(ctx context.Context, _ *denarixv1.GetMeRequest) (*denarixv1.GetMeResponse, error) {
	u, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no authenticated user")
	}
	appUser, err := s.store.Queries.GetAppUser(ctx, u.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, status.Error(codes.Unauthenticated, "no authenticated user")
		}
		return nil, status.Errorf(codes.Internal, "getting user: %v", err)
	}
	return &denarixv1.GetMeResponse{User: appUserToProto(appUser)}, nil
}

// SetGlobalAdmin is global-admin-only: transfers admin status to another
// user (see auth.GrantGlobalAdmin — there is only ever one) or revokes the
// target's own status, leaving zero admins until someone is granted it
// again.
func (s *userService) SetGlobalAdmin(ctx context.Context, req *denarixv1.SetGlobalAdminRequest) (*denarixv1.SetGlobalAdminResponse, error) {
	if err := auth.RequireGlobalAdmin(ctx, s.store.Queries); err != nil {
		return nil, err
	}

	var updated sqlcgen.AppUser
	var err error
	if req.GetIsGlobalAdmin() {
		updated, err = auth.GrantGlobalAdmin(ctx, s.store, req.GetUserId())
	} else {
		updated, err = auth.RevokeGlobalAdmin(ctx, s.store, req.GetUserId())
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, status.Errorf(codes.NotFound, "user %d not found", req.GetUserId())
		}
		return nil, status.Errorf(codes.Internal, "setting global admin: %v", err)
	}
	return &denarixv1.SetGlobalAdminResponse{User: appUserToProto(updated)}, nil
}

// ResetUserCredentials mints a single-use enrollment token letting the
// target register a new passkey — revoking their existing ones by default
// (the lost-device recovery case; pass keep_existing to just add a device).
// A global admin can reset any user; otherwise the caller must be OWNER/ADMIN
// of the given business and the target must belong to it.
func (s *userService) ResetUserCredentials(ctx context.Context, req *denarixv1.ResetUserCredentialsRequest) (*denarixv1.ResetUserCredentialsResponse, error) {
	caller, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no authenticated user")
	}
	if req.GetUserId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	// Global admin may reset anyone. Otherwise fall back to business-scoped
	// authority: OWNER/ADMIN of the business, and the target must be a member.
	if err := auth.RequireGlobalAdmin(ctx, s.store.Queries); err != nil {
		if req.GetBusinessId() == 0 {
			return nil, status.Error(codes.PermissionDenied, "global admin required, or pass a business_id you are an OWNER/ADMIN of")
		}
		if err := auth.RequireBusinessRole(ctx, s.store.Queries, req.GetBusinessId(), "ADMIN"); err != nil {
			return nil, err
		}
		if _, err := s.store.Queries.GetBusinessUser(ctx, sqlcgen.GetBusinessUserParams{BusinessID: req.GetBusinessId(), UserID: req.GetUserId()}); err != nil {
			if isNoRows(err) {
				return nil, status.Errorf(codes.NotFound, "user %d is not a member of business %d", req.GetUserId(), req.GetBusinessId())
			}
			return nil, translatePgError(err)
		}
	}

	target, err := s.store.Queries.GetAppUser(ctx, req.GetUserId())
	if err != nil {
		if isNoRows(err) {
			return nil, status.Errorf(codes.NotFound, "user %d not found", req.GetUserId())
		}
		return nil, status.Errorf(codes.Internal, "getting user: %v", err)
	}

	rawToken, err := auth.NewEnrollmentToken()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "generating enrollment token: %v", err)
	}
	created, err := s.store.Queries.CreateCredentialEnrollment(ctx, sqlcgen.CreateCredentialEnrollmentParams{
		UserID:          target.ID,
		TokenHash:       auth.HashEnrollmentToken(rawToken),
		RevokeExisting:  !req.GetKeepExisting(),
		Purpose:         "RESET",
		CreatedByUserID: &caller.ID,
		ExpiresAt:       pgtype.Timestamp{Time: time.Now().Add(resetEnrollmentTTL), Valid: true},
	})
	if err != nil {
		return nil, translatePgError(err)
	}

	return &denarixv1.ResetUserCredentialsResponse{
		Token:     rawToken,
		ExpiresAt: timestampProto(created.ExpiresAt),
	}, nil
}

func appUserToProto(u sqlcgen.AppUser) *denarixv1.AppUser {
	return &denarixv1.AppUser{
		Id:            u.ID,
		Email:         u.Email,
		DisplayName:   u.DisplayName,
		IsGlobalAdmin: u.IsGlobalAdmin,
		IsActive:      u.IsActive,
		CreatedAt:     timestampProto(u.CreatedAt),
	}
}
