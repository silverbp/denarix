// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestExpectedResourceVersion(t *testing.T) {
	if got := expectedResourceVersion(0); got != nil {
		t.Fatalf("0 should mean unconditional (nil), got %d", *got)
	}
	if got := expectedResourceVersion(7); got == nil || *got != 7 {
		t.Fatalf("7 should round-trip, got %v", got)
	}
}

func TestTranslateUpdateError(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		expected int64
		want     codes.Code
	}{
		{"no row, precondition set -> Aborted", pgx.ErrNoRows, 3, codes.Aborted},
		{"no row, unconditional -> NotFound", pgx.ErrNoRows, 0, codes.NotFound},
		{"other error passes through translatePgError", errors.New("boom"), 3, codes.Internal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := translateUpdateError(c.err, "invoice", 42, c.expected)
			if status.Code(got) != c.want {
				t.Fatalf("got %v (%v), want %v", status.Code(got), got, c.want)
			}
		})
	}
	// A conflict must survive txErrorStatus, which the transactional
	// handlers (UpdateInvoiceLineItems / UpdateEstimateLineItems) route
	// every in-transaction error through.
	conflict := translateUpdateError(pgx.ErrNoRows, "invoice", 42, 3)
	if got := txErrorStatus(conflict); status.Code(got) != codes.Aborted {
		t.Fatalf("txErrorStatus rewrote Aborted to %v", status.Code(got))
	}
}
