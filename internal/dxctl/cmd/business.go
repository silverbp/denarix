// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/dxctl/output"
	"github.com/silverbp/denarix/internal/dxctl/resource"
)

var businessNoun = resource.Noun{
	Singular: "business",
	Plural:   "businesses",
	Aliases:  []string{"businesses", "biz"},
	Columns: []resource.Column{
		resource.Int("ID", (*denarixv1.Business).GetId),
		resource.Str("NAME", (*denarixv1.Business).GetName),
		resource.Str("CURRENCY", (*denarixv1.Business).GetCurrencyCode),
		resource.Bool("ACTIVE", (*denarixv1.Business).GetIsActive),
		resource.Int("VERSION", (*denarixv1.Business).GetResourceVersion),
	},
}

var businessInviteNoun = resource.Noun{
	Singular: "invite",
	Plural:   "invites",
	Columns: []resource.Column{
		resource.Int("ID", (*denarixv1.BusinessInvite).GetId),
		resource.Str("EMAIL", (*denarixv1.BusinessInvite).GetEmail),
		resource.Str("ROLE", (*denarixv1.BusinessInvite).GetRole),
		resource.Bool("ACCEPTED", func(i *denarixv1.BusinessInvite) bool { return i.AcceptedAt != nil }),
		resource.Bool("REVOKED", func(i *denarixv1.BusinessInvite) bool { return i.RevokedAt != nil }),
	},
}

func newBusinessCmd() *cobra.Command {
	root := newGroupCmd(businessNoun, "Manage businesses")
	root.AddCommand(
		// "list businesses" naturally means "list businesses I belong to", not
		// "list businesses scoped to a business", so the business id is ignored.
		newListCmd(businessNoun, func(r run) ([]proto.Message, error) {
			resp, err := denarixv1.NewBusinessServiceClient(r.conn).ListMyBusinesses(r.ctx, &denarixv1.ListMyBusinessesRequest{})
			items := make([]proto.Message, 0, len(resp.GetMemberships()))
			for _, m := range resp.GetMemberships() {
				items = append(items, m.GetBusiness())
			}
			return items, err
		}),
		newGetCmd(businessNoun, func(r run, id int64) (proto.Message, error) {
			resp, err := denarixv1.NewBusinessServiceClient(r.conn).GetBusiness(r.ctx, &denarixv1.GetBusinessRequest{Id: id})
			return resp.GetBusiness(), err
		}),
		newBusinessCreateCmd(),
		newBusinessUpdateCmd(),
		newVersionedMutateCmd(businessNoun, "deactivate", resource.Doc{Summary: "Deactivate a business"}, func(r run, id, resourceVersion int64) (proto.Message, error) {
			resp, err := denarixv1.NewBusinessServiceClient(r.conn).DeactivateBusiness(r.ctx, &denarixv1.DeactivateBusinessRequest{Id: id, ResourceVersion: resourceVersion})
			return resp.GetBusiness(), err
		}),
		newBusinessInviteCmd(),
	)
	return root
}

