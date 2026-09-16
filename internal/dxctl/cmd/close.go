// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package cmd

import (
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/dxctl/resource"
)

var periodCloseNoun = resource.Noun{
	Singular: "close",
	Plural:   "period closes",
	Columns: []resource.Column{
		resource.Int("ID", (*denarixv1.PeriodClose).GetId),
		resource.Date("PERIOD_START", (*denarixv1.PeriodClose).GetPeriodStart),
		resource.Date("PERIOD_END", (*denarixv1.PeriodClose).GetPeriodEnd),
		resource.Bool("REVERSED", func(pc *denarixv1.PeriodClose) bool { return pc.GetReversedAt() != nil }),
		resource.Int("ENTRIES", func(pc *denarixv1.PeriodClose) int { return len(pc.GetGeneratedLedgerTransactionIds()) }),
	},
}

// newCloseCmd is the `close` parent — trigger/reverse/list are period-close
// verbs rather than CRUD, but they still reduce to the generic verb shapes.
func newCloseCmd() *cobra.Command {
	root := newGroupCmd(periodCloseNoun, "Trigger, reverse, or list period closes")
	root.AddCommand(
		newCloseTriggerCmd(),
		newMutateCmd(periodCloseNoun, "reverse", resource.Doc{
			Summary: "Reverse the latest period close",
			Detail: "Closes stack: each close locks the books through its period end, and there is " +
				"no cascade. Reverse the newest close first - a close with a later close still in " +
				"place is refused. Reversing a close posts mirrored transactions for every entry it " +
				"generated, including its Income Summary sweep into Retained Earnings, dated at " +
				"that close's period end. Re-closing afterwards regenerates fresh closing entries. " +
				"To fix a transaction several closes deep, either reverse the closes newest-first " +
				"down to that period and re-close afterwards, or post a correcting transaction in " +
				"the open period instead.",
			Examples: []resource.Example{{Cmd: "dxctl close reverse 5"}},
		}, func(r run, id int64) (proto.Message, error) {
			resp, err := denarixv1.NewPeriodCloseServiceClient(r.conn).ReverseClose(r.ctx, &denarixv1.ReverseCloseRequest{Id: id})
			return resp.GetPeriodClose(), err
		}),
		newTableCmd(periodCloseNoun, "list", resource.Doc{Summary: "List a business's close history"}, periodCloseNoun.Columns, func(r run) ([]proto.Message, error) {
			resp, err := denarixv1.NewPeriodCloseServiceClient(r.conn).ListPeriodCloses(r.ctx, &denarixv1.ListPeriodClosesRequest{BusinessId: r.businessID})
			return toMessages(resp.GetPeriodCloses()), err
		}),
	)
	return root
}

func newCloseTriggerCmd() *cobra.Command {
	var periodEnd string
	cmd := newNoArgCmd(periodCloseNoun, "trigger", resource.Doc{
		Summary:  "Close the books through a date",
		Examples: []resource.Example{{Cmd: "dxctl close trigger --through 2026-01-31"}},
	}, func(r run) (proto.Message, error) {
		d, err := parseDateFlag("through", periodEnd)
		if err != nil {
			return nil, err
		}
		resp, err := denarixv1.NewPeriodCloseServiceClient(r.conn).TriggerClose(r.ctx, &denarixv1.TriggerCloseRequest{BusinessId: r.businessID, PeriodEnd: d})
		return resp.GetPeriodClose(), err
	})
	cmd.Flags().StringVar(&periodEnd, "through", "", "close through this date, YYYY-MM-DD (required)")
	_ = cmd.MarkFlagRequired("through")
	return cmd
}
