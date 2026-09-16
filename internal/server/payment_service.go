// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

// PaymentService — applies to invoice paid_amount/balance_due regardless of
// posting, and optionally posts to the ledger (see proto comment).

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/auth"
	"github.com/silverbp/denarix/internal/datepb"
	"github.com/silverbp/denarix/internal/db"
	"github.com/silverbp/denarix/internal/db/sqlcgen"
	"github.com/silverbp/denarix/internal/ledgermath"
	"github.com/silverbp/denarix/internal/ledgerpost"
	"github.com/silverbp/denarix/internal/moneypb"
)

type paymentService struct {
	denarixv1.UnimplementedPaymentServiceServer
	store *db.Store
}

func newPaymentService(store *db.Store) *paymentService {
	return &paymentService{store: store}
}

func (s *paymentService) GetPayment(ctx context.Context, req *denarixv1.GetPaymentRequest) (*denarixv1.GetPaymentResponse, error) {
	p, err := paymentRes.load(ctx, s.store.Queries, req.GetId(), "VIEWER")
	if err != nil {
		return nil, err
	}
	pb, err := paymentToProto(ctx, s.store.Queries, p)
	if err != nil {
		return nil, err
	}
	return &denarixv1.GetPaymentResponse{Payment: pb}, nil
}

func (s *paymentService) ListPayments(ctx context.Context, req *denarixv1.ListPaymentsRequest) (*denarixv1.ListPaymentsResponse, error) {
	if err := auth.RequireBusinessRole(ctx, s.store.Queries, req.GetBusinessId(), "VIEWER"); err != nil {
		return nil, err
	}
	rows, err := s.store.Queries.ListPayments(ctx, req.GetBusinessId())
	if err != nil {
		return nil, translatePgError(err)
	}
	pbs, err := paymentsToProto(ctx, s.store.Queries, rows)
	if err != nil {
		return nil, err
	}
	return &denarixv1.ListPaymentsResponse{Payments: pbs}, nil
}

