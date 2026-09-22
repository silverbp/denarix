// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

// Package pdf renders reports and trading documents to PDF using
// github.com/go-pdf/fpdf — a pure-Go library, so cmd/denarix stays a single
// static binary with no runtime dependency (no headless Chrome, no
// wkhtmltopdf). fpdf supports embedding PNG/JPEG images directly, so a
// business logo can be added later once there's a binary source for one
// (business.logo_url today is just a URL string, not stored image bytes —
// that needs Phase 8's attachment infrastructure first).
package pdf

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/go-pdf/fpdf"
)

const (
	// pageWidth/pageHeight are US Letter (8.5in x 11in) — chosen over A4 so
	// invoices, estimates, and customer statements can be tri-folded into a
	// standard #10 window envelope (see WindowEnvelopeHeader); every report
	// in this package uses the same page size for consistency even though
	// only those three documents rely on it.
	pageWidth   = 215.9
	pageHeight  = 279.4
	marginLeft  = 15.0
	marginRight = 15.0
	// marginBottom is generous enough to fit Document.SetFooter's rule plus
	// several lines of text (a business name, a two-line address, phone,
	// email) without colliding with the page's normal content.
	marginBottom = 30.0
	// marginTop matches the value passed to SetMargins below — pulled out
	// as a constant so renderHeader can reset the cursor to it after
	// drawing the page-number stamp, which sits above it in the gutter.
	marginTop = 15.0
	contentW  = pageWidth - marginLeft - marginRight
)

// Document wraps fpdf with the layout conventions shared by every report
// and trading document this package renders: a business header, a title,
// and bordered tables.
type Document struct {
	pdf *fpdf.Fpdf
	// tr converts UTF-8 Go strings to cp1252, which fpdf's core Helvetica
	// font expects — every string handed to fpdf goes through this. Without
	// it, any non-ASCII character (an accented business name, a curly quote
	// pasted from a word processor, an em dash) renders as mojibake: fpdf's
	// core fonts don't auto-transcode UTF-8 input.
	tr func(string) string
	// footer, if set via SetFooter, is printed at the bottom of every page.
	footer *Party
	// activeTableHeader, while non-nil, is invoked by renderHeader on every
	// page break so a table's column-header row reprints at the top of
	// each continuation page — set by BorderlessTable/Table around their
	// row loop, cleared once the table finishes. Without this, a table
	// that spans a page break opens its continuation page with data rows
	// and no header, which is disorienting on anything long enough to
	// paginate (a general ledger, a busy customer statement).
	activeTableHeader func()
}

// New starts a new US Letter document with margins set, page-number
// aliasing enabled (see renderHeader), and the first page added.
func New() *Document {
	p := fpdf.New("P", "mm", "Letter", "")
	p.SetMargins(marginLeft, marginTop, marginRight)
	p.SetAutoPageBreak(true, marginBottom)
	p.AliasNbPages("")
	d := &Document{pdf: p, tr: p.UnicodeTranslatorFromDescriptor("")}
	p.SetFooterFunc(func() { d.renderFooter() })
	p.SetHeaderFunc(func() { d.renderHeader() })
	p.AddPage()
	return d
}

// renderHeader runs at the top of every page (including the first) via
// SetHeaderFunc: it stamps a small "Page X of Y" marker in the gutter
// above the normal content area, then — if a table is mid-render when the
// page breaks — reprints that table's column-header row (see
// activeTableHeader) before resetting the cursor to the page's normal
// content origin.
func (d *Document) renderHeader() {
	d.pdf.SetFont("Helvetica", "", 8)
	d.pdf.SetTextColor(120, 120, 120)
	d.pdf.SetXY(pageWidth-marginRight-40, 7)
	d.pdf.CellFormat(40, 4, d.tr(fmt.Sprintf("Page %d of {nb}", d.pdf.PageNo())), "", 0, "R", false, 0, "")
	d.pdf.SetTextColor(0, 0, 0)
	d.pdf.SetXY(marginLeft, marginTop)
	if d.activeTableHeader != nil {
		d.activeTableHeader()
	}
}

