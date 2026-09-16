// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

// Package datepb converts between Postgres DATE columns (as scanned by
// sqlc/pgx into pgtype.Date) and google.type.Date — not
// google.protobuf.Timestamp, which implies a time-of-day these columns
// don't have.
package datepb

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	typepb "google.golang.org/genproto/googleapis/type/date"
)

// ToProto converts a pgtype.Date into a *typepb.Date, returning nil for a
// SQL NULL.
func ToProto(d pgtype.Date) *typepb.Date {
	if !d.Valid {
		return nil
	}
	return &typepb.Date{
		Year:  int32(d.Time.Year()),
		Month: int32(d.Time.Month()),
		Day:   int32(d.Time.Day()),
	}
}

// ToPgDate converts a *typepb.Date into a pgtype.Date, returning a SQL NULL
// for a nil message.
func ToPgDate(d *typepb.Date) pgtype.Date {
	if d == nil {
		return pgtype.Date{}
	}
	t := time.Date(int(d.GetYear()), time.Month(d.GetMonth()), int(d.GetDay()), 0, 0, 0, 0, time.UTC)
	return pgtype.Date{Time: t, Valid: true}
}

// FromTime converts a time.Time (date part only) into a *typepb.Date.
func FromTime(t time.Time) *typepb.Date {
	return &typepb.Date{Year: int32(t.Year()), Month: int32(t.Month()), Day: int32(t.Day())}
}
