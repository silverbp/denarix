// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package auth

import "context"

// User is the caller resolved by the auth interceptor, stashed in the
// request context for handlers and RequireBusinessRole to read.
type User struct {
	ID    int64
	Email string
}

type ctxKey struct{}

func ContextWithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// UserFromContext returns the caller resolved for this RPC, or false if
// none was resolved (should never happen once the interceptor is wired up
// for every service — a missing user is a bug, not a valid unauthenticated
// state).
func UserFromContext(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(ctxKey{}).(*User)
	return u, ok
}