// reserve forces an explicit page break first if fewer than h mm remain
// above the bottom margin, so a block that must print as one visual unit —
// several SummaryBlock rows, a rule-bracketed report subtotal — never gets
// split by a page break landing in the middle of it. A single CellFormat
// row can't be split this way (fpdf's own auto-page-break already moves an
// entire cell row to the next page rather than clipping it mid-row); this
// covers the multi-row case fpdf has no native "keep together" for.
func (d *Document) reserve(h float64) {
	if d.pdf.GetY()+h > pageHeight-marginBottom {
		d.pdf.AddPage()
	}
}

// SetFooter prints p's name and detail lines, one per centered row, below
// a horizontal rule at the bottom of every page — e.g. the business's
// identity and address, so a multi-page document still identifies itself
// on later pages. Each line gets its own row rather than being joined onto
// one, so a full mailing address wraps naturally instead of being squeezed
// into a single long line.
func (d *Document) SetFooter(p Party) {
	d.footer = &p
}

func (d *Document) renderFooter() {
	if d.footer == nil {
		return
	}
	lines := append([]string{d.footer.Name}, d.footer.Lines...)
	lines = append(lines, d.footer.Contact...)
	const lineH = 3.5
	height := 3.0 + float64(len(lines))*lineH + 2.0
	d.pdf.SetY(-height)
	d.pdf.SetDrawColor(180, 180, 180)
	d.pdf.Line(marginLeft, d.pdf.GetY(), pageWidth-marginRight, d.pdf.GetY())
	d.pdf.Ln(1.5)
	d.pdf.SetX(marginLeft)
	d.pdf.SetFont("Helvetica", "", 7.5)
	d.pdf.SetTextColor(120, 120, 120)
	for _, l := range lines {
		d.pdf.CellFormat(contentW, lineH, d.tr(l), "", 1, "C", false, 0, "")
	}
	d.pdf.SetTextColor(0, 0, 0)
}

// Header prints the business name (bold) and any additional lines
// (address, phone, email — caller's choice) below it, left-aligned.
func (d *Document) Header(businessName string, lines ...string) {
	d.pdf.SetFont("Helvetica", "B", 14)
	d.pdf.CellFormat(contentW, 7, d.tr(businessName), "", 1, "L", false, 0, "")
	d.pdf.SetFont("Helvetica", "", 9)
	for _, l := range lines {
		d.pdf.CellFormat(contentW, 5, d.tr(l), "", 1, "L", false, 0, "")
	}
	d.pdf.Ln(4)
}

// Title prints a document title, e.g. "INVOICE INV1000" or "Trial Balance".
func (d *Document) Title(text string) {
	d.pdf.SetFont("Helvetica", "B", 16)
	d.pdf.CellFormat(contentW, 9, d.tr(text), "", 1, "L", false, 0, "")
	d.pdf.Ln(2)
}

// CenteredTitle prints a centered, italicized document title — for
// documents that head with an AddressBlock instead of Header's plain
// business name, e.g. "Sales Invoice" above an invoice's Bill To/business
// address columns.
func (d *Document) CenteredTitle(text string) {
	d.pdf.SetFont("Helvetica", "BI", 16)
	d.pdf.CellFormat(contentW, 9, d.tr(text), "", 1, "C", false, 0, "")
	d.pdf.Ln(2)
}

// Party is a name plus two kinds of detail line: Lines is the mailing
// address alone — exactly what's safe to print inside a window envelope's
// die-cut window (see WindowEnvelopeHeader), nothing else — and Contact is
// everything else (phone, email, ...), which prints alongside Lines in a
// flowing AddressBlock or a page footer (SetFooter) but is never put
// inside a fixed-position window.
type Party struct {
	Name    string
	Lines   []string
	Contact []string
}

