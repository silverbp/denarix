// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package ledgerpost

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestDebitCreditFor(t *testing.T) {
	cases := []struct {
		name         string
		amount       string
		naturalDebit bool
		wantDebit    string
		wantCredit   string
	}{
		{"positive natural-debit stays a debit", "1000.00", true, "1000.00", "0"},
		{"positive natural-credit stays a credit", "1000.00", false, "0", "1000.00"},
		{"negative natural-debit flips to a credit", "-100.00", true, "0", "100.00"},
		{"negative natural-credit flips to a debit", "-100.00", false, "100.00", "0"},
		{"zero stays zero either way", "0", true, "0", "0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			amount := decimal.RequireFromString(c.amount)
			debit, credit := DebitCreditFor(amount, c.naturalDebit)
			if !debit.Equal(decimal.RequireFromString(c.wantDebit)) {
				t.Errorf("debit = %s, want %s", debit, c.wantDebit)
			}
			if !credit.Equal(decimal.RequireFromString(c.wantCredit)) {
				t.Errorf("credit = %s, want %s", credit, c.wantCredit)
			}
			// Exactly one side must ever be strictly positive, matching
			// ledger_entry_debit_or_credit — except the zero case, which
			// callers must skip rather than insert.
			if !amount.IsZero() {
				if debit.IsPositive() == credit.IsPositive() {
					t.Errorf("expected exactly one side positive, got debit=%s credit=%s", debit, credit)
				}
			}
		})
	}
}