func newBusinessCreateCmd() *cobra.Command {
	var name, taxID, addr1, addr2, city, state, postal, country, phone, email string

	cmd := newCreateCmd(businessNoun, resource.Doc{
		Summary:  "Create a business",
		Detail:   "Requires global-admin.",
		Examples: []resource.Example{{Cmd: `dxctl business create --name "Acme Co"`}},
	}, func(r run) (proto.Message, error) {
		resp, err := denarixv1.NewBusinessServiceClient(r.conn).CreateBusiness(r.ctx, &denarixv1.CreateBusinessRequest{
			Name:         name,
			TaxId:        r.optString("tax-id", &taxID),
			AddressLine1: r.optString("address1", &addr1),
			AddressLine2: r.optString("address2", &addr2),
			City:         r.optString("city", &city),
			State:        r.optString("state", &state),
			PostalCode:   r.optString("postal-code", &postal),
			Country:      r.optString("country", &country),
			Phone:        r.optString("phone", &phone),
			Email:        r.optString("email", &email),
		})
		return resp.GetBusiness(), err
	})
	cmd.Flags().StringVar(&name, "name", "", "business name (required)")
	cmd.Flags().StringVar(&taxID, "tax-id", "", "tax id")
	cmd.Flags().StringVar(&addr1, "address1", "", "address line 1")
	cmd.Flags().StringVar(&addr2, "address2", "", "address line 2")
	cmd.Flags().StringVar(&city, "city", "", "city")
	cmd.Flags().StringVar(&state, "state", "", "state/province")
	cmd.Flags().StringVar(&postal, "postal-code", "", "postal code")
	cmd.Flags().StringVar(&country, "country", "", "country")
	cmd.Flags().StringVar(&phone, "phone", "", "phone number")
	cmd.Flags().StringVar(&email, "email", "", "email address")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newBusinessUpdateCmd() *cobra.Command {
	var name, taxID, addr1, addr2, city, state, postal, country, phone, email string

	cmd := newVersionedMutateCmd(businessNoun, "update", resource.Doc{
		Summary:  "Update a business's profile",
		Detail:   "Only flags you pass are sent - omit a flag to leave that field unchanged.",
		Examples: []resource.Example{{Cmd: "dxctl business update 1 --phone 555-0100"}},
	}, func(r run, id, resourceVersion int64) (proto.Message, error) {
		resp, err := denarixv1.NewBusinessServiceClient(r.conn).UpdateBusiness(r.ctx, &denarixv1.UpdateBusinessRequest{
			Id:              id,
			ResourceVersion: resourceVersion,
			Name:            r.optString("name", &name),
			TaxId:           r.optString("tax-id", &taxID),
			AddressLine1:    r.optString("address1", &addr1),
			AddressLine2:    r.optString("address2", &addr2),
			City:            r.optString("city", &city),
			State:           r.optString("state", &state),
			PostalCode:      r.optString("postal-code", &postal),
			Country:         r.optString("country", &country),
			Phone:           r.optString("phone", &phone),
			Email:           r.optString("email", &email),
		})
		return resp.GetBusiness(), err
	})
	cmd.Flags().StringVar(&name, "name", "", "new business name")
	cmd.Flags().StringVar(&taxID, "tax-id", "", "new tax id")
	cmd.Flags().StringVar(&addr1, "address1", "", "new address line 1")
	cmd.Flags().StringVar(&addr2, "address2", "", "new address line 2")
	cmd.Flags().StringVar(&city, "city", "", "new city")
	cmd.Flags().StringVar(&state, "state", "", "new state/province")
	cmd.Flags().StringVar(&postal, "postal-code", "", "new postal code")
	cmd.Flags().StringVar(&country, "country", "", "new country")
	cmd.Flags().StringVar(&phone, "phone", "", "new phone number")
	cmd.Flags().StringVar(&email, "email", "", "new email address")
	return cmd
}

func newBusinessInviteCmd() *cobra.Command {
	root := newGroupCmd(businessInviteNoun, "Invite people into a business")
	root.AddCommand(
		// "list invites" naturally means "outstanding invites on the business
		// I'm working in".
		newListCmd(businessInviteNoun, func(r run) ([]proto.Message, error) {
			resp, err := denarixv1.NewBusinessServiceClient(r.conn).ListBusinessInvites(r.ctx, &denarixv1.ListBusinessInvitesRequest{BusinessId: r.businessID})
			return toMessages(resp.GetInvites()), err
		}),
		newBusinessInviteCreateCmd(),
		newMutateCmd(businessInviteNoun, "revoke", resource.Doc{Summary: "Revoke a business invite"}, func(r run, id int64) (proto.Message, error) {
			resp, err := denarixv1.NewBusinessServiceClient(r.conn).RevokeBusinessInvite(r.ctx, &denarixv1.RevokeBusinessInviteRequest{Id: id})
			return resp.GetInvite(), err
		}),
	)
	return root
}

// newBusinessInviteCreateCmd is hand-written rather than a newCreateCmd: in
// table mode it prints the one-time token alongside the invite, which the
// generic single-object output can't carry.
func newBusinessInviteCreateCmd() *cobra.Command {
	var email, role string

	cmd := &cobra.Command{
		Use:  "create",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := dialRun(cmd)
			if err != nil {
				return err
			}
			defer r.conn.Close()

			resp, err := denarixv1.NewBusinessServiceClient(r.conn).CreateBusinessInvite(r.ctx, &denarixv1.CreateBusinessInviteRequest{
				BusinessId: r.businessID,
				Email:      email,
				Role:       role,
			})
			if err != nil {
				return err
			}

			if flagOutput != output.FormatTable {
				return output.PrintOne(cmd.OutOrStdout(), flagOutput, resp.GetInvite(), businessInviteNoun.Columns)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Invited %s as %s (invite id %d, expires %s)\n",
				email, role, resp.GetInvite().GetId(), resp.GetInvite().GetExpiresAt().AsTime().Format("2006-01-02"))
			fmt.Fprintf(w, "\nToken (shown once - share it with %s yourself):\n%s\n", email, resp.GetToken())
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "invitee's email (required)")
	cmd.Flags().StringVar(&role, "role", "MEMBER", "OWNER, ADMIN, MEMBER, or VIEWER")
	_ = cmd.MarkFlagRequired("email")
	resource.Doc{
		Summary: "Invite someone into the current business",
		Detail: "Requires global-admin, or OWNER/ADMIN on the business itself. Prints a " +
			"one-time token - copy and send it to the invitee yourself (Slack, text, " +
			"email); denarix never sends it anywhere, and it's never shown again after this.",
		Examples: []resource.Example{{Cmd: "dxctl business invite create --email jane@example.com --role MEMBER"}},
	}.Apply(cmd)
	return cmd
}