func (s *paymentService) CreatePayment(ctx context.Context, req *denarixv1.CreatePaymentRequest) (*denarixv1.CreatePaymentResponse, error) {
	u, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no authenticated user")
	}
	if err := auth.RequireBusinessRole(ctx, s.store.Queries, req.GetBusinessId(), "MEMBER"); err != nil {
		return nil, err
	}
	if req.GetPaymentType() != "RECEIVED" && req.GetPaymentType() != "MADE" {
		return nil, status.Error(codes.InvalidArgument, "payment_type must be RECEIVED or MADE")
	}
	if req.GetPaymentNumber() == "" || req.GetAmount() == nil || req.GetPaymentDate() == nil || req.GetPaymentMethod() == "" {
		return nil, status.Error(codes.InvalidArgument, "payment_number, amount, payment_date, and payment_method are required")
	}
	if _, err := contactRes.requireInBusiness(ctx, s.store.Queries, req.GetBusinessId(), req.GetContactId()); err != nil {
		return nil, err
	}
	if req.LedgerAccountId != nil {
		if err := requirePostableAccount(ctx, s.store.Queries, req.GetBusinessId(), req.GetLedgerAccountId()); err != nil {
			return nil, err
		}
	}

	amount, err := decimal.NewFromString(req.GetAmount().GetValue())
	if err != nil || !amount.IsPositive() {
		return nil, status.Error(codes.InvalidArgument, "amount must be a positive number")
	}
	amountNum, err := ledgermath.DecimalToNumeric(amount)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid amount: %v", err)
	}

	// Each application's own amount, validated up front so a bad one fails
	// before anything's written - and their sum can't exceed what was
	// actually received (a payment may be partially or fully unapplied,
	// but never over-applied).
	appliedAmounts := make([]decimal.Decimal, len(req.GetApplications()))
	total := decimal.Zero
	perInvoiceTotal := make(map[int64]decimal.Decimal)
	for i, app := range req.GetApplications() {
		if app.GetInvoiceId() == 0 {
			return nil, status.Error(codes.InvalidArgument, "applications[].invoice_id is required")
		}
		applied, err := decimal.NewFromString(app.GetAppliedAmount().GetValue())
		if err != nil || !applied.IsPositive() {
			return nil, status.Errorf(codes.InvalidArgument, "applications[%d].applied_amount must be a positive number", i)
		}
		appliedAmounts[i] = applied
		total = total.Add(applied)
		perInvoiceTotal[app.GetInvoiceId()] = perInvoiceTotal[app.GetInvoiceId()].Add(applied)
	}
	if total.GreaterThan(amount) {
		return nil, status.Error(codes.InvalidArgument, "sum of applications[].applied_amount exceeds amount")
	}

	// Each application must also fit the invoice's own remaining
	// balance_due, not just this payment's amount - two separate payments
	// could otherwise over-apply the same invoice and drive balance_due
	// negative. This is a friendlier pre-check; ApplyPaymentToInvoice's own
	// `balance_due >= amount` guard is what actually enforces it under
	// concurrent requests.
	for invoiceID, requested := range perInvoiceTotal {
		inv, err := invoiceRes.requireInBusiness(ctx, s.store.Queries, req.GetBusinessId(), invoiceID)
		if err != nil {
			return nil, err
		}
		// Only a sent invoice takes a payment: DRAFT so that voiding the
		// payment later can always restore SENT (UnapplyPaymentFromInvoice
		// has no record of the pre-payment status), CANCELLED because there
		// is nothing left to pay. ApplyPaymentToInvoice enforces the same.
		switch inv.Status {
		case "DRAFT":
			return nil, status.Errorf(codes.FailedPrecondition, "invoice %d is still DRAFT - send it before applying a payment", invoiceID)
		case "CANCELLED":
			return nil, status.Errorf(codes.FailedPrecondition, "invoice %d is cancelled", invoiceID)
		}
		balanceDue, err := ledgermath.NumericToDecimal(inv.BalanceDue)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "reading invoice %d balance_due: %v", invoiceID, err)
		}
		if requested.GreaterThan(balanceDue) {
			return nil, status.Errorf(codes.FailedPrecondition,
				"applications against invoice %d total %s, more than its remaining balance_due %s", invoiceID, requested, balanceDue)
		}
	}

	var payment sqlcgen.Payment
	var applications []sqlcgen.PaymentApplication
	err = s.store.ExecTx(ctx, func(q *sqlcgen.Queries) error {
		var err error
		payment, err = q.CreatePayment(ctx, sqlcgen.CreatePaymentParams{
			BusinessID:      req.GetBusinessId(),
			ContactID:       req.GetContactId(),
			PaymentType:     req.GetPaymentType(),
			PaymentNumber:   req.GetPaymentNumber(),
			PaymentDate:     datepb.ToPgDate(req.GetPaymentDate()),
			Amount:          amountNum,
			PaymentMethod:   req.GetPaymentMethod(),
			LedgerAccountID: req.LedgerAccountId,
			ReferenceNumber: req.ReferenceNumber,
			Notes:           req.Notes,
			CreatedByUserID: &u.ID,
		})
		if err != nil {
			return err
		}

		for i, app := range req.GetApplications() {
			appliedNum, err := ledgermath.DecimalToNumeric(appliedAmounts[i])
			if err != nil {
				return err
			}
			application, err := q.CreatePaymentApplication(ctx, sqlcgen.CreatePaymentApplicationParams{
				PaymentID:     payment.ID,
				InvoiceID:     app.GetInvoiceId(),
				AppliedAmount: appliedNum,
			})
			if err != nil {
				return err
			}
			applications = append(applications, application)
			if _, err := q.ApplyPaymentToInvoice(ctx, sqlcgen.ApplyPaymentToInvoiceParams{ID: app.GetInvoiceId(), Amount: appliedNum}); err != nil {
				if isNoRows(err) {
					// The pre-check above passed but the guarded UPDATE matched
					// nothing: the invoice was paid down, cancelled, or deleted
					// concurrently. Same precondition failure as the pre-check
					// reports, so the same code (txErrorStatus passes a
					// status through unchanged).
					return status.Errorf(codes.FailedPrecondition,
						"invoice %d can no longer take this application - its balance_due or status changed concurrently; re-read it and retry", app.GetInvoiceId())
				}
				return err
			}
		}

		txnID, err := maybePostPayment(ctx, q, req.GetBusinessId(), payment, &u.ID)
		if err != nil {
			return err
		}
		if txnID != nil {
			payment, err = q.SetPaymentLedgerTransaction(ctx, sqlcgen.SetPaymentLedgerTransactionParams{ID: payment.ID, LedgerTransactionID: txnID})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, txErrorStatus(err)
	}
	return &denarixv1.CreatePaymentResponse{Payment: paymentWithApplications(payment, applications)}, nil
}

// VoidPayment reverses a payment end to end: every payment_application is
// removed and its invoice's paid_amount/balance_due/status restored
// (UnapplyPaymentFromInvoice), then - if the payment was posted - a
// reversing ledger transaction is posted (ledgerpost.ReverseTransaction, the
// original posting untouched), and finally the payment itself is
// soft-deleted. GetPayment already filters deleted_at IS NULL, so voiding an
// already-void payment surfaces as NotFound here rather than a distinct
// "already void" error.
func (s *paymentService) VoidPayment(ctx context.Context, req *denarixv1.VoidPaymentRequest) (*denarixv1.VoidPaymentResponse, error) {
	u, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no authenticated user")
	}
	payment, err := paymentRes.load(ctx, s.store.Queries, req.GetId(), "MEMBER")
	if err != nil {
		return nil, err
	}
	applications, err := s.store.Queries.ListPaymentApplicationsForPayment(ctx, payment.ID)
	if err != nil {
		return nil, translatePgError(err)
	}

	reversalDate, err := resolveReversalDate(payment.PaymentDate, req.ReversalDate, "payment_date")
	if err != nil {
		return nil, err
	}

	// Snapshot before voiding - what VoidPaymentResponse returns, since the
	// payment row (and its applications) won't be readable through the
	// normal Get/List queries once soft-deleted.
	pb := paymentWithApplications(payment, applications)

	err = s.store.ExecTx(ctx, func(q *sqlcgen.Queries) error {
		for _, app := range applications {
			if _, err := q.UnapplyPaymentFromInvoice(ctx, sqlcgen.UnapplyPaymentFromInvoiceParams{ID: app.InvoiceID, Amount: app.AppliedAmount}); err != nil {
				return err
			}
		}
		if err := q.DeletePaymentApplicationsForPayment(ctx, payment.ID); err != nil {
			return err
		}
		if payment.LedgerTransactionID != nil {
			description := fmt.Sprintf("Void of payment %d (%s)", payment.ID, payment.PaymentNumber)
			if _, err := ledgerpost.ReverseTransaction(ctx, q, payment.BusinessID, *payment.LedgerTransactionID, reversalDate, description, &u.ID); err != nil {
				return err
			}
		}
		_, err := q.SoftDeletePayment(ctx, payment.ID)
		return err
	})
	if err != nil {
		return nil, txErrorStatus(err)
	}
	return &denarixv1.VoidPaymentResponse{Payment: pb}, nil
}

