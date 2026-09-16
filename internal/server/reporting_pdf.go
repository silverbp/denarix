// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/pdf"
	"github.com/silverbp/denarix/internal/reporting"
)

// The PDF variants of the five reports: same prologue as the structured
// handlers (reporting_service.go) plus the business name for the page
// header, then the matching internal/pdf renderer.

// pdfScope authorizes a business-wide report and returns the business name
// for the PDF header.
func (s *reportingService) pdfScope(ctx context.Context, businessID int64) (string, error) {
	if err := s.scope(ctx, businessID); err != nil {
		return "", err
	}
	b, err := s.store.Queries.GetBusiness(ctx, businessID)
	if err != nil {
		return "", translatePgError(err)
	}
	return b.Name, nil
}

// renderedPDF wraps a renderer's result into the uniform Internal status.
func renderedPDF(content []byte, err error) ([]byte, error) {
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rendering pdf: %v", err)
	}
	return content, nil
}

const pdfDateLayout = "2006-01-02"

func (s *reportingService) GetTrialBalancePdf(ctx context.Context, req *denarixv1.GetTrialBalancePdfRequest) (*denarixv1.GetTrialBalancePdfResponse, error) {
	businessName, err := s.pdfScope(ctx, req.GetBusinessId())
	if err != nil {
		return nil, err
	}
	asOf, err := requireDate(req.GetAsOf(), "as_of")
	if err != nil {
		return nil, err
	}
	result, err := reporting.TrialBalance(ctx, s.store.Queries, req.GetBusinessId(), asOf)
	if err != nil {
		return nil, translatePgError(err)
	}
	content, err := renderedPDF(pdf.RenderTrialBalance(businessName, asOf.Format(pdfDateLayout), result))
	if err != nil {
		return nil, err
	}
	return &denarixv1.GetTrialBalancePdfResponse{Content: content}, nil
}

func (s *reportingService) GetBalanceSheetPdf(ctx context.Context, req *denarixv1.GetBalanceSheetPdfRequest) (*denarixv1.GetBalanceSheetPdfResponse, error) {
	businessName, err := s.pdfScope(ctx, req.GetBusinessId())
	if err != nil {
		return nil, err
	}
	asOf, err := requireDate(req.GetAsOf(), "as_of")
	if err != nil {
		return nil, err
	}
	result, err := reporting.BalanceSheet(ctx, s.store.Queries, req.GetBusinessId(), asOf)
	if err != nil {
		return nil, translatePgError(err)
	}
	content, err := renderedPDF(pdf.RenderBalanceSheet(businessName, asOf.Format(pdfDateLayout), result))
	if err != nil {
		return nil, err
	}
	return &denarixv1.GetBalanceSheetPdfResponse{Content: content}, nil
}

func (s *reportingService) GetIncomeStatementPdf(ctx context.Context, req *denarixv1.GetIncomeStatementPdfRequest) (*denarixv1.GetIncomeStatementPdfResponse, error) {
	businessName, err := s.pdfScope(ctx, req.GetBusinessId())
	if err != nil {
		return nil, err
	}
	start, end, err := requirePeriod(req.GetPeriodStart(), req.GetPeriodEnd())
	if err != nil {
		return nil, err
	}
	result, err := reporting.IncomeStatement(ctx, s.store.Queries, req.GetBusinessId(), start, end)
	if err != nil {
		return nil, translatePgError(err)
	}
	content, err := renderedPDF(pdf.RenderIncomeStatement(businessName, periodLabel(start, end), result))
	if err != nil {
		return nil, err
	}
	return &denarixv1.GetIncomeStatementPdfResponse{Content: content}, nil
}

func (s *reportingService) GetGeneralLedgerPdf(ctx context.Context, req *denarixv1.GetGeneralLedgerPdfRequest) (*denarixv1.GetGeneralLedgerPdfResponse, error) {
	businessName, err := s.pdfScope(ctx, req.GetBusinessId())
	if err != nil {
		return nil, err
	}
	if _, err := ledgerAccountRes.requireInBusiness(ctx, s.store.Queries, req.GetBusinessId(), int64(req.GetAccountId())); err != nil {
		return nil, err
	}
	start, end, err := requirePeriod(req.GetPeriodStart(), req.GetPeriodEnd())
	if err != nil {
		return nil, err
	}
	result, err := reporting.GeneralLedger(ctx, s.store.Queries, req.GetAccountId(), start, end)
	if err != nil {
		return nil, translatePgError(err)
	}
	content, err := renderedPDF(pdf.RenderGeneralLedger(businessName, periodLabel(start, end), result))
	if err != nil {
		return nil, err
	}
	return &denarixv1.GetGeneralLedgerPdfResponse{Content: content}, nil
}

func (s *reportingService) GetCustomerStatementPdf(ctx context.Context, req *denarixv1.GetCustomerStatementPdfRequest) (*denarixv1.GetCustomerStatementPdfResponse, error) {
	businessID, err := s.contactScope(ctx, req.GetContactId())
	if err != nil {
		return nil, err
	}
	businessName, err := s.pdfScope(ctx, businessID)
	if err != nil {
		return nil, err
	}
	start, end, err := requirePeriod(req.GetPeriodStart(), req.GetPeriodEnd())
	if err != nil {
		return nil, err
	}
	result, err := reporting.CustomerStatement(ctx, s.store.Queries, req.GetContactId(), start, end)
	if err != nil {
		return nil, translateStatementError(err)
	}
	content, err := renderedPDF(pdf.RenderCustomerStatement(businessName, result))
	if err != nil {
		return nil, err
	}
	return &denarixv1.GetCustomerStatementPdfResponse{Content: content}, nil
}

func periodLabel(start, end interface{ Format(string) string }) string {
	return start.Format(pdfDateLayout) + " through " + end.Format(pdfDateLayout)
}
