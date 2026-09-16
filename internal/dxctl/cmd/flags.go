// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	typepb "google.golang.org/genproto/googleapis/type/date"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
)

// parseID parses a positional <id> argument, naming the noun in the error.
func parseID(noun, s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s id %q: %w", noun, s, err)
	}
	return n, nil
}

// addResourceVersionFlag registers the optimistic-concurrency flag every
// update/deactivate/status verb takes - the same wording everywhere, so
// `--help` reads identically across nouns. 0 (the default) sends no
// precondition; see Business.resource_version in proto/denarix/v1/business.proto.
func addResourceVersionFlag(cmd *cobra.Command, dst *int64) {
	cmd.Flags().Int64Var(dst, "resource-version", 0,
		"only apply if the resource is still at this resource_version (the VERSION column of get/list); omit to write unconditionally")
}

// parseDateFlag parses a YYYY-MM-DD flag value into a google.type.Date.
// flag names the flag in the error ("due", "as-of", ...).
func parseDateFlag(flag, s string) (*typepb.Date, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, fmt.Errorf("invalid --%s %q: expected YYYY-MM-DD", flag, s)
	}
	return &typepb.Date{Year: int32(t.Year()), Month: int32(t.Month()), Day: int32(t.Day())}, nil
}

// Optional-field helpers for create/update requests. Each returns nil
// unless the named flag was actually passed on the command line, which is
// the "only flags you pass are sent - omit a flag to leave that field
// unchanged" contract every update verb documents (and, on create, what
// lets the server apply its own default). A request literal then reads one
// field per line: Name: r.optString("name", &name).

func (r run) changed(flag string) bool { return r.cmd.Flags().Changed(flag) }

func (r run) optString(flag string, v *string) *string {
	if !r.changed(flag) {
		return nil
	}
	return v
}

func (r run) optInt32(flag string, v *int32) *int32 {
	if !r.changed(flag) {
		return nil
	}
	return v
}

func (r run) optInt64(flag string, v *int64) *int64 {
	if !r.changed(flag) {
		return nil
	}
	return v
}

func (r run) optBool(flag string, v *bool) *bool {
	if !r.changed(flag) {
		return nil
	}
	return v
}

func (r run) optDecimal(flag string, v *string) *denarixv1.Decimal {
	if !r.changed(flag) {
		return nil
	}
	return &denarixv1.Decimal{Value: *v}
}

func (r run) optDate(flag string, v *string) (*typepb.Date, error) {
	if !r.changed(flag) {
		return nil, nil
	}
	return parseDateFlag(flag, *v)
}

// lineFlagKeys is every key a --line accepts, for estimate and invoice alike. item is
// required (every line references a catalog item - no free-text lines); the rest override
// that item's defaults. There is deliberately no account= key: an invoice line always posts
// to its item's default_ledger_account_id.
var lineFlagKeys = map[string]bool{
	"item": true, "desc": true, "qty": true, "price": true, "taxable": true, "tax-rate": true,
}

// lineFlagHelp is the shared --line usage string for estimate/invoice create and update-lines.
const lineFlagHelp = "item=<id>[,desc=...][,qty=...][,price=...][,taxable][,tax-rate=<id>] (repeatable) - desc/price/taxable/tax-rate default from the item when omitted"

// parseLineFlags parses repeatable --line "key=value,key=value" flags,
// shared by estimate/invoice create and update-lines, into ordered field
// maps (line_number is 1-based position in the flag list). Unknown keys are
// an error rather than silently dropped, so a stale account=<id> fails loudly.
func parseLineFlags(raw []string) ([]map[string]string, error) {
	lines := make([]map[string]string, 0, len(raw))
	for _, r := range raw {
		fields := map[string]string{}
		for _, part := range strings.Split(r, ",") {
			if part == "" {
				continue
			}
			kv := strings.SplitN(part, "=", 2)
			if !lineFlagKeys[kv[0]] {
				return nil, fmt.Errorf("invalid --line %q: unknown key %q (want one of item, desc, qty, price, taxable, tax-rate)", r, kv[0])
			}
			if len(kv) == 1 {
				fields[kv[0]] = "true" // bare flag, e.g. "taxable"
				continue
			}
			fields[kv[0]] = kv[1]
		}
		if _, ok := fields["item"]; !ok {
			return nil, fmt.Errorf("invalid --line %q: missing item=<id> (every line must reference a catalog item)", r)
		}
		lines = append(lines, fields)
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("at least one --line is required")
	}
	return lines, nil
}

