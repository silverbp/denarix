// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	typepb "google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/protobuf/proto"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/dxctl/resource"
)

var invoiceNoun = resource.Noun{
	Singular: "invoice",
	Plural:   "invoices",
	Aliases:  []string{"invoices", "inv"},
	Columns: []resource.Column{
		resource.Int("ID", (*denarixv1.Invoice).GetId),
		resource.Str("NUMBER", (*denarixv1.Invoice).GetInvoiceNumber),
		resource.Str("TYPE", (*denarixv1.Invoice).GetInvoiceType),
		resource.Str("STATUS", (*denarixv1.Invoice).GetStatus),
		resource.Money("TOTAL", (*denarixv1.Invoice).GetTotalAmount),
		resource.Money("BALANCE_DUE", (*denarixv1.Invoice).GetBalanceDue),
		resource.Bool("POSTED", func(i *denarixv1.Invoice) bool { return i.LedgerTransactionId != nil }),
		resource.Int("VERSION", (*denarixv1.Invoice).GetResourceVersion),
	},
}

func newInvoiceCmd() *cobra.Command {
	root := newGroupCmd(invoiceNoun, "Manage invoices")

	var includeAll bool
	listCmd := newListCmd(invoiceNoun, func(r run) ([]proto.Message, error) {
		resp, err := denarixv1.NewInvoiceServiceClient(r.conn).ListInvoices(r.ctx, &denarixv1.ListInvoicesRequest{BusinessId: r.businessID, IncludeAll: includeAll})
		return toMessages(resp.GetInvoices()), err
	})
	listCmd.Flags().BoolVar(&includeAll, "all", false, "also include paid and cancelled invoices")

	root.AddCommand(
		listCmd,
		newGetCmd(invoiceNoun, func(r run, id int64) (proto.Message, error) {
			resp, err := denarixv1.NewInvoiceServiceClient(r.conn).GetInvoice(r.ctx, &denarixv1.GetInvoiceRequest{Id: id})
			return resp.GetInvoice(), err
		}, func(r run, id int64) ([]byte, error) {
			resp, err := denarixv1.NewInvoiceServiceClient(r.conn).GetInvoicePdf(r.ctx, &denarixv1.GetInvoicePdfRequest{Id: id})
			return resp.GetContent(), err
		}),
		newInvoiceCreateCmd(),
		newInvoiceUpdateCmd(),
		newInvoiceUpdateLinesCmd(),
		newInvoiceStatusCmd("send", resource.Doc{Summary: "Mark an invoice SENT"}, "SENT", nil),
		newInvoiceCancelCmd(),
		newInvoiceStatusCmd("mark-overdue", resource.Doc{Summary: "Mark an invoice OVERDUE"}, "OVERDUE", nil),
	)
	return root
}

// newInvoiceStatusCmd builds a versioned status transition. reversalDate, if
// non-nil, is read at call time (cancel's --date flag).
func newInvoiceStatusCmd(verb string, doc resource.Doc, status string, reversalDate func(r run) (*typepb.Date, error)) *cobra.Command {
	return newVersionedMutateCmd(invoiceNoun, verb, doc, func(r run, id, resourceVersion int64) (proto.Message, error) {
		var date *typepb.Date
		if reversalDate != nil {
			var err error
			if date, err = reversalDate(r); err != nil {
				return nil, err
			}
		}
		resp, err := denarixv1.NewInvoiceServiceClient(r.conn).UpdateInvoiceStatus(r.ctx, &denarixv1.UpdateInvoiceStatusRequest{Id: id, Status: status, ResourceVersion: resourceVersion, ReversalDate: date})
		return resp.GetInvoice(), err
	})
}

// newInvoiceCancelCmd is the versioned status transition plus a --date for
// the cancellation's reversing ledger transaction.
func newInvoiceCancelCmd() *cobra.Command {
	var date string
	cmd := newInvoiceStatusCmd("cancel", resource.Doc{
		Summary: "Cancel an invoice",
		Detail: "Reverses the invoice's ledger posting (a new mirrored transaction; the original is untouched) " +
			"and zeroes its balance due. Rejected while any payment is still applied - `payment void` those first.",
		Examples: []resource.Example{
			{Cmd: "dxctl invoice cancel 42"},
			{Cmd: "dxctl invoice cancel 42 --date 2026-02-01", Desc: "invoice is in a closed period - post the reversal in the open one"},
		},
	}, "CANCELLED", func(r run) (*typepb.Date, error) { return r.optDate("date", &date) })
	cmd.Flags().StringVar(&date, "date", "", "date to post the reversing ledger transaction on (YYYY-MM-DD); defaults to the invoice date - set it when the invoice falls in a closed period")
	return cmd
}

