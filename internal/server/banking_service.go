// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/auth"
	"github.com/silverbp/denarix/internal/datepb"
	"github.com/silverbp/denarix/internal/db"
	"github.com/silverbp/denarix/internal/db/sqlcgen"
	"github.com/silverbp/denarix/internal/ledgermath"
	"github.com/silverbp/denarix/internal/moneypb"
)

type bankStatementService struct {
	denarixv1.UnimplementedBankStatementServiceServer
	store *db.Store
}

func newBankStatementService(store *db.Store) *bankStatementService {
	return &bankStatementService{store: store}
}

func (s *bankStatementService) GetBankStatement(ctx context.Context, req *denarixv1.GetBankStatementRequest) (*denarixv1.GetBankStatementResponse, error) {
	bs, err := bankStatementRes.load(ctx, s.store.Queries, req.GetId(), "VIEWER")
	if err != nil {
		return nil, err
	}
	pb, err := bankStatementToProto(ctx, s.store.Queries, bs)
	if err != nil {
		return nil, err
	}
	return &denarixv1.GetBankStatementResponse{BankStatement: pb}, nil
}

func (s *bankStatementService) ListBankStatements(ctx context.Context, req *denarixv1.ListBankStatementsRequest) (*denarixv1.ListBankStatementsResponse, error) {
	if err := auth.RequireBusinessRole(ctx, s.store.Queries, req.GetBusinessId(), "VIEWER"); err != nil {
		return nil, err
	}
	rows, err := s.store.Queries.ListBankStatements(ctx, req.GetBusinessId())
	if err != nil {
		return nil, translatePgError(err)
	}
	pbs, err := bankStatementsToProto(ctx, s.store.Queries, rows)
	if err != nil {
		return nil, err
	}
	return &denarixv1.ListBankStatementsResponse{BankStatements: pbs}, nil
}

func (s *bankStatementService) CreateBankStatement(ctx context.Context, req *denarixv1.CreateBankStatementRequest) (*denarixv1.CreateBankStatementResponse, error) {
	u, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no authenticated user")
	}
	if err := auth.RequireBusinessRole(ctx, s.store.Queries, req.GetBusinessId(), "MEMBER"); err != nil {
		return nil, err
	}
	if req.GetStatementName() == "" || req.GetStatementDate() == nil {
		return nil, status.Error(codes.InvalidArgument, "statement_name and statement_date are required")
	}

	account, err := ledgerAccountRes.requireInBusiness(ctx, s.store.Queries, req.GetBusinessId(), int64(req.GetLedgerAccountId()))
	if err != nil {
		return nil, err
	}
	if !account.IsReconcilable {
		return nil, status.Errorf(codes.InvalidArgument, "ledger account %d is not marked is_reconcilable", account.ID)
	}

	opening, err := moneypb.ToNumeric(req.OpeningBalance)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid opening_balance: %v", err)
	}
	closing, err := moneypb.ToNumeric(req.ClosingBalance)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid closing_balance: %v", err)
	}

	statementDate := datepb.ToPgDate(req.GetStatementDate())
	if err := s.checkOpeningBalanceChaining(ctx, req.GetLedgerAccountId(), 0, statementDate, opening, req.GetAllowOpeningMismatch()); err != nil {
		return nil, err
	}

	created, err := s.store.Queries.CreateBankStatement(ctx, sqlcgen.CreateBankStatementParams{
		BusinessID:      req.GetBusinessId(),
		LedgerAccountID: req.GetLedgerAccountId(),
		StatementName:   req.GetStatementName(),
		StatementDate:   statementDate,
		OpeningBalance:  opening,
		ClosingBalance:  closing,
		CreatedByUserID: &u.ID,
	})
	if err != nil {
		return nil, translatePgError(err)
	}
	pb, err := bankStatementToProto(ctx, s.store.Queries, created)
	if err != nil {
		return nil, err
	}
	return &denarixv1.CreateBankStatementResponse{BankStatement: pb}, nil
}

