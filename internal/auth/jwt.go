// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// AccessTokenTTL is short deliberately: the interceptor verifies the
// signature in-process on every RPC with no DB round trip, so a stolen
// access token stays valid at most this long — real revocation happens at
// the refresh-token layer (see session.go), not here.
const AccessTokenTTL = 15 * time.Minute

type accessClaims struct {
	Email string `json:"email"`
	jwt.RegisteredClaims
}

// MintAccessToken signs a short-lived HS256 JWT for u, using secret from
// config.JWTSecret.
func MintAccessToken(secret string, u *User) (token string, expiresAt time.Time, err error) {
	now := time.Now()
	expiresAt = now.Add(AccessTokenTTL)
	claims := accessClaims{
		Email: u.Email,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprintf("%d", u.ID),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	return signed, expiresAt, err
}

// VerifyAccessToken checks signature and expiry (jwt.ParseWithClaims
// validates exp automatically) and returns the encoded user.
func VerifyAccessToken(secret, token string) (*User, error) {
	var claims accessClaims
	parsed, err := jwt.ParseWithClaims(token, &claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	if !parsed.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	var userID int64
	if _, err := fmt.Sscanf(claims.Subject, "%d", &userID); err != nil {
		return nil, fmt.Errorf("invalid subject claim: %w", err)
	}
	return &User{ID: userID, Email: claims.Email}, nil
}
