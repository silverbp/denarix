// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// publicMethods are reachable without a valid access token — AuthService
// itself, since a caller by definition doesn't have one yet when
// exchanging a code, and may not have a valid one left when refreshing or
// logging out.
var publicMethods = map[string]bool{
	"/denarix.v1.AuthService/ExchangeCode": true,
	"/denarix.v1.AuthService/RefreshToken": true,
	"/denarix.v1.AuthService/Logout":       true,
}

// resolveContext is the shared body of UnaryInterceptor/StreamInterceptor:
// the caller must present a valid "authorization: Bearer <access token>"
// metadata entry, verified via verifyAccessToken — except for
// publicMethods, which pass through unresolved.
func resolveContext(ctx context.Context, verifyAccessToken func(token string) (*User, error), fullMethod string) (context.Context, error) {
	if publicMethods[fullMethod] {
		return ctx, nil
	}
	token, err := bearerToken(ctx)
	if err != nil {
		return nil, err
	}
	u, err := verifyAccessToken(token)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "invalid access token: %v", err)
	}
	return ContextWithUser(ctx, u), nil
}

// UnaryInterceptor resolves the calling user for every unary RPC before it
// reaches a handler — see resolveContext.
func UnaryInterceptor(verifyAccessToken func(token string) (*User, error)) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, err := resolveContext(ctx, verifyAccessToken, info.FullMethod)
		if err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamInterceptor is UnaryInterceptor's counterpart for client-streaming/
// server-streaming/bidi RPCs (e.g. AttachmentService.UploadAttachment/
// DownloadAttachment) — gRPC only ever applies a UnaryServerInterceptor to
// unary RPCs, so without this registered too, a streaming handler's
// stream.Context() would never carry a resolved user at all.
func StreamInterceptor(verifyAccessToken func(token string) (*User, error)) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, err := resolveContext(ss.Context(), verifyAccessToken, info.FullMethod)
		if err != nil {
			return err
		}
		return handler(srv, &authenticatedServerStream{ServerStream: ss, ctx: ctx})
	}
}

// authenticatedServerStream overrides Context() to return the
// user-resolved context StreamInterceptor built, since grpc.ServerStream
// otherwise always returns the original (unauthenticated) one.
type authenticatedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *authenticatedServerStream) Context() context.Context { return s.ctx }

func bearerToken(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "no authorization metadata")
	}
	vals := md.Get("authorization")
	if len(vals) == 0 {
		return "", status.Error(codes.Unauthenticated, "no authorization metadata")
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(vals[0], prefix) {
		return "", status.Error(codes.Unauthenticated, `authorization metadata must be "Bearer <token>"`)
	}
	return strings.TrimPrefix(vals[0], prefix), nil
}
