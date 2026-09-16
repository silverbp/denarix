// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Every translation from a storage-layer error to a gRPC status lives in
// this file, so a handler never builds one by hand:
//
//   - translatePgError: any error from a single sqlc query.
//   - translateUpdateError: the same, plus the no-row-matched outcome of an
//     optimistic-concurrency UPDATE.
//   - txErrorStatus: whatever came out of a store.ExecTx body.

// translatePgError maps a raw Postgres error into a gRPC status, so schema
// invariants (the period-lock trigger, CHECK constraints, unique indexes)
// surface as meaningful client errors instead of an opaque Internal.
func translatePgError(err error) error {
	if err == nil {
		return nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "P0001": // RAISE EXCEPTION, e.g. enforce_period_lock
			return status.Error(codes.FailedPrecondition, pgErr.Message)
		case "23505": // unique_violation
			return status.Error(codes.AlreadyExists, pgErr.Message)
		case "23514", "23503", "23502": // check_violation, foreign_key_violation, not_null_violation
			return status.Error(codes.InvalidArgument, pgErr.Message)
		}
	}

	return status.Errorf(codes.Internal, "%v", err)
}

// translateUpdateError is translatePgError plus the one outcome an
// optimistic-concurrency UPDATE adds: no row matched. Every update handler
// reads the row first (for the business-role check), so if the UPDATE then
// matches nothing, the row changed - or was soft-deleted - between that
// read and this write. With a precondition that's ABORTED (the gRPC code
// for a failed compare-and-swap; k8s would say 409 Conflict): nothing was
// written, the caller re-reads and retries. Without one, the only way to
// miss is the row disappearing, so NotFound.
func translateUpdateError(err error, kind string, id int64, expected int64) error {
	if errors.Is(err, pgx.ErrNoRows) {
		if expected != 0 {
			return status.Errorf(codes.Aborted,
				"%s %d has been modified since resource_version %d; re-read it and apply your changes to the latest version",
				kind, id, expected)
		}
		return status.Errorf(codes.NotFound, "%s %d not found", kind, id)
	}
	return translatePgError(err)
}

// txErrorStatus turns whatever came out of a store.ExecTx body into a gRPC
// status: a status error passes through unchanged (handlers return those
// from inside the transaction for validation failures), a recognisable
// Postgres error goes through translatePgError (e.g. FailedPrecondition
// from enforce_period_lock, AlreadyExists from a unique index), and any
// other plain Go error - periodclose's contiguity/idempotency guard rails,
// a posting helper's own precondition - is reported as InvalidArgument.
func txErrorStatus(err error) error {
	if _, ok := status.FromError(err); ok {
		return err
	}
	if pgErr := translatePgError(err); status.Code(pgErr) != codes.Internal {
		return pgErr
	}
	return status.Error(codes.InvalidArgument, err.Error())
}