// AddressBlock prints two Party blocks side by side, e.g. the customer
// being billed on the left and the business issuing the document on the
// right — each as a bold name line followed by its Lines then Contact
// detail lines. Superseded by WindowEnvelopeHeader for invoices, estimates,
// and customer statements, which need their recipient address at a fixed
// position instead of wherever this flowing two-column layout lands it;
// kept for any future document that wants a flowing address block instead.
func (d *Document) AddressBlock(left, right Party) {
	colW := contentW / 2
	startY := d.pdf.GetY()

	leftEndY := d.partyColumn(marginLeft, startY, colW, left)
	rightEndY := d.partyColumn(marginLeft+colW, startY, colW, right)

	endY := leftEndY
	if rightEndY > endY {
		endY = rightEndY
	}
	d.pdf.SetXY(marginLeft, endY)
	d.pdf.Ln(4)
}

// partyColumn prints one Party's name, Lines, and Contact detail lines at
// (x, y), each line its own row so the two columns of an AddressBlock
// don't have to have the same number of lines, and returns the y position
// just past its last line.
func (d *Document) partyColumn(x, y, w float64, p Party) float64 {
	d.pdf.SetXY(x, y)
	d.pdf.SetFont("Helvetica", "B", 10)
	d.pdf.CellFormat(w, 5.5, d.tr(p.Name), "", 0, "L", false, 0, "")
	d.pdf.SetFont("Helvetica", "", 9)
	all := append(append([]string{}, p.Lines...), p.Contact...)
	for i, l := range all {
		d.pdf.SetXY(x, y+5.5+float64(i)*5)
		d.pdf.CellFormat(w, 5, d.tr(l), "", 0, "L", false, 0, "")
	}
	return y + 5.5 + float64(len(all))*5
}

// Fixed window positions for a standard double-window #10 invoice envelope
// on US Letter paper, tri-folded — the convention most double-window
// invoice envelope products (and Word's own double-window envelope
// template) are built around: a return-address window near the top-left,
// a delivery-address window lower and offset to the right. Exact offsets
// vary a little between envelope manufacturers, so these leave generous
// padding inside each window; verify against actual envelope stock before
// a real print run.
const (
	envReturnX, envReturnY, envReturnW, envReturnH         = 12.7, 12.7, 88.9, 19.05 // 0.5in, 0.5in, 3.5in, 0.75in
	envDeliveryX, envDeliveryY, envDeliveryW, envDeliveryH = 101.6, 61.0, 88.9, 25.4 // 4in, 2.4in, 3.5in, 1in
)

// WindowEnvelopeHeader prints business (the return address) and recipient
// (the delivery address) at the fixed positions above, instead of
// AddressBlock's flowing two-column layout, so both addresses land inside
// a double-window #10 envelope's die-cut windows once the page is
// tri-folded. Only Name and Lines print — never Contact (phone/email),
// which would spill outside the window; put that on SetFooter instead.
// Resets the cursor to just below the delivery window when done, so the
// caller's next call (CenteredTitle, ReportHeader, ...) continues in the
// normal content flow.
func (d *Document) WindowEnvelopeHeader(business, recipient Party) {
	d.windowBlock(envReturnX, envReturnY, envReturnW, envReturnH, business)
	d.windowBlock(envDeliveryX, envDeliveryY, envDeliveryW, envDeliveryH, recipient)
	d.pdf.SetXY(marginLeft, envDeliveryY+envDeliveryH+6)
}

// windowBlock prints p's Name (bold) and Lines at a fixed (x, y), wrapping
// none of it — a window envelope's address block is a handful of short
// lines by construction (a street address, a city/state/zip), so clipping
// never comes up in practice the way it does for a table cell.
func (d *Document) windowBlock(x, y, w, h float64, p Party) {
	d.pdf.SetXY(x, y)
	d.pdf.SetFont("Helvetica", "B", 9.5)
	d.pdf.CellFormat(w, 4.5, d.tr(p.Name), "", 2, "L", false, 0, "")
	d.pdf.SetFont("Helvetica", "", 9)
	for _, l := range p.Lines {
		d.pdf.SetX(x)
		d.pdf.CellFormat(w, 4.2, d.tr(l), "", 2, "L", false, 0, "")
	}
}

