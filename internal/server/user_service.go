// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/auth"
	"github.com/silverbp/denarix/internal/db"
	"github.com/silverbp/denarix/internal/db/sqlcgen"
)

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
