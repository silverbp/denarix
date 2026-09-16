// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

// Optimistic concurrency, Kubernetes-style. Every mutable resource carries a
// resource_version the database bumps on each committed write to its row
// (the bump_resource_version trigger in migrations/00001_initial.up.sql);
// a client that read version N and sends it back on an update only wins if
// the row is still at N. The check itself is in SQL (`AND resource_version
// = $expected` on every Update*/Deactivate* query) so it's atomic with the
// write - this helper shapes the request field going in; translateUpdateError
// (pgerror.go) shapes the no-row-matched result coming out.

// expectedResourceVersion turns a request's resource_version into the
// nullable precondition the update queries take: 0 (unset) means
// unconditional, like an empty resourceVersion in k8s.
func expectedResourceVersion(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}
