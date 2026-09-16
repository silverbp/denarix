// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

// InvoiceService — posts to the ledger atomically on create (see proto
// comment); the posting itself lives in invoice_posting.go.

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/auth"
	"github.com/silverbp/denarix/internal/datepb"
	"github.com/silverbp/denarix/internal/db"
	"github.com/silverbp/denarix/internal/db/sqlcgen"
	"github.com/silverbp/denarix/internal/ledgermath"
	"github.com/silverbp/denarix/internal/ledgerpost"
	"github.com/silverbp/denarix/internal/moneypb"
	"github.com/silverbp/denarix/internal/pdf"
)

type invoiceService struct {
	denarixv1.UnimplementedInvoiceServiceServer
	store *db.Store
}

func newInvoiceService(store *db.Store) *invoiceService {
	return &invoiceService{store: store}
}

func (s *invoiceService) GetInvoice(ctx context.Context, req *denarixv1.GetInvoiceRequest) (*denarixv1.GetInvoiceResponse, error) {
	inv, err := invoiceRes.load(ctx, s.store.Queries, req.GetId(), "VIEWER")
	if err != nil {
		return nil, err
	}
	pb, err := invoiceToProto(ctx, s.store.Queries, inv)
	if err != nil {
		return nil, err
	}
	return &denarixv1.GetInvoiceResponse{Invoice: pb}, nil
}

func (s *invoiceService) ListInvoices(ctx context.Context, req *denarixv1.ListInvoicesRequest) (*denarixv1.ListInvoicesResponse, error) {
	if err := auth.RequireBusinessRole(ctx, s.store.Queries, req.GetBusinessId(), "VIEWER"); err != nil {
		return nil, err
	}
	rows, err := s.store.Queries.ListInvoices(ctx, sqlcgen.ListInvoicesParams{
		BusinessID: req.GetBusinessId(),
		IncludeAll: req.GetIncludeAll(),
	})
	if err != nil {
		return nil, translatePgError(err)
	}
	pbs, err := invoicesToProto(ctx, s.store.Queries, rows)
	if err != nil {
		return nil, err
	}
	return &denarixv1.ListInvoicesResponse{Invoices: pbs}, nil
}

func (s *invoiceService) CreateInvoice(ctx context.Context, req *denarixv1.CreateInvoiceRequest) (*denarixv1.CreateInvoiceResponse, error) {
	u, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no authenticated user")
	}
	if err := auth.RequireBusinessRole(ctx, s.store.Queries, req.GetBusinessId(), "MEMBER"); err != nil {
		return nil, err
	}
	if req.GetInvoiceType() != "SALES" && req.GetInvoiceType() != "PURCHASE" {
		return nil, status.Error(codes.InvalidArgument, "invoice_type must be SALES or PURCHASE")
	}
	if len(req.GetLineItems()) == 0 && req.EstimateId == nil {
		return nil, status.Error(codes.InvalidArgument, "at least one line item is required")
	}
	if req.GetInvoiceType() == "PURCHASE" && req.GetInvoiceNumber() == "" {
		return nil, status.Error(codes.InvalidArgument, "invoice_number is required for PURCHASE invoices")
	}
	if req.GetInvoiceDate() == nil || req.GetDueDate() == nil {
		return nil, status.Error(codes.InvalidArgument, "invoice_date and due_date are required")
	}
	if _, err := contactRes.requireInBusiness(ctx, s.store.Queries, req.GetBusinessId(), req.GetContactId()); err != nil {
		return nil, err
	}

	var (
		invoice   sqlcgen.Invoice
		lineItems []sqlcgen.InvoiceLineItem
	)
	err := s.store.ExecTx(ctx, func(q *sqlcgen.Queries) error {
		rawLineItems := req.GetLineItems()
		if len(rawLineItems) == 0 {
			est, err := estimateRes.requireInBusiness(ctx, q, req.GetBusinessId(), req.GetEstimateId())
			if err != nil {
				return err
			}
			estLines, err := q.ListEstimateLineItems(ctx, est.ID)
			if err != nil {
				return err
			}
			if len(estLines) == 0 {
				return status.Errorf(codes.InvalidArgument, "estimate %d has no line items", est.ID)
			}
			rawLineItems = newDocumentLineItemsFromEstimate(estLines)
		}

		lines, totals, err := buildDocumentLines(ctx, q, req.GetBusinessId(), rawLineItems, true)
		if err != nil {
			return err
		}

		invoiceNumber := req.GetInvoiceNumber()
		if req.GetInvoiceType() == "SALES" {
			claimed, err := q.ConsumeNextInvoiceNumber(ctx, req.GetBusinessId())
			if err != nil {
				return err
			}
			invoiceNumber = fmt.Sprintf("%s%d", derefOr(claimed.Prefix, "INV"), claimed.ClaimedNumber)
		}

		invoice, err = q.CreateInvoice(ctx, sqlcgen.CreateInvoiceParams{
			BusinessID:      req.GetBusinessId(),
			ContactID:       req.GetContactId(),
			InvoiceType:     req.GetInvoiceType(),
			EstimateID:      req.EstimateId,
			InvoiceNumber:   invoiceNumber,
			InvoiceDate:     datepb.ToPgDate(req.GetInvoiceDate()),
			DueDate:         datepb.ToPgDate(req.GetDueDate()),
			Subtotal:        totals.Subtotal,
			TotalTaxAmount:  totals.TotalTax,
			TotalAmount:     totals.Total,
			BalanceDue:      totals.Total,
			Notes:           req.Notes,
			Terms:           req.Terms,
			CreatedByUserID: &u.ID,
		})
		if err != nil {
			return err
		}
		if lineItems, err = insertInvoiceLines(ctx, q, invoice.ID, lines); err != nil {
			return err
		}

		txnID, err := postInvoiceLedger(ctx, q, req.GetBusinessId(), invoice, lineItems, &u.ID)
		if err != nil {
			return err
		}
		invoice, err = q.SetInvoiceLedgerTransaction(ctx, sqlcgen.SetInvoiceLedgerTransactionParams{ID: invoice.ID, LedgerTransactionID: &txnID})
		return err
	})
	if err != nil {
		return nil, txErrorStatus(err)
	}
	return &denarixv1.CreateInvoiceResponse{Invoice: invoiceWithLines(invoice, lineItems)}, nil
}

