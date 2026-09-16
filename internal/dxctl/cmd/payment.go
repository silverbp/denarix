// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/dxctl/resource"
)

var paymentNoun = resource.Noun{
	Singular: "payment",
	Plural:   "payments",
	Aliases:  []string{"payments", "pay"},
	Columns: []resource.Column{
		resource.Int("ID", (*denarixv1.Payment).GetId),
		resource.Str("NUMBER", (*denarixv1.Payment).GetPaymentNumber),
		resource.Str("TYPE", (*denarixv1.Payment).GetPaymentType),
		resource.Money("AMOUNT", (*denarixv1.Payment).GetAmount),
		resource.Str("METHOD", (*denarixv1.Payment).GetPaymentMethod),
		resource.Bool("POSTED", func(p *denarixv1.Payment) bool { return p.LedgerTransactionId != nil }),
		resource.Str("INVOICES", func(p *denarixv1.Payment) string {
			ids := make([]string, len(p.GetApplications()))
			for i, a := range p.GetApplications() {
				ids[i] = fmt.Sprintf("%d", a.GetInvoiceId())
			}
			return strings.Join(ids, ",")
		}),
	},
}

func newPaymentCmd() *cobra.Command {
	root := newGroupCmd(paymentNoun, "Record and manage payments")
	root.AddCommand(
		newListCmd(paymentNoun, func(r run) ([]proto.Message, error) {
			resp, err := denarixv1.NewPaymentServiceClient(r.conn).ListPayments(r.ctx, &denarixv1.ListPaymentsRequest{BusinessId: r.businessID})
			return toMessages(resp.GetPayments()), err
		}),
		newGetCmd(paymentNoun, func(r run, id int64) (proto.Message, error) {
			resp, err := denarixv1.NewPaymentServiceClient(r.conn).GetPayment(r.ctx, &denarixv1.GetPaymentRequest{Id: id})
			return resp.GetPayment(), err
		}),
		newPaymentCreateCmd(),
		newPaymentVoidCmd(),
	)
	return root
}

func newPaymentVoidCmd() *cobra.Command {
	var date string
	cmd := newMutateCmd(paymentNoun, "void", resource.Doc{
		Summary: "Void a payment",
		Detail: "Removes every application (restoring the invoice's paid_amount/balance_due/status), " +
			"reverses the payment's ledger posting if it had one, then removes the payment itself. " +
			"The output shows the payment as it stood immediately before voiding - a second `get`/`void` " +
			"on the same id returns not-found.",
		Examples: []resource.Example{
			{Cmd: "dxctl payment void 42"},
			{Cmd: "dxctl payment void 42 --date 2026-02-01", Desc: "payment is in a closed period - post the reversal in the open one"},
		},
	}, func(r run, id int64) (proto.Message, error) {
		reversalDate, err := r.optDate("date", &date)
		if err != nil {
			return nil, err
		}
		resp, err := denarixv1.NewPaymentServiceClient(r.conn).VoidPayment(r.ctx, &denarixv1.VoidPaymentRequest{Id: id, ReversalDate: reversalDate})
		return resp.GetPayment(), err
	})
	cmd.Flags().StringVar(&date, "date", "", "date to post the reversing ledger transaction on (YYYY-MM-DD); defaults to the payment date - set it when the payment falls in a closed period")
	return cmd
}

func newPaymentCreateCmd() *cobra.Command {
	var contact int64
	var applyRaw []string
	var paymentType, number, date, amount, method string
	var ledgerAccount int32
	var reference, notes string

	cmd := newCreateCmd(paymentNoun, resource.Doc{
		Summary: "Record a payment",
		Detail: "--apply invoice_id:amount (repeatable) applies part or all of the " +
			"payment to one or more invoices at once. Add --account (plus a contact " +
			"with --ledger-account already set) to post the payment to the ledger " +
			"atomically.",
		Examples: []resource.Example{
			{Cmd: "dxctl payment create --contact 5 --type RECEIVED --number PAY-1 " +
				"--date 2026-01-15 --amount 500.00 --method CASH --apply 12:500.00 --account 10"},
			{Desc: "one payment applied across several invoices",
				Cmd: "dxctl payment create --contact 5 --type RECEIVED --number PAY-2 " +
					"--date 2026-01-15 --amount 605.00 --method CASH " +
					"--apply 517:110.00 --apply 518:220.00 --apply 519:275.00"},
		},
	}, func(r run) (proto.Message, error) {
		dateArg, err := parseDateFlag("date", date)
		if err != nil {
			return nil, err
		}
		applications, err := parsePaymentApplyFlags(applyRaw)
		if err != nil {
			return nil, err
		}
		resp, err := denarixv1.NewPaymentServiceClient(r.conn).CreatePayment(r.ctx, &denarixv1.CreatePaymentRequest{
			BusinessId:      r.businessID,
			ContactId:       contact,
			PaymentType:     paymentType,
			PaymentNumber:   number,
			PaymentDate:     dateArg,
			Amount:          &denarixv1.Decimal{Value: amount},
			PaymentMethod:   method,
			Applications:    applications,
			LedgerAccountId: r.optInt32("account", &ledgerAccount),
			ReferenceNumber: r.optString("reference", &reference),
			Notes:           r.optString("notes", &notes),
		})
		return resp.GetPayment(), err
	})
	cmd.Flags().Int64Var(&contact, "contact", 0, "customer or vendor contact id (required)")
	cmd.Flags().StringArrayVar(&applyRaw, "apply", nil, "invoice_id:amount to apply this payment against (repeatable)")
	cmd.Flags().StringVar(&paymentType, "type", "RECEIVED", "RECEIVED or MADE")
	cmd.Flags().StringVar(&number, "number", "", "payment number (required)")
	cmd.Flags().StringVar(&date, "date", "", "payment date, YYYY-MM-DD (required)")
	cmd.Flags().StringVar(&amount, "amount", "", "payment amount (required)")
	cmd.Flags().StringVar(&method, "method", "", "CASH, CHECK, CREDIT_CARD, ACH, WIRE, or OTHER (required)")
	cmd.Flags().Int32Var(&ledgerAccount, "account", 0, "cash/bank ledger account id, to post this payment")
	cmd.Flags().StringVar(&reference, "reference", "", "reference number")
	cmd.Flags().StringVar(&notes, "notes", "", "notes")
	_ = cmd.MarkFlagRequired("contact")
	_ = cmd.MarkFlagRequired("number")
	_ = cmd.MarkFlagRequired("date")
	_ = cmd.MarkFlagRequired("amount")
	_ = cmd.MarkFlagRequired("method")
	return cmd
}
