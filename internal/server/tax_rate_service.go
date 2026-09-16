// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/auth"
	"github.com/silverbp/denarix/internal/db"
	"github.com/silverbp/denarix/internal/db/sqlcgen"
	"github.com/silverbp/denarix/internal/moneypb"
)

type taxRateService struct {
	denarixv1.UnimplementedTaxRateServiceServer
	store *db.Store
}

func newTaxRateService(store *db.Store) *taxRateService {
	return &taxRateService{store: store}
}

func (s *taxRateService) GetTaxRate(ctx context.Context, req *denarixv1.GetTaxRateRequest) (*denarixv1.GetTaxRateResponse, error) {
	tr, err := taxRateRes.load(ctx, s.store.Queries, req.GetId(), "VIEWER")
	if err != nil {
		return nil, err
	}
	return &denarixv1.GetTaxRateResponse{TaxRate: taxRateToProto(tr)}, nil
}

func (s *taxRateService) ListTaxRates(ctx context.Context, req *denarixv1.ListTaxRatesRequest) (*denarixv1.ListTaxRatesResponse, error) {
	if err := auth.RequireBusinessRole(ctx, s.store.Queries, req.GetBusinessId(), "VIEWER"); err != nil {
		return nil, err
	}
	rows, err := s.store.Queries.ListTaxRates(ctx, req.GetBusinessId())
	if err != nil {
		return nil, translatePgError(err)
	}
	resp := &denarixv1.ListTaxRatesResponse{}
	for _, tr := range rows {
		resp.TaxRates = append(resp.TaxRates, taxRateToProto(tr))
	}
	return resp, nil
}

func (s *taxRateService) CreateTaxRate(ctx context.Context, req *denarixv1.CreateTaxRateRequest) (*denarixv1.CreateTaxRateResponse, error) {
	u, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no authenticated user")
	}
	if err := auth.RequireBusinessRole(ctx, s.store.Queries, req.GetBusinessId(), "ADMIN"); err != nil {
		return nil, err
	}
	if req.GetName() == "" || req.GetRate() == nil || req.GetTaxLiabilityAccountId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "name, rate, and tax_liability_account_id are required")
	}
	if err := requirePostableAccount(ctx, s.store.Queries, req.GetBusinessId(), req.GetTaxLiabilityAccountId()); err != nil {
		return nil, err
	}

	rate, err := moneypb.ToNumeric(req.GetRate())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid rate: %v", err)
	}

	created, err := s.store.Queries.CreateTaxRate(ctx, sqlcgen.CreateTaxRateParams{
		BusinessID:            req.GetBusinessId(),
		Name:                  req.GetName(),
		Rate:                  rate,
		TaxLiabilityAccountID: req.GetTaxLiabilityAccountId(),
		CreatedByUserID:       &u.ID,
	})
	if err != nil {
		return nil, translatePgError(err)
	}
	return &denarixv1.CreateTaxRateResponse{TaxRate: taxRateToProto(created)}, nil
}

func (s *taxRateService) UpdateTaxRate(ctx context.Context, req *denarixv1.UpdateTaxRateRequest) (*denarixv1.UpdateTaxRateResponse, error) {
	if _, err := taxRateRes.load(ctx, s.store.Queries, req.GetId(), "ADMIN"); err != nil {
		return nil, err
	}

	rate, err := moneypb.ToNumeric(req.Rate)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid rate: %v", err)
	}

	updated, err := s.store.Queries.UpdateTaxRate(ctx, sqlcgen.UpdateTaxRateParams{
		ID:              req.GetId(),
		Name:            req.Name,
		Rate:            rate,
		ResourceVersion: expectedResourceVersion(req.GetResourceVersion()),
	})
	if err != nil {
		return nil, translateUpdateError(err, taxRateRes.kind, req.GetId(), req.GetResourceVersion())
	}
	return &denarixv1.UpdateTaxRateResponse{TaxRate: taxRateToProto(updated)}, nil
}

func (s *taxRateService) DeactivateTaxRate(ctx context.Context, req *denarixv1.DeactivateTaxRateRequest) (*denarixv1.DeactivateTaxRateResponse, error) {
	if _, err := taxRateRes.load(ctx, s.store.Queries, req.GetId(), "ADMIN"); err != nil {
		return nil, err
	}
	deactivated, err := s.store.Queries.DeactivateTaxRate(ctx, sqlcgen.DeactivateTaxRateParams{
		ID:              req.GetId(),
		ResourceVersion: expectedResourceVersion(req.GetResourceVersion()),
	})
	if err != nil {
		return nil, translateUpdateError(err, taxRateRes.kind, req.GetId(), req.GetResourceVersion())
	}
	return &denarixv1.DeactivateTaxRateResponse{TaxRate: taxRateToProto(deactivated)}, nil
}

func taxRateToProto(tr sqlcgen.TaxRate) *denarixv1.TaxRate {
	return &denarixv1.TaxRate{
		Id:                    tr.ID,
		BusinessId:            tr.BusinessID,
		Name:                  tr.Name,
		Rate:                  moneypb.ToProto(tr.Rate),
		TaxLiabilityAccountId: tr.TaxLiabilityAccountID,
		IsActive:              tr.IsActive,
		CreatedByUserId:       tr.CreatedByUserID,
		CreatedAt:             timestampProto(tr.CreatedAt),
		UpdatedAt:             timestampProto(tr.UpdatedAt),
		ResourceVersion:       tr.ResourceVersion,
	}
}
