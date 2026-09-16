// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

// EstimateService — no ledger impact by design (docs/schema.md).

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/auth"
	"github.com/silverbp/denarix/internal/datepb"
	"github.com/silverbp/denarix/internal/db"
	"github.com/silverbp/denarix/internal/db/sqlcgen"
	"github.com/silverbp/denarix/internal/moneypb"
	"github.com/silverbp/denarix/internal/pdf"
)

type estimateService struct {
	denarixv1.UnimplementedEstimateServiceServer
	store *db.Store
}

func newEstimateService(store *db.Store) *estimateService {
	return &estimateService{store: store}
}

func (s *estimateService) GetEstimate(ctx context.Context, req *denarixv1.GetEstimateRequest) (*denarixv1.GetEstimateResponse, error) {
	e, err := estimateRes.load(ctx, s.store.Queries, req.GetId(), "VIEWER")
	if err != nil {
		return nil, err
	}
	pb, err := estimateToProto(ctx, s.store.Queries, e)
	if err != nil {
		return nil, err
	}
	return &denarixv1.GetEstimateResponse{Estimate: pb}, nil
}

func (s *estimateService) ListEstimates(ctx context.Context, req *denarixv1.ListEstimatesRequest) (*denarixv1.ListEstimatesResponse, error) {
	if err := auth.RequireBusinessRole(ctx, s.store.Queries, req.GetBusinessId(), "VIEWER"); err != nil {
		return nil, err
	}
	rows, err := s.store.Queries.ListEstimates(ctx, sqlcgen.ListEstimatesParams{
		BusinessID: req.GetBusinessId(),
		IncludeAll: req.GetIncludeAll(),
	})
	if err != nil {
		return nil, translatePgError(err)
	}
	pbs, err := estimatesToProto(ctx, s.store.Queries, rows)
	if err != nil {
		return nil, err
	}
	return &denarixv1.ListEstimatesResponse{Estimates: pbs}, nil
}

func (s *estimateService) CreateEstimate(ctx context.Context, req *denarixv1.CreateEstimateRequest) (*denarixv1.CreateEstimateResponse, error) {
	u, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no authenticated user")
	}
	if err := auth.RequireBusinessRole(ctx, s.store.Queries, req.GetBusinessId(), "MEMBER"); err != nil {
		return nil, err
	}
	if len(req.GetLineItems()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one line item is required")
	}
	if req.GetEstimateDate() == nil || req.GetExpirationDate() == nil {
		return nil, status.Error(codes.InvalidArgument, "estimate_date and expiration_date are required")
	}
	if _, err := contactRes.requireInBusiness(ctx, s.store.Queries, req.GetBusinessId(), req.GetCustomerId()); err != nil {
		return nil, err
	}

	var (
		estimate  sqlcgen.Estimate
		lineItems []sqlcgen.EstimateLineItem
	)
	err := s.store.ExecTx(ctx, func(q *sqlcgen.Queries) error {
		lines, totals, err := buildDocumentLines(ctx, q, req.GetBusinessId(), req.GetLineItems(), false)
		if err != nil {
			return err
		}

		claimed, err := q.ConsumeNextEstimateNumber(ctx, req.GetBusinessId())
		if err != nil {
			return err
		}
		estimateNumber := fmt.Sprintf("%s%d", derefOr(claimed.Prefix, "EST"), claimed.ClaimedNumber)

		estimate, err = q.CreateEstimate(ctx, sqlcgen.CreateEstimateParams{
			BusinessID:      req.GetBusinessId(),
			CustomerID:      req.GetCustomerId(),
			EstimateNumber:  estimateNumber,
			EstimateDate:    datepb.ToPgDate(req.GetEstimateDate()),
			ExpirationDate:  datepb.ToPgDate(req.GetExpirationDate()),
			Subtotal:        totals.Subtotal,
			TotalTaxAmount:  totals.TotalTax,
			TotalAmount:     totals.Total,
			Notes:           req.Notes,
			Terms:           req.Terms,
			CreatedByUserID: &u.ID,
		})
		if err != nil {
			return err
		}
		lineItems, err = insertEstimateLines(ctx, q, estimate.ID, lines)
		return err
	})
	if err != nil {
		return nil, txErrorStatus(err)
	}
	return &denarixv1.CreateEstimateResponse{Estimate: estimateWithLines(estimate, lineItems)}, nil
}

