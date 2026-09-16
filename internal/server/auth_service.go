// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/auth"
	"github.com/silverbp/denarix/internal/config"
	"github.com/silverbp/denarix/internal/db"
)

// authService implements AuthService — the gRPC-side half of the login
// flow. See httpauth.go for the HTTP-side half (the WebAuthn ceremony
// itself) and docs/implementation-plan.md's Phase 9 notes for how the two
// connect via internal/auth/authcode.go.
type authService struct {
	denarixv1.UnimplementedAuthServiceServer
	store *db.Store
	cfg   config.Config
}

func newAuthService(store *db.Store, cfg config.Config) *authService {
	return &authService{store: store, cfg: cfg}
}

func (s *authService) ExchangeCode(ctx context.Context, req *denarixv1.ExchangeCodeRequest) (*denarixv1.ExchangeCodeResponse, error) {
	if req.GetCode() == "" {
		return nil, status.Error(codes.InvalidArgument, "code is required")
	}
	userID, ok := auth.ConsumeAuthCode(req.GetCode())
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "code is invalid, expired, or already used")
	}

	appUser, err := s.store.Queries.GetAppUser(ctx, userID)
	if err != nil {
		return nil, translatePgError(err)
	}
	u := &auth.User{ID: appUser.ID, Email: appUser.Email}

	accessToken, expiresAt, err := auth.MintAccessToken(s.cfg.JWTSecret, u)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "minting access token: %v", err)
	}
	refreshToken, _, err := auth.IssueSession(ctx, s.store.Queries, u.ID, req.GetClientName())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "issuing session: %v", err)
	}

	return &denarixv1.ExchangeCodeResponse{
		AccessToken:          accessToken,
		RefreshToken:         refreshToken,
		AccessTokenExpiresAt: timestamppb.New(expiresAt),
	}, nil
}

func (s *authService) RefreshToken(ctx context.Context, req *denarixv1.RefreshTokenRequest) (*denarixv1.RefreshTokenResponse, error) {
	if req.GetRefreshToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "refresh_token is required")
	}
	newRefreshToken, u, err := auth.RotateSession(ctx, s.store.Queries, req.GetRefreshToken(), req.GetClientName())
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "%v", err)
	}
	accessToken, expiresAt, err := auth.MintAccessToken(s.cfg.JWTSecret, u)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "minting access token: %v", err)
	}
	return &denarixv1.RefreshTokenResponse{
		AccessToken:          accessToken,
		RefreshToken:         newRefreshToken,
		AccessTokenExpiresAt: timestamppb.New(expiresAt),
	}, nil
}

func (s *authService) Logout(ctx context.Context, req *denarixv1.LogoutRequest) (*denarixv1.LogoutResponse, error) {
	if req.GetRefreshToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "refresh_token is required")
	}
	if err := auth.RevokeSession(ctx, s.store.Queries, req.GetRefreshToken()); err != nil {
		return nil, status.Errorf(codes.Internal, "revoking session: %v", err)
	}
	return &denarixv1.LogoutResponse{}, nil
}
