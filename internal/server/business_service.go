// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/auth"
	"github.com/silverbp/denarix/internal/db"
	"github.com/silverbp/denarix/internal/db/sqlcgen"
	"github.com/silverbp/denarix/internal/moneypb"
	"github.com/silverbp/denarix/internal/periodclose"
)

type businessService struct {
	denarixv1.UnimplementedBusinessServiceServer
	store *db.Store
}

func newBusinessService(store *db.Store) *businessService {
	return &businessService{store: store}
}

func (s *businessService) GetBusiness(ctx context.Context, req *denarixv1.GetBusinessRequest) (*denarixv1.GetBusinessResponse, error) {
	b, err := businessRes.load(ctx, s.store.Queries, req.GetId(), "VIEWER")
	if err != nil {
		return nil, err
	}
	return &denarixv1.GetBusinessResponse{Business: businessToProto(b)}, nil
}

func (s *businessService) ListMyBusinesses(ctx context.Context, _ *denarixv1.ListMyBusinessesRequest) (*denarixv1.ListMyBusinessesResponse, error) {
	u, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no authenticated user")
	}

	rows, err := s.store.Queries.ListBusinessesForUser(ctx, u.ID)
	if err != nil {
		return nil, translatePgError(err)
	}

	resp := &denarixv1.ListMyBusinessesResponse{}
	for _, row := range rows {
		resp.Memberships = append(resp.Memberships, &denarixv1.BusinessMembership{
			Business: businessToProto(sqlcgen.Business{
				ID:                      row.ID,
				Name:                    row.Name,
				TaxID:                   row.TaxID,
				AddressLine1:            row.AddressLine1,
				AddressLine2:            row.AddressLine2,
				City:                    row.City,
				State:                   row.State,
				PostalCode:              row.PostalCode,
				Country:                 row.Country,
				Phone:                   row.Phone,
				Email:                   row.Email,
				WebsiteUrl:              row.WebsiteUrl,
				LogoUrl:                 row.LogoUrl,
				DefaultPaymentTermsDays: row.DefaultPaymentTermsDays,
				DefaultTaxRate:          row.DefaultTaxRate,
				DefaultInvoiceTerms:     row.DefaultInvoiceTerms,
				DefaultEstimateTerms:    row.DefaultEstimateTerms,
				InvoiceNumberPrefix:     row.InvoiceNumberPrefix,
				EstimateNumberPrefix:    row.EstimateNumberPrefix,
				NextInvoiceNumber:       row.NextInvoiceNumber,
				NextEstimateNumber:      row.NextEstimateNumber,
				Timezone:                row.Timezone,
				CurrencyCode:            row.CurrencyCode,
				IsActive:                row.IsActive,
				CreatedByUserID:         row.CreatedByUserID,
				CreatedAt:               row.CreatedAt,
				UpdatedAt:               row.UpdatedAt,
				ResourceVersion:         row.ResourceVersion,
				DeletedAt:               row.DeletedAt,
			}),
			Role: row.MembershipRole,
		})
	}
	return resp, nil
}

func (s *businessService) CreateBusiness(ctx context.Context, req *denarixv1.CreateBusinessRequest) (*denarixv1.CreateBusinessResponse, error) {
	if err := auth.RequireGlobalAdmin(ctx, s.store.Queries); err != nil {
		return nil, err
	}
	u, _ := auth.UserFromContext(ctx)
	if req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}

	var created sqlcgen.Business
	err := s.store.ExecTx(ctx, func(q *sqlcgen.Queries) error {
		var err error
		created, err = q.CreateBusiness(ctx, sqlcgen.CreateBusinessParams{
			Name:            req.GetName(),
			TaxID:           req.TaxId,
			AddressLine1:    req.AddressLine1,
			AddressLine2:    req.AddressLine2,
			City:            req.City,
			State:           req.State,
			PostalCode:      req.PostalCode,
			Country:         req.Country,
			Phone:           req.Phone,
			Email:           req.Email,
			CreatedByUserID: &u.ID,
		})
		if err != nil {
			return err
		}
		if _, err = q.CreateBusinessUser(ctx, sqlcgen.CreateBusinessUserParams{
			BusinessID: created.ID,
			UserID:     u.ID,
			Role:       "OWNER",
		}); err != nil {
			return err
		}

		// Every business needs Income Summary / Retained Earnings before it
		// can ever be closed (see docs/architecture.md#period-close).
		if _, _, err = periodclose.ProvisionSystemAccounts(ctx, q, created.ID, &u.ID); err != nil {
			return err
		}
		// ...and its own AR/AP roll-up containers, so `contact create`
		// always has somewhere to hang a new customer/vendor's own
		// sub-account (see getOrCreateContactAccount in system_accounts.go).
		if err = provisionARAPContainers(ctx, q, created.ID, &u.ID); err != nil {
			return err
		}
		// Each provision* call above persists its ids onto this row (a
		// guarded UPDATE per id, since it's resolved one at a time), so
		// `created` - captured right after the initial INSERT - is stale by
		// the time the transaction commits. Nobody could have read the
		// pre-provisioning version (the business doesn't exist to any other
		// caller until this handler returns), so the jump itself is
		// harmless - but the response needs to reflect it, not the stale
		// copy.
		created, err = q.GetBusiness(ctx, created.ID)
		return err
	})
	if err != nil {
		return nil, txErrorStatus(err)
	}
	return &denarixv1.CreateBusinessResponse{Business: businessToProto(created)}, nil
}

