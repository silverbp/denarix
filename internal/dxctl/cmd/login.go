// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package cmd

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/dxctl/apiclient"
	"github.com/silverbp/denarix/internal/dxctl/config"
)

const loginTimeout = 3 * time.Minute

func newLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate via passkey",
		Long: `Authenticate the current context's server via passkey (WebAuthn): opens
your browser to complete the ceremony, then stores the resulting session in
~/.dxctl/config. Requires a context to already exist — run
"dxctl config set-context" first.

Redeeming an invite or a passkey-reset link? Pass --token <token>: the same
browser trip creates your passkey and signs you in.`,
		Example: "  dxctl login\n  dxctl login --token abc123token",
		RunE:    runLogin,
	}
	cmd.Flags().String("token", "", "invite or passkey-reset token: create a passkey and sign in in one step")
	return cmd
}

func runLogin(cmd *cobra.Command, args []string) error {
	enrollToken, _ := cmd.Flags().GetString("token")
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.CurrentContext == "" {
		return fmt.Errorf("no current context set; run `dxctl config set-context` first")
	}
	server, insecureTransport, _, err := cfg.Current()
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("starting local callback listener: %w", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	state, err := randomURLSafe(24)
	if err != nil {
		return err
	}

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	httpSrv := &http.Server{
		Handler: loginCallbackHandler(state, codeCh, errCh),
	}
	go func() { _ = httpSrv.Serve(listener) }()
	defer httpSrv.Close()

	authURL := fmt.Sprintf("%s/auth/start?redirect_uri=%s&state=%s",
		httpBaseURL(server, insecureTransport), url.QueryEscape(redirectURI), url.QueryEscape(state))
	if enrollToken != "" {
		authURL += "&token=" + url.QueryEscape(enrollToken)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Opening your browser to complete passkey sign-in...\n%s\n", authURL)
	if err := openBrowser(authURL); err != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Couldn't open a browser automatically — open the URL above manually.\n")
	}

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return err
	case <-time.After(loginTimeout):
		return fmt.Errorf("timed out waiting for browser login")
	}

	conn, err := apiclient.Dial(server, insecureTransport, "")
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", server, err)
	}
	defer conn.Close()

	resp, err := denarixv1.NewAuthServiceClient(conn).ExchangeCode(cmd.Context(), &denarixv1.ExchangeCodeRequest{
		Code:       code,
		ClientName: clientName(),
	})
	if err != nil {
		return fmt.Errorf("exchanging login code: %w", err)
	}

	userName := cfg.Contexts[indexOfContext(cfg, cfg.CurrentContext)].Context.Cluster
	cfg.SetUserCredentials(userName, config.UserCredentials{
		RefreshToken:      resp.GetRefreshToken(),
		AccessToken:       resp.GetAccessToken(),
		AccessTokenExpiry: resp.GetAccessTokenExpiresAt().AsTime(),
	})
	if err := cfg.SetContextUser(cfg.CurrentContext, userName); err != nil {
		return err
	}
	if err := cfg.Save(); err != nil {
		return err
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Login successful.")
	return nil
}

func newAcceptInviteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "accept-invite <token>",
		Short: "Accept a business invite",
		Long: `Redeems a business-invite token — you must already be signed in
("dxctl login") as the email the invite was sent to.`,
		Example: "  dxctl accept-invite abc123token",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, _, _, err := dial()
			if err != nil {
				return err
			}
			defer conn.Close()

			resp, err := denarixv1.NewBusinessServiceClient(conn).AcceptBusinessInvite(cmd.Context(), &denarixv1.AcceptBusinessInviteRequest{Token: args[0]})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Joined %q (id %d) as %s\n", resp.GetBusiness().GetName(), resp.GetBusiness().GetId(), resp.GetRole())
			return nil
		},
	}
}

func indexOfContext(cfg *config.Config, name string) int {
	for i, nc := range cfg.Contexts {
		if nc.Name == name {
			return i
		}
	}
	return -1
}

func loginCallbackHandler(wantState string, codeCh chan<- string, errCh chan<- error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("state"); got != wantState {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			errCh <- fmt.Errorf("state mismatch in callback (possible CSRF)")
			return
		}
		if errMsg := r.URL.Query().Get("error"); errMsg != "" {
			http.Error(w, errMsg, http.StatusBadRequest)
			errCh <- fmt.Errorf("login failed: %s", errMsg)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			errCh <- fmt.Errorf("missing code in callback")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<html><body><p>Login complete — you can close this tab.</p></body></html>")
		codeCh <- code
	})
	return mux
}

// httpBaseURL derives the plain-HTTP auth base URL from the gRPC server
// address. In dev (insecureTransport), the auth pages share the same host
// on DENARIX_HTTP_ADDR's default port; in a real deployment, a reverse proxy
// fronts both on the same public host + port, routed by path.
func httpBaseURL(grpcServer string, insecureTransport bool) string {
	host := grpcServer
	if i := strings.LastIndex(grpcServer, ":"); i != -1 {
		host = grpcServer[:i]
	}
	if !insecureTransport {
		return "https://" + host
	}
	return fmt.Sprintf("http://%s:9091", host)
}

func randomURLSafe(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func openBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Start()
}
