// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
)

func TestParseLineFlags_ItemRequired(t *testing.T) {
	_, err := parseLineFlags([]string{"desc=Free text,price=10"})
	if err == nil || !strings.Contains(err.Error(), "missing item=") {
		t.Fatalf("free-text line should be rejected, got %v", err)
	}
}

func TestParseLineFlags_UnknownKeyRejected(t *testing.T) {
	_, err := parseLineFlags([]string{"item=71,account=40"})
	if err == nil || !strings.Contains(err.Error(), `unknown key "account"`) {
		t.Fatalf("account= should be rejected as unknown, got %v", err)
	}
}

func TestParseLineFlags_DescOptionalAndBareTaxable(t *testing.T) {
	lines, err := parseLineFlags([]string{"item=71,qty=10,taxable", "item=72,desc=Custom,tax-rate=1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if lines[0]["taxable"] != "true" {
		t.Errorf("bare taxable should parse as true, got %q", lines[0]["taxable"])
	}
	if _, ok := lines[0]["desc"]; ok {
		t.Error("desc should be absent when not given (server defaults it from the item)")
	}
	if lines[1]["desc"] != "Custom" {
		t.Errorf("desc = %q, want Custom", lines[1]["desc"])
	}
}

func TestParseLineFlags_AtLeastOne(t *testing.T) {
	if _, err := parseLineFlags(nil); err == nil {
		t.Fatal("no --line should be an error")
	}
}

func TestNewDocumentLineItems_MapsItemAndLineNumber(t *testing.T) {
	lines, err := newDocumentLineItems([]string{"item=71,qty=10", "item=72,price=5.00,tax-rate=3"})
	if err != nil {
		t.Fatal(err)
	}
	if lines[0].GetItemId() != 71 || lines[0].GetLineNumber() != 1 || lines[0].GetQuantity().GetValue() != "10" {
		t.Errorf("line 0 = %+v", lines[0])
	}
	if lines[1].GetItemId() != 72 || lines[1].GetLineNumber() != 2 || lines[1].GetTaxRateId() != 3 || lines[1].GetUnitPrice().GetValue() != "5.00" {
		t.Errorf("line 1 = %+v", lines[1])
	}
	if _, err := newDocumentLineItems([]string{"item=abc"}); err == nil {
		t.Error("non-integer item= should error")
	}
}

func TestParseID(t *testing.T) {
	if id, err := parseID("invoice", "42"); err != nil || id != 42 {
		t.Fatalf("parseID(42) = %d, %v", id, err)
	}
	_, err := parseID("invoice", "x")
	if err == nil || !strings.Contains(err.Error(), `invalid invoice id "x"`) {
		t.Fatalf("want the noun in the error, got %v", err)
	}
}

func TestParseDateFlag_NamesTheFlag(t *testing.T) {
	if _, err := parseDateFlag("due", "not-a-date"); err == nil || !strings.Contains(err.Error(), "--due") {
		t.Fatalf("want --due in the error, got %v", err)
	}
	d, err := parseDateFlag("as-of", "2026-01-31")
	if err != nil || d.GetYear() != 2026 || d.GetMonth() != 1 || d.GetDay() != 31 {
		t.Fatalf("got %v, %v", d, err)
	}
}

// TestOptHelpers_OnlyWhenPassed pins the "omit a flag to leave it alone"
// contract: a flag left at its default is nil, a flag passed - even with a
// zero/empty value - is sent.
func TestOptHelpers_OnlyWhenPassed(t *testing.T) {
	var name, price string
	var terms int32
	cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.Flags().StringVar(&name, "name", "", "")
	cmd.Flags().StringVar(&price, "price", "", "")
	cmd.Flags().Int32Var(&terms, "terms", 0, "")
	cmd.SetArgs([]string{"--name=", "--price", "1.50"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	r := run{cmd: cmd}
	if got := r.optString("name", &name); got == nil || *got != "" {
		t.Errorf("--name= was passed explicitly, want non-nil empty string, got %v", got)
	}
	if got := r.optDecimal("price", &price); got == nil || got.GetValue() != "1.50" {
		t.Errorf("optDecimal = %v", got)
	}
	if got := r.optInt32("terms", &terms); got != nil {
		t.Errorf("--terms not passed, want nil, got %d", *got)
	}
}

func TestColumns_RenderThroughGetters(t *testing.T) {
	inv := &denarixv1.Invoice{Id: 7, InvoiceNumber: "INV7", TotalAmount: &denarixv1.Decimal{Value: "10.00"}, ResourceVersion: 3}
	for _, c := range invoiceNoun.Columns {
		switch c.Header {
		case "ID":
			if got := c.Value(inv); got != "7" {
				t.Errorf("ID = %q", got)
			}
		case "NUMBER":
			if got := c.Value(inv); got != "INV7" {
				t.Errorf("NUMBER = %q", got)
			}
		case "TOTAL":
			if got := c.Value(inv); got != "10.00" {
				t.Errorf("TOTAL = %q", got)
			}
		case "POSTED":
			if got := c.Value(inv); got != "false" {
				t.Errorf("POSTED = %q", got)
			}
		case "BALANCE_DUE":
			if got := c.Value(inv); got != "" {
				t.Errorf("unset Decimal should render empty, got %q", got)
			}
		}
	}
	// A column never panics on the wrong message type.
	if got := invoiceNoun.Columns[0].Value(&denarixv1.Contact{Id: 1}); got != "" {
		t.Errorf("wrong type should render empty, got %q", got)
	}
}

// TestCommandTree_Builds walks the whole tree once so a broken builder
// (duplicate flag, nil closure) fails here rather than at first use.
func TestCommandTree_Builds(t *testing.T) {
	root := NewRootCmd()
	m := buildManifest(root)
	want := []string{"invoice update-lines", "estimate accept", "close reverse", "context get-attachment", "report customer-statement", "bank-statement unreconciled"}
	seen := map[string]bool{}
	for _, c := range m.Commands {
		seen[strings.Join(c.Path, " ")] = true
	}
	for _, w := range want {
		if !seen[w] {
			t.Errorf("command %q missing from tree", w)
		}
	}
}
