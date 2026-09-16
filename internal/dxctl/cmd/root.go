// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

// Package cmd is dxctl's cobra command tree: noun-first command groups
// (invoice, contact, ledger-account, ...), each with the verbs that
// actually apply to it rather than generic get/create/delete, -o
// json|yaml|table, and a kubeconfig-shaped ~/.dxctl/config for server +
// business context. `dxctl commands --json` dumps the whole tree as
// structured data.
package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/dxctl/apiclient"
	"github.com/silverbp/denarix/internal/dxctl/config"
	"github.com/silverbp/denarix/internal/dxctl/output"
	"github.com/silverbp/denarix/internal/version"
)

var (
	flagServer   string
	flagInsecure bool
	flagBusiness int64
	flagOutput   string
)

// Command groups, used to organize `dxctl --help`'s "Available Commands"
// section (see cobra's Command.GroupID / AddGroup) instead of dumping all
// ~15 top-level commands into one undifferentiated list.
const (
	groupAccounting = "accounting"
	groupAccount    = "account"
	groupCLI        = "cli"
)

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "dxctl",
		Short:         "dxctl controls the Denarix accounting API",
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	root.PersistentFlags().StringVar(&flagServer, "server", "", "override the current context's server address")
	root.PersistentFlags().BoolVar(&flagInsecure, "insecure", false, "disable TLS when overriding --server (a local dev server has no certificate)")
	root.PersistentFlags().Int64Var(&flagBusiness, "business", 0, "override the current context's business id")
	root.PersistentFlags().StringVarP(&flagOutput, "output", "o", output.FormatTable, "output format: table|json|yaml|pdf (pdf only where supported - reports, and get on invoice/estimate)")

	root.AddGroup(
		&cobra.Group{ID: groupAccounting, Title: "Accounting Commands:"},
		&cobra.Group{ID: groupAccount, Title: "Account Commands:"},
		&cobra.Group{ID: groupCLI, Title: "CLI Commands:"},
	)
	root.SetHelpCommandGroupID(groupCLI)
	root.SetCompletionCommandGroupID(groupCLI)

	addGrouped(root, groupAccounting,
		newInvoiceCmd(), newEstimateCmd(), newPaymentCmd(),
		newLedgerAccountCmd(), newLedgerTransactionCmd(),
		newContactCmd(), newItemCmd(), newTaxRateCmd(),
		newBankStatementCmd(), newBusinessCmd(), newContextCmd(),
		newReportCmd(), newCloseCmd(),
	)
	addGrouped(root, groupAccount,
		newLoginCmd(), newWhoamiCmd(), newAcceptInviteCmd(),
		newConfigCmd(), newAdminCmd(),
	)
	addGrouped(root, groupCLI,
		newCommandsCmd(), newVersionCmd(),
	)
	return root
}

// addGrouped adds each of cmds to root under groupID, so `dxctl --help`
// lists them together instead of in one flat, undifferentiated list.
func addGrouped(root *cobra.Command, groupID string, cmds ...*cobra.Command) {
	for _, c := range cmds {
		c.GroupID = groupID
		root.AddCommand(c)
	}
}

// resolveTarget resolves the server address, whether to dial it without
// TLS, and the business id to operate on: --server/--business flags take
// priority, falling back to the current ~/.dxctl/config context.
func resolveTarget() (server string, insecureTransport bool, businessID int64, err error) {
	cfg, err := config.Load()
	if err != nil {
		return "", false, 0, err
	}

	server, insecureTransport, businessID, currentErr := cfg.Current()
	if flagServer != "" {
		server = flagServer
		insecureTransport = flagInsecure
	}
	if flagBusiness != 0 {
		businessID = flagBusiness
	}
	if server == "" {
		if currentErr != nil {
			return "", false, 0, currentErr
		}
		return "", false, 0, fmt.Errorf("no server address: pass --server or set one via `dxctl config set-context`")
	}
	return server, insecureTransport, businessID, nil
}

func dial() (*grpc.ClientConn, string, int64, error) {
	server, insecureTransport, businessID, err := resolveTarget()
	if err != nil {
		return nil, "", 0, err
	}

	accessToken, err := resolveAccessToken(server, insecureTransport)
	if err != nil {
		return nil, "", 0, err
	}

	conn, err := apiclient.Dial(server, insecureTransport, accessToken)
	if err != nil {
		return nil, "", 0, fmt.Errorf("connecting to %s: %w", server, err)
	}
	return conn, server, businessID, nil
}

// resolveAccessToken returns the current context's access token,
// refreshing (and persisting the refresh) first if it's expired or about
// to expire. Returns "" with no error if the context has no user at all —
// e.g. one talking to a dev-bypass server, where no token is needed.
func resolveAccessToken(server string, insecureTransport bool) (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	creds, userName, ok, err := cfg.CurrentUserCredentials()
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	if time.Now().Before(creds.AccessTokenExpiry.Add(-30 * time.Second)) {
		return creds.AccessToken, nil
	}

	conn, err := apiclient.Dial(server, insecureTransport, "")
	if err != nil {
		return "", fmt.Errorf("connecting to %s to refresh session: %w", server, err)
	}
	defer conn.Close()

	resp, err := denarixv1.NewAuthServiceClient(conn).RefreshToken(context.Background(), &denarixv1.RefreshTokenRequest{
		RefreshToken: creds.RefreshToken,
		ClientName:   clientName(),
	})
	if err != nil {
		return "", fmt.Errorf("refreshing session (try `dxctl login` again): %w", err)
	}

	newCreds := config.UserCredentials{
		RefreshToken:      resp.GetRefreshToken(),
		AccessToken:       resp.GetAccessToken(),
		AccessTokenExpiry: resp.GetAccessTokenExpiresAt().AsTime(),
	}
	cfg.SetUserCredentials(userName, newCreds)
	if err := cfg.Save(); err != nil {
		return "", fmt.Errorf("saving refreshed session: %w", err)
	}
	return newCreds.AccessToken, nil
}

func clientName() string {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown-host"
	}
	return "dxctl on " + host
}
