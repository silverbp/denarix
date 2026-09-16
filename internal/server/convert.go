// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

// Small proto/pg conversion helpers used by every service - the per-type
// *ToProto functions live next to their service; only the cross-cutting
// scalar helpers are here. Money goes through internal/moneypb, dates
// through internal/datepb.

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	typepb "google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
)

func timestampProto(t pgtype.Timestamp) *timestamppb.Timestamp {
	if !t.Valid {
		return nil
	}
	return timestamppb.New(t.Time)
}

func derefOr(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}

// requireDate rejects a missing date field with InvalidArgument naming it.
func requireDate(d *typepb.Date, field string) (time.Time, error) {
	if d == nil {
		return time.Time{}, status.Errorf(codes.InvalidArgument, "%s is required", field)
	}
	return time.Date(int(d.GetYear()), time.Month(d.GetMonth()), int(d.GetDay()), 0, 0, 0, 0, time.UTC), nil
}

// requirePeriod is requireDate for the period_start/period_end pair every
// range report takes.
func requirePeriod(start, end *typepb.Date) (time.Time, time.Time, error) {
	s, err := requireDate(start, "period_start")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	e, err := requireDate(end, "period_end")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return s, e, nil
}

// parseDecimalOrDefault parses a request Decimal, substituting def when
// the field was left unset.
func parseDecimalOrDefault(d *denarixv1.Decimal, def string) (decimal.Decimal, error) {
	if d == nil || d.GetValue() == "" {
		return decimal.NewFromString(def)
	}
	return decimal.NewFromString(d.GetValue())
}
