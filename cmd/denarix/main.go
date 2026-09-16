// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

// Command denarix runs the gRPC API server.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/silverbp/denarix/internal/config"
	"github.com/silverbp/denarix/internal/migrate"
	"github.com/silverbp/denarix/internal/server"
	"github.com/silverbp/denarix/internal/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		runMigrate()
		return
	}

	slog.Info("starting denarix", "version", version.Version, "git_commit", version.GitCommit, "build_date", version.BuildDate)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	srv, err := server.New(ctx, cfg)
	if err != nil {
		slog.Error("failed to start server", "error", err)
		os.Exit(1)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run() }()

	select {
	case <-ctx.Done():
		slog.Info("shutting down")
		srv.Close()
	case err := <-errCh:
		if err != nil {
			slog.Error("server exited", "error", err)
			os.Exit(1)
		}
	}
}

// runMigrate applies pending migrations and exits, without starting either
// server listener - meant to run as a k8s init container ahead of the
// actual denarix container. Only needs DENARIX_POSTGRES_DSN, so it deliberately
// doesn't go through config.Load (which requires DENARIX_JWT_SECRET too).
func runMigrate() {
	dsn := os.Getenv("DENARIX_POSTGRES_DSN")
	if dsn == "" {
		dsn = "postgres://denarix:denarix@localhost:5432/denarix?sslmode=disable"
	}
	if err := migrate.Up(dsn); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
	slog.Info("migrations applied")
}
