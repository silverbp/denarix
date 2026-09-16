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

// RenderTrialBalance renders a TrialBalanceResult to PDF.
func RenderTrialBalance(businessName string, asOf string, r *reporting.TrialBalanceResult) ([]byte, error) {
	d := New()
	d.Header(businessName)
	d.Title("Trial Balance")
	d.Subtitle("As of " + asOf)

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
	d.Table(cols, rows, []string{"", "Total", r.TotalDebit.StringFixed(2), r.TotalCredit.StringFixed(2)})

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

// RenderBalanceSheet renders a BalanceSheetResult to PDF, grouped into the
// balance_sheet_category sections computed by reporting.BalanceSheet, with
// a two-column Asset/Liability layout and the derived subtotal rows between
// sections (Net current assets, Total assets less current liabilities,
// Total net assets) that a classic UK-style statutory balance sheet uses.
func RenderBalanceSheet(businessName string, asOf string, r *reporting.BalanceSheetResult) ([]byte, error) {
	d := New()
	d.Header(businessName)
	d.Title("Balance Sheet")
	d.Subtitle("As of " + asOf)

	cols := []TableColumn{
		{Header: "Account", Width: 0.5},
		{Header: "Asset", Width: 0.25, Right: true},
		{Header: "Liability", Width: 0.25, Right: true},
	}
	section := func(s reporting.BalanceSheetSection) {
		d.Spacer(3)
		d.SetSectionTitle(strings.ToUpper(s.Title))
		var rows [][]string
		for _, l := range s.AssetLines {
			rows = append(rows, []string{l.Name, formatMoney(l.Amount), ""})
		}
		for _, l := range s.LiabilityLines {
			rows = append(rows, []string{l.Name, "", formatMoney(l.Amount)})
		}
		d.Table(cols, rows, []string{s.Title + " (total)", formatMoney(s.TotalAssets), formatMoney(s.TotalLiabilities)})
	}

	// Fixed order from reporting.BalanceSheet: Long-term Assets, Current
	// Assets & Liabilities, Long-term Liabilities, Capital & Reserves, then
	// (only if non-empty) an Uncategorized catch-all.
	for i, s := range r.Sections {
		section(s)
		switch i {
		case 1: // after Current Assets & Liabilities
			d.SummaryLine("Net current assets (liabilities)", formatMoney(r.NetCurrentAssets))
			d.SummaryLine("Total assets less current liabilities", formatMoney(r.TotalAssetsLessCurrentLiabilities))
		case 2: // after Long-term Liabilities
			d.SummaryLine("Total net assets (liabilities)", formatMoney(r.TotalNetAssets))
		}
	}

	d.Spacer(3)
	d.Table(cols, nil, []string{"Total", formatMoney(r.TotalAssets), formatMoney(r.TotalLiabilities)})

	return d.Bytes()
}

// RenderIncomeStatement renders an IncomeStatementResult to PDF.
func RenderIncomeStatement(businessName string, periodLabel string, r *reporting.IncomeStatementResult) ([]byte, error) {
	d := New()
	d.Header(businessName)
	d.Title("Income Statement")
	d.Subtitle(periodLabel)

	cols := []TableColumn{
		{Header: "Code", Width: 0.2},
		{Header: "Account", Width: 0.55},
		{Header: "Amount", Width: 0.25, Right: true},
	}
	section := func(title string, lines []reporting.AccountLine, total interface{ StringFixed(int32) string }) {
		d.Spacer(3)
		d.SetSectionTitle(title)
		var rows [][]string
		for _, l := range lines {
			rows = append(rows, []string{l.Code, l.Name, l.Amount.StringFixed(2)})
		}
		d.Table(cols, rows, []string{"", "Total " + title, total.StringFixed(2)})
	}
	section("Revenue", r.Revenue, r.TotalRevenue)

	d.Spacer(3)
	d.SummaryLine("Total Revenue", r.TotalRevenue.StringFixed(2))

	section("Cost of Goods Sold", r.CostOfGoodsSold, r.TotalCostOfGoodsSold)

	d.Spacer(3)
	d.SummaryLine("Gross Profit", r.GrossProfit.StringFixed(2))

	section("Operating Expenses", r.OperatingExpenses, r.TotalOperatingExpenses)

	d.Spacer(3)
	d.SummaryLine("Total Expenses", r.TotalExpenses.StringFixed(2))
	d.SummaryLine("Net Income", r.NetIncome.StringFixed(2))

	return d.Bytes()
}

// RenderGeneralLedger renders a GeneralLedgerResult to PDF.
func RenderGeneralLedger(businessName string, periodLabel string, r *reporting.GeneralLedgerResult) ([]byte, error) {
	d := New()
	d.Header(businessName)
	d.Title(fmt.Sprintf("General Ledger - %s %s", r.Code, r.Name))
	d.Subtitle(periodLabel)

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
	d.Table(cols, rows, []string{"", "", "", "Ending Balance", r.EndingBalance.StringFixed(2)})

	return d.Bytes()
}

// RenderCustomerStatement renders a CustomerStatementResult to PDF.
func RenderCustomerStatement(businessName string, r *reporting.CustomerStatementResult) ([]byte, error) {
	d := New()
	d.Header(businessName)
	d.Title("Customer Statement - " + r.ContactName)
	d.Subtitle(fmt.Sprintf("%s through %s", fmtDate(r.PeriodStart), fmtDate(r.PeriodEnd)))

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
	d.Table(activityCols, activityRows, []string{"", "", "", "Ending Balance", r.EndingBalance.StringFixed(2)})

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
	d.Table(agingCols, [][]string{agingRow}, nil)

	return d.Bytes()
}
