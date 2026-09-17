// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"

	gowebauthn "github.com/go-webauthn/webauthn/webauthn"

	"github.com/silverbp/denarix/internal/auth"
	"github.com/silverbp/denarix/internal/config"
	"github.com/silverbp/denarix/internal/db"
)

// logoPNG is the same app icon used by the Mac app, served at
// /auth/logo.png so the login page carries the same branding.
//
//go:embed static/logo.png
var logoPNG []byte

// rpDisplayName is shown to the user by their browser/OS passkey UI.
const rpDisplayName = "Denarix"

// newAuthMux serves the WebAuthn registration/login pages and their
// begin/finish endpoints.
func newAuthMux(store *db.Store, cfg config.Config) (http.Handler, error) {
	mux := http.NewServeMux()

	w, err := auth.NewWebAuthn(cfg.RPID, rpDisplayName, cfg.PublicBaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid WebAuthn config (check DENARIX_RP_ID/DENARIX_PUBLIC_BASE_URL): %w", err)
	}

	h := &authHandlers{store: store, webauthn: w}
	mux.HandleFunc("/auth/start", h.start)
	mux.HandleFunc("/auth/logo.png", h.logo)
	mux.HandleFunc("/auth/webauthn/register/begin", h.registerBegin)
	mux.HandleFunc("/auth/webauthn/register/finish", h.registerFinish)
	mux.HandleFunc("/auth/webauthn/login/begin", h.loginBegin)
	mux.HandleFunc("/auth/webauthn/login/finish", h.loginFinish)
	return mux, nil
}

type authHandlers struct {
	store    *db.Store
	webauthn *gowebauthn.WebAuthn
}

// start serves the page dxctl login opens: it reads redirect_uri/state
// from its own query string and, on success, hands the CLI's loopback
// listener a one-time code via that redirect — see the inline JS below and
// internal/auth/authcode.go. invite_token, if present, is threaded through
// to the register button — see auth.BeginRegistration: it's required to
// create a brand-new account, so an invite link is
// "/auth/start?invite_token=<token>".
func (h *authHandlers) start(w http.ResponseWriter, r *http.Request) {
	// token drives registration (a business invite for a new account, or a
	// credential-enrollment/reset token for an existing one). invite_token is
	// the legacy name for the same query param, still accepted.
	token := r.URL.Query().Get("token")
	if token == "" {
		token = r.URL.Query().Get("invite_token")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := authStartTemplate.Execute(w, map[string]string{
		"RedirectURI": r.URL.Query().Get("redirect_uri"),
		"State":       r.URL.Query().Get("state"),
		"Token":       token,
	}); err != nil {
		slog.Error("rendering auth start page", "error", err)
	}
}

func (h *authHandlers) logo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
	_, _ = w.Write(logoPNG)
}

func (h *authHandlers) registerBegin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		// Token is the sole identity input: a business invite (new account)
		// or a credential-enrollment/reset token (existing account). There is
		// no free-typed email — the token decides whose account this binds to.
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		http.Error(w, "a registration token is required", http.StatusBadRequest)
		return
	}

	creation, sessionID, err := auth.BeginRegistration(r.Context(), h.store.Queries, h.webauthn, req.Token)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	writeJSON(w, map[string]any{"options": creation, "session_id": sessionID})
}

func (h *authHandlers) registerFinish(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	sessionID := r.URL.Query().Get("session_id")
	if token == "" || sessionID == "" {
		http.Error(w, "token and session_id are required", http.StatusBadRequest)
		return
	}
	u, err := auth.FinishRegistration(r.Context(), h.store, h.webauthn, token, sessionID, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	h.issueCodeAndRespond(w, u)
}

func (h *authHandlers) loginBegin(w http.ResponseWriter, r *http.Request) {
	assertion, sessionID, err := auth.BeginLoginCeremony(h.webauthn)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"options": assertion, "session_id": sessionID})
}

func (h *authHandlers) loginFinish(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}
	u, err := auth.FinishLoginCeremony(r.Context(), h.store.Queries, h.webauthn, sessionID, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	h.issueCodeAndRespond(w, u)
}

func (h *authHandlers) issueCodeAndRespond(w http.ResponseWriter, u *auth.User) {
	code, err := auth.IssueAuthCode(u.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"code": code})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

