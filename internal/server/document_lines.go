// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

// The line-item pipeline shared by EstimateService and InvoiceService. An
// estimate line and an invoice line are the same thing minus the ledger
// account (estimates have no ledger impact - docs/schema.md), so one
// request message (NewDocumentLineItem), one resolver, one computation and
// one tax breakdown serve both; only the final INSERT differs, because the
// two sqlc param structs do.
//
// Order of operations for a create / update-lines handler:
//
//	lines, totals, err := buildDocumentLines(ctx, q, businessID, req.GetLineItems(), forInvoice)
//	row, err := q.CreateX(... totals.Subtotal, totals.TotalTax, totals.Total ...)
//	items, err := insertXLines(ctx, q, row.ID, lines)

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/db/sqlcgen"
	"github.com/silverbp/denarix/internal/ledgermath"
	"github.com/silverbp/denarix/internal/moneypb"
	"github.com/silverbp/denarix/internal/pdf"
)

// resolvedLine is a NewDocumentLineItem after applying item-catalog defaults
// (see resolveLine) - description/unit_price/is_taxable/tax_rate_id may have
// originated from the request or the item, but from here on every caller
// treats them as final. ItemID is a pointer only because the column is
// nullable; it's always set here. LedgerAccountID is set for invoice lines
// (always the item's default_ledger_account_id) and nil for estimate lines.
type resolvedLine struct {
	ItemID          *int64
	LedgerAccountID *int32
	LineNumber      int32
	Description     string
	Quantity        *denarixv1.Decimal
	UnitPrice       *denarixv1.Decimal
	IsTaxable       bool
	TaxRateID       *int64
}

// computedLine is the server-side arithmetic for one resolved line.
type computedLine struct {
	Quantity     pgtype.Numeric
	UnitPrice    pgtype.Numeric
	LineSubtotal pgtype.Numeric
	TaxRate      pgtype.Numeric
	TaxAmount    pgtype.Numeric
	LineTotal    pgtype.Numeric
}

// documentLine is one line ready to INSERT: what the caller asked for
// (resolved against the catalog) and what it computes to.
type documentLine struct {
	resolvedLine
	computedLine
}

// documentTotals are the document-level sums, already converted for the
// document row's NUMERIC columns.
type documentTotals struct {
	Subtotal, TotalTax, Total pgtype.Numeric
	// TotalDecimal is Total as a decimal, for callers that still need to
	// compare it (UpdateInvoiceLineItems checks it against paid_amount).
	TotalDecimal decimal.Decimal
}

// buildDocumentLines is the whole pipeline: look each line's item up
// (scoped to businessID), apply the item's defaults, compute
// subtotal/tax/total per line and for the document. forInvoice additionally
// requires every item to carry a default_ledger_account_id and records it on
// the line. Statuses returned from inside ExecTx pass through unchanged
// (txErrorStatus keeps an existing status).
func buildDocumentLines(ctx context.Context, q *sqlcgen.Queries, businessID int64, raw []*denarixv1.NewDocumentLineItem, forInvoice bool) ([]documentLine, documentTotals, error) {
	resolved, err := resolveLines(ctx, q, businessID, raw, forInvoice)
	if err != nil {
		return nil, documentTotals{}, err
	}
	computed, subtotal, totalTax, total, err := computeLines(ctx, q, businessID, resolved)
	if err != nil {
		return nil, documentTotals{}, err
	}
	subtotalNum, e1 := ledgermath.DecimalToNumeric(subtotal)
	totalTaxNum, e2 := ledgermath.DecimalToNumeric(totalTax)
	totalNum, e3 := ledgermath.DecimalToNumeric(total)
	if err := firstErr(e1, e2, e3); err != nil {
		return nil, documentTotals{}, err
	}
	lines := make([]documentLine, len(resolved))
	for i := range resolved {
		lines[i] = documentLine{resolvedLine: resolved[i], computedLine: computed[i]}
	}
	return lines, documentTotals{Subtotal: subtotalNum, TotalTax: totalTaxNum, Total: totalNum, TotalDecimal: total}, nil
}

