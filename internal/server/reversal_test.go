// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	typepb "google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestResolveReversalDate(t *testing.T) {
	original := pgtype.Date{Time: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), Valid: true}

	t.Run("unset falls back to the original date", func(t *testing.T) {
		got, err := resolveReversalDate(original, nil, "invoice_date")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got.Time.Equal(original.Time) {
			t.Errorf("got %s, want %s", got.Time, original.Time)
		}
	})

	t.Run("later date is accepted", func(t *testing.T) {
		got, err := resolveReversalDate(original, &typepb.Date{Year: 2026, Month: 2, Day: 1}, "invoice_date")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Time.Format("2006-01-02") != "2026-02-01" {
			t.Errorf("got %s, want 2026-02-01", got.Time.Format("2006-01-02"))
		}
	})

	t.Run("same date is accepted", func(t *testing.T) {
		if _, err := resolveReversalDate(original, &typepb.Date{Year: 2026, Month: 1, Day: 15}, "invoice_date"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("earlier date is rejected", func(t *testing.T) {
		_, err := resolveReversalDate(original, &typepb.Date{Year: 2026, Month: 1, Day: 14}, "invoice_date")
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("got %v, want InvalidArgument", err)
		}
	})
}
