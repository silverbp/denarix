// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/silverbp/denarix/internal/db/sqlcgen"
)

// RefreshTokenTTL is long-lived deliberately — unlike the access token, a
// refresh token is server-side and individually revocable (user_session),
// so its risk is bounded by revocation, not lifetime.
const RefreshTokenTTL = 30 * 24 * time.Hour

// IssueSession creates a new refresh token for userID, returning the raw
// token (given to the client, never persisted) and the session row.
func IssueSession(ctx context.Context, q *sqlcgen.Queries, userID int64, clientName string) (rawToken string, session sqlcgen.UserSession, err error) {
	rawToken, err = randomToken()
	if err != nil {
		return "", sqlcgen.UserSession{}, err
	}

	session, err = q.CreateUserSession(ctx, sqlcgen.CreateUserSessionParams{
		UserID:           userID,
		RefreshTokenHash: hashToken(rawToken),
		ClientName:       &clientName,
		ExpiresAt:        pgtype.Timestamp{Time: time.Now().Add(RefreshTokenTTL), Valid: true},
	})
	if err != nil {
		return "", sqlcgen.UserSession{}, err
	}
	return rawToken, session, nil
}

// RotateSession verifies rawToken against an active (unrevoked,
// unexpired) session, revokes it, and issues a new one in its place —
// rotation-on-use, so a stolen-and-reused old refresh token is detectable
// (it's already revoked) rather than silently accepted forever.
func RotateSession(ctx context.Context, q *sqlcgen.Queries, rawToken, clientName string) (newRawToken string, u *User, err error) {
	old, err := q.GetUserSessionByHash(ctx, hashToken(rawToken))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, fmt.Errorf("refresh token is invalid, expired, or revoked")
		}
		return "", nil, err
	}

	newRawToken, newSession, err := IssueSession(ctx, q, old.UserID, clientName)
	if err != nil {
		return "", nil, err
	}
	if _, err := q.RevokeUserSession(ctx, sqlcgen.RevokeUserSessionParams{ID: old.ID, ReplacedBySessionID: &newSession.ID}); err != nil {
		return "", nil, err
	}

	appUser, err := q.GetAppUser(ctx, old.UserID)
	if err != nil {
		return "", nil, err
	}
	return newRawToken, &User{ID: appUser.ID, Email: appUser.Email}, nil
}

// RevokeSession revokes rawToken's session (logout). Not an error if the
// token is already invalid/expired/revoked — logout is idempotent.
func RevokeSession(ctx context.Context, q *sqlcgen.Queries, rawToken string) error {
	session, err := q.GetUserSessionByHash(ctx, hashToken(rawToken))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	_, err = q.RevokeUserSession(ctx, sqlcgen.RevokeUserSessionParams{ID: session.ID, ReplacedBySessionID: nil})
	return err
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewInviteToken/HashInviteToken back business_invite.token_hash — exported
// wrappers around the same random-token/sha256-digest pattern
// refresh_token_hash already uses (see randomToken/hashToken above), for
// callers outside this package (business_service.go's invite RPCs).
func NewInviteToken() (string, error)     { return randomToken() }
func HashInviteToken(token string) string { return hashToken(token) }
