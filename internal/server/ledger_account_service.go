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
)

type ledgerAccountService struct {
	denarixv1.UnimplementedLedgerAccountServiceServer
	store *db.Store
}

func newLedgerAccountService(store *db.Store) *ledgerAccountService {
	return &ledgerAccountService{store: store}
}

func (s *ledgerAccountService) GetLedgerAccount(ctx context.Context, req *denarixv1.GetLedgerAccountRequest) (*denarixv1.GetLedgerAccountResponse, error) {
	a, err := ledgerAccountRes.load(ctx, s.store.Queries, int64(req.GetId()), "VIEWER")
	if err != nil {
		return nil, err
	}
	return &denarixv1.GetLedgerAccountResponse{Account: ledgerAccountToProto(a)}, nil
}

func (s *ledgerAccountService) ListLedgerAccounts(ctx context.Context, req *denarixv1.ListLedgerAccountsRequest) (*denarixv1.ListLedgerAccountsResponse, error) {
	if err := auth.RequireBusinessRole(ctx, s.store.Queries, req.GetBusinessId(), "VIEWER"); err != nil {
		return nil, err
	}

	rows, err := s.store.Queries.ListLedgerAccounts(ctx, req.GetBusinessId())
	if err != nil {
		return nil, translatePgError(err)
	}

	resp := &denarixv1.ListLedgerAccountsResponse{}
	for _, a := range rows {
		resp.Accounts = append(resp.Accounts, ledgerAccountToProto(a))
	}
	return resp, nil
}

func (s *ledgerAccountService) CreateLedgerAccount(ctx context.Context, req *denarixv1.CreateLedgerAccountRequest) (*denarixv1.CreateLedgerAccountResponse, error) {
	u, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no authenticated user")
	}
	if err := auth.RequireBusinessRole(ctx, s.store.Queries, req.GetBusinessId(), "MEMBER"); err != nil {
		return nil, err
	}
	if req.GetCode() == "" || req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "code and name are required")
	}
	if req.ParentAccountId != nil {
		if _, err := ledgerAccountRes.requireInBusiness(ctx, s.store.Queries, req.GetBusinessId(), int64(req.GetParentAccountId())); err != nil {
			return nil, err
		}
	}

	created, err := s.store.Queries.CreateLedgerAccount(ctx, sqlcgen.CreateLedgerAccountParams{
		BusinessID:                req.GetBusinessId(),
		AccountTypeID:             req.GetAccountTypeId(),
		ParentAccountID:           req.ParentAccountId,
		Code:                      req.GetCode(),
		Name:                      req.GetName(),
		Description:               req.Description,
		IsSystem:                  false, // system accounts are provisioned internally, never via the API
		IsReconcilable:            req.GetIsReconcilable(),
		IsContainer:               req.GetIsContainer(),
		CashFlowCategoryID:        req.CashFlowCategoryId,
		BalanceSheetCategoryID:    req.BalanceSheetCategoryId,
		IncomeStatementCategoryID: req.IncomeStatementCategoryId,
		CreatedByUserID:           &u.ID,
	})
	if err != nil {
		return nil, translatePgError(err)
	}
	return &denarixv1.CreateLedgerAccountResponse{Account: ledgerAccountToProto(created)}, nil
}

func (s *ledgerAccountService) UpdateLedgerAccount(ctx context.Context, req *denarixv1.UpdateLedgerAccountRequest) (*denarixv1.UpdateLedgerAccountResponse, error) {
	if _, err := ledgerAccountRes.load(ctx, s.store.Queries, int64(req.GetId()), "ADMIN"); err != nil {
		return nil, err
	}

	updated, err := s.store.Queries.UpdateLedgerAccount(ctx, sqlcgen.UpdateLedgerAccountParams{
		ID:                        req.GetId(),
		Name:                      req.Name,
		Description:               req.Description,
		IsReconcilable:            req.IsReconcilable,
		IsContainer:               req.IsContainer,
		CashFlowCategoryID:        req.CashFlowCategoryId,
		BalanceSheetCategoryID:    req.BalanceSheetCategoryId,
		IncomeStatementCategoryID: req.IncomeStatementCategoryId,
		ResourceVersion:           expectedResourceVersion(req.GetResourceVersion()),
	})
	if err != nil {
		return nil, translateUpdateError(err, ledgerAccountRes.kind, int64(req.GetId()), req.GetResourceVersion())
	}
	return &denarixv1.UpdateLedgerAccountResponse{Account: ledgerAccountToProto(updated)}, nil
}

func (s *ledgerAccountService) DeactivateLedgerAccount(ctx context.Context, req *denarixv1.DeactivateLedgerAccountRequest) (*denarixv1.DeactivateLedgerAccountResponse, error) {
	existing, err := ledgerAccountRes.load(ctx, s.store.Queries, int64(req.GetId()), "ADMIN")
	if err != nil {
		return nil, err
	}
	if existing.IsSystem {
		return nil, status.Error(codes.FailedPrecondition, "system ledger accounts cannot be deactivated")
	}

	deactivated, err := s.store.Queries.DeactivateLedgerAccount(ctx, sqlcgen.DeactivateLedgerAccountParams{
		ID:              req.GetId(),
		ResourceVersion: expectedResourceVersion(req.GetResourceVersion()),
	})
	if err != nil {
		return nil, translateUpdateError(err, ledgerAccountRes.kind, int64(req.GetId()), req.GetResourceVersion())
	}
	return &denarixv1.DeactivateLedgerAccountResponse{Account: ledgerAccountToProto(deactivated)}, nil
}

func ledgerAccountToProto(a sqlcgen.LedgerAccount) *denarixv1.LedgerAccount {
	return &denarixv1.LedgerAccount{
		Id:                        a.ID,
		BusinessId:                a.BusinessID,
		AccountTypeId:             a.AccountTypeID,
		ParentAccountId:           a.ParentAccountID,
		Code:                      a.Code,
		Name:                      a.Name,
		Description:               a.Description,
		IsSystem:                  a.IsSystem,
		IsReconcilable:            a.IsReconcilable,
		IsContainer:               a.IsContainer,
		CashFlowCategoryId:        a.CashFlowCategoryID,
		BalanceSheetCategoryId:    a.BalanceSheetCategoryID,
		IncomeStatementCategoryId: a.IncomeStatementCategoryID,
		IsActive:                  a.IsActive,
		CreatedByUserId:           a.CreatedByUserID,
		CreatedAt:                 timestampProto(a.CreatedAt),
		UpdatedAt:                 timestampProto(a.UpdatedAt),
		ResourceVersion:           a.ResourceVersion,
	}
}
