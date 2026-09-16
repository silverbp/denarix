// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package cmd

import (
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/dxctl/resource"
)

var taxRateNoun = resource.Noun{
	Singular: "tax-rate",
	Plural:   "tax rates",
	Aliases:  []string{"tax-rates"},
	Columns: []resource.Column{
		resource.Int("ID", (*denarixv1.TaxRate).GetId),
		resource.Str("NAME", (*denarixv1.TaxRate).GetName),
		resource.Money("RATE", (*denarixv1.TaxRate).GetRate),
		resource.Int("LIABILITY_ACCOUNT", (*denarixv1.TaxRate).GetTaxLiabilityAccountId),
		resource.Bool("ACTIVE", (*denarixv1.TaxRate).GetIsActive),
		resource.Int("VERSION", (*denarixv1.TaxRate).GetResourceVersion),
	},
}

func newTaxRateCmd() *cobra.Command {
	root := newGroupCmd(taxRateNoun, "Manage named tax rates")
	root.AddCommand(
		newListCmd(taxRateNoun, func(r run) ([]proto.Message, error) {
			resp, err := denarixv1.NewTaxRateServiceClient(r.conn).ListTaxRates(r.ctx, &denarixv1.ListTaxRatesRequest{BusinessId: r.businessID})
			return toMessages(resp.GetTaxRates()), err
		}),
		newGetCmd(taxRateNoun, func(r run, id int64) (proto.Message, error) {
			resp, err := denarixv1.NewTaxRateServiceClient(r.conn).GetTaxRate(r.ctx, &denarixv1.GetTaxRateRequest{Id: id})
			return resp.GetTaxRate(), err
		}),
		newTaxRateCreateCmd(),
		newTaxRateUpdateCmd(),
		newVersionedMutateCmd(taxRateNoun, "deactivate", resource.Doc{Summary: "Deactivate a tax rate"}, func(r run, id, resourceVersion int64) (proto.Message, error) {
			resp, err := denarixv1.NewTaxRateServiceClient(r.conn).DeactivateTaxRate(r.ctx, &denarixv1.DeactivateTaxRateRequest{Id: id, ResourceVersion: resourceVersion})
			return resp.GetTaxRate(), err
		}),
	)
	return root
}

func newTaxRateCreateCmd() *cobra.Command {
	var name, rate string
	var liabilityAccount int32

	cmd := newCreateCmd(taxRateNoun, resource.Doc{
		Summary:  "Create a named tax rate",
		Examples: []resource.Example{{Cmd: `dxctl tax-rate create --name "Sales Tax" --rate 0.0825 --liability-account 30`}},
	}, func(r run) (proto.Message, error) {
		resp, err := denarixv1.NewTaxRateServiceClient(r.conn).CreateTaxRate(r.ctx, &denarixv1.CreateTaxRateRequest{
			BusinessId:            r.businessID,
			Name:                  name,
			Rate:                  &denarixv1.Decimal{Value: rate},
			TaxLiabilityAccountId: liabilityAccount,
		})
		return resp.GetTaxRate(), err
	})
	cmd.Flags().StringVar(&name, "name", "", "tax rate name, e.g. \"Sales Tax\" (required)")
	cmd.Flags().StringVar(&rate, "rate", "", "rate as a decimal fraction, e.g. 0.0825 for 8.25% (required)")
	cmd.Flags().Int32Var(&liabilityAccount, "liability-account", 0, "TAX_LIABILITY ledger account id this rate is collected into (required)")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("rate")
	_ = cmd.MarkFlagRequired("liability-account")
	return cmd
}

func newTaxRateUpdateCmd() *cobra.Command {
	var name, rate string

	cmd := newVersionedMutateCmd(taxRateNoun, "update", resource.Doc{
		Summary:  "Update a tax rate's name or rate",
		Detail:   "Only flags you pass are sent - omit a flag to leave that field unchanged.",
		Examples: []resource.Example{{Cmd: "dxctl tax-rate update 3 --rate 0.09"}},
	}, func(r run, id, resourceVersion int64) (proto.Message, error) {
		resp, err := denarixv1.NewTaxRateServiceClient(r.conn).UpdateTaxRate(r.ctx, &denarixv1.UpdateTaxRateRequest{
			Id:              id,
			ResourceVersion: resourceVersion,
			Name:            r.optString("name", &name),
			Rate:            r.optDecimal("rate", &rate),
		})
		return resp.GetTaxRate(), err
	})
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&rate, "rate", "", "new rate as a decimal fraction")
	return cmd
}
