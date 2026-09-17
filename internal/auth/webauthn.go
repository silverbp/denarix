// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/go-webauthn/webauthn/protocol"
	gowebauthn "github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"

	"github.com/silverbp/denarix/internal/db"
	"github.com/silverbp/denarix/internal/db/sqlcgen"
)

// NewWebAuthn builds the relying-party instance used for every ceremony.
// rpID must be the domain the browser is actually on during the ceremony
// (no scheme/port); origin is the exact scheme+host[+port].
func NewWebAuthn(rpID, rpDisplayName, origin string) (*gowebauthn.WebAuthn, error) {
	return gowebauthn.New(&gowebauthn.Config{
		RPDisplayName: rpDisplayName,
		RPID:          rpID,
		RPOrigins:     []string{origin},
	})
}

// webauthnUser adapts an app_user + its stored credentials to the
// go-webauthn User interface.
type webauthnUser struct {
	id          int64
	email       string
	displayName string
	credentials []gowebauthn.Credential
}

func (u *webauthnUser) WebAuthnID() []byte                           { return []byte(fmt.Sprintf("%d", u.id)) }
func (u *webauthnUser) WebAuthnName() string                         { return u.email }
func (u *webauthnUser) WebAuthnDisplayName() string                  { return u.displayName }
func (u *webauthnUser) WebAuthnCredentials() []gowebauthn.Credential { return u.credentials }

func loadWebAuthnUser(ctx context.Context, q *sqlcgen.Queries, appUser sqlcgen.AppUser) (*webauthnUser, error) {
	rows, err := q.ListWebAuthnCredentialsForUser(ctx, appUser.ID)
	if err != nil {
		return nil, err
	}
	creds := make([]gowebauthn.Credential, len(rows))
	for i, r := range rows {
		creds[i] = credentialFromRow(r)
	}
	displayName := appUser.Email
	if appUser.DisplayName != nil && *appUser.DisplayName != "" {
		displayName = *appUser.DisplayName
	}
	return &webauthnUser{id: appUser.ID, email: appUser.Email, displayName: displayName, credentials: creds}, nil
}

func credentialFromRow(r sqlcgen.WebauthnCredential) gowebauthn.Credential {
	var transports []protocol.AuthenticatorTransport
	if r.Transports != nil && *r.Transports != "" {
		for _, t := range strings.Split(*r.Transports, ",") {
			transports = append(transports, protocol.AuthenticatorTransport(t))
		}
	}
	var attestationType string
	if r.AttestationType != nil {
		attestationType = *r.AttestationType
	}
	return gowebauthn.Credential{
		ID:              r.CredentialID,
		PublicKey:       r.PublicKey,
		AttestationType: attestationType,
		Transport:       transports,
		Flags: gowebauthn.CredentialFlags{
			BackupEligible: r.BackupEligible,
			BackupState:    r.BackupState,
		},
		Authenticator: gowebauthn.Authenticator{
			AAGUID:    r.Aaguid,
			SignCount: uint32(r.SignCount),
		},
	}
}

// --- ceremony session store ---
//
// SessionData (the challenge, etc.) must survive between a ceremony's
// begin and finish HTTP requests. In-memory by design, same rationale as
// authcode.go: a ceremony completes within seconds, so this doesn't need
// to survive a server restart or be shared across replicas.

var (
	ceremonyMu    sync.Mutex
	ceremonyStore = map[string]*gowebauthn.SessionData{}
)

func saveCeremonySession(s *gowebauthn.SessionData) (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating ceremony session id: %w", err)
	}
	id := base64.RawURLEncoding.EncodeToString(buf)

	ceremonyMu.Lock()
	defer ceremonyMu.Unlock()
	ceremonyStore[id] = s
	return id, nil
}

func loadCeremonySession(id string) (*gowebauthn.SessionData, bool) {
	ceremonyMu.Lock()
	defer ceremonyMu.Unlock()
	s, ok := ceremonyStore[id]
	delete(ceremonyStore, id) // one-shot
	return s, ok
}

// --- registration ceremony ---
//
// Registration is always driven by a token, never by a free-typed email.
// A brand-new account is created from a business_invite (which carries the
// invited email); an additional/replacement passkey for an EXISTING
// account is authorized by a credential_enrollment token (see
// UserService.ResetUserCredentials and bootstrap.go). This is what closes
// account takeover: there is no way to say "enroll a passkey for email X";
// you redeem a secret token, and the token decides whose account it binds
// to. Knowing someone's email is never sufficient.

type registrationKind int

const (
	regNewAccount registrationKind = iota // business_invite -> create account + grant
	regEnrollment                         // credential_enrollment -> add/replace a passkey on an existing account
)

