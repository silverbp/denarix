// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
)

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "whoami",
		Short:   "Show the signed-in user",
		Example: "  dxctl whoami",
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, _, _, err := dial()
			if err != nil {
				return err
			}
			defer conn.Close()

			resp, err := denarixv1.NewUserServiceClient(conn).GetMe(cmd.Context(), &denarixv1.GetMeRequest{})
			if err != nil {
				return err
			}
			u := resp.GetUser()
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "id:    %d\n", u.GetId())
			fmt.Fprintf(w, "email: %s\n", u.GetEmail())
			if u.GetIsGlobalAdmin() {
				fmt.Fprintln(w, "role:  global admin")
			}
			return nil
		},
	}
}

// newAdminCmd groups global-admin-only operations, which aren't scoped to
// a business the way every other noun's verbs are (there is only ever one
// global admin at a time — see auth.GrantGlobalAdmin).
func newAdminCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "admin",
		Short: "Global-admin-only user management",
	}
	root.AddCommand(newAdminGrantCmd())
	root.AddCommand(newAdminRevokeCmd())
	return root
}

func newAdminGrantCmd() *cobra.Command {
	var userID int64
	cmd := &cobra.Command{
		Use:     "grant",
		Short:   "Transfer global-admin status to another user",
		Example: "  dxctl admin grant --user 7",
		Long:    `Grants --user global-admin status, revoking it from whoever currently holds it — there is only ever one.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, _, _, err := dial()
			if err != nil {
				return err
			}
			defer conn.Close()
			resp, err := denarixv1.NewUserServiceClient(conn).SetGlobalAdmin(cmd.Context(), &denarixv1.SetGlobalAdminRequest{UserId: userID, IsGlobalAdmin: true})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s (id %d) is now the global admin\n", resp.GetUser().GetEmail(), resp.GetUser().GetId())
			return nil
		},
	}
	cmd.Flags().Int64Var(&userID, "user", 0, "user id to grant global-admin to (required)")
	_ = cmd.MarkFlagRequired("user")
	return cmd
}

func newAdminRevokeCmd() *cobra.Command {
	var userID int64
	cmd := &cobra.Command{
		Use:     "revoke",
		Short:   "Revoke a user's global-admin status",
		Example: "  dxctl admin revoke --user 7",
		Long:    `Leaves zero global admins until someone is granted it again.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, _, _, err := dial()
			if err != nil {
				return err
			}
			defer conn.Close()
			resp, err := denarixv1.NewUserServiceClient(conn).SetGlobalAdmin(cmd.Context(), &denarixv1.SetGlobalAdminRequest{UserId: userID, IsGlobalAdmin: false})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s (id %d) is no longer a global admin\n", resp.GetUser().GetEmail(), resp.GetUser().GetId())
			return nil
		},
	}
	cmd.Flags().Int64Var(&userID, "user", 0, "user id to revoke global-admin from (required)")
	_ = cmd.MarkFlagRequired("user")
	return cmd
}
