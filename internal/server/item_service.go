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

// item.item_type values - mirrors the CHECK constraint on item.item_type
// (migrations/00001_initial.up.sql). A string enum like invoice.invoice_type,
// not a proto enum, since more modes are expected and the schema's other
// enums already take that shape.
const (
	itemTypeService      = "SERVICE"       // labour/time, nothing physical
	itemTypeNonInventory = "NON_INVENTORY" // physical product, stock not tracked
	itemTypeInventory    = "INVENTORY"     // physical product, on-hand quantity tracked
)

var validItemTypes = map[string]bool{
	itemTypeService:      true,
	itemTypeNonInventory: true,
	itemTypeInventory:    true,
}

func itemTypeList() string {
	return itemTypeService + ", " + itemTypeNonInventory + ", " + itemTypeInventory
}

type itemService struct {
	denarixv1.UnimplementedItemServiceServer
	store *db.Store
}

func newItemService(store *db.Store) *itemService {
	return &itemService{store: store}
}

func (s *itemService) GetItem(ctx context.Context, req *denarixv1.GetItemRequest) (*denarixv1.GetItemResponse, error) {
	item, err := itemRes.load(ctx, s.store.Queries, req.GetId(), "VIEWER")
	if err != nil {
		return nil, err
	}
	return &denarixv1.GetItemResponse{Item: itemToProto(item)}, nil
}

func (s *itemService) ListItems(ctx context.Context, req *denarixv1.ListItemsRequest) (*denarixv1.ListItemsResponse, error) {
	if err := auth.RequireBusinessRole(ctx, s.store.Queries, req.GetBusinessId(), "VIEWER"); err != nil {
		return nil, err
	}
	rows, err := s.store.Queries.ListItems(ctx, sqlcgen.ListItemsParams{
		BusinessID:      req.GetBusinessId(),
		IncludeInactive: req.GetIncludeInactive(),
	})
	if err != nil {
		return nil, translatePgError(err)
	}
	resp := &denarixv1.ListItemsResponse{}
	for _, item := range rows {
		resp.Items = append(resp.Items, itemToProto(item))
	}
	return resp, nil
}

func (s *itemService) CreateItem(ctx context.Context, req *denarixv1.CreateItemRequest) (*denarixv1.CreateItemResponse, error) {
	u, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no authenticated user")
	}
	if err := auth.RequireBusinessRole(ctx, s.store.Queries, req.GetBusinessId(), "MEMBER"); err != nil {
		return nil, err
	}
	if req.GetItemCode() == "" || req.GetName() == "" || req.GetRetailPrice() == nil {
		return nil, status.Error(codes.InvalidArgument, "item_code, name, and retail_price are required")
	}
	// Every invoice line posts to its item's default_ledger_account_id (there's no per-line
	// override), so an item without one could never be invoiced - reject it up front.
	if req.DefaultLedgerAccountId == nil {
		return nil, status.Error(codes.InvalidArgument, "default_ledger_account_id is required")
	}
	if err := requirePostableAccount(ctx, s.store.Queries, req.GetBusinessId(), req.GetDefaultLedgerAccountId()); err != nil {
		return nil, err
	}
	itemType := itemTypeService
	if req.ItemType != nil {
		itemType = req.GetItemType()
		if !validItemTypes[itemType] {
			return nil, status.Errorf(codes.InvalidArgument, "invalid item_type %q (want one of %s)", itemType, itemTypeList())
		}
	}

	costPrice, err := moneypb.ToNumeric(req.CostPrice)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid cost_price: %v", err)
	}
	retailPrice, err := moneypb.ToNumeric(req.GetRetailPrice())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid retail_price: %v", err)
	}

	created, err := s.store.Queries.CreateItem(ctx, sqlcgen.CreateItemParams{
		BusinessID:             req.GetBusinessId(),
		ItemCode:               req.GetItemCode(),
		ItemType:               itemType,
		Name:                   req.GetName(),
		Description:            req.Description,
		UnitOfMeasure:          req.UnitOfMeasure,
		CostPrice:              costPrice,
		RetailPrice:            retailPrice,
		IsTaxable:              req.GetIsTaxable(),
		DefaultTaxRateID:       req.DefaultTaxRateId,
		DefaultLedgerAccountID: req.DefaultLedgerAccountId,
		CreatedByUserID:        &u.ID,
	})
	if err != nil {
		return nil, translatePgError(err)
	}
	return &denarixv1.CreateItemResponse{Item: itemToProto(created)}, nil
}