func (s *businessService) UpdateBusiness(ctx context.Context, req *denarixv1.UpdateBusinessRequest) (*denarixv1.UpdateBusinessResponse, error) {
	if _, err := businessRes.load(ctx, s.store.Queries, req.GetId(), "ADMIN"); err != nil {
		return nil, err
	}

	updated, err := s.store.Queries.UpdateBusiness(ctx, sqlcgen.UpdateBusinessParams{
		ID:              req.GetId(),
		Name:            req.Name,
		TaxID:           req.TaxId,
		AddressLine1:    req.AddressLine1,
		AddressLine2:    req.AddressLine2,
		City:            req.City,
		State:           req.State,
		PostalCode:      req.PostalCode,
		Country:         req.Country,
		Phone:           req.Phone,
		Email:           req.Email,
		ResourceVersion: expectedResourceVersion(req.GetResourceVersion()),
	})
	if err != nil {
		return nil, translateUpdateError(err, businessRes.kind, req.GetId(), req.GetResourceVersion())
	}
	return &denarixv1.UpdateBusinessResponse{Business: businessToProto(updated)}, nil
}

func (s *businessService) DeactivateBusiness(ctx context.Context, req *denarixv1.DeactivateBusinessRequest) (*denarixv1.DeactivateBusinessResponse, error) {
	if _, err := businessRes.load(ctx, s.store.Queries, req.GetId(), "ADMIN"); err != nil {
		return nil, err
	}

	deactivated, err := s.store.Queries.DeactivateBusiness(ctx, sqlcgen.DeactivateBusinessParams{
		ID:              req.GetId(),
		ResourceVersion: expectedResourceVersion(req.GetResourceVersion()),
	})
	if err != nil {
		return nil, translateUpdateError(err, businessRes.kind, req.GetId(), req.GetResourceVersion())
	}
	return &denarixv1.DeactivateBusinessResponse{Business: businessToProto(deactivated)}, nil
}

// businessInviteTTL is deliberately short — a copy/pasted invite token
// sitting unused in a chat channel is exposure with no benefit; 7 days is
// enough for someone to actually see it and register.
const businessInviteTTL = 7 * 24 * time.Hour

var validBusinessUserRoles = map[string]bool{"OWNER": true, "ADMIN": true, "MEMBER": true, "VIEWER": true}

// CreateBusinessInvite is callable by a global admin (any business) or
// that business's own OWNER/ADMIN. It never creates a business_user row
// itself — only AcceptBusinessInvite does, once the invitee proves they
// hold the token AND are signed in as the invited email.
func (s *businessService) CreateBusinessInvite(ctx context.Context, req *denarixv1.CreateBusinessInviteRequest) (*denarixv1.CreateBusinessInviteResponse, error) {
	if err := auth.RequireGlobalAdminOrBusinessRole(ctx, s.store.Queries, req.GetBusinessId(), "ADMIN"); err != nil {
		return nil, err
	}
	u, _ := auth.UserFromContext(ctx)

	email := strings.TrimSpace(req.GetEmail())
	if email == "" {
		return nil, status.Error(codes.InvalidArgument, "email is required")
	}
	role := req.GetRole()
	if !validBusinessUserRoles[role] {
		return nil, status.Errorf(codes.InvalidArgument, "role must be one of OWNER, ADMIN, MEMBER, VIEWER")
	}

	rawToken, err := auth.NewInviteToken()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "generating invite token: %v", err)
	}

	created, err := s.store.Queries.CreateBusinessInvite(ctx, sqlcgen.CreateBusinessInviteParams{
		BusinessID:      req.GetBusinessId(),
		Email:           email,
		Role:            role,
		TokenHash:       auth.HashInviteToken(rawToken),
		InvitedByUserID: &u.ID,
		ExpiresAt:       pgtype.Timestamp{Time: time.Now().Add(businessInviteTTL), Valid: true},
	})
	if err != nil {
		return nil, translatePgError(err)
	}

	return &denarixv1.CreateBusinessInviteResponse{
		Invite: businessInviteToProto(created),
		Token:  rawToken,
	}, nil
}

func (s *businessService) ListBusinessInvites(ctx context.Context, req *denarixv1.ListBusinessInvitesRequest) (*denarixv1.ListBusinessInvitesResponse, error) {
	if err := auth.RequireGlobalAdminOrBusinessRole(ctx, s.store.Queries, req.GetBusinessId(), "ADMIN"); err != nil {
		return nil, err
	}
	rows, err := s.store.Queries.ListBusinessInvitesForBusiness(ctx, req.GetBusinessId())
	if err != nil {
		return nil, translatePgError(err)
	}
	resp := &denarixv1.ListBusinessInvitesResponse{}
	for _, r := range rows {
		resp.Invites = append(resp.Invites, businessInviteToProto(r))
	}
	return resp, nil
}