// Subtitle prints a smaller line under the title, e.g. a date range or
// as-of date.
func (d *Document) Subtitle(text string) {
	d.pdf.SetFont("Helvetica", "I", 10)
	d.pdf.CellFormat(contentW, 6, d.tr(text), "", 1, "L", false, 0, "")
	d.pdf.Ln(2)
}

// KeyValueRow prints a label/value pair on one line — for a document's
// metadata block (invoice number, date, due date, bill-to, ...).
func (d *Document) KeyValueRow(label, value string) {
	d.pdf.SetFont("Helvetica", "B", 9)
	d.pdf.CellFormat(35, 5.5, d.tr(label), "", 0, "L", false, 0, "")
	d.pdf.SetFont("Helvetica", "", 9)
	d.pdf.CellFormat(contentW-35, 5.5, d.tr(value), "", 1, "L", false, 0, "")
}

// SummaryRow is one row of a Document.SummaryBlock: a label/value pair,
// optionally emphasized (bold, larger) and/or preceded by a divider rule —
// e.g. an invoice's Total or Balance Due standing out from its plain
// Subtotal/Tax/Paid rows above.
type SummaryRow struct {
	Label   string
	Value   string
	Bold    bool
	Divider bool
}

// SummaryBlock prints a label/value block anchored to the content area's
// right edge — lining it up under the right-hand columns of the table
// above it — instead of KeyValueRow/MoneyRow's left margin. Reserves its
// own height first (see Document.reserve) so a page break can't land
// between two of its rows — e.g. separating "Total" from "Balance Due".
func (d *Document) SummaryBlock(rows []SummaryRow) {
	const blockW = 75.0
	labelW := blockW * 0.5
	valueW := blockW - labelW
	x := marginLeft + contentW - blockW

	var need float64
	for _, r := range rows {
		if r.Divider {
			need += 1.5
		}
		if r.Bold {
			need += 7
		} else {
			need += 5.5
		}
	}
	d.reserve(need)

	for _, r := range rows {
		style, size, h := "", 9.0, 5.5
		if r.Bold {
			style, size, h = "B", 11, 7
		}
		if r.Divider {
			y := d.pdf.GetY()
			d.pdf.SetDrawColor(180, 180, 180)
			d.pdf.Line(x, y, x+blockW, y)
			d.pdf.Ln(1.5)
			d.pdf.SetDrawColor(0, 0, 0)
		}
		d.pdf.SetX(x)
		d.pdf.SetFont("Helvetica", style, size)
		d.pdf.CellFormat(labelW, h, d.tr(r.Label), "", 0, "L", false, 0, "")
		d.pdf.CellFormat(valueW, h, d.tr(r.Value), "", 1, "R", false, 0, "")
	}
}

func (d *Document) Spacer(h float64) {
	d.pdf.Ln(h)
}

// SummaryLine prints a bold label/value line with no border — for a
// derived subtotal that stands outside any bordered table (e.g. a balance
// sheet's "Net current assets" row between sections).
func (d *Document) SummaryLine(label, value string) {
	d.pdf.SetFont("Helvetica", "B", 10)
	d.pdf.CellFormat(contentW*0.75, 6, d.tr(label), "", 0, "L", false, 0, "")
	d.pdf.CellFormat(contentW*0.25, 6, d.tr(value), "", 1, "R", false, 0, "")
}

// SetSectionTitle prints a bold section heading, e.g. "Assets" on a
// balance sheet or "Activity" on a statement.
func (d *Document) SetSectionTitle(text string) {
	d.pdf.SetFont("Helvetica", "B", 11)
	d.pdf.CellFormat(contentW, 6, d.tr(text), "", 1, "L", false, 0, "")
}

