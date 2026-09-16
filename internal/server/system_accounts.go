// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/silverbp/denarix/internal/db/sqlcgen"
)

// Fixed codes for the AR/AP roll-up containers every business gets at
// creation - used only to resolve (or create, for a business that
// predates this) the account the first time; business.ar_account_id/
// ap_account_id is the source of truth after that (see resolveARContainer/
// resolveAPContainer). Matches the QuickBooks-style numbering convention
// documented in migrations/00001_initial.up.sql.
const (
	accountsReceivableCode = "1100"
	accountsPayableCode    = "2000"

	// currentAssetsLiabilitiesCategoryID is balance_sheet_category.id for
	// "Current Assets & Liabilities", seeded by the initial migration.
	currentAssetsLiabilitiesCategoryID = 2
)

// provisionARAPContainers ensures a business has its AR/AP container ids
// resolved and persisted on its own row (business.ar_account_id/
// ap_account_id) - called once at business creation; resolveARContainer/
// resolveAPContainer repeat the same idempotent resolve-and-persist for any
// business that predates this (there's no backfill migration for existing
// businesses, so the first contact created for one self-heals it).
func provisionARAPContainers(ctx context.Context, q *sqlcgen.Queries, businessID int64, createdByUserID *int64) error {
	if _, err := resolveARContainer(ctx, q, businessID, createdByUserID); err != nil {
		return fmt.Errorf("provisioning %s: %w", accountsReceivableCode, err)
	}
	if _, err := resolveAPContainer(ctx, q, businessID, createdByUserID); err != nil {
		return fmt.Errorf("provisioning %s: %w", accountsPayableCode, err)
	}
	return nil
}

// resolveARContainer returns businessID's Accounts Receivable container id,
// preferring the id already stored on the business row (the fast, steady-
// state path - no lookup) and falling back to find-or-create-by-code plus a
// one-time persist for a business that doesn't have it stored yet.
func resolveARContainer(ctx context.Context, q *sqlcgen.Queries, businessID int64, createdByUserID *int64) (int32, error) {
	return resolveSystemAccount(ctx, q, businessID,
		func(b sqlcgen.Business) *int32 { return b.ArAccountID },
		func(id int32) error {
			return q.SetBusinessARAccountID(ctx, sqlcgen.SetBusinessARAccountIDParams{ID: businessID, ArAccountID: &id})
		},
		func() (sqlcgen.LedgerAccount, error) {
			return getOrCreateContainerAccount(ctx, q, businessID, assetsAccountTypeID, accountsReceivableCode, "Accounts Receivable", createdByUserID)
		})
}

// resolveAPContainer is resolveARContainer's AP-side mirror.
func resolveAPContainer(ctx context.Context, q *sqlcgen.Queries, businessID int64, createdByUserID *int64) (int32, error) {
	return resolveSystemAccount(ctx, q, businessID,
		func(b sqlcgen.Business) *int32 { return b.ApAccountID },
		func(id int32) error {
			return q.SetBusinessAPAccountID(ctx, sqlcgen.SetBusinessAPAccountIDParams{ID: businessID, ApAccountID: &id})
		},
		func() (sqlcgen.LedgerAccount, error) {
			return getOrCreateContainerAccount(ctx, q, businessID, liabilitiesAccountTypeID, accountsPayableCode, "Accounts Payable", createdByUserID)
		})
}

// resolveSystemAccount is the shared shape behind resolveARContainer/
// resolveAPContainer (and periodclose's equivalent for Income Summary/
// Retained Earnings): read the id business.<column> already holds; if
// unset, resolve it (by code, idempotent) and persist it via a guarded
// UPDATE (WHERE <column> IS NULL). Once <column> is set, every later call
// is a pure read with no write at all - the guard only matters for the
// first caller for a given business (or a legacy business self-healing
// here for the first time), which does bump business.resource_version
// once via the UPDATE, the same class of side effect docs/schema.md's
// "Patterns worth carrying forward" already documents for
// ConsumeNextInvoiceNumber. A second concurrent resolver's UPDATE matches
// zero rows (no bump, no error) once the first one commits.
func resolveSystemAccount(ctx context.Context, q *sqlcgen.Queries, businessID int64, stored func(sqlcgen.Business) *int32, persist func(int32) error, findOrCreate func() (sqlcgen.LedgerAccount, error)) (int32, error) {
	biz, err := q.GetBusiness(ctx, businessID)
	if err != nil {
		return 0, err
	}
	if id := stored(biz); id != nil {
		return *id, nil
	}
	account, err := findOrCreate()
	if err != nil {
		return 0, err
	}
	if err := persist(account.ID); err != nil {
		return 0, err
	}
	return account.ID, nil
}

