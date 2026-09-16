// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/auth"
	"github.com/silverbp/denarix/internal/db"
	"github.com/silverbp/denarix/internal/db/sqlcgen"
	"github.com/silverbp/denarix/internal/ledgermath"
	"github.com/silverbp/denarix/internal/moneypb"
	"github.com/silverbp/denarix/internal/reporting"
)

type contactService struct {
	denarixv1.UnimplementedContactServiceServer
	store *db.Store
}

func newContactService(store *db.Store) *contactService {
	return &contactService{store: store}
}

func (s *contactService) GetContact(ctx context.Context, req *denarixv1.GetContactRequest) (*denarixv1.GetContactResponse, error) {
	c, err := contactRes.load(ctx, s.store.Queries, req.GetId(), "VIEWER")
	if err != nil {
		return nil, err
	}
	pb, err := contactToProto(ctx, s.store.Queries, c)
	if err != nil {
		return nil, err
	}
	return &denarixv1.GetContactResponse{Contact: pb}, nil
}

func (s *contactService) ListContacts(ctx context.Context, req *denarixv1.ListContactsRequest) (*denarixv1.ListContactsResponse, error) {
	if err := auth.RequireBusinessRole(ctx, s.store.Queries, req.GetBusinessId(), "VIEWER"); err != nil {
		return nil, err
	}
	rows, err := s.store.Queries.ListContacts(ctx, sqlcgen.ListContactsParams{
		BusinessID:      req.GetBusinessId(),
		IncludeInactive: req.GetIncludeInactive(),
	})
	if err != nil {
		return nil, translatePgError(err)
	}
	pbs, err := contactsToProto(ctx, s.store.Queries, rows)
	if err != nil {
		return nil, err
	}
	return &denarixv1.ListContactsResponse{Contacts: pbs}, nil
}