// newDocumentLineItems maps --line flags (parseLineFlags) onto the request
// shape shared by estimate and invoice. line_number is the 1-based position
// in the flag list.
func newDocumentLineItems(rawLines []string) ([]*denarixv1.NewDocumentLineItem, error) {
	rawFields, err := parseLineFlags(rawLines)
	if err != nil {
		return nil, err
	}
	lineItems := make([]*denarixv1.NewDocumentLineItem, 0, len(rawFields))
	for i, f := range rawFields {
		itemID, err := parseRequiredInt64(f, "item")
		if err != nil {
			return nil, err
		}
		taxRateID, err := parseOptionalInt64(f, "tax-rate")
		if err != nil {
			return nil, err
		}
		lineItems = append(lineItems, &denarixv1.NewDocumentLineItem{
			ItemId:      itemID,
			LineNumber:  int32(i + 1),
			Description: f["desc"],
			Quantity:    parseDecimalField(f, "qty"),
			UnitPrice:   parseDecimalField(f, "price"),
			IsTaxable:   parseOptionalBool(f, "taxable"),
			TaxRateId:   taxRateID,
		})
	}
	return lineItems, nil
}

// parseRequiredInt64 is parseOptionalInt64 for a key parseLineFlags has
// already guaranteed is present (item).
func parseRequiredInt64(fields map[string]string, key string) (int64, error) {
	n, err := parseOptionalInt64(fields, key)
	if err != nil {
		return 0, err
	}
	if n == nil {
		return 0, fmt.Errorf("%s= is required", key)
	}
	return *n, nil
}

func parseOptionalInt64(fields map[string]string, key string) (*int64, error) {
	v, ok := fields[key]
	if !ok {
		return nil, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return &n, nil
}

func parseDecimalField(fields map[string]string, key string) *denarixv1.Decimal {
	v, ok := fields[key]
	if !ok {
		return nil
	}
	return &denarixv1.Decimal{Value: v}
}

// parseOptionalBool distinguishes "not set at all" (nil - an item's own
// default applies, if the line has one) from an explicit true/false.
func parseOptionalBool(fields map[string]string, key string) *bool {
	v, ok := fields[key]
	if !ok {
		return nil
	}
	b := v == "true"
	return &b
}

// parseEntryFlags parses repeatable --entry "account=<id>,debit=<amt>" or
// "account=<id>,credit=<amt>" flags for `ledger-transaction post`.
func parseEntryFlags(raw []string) ([]*denarixv1.NewLedgerEntry, error) {
	var entries []*denarixv1.NewLedgerEntry
	for _, r := range raw {
		fields := map[string]string{}
		for _, part := range strings.Split(r, ",") {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) != 2 {
				return nil, fmt.Errorf("invalid --entry %q: expected comma-separated key=value pairs", r)
			}
			fields[kv[0]] = kv[1]
		}

		accountStr, ok := fields["account"]
		if !ok {
			return nil, fmt.Errorf("invalid --entry %q: missing account=", r)
		}
		accountID, err := strconv.ParseInt(accountStr, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid --entry %q: account must be an integer", r)
		}

		ne := &denarixv1.NewLedgerEntry{AccountId: int32(accountID)}
		if debit, ok := fields["debit"]; ok {
			ne.DebitAmount = &denarixv1.Decimal{Value: debit}
		}
		if credit, ok := fields["credit"]; ok {
			ne.CreditAmount = &denarixv1.Decimal{Value: credit}
		}
		entries = append(entries, ne)
	}
	return entries, nil
}

// parsePaymentApplyFlags parses repeatable --apply "invoice_id:amount"
// flags into PaymentApplicationInput values.
func parsePaymentApplyFlags(raw []string) ([]*denarixv1.PaymentApplicationInput, error) {
	applications := make([]*denarixv1.PaymentApplicationInput, 0, len(raw))
	for _, r := range raw {
		invoiceID, amount, ok := strings.Cut(r, ":")
		if !ok {
			return nil, fmt.Errorf("invalid --apply %q, want invoice_id:amount", r)
		}
		id, err := strconv.ParseInt(invoiceID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid --apply %q: invoice_id: %w", r, err)
		}
		applications = append(applications, &denarixv1.PaymentApplicationInput{
			InvoiceId:     id,
			AppliedAmount: &denarixv1.Decimal{Value: amount},
		})
	}
	return applications, nil
}
