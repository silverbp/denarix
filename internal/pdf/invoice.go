// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package pdf

import (
	"fmt"

	"github.com/shopspring/decimal"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
)

// TaxBreakdownRow is one row of an invoice's tax breakdown, grouped by tax
// rate. Invoice PDFs conventionally roll tax up by rate at the bottom
// rather than showing it as a per-line column, since most line items on a
// given invoice carry no tax at all.
type TaxBreakdownRow struct {
	Label string
	Net   decimal.Decimal
	Tax   decimal.Decimal
	Total decimal.Decimal
}

// RenderInvoice renders an *denarixv1.Invoice (with its line items already
// populated) to PDF. Takes the proto type directly, unlike the report
// renderers — an invoice is a document the API already returns fully
// formed, with no separate Go-native computation layer to render from.
func RenderInvoice(business, billTo Party, inv *denarixv1.Invoice, breakdown []TaxBreakdownRow) ([]byte, error) {
	d := New()
	d.AddressBlock(billTo, business)
	d.SetFooter(business)

	title := "Sales Invoice"
	if inv.GetInvoiceType() == "PURCHASE" {
		title = "Purchase Bill"
	}
	d.CenteredTitle(title)

	d.KeyValueRow("Invoice Number", inv.GetInvoiceNumber())
	d.KeyValueRow("Invoice Date", formatProtoDate(inv.GetInvoiceDate()))
	d.KeyValueRow("Due Date", formatProtoDate(inv.GetDueDate()))
	d.KeyValueRow("Status", inv.GetStatus())
	d.Spacer(4)

	cols := []TableColumn{
		{Header: "Description", Width: 0.5},
		{Header: "Qty", Width: 0.12, Right: true},
		{Header: "Unit Price", Width: 0.18, Right: true},
		{Header: "Line Total", Width: 0.2, Right: true},
	}
	var rows [][]string
	for _, li := range inv.GetLineItems() {
		rows = append(rows, []string{
			li.GetDescription(),
			formatQuantity(li.GetQuantity().GetValue()),
			formatMoneyString(li.GetUnitPrice().GetValue()),
			// LineTotal is tax-inclusive (LineSubtotal + TaxAmount); shown
			// per-line without a tax figure next to it, that reads as if
			// tax were silently baked in. LineSubtotal (qty * unit price)
			// is what "Line Total" should show here — tax is rolled up
			// separately in the breakdown table and summary below.
			formatMoneyString(li.GetLineSubtotal().GetValue()),
		})
	}
	d.Table(cols, rows, nil)

	if showsTaxBreakdown(breakdown) {
		d.Spacer(2)
		d.SetSectionTitle("Tax Breakdown")
		breakdownCols := []TableColumn{
			{Header: "Rate", Width: 0.4},
			{Header: "Net", Width: 0.2, Right: true},
			{Header: "Tax", Width: 0.2, Right: true},
			{Header: "Total", Width: 0.2, Right: true},
		}
		var breakdownRows [][]string
		for _, b := range breakdown {
			breakdownRows = append(breakdownRows, []string{b.Label, formatMoney(b.Net), formatMoney(b.Tax), formatMoney(b.Total)})
		}
		d.Table(breakdownCols, breakdownRows, nil)
	}

	d.Spacer(2)
	d.SummaryBlock([]SummaryRow{
		{Label: "Subtotal", Value: formatMoneyString(inv.GetSubtotal().GetValue())},
		{Label: "Tax", Value: formatMoneyString(inv.GetTotalTaxAmount().GetValue())},
		{Label: "Total", Value: formatMoneyString(inv.GetTotalAmount().GetValue()), Bold: true, Divider: true},
		{Label: "Paid", Value: formatMoneyString(inv.GetPaidAmount().GetValue())},
		{Label: "Balance Due", Value: formatMoneyString(inv.GetBalanceDue().GetValue()), Bold: true, Divider: true},
	})

	return d.Bytes()
}

// formatMoneyString reformats a Decimal proto's raw value as a two-decimal
// money amount. The raw value carries whatever scale the stored NUMERIC
// happened to have, and an unset Decimal (e.g. a line item with no tax)
// comes through as an empty string rather than "0" — both are treated as
// zero so every amount renders as "0.00" instead of some inconsistent or
// blank.
func formatMoneyString(v string) string {
	if v == "" {
		v = "0"
	}
	dec, err := decimal.NewFromString(v)
	if err != nil {
		return v
	}
	return formatMoney(dec)
}

// showsTaxBreakdown reports whether the breakdown table is worth printing:
// skip it when there's a single untaxed bucket, since the Tax line below
// already says 0.00.
func showsTaxBreakdown(breakdown []TaxBreakdownRow) bool {
	if len(breakdown) > 1 {
		return true
	}
	return len(breakdown) == 1 && !breakdown[0].Tax.IsZero()
}

func formatProtoDate(d interface {
	GetYear() int32
	GetMonth() int32
	GetDay() int32
}) string {
	if d == nil {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", d.GetYear(), d.GetMonth(), d.GetDay())
}
