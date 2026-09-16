// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import "testing"

func TestIsValidInvoiceStatusTransition(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{"DRAFT", "SENT", true},
		{"DRAFT", "CANCELLED", true},
		{"SENT", "OVERDUE", true},
		{"SENT", "CANCELLED", true},
		{"OVERDUE", "SENT", true},
		{"OVERDUE", "CANCELLED", true},

		// PAID is never a direct target - it's set automatically as
		// balance_due crosses zero, never chosen by the caller.
		{"DRAFT", "PAID", false},
		{"SENT", "PAID", false},
		{"OVERDUE", "PAID", false},

		// PAID and CANCELLED are both terminal for this RPC.
		{"PAID", "SENT", false},
		{"PAID", "CANCELLED", false},
		{"PAID", "OVERDUE", false},
		{"CANCELLED", "DRAFT", false},
		{"CANCELLED", "SENT", false},

		// A status not moving anywhere, and garbage input, both read false
		// rather than panicking (nil map lookup).
		{"DRAFT", "DRAFT", false},
		{"BOGUS", "SENT", false},
		{"DRAFT", "BOGUS", false},
	}
	for _, c := range cases {
		if got := isValidInvoiceStatusTransition(c.from, c.to); got != c.want {
			t.Errorf("isValidInvoiceStatusTransition(%q, %q) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}
