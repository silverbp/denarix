// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package pdf

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/silverbp/denarix/internal/reporting"
)

func fmtDate(t interface{ Format(string) string }) string {
	return t.Format("2006-01-02")
}

// RenderTrialBalance renders a TrialBalanceResult to PDF, with the same
// centered masthead and lineless table every report in this package uses
// now (see RenderBalanceSheet, RenderIncomeStatement).
func RenderTrialBalance(businessName string, asOf string, r *reporting.TrialBalanceResult) ([]byte, error) {
	d := New()
	d.ReportHeader(businessName, "Trial Balance", "As of "+asOf)

	cols := []TableColumn{
		{Header: "Code", Width: 0.15},
		{Header: "Account", Width: 0.45},
		{Header: "Debit", Width: 0.20, Right: true},
		{Header: "Credit", Width: 0.20, Right: true},
	}
	var rows [][]string
	for _, l := range r.Lines {
		rows = append(rows, []string{l.Code, l.Name, l.Debit.StringFixed(2), l.Credit.StringFixed(2)})
	}
	d.BorderlessTable(cols, rows, []string{"", "Total", r.TotalDebit.StringFixed(2), r.TotalCredit.StringFixed(2)})

	return d.Bytes()
}

// formatMoney renders a decimal the way this report family displays money:
// thousands-grouped, with parentheses instead of a leading minus for a
// negative value (standard accounting notation).
func formatMoney(v decimal.Decimal) string {
	s := groupThousands(v.Abs().StringFixed(2))
	if v.IsNegative() {
		return "(" + s + ")"
	}
	return s
}

// formatQuantity renders a line item's quantity consistently with the money
// columns beside it: thousands-grouped, parentheses instead of a leading
// minus for negative (a discount line posted as qty=-1), and its stored
// scale (DECIMAL(15,4)) trimmed of trailing zeros — "10.0000" as "10", not
// left at full precision like the raw proto value.
func formatQuantity(v string) string {
	if v == "" {
		v = "0"
	}
	dec, err := decimal.NewFromString(v)
	if err != nil {
		return v
	}
	s := groupThousands(dec.Abs().String())
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	if dec.IsNegative() {
		return "(" + s + ")"
	}
	return s
}

