// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

// Package migrate applies the embedded contents of the migrations package
// to Postgres via golang-migrate, which tracks applied versions in a
// schema_migrations table it manages itself - so Up is safe to call on
// every process start (a k8s init container, in particular): it's a no-op
// once the database is already caught up.
package migrate

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers the "pgx5" driver denarix's own pgx/v5 dependency uses
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/silverbp/denarix/migrations"
)

// Up applies every not-yet-applied migration to dsn (a "postgres://..."
// connection string, same as DENARIX_POSTGRES_DSN).
func Up(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("parsing dsn: %w", err)
	}
	// golang-migrate dispatches by URL scheme; the pgx/v5 driver registers
	// itself as "pgx5" but rewrites the scheme back to "postgres" before
	// actually connecting, so this swap only affects driver selection.
	parsed.Scheme = "pgx5"

	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("loading embedded migrations: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", source, parsed.String())
	if err != nil {
		return fmt.Errorf("connecting migrator: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("applying migrations: %w", err)
	}
	return nil
}