func (s *itemService) UpdateItem(ctx context.Context, req *denarixv1.UpdateItemRequest) (*denarixv1.UpdateItemResponse, error) {
	existing, err := itemRes.load(ctx, s.store.Queries, req.GetId(), "MEMBER")
	if err != nil {
		return nil, err
	}

	if req.ItemType != nil && !validItemTypes[req.GetItemType()] {
		return nil, status.Errorf(codes.InvalidArgument, "invalid item_type %q (want one of %s)", req.GetItemType(), itemTypeList())
	}
	if req.DefaultLedgerAccountId != nil {
		if err := requirePostableAccount(ctx, s.store.Queries, existing.BusinessID, req.GetDefaultLedgerAccountId()); err != nil {
			return nil, err
		}
	}
	retailPrice, err := moneypb.ToNumeric(req.RetailPrice)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid retail_price: %v", err)
	}
	costPrice, err := moneypb.ToNumeric(req.CostPrice)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid cost_price: %v", err)
	}

	updated, err := s.store.Queries.UpdateItem(ctx, sqlcgen.UpdateItemParams{
		ID:                     req.GetId(),
		ItemType:               req.ItemType,
		Name:                   req.Name,
		Description:            req.Description,
		RetailPrice:            retailPrice,
		CostPrice:              costPrice,
		IsTaxable:              req.IsTaxable,
		DefaultTaxRateID:       req.DefaultTaxRateId,
		DefaultLedgerAccountID: req.DefaultLedgerAccountId,
		ResourceVersion:        expectedResourceVersion(req.GetResourceVersion()),
	})
	if err != nil {
		return nil, translateUpdateError(err, itemRes.kind, req.GetId(), req.GetResourceVersion())
	}
	return &denarixv1.UpdateItemResponse{Item: itemToProto(updated)}, nil
}

func (s *itemService) DeactivateItem(ctx context.Context, req *denarixv1.DeactivateItemRequest) (*denarixv1.DeactivateItemResponse, error) {
	if _, err := itemRes.load(ctx, s.store.Queries, req.GetId(), "MEMBER"); err != nil {
		return nil, err
	}
	deactivated, err := s.store.Queries.DeactivateItem(ctx, sqlcgen.DeactivateItemParams{
		ID:              req.GetId(),
		ResourceVersion: expectedResourceVersion(req.GetResourceVersion()),
	})
	if err != nil {
		return nil, translateUpdateError(err, itemRes.kind, req.GetId(), req.GetResourceVersion())
	}
	return &denarixv1.DeactivateItemResponse{Item: itemToProto(deactivated)}, nil
}

// requirePostableAccount vets an item's default_ledger_account_id: it must
// exist in businessID (the resource table's tenant check) and be a postable
// account - a container roll-up node can never take a ledger entry, so an
// item pointing at one would fail at invoice time instead of here.
func requirePostableAccount(ctx context.Context, q *sqlcgen.Queries, businessID int64, accountID int32) error {
	acct, err := ledgerAccountRes.requireInBusiness(ctx, q, businessID, int64(accountID))
	if err != nil {
		return err
	}
	if acct.IsContainer {
		return status.Errorf(codes.InvalidArgument, "ledger account %d (%s) is a container, not a postable account", accountID, acct.Name)
	}
	return nil
}

func itemToProto(item sqlcgen.Item) *denarixv1.Item {
	return &denarixv1.Item{
		Id:                     item.ID,
		BusinessId:             item.BusinessID,
		ItemCode:               item.ItemCode,
		ItemType:               item.ItemType,
		Name:                   item.Name,
		Description:            item.Description,
		UnitOfMeasure:          derefOr(item.UnitOfMeasure, "EACH"),
		CostPrice:              moneypb.ToProto(item.CostPrice),
		RetailPrice:            moneypb.ToProto(item.RetailPrice),
		IsTaxable:              item.IsTaxable,
		DefaultTaxRateId:       item.DefaultTaxRateID,
		DefaultLedgerAccountId: item.DefaultLedgerAccountID,
		IsActive:               item.IsActive,
		CreatedByUserId:        item.CreatedByUserID,
		CreatedAt:              timestampProto(item.CreatedAt),
		UpdatedAt:              timestampProto(item.UpdatedAt),
		ResourceVersion:        item.ResourceVersion,
	}
}
