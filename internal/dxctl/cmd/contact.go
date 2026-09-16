// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package cmd

import (
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/dxctl/resource"
)

var contactNoun = resource.Noun{
	Singular: "contact",
	Plural:   "contacts",
	Aliases:  []string{"contacts"},
	Columns: []resource.Column{
		resource.Int("ID", (*denarixv1.Contact).GetId),
		resource.Str("NUMBER", (*denarixv1.Contact).GetContactNumber),
		resource.Str("NAME", (*denarixv1.Contact).GetName),
		resource.Bool("CUSTOMER", func(c *denarixv1.Contact) bool { return c.GetCustomer() != nil }),
		resource.Bool("VENDOR", func(c *denarixv1.Contact) bool { return c.GetVendor() != nil }),
		resource.Bool("ACTIVE", (*denarixv1.Contact).GetIsActive),
		resource.Int("VERSION", (*denarixv1.Contact).GetResourceVersion),
	},
}

func newContactCmd() *cobra.Command {
	root := newGroupCmd(contactNoun, "Manage customer/vendor contacts")

	var includeInactive bool
	listCmd := newListCmd(contactNoun, func(r run) ([]proto.Message, error) {
		resp, err := denarixv1.NewContactServiceClient(r.conn).ListContacts(r.ctx, &denarixv1.ListContactsRequest{BusinessId: r.businessID, IncludeInactive: includeInactive})
		return toMessages(resp.GetContacts()), err
	})
	listCmd.Flags().BoolVar(&includeInactive, "inactive", false, "also include inactive contacts")

	root.AddCommand(
		listCmd,
		newGetCmd(contactNoun, func(r run, id int64) (proto.Message, error) {
			resp, err := denarixv1.NewContactServiceClient(r.conn).GetContact(r.ctx, &denarixv1.GetContactRequest{Id: id})
			return resp.GetContact(), err
		}),
		newContactCreateCmd(),
		newContactUpdateCmd(),
		newVersionedMutateCmd(contactNoun, "deactivate", resource.Doc{Summary: "Deactivate a contact"}, func(r run, id, resourceVersion int64) (proto.Message, error) {
			resp, err := denarixv1.NewContactServiceClient(r.conn).DeactivateContact(r.ctx, &denarixv1.DeactivateContactRequest{Id: id, ResourceVersion: resourceVersion})
			return resp.GetContact(), err
		}),
	)
	return root
}