// reportRuleGray and reportBarGray are the two shades the single-column
// report layout (RenderBalanceSheet, RenderIncomeStatement) uses in place
// of a bordered grid: a light rule bracketing a subtotal row, and a darker
// fill for a top-level section bar and the closing grand-total row.
var (
	reportRuleGray = [3]int{160, 160, 160}
	reportBarGray  = [3]int{225, 225, 225}
)

// reportValueColW is the fixed width of a single-column report's right-hand
// money column (ReportLine/ReportSubtotal/ReportGrandTotal) — wide enough
// for a seven-figure total in parentheses.
const reportValueColW = 32.0

// ReportHeader prints the centered masthead a single-column report opens
// with: the business name (bold), the report title, and an italicized
// period/as-of line beneath it — e.g. "Luxury Landscapes, LLC" / "Balance
// Sheet" / "As of Aug 31, 2026".
func (d *Document) ReportHeader(businessName, title, subtitle string) {
	d.pdf.SetFont("Helvetica", "B", 15)
	d.pdf.CellFormat(contentW, 7, d.tr(businessName), "", 1, "C", false, 0, "")
	d.pdf.SetFont("Helvetica", "", 10)
	d.pdf.CellFormat(contentW, 5.5, d.tr(title), "", 1, "C", false, 0, "")
	d.pdf.SetFont("Helvetica", "I", 9)
	d.pdf.CellFormat(contentW, 5, d.tr(subtitle), "", 1, "C", false, 0, "")
	d.pdf.Ln(5)
}

// ReportColumnHead prints a single right-aligned column label ("Total")
// over a full-width rule — the header row of a single-column report.
func (d *Document) ReportColumnHead(label string) {
	d.pdf.SetFont("Helvetica", "", 9)
	d.pdf.CellFormat(contentW, 5, d.tr(label), "", 1, "R", false, 0, "")
	d.hrule(reportRuleGray)
}

// ReportBar prints a shaded, full-width top-level section heading — e.g.
// "Assets" or "Liabilities and Equity" on a balance sheet, "Income" on a
// profit and loss statement.
func (d *Document) ReportBar(title string) {
	d.pdf.SetFont("Helvetica", "", 9)
	d.pdf.SetFillColor(reportBarGray[0], reportBarGray[1], reportBarGray[2])
	d.pdf.CellFormat(contentW, 6, d.tr(title), "", 1, "L", true, 0, "")
}

// ReportHeading prints a plain (unshaded) subsection heading indented by
// level (~4mm per level) — e.g. "Current Assets" nested under an "Assets"
// ReportBar.
func (d *Document) ReportHeading(title string, level int) {
	d.pdf.SetFont("Helvetica", "", 9)
	indent := reportIndent(level)
	d.pdf.SetX(marginLeft + indent)
	d.pdf.CellFormat(contentW-indent, 5.5, d.tr(title), "", 1, "L", false, 0, "")
}

// ReportLine prints one leaf account line: a label indented by level on the
// left, its already-formatted value right-aligned in the Total column.
func (d *Document) ReportLine(label string, value string, level int) {
	d.pdf.SetFont("Helvetica", "", 9)
	indent := reportIndent(level)
	labelW := contentW - indent - reportValueColW
	d.pdf.SetX(marginLeft + indent)
	d.pdf.CellFormat(labelW, 5.5, d.tr(d.truncate(label, labelW-2)), "", 0, "L", false, 0, "")
	d.pdf.CellFormat(reportValueColW, 5.5, d.tr(value), "", 1, "R", false, 0, "")
}

// ReportSubtotal prints a bold "Total for X" row bracketed by thin rules
// above and below — a category subtotal nested under a ReportBar. Reserves
// its own height first (see Document.reserve) so the two rules can't land
// on different pages than the row they bracket.
func (d *Document) ReportSubtotal(label, value string, level int) {
	d.reserve(9)
	d.hrule(reportRuleGray)
	indent := reportIndent(level)
	d.pdf.SetFont("Helvetica", "B", 9)
	d.pdf.SetX(marginLeft + indent)
	d.pdf.CellFormat(contentW-indent-reportValueColW, 6, d.tr(label), "", 0, "L", false, 0, "")
	d.pdf.CellFormat(reportValueColW, 6, d.tr(value), "", 1, "R", false, 0, "")
	d.hrule(reportRuleGray)
}