// UpdateInvoice edits notes/terms/due_date - see the proto doc for why
// invoice_date, contact_id, and invoice_number stay off this RPC.
func (s *invoiceService) UpdateInvoice(ctx context.Context, req *denarixv1.UpdateInvoiceRequest) (*denarixv1.UpdateInvoiceResponse, error) {
	existing, err := invoiceRes.load(ctx, s.store.Queries, req.GetId(), "MEMBER")
	if err != nil {
		return nil, err
	}
	if existing.Status == "CANCELLED" {
		return nil, status.Errorf(codes.FailedPrecondition, "invoice %d is cancelled and can no longer be edited", existing.ID)
	}

	updated, err := s.store.Queries.UpdateInvoiceHeader(ctx, sqlcgen.UpdateInvoiceHeaderParams{
		ID:              req.GetId(),
		Notes:           req.Notes,
		Terms:           req.Terms,
		DueDate:         datepb.ToPgDate(req.GetDueDate()),
		ResourceVersion: expectedResourceVersion(req.GetResourceVersion()),
	})
	if err != nil {
		return nil, translateUpdateError(err, invoiceRes.kind, req.GetId(), req.GetResourceVersion())
	}
	pb, err := invoiceToProto(ctx, s.store.Queries, updated)
	if err != nil {
		return nil, err
	}
	return &denarixv1.UpdateInvoiceResponse{Invoice: pb}, nil
}

// invoiceStatusTransitions is the allowed set of next statuses per current
// status for UpdateInvoiceStatus. PAID is never a valid target here - it's
// set automatically by ApplyPaymentToInvoice/UnapplyPaymentFromInvoice as
// balance_due crosses zero, not chosen directly. CANCELLED and PAID are
// both terminal for this RPC: a cancelled invoice stays cancelled, and a
// paid one is corrected by voiding the payment (which itself moves the
// invoice off PAID) rather than by transitioning status directly.
var invoiceStatusTransitions = map[string]map[string]bool{
	"DRAFT":   {"SENT": true, "CANCELLED": true},
	"SENT":    {"OVERDUE": true, "CANCELLED": true},
	"OVERDUE": {"SENT": true, "CANCELLED": true},
}

// isValidInvoiceStatusTransition looks up invoiceStatusTransitions; a nil
// inner map (an unlisted `from`, e.g. PAID or CANCELLED) reads as false,
// same as an unlisted `to`.
func isValidInvoiceStatusTransition(from, to string) bool {
	return invoiceStatusTransitions[from][to]
}