func newInvoiceCreateCmd() *cobra.Command {
	var contact int64
	var invoiceType, invoiceNumber, date, due, notes, terms string
	var estimate int64
	var rawLines []string

	cmd := newCreateCmd(invoiceNoun, resource.Doc{
		Summary: "Create an invoice",
		Detail: "Every line must reference a catalog item (item=<id>) - there are no free-text lines. " +
			"The line posts to that item's default_ledger_account_id; the account can't be set per line. " +
			"desc/price/taxable/tax-rate default from the item's catalog entry and may be overridden per line. " +
			"The contact must have a customer (for SALES) or vendor (for PURCHASE) record with its " +
			"own ledger_account_id set — the invoice posts to the ledger atomically as part of creation. " +
			"Pass --estimate with no --line flags to build the invoice's lines from that estimate's own " +
			"lines instead (item/description/qty/price/taxable/tax_rate carried over as-is, " +
			"ledger account resolved fresh from each line's item).",
		Examples: []resource.Example{
			{Cmd: "dxctl invoice create --contact 5 --type SALES --date 2026-01-01 --due 2026-01-31 " +
				`--line "item=71,qty=10"`},
			{Cmd: "dxctl invoice create --contact 5 --type SALES --date 2026-01-01 --due 2026-01-31 " +
				`--line "item=71,desc=Consulting (March),qty=10,price=150.00,taxable,tax-rate=1"`},
			{Cmd: "dxctl invoice create --contact 5 --type SALES --date 2026-01-01 --due 2026-01-31 --estimate 12"},
		},
	}, func(r run) (proto.Message, error) {
		if len(rawLines) == 0 && estimate == 0 {
			return nil, fmt.Errorf("either --line or --estimate is required")
		}
		var lineItems []*denarixv1.NewDocumentLineItem
		if len(rawLines) > 0 {
			var err error
			if lineItems, err = newDocumentLineItems(rawLines); err != nil {
				return nil, err
			}
		}
		dateArg, err := parseDateFlag("date", date)
		if err != nil {
			return nil, err
		}
		dueArg, err := parseDateFlag("due", due)
		if err != nil {
			return nil, err
		}
		resp, err := denarixv1.NewInvoiceServiceClient(r.conn).CreateInvoice(r.ctx, &denarixv1.CreateInvoiceRequest{
			BusinessId:    r.businessID,
			ContactId:     contact,
			InvoiceType:   invoiceType,
			InvoiceDate:   dateArg,
			DueDate:       dueArg,
			LineItems:     lineItems,
			InvoiceNumber: r.optString("number", &invoiceNumber),
			EstimateId:    r.optInt64("estimate", &estimate),
			Notes:         r.optString("notes", &notes),
			Terms:         r.optString("terms", &terms),
		})
		return resp.GetInvoice(), err
	})
	cmd.Flags().Int64Var(&contact, "contact", 0, "customer or vendor contact id (required)")
	cmd.Flags().StringVar(&invoiceType, "type", "SALES", "SALES or PURCHASE")
	cmd.Flags().StringVar(&invoiceNumber, "number", "", "invoice number (required for PURCHASE; auto-generated for SALES)")
	cmd.Flags().StringVar(&date, "date", "", "invoice date, YYYY-MM-DD (required)")
	cmd.Flags().StringVar(&due, "due", "", "due date, YYYY-MM-DD (required)")
	cmd.Flags().Int64Var(&estimate, "estimate", 0, "estimate id this invoice converts from")
	cmd.Flags().StringVar(&notes, "notes", "", "notes")
	cmd.Flags().StringVar(&terms, "terms", "", "terms")
	cmd.Flags().StringArrayVar(&rawLines, "line", nil, lineFlagHelp+". Omit entirely when --estimate is set to build the lines from that estimate instead.")
	_ = cmd.MarkFlagRequired("contact")
	_ = cmd.MarkFlagRequired("date")
	_ = cmd.MarkFlagRequired("due")
	return cmd
}

func newInvoiceUpdateCmd() *cobra.Command {
	var notes, terms, due string

	cmd := newVersionedMutateCmd(invoiceNoun, "update", resource.Doc{
		Summary: "Edit an invoice's notes, terms, or due date",
		Detail: "Only flags you pass are sent - omit a flag to leave that field unchanged. " +
			"Fields with ledger impact (contact, invoice date, invoice number) aren't editable - " +
			"cancel and recreate the invoice for those.",
		Examples: []resource.Example{{Cmd: "dxctl invoice update 42 --due 2026-03-01"}},
	}, func(r run, id, resourceVersion int64) (proto.Message, error) {
		dueArg, err := r.optDate("due", &due)
		if err != nil {
			return nil, err
		}
		resp, err := denarixv1.NewInvoiceServiceClient(r.conn).UpdateInvoice(r.ctx, &denarixv1.UpdateInvoiceRequest{
			Id:              id,
			ResourceVersion: resourceVersion,
			Notes:           r.optString("notes", &notes),
			Terms:           r.optString("terms", &terms),
			DueDate:         dueArg,
		})
		return resp.GetInvoice(), err
	})
	cmd.Flags().StringVar(&notes, "notes", "", "new notes")
	cmd.Flags().StringVar(&terms, "terms", "", "new terms")
	cmd.Flags().StringVar(&due, "due", "", "new due date, YYYY-MM-DD")
	return cmd
}

func newInvoiceUpdateLinesCmd() *cobra.Command {
	var rawLines []string

	cmd := newVersionedMutateCmd(invoiceNoun, "update-lines", resource.Doc{
		Summary: "Replace an invoice's line items",
		Detail: "Replaces the entire line item set - repeat --line once per line item, including ones you're " +
			"keeping unchanged. Every line must reference a catalog item (item=<id>) and posts to that item's " +
			"default_ledger_account_id. If the invoice is already posted to the ledger, its linked transaction's " +
			"entries are regenerated in place from the new lines rather than rejecting the edit.",
		Examples: []resource.Example{{Cmd: "dxctl invoice update-lines 42 " +
			`--line "item=71,qty=10,price=150.00,taxable,tax-rate=1"`}},
	}, func(r run, id, resourceVersion int64) (proto.Message, error) {
		lineItems, err := newDocumentLineItems(rawLines)
		if err != nil {
			return nil, err
		}
		resp, err := denarixv1.NewInvoiceServiceClient(r.conn).UpdateInvoiceLineItems(r.ctx, &denarixv1.UpdateInvoiceLineItemsRequest{
			Id:              id,
			LineItems:       lineItems,
			ResourceVersion: resourceVersion,
		})
		return resp.GetInvoice(), err
	})
	cmd.Flags().StringArrayVar(&rawLines, "line", nil, lineFlagHelp)
	_ = cmd.MarkFlagRequired("line")
	return cmd
}
