// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

// Package migrations embeds this directory's .sql files for cmd/denarix's
// `migrate` mode (see internal/migrate), which both `make migrate-up` and
// the k8s init container go through - a single code path so a database
// never ends up with schema applied outside golang-migrate's own
// schema_migrations tracking (which would make Up() misfire on it later).
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