func (s *contactService) CreateContact(ctx context.Context, req *denarixv1.CreateContactRequest) (*denarixv1.CreateContactResponse, error) {
	u, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no authenticated user")
	}
	if err := auth.RequireBusinessRole(ctx, s.store.Queries, req.GetBusinessId(), "MEMBER"); err != nil {
		return nil, err
	}
	if req.GetContactNumber() == "" || req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "contact_number and name are required")
	}

	creditLimit, err := moneypb.ToNumeric(req.CreditLimit)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid credit_limit: %v", err)
	}

	var created sqlcgen.Contact
	err = s.store.ExecTx(ctx, func(q *sqlcgen.Queries) error {
		var err error
		created, err = q.CreateContact(ctx, sqlcgen.CreateContactParams{
			BusinessID:          req.GetBusinessId(),
			ContactNumber:       req.GetContactNumber(),
			Name:                req.GetName(),
			Email:               req.Email,
			Phone:               req.Phone,
			PaymentTermsDays:    req.PaymentTermsDays,
			CreditLimit:         creditLimit,
			CreatedByUserID:     &u.ID,
			BillingAddressLine1: req.BillingAddressLine1,
			BillingAddressLine2: req.BillingAddressLine2,
			BillingCity:         req.BillingCity,
			BillingState:        req.BillingState,
			BillingPostalCode:   req.BillingPostalCode,
			BillingCountry:      req.BillingCountry,
		})
		if err != nil {
			return err
		}
		if req.GetIsCustomer() {
			// Every customer gets its own AR sub-account, always - see
			// getOrCreateContactAccount. There's no way to point a new
			// customer at an existing account instead.
			acct, err := getOrCreateContactAccount(ctx, q, req.GetBusinessId(), created.ID, created.Name, true, &u.ID)
			if err != nil {
				return err
			}
			if _, err := q.CreateCustomer(ctx, sqlcgen.CreateCustomerParams{ContactID: created.ID, LedgerAccountID: &acct.ID}); err != nil {
				return err
			}
		}
		if req.GetIsVendor() {
			acct, err := getOrCreateContactAccount(ctx, q, req.GetBusinessId(), created.ID, created.Name, false, &u.ID)
			if err != nil {
				return err
			}
			if _, err := q.CreateVendor(ctx, sqlcgen.CreateVendorParams{ContactID: created.ID, LedgerAccountID: &acct.ID}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, txErrorStatus(err)
	}

	pb, err := contactToProto(ctx, s.store.Queries, created)
	if err != nil {
		return nil, err
	}
	return &denarixv1.CreateContactResponse{Contact: pb}, nil
}

func (s *contactService) UpdateContact(ctx context.Context, req *denarixv1.UpdateContactRequest) (*denarixv1.UpdateContactResponse, error) {
	if _, err := contactRes.load(ctx, s.store.Queries, req.GetId(), "MEMBER"); err != nil {
		return nil, err
	}

	creditLimit, err := moneypb.ToNumeric(req.CreditLimit)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid credit_limit: %v", err)
	}

	updated, err := s.store.Queries.UpdateContact(ctx, sqlcgen.UpdateContactParams{
		ID:                  req.GetId(),
		Name:                req.Name,
		Email:               req.Email,
		Phone:               req.Phone,
		PaymentTermsDays:    req.PaymentTermsDays,
		CreditLimit:         creditLimit,
		BillingAddressLine1: req.BillingAddressLine1,
		BillingAddressLine2: req.BillingAddressLine2,
		BillingCity:         req.BillingCity,
		BillingState:        req.BillingState,
		BillingPostalCode:   req.BillingPostalCode,
		BillingCountry:      req.BillingCountry,
		ResourceVersion:     expectedResourceVersion(req.GetResourceVersion()),
	})
	if err != nil {
		return nil, translateUpdateError(err, contactRes.kind, req.GetId(), req.GetResourceVersion())
	}
	pb, err := contactToProto(ctx, s.store.Queries, updated)
	if err != nil {
		return nil, err
	}
	return &denarixv1.UpdateContactResponse{Contact: pb}, nil
}

func (s *contactService) DeactivateContact(ctx context.Context, req *denarixv1.DeactivateContactRequest) (*denarixv1.DeactivateContactResponse, error) {
	if _, err := contactRes.load(ctx, s.store.Queries, req.GetId(), "MEMBER"); err != nil {
		return nil, err
	}
	var deactivated sqlcgen.Contact
	err := s.store.ExecTx(ctx, func(q *sqlcgen.Queries) error {
		var err error
		deactivated, err = q.DeactivateContact(ctx, sqlcgen.DeactivateContactParams{
			ID:              req.GetId(),
			ResourceVersion: expectedResourceVersion(req.GetResourceVersion()),
		})
		if err != nil {
			return err
		}
		if err := deactivateZeroBalanceCustomerAccount(ctx, q, req.GetId()); err != nil {
			return err
		}
		return deactivateZeroBalanceVendorAccount(ctx, q, req.GetId())
	})
	if err != nil {
		return nil, translateUpdateError(err, contactRes.kind, req.GetId(), req.GetResourceVersion())
	}
	pb, err := contactToProto(ctx, s.store.Queries, deactivated)
	if err != nil {
		return nil, err
	}
	return &denarixv1.DeactivateContactResponse{Contact: pb}, nil
}

// ledger_account_type ids a customer/vendor's own sub-ledger account must
// carry - see periodclose.EquityAccountTypeID for the same convention
// applied to EQUITY.
const (
	assetsAccountTypeID      = 1
	liabilitiesAccountTypeID = 2
)

// deactivateZeroBalanceCustomerAccount deactivates a just-deactivated
// contact's customer ledger account too, but only when that account carries
// no balance - a customer with open invoices/payments keeps its account
// active so it stays visible in reporting and reconciliation until the
// balance clears.
func deactivateZeroBalanceCustomerAccount(ctx context.Context, q *sqlcgen.Queries, contactID int64) error {
	customer, err := q.GetCustomerByContactID(ctx, contactID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	return deactivateZeroBalanceAccount(ctx, q, customer.LedgerAccountID)
}

// deactivateZeroBalanceVendorAccount is deactivateZeroBalanceCustomerAccount's
// AP-side mirror.
func deactivateZeroBalanceVendorAccount(ctx context.Context, q *sqlcgen.Queries, contactID int64) error {
	vendor, err := q.GetVendorByContactID(ctx, contactID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	return deactivateZeroBalanceAccount(ctx, q, vendor.LedgerAccountID)
}

func deactivateZeroBalanceAccount(ctx context.Context, q *sqlcgen.Queries, ledgerAccountID *int32) error {
	if ledgerAccountID == nil {
		return nil
	}
	account, err := q.GetLedgerAccount(ctx, *ledgerAccountID)
	if err != nil {
		return err
	}
	if !account.IsActive {
		return nil
	}
	gl, err := reporting.GeneralLedger(ctx, q, account.ID, ledgermath.InceptionDate, time.Now())
	if err != nil {
		return err
	}
	if !gl.EndingBalance.IsZero() {
		return nil
	}
	_, err = q.DeactivateLedgerAccount(ctx, sqlcgen.DeactivateLedgerAccountParams{ID: account.ID})
	return err
}

func contactToProto(ctx context.Context, q *sqlcgen.Queries, c sqlcgen.Contact) (*denarixv1.Contact, error) {
	return one(contactsToProto(ctx, q, []sqlcgen.Contact{c}))
}

// contactsToProto converts a page of contacts, loading every customer and
// vendor role row in two queries rather than two per contact.
func contactsToProto(ctx context.Context, q *sqlcgen.Queries, rows []sqlcgen.Contact) ([]*denarixv1.Contact, error) {
	out := make([]*denarixv1.Contact, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	ids := idsOf(rows, func(c sqlcgen.Contact) int64 { return c.ID })
	customers, err := q.ListCustomersByContactIDs(ctx, ids)
	if err != nil {
		return nil, translatePgError(err)
	}
	vendors, err := q.ListVendorsByContactIDs(ctx, ids)
	if err != nil {
		return nil, translatePgError(err)
	}
	customerOf := groupBy(customers, func(c sqlcgen.Customer) int64 { return c.ContactID })
	vendorOf := groupBy(vendors, func(v sqlcgen.Vendor) int64 { return v.ContactID })

	for i, c := range rows {
		pb := &denarixv1.Contact{
			Id:                  c.ID,
			BusinessId:          c.BusinessID,
			ContactNumber:       c.ContactNumber,
			Name:                c.Name,
			Email:               c.Email,
			Phone:               c.Phone,
			PaymentTermsDays:    c.PaymentTermsDays,
			CreditLimit:         moneypb.ToProto(c.CreditLimit),
			IsActive:            c.IsActive,
			CreatedByUserId:     c.CreatedByUserID,
			CreatedAt:           timestampProto(c.CreatedAt),
			UpdatedAt:           timestampProto(c.UpdatedAt),
			BillingAddressLine1: c.BillingAddressLine1,
			BillingAddressLine2: c.BillingAddressLine2,
			BillingCity:         c.BillingCity,
			BillingState:        c.BillingState,
			BillingPostalCode:   c.BillingPostalCode,
			BillingCountry:      c.BillingCountry,
			ResourceVersion:     c.ResourceVersion,
		}
		// customer.contact_id / vendor.contact_id are unique, so at most one each.
		for _, cust := range customerOf[c.ID] {
			pb.Customer = &denarixv1.Customer{
				Id:              cust.ID,
				ContactId:       cust.ContactID,
				LedgerAccountId: cust.LedgerAccountID,
				CreatedAt:       timestampProto(cust.CreatedAt),
				UpdatedAt:       timestampProto(cust.UpdatedAt),
			}
		}
		for _, ven := range vendorOf[c.ID] {
			pb.Vendor = &denarixv1.Vendor{
				Id:              ven.ID,
				ContactId:       ven.ContactID,
				LedgerAccountId: ven.LedgerAccountID,
				CreatedAt:       timestampProto(ven.CreatedAt),
				UpdatedAt:       timestampProto(ven.UpdatedAt),
			}
		}
		out[i] = pb
	}
	return out, nil
}