func (s *estimateService) UpdateEstimateStatus(ctx context.Context, req *denarixv1.UpdateEstimateStatusRequest) (*denarixv1.UpdateEstimateStatusResponse, error) {
	if _, err := estimateRes.load(ctx, s.store.Queries, req.GetId(), "MEMBER"); err != nil {
		return nil, err
	}

	updated, err := s.store.Queries.UpdateEstimateStatus(ctx, sqlcgen.UpdateEstimateStatusParams{
		ID:              req.GetId(),
		Status:          req.GetStatus(),
		ResourceVersion: expectedResourceVersion(req.GetResourceVersion()),
	})
	if err != nil {
		return nil, translateUpdateError(err, estimateRes.kind, req.GetId(), req.GetResourceVersion())
	}
	pb, err := estimateToProto(ctx, s.store.Queries, updated)
	if err != nil {
		return nil, err
	}
	return &denarixv1.UpdateEstimateStatusResponse{Estimate: pb}, nil
}

// UpdateEstimateLineItems replaces an estimate's entire line item set and
// recomputes its totals — see the proto doc for why this is a full
// replace rather than a per-line patch.
func (s *estimateService) UpdateEstimateLineItems(ctx context.Context, req *denarixv1.UpdateEstimateLineItemsRequest) (*denarixv1.UpdateEstimateLineItemsResponse, error) {
	existing, err := estimateRes.load(ctx, s.store.Queries, req.GetId(), "MEMBER")
	if err != nil {
		return nil, err
	}
	if len(req.GetLineItems()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one line item is required")
	}

	var (
		estimate  sqlcgen.Estimate
		lineItems []sqlcgen.EstimateLineItem
	)
	err = s.store.ExecTx(ctx, func(q *sqlcgen.Queries) error {
		lines, totals, err := buildDocumentLines(ctx, q, existing.BusinessID, req.GetLineItems(), false)
		if err != nil {
			return err
		}

		// First write to the estimate row: checks the caller's resource_version
		// and takes the row lock the line-item replace below runs under, so a
		// stale caller fails here before touching anything.
		estimate, err = q.UpdateEstimateTotals(ctx, sqlcgen.UpdateEstimateTotalsParams{
			ID:              req.GetId(),
			Subtotal:        totals.Subtotal,
			TotalTaxAmount:  totals.TotalTax,
			TotalAmount:     totals.Total,
			ResourceVersion: expectedResourceVersion(req.GetResourceVersion()),
		})
		if err != nil {
			return translateUpdateError(err, estimateRes.kind, req.GetId(), req.GetResourceVersion())
		}
		if err := q.DeleteEstimateLineItems(ctx, req.GetId()); err != nil {
			return err
		}
		lineItems, err = insertEstimateLines(ctx, q, estimate.ID, lines)
		return err
	})
	if err != nil {
		return nil, txErrorStatus(err)
	}
	return &denarixv1.UpdateEstimateLineItemsResponse{Estimate: estimateWithLines(estimate, lineItems)}, nil
}

func (s *estimateService) GetEstimatePdf(ctx context.Context, req *denarixv1.GetEstimatePdfRequest) (*denarixv1.GetEstimatePdfResponse, error) {
	e, err := estimateRes.load(ctx, s.store.Queries, req.GetId(), "VIEWER")
	if err != nil {
		return nil, err
	}
	q := s.store.Queries
	lineItems, err := q.ListEstimateLineItems(ctx, e.ID)
	if err != nil {
		return nil, translatePgError(err)
	}
	business, err := q.GetBusiness(ctx, e.BusinessID)
	if err != nil {
		return nil, translatePgError(err)
	}
	customer, err := q.GetContact(ctx, e.CustomerID)
	if err != nil {
		return nil, translatePgError(err)
	}
	breakdown, err := taxBreakdown(ctx, q, lineItems, estimateBreakdownLine)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "computing tax breakdown: %v", err)
	}
	content, err := pdf.RenderEstimate(businessParty(business), billToParty(customer), estimateWithLines(e, lineItems), breakdown)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rendering pdf: %v", err)
	}
	return &denarixv1.GetEstimatePdfResponse{Content: content}, nil
}

