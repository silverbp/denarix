// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

// Package apiclient dials the denarix gRPC API for dxctl.
package apiclient

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Dial connects to server. insecure disables TLS — for a local dev server
// with no cert; a real deployment sits behind a TLS-terminating reverse
// proxy, so the client must use real transport credentials there.
// accessToken, if non-empty, is attached as "authorization: Bearer <token>"
// on every call.
func Dial(server string, insecureTransport bool, accessToken string) (*grpc.ClientConn, error) {
	transportCreds := credentials.NewClientTLSFromCert(nil, "")
	if insecureTransport {
		transportCreds = insecure.NewCredentials()
	}

	opts := []grpc.DialOption{grpc.WithTransportCredentials(transportCreds)}
	if accessToken != "" {
		opts = append(opts, grpc.WithPerRPCCredentials(bearerToken{token: accessToken, allowInsecure: insecureTransport}))
	}
	return grpc.NewClient(server, opts...)
}

type bearerToken struct {
	token         string
	allowInsecure bool
}

func (b bearerToken) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + b.token}, nil
}

func (b bearerToken) RequireTransportSecurity() bool {
	return !b.allowInsecure
}
