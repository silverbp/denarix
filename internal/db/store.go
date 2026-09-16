// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

// Package db wires up the pgx connection pool and the sqlc-generated
// Queries, plus a transaction helper used by any service that needs more
// than one statement to commit atomically (most notably period close).
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/silverbp/denarix/internal/db/sqlcgen"
)

type Store struct {
	pool    *pgxpool.Pool
	Queries *sqlcgen.Queries
}

func NewStore(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connecting to postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging postgres: %w", err)
	}
	return &Store{
		pool:    pool,
		Queries: sqlcgen.New(pool),
	}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

// ExecTx runs fn against a *sqlcgen.Queries bound to a single transaction,
// committing on success and rolling back on any error (including a panic,
// which is re-raised after rollback).
func (s *Store) ExecTx(ctx context.Context, fn func(q *sqlcgen.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()

	if err := fn(s.Queries.WithTx(tx)); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil && rbErr != pgx.ErrTxClosed {
			return fmt.Errorf("%w (rollback also failed: %v)", err, rbErr)
		}
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}
	return nil
}