type registrationTarget struct {
	kind       registrationKind
	appUser    sqlcgen.AppUser
	invite     sqlcgen.BusinessInvite      // set when kind == regNewAccount
	enrollment sqlcgen.CredentialEnrollment // set when kind == regEnrollment
}

// resolveRegistrationTarget maps a registration token to what it authorizes.
// createIfNew is true only during BeginRegistration: a brand-new invited
// account is created here so the ceremony has a stable user handle;
// FinishRegistration resolves the same token again with createIfNew=false
// and expects the account to already exist.
func resolveRegistrationTarget(ctx context.Context, q *sqlcgen.Queries, token string, createIfNew bool) (registrationTarget, error) {
	if token == "" {
		return registrationTarget{}, fmt.Errorf("registration requires an invite or enrollment token")
	}

	// An enrollment token (existing account adding/replacing a passkey) takes
	// precedence — its hash space is disjoint from invites in practice.
	enroll, err := q.GetPendingCredentialEnrollmentByTokenHash(ctx, HashEnrollmentToken(token))
	if err == nil {
		appUser, err := q.GetAppUser(ctx, enroll.UserID)
		if err != nil {
			return registrationTarget{}, err
		}
		return registrationTarget{kind: regEnrollment, appUser: appUser, enrollment: enroll}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return registrationTarget{}, err
	}

	invite, err := q.GetPendingBusinessInviteByTokenHash(ctx, HashInviteToken(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return registrationTarget{}, fmt.Errorf("token is invalid, expired, or already used")
		}
		return registrationTarget{}, err
	}

	appUser, err := q.GetAppUserByEmail(ctx, invite.Email)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return registrationTarget{}, err
		}
		if !createIfNew {
			return registrationTarget{}, fmt.Errorf("registration session is no longer valid; start again")
		}
		displayName := invite.Email
		appUser, err = q.CreateAppUser(ctx, sqlcgen.CreateAppUserParams{Email: invite.Email, DisplayName: &displayName})
		if err != nil {
			return registrationTarget{}, err
		}
		return registrationTarget{kind: regNewAccount, appUser: appUser, invite: invite}, nil
	}

	// The account already exists. An invite can only ever create a passkey for
	// a fresh account; if this account already has one, adding another is a
	// takeover attempt and is refused. Redeeming the invite's business grant
	// for an existing user is done, authenticated, via
	// BusinessService.AcceptBusinessInvite instead.
	creds, err := q.ListWebAuthnCredentialsForUser(ctx, appUser.ID)
	if err != nil {
		return registrationTarget{}, err
	}
	if len(creds) > 0 {
		return registrationTarget{}, fmt.Errorf("an account for %s already exists; sign in with your existing passkey and run `dxctl accept-invite`, or ask an admin to reset your passkey", invite.Email)
	}
	// Account exists but has no passkey (an interrupted first registration) —
	// let it complete.
	return registrationTarget{kind: regNewAccount, appUser: appUser, invite: invite}, nil
}

// BeginRegistration starts a passkey registration driven entirely by a
// token: a business_invite for a brand-new account, or a
// credential_enrollment token for an existing one. See
// resolveRegistrationTarget for the rules.
func BeginRegistration(ctx context.Context, q *sqlcgen.Queries, w *gowebauthn.WebAuthn, token string) (*protocol.CredentialCreation, string, error) {
	target, err := resolveRegistrationTarget(ctx, q, token, true)
	if err != nil {
		return nil, "", err
	}

	u, err := loadWebAuthnUser(ctx, q, target.appUser)
	if err != nil {
		return nil, "", err
	}

	creation, session, err := w.BeginRegistration(u, gowebauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired))
	if err != nil {
		return nil, "", err
	}
	sessionID, err := saveCeremonySession(session)
	if err != nil {
		return nil, "", err
	}
	return creation, sessionID, nil
}

