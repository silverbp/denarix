// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

// Package ledgermath holds double-entry sign-convention arithmetic shared
// by internal/reporting and internal/periodclose — normal_balance direction
// handling lives in exactly one place, since a subtle sign error here would
// silently corrupt both financial reports and period-close postings.
package ledgermath

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

// InceptionDate stands in for "since the business began" when a report or
// the close service needs a lower bound but there's no natural one.
var InceptionDate = time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)

// NetBalance applies an account's normal_balance to its raw debit/credit
// totals: for a DEBIT-normal account (ASSETS, EXPENSES) the natural
// positive direction is debit - credit; for a CREDIT-normal account
// (LIABILITIES, EQUITY, REVENUE, TAX_LIABILITY) it's credit - debit.
func NetBalance(normalBalance string, debit, credit decimal.Decimal) decimal.Decimal {
	if normalBalance == "DEBIT" {
		return debit.Sub(credit)
	}
	return credit.Sub(debit)
}

// EntryForNormalDelta returns the (debit, credit) pair that changes an
// account with the given normal_balance by delta in its normal (positive)
// direction: delta > 0 posts to the account's normal side, delta < 0 posts
// to the opposite side. To zero out an account currently holding `net` (in
// its own normal direction), pass net.Neg().
func EntryForNormalDelta(normalBalance string, delta decimal.Decimal) (debit, credit decimal.Decimal) {
	amt := delta.Abs()
	normalIsDebit := normalBalance == "DEBIT"
	isNegative := delta.IsNegative()
	if normalIsDebit != isNegative {
		return amt, decimal.Zero
	}
	return decimal.Zero, amt
}

// DecimalToNumeric is the inverse of NumericToDecimal.
func DecimalToNumeric(d decimal.Decimal) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(d.String()); err != nil {
		return n, err
	}
	return n, nil
}

func NumericToDecimal(n pgtype.Numeric) (decimal.Decimal, error) {
	if !n.Valid {
		return decimal.Zero, nil
	}
	v, err := n.Value()
	if err != nil {
		return decimal.Zero, err
	}
	s, _ := v.(string)
	return decimal.NewFromString(s)
}

func PgDate(t time.Time) pgtype.Date {
	return pgtype.Date{Time: t, Valid: true}
}
