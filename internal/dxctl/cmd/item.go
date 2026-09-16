// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package cmd

import (
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/dxctl/resource"
)

// itemTypeFlagHelp spells out item.item_type for --type; the server validates
// the value, this is only for --help.
const itemTypeFlagHelp = "SERVICE (labour/time), NON_INVENTORY (product, stock not tracked) or INVENTORY (product, stock tracked)"

var itemNoun = resource.Noun{
	Singular: "item",
	Plural:   "items",
	Aliases:  []string{"items"},
	Columns: []resource.Column{
		resource.Int("ID", (*denarixv1.Item).GetId),
		resource.Str("CODE", (*denarixv1.Item).GetItemCode),
		resource.Str("TYPE", (*denarixv1.Item).GetItemType),
		resource.Str("NAME", (*denarixv1.Item).GetName),
		resource.Money("PRICE", (*denarixv1.Item).GetRetailPrice),
		resource.Bool("ACTIVE", (*denarixv1.Item).GetIsActive),
		resource.Int("VERSION", (*denarixv1.Item).GetResourceVersion),
	},
}

func newItemCmd() *cobra.Command {
	root := newGroupCmd(itemNoun, "Manage catalog items (services, products, tracked inventory)")

	var includeInactive bool
	listCmd := newListCmd(itemNoun, func(r run) ([]proto.Message, error) {
		resp, err := denarixv1.NewItemServiceClient(r.conn).ListItems(r.ctx, &denarixv1.ListItemsRequest{BusinessId: r.businessID, IncludeInactive: includeInactive})
		return toMessages(resp.GetItems()), err
	})
	listCmd.Flags().BoolVar(&includeInactive, "inactive", false, "also include inactive items")

	root.AddCommand(
		listCmd,
		newGetCmd(itemNoun, func(r run, id int64) (proto.Message, error) {
			resp, err := denarixv1.NewItemServiceClient(r.conn).GetItem(r.ctx, &denarixv1.GetItemRequest{Id: id})
			return resp.GetItem(), err
		}),
		newItemCreateCmd(),
		newItemUpdateCmd(),
		newVersionedMutateCmd(itemNoun, "deactivate", resource.Doc{Summary: "Deactivate an item"}, func(r run, id, resourceVersion int64) (proto.Message, error) {
			resp, err := denarixv1.NewItemServiceClient(r.conn).DeactivateItem(r.ctx, &denarixv1.DeactivateItemRequest{Id: id, ResourceVersion: resourceVersion})
			return resp.GetItem(), err
		}),
	)
	return root
}

func newItemCreateCmd() *cobra.Command {
	var code, itemType, name, description, unit, price, cost string
	var taxable bool
	var defaultTaxRateID int64
	var defaultLedgerAccountID int32

	cmd := newCreateCmd(itemNoun, resource.Doc{
		Summary: "Create a catalog item",
		Detail: "--type picks how the business treats the item: " + itemTypeFlagHelp + ". " +
			"--default-ledger-account-id is required: every invoice line references an item and posts to " +
			"that item's account (it can't be set per line), so an item without one could never be invoiced.",
		Examples: []resource.Example{
			{Cmd: "dxctl item create --code CONSULT --name Consulting --price 150.00 --default-ledger-account-id 40"},
			{Cmd: "dxctl item create --code WIDGET --type INVENTORY --name Widget --price 25.00 --cost 10.00 --default-ledger-account-id 40"},
		},
	}, func(r run) (proto.Message, error) {
		resp, err := denarixv1.NewItemServiceClient(r.conn).CreateItem(r.ctx, &denarixv1.CreateItemRequest{
			BusinessId:             r.businessID,
			ItemCode:               code,
			Name:                   name,
			IsTaxable:              taxable,
			RetailPrice:            &denarixv1.Decimal{Value: price},
			ItemType:               r.optString("type", &itemType),
			Description:            r.optString("description", &description),
			UnitOfMeasure:          r.optString("unit", &unit),
			CostPrice:              r.optDecimal("cost", &cost),
			DefaultTaxRateId:       r.optInt64("default-tax-rate-id", &defaultTaxRateID),
			DefaultLedgerAccountId: r.optInt32("default-ledger-account-id", &defaultLedgerAccountID),
		})
		return resp.GetItem(), err
	})
	cmd.Flags().StringVar(&code, "code", "", "item code (required)")
	cmd.Flags().StringVar(&itemType, "type", "", itemTypeFlagHelp+" (default SERVICE)")
	cmd.Flags().StringVar(&name, "name", "", "item name (required)")
	cmd.Flags().StringVar(&description, "description", "", "item description")
	cmd.Flags().StringVar(&unit, "unit", "", "unit of measure, e.g. HOUR, EACH (default EACH)")
	cmd.Flags().StringVar(&price, "price", "", "retail price (required)")
	cmd.Flags().StringVar(&cost, "cost", "", "cost price")
	cmd.Flags().BoolVar(&taxable, "taxable", false, "taxable by default")
	cmd.Flags().Int64Var(&defaultTaxRateID, "default-tax-rate-id", 0, "default tax_rate id")
	cmd.Flags().Int32Var(&defaultLedgerAccountID, "default-ledger-account-id", 0, "ledger_account id every invoice line for this item posts to (required)")
	_ = cmd.MarkFlagRequired("code")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("price")
	_ = cmd.MarkFlagRequired("default-ledger-account-id")
	return cmd
}

func newItemUpdateCmd() *cobra.Command {
	var itemType, name, description, price, cost string
	var taxable bool
	var defaultTaxRateID int64
	var defaultLedgerAccountID int32

	cmd := newVersionedMutateCmd(itemNoun, "update", resource.Doc{
		Summary: "Update a catalog item",
		Detail:  "Only flags you pass are sent - omit a flag to leave that field unchanged.",
		Examples: []resource.Example{
			{Cmd: "dxctl item update 7 --price 175.00"},
			{Cmd: "dxctl item update 7 --type NON_INVENTORY"},
		},
	}, func(r run, id, resourceVersion int64) (proto.Message, error) {
		resp, err := denarixv1.NewItemServiceClient(r.conn).UpdateItem(r.ctx, &denarixv1.UpdateItemRequest{
			Id:                     id,
			ResourceVersion:        resourceVersion,
			ItemType:               r.optString("type", &itemType),
			Name:                   r.optString("name", &name),
			Description:            r.optString("description", &description),
			RetailPrice:            r.optDecimal("price", &price),
			CostPrice:              r.optDecimal("cost", &cost),
			IsTaxable:              r.optBool("taxable", &taxable),
			DefaultTaxRateId:       r.optInt64("default-tax-rate-id", &defaultTaxRateID),
			DefaultLedgerAccountId: r.optInt32("default-ledger-account-id", &defaultLedgerAccountID),
		})
		return resp.GetItem(), err
	})
	cmd.Flags().StringVar(&itemType, "type", "", "new item type: "+itemTypeFlagHelp)
	cmd.Flags().StringVar(&name, "name", "", "new item name")
	cmd.Flags().StringVar(&description, "description", "", "new item description")
	cmd.Flags().StringVar(&price, "price", "", "new retail price")
	cmd.Flags().StringVar(&cost, "cost", "", "new cost price")
	cmd.Flags().BoolVar(&taxable, "taxable", false, "taxable by default")
	cmd.Flags().Int64Var(&defaultTaxRateID, "default-tax-rate-id", 0, "new default tax_rate id")
	cmd.Flags().Int32Var(&defaultLedgerAccountID, "default-ledger-account-id", 0, "new default ledger_account id this item's lines normally post to")
	return cmd
}
