// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

const authCodeTTL = 2 * time.Minute

type authCodeEntry struct {
	userID    int64
	expiresAt time.Time
}

var (
	authCodeMu    sync.Mutex
	authCodeStore = map[string]authCodeEntry{}
)

// IssueAuthCode mints a short-lived, single-use code bound to userID — the
// hand-off between the browser-driven WebAuthn ceremony
// (server/httpauth.go) and the CLI's loopback listener (dxctl login).
// In-memory by design: it's consumed within seconds, within one login
// flow, so it doesn't need to survive a server restart or be shared across
// replicas.
func IssueAuthCode(userID int64) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating auth code: %w", err)
	}
	code := base64.RawURLEncoding.EncodeToString(buf)

	authCodeMu.Lock()
	defer authCodeMu.Unlock()
	authCodeStore[code] = authCodeEntry{userID: userID, expiresAt: time.Now().Add(authCodeTTL)}
	return code, nil
}

// ConsumeAuthCode validates and immediately invalidates code (single use,
// regardless of whether it was valid).
func ConsumeAuthCode(code string) (userID int64, ok bool) {
	authCodeMu.Lock()
	defer authCodeMu.Unlock()

	entry, found := authCodeStore[code]
	delete(authCodeStore, code)
	if !found || time.Now().After(entry.expiresAt) {
		return 0, false
	}
	return entry.userID, true
}