func (s *invoiceService) UpdateInvoiceStatus(ctx context.Context, req *denarixv1.UpdateInvoiceStatusRequest) (*denarixv1.UpdateInvoiceStatusResponse, error) {
	u, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no authenticated user")
	}
	existing, err := invoiceRes.load(ctx, s.store.Queries, req.GetId(), "MEMBER")
	if err != nil {
		return nil, err
	}

	target := req.GetStatus()
	if target == "CANCELLED" {
		// Cancelling: reject while a payment is still applied - void it
		// first, so the payment side (paid_amount/balance_due/
		// payment_application) never drifts from a cancelled invoice's
		// ledger reversal. Checked before the transition table so a PAID
		// invoice (which by construction has an application) gets this
		// actionable hint rather than the generic "cannot move" error.
		appCount, err := s.store.Queries.CountPaymentApplicationsForInvoice(ctx, existing.ID)
		if err != nil {
			return nil, translatePgError(err)
		}
		if appCount > 0 {
			return nil, status.Errorf(codes.FailedPrecondition,
				"invoice %d still has %d payment application(s) - void the payment(s) first", existing.ID, appCount)
		}
	}
	if !isValidInvoiceStatusTransition(existing.Status, target) {
		return nil, status.Errorf(codes.FailedPrecondition, "invoice %d cannot move from status %s to %s", existing.ID, existing.Status, target)
	}

	if target != "CANCELLED" {
		updated, err := s.store.Queries.UpdateInvoiceStatus(ctx, sqlcgen.UpdateInvoiceStatusParams{
			ID:              req.GetId(),
			Status:          target,
			FromStatus:      existing.Status,
			ResourceVersion: expectedResourceVersion(req.GetResourceVersion()),
		})
		if err != nil {
			return nil, s.invoiceStatusUpdateError(ctx, err, existing, target, req.GetResourceVersion())
		}
		return s.invoiceStatusResponse(ctx, updated)
	}

	reversalDate, err := resolveReversalDate(existing.InvoiceDate, req.ReversalDate, "invoice_date")
	if err != nil {
		return nil, err
	}

	var updated sqlcgen.Invoice
	err = s.store.ExecTx(ctx, func(q *sqlcgen.Queries) error {
		var err error
		// The from_status predicate is what makes this safe against a
		// concurrent cancel: whichever call's UPDATE matches first wins the
		// row lock and flips the status, and the other matches zero rows
		// here instead of also posting a reversal.
		updated, err = q.UpdateInvoiceStatus(ctx, sqlcgen.UpdateInvoiceStatusParams{
			ID:              req.GetId(),
			Status:          "CANCELLED",
			FromStatus:      existing.Status,
			ResourceVersion: expectedResourceVersion(req.GetResourceVersion()),
		})
		if err != nil {
			return s.invoiceStatusUpdateError(ctx, err, existing, "CANCELLED", req.GetResourceVersion())
		}
		if existing.LedgerTransactionID == nil {
			return nil
		}
		description := fmt.Sprintf("Cancellation of invoice %d (%s)", existing.ID, existing.InvoiceNumber)
		if _, err := ledgerpost.ReverseTransaction(ctx, q, existing.BusinessID, *existing.LedgerTransactionID, reversalDate, description, &u.ID); err != nil {
			return err
		}
		zero, err := ledgermath.DecimalToNumeric(decimal.Zero)
		if err != nil {
			return err
		}
		// Second write to this row in the same transaction - unconditional,
		// same reasoning as UpdateInvoiceLineItems (the first write above
		// already took the version check and the row lock).
		updated, err = q.UpdateInvoiceTotals(ctx, sqlcgen.UpdateInvoiceTotalsParams{
			ID:              existing.ID,
			Subtotal:        existing.Subtotal,
			TotalTaxAmount:  existing.TotalTaxAmount,
			TotalAmount:     existing.TotalAmount,
			BalanceDue:      zero,
			ResourceVersion: nil,
		})
		return err
	})
	if err != nil {
		return nil, txErrorStatus(err)
	}
	return s.invoiceStatusResponse(ctx, updated)
}

// invoiceStatusUpdateError maps UpdateInvoiceStatus matching no row. With a
// resource_version precondition that's translateUpdateError's usual ABORTED
// (any concurrent write bumps the version). Without one, the row is either
// gone (NotFound) or its status moved under us - the from_status predicate
// stopped this call from acting on a stale read, so report that as ABORTED
// with the status it moved to, and let the caller re-read.
func (s *invoiceService) invoiceStatusUpdateError(ctx context.Context, err error, existing sqlcgen.Invoice, to string, expected int64) error {
	if !isNoRows(err) || expected != 0 {
		return translateUpdateError(err, invoiceRes.kind, existing.ID, expected)
	}
	current, err := s.store.Queries.GetInvoice(ctx, existing.ID)
	if err != nil {
		if isNoRows(err) {
			return status.Errorf(codes.NotFound, "invoice %d not found", existing.ID)
		}
		return translatePgError(err)
	}
	return status.Errorf(codes.Aborted,
		"invoice %d moved from %s to %s concurrently; re-read it before transitioning to %s", existing.ID, existing.Status, current.Status, to)
}

