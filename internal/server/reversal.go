// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"github.com/jackc/pgx/v5/pgtype"
	typepb "google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/silverbp/denarix/internal/datepb"
)

// resolveReversalDate picks the date a reversing transaction posts on:
// requested when set, else the original's own date. A reversal may be
// dated later than the original - that's the whole point of the field, so
// a transaction in a closed period can be corrected in the open one - but
// never earlier, which would misstate the period the mistake stood in.
// originalField names the original date in the error (e.g. "invoice_date").
func resolveReversalDate(original pgtype.Date, requested *typepb.Date, originalField string) (pgtype.Date, error) {
	if requested == nil {
		return original, nil
	}
	d := datepb.ToPgDate(requested)
	if !d.Valid {
		return pgtype.Date{}, status.Error(codes.InvalidArgument, "invalid reversal_date")
	}
	if d.Time.Before(original.Time) {
		return pgtype.Date{}, status.Errorf(codes.InvalidArgument,
			"reversal_date %s is earlier than the original %s %s - a reversal may be dated later (e.g. in the open period), never earlier",
			d.Time.Format("2006-01-02"), originalField, original.Time.Format("2006-01-02"))
	}
	return d, nil
}