// checkOpeningBalanceChaining rejects an opening balance that doesn't match
// the prior statement's closing balance for the same account, unless
// allowMismatch is set (no prior statement - the first one on an account -
// or a deliberate mid-history import both need the override). Compares
// against the closest statement strictly before statementDate, skipping
// excludeID (the statement being updated, so a date moved later never
// chains against its own closing balance; 0 on create).
func (s *bankStatementService) checkOpeningBalanceChaining(ctx context.Context, ledgerAccountID int32, excludeID int64, statementDate pgtype.Date, opening pgtype.Numeric, allowMismatch bool) error {
	if allowMismatch || !opening.Valid {
		return nil
	}
	prior, err := s.store.Queries.GetLatestBankStatementForAccount(ctx, sqlcgen.GetLatestBankStatementForAccountParams{
		LedgerAccountID: ledgerAccountID,
		BeforeDate:      statementDate,
		ExcludeID:       excludeID,
	})
	if err != nil {
		if isNoRows(err) {
			return nil
		}
		return translatePgError(err)
	}
	if !prior.ClosingBalance.Valid {
		return nil
	}
	openingDec, err := ledgermath.NumericToDecimal(opening)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid opening_balance: %v", err)
	}
	priorClosingDec, err := ledgermath.NumericToDecimal(prior.ClosingBalance)
	if err != nil {
		return status.Errorf(codes.Internal, "reading prior statement's closing_balance: %v", err)
	}
	if !openingDec.Equal(priorClosingDec) {
		return status.Errorf(codes.InvalidArgument,
			"opening_balance %s does not match statement %d's closing_balance %s for this account - pass allow_opening_mismatch to override",
			openingDec, prior.ID, priorClosingDec)
	}
	return nil
}

func (s *bankStatementService) UpdateBankStatement(ctx context.Context, req *denarixv1.UpdateBankStatementRequest) (*denarixv1.UpdateBankStatementResponse, error) {
	existing, err := bankStatementRes.load(ctx, s.store.Queries, req.GetId(), "MEMBER")
	if err != nil {
		return nil, err
	}

	opening, err := moneypb.ToNumeric(req.OpeningBalance)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid opening_balance: %v", err)
	}
	closing, err := moneypb.ToNumeric(req.ClosingBalance)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid closing_balance: %v", err)
	}
	statementDate := datepb.ToPgDate(req.GetStatementDate())

	// Only re-run the chaining check when something it depends on is
	// changing - a rename or closing-balance fix on a statement that was
	// imported with allow_opening_mismatch must not be re-judged on its
	// (still deliberately mismatched) opening balance.
	if opening.Valid || statementDate.Valid {
		effectiveOpening, effectiveDate := opening, statementDate
		if !effectiveOpening.Valid {
			effectiveOpening = existing.OpeningBalance
		}
		if !effectiveDate.Valid {
			effectiveDate = existing.StatementDate
		}
		if err := s.checkOpeningBalanceChaining(ctx, existing.LedgerAccountID, existing.ID, effectiveDate, effectiveOpening, req.GetAllowOpeningMismatch()); err != nil {
			return nil, err
		}
	}

	updated, err := s.store.Queries.UpdateBankStatement(ctx, sqlcgen.UpdateBankStatementParams{
		ID:              req.GetId(),
		StatementName:   req.StatementName,
		StatementDate:   statementDate,
		OpeningBalance:  opening,
		ClosingBalance:  closing,
		ResourceVersion: expectedResourceVersion(req.GetResourceVersion()),
	})
	if err != nil {
		return nil, translateUpdateError(err, bankStatementRes.kind, req.GetId(), req.GetResourceVersion())
	}
	pb, err := bankStatementToProto(ctx, s.store.Queries, updated)
	if err != nil {
		return nil, err
	}
	return &denarixv1.UpdateBankStatementResponse{BankStatement: pb}, nil
}

