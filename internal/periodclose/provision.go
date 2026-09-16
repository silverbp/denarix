// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

// Package periodclose implements the period-close algorithm documented in
// docs/architecture.md, as plain Go over *sqlcgen.Queries — no gRPC
// dependency, so it's directly unit-testable against a real Postgres and
// callable both from the API and (eventually) from business-creation flow.
package periodclose

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/silverbp/denarix/internal/db/sqlcgen"
)

const (
	// EquityAccountTypeID is ledger_account_type.id for EQUITY, seeded by
	// the initial migration.
	EquityAccountTypeID = 3

	// capitalAndReservesCategoryID is balance_sheet_category.id for
	// "Capital & Reserves", seeded by the initial migration - where a swept
	// P&L balance belongs on the balance sheet report
	// (internal/reporting.BalanceSheet).
	capitalAndReservesCategoryID = 4

	// QuickBooks-style numeric account codes (see docs/schema.md's chart-of-accounts numbering
	// convention: 3000s = Equity), not descriptive strings like the schema used to use - used
	// only to resolve (or create, for a business that predates this) the account the first
	// time; business.income_summary_account_id/retained_earnings_account_id is the source of
	// truth after that.
	IncomeSummaryCode    = "3910"
	RetainedEarningsCode = "3900"
)

// ProvisionSystemAccounts ensures a business has its Income Summary and
// Retained Earnings ledger_account ids resolved and persisted on its own
// row (business.income_summary_account_id/retained_earnings_account_id),
// creating whichever are missing. Idempotent — called on every business
// creation and again before every close, but only the very first call for
// a given business does any writing; every later one just reads the ids
// business already has stored (see resolveSystemAccount).
func ProvisionSystemAccounts(ctx context.Context, q *sqlcgen.Queries, businessID int64, createdByUserID *int64) (incomeSummaryID, retainedEarningsID int32, err error) {
	incomeSummaryID, err = resolveSystemAccount(ctx, q, businessID,
		func(b sqlcgen.Business) *int32 { return b.IncomeSummaryAccountID },
		func(id int32) error {
			return q.SetBusinessIncomeSummaryAccountID(ctx, sqlcgen.SetBusinessIncomeSummaryAccountIDParams{ID: businessID, IncomeSummaryAccountID: &id})
		},
		func() (sqlcgen.LedgerAccount, error) {
			return getOrCreateSystemAccount(ctx, q, businessID, IncomeSummaryCode, "Income Summary", createdByUserID)
		})
	if err != nil {
		return 0, 0, fmt.Errorf("provisioning %s: %w", IncomeSummaryCode, err)
	}

	retainedEarningsID, err = resolveSystemAccount(ctx, q, businessID,
		func(b sqlcgen.Business) *int32 { return b.RetainedEarningsAccountID },
		func(id int32) error {
			return q.SetBusinessRetainedEarningsAccountID(ctx, sqlcgen.SetBusinessRetainedEarningsAccountIDParams{ID: businessID, RetainedEarningsAccountID: &id})
		},
		func() (sqlcgen.LedgerAccount, error) {
			return getOrCreateSystemAccount(ctx, q, businessID, RetainedEarningsCode, "Retained Earnings", createdByUserID)
		})
	if err != nil {
		return 0, 0, fmt.Errorf("provisioning %s: %w", RetainedEarningsCode, err)
	}

	return incomeSummaryID, retainedEarningsID, nil
}

// resolveSystemAccount reads the id business.<column> (via stored) already
// holds; if unset, resolves it (by code, idempotent, via findOrCreate) and
// persists it via a guarded UPDATE (persist). Once <column> is set, every
// later call is a pure read - the guard only matters for the first caller
// for a given business, which does bump business.resource_version once via
// the UPDATE (the same class of side effect docs/schema.md's "Patterns
// worth carrying forward" already documents for ConsumeNextInvoiceNumber);
// a second concurrent resolver's UPDATE matches zero rows once the first
// one commits. Mirrors internal/server/system_accounts.go's identically-
// shaped helper for AR/AP - not shared code, since that lives in a
// different package, but the same reasoning: ids don't generalize across
// businesses, only a per-business code lookup does, so that's the one-time
// fallback rather than a hardcoded id.
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

// getOrCreateSystemAccount resolves businessID's Income Summary/Retained
// Earnings account at code, creating it if missing. A found row is only
// adopted if it's actually a postable EQUITY account (not, say, a
// container someone hand-created at this code) - see
// internal/server/system_accounts.go's getOrCreateContainerAccount for the
// identical reasoning: this id gets pinned to the business row forever
// once resolved (resolveSystemAccount), so silently adopting a mismatched
// account would misfile every future close's zeroing/sweep postings with
// no way to reset it short of a direct DB fix. A plain error, not a gRPC
// status - this package has no gRPC dependency (see the package doc
// comment); the caller translates it.
func getOrCreateSystemAccount(ctx context.Context, q *sqlcgen.Queries, businessID int64, code, name string, createdByUserID *int64) (sqlcgen.LedgerAccount, error) {
	existing, err := q.GetLedgerAccountByCode(ctx, sqlcgen.GetLedgerAccountByCodeParams{
		BusinessID: businessID,
		Code:       code,
	})
	if err == nil {
		if existing.IsContainer || !existing.IsActive || existing.AccountTypeID != EquityAccountTypeID {
			return sqlcgen.LedgerAccount{}, fmt.Errorf(
				"ledger account %d already uses code %s but isn't a usable %s account (container=%v active=%v account_type=%d, want a postable, active, EQUITY(%d) account) - "+
					"rename or deactivate-and-reassign it before closing a period",
				existing.ID, code, name, existing.IsContainer, existing.IsActive, existing.AccountTypeID, EquityAccountTypeID)
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return sqlcgen.LedgerAccount{}, err
	}

	categoryID := int32(capitalAndReservesCategoryID)
	return q.CreateLedgerAccount(ctx, sqlcgen.CreateLedgerAccountParams{
		BusinessID:             businessID,
		AccountTypeID:          EquityAccountTypeID,
		Code:                   code,
		Name:                   name,
		IsSystem:               true,
		BalanceSheetCategoryID: &categoryID,
		CreatedByUserID:        createdByUserID,
	})
}
