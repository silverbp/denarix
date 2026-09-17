// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	gowebauthn "github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/auth"
	"github.com/silverbp/denarix/internal/db/sqlcgen"
)

// newWebAuthnForTest builds a relying party the begin-registration ceremony
// can use; no real authenticator is needed to exercise the pre-ceremony
// authorization gate.
func newWebAuthnForTest(t *testing.T) *gowebauthn.WebAuthn {
	t.Helper()
	w, err := auth.NewWebAuthn("localhost", "Denarix", "http://localhost:9091")
	if err != nil {
		t.Fatalf("building webauthn: %v", err)
	}
	return w
}

// mkInvite creates a pending business_invite for email and returns the raw token.
func mkInvite(t *testing.T, ctx context.Context, businessID int64, email string) string {
	t.Helper()
	raw, err := auth.NewInviteToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	_, err = testStore.Queries.CreateBusinessInvite(ctx, sqlcgen.CreateBusinessInviteParams{
		BusinessID: businessID,
		Email:      email,
		Role:       "MEMBER",
		TokenHash:  auth.HashInviteToken(raw),
		ExpiresAt:  pgtype.Timestamp{Time: time.Now().Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("creating invite: %v", err)
	}
	return raw
}

// TestRegistrationTakeoverGate is the regression test for the account-takeover
// hole: an invite must never enroll a passkey onto an account that already has
// one. A brand-new account and an admin-issued enrollment token both proceed.
func TestRegistrationTakeoverGate(t *testing.T) {
	defer failOnPanic(t)
	ten := newTenant(t)
	ctx := ten.ctx
	w := newWebAuthnForTest(t)

	t.Run("new account invite begins ok", func(t *testing.T) {
		email := fmt.Sprintf("newuser-%d@test.local", time.Now().UnixNano())
		token := mkInvite(t, ctx, ten.businessID, email)
		if _, _, err := auth.BeginRegistration(ctx, testStore.Queries, w, token); err != nil {
			t.Fatalf("new-account registration should begin, got: %v", err)
		}
	})

	t.Run("invite refused when the account already has a passkey", func(t *testing.T) {
		email := fmt.Sprintf("victim-%d@test.local", time.Now().UnixNano())
		victim := must(testStore.Queries.CreateAppUser(ctx, sqlcgen.CreateAppUserParams{Email: email}))
		// Give the victim an existing passkey.
		if _, err := testStore.Queries.CreateWebAuthnCredential(ctx, sqlcgen.CreateWebAuthnCredentialParams{
			UserID:       victim.ID,
			CredentialID: []byte(fmt.Sprintf("cred-%d", victim.ID)),
			PublicKey:    []byte("pk"),
		}); err != nil {
			t.Fatalf("seeding credential: %v", err)
		}
		token := mkInvite(t, ctx, ten.businessID, email)

		_, _, err := auth.BeginRegistration(ctx, testStore.Queries, w, token)
		if err == nil {
			t.Fatal("expected takeover gate to refuse enrolling a passkey onto an account that already has one")
		}
		if !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("enrollment token begins ok for existing account", func(t *testing.T) {
		email := fmt.Sprintf("reset-%d@test.local", time.Now().UnixNano())
		user := must(testStore.Queries.CreateAppUser(ctx, sqlcgen.CreateAppUserParams{Email: email}))
		raw, err := auth.NewEnrollmentToken()
		if err != nil {
			t.Fatalf("token: %v", err)
		}
		if _, err := testStore.Queries.CreateCredentialEnrollment(ctx, sqlcgen.CreateCredentialEnrollmentParams{
			UserID:         user.ID,
			TokenHash:      auth.HashEnrollmentToken(raw),
			RevokeExisting: true,
			Purpose:        "RESET",
			ExpiresAt:      pgtype.Timestamp{Time: time.Now().Add(time.Hour), Valid: true},
		}); err != nil {
			t.Fatalf("creating enrollment: %v", err)
		}
		if _, _, err := auth.BeginRegistration(ctx, testStore.Queries, w, raw); err != nil {
			t.Fatalf("enrollment registration should begin, got: %v", err)
		}
	})

	t.Run("empty and unknown tokens are refused", func(t *testing.T) {
		if _, _, err := auth.BeginRegistration(ctx, testStore.Queries, w, ""); err == nil {
			t.Fatal("empty token should be refused")
		}
		if _, _, err := auth.BeginRegistration(ctx, testStore.Queries, w, "not-a-real-token"); err == nil {
			t.Fatal("unknown token should be refused")
		}
	})
}

// TestResetUserCredentials covers the reset RPC's authorization and that it
// mints an enrollment token with the right revoke semantics.
func TestResetUserCredentials(t *testing.T) {
	defer failOnPanic(t)
	ten := newTenant(t)
	users := newUserService(testStore)

	// A member of the owner's business.
	member := must(testStore.Queries.CreateAppUser(context.Background(), sqlcgen.CreateAppUserParams{Email: fmt.Sprintf("member-%d@test.local", time.Now().UnixNano())}))
	must(testStore.Queries.CreateBusinessUser(context.Background(), sqlcgen.CreateBusinessUserParams{BusinessID: ten.businessID, UserID: member.ID, Role: "MEMBER"}))

	t.Run("owner resets a member and gets a token", func(t *testing.T) {
		resp, err := users.ResetUserCredentials(ten.ctx, &denarixv1.ResetUserCredentialsRequest{UserId: member.ID, BusinessId: ten.businessID})
		if err != nil {
			t.Fatalf("owner reset should succeed: %v", err)
		}
		if resp.GetToken() == "" {
			t.Fatal("expected a token")
		}
		enroll := must(testStore.Queries.GetPendingCredentialEnrollmentByTokenHash(context.Background(), auth.HashEnrollmentToken(resp.GetToken())))
		if !enroll.RevokeExisting {
			t.Fatal("reset should revoke existing passkeys by default")
		}
	})

	t.Run("keep-existing does not revoke", func(t *testing.T) {
		resp := must(users.ResetUserCredentials(ten.ctx, &denarixv1.ResetUserCredentialsRequest{UserId: member.ID, BusinessId: ten.businessID, KeepExisting: true}))
		enroll := must(testStore.Queries.GetPendingCredentialEnrollmentByTokenHash(context.Background(), auth.HashEnrollmentToken(resp.GetToken())))
		if enroll.RevokeExisting {
			t.Fatal("keep-existing should not revoke")
		}
	})

	t.Run("outsider cannot reset", func(t *testing.T) {
		_, err := users.ResetUserCredentials(ten.outsider, &denarixv1.ResetUserCredentialsRequest{UserId: member.ID, BusinessId: ten.businessID})
		if err == nil {
			t.Fatal("a non-member must not be able to reset")
		}
		if code := status.Code(err); code != codes.NotFound && code != codes.PermissionDenied {
			t.Fatalf("want NotFound/PermissionDenied, got %v (%v)", code, err)
		}
	})

	t.Run("global admin can reset anyone", func(t *testing.T) {
		// Promote the owner to global admin, then reset a user in no shared business.
		must(auth.GrantGlobalAdmin(context.Background(), testStore, ten.user.ID))
		defer func() { _, _ = auth.RevokeGlobalAdmin(context.Background(), testStore, ten.user.ID) }()

		loner := must(testStore.Queries.CreateAppUser(context.Background(), sqlcgen.CreateAppUserParams{Email: fmt.Sprintf("loner-%d@test.local", time.Now().UnixNano())}))
		resp, err := users.ResetUserCredentials(ten.ctx, &denarixv1.ResetUserCredentialsRequest{UserId: loner.ID})
		if err != nil {
			t.Fatalf("global admin reset should succeed: %v", err)
		}
		if resp.GetToken() == "" {
			t.Fatal("expected a token")
		}
	})
}