func (s *businessService) RevokeBusinessInvite(ctx context.Context, req *denarixv1.RevokeBusinessInviteRequest) (*denarixv1.RevokeBusinessInviteResponse, error) {
	// Not in the resource table: invites are gated by global-admin OR
	// business role, which no other resource is.
	invite, err := s.store.Queries.GetBusinessInvite(ctx, req.GetId())
	if err != nil {
		if isNoRows(err) {
			return nil, status.Errorf(codes.NotFound, "invite %d not found", req.GetId())
		}
		return nil, translatePgError(err)
	}
	if err := auth.RequireGlobalAdminOrBusinessRole(ctx, s.store.Queries, invite.BusinessID, "ADMIN"); err != nil {
		return nil, err
	}

	revoked, err := s.store.Queries.RevokeBusinessInvite(ctx, req.GetId())
	if err != nil {
		if isNoRows(err) {
			return nil, status.Errorf(codes.FailedPrecondition, "invite %d was already accepted or revoked", req.GetId())
		}
		return nil, translatePgError(err)
	}
	return &denarixv1.RevokeBusinessInviteResponse{Invite: businessInviteToProto(revoked)}, nil
}

// AcceptBusinessInvite is callable by anyone authenticated, but only
// succeeds if the token is valid, unexpired, and unused AND the caller's
// own account email matches the invite's — knowing/guessing the invited
// email alone isn't enough, since passkey registration has no
// email-ownership verification of its own.
func (s *businessService) AcceptBusinessInvite(ctx context.Context, req *denarixv1.AcceptBusinessInviteRequest) (*denarixv1.AcceptBusinessInviteResponse, error) {
	u, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no authenticated user")
	}
	if req.GetToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "token is required")
	}

	invite, err := s.store.Queries.GetPendingBusinessInviteByTokenHash(ctx, auth.HashInviteToken(req.GetToken()))
	if err != nil {
		if isNoRows(err) {
			return nil, status.Error(codes.NotFound, "invite not found, expired, or already used")
		}
		return nil, translatePgError(err)
	}
	if !strings.EqualFold(invite.Email, u.Email) {
		return nil, status.Errorf(codes.PermissionDenied, "this invite was sent to a different email address")
	}

	var business sqlcgen.Business
	err = s.store.ExecTx(ctx, func(q *sqlcgen.Queries) error {
		if _, err := q.CreateBusinessUser(ctx, sqlcgen.CreateBusinessUserParams{
			BusinessID: invite.BusinessID,
			UserID:     u.ID,
			Role:       invite.Role,
		}); err != nil {
			return err
		}
		if _, err := q.AcceptBusinessInvite(ctx, sqlcgen.AcceptBusinessInviteParams{ID: invite.ID, AcceptedByUserID: &u.ID}); err != nil {
			return err
		}
		var err error
		business, err = q.GetBusiness(ctx, invite.BusinessID)
		return err
	})
	if err != nil {
		return nil, txErrorStatus(err)
	}
	return &denarixv1.AcceptBusinessInviteResponse{Business: businessToProto(business), Role: invite.Role}, nil
}

func businessInviteToProto(i sqlcgen.BusinessInvite) *denarixv1.BusinessInvite {
	return &denarixv1.BusinessInvite{
		Id:              i.ID,
		BusinessId:      i.BusinessID,
		Email:           i.Email,
		Role:            i.Role,
		InvitedByUserId: i.InvitedByUserID,
		CreatedAt:       timestampProto(i.CreatedAt),
		ExpiresAt:       timestampProto(i.ExpiresAt),
		AcceptedAt:      timestampProto(i.AcceptedAt),
		RevokedAt:       timestampProto(i.RevokedAt),
	}
}

func businessToProto(b sqlcgen.Business) *denarixv1.Business {
	return &denarixv1.Business{
		Id:                      b.ID,
		Name:                    b.Name,
		TaxId:                   b.TaxID,
		AddressLine1:            b.AddressLine1,
		AddressLine2:            b.AddressLine2,
		City:                    b.City,
		State:                   b.State,
		PostalCode:              b.PostalCode,
		Country:                 b.Country,
		Phone:                   b.Phone,
		Email:                   b.Email,
		DefaultPaymentTermsDays: b.DefaultPaymentTermsDays,
		DefaultTaxRate:          moneypb.ToProto(b.DefaultTaxRate),
		InvoiceNumberPrefix:     derefOr(b.InvoiceNumberPrefix, ""),
		EstimateNumberPrefix:    derefOr(b.EstimateNumberPrefix, ""),
		Timezone:                derefOr(b.Timezone, ""),
		CurrencyCode:            derefOr(b.CurrencyCode, ""),
		IsActive:                b.IsActive,
		CreatedByUserId:         b.CreatedByUserID,
		CreatedAt:               timestampProto(b.CreatedAt),
		UpdatedAt:               timestampProto(b.UpdatedAt),
		ResourceVersion:         b.ResourceVersion,
	}
}