// ReportGrandTotal prints a bold, shaded full-width total row bracketed by
// rules — the closing "Total for Assets" / "Net Income" style line of a
// report. Reserves its own height first (see Document.reserve) so it never
// splits across a page break.
func (d *Document) ReportGrandTotal(label, value string) {
	d.reserve(9.5)
	d.hrule(reportRuleGray)
	d.pdf.SetFont("Helvetica", "B", 9.5)
	d.pdf.SetFillColor(reportBarGray[0], reportBarGray[1], reportBarGray[2])
	d.pdf.CellFormat(contentW-reportValueColW, 6.5, d.tr(label), "", 0, "L", true, 0, "")
	d.pdf.CellFormat(reportValueColW, 6.5, d.tr(value), "", 1, "R", true, 0, "")
	d.hrule(reportRuleGray)
}

func reportIndent(level int) float64 {
	return 3.0 + float64(level)*4.0
}

// hrule draws a full-content-width horizontal rule at the current Y in the
// given color and advances past it slightly — the only "line" this report
// layout draws, standing in for a bordered grid's cell edges.
func (d *Document) hrule(color [3]int) {
	y := d.pdf.GetY()
	d.pdf.SetDrawColor(color[0], color[1], color[2])
	d.pdf.Line(marginLeft, y, marginLeft+contentW, y)
	d.pdf.SetDrawColor(0, 0, 0)
	d.pdf.Ln(1)
}

// TableColumn is one column of a Table — Header text, its width as a
// fraction of the content width (must sum to ~1.0 across all columns), and
// whether values right-align (numbers) or left-align (text).
type TableColumn struct {
	Header string
	Width  float64
	Right  bool
}

// Table renders a bordered table: a shaded header row, then one row per
// entry in rows (each must have len(cols) cells), then — if totalRow is
// non-nil — a bold total row. The header row reprints at the top of every
// continuation page for as long as the table is rendering (see
// activeTableHeader) — a table long enough to paginate would otherwise
// open its second page with bare data rows and no column labels.
func (d *Document) Table(cols []TableColumn, rows [][]string, totalRow []string) {
	widths := make([]float64, len(cols))
	for i, c := range cols {
		widths[i] = contentW * c.Width
	}

	printHeader := func() {
		d.pdf.SetFont("Helvetica", "B", 9)
		d.pdf.SetFillColor(230, 230, 230)
		for i, c := range cols {
			d.pdf.CellFormat(widths[i], 7, d.tr(c.Header), "1", 0, alignOf(c.Right), true, 0, "")
		}
		d.pdf.Ln(-1)
	}
	printHeader()
	d.activeTableHeader = printHeader

	d.pdf.SetFont("Helvetica", "", 9)
	for _, row := range rows {
		for i, cell := range row {
			text := singleLine(cell)
			if !cols[i].Right {
				text = d.truncate(text, widths[i]-2)
			}
			d.pdf.CellFormat(widths[i], 6, d.tr(text), "1", 0, alignOf(cols[i].Right), false, 0, "")
		}
		d.pdf.Ln(-1)
	}
	d.activeTableHeader = nil

	if totalRow != nil {
		d.pdf.SetFont("Helvetica", "B", 9)
		for i, cell := range totalRow {
			text := singleLine(cell)
			if !cols[i].Right {
				text = d.truncate(text, widths[i]-2)
			}
			d.pdf.CellFormat(widths[i], 7, d.tr(text), "1", 0, alignOf(cols[i].Right), false, 0, "")
		}
		d.pdf.Ln(-1)
	}
}

