// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

// Package config loads server configuration from the environment.
package config

import (
	"fmt"
	"os"
)

type Config struct {
	// PostgresDSN is the connection string for the denarix Postgres database.
	PostgresDSN string
	// GRPCAddr is the listen address for the gRPC server.
	GRPCAddr string
	// HTTPAddr is the listen address for the plain HTTP server that hosts
	// the WebAuthn registration/login pages and callback endpoints. A
	// reverse proxy fronts both this and GRPCAddr on one public domain +
	// port, routed by path (see internal/dxctl/cmd/login.go's httpBaseURL).
	HTTPAddr string
	// PublicBaseURL is this server's own externally-reachable origin, e.g.
	// "https://denarix.example.com" — used both as the WebAuthn
	// relying-party origin and to build absolute links in the auth pages.
	PublicBaseURL string
	// RPID is the WebAuthn relying party ID: the domain passkeys are scoped
	// to, e.g. "denarix.example.com" (no scheme/port). Must exactly
	// match (or be a registrable-domain suffix of) the origin the browser
	// is actually on, or the ceremony fails.
	RPID string
	// JWTSecret signs and verifies access tokens (HMAC-SHA256). Required.
	JWTSecret string
	// BootstrapAdminEmail, if set, is granted global-admin status at server
	// startup — but only if no global admin exists yet (see
	// auth.EnsureBootstrapAdmin); a legitimate later transfer via
	// UserService.SetGlobalAdmin sticks even if this stays set. Optional —
	// leave unset once a real admin exists.
	BootstrapAdminEmail string
	// StorageEndpoint is the object-storage backend's S3-compatible API
	// address (host:port, no scheme) - SeaweedFS locally, see
	// docker-compose.yml's seaweedfs service and internal/storage.
	StorageEndpoint string
	// StorageAccessKey/StorageSecretKey authenticate to the storage
	// backend. SeaweedFS accepts any non-empty pair when it hasn't been
	// configured with a real identity file (true for the local dev
	// compose setup); a real deploy should use real credentials.
	StorageAccessKey string
	StorageSecretKey string
	// StorageBucket is the bucket attachments are stored in.
	StorageBucket string
	// StorageUseSSL selects http vs https to the storage endpoint. False
	// for the local dev SeaweedFS container.
	StorageUseSSL bool
}

func Load() (Config, error) {
	cfg := Config{
		PostgresDSN:   getEnv("DENARIX_POSTGRES_DSN", "postgres://denarix:denarix@localhost:5432/denarix?sslmode=disable"),
		GRPCAddr:      getEnv("DENARIX_GRPC_ADDR", ":9090"),
		HTTPAddr:      getEnv("DENARIX_HTTP_ADDR", ":9091"),
		PublicBaseURL: getEnv("DENARIX_PUBLIC_BASE_URL", "http://localhost:9091"),
		RPID:          getEnv("DENARIX_RP_ID", "localhost"),
		JWTSecret:     getEnv("DENARIX_JWT_SECRET", ""),

		BootstrapAdminEmail: getEnv("DENARIX_BOOTSTRAP_ADMIN_EMAIL", ""),

		StorageEndpoint:  getEnv("DENARIX_STORAGE_ENDPOINT", "localhost:8333"),
		StorageAccessKey: getEnv("DENARIX_STORAGE_ACCESS_KEY", "seaweedfs"),
		StorageSecretKey: getEnv("DENARIX_STORAGE_SECRET_KEY", "seaweedfs"),
		StorageBucket:    getEnv("DENARIX_STORAGE_BUCKET", "denarix-attachments"),
		StorageUseSSL:    getEnv("DENARIX_STORAGE_USE_SSL", "false") == "true",
	}

	if cfg.JWTSecret == "" {
		return Config{}, fmt.Errorf("DENARIX_JWT_SECRET is required")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