func groupThousands(s string) string {
	intPart, frac, hasFrac := strings.Cut(s, ".")
	var out []byte
	for i, c := range []byte(intPart) {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	if hasFrac {
		return string(out) + "." + frac
	}
	return string(out)
}

// formatMoneyTotal is formatMoney with the "$" a subtotal/grand-total row
// carries that a plain leaf line doesn't — matching the source report
// format's convention of a bare number on every account line but a
// dollar-prefixed figure on every "Total for X" and grand-total row.
func formatMoneyTotal(v decimal.Decimal) string {
	return "$" + formatMoney(v)
}

// accountLabel is a report line's left-hand label: "<code> <name>" for a
// real ledger_account, or just the name for reporting's synthetic "Current
// Period Earnings" line (AccountID 0 — no real ledger_account ever uses
// that id, see reporting.currentEarningsLineCode), which has no real code
// worth printing.
func accountLabel(l reporting.AccountLine) string {
	if l.AccountID == 0 {
		return l.Name
	}
	return l.Code + " " + l.Name
}

// Fixed section order from reporting.BalanceSheet's balanceSheetSectionOrder: Long-term Assets,
// Current Assets & Liabilities, Long-term Liabilities, Capital & Reserves, Opening Balances, then
// (only if non-empty, appended last) an Uncategorized catch-all.
const (
	bsIdxCurrent            = 1
	bsIdxCapitalAndReserves = 3
)

// bsSectionTitle is a BalanceSheetSection's display heading for one side of
// the report (asset vs. liability) — every section but "Current Assets &
// Liabilities" uses its own Title on both sides; that one section
// deliberately mixes both columns (see reporting.BalanceSheetSection), so
// it needs a column-specific label instead of showing "Current Assets &
// Liabilities" as a heading on both an assets-only and a liabilities-only
// subsection.
func bsSectionTitle(i int, title, assetLabel, liabilityLabel string) (string, string) {
	if i == bsIdxCurrent {
		return assetLabel, liabilityLabel
	}
	return title, title
}

// RenderBalanceSheet renders a BalanceSheetResult to PDF as a single
// "Total" column, modeled on the classic accounting-software layout: a
// shaded "Assets" bar over every asset-bearing section with its own
// indented "Total for X" subtotal, a bold grand "Total for Assets"; then
// the same shape again for "Liabilities and Equity" (liabilities first,
// equity last), closing on a bold, shaded "Total for Liabilities and
// Equity". reporting.BalanceSheet's derived UK-statutory-style subtotals
// (Net current assets, Total assets less current liabilities, Total net
// assets) aren't shown here — this layout doesn't use them.
func RenderBalanceSheet(businessName string, asOf string, r *reporting.BalanceSheetResult) ([]byte, error) {
	d := New()
	d.ReportHeader(businessName, "Balance Sheet", "As of "+asOf)
	d.ReportColumnHead("Total")

	d.ReportBar("Assets")
	for i, s := range r.Sections {
		if len(s.AssetLines) == 0 {
			continue
		}
		title, _ := bsSectionTitle(i, s.Title, "Current Assets", "Current Liabilities")
		d.ReportHeading(title, 1)
		for _, l := range s.AssetLines {
			d.ReportLine(accountLabel(l), formatMoney(l.Amount), 2)
		}
		d.ReportSubtotal("Total for "+title, formatMoneyTotal(s.TotalAssets), 1)
	}
	d.ReportGrandTotal("Total for Assets", formatMoneyTotal(r.TotalAssets))

	d.Spacer(2)
	d.ReportBar("Liabilities and Equity")
	d.ReportHeading("Liabilities", 1)
	liabilitiesTotal := decimal.Zero
	for i, s := range r.Sections {
		if i == bsIdxCapitalAndReserves || len(s.LiabilityLines) == 0 {
			continue
		}
		_, title := bsSectionTitle(i, s.Title, "Current Assets", "Current Liabilities")
		d.ReportHeading(title, 2)
		for _, l := range s.LiabilityLines {
			d.ReportLine(accountLabel(l), formatMoney(l.Amount), 3)
		}
		d.ReportSubtotal("Total for "+title, formatMoneyTotal(s.TotalLiabilities), 2)
		liabilitiesTotal = liabilitiesTotal.Add(s.TotalLiabilities)
	}
	d.ReportSubtotal("Total for Liabilities", formatMoneyTotal(liabilitiesTotal), 1)

	equity := r.Sections[bsIdxCapitalAndReserves]
	d.ReportHeading("Equity", 1)
	for _, l := range equity.LiabilityLines {
		d.ReportLine(accountLabel(l), formatMoney(l.Amount), 2)
	}
	d.ReportSubtotal("Total for Equity", formatMoneyTotal(equity.TotalLiabilities), 1)

	d.ReportGrandTotal("Total for Liabilities and Equity", formatMoneyTotal(r.TotalLiabilities))

	return d.Bytes()
}

// RenderIncomeStatement renders an IncomeStatementResult to PDF as a single
// "Total" column: a shaded "Revenue" bar over its lines and indented "Total
// for Revenue", the same for "Cost of Goods Sold", a bold shaded "Gross
// Profit" row, "Operating Expenses" and its total, then bold shaded "Net
// Operating Income" and "Net Income" closing rows — matching the classic
// accounting-software profit-and-loss layout (see RenderBalanceSheet).
// IncomeStatementResult has no separate other-income/other-expense
// category, so Net Operating Income and Net Income are the same figure
// printed twice, as that source layout does for a business with no
// non-operating activity.
func RenderIncomeStatement(businessName string, periodLabel string, r *reporting.IncomeStatementResult) ([]byte, error) {
	d := New()
	d.ReportHeader(businessName, "Income Statement", periodLabel)
	d.ReportColumnHead("Total")

	section := func(title string, lines []reporting.AccountLine, total decimal.Decimal) {
		d.ReportBar(title)
		for _, l := range lines {
			d.ReportLine(accountLabel(l), formatMoney(l.Amount), 1)
		}
		d.ReportSubtotal("Total for "+title, formatMoneyTotal(total), 0)
	}

	section("Revenue", r.Revenue, r.TotalRevenue)
	section("Cost of Goods Sold", r.CostOfGoodsSold, r.TotalCostOfGoodsSold)
	d.ReportGrandTotal("Gross Profit", formatMoneyTotal(r.GrossProfit))

	section("Operating Expenses", r.OperatingExpenses, r.TotalOperatingExpenses)
	d.ReportGrandTotal("Net Operating Income", formatMoneyTotal(r.NetIncome))
	d.ReportGrandTotal("Net Income", formatMoneyTotal(r.NetIncome))

	return d.Bytes()
}

// RenderGeneralLedger renders a GeneralLedgerResult to PDF, with the same
// centered masthead and lineless table every report in this package uses
// now (see RenderBalanceSheet, RenderIncomeStatement).
func RenderGeneralLedger(businessName string, periodLabel string, r *reporting.GeneralLedgerResult) ([]byte, error) {
	d := New()
	d.ReportHeader(businessName, fmt.Sprintf("General Ledger - %s %s", r.Code, r.Name), periodLabel)

	cols := []TableColumn{
		{Header: "Date", Width: 0.15},
		{Header: "Txn", Width: 0.15, Right: true},
		{Header: "Debit", Width: 0.2, Right: true},
		{Header: "Credit", Width: 0.2, Right: true},
		{Header: "Balance", Width: 0.3, Right: true},
	}
	var rows [][]string
	for _, l := range r.Lines {
		rows = append(rows, []string{
			fmtDate(l.TransactionDate), fmt.Sprintf("%d", l.LedgerTransactionID),
			l.Debit.StringFixed(2), l.Credit.StringFixed(2), l.RunningBalance.StringFixed(2),
		})
	}
	d.BorderlessTable(cols, rows, []string{"", "", "", "Ending Balance", r.EndingBalance.StringFixed(2)})

	return d.Bytes()
}

// RenderCustomerStatement renders a CustomerStatementResult to PDF: a
// WindowEnvelopeHeader (business/recipient print inside a double-window
// #10 envelope's windows, same as RenderInvoice/RenderEstimate) followed
// by the same centered masthead every report in this package uses (see
// RenderBalanceSheet, RenderIncomeStatement). Its Activity/Aging tables
// were already switched to BorderlessTable.
func RenderCustomerStatement(business, recipient Party, r *reporting.CustomerStatementResult) ([]byte, error) {
	d := New()
	d.WindowEnvelopeHeader(business, recipient)
	d.SetFooter(business)
	d.ReportHeader(business.Name, "Customer Statement - "+r.ContactName, fmt.Sprintf("%s through %s", fmtDate(r.PeriodStart), fmtDate(r.PeriodEnd)))

	d.SetSectionTitle("Activity")
	activityCols := []TableColumn{
		{Header: "Date", Width: 0.15},
		{Header: "Description", Width: 0.45},
		{Header: "Debit", Width: 0.13, Right: true},
		{Header: "Credit", Width: 0.13, Right: true},
		{Header: "Balance", Width: 0.14, Right: true},
	}
	var activityRows [][]string
	for _, a := range r.Activity {
		activityRows = append(activityRows, []string{
			fmtDate(a.Date), a.Description, a.Debit.StringFixed(2), a.Credit.StringFixed(2), a.RunningBalance.StringFixed(2),
		})
	}
	d.BorderlessTable(activityCols, activityRows, []string{"", "", "", "Ending Balance", r.EndingBalance.StringFixed(2)})

	d.Spacer(4)
	d.SetSectionTitle("Aging")
	agingCols := []TableColumn{
		{Header: "Current", Width: 0.2, Right: true},
		{Header: "1-30", Width: 0.2, Right: true},
		{Header: "31-60", Width: 0.2, Right: true},
		{Header: "61-90", Width: 0.2, Right: true},
		{Header: "90+", Width: 0.2, Right: true},
	}
	var agingRow []string
	for _, b := range r.AgingBuckets {
		agingRow = append(agingRow, b.Amount.StringFixed(2))
	}
	d.BorderlessTable(agingCols, [][]string{agingRow}, nil)

	return d.Bytes()
}