// maybePostPayment posts payment to the ledger iff both ledger_account_id
// (the cash/bank account) and the contact's own ledger_account_id are set;
// returns (nil, nil) — not an error — otherwise, leaving it unposted.
func maybePostPayment(ctx context.Context, q *sqlcgen.Queries, businessID int64, payment sqlcgen.Payment, createdByUserID *int64) (*int64, error) {
	if payment.LedgerAccountID == nil {
		return nil, nil
	}
	contactLedgerAccountID, err := resolveContactLedgerAccountID(ctx, q, payment.ContactID, payment.PaymentType == "RECEIVED")
	if err != nil {
		return nil, err
	}
	if contactLedgerAccountID == nil {
		return nil, fmt.Errorf("cannot post payment: contact %d has no customer/vendor ledger_account_id set", payment.ContactID)
	}

	amount, err := ledgermath.NumericToDecimal(payment.Amount)
	if err != nil {
		return nil, err
	}

	description := fmt.Sprintf("Payment %s", payment.PaymentNumber)
	txn, err := q.CreateLedgerTransaction(ctx, sqlcgen.CreateLedgerTransactionParams{
		BusinessID:      businessID,
		TransactionDate: payment.PaymentDate,
		Description:     &description,
		CreatedByUserID: createdByUserID,
	})
	if err != nil {
		return nil, err
	}

	// RECEIVED: DEBIT cash, CREDIT the contact's AR. MADE: DEBIT the
	// contact's AP, CREDIT cash - the same two legs with the sides swapped.
	cashDebit := payment.PaymentType == "RECEIVED"
	cashDr, cashCr := ledgerpost.DebitCreditFor(amount, cashDebit)
	contactDr, contactCr := ledgerpost.DebitCreditFor(amount, !cashDebit)
	if err := ledgerpost.CreateDecimalEntry(ctx, q, businessID, txn.ID, *payment.LedgerAccountID, cashDr, cashCr); err != nil {
		return nil, err
	}
	if err := ledgerpost.CreateDecimalEntry(ctx, q, businessID, txn.ID, *contactLedgerAccountID, contactDr, contactCr); err != nil {
		return nil, err
	}
	if err := ledgerpost.VerifyBalanced(ctx, q, txn.ID); err != nil {
		return nil, err
	}
	return &txn.ID, nil
}