func (s *invoiceService) invoiceStatusResponse(ctx context.Context, updated sqlcgen.Invoice) (*denarixv1.UpdateInvoiceStatusResponse, error) {
	pb, err := invoiceToProto(ctx, s.store.Queries, updated)
	if err != nil {
		return nil, err
	}
	return &denarixv1.UpdateInvoiceStatusResponse{Invoice: pb}, nil
}

// UpdateInvoiceLineItems replaces an invoice's entire line item set and
// recomputes its totals — see the proto doc for why this is a full replace
// rather than a per-line patch. If the invoice is already posted to the
// ledger, its entries are regenerated in place (repostInvoiceLedger) rather
// than rejecting the edit.
func (s *invoiceService) UpdateInvoiceLineItems(ctx context.Context, req *denarixv1.UpdateInvoiceLineItemsRequest) (*denarixv1.UpdateInvoiceLineItemsResponse, error) {
	u, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no authenticated user")
	}
	existing, err := invoiceRes.load(ctx, s.store.Queries, req.GetId(), "MEMBER")
	if err != nil {
		return nil, err
	}
	if len(req.GetLineItems()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one line item is required")
	}
	// A cancelled invoice's posting has already been mirrored by its
	// reversal; re-posting under the same ledger_transaction_id would leave
	// the reversal out of step with it (and put balance_due back on a
	// CANCELLED row). A paid one is corrected by voiding the payment first.
	switch existing.Status {
	case "CANCELLED":
		return nil, status.Errorf(codes.FailedPrecondition, "invoice %d is cancelled and can no longer be edited", existing.ID)
	case "PAID":
		return nil, status.Errorf(codes.FailedPrecondition, "invoice %d is paid - void its payment(s) before changing its lines", existing.ID)
	}

	paidAmount, err := ledgermath.NumericToDecimal(existing.PaidAmount)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "reading paid_amount: %v", err)
	}

	var (
		invoice   sqlcgen.Invoice
		lineItems []sqlcgen.InvoiceLineItem
	)
	err = s.store.ExecTx(ctx, func(q *sqlcgen.Queries) error {
		lines, totals, err := buildDocumentLines(ctx, q, existing.BusinessID, req.GetLineItems(), true)
		if err != nil {
			return err
		}
		balanceDue := totals.TotalDecimal.Sub(paidAmount)
		if balanceDue.IsNegative() {
			return fmt.Errorf("new total %s is less than the %s already paid on this invoice", totals.TotalDecimal, paidAmount)
		}
		balanceDueNum, err := ledgermath.DecimalToNumeric(balanceDue)
		if err != nil {
			return err
		}

		// First write to the invoice row: checks the caller's resource_version
		// and takes the row lock the line-item replace and ledger (re)post
		// below run under, so a stale caller fails here before touching
		// anything. SetInvoiceLedgerTransaction later in this transaction
		// bumps the version again - the returned invoice carries the final one.
		invoice, err = q.UpdateInvoiceTotals(ctx, sqlcgen.UpdateInvoiceTotalsParams{
			ID:              req.GetId(),
			Subtotal:        totals.Subtotal,
			TotalTaxAmount:  totals.TotalTax,
			TotalAmount:     totals.Total,
			BalanceDue:      balanceDueNum,
			ResourceVersion: expectedResourceVersion(req.GetResourceVersion()),
		})
		if err != nil {
			return translateUpdateError(err, invoiceRes.kind, req.GetId(), req.GetResourceVersion())
		}
		if err := q.DeleteInvoiceLineItems(ctx, req.GetId()); err != nil {
			return err
		}
		if lineItems, err = insertInvoiceLines(ctx, q, invoice.ID, lines); err != nil {
			return err
		}

		if existing.LedgerTransactionID == nil {
			txnID, err := postInvoiceLedger(ctx, q, existing.BusinessID, invoice, lineItems, &u.ID)
			if err != nil {
				return err
			}
			invoice, err = q.SetInvoiceLedgerTransaction(ctx, sqlcgen.SetInvoiceLedgerTransactionParams{ID: invoice.ID, LedgerTransactionID: &txnID})
			return err
		}
		return repostInvoiceLedger(ctx, q, existing.BusinessID, *existing.LedgerTransactionID, invoice, lineItems)
	})
	if err != nil {
		return nil, txErrorStatus(err)
	}
	return &denarixv1.UpdateInvoiceLineItemsResponse{Invoice: invoiceWithLines(invoice, lineItems)}, nil
}

