// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

// Package resource holds the shared metadata every dxctl noun command
// group (invoice, contact, ledger-account, ...) is built from: table
// columns for -o table output, and the naming/help-text scaffolding in
// doc.go that keeps every command's --help and `dxctl commands --json`
// output uniform.
package resource

import (
	"google.golang.org/protobuf/proto"
)

// Column renders one field of a resource for table (-o table) output.
type Column struct {
	Header string
	Value  func(v proto.Message) string
}

// Noun carries the metadata one accounting noun's whole command group
// shares: table columns, and the singular/plural forms used to render
// consistent help text across every verb without repeating it per command.
type Noun struct {
	Singular string // "invoice" — used in generated Use/Short/Example text
	Plural   string // "invoices" — used in list's Short text
	Aliases  []string
	Columns  []Column
}