func (s *bankStatementService) DeactivateBankStatement(ctx context.Context, req *denarixv1.DeactivateBankStatementRequest) (*denarixv1.DeactivateBankStatementResponse, error) {
	existing, err := bankStatementRes.load(ctx, s.store.Queries, req.GetId(), "MEMBER")
	if err != nil {
		return nil, err
	}

	lineCount, err := s.store.Queries.CountBankStatementLines(ctx, existing.ID)
	if err != nil {
		return nil, translatePgError(err)
	}
	if lineCount > 0 {
		return nil, status.Errorf(codes.FailedPrecondition,
			"bank statement %d still has %d reconciled line(s) - unreconcile them first", existing.ID, lineCount)
	}

	deactivated, err := s.store.Queries.DeactivateBankStatement(ctx, sqlcgen.DeactivateBankStatementParams{
		ID:              req.GetId(),
		ResourceVersion: expectedResourceVersion(req.GetResourceVersion()),
	})
	if err != nil {
		return nil, translateUpdateError(err, bankStatementRes.kind, req.GetId(), req.GetResourceVersion())
	}
	pb, err := bankStatementToProto(ctx, s.store.Queries, deactivated)
	if err != nil {
		return nil, err
	}
	return &denarixv1.DeactivateBankStatementResponse{BankStatement: pb}, nil
}

func (s *bankStatementService) ReconcileLedgerTransactions(ctx context.Context, req *denarixv1.ReconcileLedgerTransactionsRequest) (*denarixv1.ReconcileLedgerTransactionsResponse, error) {
	bs, err := bankStatementRes.load(ctx, s.store.Queries, req.GetBankStatementId(), "MEMBER")
	if err != nil {
		return nil, err
	}
	if len(req.GetLedgerTransactionIds()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one ledger_transaction_id is required")
	}

	err = s.store.ExecTx(ctx, func(q *sqlcgen.Queries) error {
		existing, err := q.ListBankStatementLines(ctx, bs.ID)
		if err != nil {
			return err
		}
		nextSeq := int32(len(existing)) + 1

		for _, txnID := range req.GetLedgerTransactionIds() {
			if _, err := ledgerTransactionRes.requireInBusiness(ctx, q, bs.BusinessID, txnID); err != nil {
				return err
			}
			exists, err := q.LedgerEntryExistsForAccount(ctx, sqlcgen.LedgerEntryExistsForAccountParams{
				LedgerTransactionID: txnID,
				AccountID:           bs.LedgerAccountID,
			})
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("ledger transaction %d does not post to account %d", txnID, bs.LedgerAccountID)
			}

			if _, err := q.CreateBankStatementLine(ctx, sqlcgen.CreateBankStatementLineParams{
				BankStatementID:     bs.ID,
				LedgerTransactionID: txnID,
				DisplaySequence:     nextSeq,
			}); err != nil {
				return err
			}
			nextSeq++
		}
		return nil
	})
	if err != nil {
		return nil, txErrorStatus(err)
	}

	pb, err := bankStatementToProto(ctx, s.store.Queries, bs)
	if err != nil {
		return nil, err
	}
	return &denarixv1.ReconcileLedgerTransactionsResponse{BankStatement: pb}, nil
}