// FinishRegistration completes the ceremony begun by BeginRegistration,
// persisting the new credential and, in the same transaction, doing the
// token's side effect:
//
//   - business_invite  -> create the business_user grant and mark the invite
//     accepted, so an account never exists without the access it was
//     invited for.
//   - credential_enrollment -> optionally revoke the account's other
//     passkeys (the reset case) and consume the token.
//
// If any of that fails the whole registration rolls back.
func FinishRegistration(ctx context.Context, store *db.Store, w *gowebauthn.WebAuthn, token, sessionID string, r *http.Request) (*User, error) {
	session, ok := loadCeremonySession(sessionID)
	if !ok {
		return nil, fmt.Errorf("registration session expired or not found")
	}

	target, err := resolveRegistrationTarget(ctx, store.Queries, token, false)
	if err != nil {
		return nil, err
	}
	u, err := loadWebAuthnUser(ctx, store.Queries, target.appUser)
	if err != nil {
		return nil, err
	}

	cred, err := w.FinishRegistration(u, *session, r)
	if err != nil {
		return nil, err
	}

	var transportsPtr *string
	if len(cred.Transport) > 0 {
		strs := make([]string, len(cred.Transport))
		for i, t := range cred.Transport {
			strs[i] = string(t)
		}
		joined := strings.Join(strs, ",")
		transportsPtr = &joined
	}
	var attestationTypePtr *string
	if cred.AttestationType != "" {
		attestationTypePtr = &cred.AttestationType
	}

	err = store.ExecTx(ctx, func(q *sqlcgen.Queries) error {
		if target.kind == regEnrollment && target.enrollment.RevokeExisting {
			if err := q.DeleteWebAuthnCredentialsForUser(ctx, target.appUser.ID); err != nil {
				return err
			}
		}

		if _, err := q.CreateWebAuthnCredential(ctx, sqlcgen.CreateWebAuthnCredentialParams{
			UserID:          target.appUser.ID,
			CredentialID:    cred.ID,
			PublicKey:       cred.PublicKey,
			AttestationType: attestationTypePtr,
			Transports:      transportsPtr,
			Aaguid:          cred.Authenticator.AAGUID,
			SignCount:       int64(cred.Authenticator.SignCount),
			BackupEligible:  cred.Flags.BackupEligible,
			BackupState:     cred.Flags.BackupState,
		}); err != nil {
			return err
		}

		switch target.kind {
		case regEnrollment:
			return q.ConsumeCredentialEnrollment(ctx, target.enrollment.ID)
		case regNewAccount:
			if _, err := q.CreateBusinessUser(ctx, sqlcgen.CreateBusinessUserParams{
				BusinessID: target.invite.BusinessID,
				UserID:     target.appUser.ID,
				Role:       target.invite.Role,
			}); err != nil {
				return err
			}
			_, err := q.AcceptBusinessInvite(ctx, sqlcgen.AcceptBusinessInviteParams{ID: target.invite.ID, AcceptedByUserID: &target.appUser.ID})
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &User{ID: target.appUser.ID, Email: target.appUser.Email}, nil
}
// --- login ceremony ---
//
// Usernameless/discoverable: the browser's own passkey UI picks which
// credential to use (including via cross-device QR/BLE — handled entirely
// by the browser, nothing here implements that transport), so there's no
// "enter your email" step before this.

func BeginLoginCeremony(w *gowebauthn.WebAuthn) (*protocol.CredentialAssertion, string, error) {
	assertion, session, err := w.BeginDiscoverableLogin()
	if err != nil {
		return nil, "", err
	}
	sessionID, err := saveCeremonySession(session)
	if err != nil {
		return nil, "", err
	}
	return assertion, sessionID, nil
}

func FinishLoginCeremony(ctx context.Context, q *sqlcgen.Queries, w *gowebauthn.WebAuthn, sessionID string, r *http.Request) (*User, error) {
	session, ok := loadCeremonySession(sessionID)
	if !ok {
		return nil, fmt.Errorf("login session expired or not found")
	}

	handler := func(rawID, userHandle []byte) (gowebauthn.User, error) {
		var id int64
		if _, err := fmt.Sscanf(string(userHandle), "%d", &id); err != nil {
			return nil, fmt.Errorf("invalid user handle: %w", err)
		}
		appUser, err := q.GetAppUser(ctx, id)
		if err != nil {
			return nil, err
		}
		return loadWebAuthnUser(ctx, q, appUser)
	}

	validatedUser, validatedCred, err := w.FinishPasskeyLogin(handler, *session, r)
	if err != nil {
		return nil, err
	}
	wu, ok := validatedUser.(*webauthnUser)
	if !ok {
		return nil, fmt.Errorf("unexpected user type %T from FinishPasskeyLogin", validatedUser)
	}

	// Clone-detection bookkeeping: persist the authenticator's post-ceremony
	// sign count (FinishPasskeyLogin already rejected the login if it looked
	// cloned; this just keeps our stored baseline current for next time).
	// backup_state can legitimately change between logins (e.g. a passkey
	// newly synced to iCloud Keychain) and must be kept current too, or a
	// later login's BE/BS consistency check can fail against a stale value.
	credRow, err := q.GetWebAuthnCredentialByCredentialID(ctx, validatedCred.ID)
	if err != nil {
		return nil, err
	}
	if err := q.UpdateWebAuthnCredentialAfterLogin(ctx, sqlcgen.UpdateWebAuthnCredentialAfterLoginParams{
		ID:          credRow.ID,
		SignCount:   int64(validatedCred.Authenticator.SignCount),
		BackupState: validatedCred.Flags.BackupState,
	}); err != nil {
		return nil, err
	}

	return &User{ID: wu.id, Email: wu.email}, nil
}