// authStartTemplate is the entire browser-side half of the login flow:
// plain HTML + vanilla JS calling navigator.credentials.create()/get()
// directly. Cross-device sign-in (scan a QR code from your phone) is the
// browser's own built-in passkey UI — nothing here implements that
// transport.
var authStartTemplate = template.Must(template.New("auth-start").Parse(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="color-scheme" content="dark">
<title>Sign in to Denarix</title>
<style>
  :root {
    --bg: #16171a;
    --panel: #1e2024;
    --border: #303339;
    --text: #f2eee6;
    --text-dim: #9a9ca3;
    --accent: #e8a94a;
    --accent-text: #16171a;
    --danger: #e2665a;
  }
  * { box-sizing: border-box; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, sans-serif;
    background: var(--bg);
    color: var(--text);
    max-width: 380px;
    margin: 80px auto;
    padding: 0 20px;
  }
  .logo { display: block; width: 56px; height: 56px; margin: 0 auto 20px; border-radius: 14px; }
  h1 { font-size: 20px; text-align: center; margin: 0 0 24px; font-weight: 600; }
  p { color: var(--text-dim); font-size: 14px; line-height: 1.5; }
  hr { border: none; border-top: 1px solid var(--border); margin: 20px 0; }
  input {
    width: 100%; padding: 10px 12px; margin: 8px 0;
    background: var(--panel); border: 1px solid var(--border); border-radius: 8px;
    color: var(--text); font-size: 14px;
  }
  input::placeholder { color: var(--text-dim); }
  input:focus { outline: none; border-color: var(--accent); }
  button {
    width: 100%; padding: 11px; margin: 6px 0; cursor: pointer;
    border-radius: 8px; border: 1px solid var(--border);
    background: var(--panel); color: var(--text); font-size: 14px; font-weight: 500;
  }
  button:hover { border-color: var(--accent); }
  #login-btn { background: var(--accent); color: var(--accent-text); border-color: var(--accent); font-weight: 600; }
  #login-btn:hover { filter: brightness(1.08); }
  #error { color: var(--danger); white-space: pre-wrap; font-size: 13px; }
  #status { color: var(--text-dim); font-size: 13px; }
</style>
</head>
<body>
<img class="logo" src="/auth/logo.png" alt="Denarix">
<h1>Sign in to Denarix</h1>
<button id="login-btn">Sign in with a passkey</button>
{{if .Token}}
<hr>
<p>Your invite or reset link is ready. Create a passkey to finish — the account it belongs to is set by your link, so there's nothing to type.</p>
<button id="register-btn">Create your passkey</button>
{{else}}
<hr>
<p>New here? Denarix is invite-only — ask a global admin or a business owner to invite your email, then open the link they send you. Lost your device? Ask an admin to reset your passkey.</p>
{{end}}
<p id="status"></p>
<p id="error"></p>

<script>
const redirectURI = {{.RedirectURI}};
const state = {{.State}};
const token = {{.Token}};

function status(msg) { document.getElementById('status').textContent = msg; }
function fail(msg) { document.getElementById('error').textContent = msg; }

function base64urlToBuffer(base64url) {
  const padding = '='.repeat((4 - base64url.length % 4) % 4);
  const base64 = (base64url + padding).replace(/-/g, '+').replace(/_/g, '/');
  const raw = atob(base64);
  const bytes = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
  return bytes.buffer;
}

function bufferToBase64url(buffer) {
  const bytes = new Uint8Array(buffer);
  let str = '';
  for (const b of bytes) str += String.fromCharCode(b);
  return btoa(str).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function finishWithCode(code) {
  status('Success! Redirecting...');
  window.location.href = redirectURI + '?code=' + encodeURIComponent(code) + '&state=' + encodeURIComponent(state);
}

async function register() {
  fail(''); status('');
  if (!token) { fail('This page needs an invite or reset link to create a passkey.'); return; }
  try {
    status('Requesting a new passkey challenge...');
    const beginResp = await fetch('/auth/webauthn/register/begin', {
      method: 'POST', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({token: token}),
    });
    if (!beginResp.ok) throw new Error(await beginResp.text());
    const {options, session_id} = await beginResp.json();

    const publicKey = options.publicKey;
    publicKey.challenge = base64urlToBuffer(publicKey.challenge);
    publicKey.user.id = base64urlToBuffer(publicKey.user.id);
    if (publicKey.excludeCredentials) {
      for (const c of publicKey.excludeCredentials) c.id = base64urlToBuffer(c.id);
    }

    status('Waiting for your device...');
    const cred = await navigator.credentials.create({publicKey});

    const credentialJSON = {
      id: cred.id,
      rawId: bufferToBase64url(cred.rawId),
      type: cred.type,
      response: {
        clientDataJSON: bufferToBase64url(cred.response.clientDataJSON),
        attestationObject: bufferToBase64url(cred.response.attestationObject),
        transports: cred.response.getTransports ? cred.response.getTransports() : [],
      },
      clientExtensionResults: cred.getClientExtensionResults(),
    };

    status('Finishing registration...');
    const finishResp = await fetch(
      '/auth/webauthn/register/finish?token=' + encodeURIComponent(token) + '&session_id=' + encodeURIComponent(session_id),
      {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(credentialJSON)});
    if (!finishResp.ok) throw new Error(await finishResp.text());
    const {code} = await finishResp.json();
    finishWithCode(code);
  } catch (e) {
    fail(String(e));
  }
}

async function login() {
  fail(''); status('');
  try {
    status('Requesting a login challenge...');
    const beginResp = await fetch('/auth/webauthn/login/begin', {method: 'POST'});
    if (!beginResp.ok) throw new Error(await beginResp.text());
    const {options, session_id} = await beginResp.json();

    const publicKey = options.publicKey;
    publicKey.challenge = base64urlToBuffer(publicKey.challenge);
    if (publicKey.allowCredentials) {
      for (const c of publicKey.allowCredentials) c.id = base64urlToBuffer(c.id);
    }

    status('Waiting for your device...');
    const cred = await navigator.credentials.get({publicKey, mediation: 'optional'});

    const credentialJSON = {
      id: cred.id,
      rawId: bufferToBase64url(cred.rawId),
      type: cred.type,
      response: {
        clientDataJSON: bufferToBase64url(cred.response.clientDataJSON),
        authenticatorData: bufferToBase64url(cred.response.authenticatorData),
        signature: bufferToBase64url(cred.response.signature),
        userHandle: cred.response.userHandle ? bufferToBase64url(cred.response.userHandle) : undefined,
      },
      clientExtensionResults: cred.getClientExtensionResults(),
    };

    status('Finishing login...');
    const finishResp = await fetch(
      '/auth/webauthn/login/finish?session_id=' + encodeURIComponent(session_id),
      {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(credentialJSON)});
    if (!finishResp.ok) throw new Error(await finishResp.text());
    const {code} = await finishResp.json();
    finishWithCode(code);
  } catch (e) {
    fail(String(e));
  }
}

document.getElementById('login-btn').addEventListener('click', login);
const registerBtn = document.getElementById('register-btn');
if (registerBtn) registerBtn.addEventListener('click', register);
</script>
</body>
</html>
`))