func (s *bankStatementService) UnreconcileLedgerTransactions(ctx context.Context, req *denarixv1.UnreconcileLedgerTransactionsRequest) (*denarixv1.UnreconcileLedgerTransactionsResponse, error) {
	bs, err := bankStatementRes.load(ctx, s.store.Queries, req.GetBankStatementId(), "MEMBER")
	if err != nil {
		return nil, err
	}
	if len(req.GetLedgerTransactionIds()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one ledger_transaction_id is required")
	}

	err = s.store.ExecTx(ctx, func(q *sqlcgen.Queries) error {
		for _, txnID := range req.GetLedgerTransactionIds() {
			if err := q.DeleteBankStatementLine(ctx, sqlcgen.DeleteBankStatementLineParams{
				BankStatementID:     bs.ID,
				LedgerTransactionID: txnID,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, txErrorStatus(err)
	}

	pb, err := bankStatementToProto(ctx, s.store.Queries, bs)
	if err != nil {
		return nil, err
	}
	return &denarixv1.UnreconcileLedgerTransactionsResponse{BankStatement: pb}, nil
}

func (s *bankStatementService) ListUnreconciledLedgerTransactions(ctx context.Context, req *denarixv1.ListUnreconciledLedgerTransactionsRequest) (*denarixv1.ListUnreconciledLedgerTransactionsResponse, error) {
	if _, err := ledgerAccountRes.load(ctx, s.store.Queries, int64(req.GetLedgerAccountId()), "VIEWER"); err != nil {
		return nil, err
	}
	throughDate, err := requireDate(req.GetThroughDate(), "through_date")
	if err != nil {
		return nil, err
	}

	rows, err := s.store.Queries.ListUnreconciledLedgerTransactions(ctx, sqlcgen.ListUnreconciledLedgerTransactionsParams{
		AccountID:   req.GetLedgerAccountId(),
		ThroughDate: ledgermath.PgDate(throughDate),
	})
	if err != nil {
		return nil, translatePgError(err)
	}
	pbs, err := ledgerTransactionsToProto(ctx, s.store.Queries, rows)
	if err != nil {
		return nil, err
	}
	return &denarixv1.ListUnreconciledLedgerTransactionsResponse{Transactions: pbs}, nil
}

func bankStatementToProto(ctx context.Context, q *sqlcgen.Queries, bs sqlcgen.BankStatement) (*denarixv1.BankStatement, error) {
	return one(bankStatementsToProto(ctx, q, []sqlcgen.BankStatement{bs}))
}

// bankStatementsToProto converts a page of statements: their lines in one
// query, and the reconciled activity (with the account's normal balance)
// that reconciled_balance/difference derive from in another.
func bankStatementsToProto(ctx context.Context, q *sqlcgen.Queries, rows []sqlcgen.BankStatement) ([]*denarixv1.BankStatement, error) {
	out := make([]*denarixv1.BankStatement, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	ids := idsOf(rows, func(b sqlcgen.BankStatement) int64 { return b.ID })
	lines, err := q.ListBankStatementLinesByStatementIDs(ctx, ids)
	if err != nil {
		return nil, translatePgError(err)
	}
	activities, err := q.SumReconciledActivityByStatementIDs(ctx, ids)
	if err != nil {
		return nil, translatePgError(err)
	}
	linesOf := groupBy(lines, func(l sqlcgen.BankStatementLine) int64 { return l.BankStatementID })
	activityOf := make(map[int64]sqlcgen.SumReconciledActivityByStatementIDsRow, len(activities))
	for _, a := range activities {
		activityOf[a.BankStatementID] = a
	}

	for i, bs := range rows {
		pb := &denarixv1.BankStatement{
			Id:              bs.ID,
			BusinessId:      bs.BusinessID,
			LedgerAccountId: bs.LedgerAccountID,
			StatementName:   bs.StatementName,
			StatementDate:   datepb.ToProto(bs.StatementDate),
			OpeningBalance:  moneypb.ToProto(bs.OpeningBalance),
			ClosingBalance:  moneypb.ToProto(bs.ClosingBalance),
			CreatedByUserId: bs.CreatedByUserID,
			CreatedAt:       timestampProto(bs.CreatedAt),
			ResourceVersion: bs.ResourceVersion,
		}
		for _, l := range linesOf[bs.ID] {
			pb.Lines = append(pb.Lines, &denarixv1.BankStatementLine{
				Id:                  l.ID,
				BankStatementId:     l.BankStatementID,
				LedgerTransactionId: l.LedgerTransactionID,
				DisplaySequence:     l.DisplaySequence,
			})
		}

		activity := activityOf[bs.ID]
		debit, err := ledgermath.NumericToDecimal(activity.TotalDebit)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "bank statement %d: %v", bs.ID, err)
		}
		credit, err := ledgermath.NumericToDecimal(activity.TotalCredit)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "bank statement %d: %v", bs.ID, err)
		}
		openingDec, err := ledgermath.NumericToDecimal(bs.OpeningBalance)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "bank statement %d: %v", bs.ID, err)
		}
		reconciled := openingDec.Add(ledgermath.NetBalance(activity.NormalBalance, debit, credit))
		pb.ReconciledBalance = moneypb.FromDecimal(reconciled)
		if bs.ClosingBalance.Valid {
			closingDec, err := ledgermath.NumericToDecimal(bs.ClosingBalance)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "bank statement %d: %v", bs.ID, err)
			}
			pb.Difference = moneypb.FromDecimal(closingDec.Sub(reconciled))
		}
		out[i] = pb
	}
	return out, nil
}
