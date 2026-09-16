// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package cmd

import (
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/dxctl/resource"
)

var estimateNoun = resource.Noun{
	Singular: "estimate",
	Plural:   "estimates",
	Aliases:  []string{"estimates", "est"},
	Columns: []resource.Column{
		resource.Int("ID", (*denarixv1.Estimate).GetId),
		resource.Str("NUMBER", (*denarixv1.Estimate).GetEstimateNumber),
		resource.Str("STATUS", (*denarixv1.Estimate).GetStatus),
		resource.Money("TOTAL", (*denarixv1.Estimate).GetTotalAmount),
		resource.Int("VERSION", (*denarixv1.Estimate).GetResourceVersion),
	},
}

func newEstimateCmd() *cobra.Command {
	root := newGroupCmd(estimateNoun, "Manage estimates")

	var includeAll bool
	listCmd := newListCmd(estimateNoun, func(r run) ([]proto.Message, error) {
		resp, err := denarixv1.NewEstimateServiceClient(r.conn).ListEstimates(r.ctx, &denarixv1.ListEstimatesRequest{BusinessId: r.businessID, IncludeAll: includeAll})
		return toMessages(resp.GetEstimates()), err
	})
	listCmd.Flags().BoolVar(&includeAll, "all", false, "also include accepted, declined, and expired estimates")

	root.AddCommand(
		listCmd,
		newGetCmd(estimateNoun, func(r run, id int64) (proto.Message, error) {
			resp, err := denarixv1.NewEstimateServiceClient(r.conn).GetEstimate(r.ctx, &denarixv1.GetEstimateRequest{Id: id})
			return resp.GetEstimate(), err
		}, func(r run, id int64) ([]byte, error) {
			resp, err := denarixv1.NewEstimateServiceClient(r.conn).GetEstimatePdf(r.ctx, &denarixv1.GetEstimatePdfRequest{Id: id})
			return resp.GetContent(), err
		}),
		newEstimateCreateCmd(),
		newEstimateUpdateCmd(),
		newEstimateUpdateLinesCmd(),
		newEstimateStatusCmd("send", "Mark an estimate SENT", "SENT"),
		newEstimateStatusCmd("accept", "Mark an estimate ACCEPTED", "ACCEPTED"),
		newEstimateStatusCmd("decline", "Mark an estimate DECLINED", "DECLINED"),
		newEstimateStatusCmd("expire", "Mark an estimate EXPIRED", "EXPIRED"),
	)
	return root
}

func newEstimateStatusCmd(verb, summary, status string) *cobra.Command {
	return newVersionedMutateCmd(estimateNoun, verb, resource.Doc{Summary: summary}, func(r run, id, resourceVersion int64) (proto.Message, error) {
		resp, err := denarixv1.NewEstimateServiceClient(r.conn).UpdateEstimateStatus(r.ctx, &denarixv1.UpdateEstimateStatusRequest{Id: id, Status: status, ResourceVersion: resourceVersion})
		return resp.GetEstimate(), err
	})
}