// getOrCreateContainerAccount resolves businessID's roll-up container at
// code, creating it (is_container = true, is_system = true) if missing.
// A found row is only adopted if it's actually shaped like the container
// this is looking for - a container, active, the right account type -
// since this id gets pinned to the business row forever once resolved
// (resolveSystemAccount): silently adopting a mismatched account (a
// postable non-container someone hand-created at this code, or one that's
// since been deactivated) would parent every future customer/vendor
// sub-account under the wrong thing with no way to reset it short of a
// direct DB fix.
func getOrCreateContainerAccount(ctx context.Context, q *sqlcgen.Queries, businessID int64, accountTypeID int32, code, name string, createdByUserID *int64) (sqlcgen.LedgerAccount, error) {
	existing, err := q.GetLedgerAccountByCode(ctx, sqlcgen.GetLedgerAccountByCodeParams{BusinessID: businessID, Code: code})
	if err == nil {
		if !existing.IsContainer || !existing.IsActive || existing.AccountTypeID != accountTypeID {
			return sqlcgen.LedgerAccount{}, status.Errorf(codes.FailedPrecondition,
				"ledger account %d already uses code %s but isn't a usable %s container (container=%v active=%v account_type=%d, want %d) - "+
					"rename or deactivate-and-reassign it before creating a contact",
				existing.ID, code, name, existing.IsContainer, existing.IsActive, existing.AccountTypeID, accountTypeID)
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return sqlcgen.LedgerAccount{}, err
	}
	categoryID := int32(currentAssetsLiabilitiesCategoryID)
	return q.CreateLedgerAccount(ctx, sqlcgen.CreateLedgerAccountParams{
		BusinessID:             businessID,
		AccountTypeID:          accountTypeID,
		Code:                   code,
		Name:                   name,
		IsSystem:               true,
		IsContainer:            true,
		BalanceSheetCategoryID: &categoryID,
		CreatedByUserID:        createdByUserID,
	})
}

// getOrCreateContactAccount returns contactID's own postable sub-account
// under the AR (isCustomerSide) or AP container, creating it (is_system =
// true - lifecycle-managed by the contact, not hand-edited via
// `ledger-account`). The code embeds the contact's own id rather than its
// (much longer) contact_number, since ledger_account.code is VARCHAR(20).
func getOrCreateContactAccount(ctx context.Context, q *sqlcgen.Queries, businessID, contactID int64, contactName string, isCustomerSide bool, createdByUserID *int64) (sqlcgen.LedgerAccount, error) {
	accountTypeID := int32(assetsAccountTypeID)
	containerCode := accountsReceivableCode
	var containerID int32
	var err error
	if isCustomerSide {
		containerID, err = resolveARContainer(ctx, q, businessID, createdByUserID)
	} else {
		accountTypeID = liabilitiesAccountTypeID
		containerCode = accountsPayableCode
		containerID, err = resolveAPContainer(ctx, q, businessID, createdByUserID)
	}
	if err != nil {
		return sqlcgen.LedgerAccount{}, err
	}

	code := fmt.Sprintf("%s-%d", containerCode, contactID)
	categoryID := int32(currentAssetsLiabilitiesCategoryID)
	return q.CreateLedgerAccount(ctx, sqlcgen.CreateLedgerAccountParams{
		BusinessID:             businessID,
		AccountTypeID:          accountTypeID,
		Code:                   code,
		Name:                   contactName,
		ParentAccountID:        &containerID,
		IsSystem:               true,
		BalanceSheetCategoryID: &categoryID,
		CreatedByUserID:        createdByUserID,
	})
}