// estimateToProto converts one estimate, loading its lines.
func (s *estimateService) UpdateEstimate(ctx context.Context, req *denarixv1.UpdateEstimateRequest) (*denarixv1.UpdateEstimateResponse, error) {
	if _, err := estimateRes.load(ctx, s.store.Queries, req.GetId(), "MEMBER"); err != nil {
		return nil, err
	}
	updated, err := s.store.Queries.UpdateEstimateHeader(ctx, sqlcgen.UpdateEstimateHeaderParams{
		ID:              req.GetId(),
		Notes:           req.Notes,
		Terms:           req.Terms,
		ExpirationDate:  datepb.ToPgDate(req.GetExpirationDate()),
		ResourceVersion: expectedResourceVersion(req.GetResourceVersion()),
	})
	if err != nil {
		return nil, translateUpdateError(err, estimateRes.kind, req.GetId(), req.GetResourceVersion())
	}
	pb, err := estimateToProto(ctx, s.store.Queries, updated)
	if err != nil {
		return nil, err
	}
	return &denarixv1.UpdateEstimateResponse{Estimate: pb}, nil
}

func estimateToProto(ctx context.Context, q *sqlcgen.Queries, e sqlcgen.Estimate) (*denarixv1.Estimate, error) {
	return one(estimatesToProto(ctx, q, []sqlcgen.Estimate{e}))
}

// estimatesToProto converts a page of estimates with their lines loaded in
// one query.
func estimatesToProto(ctx context.Context, q *sqlcgen.Queries, rows []sqlcgen.Estimate) ([]*denarixv1.Estimate, error) {
	return withChildren(ctx, q, rows,
		func(e sqlcgen.Estimate) int64 { return e.ID },
		(*sqlcgen.Queries).ListEstimateLineItemsByEstimateIDs,
		func(li sqlcgen.EstimateLineItem) int64 { return li.EstimateID },
		estimateWithLines)
}

// estimateWithLines converts an estimate whose lines the caller already
// holds.
func estimateWithLines(e sqlcgen.Estimate, lineItems []sqlcgen.EstimateLineItem) *denarixv1.Estimate {
	pb := &denarixv1.Estimate{
		Id:              e.ID,
		BusinessId:      e.BusinessID,
		CustomerId:      e.CustomerID,
		EstimateNumber:  e.EstimateNumber,
		EstimateDate:    datepb.ToProto(e.EstimateDate),
		ExpirationDate:  datepb.ToProto(e.ExpirationDate),
		Subtotal:        moneypb.ToProto(e.Subtotal),
		TotalTaxAmount:  moneypb.ToProto(e.TotalTaxAmount),
		TotalAmount:     moneypb.ToProto(e.TotalAmount),
		Status:          e.Status,
		Notes:           e.Notes,
		Terms:           e.Terms,
		CreatedByUserId: e.CreatedByUserID,
		CreatedAt:       timestampProto(e.CreatedAt),
		ResourceVersion: e.ResourceVersion,
	}
	for _, li := range lineItems {
		a := lineAmountsToProto(li.Quantity, li.UnitPrice, li.LineSubtotal, li.TaxAmount, li.LineTotal)
		pb.LineItems = append(pb.LineItems, &denarixv1.EstimateLineItem{
			Id:           li.ID,
			EstimateId:   li.EstimateID,
			ItemId:       li.ItemID,
			LineNumber:   li.LineNumber,
			Description:  li.Description,
			Quantity:     a.Quantity,
			UnitPrice:    a.UnitPrice,
			LineSubtotal: a.LineSubtotal,
			IsTaxable:    li.IsTaxable,
			TaxRateId:    li.TaxRateID,
			TaxAmount:    a.TaxAmount,
			LineTotal:    a.LineTotal,
		})
	}
	return pb
}