func newEstimateCreateCmd() *cobra.Command {
	var customer int64
	var date, expires string
	var notes, terms string
	var rawLines []string

	cmd := newCreateCmd(estimateNoun, resource.Doc{
		Summary: "Create an estimate",
		Detail: "Repeat --line once per line item. Every line must reference a catalog item (item=<id>) - " +
			"there are no free-text lines. desc/price/taxable/tax-rate default from the item's catalog entry " +
			"when omitted and may be overridden per line.",
		Examples: []resource.Example{
			{Cmd: "dxctl estimate create --customer 5 --date 2026-01-01 --expires 2026-02-01 " + `--line "item=71,qty=10"`},
			{Cmd: "dxctl estimate create --customer 5 --date 2026-01-01 --expires 2026-02-01 " + `--line "item=71,desc=Consulting (March),qty=10,price=150.00,taxable,tax-rate=1"`},
		},
	}, func(r run) (proto.Message, error) {
		lineItems, err := newDocumentLineItems(rawLines)
		if err != nil {
			return nil, err
		}
		dateArg, err := parseDateFlag("date", date)
		if err != nil {
			return nil, err
		}
		expiresArg, err := parseDateFlag("expires", expires)
		if err != nil {
			return nil, err
		}
		resp, err := denarixv1.NewEstimateServiceClient(r.conn).CreateEstimate(r.ctx, &denarixv1.CreateEstimateRequest{
			BusinessId:     r.businessID,
			CustomerId:     customer,
			EstimateDate:   dateArg,
			ExpirationDate: expiresArg,
			LineItems:      lineItems,
			Notes:          r.optString("notes", &notes),
			Terms:          r.optString("terms", &terms),
		})
		return resp.GetEstimate(), err
	})
	cmd.Flags().Int64Var(&customer, "customer", 0, "customer contact id (required)")
	cmd.Flags().StringVar(&date, "date", "", "estimate date, YYYY-MM-DD (required)")
	cmd.Flags().StringVar(&expires, "expires", "", "expiration date, YYYY-MM-DD (required)")
	cmd.Flags().StringVar(&notes, "notes", "", "notes")
	cmd.Flags().StringVar(&terms, "terms", "", "terms")
	cmd.Flags().StringArrayVar(&rawLines, "line", nil, lineFlagHelp)
	_ = cmd.MarkFlagRequired("customer")
	_ = cmd.MarkFlagRequired("date")
	_ = cmd.MarkFlagRequired("expires")
	_ = cmd.MarkFlagRequired("line")
	return cmd
}

func newEstimateUpdateCmd() *cobra.Command {
	var notes, terms, expires string

	cmd := newVersionedMutateCmd(estimateNoun, "update", resource.Doc{
		Summary: "Edit an estimate's notes, terms, or expiration date",
		Detail: "Only flags you pass are sent - omit a flag to leave that field unchanged. " +
			"Identifying fields (customer, estimate date, estimate number) aren't editable - " +
			"recreate the estimate for those; lines are replaced with `estimate update-lines`.",
		Examples: []resource.Example{{Cmd: "dxctl estimate update 42 --expires 2026-03-01"}},
	}, func(r run, id, resourceVersion int64) (proto.Message, error) {
		expiresArg, err := r.optDate("expires", &expires)
		if err != nil {
			return nil, err
		}
		resp, err := denarixv1.NewEstimateServiceClient(r.conn).UpdateEstimate(r.ctx, &denarixv1.UpdateEstimateRequest{
			Id:              id,
			ResourceVersion: resourceVersion,
			Notes:           r.optString("notes", &notes),
			Terms:           r.optString("terms", &terms),
			ExpirationDate:  expiresArg,
		})
		return resp.GetEstimate(), err
	})
	cmd.Flags().StringVar(&notes, "notes", "", "new notes")
	cmd.Flags().StringVar(&terms, "terms", "", "new terms")
	cmd.Flags().StringVar(&expires, "expires", "", "new expiration date, YYYY-MM-DD")
	return cmd
}

func newEstimateUpdateLinesCmd() *cobra.Command {
	var rawLines []string

	cmd := newVersionedMutateCmd(estimateNoun, "update-lines", resource.Doc{
		Summary: "Replace an estimate's line items",
		Detail: "Replaces the entire line item set - repeat --line once per line item, including ones you're " +
			"keeping unchanged. Every line must reference a catalog item (item=<id>).",
		Examples: []resource.Example{{Cmd: "dxctl estimate update-lines 42 " +
			`--line "item=71,qty=10,price=150.00,taxable,tax-rate=1"`}},
	}, func(r run, id, resourceVersion int64) (proto.Message, error) {
		lineItems, err := newDocumentLineItems(rawLines)
		if err != nil {
			return nil, err
		}
		resp, err := denarixv1.NewEstimateServiceClient(r.conn).UpdateEstimateLineItems(r.ctx, &denarixv1.UpdateEstimateLineItemsRequest{
			Id:              id,
			LineItems:       lineItems,
			ResourceVersion: resourceVersion,
		})
		return resp.GetEstimate(), err
	})
	cmd.Flags().StringArrayVar(&rawLines, "line", nil, lineFlagHelp)
	_ = cmd.MarkFlagRequired("line")
	return cmd
}