func newContactCreateCmd() *cobra.Command {
	var contactNumber, name, email, phone string
	var paymentTerms int32
	var isCustomer, isVendor bool
	var creditLimit string
	var addr1, addr2, city, state, postal, country string

	cmd := newCreateCmd(contactNoun, resource.Doc{
		Summary: "Create a customer/vendor contact",
		Detail: "Marking a contact --customer and/or --vendor auto-creates that role's own AR/AP " +
			"ledger sub-account (named after the contact, under the business's Accounts " +
			"Receivable/Payable container) - there's no way to point it at an existing account instead.",
		Examples: []resource.Example{{Cmd: "dxctl contact create --contact-number C-1 --name \"Acme Co\""}},
	}, func(r run) (proto.Message, error) {
		resp, err := denarixv1.NewContactServiceClient(r.conn).CreateContact(r.ctx, &denarixv1.CreateContactRequest{
			BusinessId:          r.businessID,
			ContactNumber:       contactNumber,
			Name:                name,
			IsCustomer:          isCustomer,
			IsVendor:            isVendor,
			Email:               r.optString("email", &email),
			Phone:               r.optString("phone", &phone),
			PaymentTermsDays:    r.optInt32("payment-terms", &paymentTerms),
			CreditLimit:         r.optDecimal("credit-limit", &creditLimit),
			BillingAddressLine1: r.optString("address1", &addr1),
			BillingAddressLine2: r.optString("address2", &addr2),
			BillingCity:         r.optString("city", &city),
			BillingState:        r.optString("state", &state),
			BillingPostalCode:   r.optString("postal-code", &postal),
			BillingCountry:      r.optString("country", &country),
		})
		return resp.GetContact(), err
	})
	cmd.Flags().StringVar(&contactNumber, "contact-number", "", "unique contact number (required)")
	cmd.Flags().StringVar(&name, "name", "", "contact name (required)")
	cmd.Flags().StringVar(&email, "email", "", "email address")
	cmd.Flags().StringVar(&phone, "phone", "", "phone number")
	cmd.Flags().BoolVar(&isCustomer, "customer", true, "this contact is a customer")
	cmd.Flags().BoolVar(&isVendor, "vendor", false, "this contact is a vendor")
	cmd.Flags().Int32Var(&paymentTerms, "payment-terms", 0, "default payment terms, in days")
	cmd.Flags().StringVar(&creditLimit, "credit-limit", "", "credit limit")
	cmd.Flags().StringVar(&addr1, "address1", "", "billing address line 1")
	cmd.Flags().StringVar(&addr2, "address2", "", "billing address line 2")
	cmd.Flags().StringVar(&city, "city", "", "billing city")
	cmd.Flags().StringVar(&state, "state", "", "billing state/province")
	cmd.Flags().StringVar(&postal, "postal-code", "", "billing postal code")
	cmd.Flags().StringVar(&country, "country", "", "billing country")
	_ = cmd.MarkFlagRequired("contact-number")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newContactUpdateCmd() *cobra.Command {
	var name, email, phone string
	var paymentTerms int32
	var creditLimit string
	var addr1, addr2, city, state, postal, country string

	cmd := newVersionedMutateCmd(contactNoun, "update", resource.Doc{
		Summary:  "Update a contact",
		Detail:   "Only flags you pass are sent - omit a flag to leave that field unchanged.",
		Examples: []resource.Example{{Cmd: "dxctl contact update 5 --phone 555-0100"}},
	}, func(r run, id, resourceVersion int64) (proto.Message, error) {
		resp, err := denarixv1.NewContactServiceClient(r.conn).UpdateContact(r.ctx, &denarixv1.UpdateContactRequest{
			Id:                  id,
			ResourceVersion:     resourceVersion,
			Name:                r.optString("name", &name),
			Email:               r.optString("email", &email),
			Phone:               r.optString("phone", &phone),
			PaymentTermsDays:    r.optInt32("payment-terms", &paymentTerms),
			CreditLimit:         r.optDecimal("credit-limit", &creditLimit),
			BillingAddressLine1: r.optString("address1", &addr1),
			BillingAddressLine2: r.optString("address2", &addr2),
			BillingCity:         r.optString("city", &city),
			BillingState:        r.optString("state", &state),
			BillingPostalCode:   r.optString("postal-code", &postal),
			BillingCountry:      r.optString("country", &country),
		})
		return resp.GetContact(), err
	})
	cmd.Flags().StringVar(&name, "name", "", "new contact name")
	cmd.Flags().StringVar(&email, "email", "", "new email address")
	cmd.Flags().StringVar(&phone, "phone", "", "new phone number")
	cmd.Flags().Int32Var(&paymentTerms, "payment-terms", 0, "new default payment terms, in days")
	cmd.Flags().StringVar(&creditLimit, "credit-limit", "", "new credit limit")
	cmd.Flags().StringVar(&addr1, "address1", "", "new billing address line 1")
	cmd.Flags().StringVar(&addr2, "address2", "", "new billing address line 2")
	cmd.Flags().StringVar(&city, "city", "", "new billing city")
	cmd.Flags().StringVar(&state, "state", "", "new billing state/province")
	cmd.Flags().StringVar(&postal, "postal-code", "", "new billing postal code")
	cmd.Flags().StringVar(&country, "country", "", "new billing country")
	return cmd
}
