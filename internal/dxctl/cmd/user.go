// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
)

// newUserCmd groups app_user-level operations that aren't a business
// resource. reset-passkey is usable by a global admin (any user) or a
// business OWNER/ADMIN over a member of the current context's business.
func newUserCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "user",
		Short: "User account operations",
	}
	root.AddCommand(newUserResetPasskeyCmd())
	return root
}

func newUserResetPasskeyCmd() *cobra.Command {
	var userID int64
	var keepExisting bool
	cmd := &cobra.Command{
		Use:   "reset-passkey",
		Short: "Issue a one-time link so a user can register a new passkey",
		Long: `Mints a single-use enrollment token for a user who lost or replaced their
device. By default it revokes their existing passkeys (so a lost device can no
longer sign in); pass --keep-existing to just add a device.

Requires global-admin, or OWNER/ADMIN on the current context's business (the
target must be a member of it). Prints a one-time token — hand it to the user
yourself; denarix never sends it anywhere.`,
		Example: "  dxctl user reset-passkey --user 7",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := dialRun(cmd)
			if err != nil {
				return err
			}
			defer r.conn.Close()

			resp, err := denarixv1.NewUserServiceClient(r.conn).ResetUserCredentials(r.ctx, &denarixv1.ResetUserCredentialsRequest{
				UserId:       userID,
				BusinessId:   r.businessID,
				KeepExisting: keepExisting,
			})
			if err != nil {
				return err
			}

			effect := "existing passkeys will be revoked on redemption"
			if keepExisting {
				effect = "existing passkeys kept; this adds a device"
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Enrollment token for user %d (%s, expires %s):\n%s\n",
				userID, effect, resp.GetExpiresAt().AsTime().Format("2006-01-02"), resp.GetToken())
			fmt.Fprintf(w, "\nShare it with the user; they run:\n  dxctl login --token %s\n", resp.GetToken())
			return nil
		},
	}
	cmd.Flags().Int64Var(&userID, "user", 0, "user id to reset (required)")
	cmd.Flags().BoolVar(&keepExisting, "keep-existing", false, "add a device instead of revoking existing passkeys")
	_ = cmd.MarkFlagRequired("user")
	return cmd
}