func (s *invoiceService) GetInvoicePdf(ctx context.Context, req *denarixv1.GetInvoicePdfRequest) (*denarixv1.GetInvoicePdfResponse, error) {
	inv, err := invoiceRes.load(ctx, s.store.Queries, req.GetId(), "VIEWER")
	if err != nil {
		return nil, err
	}
	q := s.store.Queries
	lineItems, err := q.ListInvoiceLineItems(ctx, inv.ID)
	if err != nil {
		return nil, translatePgError(err)
	}
	business, err := q.GetBusiness(ctx, inv.BusinessID)
	if err != nil {
		return nil, translatePgError(err)
	}
	contact, err := q.GetContact(ctx, inv.ContactID)
	if err != nil {
		return nil, translatePgError(err)
	}
	breakdown, err := taxBreakdown(ctx, q, lineItems, invoiceBreakdownLine)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "computing tax breakdown: %v", err)
	}
	content, err := pdf.RenderInvoice(businessParty(business), billToParty(contact), invoiceWithLines(inv, lineItems), breakdown)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rendering pdf: %v", err)
	}
	return &denarixv1.GetInvoicePdfResponse{Content: content}, nil
}

// invoiceToProto converts one invoice, loading its lines.
func invoiceToProto(ctx context.Context, q *sqlcgen.Queries, inv sqlcgen.Invoice) (*denarixv1.Invoice, error) {
	return one(invoicesToProto(ctx, q, []sqlcgen.Invoice{inv}))
}

// invoicesToProto converts a page of invoices with their lines loaded in
// one query.
func invoicesToProto(ctx context.Context, q *sqlcgen.Queries, rows []sqlcgen.Invoice) ([]*denarixv1.Invoice, error) {
	return withChildren(ctx, q, rows,
		func(i sqlcgen.Invoice) int64 { return i.ID },
		(*sqlcgen.Queries).ListInvoiceLineItemsByInvoiceIDs,
		func(li sqlcgen.InvoiceLineItem) int64 { return li.InvoiceID },
		invoiceWithLines)
}

// invoiceWithLines converts an invoice whose lines the caller already holds.
func invoiceWithLines(inv sqlcgen.Invoice, lineItems []sqlcgen.InvoiceLineItem) *denarixv1.Invoice {
	pb := &denarixv1.Invoice{
		Id:                  inv.ID,
		BusinessId:          inv.BusinessID,
		ContactId:           inv.ContactID,
		InvoiceType:         inv.InvoiceType,
		EstimateId:          inv.EstimateID,
		InvoiceNumber:       inv.InvoiceNumber,
		InvoiceDate:         datepb.ToProto(inv.InvoiceDate),
		DueDate:             datepb.ToProto(inv.DueDate),
		Subtotal:            moneypb.ToProto(inv.Subtotal),
		TotalTaxAmount:      moneypb.ToProto(inv.TotalTaxAmount),
		TotalAmount:         moneypb.ToProto(inv.TotalAmount),
		PaidAmount:          moneypb.ToProto(inv.PaidAmount),
		BalanceDue:          moneypb.ToProto(inv.BalanceDue),
		Status:              inv.Status,
		Notes:               inv.Notes,
		Terms:               inv.Terms,
		LedgerTransactionId: inv.LedgerTransactionID,
		CreatedByUserId:     inv.CreatedByUserID,
		CreatedAt:           timestampProto(inv.CreatedAt),
		ResourceVersion:     inv.ResourceVersion,
	}
	for _, li := range lineItems {
		a := lineAmountsToProto(li.Quantity, li.UnitPrice, li.LineSubtotal, li.TaxAmount, li.LineTotal)
		pb.LineItems = append(pb.LineItems, &denarixv1.InvoiceLineItem{
			Id:              li.ID,
			InvoiceId:       li.InvoiceID,
			ItemId:          li.ItemID,
			LedgerAccountId: li.LedgerAccountID,
			LineNumber:      li.LineNumber,
			Description:     li.Description,
			Quantity:        a.Quantity,
			UnitPrice:       a.UnitPrice,
			LineSubtotal:    a.LineSubtotal,
			IsTaxable:       li.IsTaxable,
			TaxRateId:       li.TaxRateID,
			TaxAmount:       a.TaxAmount,
			LineTotal:       a.LineTotal,
		})
	}
	return pb
}
