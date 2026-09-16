// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/auth"
	"github.com/silverbp/denarix/internal/db"
	"github.com/silverbp/denarix/internal/db/sqlcgen"
	"github.com/silverbp/denarix/internal/moneypb"
)

type entityContextService struct {
	denarixv1.UnimplementedEntityContextServiceServer
	store *db.Store
}

func newEntityContextService(store *db.Store) *entityContextService {
	return &entityContextService{store: store}
}

func (s *entityContextService) GetEntityContext(ctx context.Context, req *denarixv1.GetEntityContextRequest) (*denarixv1.GetEntityContextResponse, error) {
	ec, err := entityContextRes.load(ctx, s.store.Queries, req.GetId(), "VIEWER")
	if err != nil {
		return nil, err
	}
	return &denarixv1.GetEntityContextResponse{EntityContext: entityContextToProto(ec)}, nil
}

func (s *entityContextService) ListEntityContext(ctx context.Context, req *denarixv1.ListEntityContextRequest) (*denarixv1.ListEntityContextResponse, error) {
	if err := auth.RequireBusinessRole(ctx, s.store.Queries, req.GetBusinessId(), "VIEWER"); err != nil {
		return nil, err
	}
	rows, err := s.store.Queries.ListEntityContextForEntity(ctx, sqlcgen.ListEntityContextForEntityParams{
		BusinessID:        req.GetBusinessId(),
		EntityType:        req.GetEntityType(),
		EntityID:          req.GetEntityId(),
		IncludeSuperseded: req.GetIncludeSuperseded(),
	})
	if err != nil {
		return nil, translatePgError(err)
	}
	resp := &denarixv1.ListEntityContextResponse{}
	for _, ec := range rows {
		resp.EntityContexts = append(resp.EntityContexts, entityContextToProto(ec))
	}
	return resp, nil
}

func (s *entityContextService) CreateEntityContext(ctx context.Context, req *denarixv1.CreateEntityContextRequest) (*denarixv1.CreateEntityContextResponse, error) {
	u, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no authenticated user")
	}
	if err := auth.RequireBusinessRole(ctx, s.store.Queries, req.GetBusinessId(), "MEMBER"); err != nil {
		return nil, err
	}
	if req.GetContextType() == "" || req.GetContent() == "" {
		return nil, status.Error(codes.InvalidArgument, "context_type and content are required")
	}
	if err := validateEntityRef(ctx, s.store.Queries, req.GetBusinessId(), req.GetEntityType(), req.GetEntityId()); err != nil {
		return nil, err
	}

	var metadata []byte
	if req.MetadataJson != nil {
		if !json.Valid([]byte(req.GetMetadataJson())) {
			return nil, status.Error(codes.InvalidArgument, "metadata_json is not valid JSON")
		}
		metadata = []byte(req.GetMetadataJson())
	}
	confidence, err := moneypb.ToNumeric(req.Confidence)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid confidence: %v", err)
	}

	var created sqlcgen.EntityContext
	err = s.store.ExecTx(ctx, func(q *sqlcgen.Queries) error {
		var err error
		created, err = q.CreateEntityContext(ctx, sqlcgen.CreateEntityContextParams{
			BusinessID:      req.GetBusinessId(),
			EntityType:      req.GetEntityType(),
			EntityID:        req.GetEntityId(),
			ContextType:     req.GetContextType(),
			Content:         req.GetContent(),
			Metadata:        metadata,
			Source:          req.Source,
			Confidence:      confidence,
			CreatedByUserID: &u.ID,
		})
		if err != nil {
			return err
		}
		for _, oldID := range req.GetSupersedesIds() {
			if _, err := entityContextRes.requireInBusiness(ctx, q, req.GetBusinessId(), oldID); err != nil {
				return err
			}
			if err := q.SupersedeEntityContext(ctx, sqlcgen.SupersedeEntityContextParams{ID: oldID, SupersededByID: &created.ID}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, txErrorStatus(err)
	}
	return &denarixv1.CreateEntityContextResponse{EntityContext: entityContextToProto(created)}, nil
}

func (s *entityContextService) DeleteEntityContext(ctx context.Context, req *denarixv1.DeleteEntityContextRequest) (*denarixv1.DeleteEntityContextResponse, error) {
	if _, err := entityContextRes.load(ctx, s.store.Queries, req.GetId(), "MEMBER"); err != nil {
		return nil, err
	}
	var deleted sqlcgen.EntityContext
	err := s.store.ExecTx(ctx, func(q *sqlcgen.Queries) error {
		var err error
		deleted, err = q.DeleteEntityContext(ctx, req.GetId())
		if err != nil {
			return err
		}
		// Anything this note superseded becomes current again - otherwise
		// it would stay hidden from the default listing behind a pointer to
		// a deleted row.
		return q.ClearSupersededBy(ctx, deleted.ID)
	})
	if err != nil {
		if isNoRows(err) {
			return nil, status.Errorf(codes.NotFound, "entity context %d not found", req.GetId())
		}
		return nil, txErrorStatus(err)
	}
	return &denarixv1.DeleteEntityContextResponse{EntityContext: entityContextToProto(deleted)}, nil
}

func entityContextToProto(ec sqlcgen.EntityContext) *denarixv1.EntityContext {
	pb := &denarixv1.EntityContext{
		Id:              ec.ID,
		BusinessId:      ec.BusinessID,
		EntityType:      ec.EntityType,
		EntityId:        ec.EntityID,
		ContextType:     ec.ContextType,
		Content:         ec.Content,
		Source:          ec.Source,
		Confidence:      moneypb.ToProto(ec.Confidence),
		SupersededById:  ec.SupersededByID,
		CreatedByUserId: ec.CreatedByUserID,
		CreatedAt:       timestampProto(ec.CreatedAt),
	}
	if len(ec.Metadata) > 0 {
		s := string(ec.Metadata)
		pb.MetadataJson = &s
	}
	return pb
}
