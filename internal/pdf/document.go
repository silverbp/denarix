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
	pageWidth   = 210.0 // A4, mm
	marginLeft  = 15.0
	marginRight = 15.0
	// marginBottom is generous enough to fit Document.SetFooter's rule plus
	// several lines of text (a business name, a two-line address, phone,
	// email) without colliding with the page's normal content.
	marginBottom = 30.0
	contentW     = pageWidth - marginLeft - marginRight
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
}

// New starts a new A4 document with margins set and the first page added.
func New() *Document {
	p := fpdf.New("P", "mm", "A4", "")
	p.SetMargins(marginLeft, 15, marginRight)
	p.SetAutoPageBreak(true, marginBottom)
	d := &Document{pdf: p, tr: p.UnicodeTranslatorFromDescriptor("")}
	p.SetFooterFunc(func() { d.renderFooter() })
	p.AddPage()
	return d
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

// Party is a name plus arbitrary detail lines (address, phone, email) for
// AddressBlock.
type Party struct {
	Name  string
	Lines []string
}

// AddressBlock prints two Party blocks side by side, e.g. the customer
// being billed on the left and the business issuing the document on the
// right — each as a bold name line followed by plain detail lines.
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

// partyColumn prints one Party's name and detail lines at (x, y), each
// line's own row so the two columns of an AddressBlock don't have to have
// the same number of lines, and returns the y position just past its last
// line.
func (d *Document) partyColumn(x, y, w float64, p Party) float64 {
	d.pdf.SetXY(x, y)
	d.pdf.SetFont("Helvetica", "B", 10)
	d.pdf.CellFormat(w, 5.5, d.tr(p.Name), "", 0, "L", false, 0, "")
	d.pdf.SetFont("Helvetica", "", 9)
	for i, l := range p.Lines {
		d.pdf.SetXY(x, y+5.5+float64(i)*5)
		d.pdf.CellFormat(w, 5, d.tr(l), "", 0, "L", false, 0, "")
	}
	return y + 5.5 + float64(len(p.Lines))*5
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
// above it — instead of KeyValueRow/MoneyRow's left margin.
func (d *Document) SummaryBlock(rows []SummaryRow) {
	const blockW = 75.0
	labelW := blockW * 0.5
	valueW := blockW - labelW
	x := marginLeft + contentW - blockW

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
// non-nil — a bold total row.
func (d *Document) Table(cols []TableColumn, rows [][]string, totalRow []string) {
	widths := make([]float64, len(cols))
	for i, c := range cols {
		widths[i] = contentW * c.Width
	}

	d.pdf.SetFont("Helvetica", "B", 9)
	d.pdf.SetFillColor(230, 230, 230)
	for i, c := range cols {
		d.pdf.CellFormat(widths[i], 7, d.tr(c.Header), "1", 0, alignOf(c.Right), true, 0, "")
	}
	d.pdf.Ln(-1)

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
