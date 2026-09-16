// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/silverbp/denarix/internal/db/sqlcgen"
)

// entityRefChecks is the allowlist entity_context/attachment validate
// entity_type against — the schema deliberately carries no FK for these
// polymorphic references (docs/schema.md), so this is the only check
// standing between a typo/nonsense entity_type and a silently-orphaned
// row. Each entry is the resource table's own tenant check.
var entityRefChecks = map[string]func(ctx context.Context, q *sqlcgen.Queries, businessID, id int64) error{
	"business":           inBusiness(businessRes),
	"contact":            inBusiness(contactRes),
	"ledger_account":     inBusiness(ledgerAccountRes),
	"ledger_transaction": inBusiness(ledgerTransactionRes),
	"invoice":            inBusiness(invoiceRes),
	"payment":            inBusiness(paymentRes),
	"estimate":           inBusiness(estimateRes),
	"item":               inBusiness(itemRes),
	"tax_rate":           inBusiness(taxRateRes),
	"bank_statement":     inBusiness(bankStatementRes),
	"period_close":       inBusiness(periodCloseRes),
}

func inBusiness[T any](r resourceDef[T]) func(ctx context.Context, q *sqlcgen.Queries, businessID, id int64) error {
	return func(ctx context.Context, q *sqlcgen.Queries, businessID, id int64) error {
		_, err := r.requireInBusiness(ctx, q, businessID, id)
		return err
	}
}

// validateEntityRef confirms entity_type is a known target and entity_id
// exists in businessID — tenant isolation the schema can't enforce here,
// since entity_id has no FK to lean on.
func validateEntityRef(ctx context.Context, q *sqlcgen.Queries, businessID int64, entityType string, entityID int64) error {
	check, ok := entityRefChecks[entityType]
	if !ok {
		return status.Errorf(codes.InvalidArgument, "unknown entity_type %q", entityType)
	}
	return check(ctx, q, businessID, entityID)
}