// insertEstimateLines writes lines under estimateID, in order.
func insertEstimateLines(ctx context.Context, q *sqlcgen.Queries, estimateID int64, lines []documentLine) ([]sqlcgen.EstimateLineItem, error) {
	out := make([]sqlcgen.EstimateLineItem, 0, len(lines))
	for _, l := range lines {
		li, err := q.CreateEstimateLineItem(ctx, sqlcgen.CreateEstimateLineItemParams{
			EstimateID:   estimateID,
			ItemID:       l.ItemID,
			LineNumber:   l.LineNumber,
			Description:  l.Description,
			Quantity:     l.computedLine.Quantity,
			UnitPrice:    l.computedLine.UnitPrice,
			LineSubtotal: l.LineSubtotal,
			IsTaxable:    l.IsTaxable,
			TaxRateID:    l.TaxRateID,
			TaxRate:      l.TaxRate,
			TaxAmount:    l.TaxAmount,
			LineTotal:    l.LineTotal,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, li)
	}
	return out, nil
}

// insertInvoiceLines writes lines under invoiceID, in order.
func insertInvoiceLines(ctx context.Context, q *sqlcgen.Queries, invoiceID int64, lines []documentLine) ([]sqlcgen.InvoiceLineItem, error) {
	out := make([]sqlcgen.InvoiceLineItem, 0, len(lines))
	for _, l := range lines {
		li, err := q.CreateInvoiceLineItem(ctx, sqlcgen.CreateInvoiceLineItemParams{
			InvoiceID:       invoiceID,
			ItemID:          l.ItemID,
			LedgerAccountID: l.LedgerAccountID,
			LineNumber:      l.LineNumber,
			Description:     l.Description,
			Quantity:        l.computedLine.Quantity,
			UnitPrice:       l.computedLine.UnitPrice,
			LineSubtotal:    l.LineSubtotal,
			IsTaxable:       l.IsTaxable,
			TaxRateID:       l.TaxRateID,
			TaxRate:         l.TaxRate,
			TaxAmount:       l.TaxAmount,
			LineTotal:       l.LineTotal,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, li)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Item-catalog resolution.
//
// Every line must reference a catalog item from the document's own business
// (Xero/QuickBooks style - no free-text lines). The item supplies the line's
// defaults (description/unit_price/is_taxable/tax_rate_id, each overridable
// per line) and, for invoices, *the* ledger account the line posts to (never
// overridable). Enforcement is API-level only: the item_id/ledger_account_id
// columns stay nullable for pre-catalog rows.
// ---------------------------------------------------------------------------

// lookupLineItem fetches the item a line references, scoped to businessID.
// It is the single gate that makes items mandatory: a missing item_id, an
// item that isn't in this business (or doesn't exist), and an inactive item
// each get a distinct status so the caller knows which to fix.
func lookupLineItem(ctx context.Context, q *sqlcgen.Queries, businessID int64, lineIdx int, itemID int64) (sqlcgen.Item, error) {
	if itemID == 0 {
		return sqlcgen.Item{}, status.Errorf(codes.InvalidArgument, "line %d: item_id is required", lineIdx)
	}
	item, err := itemRes.requireInBusiness(ctx, q, businessID, itemID)
	if err != nil {
		return sqlcgen.Item{}, prefixStatus(err, "line %d", lineIdx)
	}
	if err := checkLineItemUsable(lineIdx, item); err != nil {
		return sqlcgen.Item{}, err
	}
	return item, nil
}

// checkLineItemUsable is the DB-free half of lookupLineItem: an item that has been
// deactivated can't go on a new line (existing lines that already reference it are
// untouched - they're history). Split out so it's unit-testable.
func checkLineItemUsable(lineIdx int, item sqlcgen.Item) error {
	if !item.IsActive {
		return status.Errorf(codes.FailedPrecondition, "line %d: item %d (%s) is inactive", lineIdx, item.ID, item.ItemCode)
	}
	return nil
}

// resolveLine applies item's defaults to li: description falls back to the
// item's name, unit_price/is_taxable/tax_rate_id to
// retail_price/is_taxable/default_tax_rate_id. An explicit value on the line
// always wins (docs/schema.md, "item"). For an invoice line the ledger
// account is *always* the item's default_ledger_account_id - there is no
// per-line override - so an item without one is rejected up front rather
// than producing a line the ledger posting would then choke on. Pure: item
// has already been fetched and vetted by lookupLineItem.
func resolveLine(lineIdx int, li *denarixv1.NewDocumentLineItem, item sqlcgen.Item, forInvoice bool) (resolvedLine, error) {
	if forInvoice && item.DefaultLedgerAccountID == nil {
		return resolvedLine{}, status.Errorf(codes.FailedPrecondition,
			"line %d: item %d (%s) has no default_ledger_account_id - set one on the item before invoicing it", lineIdx, item.ID, item.ItemCode)
	}
	itemID := item.ID
	r := resolvedLine{
		ItemID:      &itemID,
		LineNumber:  li.LineNumber,
		Description: li.Description,
		Quantity:    li.Quantity,
		UnitPrice:   li.UnitPrice,
		IsTaxable:   li.GetIsTaxable(),
		TaxRateID:   li.TaxRateId,
	}
	if forInvoice {
		r.LedgerAccountID = item.DefaultLedgerAccountID
	}
	if r.Description == "" {
		r.Description = item.Name
	}
	if r.UnitPrice == nil {
		r.UnitPrice = moneypb.ToProto(item.RetailPrice)
	}
	if li.IsTaxable == nil {
		r.IsTaxable = item.IsTaxable
	}
	if r.TaxRateID == nil && r.IsTaxable {
		r.TaxRateID = item.DefaultTaxRateID
	}
	return r, nil
}

// resolveLines is lookupLineItem + resolveLine over every line of a request,
// scoped to the document's business.
func resolveLines(ctx context.Context, q *sqlcgen.Queries, businessID int64, raw []*denarixv1.NewDocumentLineItem, forInvoice bool) ([]resolvedLine, error) {
	resolved := make([]resolvedLine, len(raw))
	for i, li := range raw {
		item, err := lookupLineItem(ctx, q, businessID, i, li.GetItemId())
		if err != nil {
			return nil, err
		}
		r, err := resolveLine(i, li, item, forInvoice)
		if err != nil {
			return nil, err
		}
		resolved[i] = r
	}
	return resolved, nil
}

// newDocumentLineItemsFromEstimate converts an accepted estimate's line items
// back into request shape so CreateInvoice can run them through the normal
// pipeline, carrying over item_id and the description/quantity/price/tax
// fields the estimate already resolved. The ledger account is never carried
// (estimate_line_item has no such column); resolveLines takes it from the
// item's *current* default_ledger_account_id at invoice time, same as any
// hand-entered line. A pre-catalog estimate line with no item_id becomes
// item_id 0 and is rejected by lookupLineItem ("item_id is required") - it
// has to be re-pointed at a real item first.
func newDocumentLineItemsFromEstimate(estLines []sqlcgen.EstimateLineItem) []*denarixv1.NewDocumentLineItem {
	out := make([]*denarixv1.NewDocumentLineItem, len(estLines))
	for i, eli := range estLines {
		isTaxable := eli.IsTaxable
		var itemID int64
		if eli.ItemID != nil {
			itemID = *eli.ItemID
		}
		out[i] = &denarixv1.NewDocumentLineItem{
			ItemId:      itemID,
			LineNumber:  eli.LineNumber,
			Description: eli.Description,
			Quantity:    moneypb.ToProto(eli.Quantity),
			UnitPrice:   moneypb.ToProto(eli.UnitPrice),
			IsTaxable:   &isTaxable,
			TaxRateId:   eli.TaxRateID,
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Arithmetic.
// ---------------------------------------------------------------------------

// computeLines fills in each line's subtotal/tax/total server-side (never
// trusting client-supplied aggregates), snapshotting the tax_rate actually
// applied onto the line per the schema's own convention ("snapshot, don't
// reference, at the point of sale" — docs/schema.md).
//
// A line's own subtotal/tax/total may be negative — that's how a discount
// posts, as a negative line against a catalog item pointed at a
// contra-revenue/expense account (see ledgerpost.DebitCreditFor) — but the
// document as a whole may not net negative: nothing downstream (payments,
// balance_due, invoice.status) models a credit note, so a wholly negative
// document is rejected here rather than accepted and breaking later.
func computeLines(ctx context.Context, q *sqlcgen.Queries, businessID int64, inputs []resolvedLine) (lines []computedLine, subtotal, totalTax, total decimal.Decimal, err error) {
	subtotal, totalTax = decimal.Zero, decimal.Zero

	for i, in := range inputs {
		qty, err := parseDecimalOrDefault(in.Quantity, "1")
		if err != nil {
			return nil, decimal.Zero, decimal.Zero, decimal.Zero, fmt.Errorf("line %d: invalid quantity: %w", i, err)
		}
		price, err := parseDecimalOrDefault(in.UnitPrice, "0")
		if err != nil {
			return nil, decimal.Zero, decimal.Zero, decimal.Zero, fmt.Errorf("line %d: invalid unit_price: %w", i, err)
		}
		lineSubtotal := qty.Mul(price).Round(2)

		taxRate, taxAmount := decimal.Zero, decimal.Zero
		if in.IsTaxable && in.TaxRateID != nil {
			tr, err := taxRateRes.requireInBusiness(ctx, q, businessID, *in.TaxRateID)
			if err != nil {
				return nil, decimal.Zero, decimal.Zero, decimal.Zero, prefixStatus(err, "line %d", i)
			}
			taxRate, err = ledgermath.NumericToDecimal(tr.Rate)
			if err != nil {
				return nil, decimal.Zero, decimal.Zero, decimal.Zero, err
			}
			taxAmount = lineSubtotal.Mul(taxRate).Round(2)
		}
		lineTotal := lineSubtotal.Add(taxAmount)

		qtyNum, err1 := ledgermath.DecimalToNumeric(qty)
		priceNum, err2 := ledgermath.DecimalToNumeric(price)
		subtotalNum, err3 := ledgermath.DecimalToNumeric(lineSubtotal)
		taxRateNum, err4 := ledgermath.DecimalToNumeric(taxRate)
		taxAmountNum, err5 := ledgermath.DecimalToNumeric(taxAmount)
		totalNum, err6 := ledgermath.DecimalToNumeric(lineTotal)
		if err := firstErr(err1, err2, err3, err4, err5, err6); err != nil {
			return nil, decimal.Zero, decimal.Zero, decimal.Zero, err
		}

		lines = append(lines, computedLine{
			Quantity:     qtyNum,
			UnitPrice:    priceNum,
			LineSubtotal: subtotalNum,
			TaxRate:      taxRateNum,
			TaxAmount:    taxAmountNum,
			LineTotal:    totalNum,
		})
		subtotal = subtotal.Add(lineSubtotal)
		totalTax = totalTax.Add(taxAmount)
	}

	total = subtotal.Add(totalTax)
	if total.IsNegative() {
		return nil, decimal.Zero, decimal.Zero, decimal.Zero, status.Errorf(codes.InvalidArgument, "document total %s is negative — a discount line may not exceed the rest of the document", total)
	}
	return lines, subtotal, totalTax, total, nil
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Tax breakdown for the PDFs.
// ---------------------------------------------------------------------------

// taxBreakdownLine is the subset of a stored line item taxBreakdown needs.
type taxBreakdownLine struct {
	TaxRateID    *int64
	LineSubtotal pgtype.Numeric
	TaxAmount    pgtype.Numeric
	LineTotal    pgtype.Numeric
}

func estimateBreakdownLine(li sqlcgen.EstimateLineItem) taxBreakdownLine {
	return taxBreakdownLine{TaxRateID: li.TaxRateID, LineSubtotal: li.LineSubtotal, TaxAmount: li.TaxAmount, LineTotal: li.LineTotal}
}

func invoiceBreakdownLine(li sqlcgen.InvoiceLineItem) taxBreakdownLine {
	return taxBreakdownLine{TaxRateID: li.TaxRateID, LineSubtotal: li.LineSubtotal, TaxAmount: li.TaxAmount, LineTotal: li.LineTotal}
}

// taxBreakdown groups line items by tax rate, in the order each rate first
// appears, and sums their net/tax/total amounts — a PDF renders one row per
// group instead of a per-line tax column. pick maps the document's own line
// type (estimate or invoice) onto the fields this needs.
func taxBreakdown[L any](ctx context.Context, q *sqlcgen.Queries, lineItems []L, pick func(L) taxBreakdownLine) ([]pdf.TaxBreakdownRow, error) {
	type group struct {
		label           string
		net, tax, total decimal.Decimal
	}
	const noTaxKey = int64(0) // tax_rate.id is a BIGSERIAL, so 0 never occurs — safe sentinel for "no tax rate".

	groups := map[int64]*group{}
	var order []int64
	rateNames := map[int64]string{}

	for _, raw := range lineItems {
		li := pick(raw)
		key := noTaxKey
		if li.TaxRateID != nil {
			key = *li.TaxRateID
		}
		g, ok := groups[key]
		if !ok {
			label := ""
			if key != noTaxKey {
				name, ok := rateNames[key]
				if !ok {
					tr, err := q.GetTaxRate(ctx, key)
					if err != nil {
						return nil, err
					}
					name = tr.Name
					rateNames[key] = name
				}
				label = name
			}
			g = &group{label: label}
			groups[key] = g
			order = append(order, key)
		}

		net, err := ledgermath.NumericToDecimal(li.LineSubtotal)
		if err != nil {
			return nil, err
		}
		tax, err := ledgermath.NumericToDecimal(li.TaxAmount)
		if err != nil {
			return nil, err
		}
		total, err := ledgermath.NumericToDecimal(li.LineTotal)
		if err != nil {
			return nil, err
		}
		g.net = g.net.Add(net)
		g.tax = g.tax.Add(tax)
		g.total = g.total.Add(total)
	}

	rows := make([]pdf.TaxBreakdownRow, len(order))
	for i, key := range order {
		g := groups[key]
		if key == noTaxKey {
			// The label depends on the group's fully-aggregated tax, not
			// just its first line — a line with no linked tax rate but a
			// non-zero tax_amount (e.g. tax data migrated from another
			// system without a matching tax rate) still needs to show its
			// tax; labeling it "No Tax" would hide a real tax figure under
			// a name that says there isn't one.
			g.label = "No Tax"
			if !g.tax.IsZero() {
				g.label = "Tax (no rate)"
			}
		}
		rows[i] = pdf.TaxBreakdownRow{Label: g.label, Net: g.net, Tax: g.tax, Total: g.total}
	}
	return rows, nil
}

// lineAmounts converts the five NUMERIC amounts every stored line carries.
type lineAmounts struct {
	Quantity, UnitPrice, LineSubtotal, TaxAmount, LineTotal *denarixv1.Decimal
}

func lineAmountsToProto(quantity, unitPrice, lineSubtotal, taxAmount, lineTotal pgtype.Numeric) lineAmounts {
	return lineAmounts{
		Quantity:     moneypb.ToProto(quantity),
		UnitPrice:    moneypb.ToProto(unitPrice),
		LineSubtotal: moneypb.ToProto(lineSubtotal),
		TaxAmount:    moneypb.ToProto(taxAmount),
		LineTotal:    moneypb.ToProto(lineTotal),
	}
}

// isNoRows reports whether err is a sqlc "no row" result.
func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
