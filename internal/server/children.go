// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"context"

	"github.com/silverbp/denarix/internal/db/sqlcgen"
)

// Batch child loading for list handlers. A resource with children (an
// invoice's lines, a payment's applications, a transaction's entries) is
// converted through withChildren so the children of a whole page come back
// in one query and each parent is handed its own slice - never one query per
// parent. Single-row handlers go through the same function with a
// one-element slice (see one), so there is exactly one conversion path per
// resource.

// withChildren lists the children of every parent in one call to list and
// converts each parent together with its own children, in parent order.
func withChildren[P, C, PB any](
	ctx context.Context, q *sqlcgen.Queries, parents []P,
	parentID func(P) int64,
	list func(q *sqlcgen.Queries, ctx context.Context, parentIDs []int64) ([]C, error),
	childParent func(C) int64,
	convert func(P, []C) PB,
) ([]PB, error) {
	out := make([]PB, len(parents))
	if len(parents) == 0 {
		return out, nil
	}
	children, err := list(q, ctx, idsOf(parents, parentID))
	if err != nil {
		return nil, translatePgError(err)
	}
	byParent := groupBy(children, childParent)
	for i, p := range parents {
		out[i] = convert(p, byParent[parentID(p)])
	}
	return out, nil
}

// idsOf collects one int64 per element, in order.
func idsOf[T any](xs []T, id func(T) int64) []int64 {
	ids := make([]int64, len(xs))
	for i, x := range xs {
		ids[i] = id(x)
	}
	return ids
}

// groupBy buckets xs by key, preserving each bucket's input order.
func groupBy[K comparable, V any](xs []V, key func(V) K) map[K][]V {
	m := make(map[K][]V)
	for _, x := range xs {
		k := key(x)
		m[k] = append(m[k], x)
	}
	return m
}

// one unwraps a single-parent call to a batch converter.
func one[T any](xs []T, err error) (T, error) {
	var zero T
	if err != nil {
		return zero, err
	}
	return xs[0], nil
}