// BorderlessTable renders the same shape as Table — a header row, one row
// per entry, and (if totalRow is non-nil) a bold total row — but with no
// grid lines at all, vertical or horizontal: a light gray fill picks the
// header row out from the data (no line needed for that), and bold weight
// plus whitespace alone set the total row apart, for a flatter, more
// modern look. Used by every report and trading document in this package.
// Like Table, its header row (fill included) reprints at the top of every
// continuation page for as long as the table is rendering (see
// activeTableHeader).
func (d *Document) BorderlessTable(cols []TableColumn, rows [][]string, totalRow []string) {
	widths := make([]float64, len(cols))
	for i, c := range cols {
		widths[i] = contentW * c.Width
	}

	printHeader := func() {
		d.pdf.SetFont("Helvetica", "B", 9)
		d.pdf.SetFillColor(reportBarGray[0], reportBarGray[1], reportBarGray[2])
		for i, c := range cols {
			d.pdf.CellFormat(widths[i], 7, d.tr(c.Header), "", 0, alignOf(c.Right), true, 0, "")
		}
		d.pdf.Ln(-1)
		d.pdf.Ln(1)
	}
	printHeader()
	d.activeTableHeader = printHeader

	d.pdf.SetFont("Helvetica", "", 9)
	for _, row := range rows {
		for i, cell := range row {
			text := singleLine(cell)
			if !cols[i].Right {
				text = d.truncate(text, widths[i]-2)
			}
			d.pdf.CellFormat(widths[i], 6.5, d.tr(text), "", 0, alignOf(cols[i].Right), false, 0, "")
		}
		d.pdf.Ln(-1)
	}
	d.activeTableHeader = nil

	if totalRow != nil {
		d.pdf.Ln(1)
		d.pdf.SetFont("Helvetica", "B", 9)
		for i, cell := range totalRow {
			text := singleLine(cell)
			if !cols[i].Right {
				text = d.truncate(text, widths[i]-2)
			}
			d.pdf.CellFormat(widths[i], 7, d.tr(text), "", 0, alignOf(cols[i].Right), false, 0, "")
		}
		d.pdf.Ln(-1)
	}
}

// singleLine collapses embedded line breaks to spaces — a table cell is
// rendered with a single CellFormat call, which neither wraps nor advances
// to a new line for an embedded "\n" (e.g. from a migrated line-item
// description), so left uninterrupted it renders as an undefined-width
// control character that can spill past the cell's border.
func singleLine(text string) string {
	text = strings.ReplaceAll(text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	return text
}

// truncate shortens text with a trailing "..." if it's wider than maxWidth
// under the document's current font — fpdf's CellFormat neither wraps nor
// clips text that's too wide for its cell, so an over-long value (a long
// line-item description, say) would otherwise spill visibly into the next
// column instead of being confined to its own. text is plain (untranslated)
// UTF-8 — width is measured against d.tr(candidate), the actual bytes that
// will be rendered, since fpdf's core Helvetica font scores string width
// per byte of the cp1252-translated form: measuring the raw multi-byte
// UTF-8 encoding of an accented character or curly quote would score it as
// several unrelated cp1252 glyphs instead of the one glyph it renders as.
// Rune-slicing stays on the untranslated string because d.tr's output byte
// values above 0x7F aren't valid UTF-8 on their own — slicing runes out of
// it would corrupt the very characters this measurement fix is for.
func (d *Document) truncate(text string, maxWidth float64) string {
	if d.pdf.GetStringWidth(d.tr(text)) <= maxWidth {
		return text
	}
	const ellipsis = "..."
	runes := []rune(text)
	for len(runes) > 0 {
		runes = runes[:len(runes)-1]
		candidate := string(runes) + ellipsis
		if d.pdf.GetStringWidth(d.tr(candidate)) <= maxWidth {
			return candidate
		}
	}
	return ellipsis
}

func alignOf(right bool) string {
	if right {
		return "R"
	}
	return "L"
}

// Bytes finalizes the document and returns its PDF bytes.
func (d *Document) Bytes() ([]byte, error) {
	var buf bytes.Buffer
	if err := d.pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("rendering pdf: %w", err)
	}
	return buf.Bytes(), nil
}
