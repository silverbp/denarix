// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package resource

import (
	"fmt"

	typepb "google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/protobuf/proto"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
)

// Typed column constructors. A noun's table is a list of these, one per
// column, each taking the proto message's own getter as a method
// expression - e.g. resource.Int("ID", (*denarixv1.Invoice).GetId) - so the
// per-cell type assertion and fmt.Sprintf live here once instead of in
// every noun file. A value of the wrong message type renders as "" rather
// than panicking, since a column is only ever paired with its own noun.

// Integer is any integer a column can render (ids, counts, versions).
type Integer interface {
	~int | ~int32 | ~int64
}

func col[T proto.Message](header string, f func(T) string) Column {
	return Column{Header: header, Value: func(v proto.Message) string {
		m, ok := v.(T)
		if !ok {
			return ""
		}
		return f(m)
	}}
}

// Str renders a string field as-is.
func Str[T proto.Message](header string, f func(T) string) Column {
	return col(header, f)
}

// Int renders an integer field (an id, a count, a resource version).
func Int[T proto.Message, N Integer](header string, f func(T) N) Column {
	return col(header, func(m T) string { return fmt.Sprintf("%d", f(m)) })
}

// OptInt renders an optional integer field, "" when unset.
func OptInt[T proto.Message, N Integer](header string, f func(T) *N) Column {
	return col(header, func(m T) string {
		p := f(m)
		if p == nil {
			return ""
		}
		return fmt.Sprintf("%d", *p)
	})
}

// Bool renders a bool field as true/false.
func Bool[T proto.Message](header string, f func(T) bool) Column {
	return col(header, func(m T) string { return fmt.Sprintf("%v", f(m)) })
}

// Money renders a Decimal field's exact string value, "" when unset.
func Money[T proto.Message](header string, f func(T) *denarixv1.Decimal) Column {
	return col(header, func(m T) string { return f(m).GetValue() })
}

// Date renders a google.type.Date field as YYYY-MM-DD, "" when unset.
func Date[T proto.Message](header string, f func(T) *typepb.Date) Column {
	return col(header, func(m T) string { return FormatDate(f(m)) })
}

// FormatDate renders a google.type.Date as YYYY-MM-DD, "" for nil.
func FormatDate(d *typepb.Date) string {
	if d == nil {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", d.GetYear(), d.GetMonth(), d.GetDay())
}
