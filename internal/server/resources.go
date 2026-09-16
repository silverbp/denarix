// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/silverbp/denarix/internal/auth"
	"github.com/silverbp/denarix/internal/db/sqlcgen"
)

// The resource table. Every tenant-scoped resource is described once here -
// how to fetch its row and which business it belongs to - and every
// handler's "load the row, 404 if missing, check the caller's role on its
// business" prologue is resourceDef.load. The same table backs the other
// "does this id belong to this business" question (requireInBusiness, used
// for ids that arrive inside a request body: an item on a line, the
// entity an attachment points at, an item's ledger account).
//
// Adding a resource: one var below, then handlers use xRes.load(...).

type resourceDef[T any] struct {
	// kind is how the resource reads in errors: "invoice 42 not found".
	kind string
	// get is the sqlc Get* query, as a method expression so the table stays
	// one line per resource.
	get func(q *sqlcgen.Queries, ctx context.Context, id int64) (T, error)
	// businessOf reports which business a row belongs to.
	businessOf func(T) int64
}

// load fetches id and checks the caller holds at least role on the row's
// business: NotFound if there is no such row (or, via RequireBusinessRole,
// if the caller has no membership there at all - a non-member can't tell a
// foreign id from a missing one), PermissionDenied if the caller's role is
// too low.
func (r resourceDef[T]) load(ctx context.Context, q *sqlcgen.Queries, id int64, role string) (T, error) {
	var zero T
	row, err := r.get(q, ctx, id)
	if err != nil {
		if isNoRows(err) {
			return zero, status.Errorf(codes.NotFound, "%s %d not found", r.kind, id)
		}
		return zero, translatePgError(err)
	}
	if err := auth.RequireBusinessRole(ctx, q, r.businessOf(row), role); err != nil {
		return zero, err
	}
	return row, nil
}

// requireInBusiness fetches id for use as a reference inside a request
// already authorized against businessID, and rejects it as InvalidArgument
// unless it exists and belongs to that business. One message for both
// cases, so a caller probing ids can't learn whether a foreign one exists.
func (r resourceDef[T]) requireInBusiness(ctx context.Context, q *sqlcgen.Queries, businessID, id int64) (T, error) {
	var zero T
	row, err := r.get(q, ctx, id)
	if err != nil {
		if isNoRows(err) {
			return zero, status.Errorf(codes.InvalidArgument, "%s %d not found in business %d", r.kind, id, businessID)
		}
		return zero, translatePgError(err)
	}
	if r.businessOf(row) != businessID {
		return zero, status.Errorf(codes.InvalidArgument, "%s %d not found in business %d", r.kind, id, businessID)
	}
	return row, nil
}

var (
	businessRes = resourceDef[sqlcgen.Business]{"business", (*sqlcgen.Queries).GetBusiness,
		func(b sqlcgen.Business) int64 { return b.ID }}
	contactRes = resourceDef[sqlcgen.Contact]{"contact", (*sqlcgen.Queries).GetContact,
		func(c sqlcgen.Contact) int64 { return c.BusinessID }}
	itemRes = resourceDef[sqlcgen.Item]{"item", (*sqlcgen.Queries).GetItem,
		func(i sqlcgen.Item) int64 { return i.BusinessID }}
	taxRateRes = resourceDef[sqlcgen.TaxRate]{"tax rate", (*sqlcgen.Queries).GetTaxRate,
		func(t sqlcgen.TaxRate) int64 { return t.BusinessID }}
	// ledger_account.id is an INTEGER - the one narrowing in the table.
	ledgerAccountRes = resourceDef[sqlcgen.LedgerAccount]{"ledger account",
		func(q *sqlcgen.Queries, ctx context.Context, id int64) (sqlcgen.LedgerAccount, error) {
			return q.GetLedgerAccount(ctx, int32(id))
		},
		func(a sqlcgen.LedgerAccount) int64 { return a.BusinessID }}
	ledgerTransactionRes = resourceDef[sqlcgen.LedgerTransaction]{"ledger transaction", (*sqlcgen.Queries).GetLedgerTransaction,
		func(t sqlcgen.LedgerTransaction) int64 { return t.BusinessID }}
	estimateRes = resourceDef[sqlcgen.Estimate]{"estimate", (*sqlcgen.Queries).GetEstimate,
		func(e sqlcgen.Estimate) int64 { return e.BusinessID }}
	invoiceRes = resourceDef[sqlcgen.Invoice]{"invoice", (*sqlcgen.Queries).GetInvoice,
		func(i sqlcgen.Invoice) int64 { return i.BusinessID }}
	paymentRes = resourceDef[sqlcgen.Payment]{"payment", (*sqlcgen.Queries).GetPayment,
		func(p sqlcgen.Payment) int64 { return p.BusinessID }}
	bankStatementRes = resourceDef[sqlcgen.BankStatement]{"bank statement", (*sqlcgen.Queries).GetBankStatement,
		func(b sqlcgen.BankStatement) int64 { return b.BusinessID }}
	periodCloseRes = resourceDef[sqlcgen.PeriodClose]{"period close", (*sqlcgen.Queries).GetPeriodClose,
		func(p sqlcgen.PeriodClose) int64 { return p.BusinessID }}
	entityContextRes = resourceDef[sqlcgen.EntityContext]{"entity context", (*sqlcgen.Queries).GetEntityContext,
		func(e sqlcgen.EntityContext) int64 { return e.BusinessID }}
	attachmentRes = resourceDef[sqlcgen.Attachment]{"attachment", (*sqlcgen.Queries).GetAttachment,
		func(a sqlcgen.Attachment) int64 { return a.BusinessID }}
)

// prefixStatus prepends context to a status error's message, keeping its
// code: "line 2: item 71 not found in business 1".
func prefixStatus(err error, format string, args ...any) error {
	st, ok := status.FromError(err)
	if !ok {
		return fmt.Errorf(format+": %w", append(args, err)...)
	}
	return status.Errorf(st.Code(), "%s: %s", fmt.Sprintf(format, args...), st.Message())
}
