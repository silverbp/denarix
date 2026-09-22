// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package pdf

import (
	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
)

// RenderEstimate renders an *denarixv1.Estimate (with its line items already
// populated) to PDF. Mirrors RenderInvoice's layout — same windowed
// address header, line item table, and tax breakdown — since an estimate
// is the same kind of pre-sale document, just without payment/balance
// fields.
func RenderEstimate(business, customer Party, est *denarixv1.Estimate, breakdown []TaxBreakdownRow) ([]byte, error) {
	d := New()
	d.WindowEnvelopeHeader(business, customer)
	d.SetFooter(business)

	d.CenteredTitle("Estimate")

	d.KeyValueRow("Estimate Number", est.GetEstimateNumber())
	d.KeyValueRow("Estimate Date", formatProtoDate(est.GetEstimateDate()))
	d.KeyValueRow("Expiration Date", formatProtoDate(est.GetExpirationDate()))
	d.KeyValueRow("Status", est.GetStatus())
	d.Spacer(4)

	cols := []TableColumn{
		{Header: "Description", Width: 0.5},
		{Header: "Qty", Width: 0.12, Right: true},
		{Header: "Unit Price", Width: 0.18, Right: true},
		{Header: "Line Total", Width: 0.2, Right: true},
	}
	var rows [][]string
	for _, li := range est.GetLineItems() {
		rows = append(rows, []string{
			li.GetDescription(),
			formatQuantity(li.GetQuantity().GetValue()),
			formatMoneyString(li.GetUnitPrice().GetValue()),
			formatMoneyString(li.GetLineSubtotal().GetValue()),
		})
	}
	d.BorderlessTable(cols, rows, nil)

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
		d.BorderlessTable(breakdownCols, breakdownRows, nil)
	}

	d.Spacer(2)
	d.SummaryBlock([]SummaryRow{
		{Label: "Subtotal", Value: formatMoneyString(est.GetSubtotal().GetValue())},
		{Label: "Tax", Value: formatMoneyString(est.GetTotalTaxAmount().GetValue())},
		{Label: "Total", Value: formatMoneyString(est.GetTotalAmount().GetValue()), Bold: true, Divider: true},
	})

	return d.Bytes()
}
