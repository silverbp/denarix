// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

// Package moneypb converts between Postgres NUMERIC columns (as scanned by
// sqlc/pgx into pgtype.Numeric) and the wire-level denarix.v1.Decimal message,
// keeping every conversion an exact decimal-string round trip.
package moneypb

import (
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
)

// ToProto converts a pgtype.Numeric into an *denarixv1.Decimal, returning nil
// for a SQL NULL. Infallible: pgx's text encoder handles NaN/Infinity and
// otherwise cannot fail for a Numeric pgx itself scanned, so there is no
// error for a caller to wrap - which is what lets every *ToProto function
// in internal/server be a plain conversion.
func ToProto(n pgtype.Numeric) *denarixv1.Decimal {
	if !n.Valid {
		return nil
	}
	v, err := n.Value()
	if err != nil {
		// Unreachable in practice (see above); surface rather than silently
		// zero the amount if it ever does happen.
		return &denarixv1.Decimal{Value: "NaN"}
	}
	s, _ := v.(string)
	return &denarixv1.Decimal{Value: s}
}

// FromDecimal converts a decimal.Decimal into an *denarixv1.Decimal.
func FromDecimal(d decimal.Decimal) *denarixv1.Decimal {
	return &denarixv1.Decimal{Value: d.String()}
}

// ToNumeric converts an *denarixv1.Decimal into a pgtype.Numeric, returning a
// SQL NULL for a nil message.
func ToNumeric(d *denarixv1.Decimal) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if d == nil {
		return n, nil
	}
	if err := n.Scan(d.GetValue()); err != nil {
		return n, err
	}
	return n, nil
}

// ToNumericOrZero is ToNumeric, but a nil message converts to numeric 0
// rather than SQL NULL — for NOT NULL DEFAULT 0.00 columns like
// ledger_entry.debit_amount/credit_amount, where an explicit NULL would
// violate the column's NOT NULL constraint instead of falling back to its
// default.
func ToNumericOrZero(d *denarixv1.Decimal) (pgtype.Numeric, error) {
	if d == nil {
		var n pgtype.Numeric
		if err := n.Scan("0"); err != nil {
			return n, err
		}
		return n, nil
	}
	return ToNumeric(d)
}