// paymentToProto converts one payment, loading its applications.
func paymentToProto(ctx context.Context, q *sqlcgen.Queries, p sqlcgen.Payment) (*denarixv1.Payment, error) {
	return one(paymentsToProto(ctx, q, []sqlcgen.Payment{p}))
}

// paymentsToProto converts a page of payments with their applications
// loaded in one query.
func paymentsToProto(ctx context.Context, q *sqlcgen.Queries, rows []sqlcgen.Payment) ([]*denarixv1.Payment, error) {
	return withChildren(ctx, q, rows,
		func(p sqlcgen.Payment) int64 { return p.ID },
		(*sqlcgen.Queries).ListPaymentApplicationsByPaymentIDs,
		func(a sqlcgen.PaymentApplication) int64 { return a.PaymentID },
		paymentWithApplications)
}

// paymentWithApplications converts a payment whose applications the caller
// already holds.
func paymentWithApplications(p sqlcgen.Payment, applications []sqlcgen.PaymentApplication) *denarixv1.Payment {
	pbApplications := make([]*denarixv1.PaymentApplication, len(applications))
	for i, a := range applications {
		pbApplications[i] = &denarixv1.PaymentApplication{
			Id:            a.ID,
			PaymentId:     a.PaymentID,
			InvoiceId:     a.InvoiceID,
			AppliedAmount: moneypb.ToProto(a.AppliedAmount),
			CreatedAt:     timestampProto(a.CreatedAt),
		}
	}
	return &denarixv1.Payment{
		Id:                  p.ID,
		BusinessId:          p.BusinessID,
		ContactId:           p.ContactID,
		PaymentType:         p.PaymentType,
		PaymentNumber:       p.PaymentNumber,
		PaymentDate:         datepb.ToProto(p.PaymentDate),
		Amount:              moneypb.ToProto(p.Amount),
		PaymentMethod:       p.PaymentMethod,
		LedgerAccountId:     p.LedgerAccountID,
		ReferenceNumber:     p.ReferenceNumber,
		Notes:               p.Notes,
		LedgerTransactionId: p.LedgerTransactionID,
		CreatedByUserId:     p.CreatedByUserID,
		CreatedAt:           timestampProto(p.CreatedAt),
		Applications:        pbApplications,
	}
}
