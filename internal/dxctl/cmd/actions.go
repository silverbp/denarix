// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/silverbp/denarix/internal/dxctl/output"
	"github.com/silverbp/denarix/internal/dxctl/resource"
)

// Every noun's verbs reduce to a handful of RPC-calling shapes. A noun's own
// file supplies one small closure per verb and the builders below (newGetCmd,
// newListCmd, newNoArgCmd, newMutateCmd, ...) wire it into a cobra.Command
// with uniform id parsing, dialing, output formatting, and help text - so no
// noun file parses an id, opens a connection, or formats output itself.

// run is what a verb's closure receives: the request context, the dialed
// connection, the business id from the active context (or --business), and
// the command itself, for reading its own flags via the opt* helpers in
// flags.go. The builder owns dialing and closing the connection.
type run struct {
	ctx        context.Context
	conn       *grpc.ClientConn
	businessID int64
	cmd        *cobra.Command
}

type getFunc func(r run, id int64) (proto.Message, error)
type listFunc func(r run) ([]proto.Message, error)
type noArgFunc func(r run) (proto.Message, error)
type mutateFunc func(r run, id int64) (proto.Message, error)

// versionedMutateFunc is mutateFunc for verbs whose RPC takes a
// resource_version precondition (every Update*/Deactivate* on a versioned
// resource - see addResourceVersionFlag); 0 means unconditional.
type versionedMutateFunc func(r run, id int64, resourceVersion int64) (proto.Message, error)
type pdfFunc func(r run, id int64) ([]byte, error)

// toMessages widens a typed response slice to the []proto.Message the
// output package renders.
func toMessages[T proto.Message](xs []T) []proto.Message {
	out := make([]proto.Message, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}

// newGroupCmd builds a noun's parent command (e.g. `dxctl invoice`).
func newGroupCmd(n resource.Noun, summary string) *cobra.Command {
	cmd := &cobra.Command{Use: n.Singular, Aliases: n.Aliases}
	resource.Doc{Summary: summary}.Apply(cmd)
	return cmd
}

// article returns "an" before a vowel sound, else "a" — so generated help
// text reads "Get an invoice" / "Get a payment" instead of "Get a invoice".
func article(s string) string {
	if len(s) > 0 && strings.ContainsRune("aeiouAEIOU", rune(s[0])) {
		return "an"
	}
	return "a"
}

// withDefaults fills a verb's Doc with the standard example when the noun
// file gave none, so every generated command has at least one.
func withDefaults(doc resource.Doc, n resource.Noun, verb string, args string) resource.Doc {
	if len(doc.Examples) == 0 {
		doc.Examples = []resource.Example{{Cmd: strings.TrimSpace(fmt.Sprintf("dxctl %s %s %s", n.Singular, verb, args))}}
	}
	return doc
}

// dialRun opens the connection every verb needs and packages it as a run.
// The caller must r.conn.Close().
func dialRun(cmd *cobra.Command) (run, error) {
	conn, _, businessID, err := dial()
	if err != nil {
		return run{}, err
	}
	return run{ctx: cmd.Context(), conn: conn, businessID: businessID, cmd: cmd}, nil
}

// newGetCmd builds a noun's `get <id>` command. pdfFn is optional (variadic
// so nouns without a PDF rendering don't have to pass anything) — when
// given, `-o pdf` calls it and writes the raw PDF bytes instead of calling
// fn and formatting the result as table/json/yaml, the same output-format
// switch the report commands use rather than a separate `pdf` subcommand.
func newGetCmd(n resource.Noun, fn getFunc, pdfFn ...pdfFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:  "get <id>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(n.Singular, args[0])
			if err != nil {
				return err
			}
			r, err := dialRun(cmd)
			if err != nil {
				return err
			}
			defer r.conn.Close()
			if flagOutput == output.FormatPDF {
				if len(pdfFn) == 0 {
					return fmt.Errorf("pdf output is not supported for %s", n.Singular)
				}
				content, err := pdfFn[0](r, id)
				if err != nil {
					return err
				}
				_, err = cmd.OutOrStdout().Write(content)
				return err
			}
			obj, err := fn(r, id)
			if err != nil {
				return err
			}
			return output.PrintOne(cmd.OutOrStdout(), flagOutput, obj, n.Columns)
		},
	}
	examples := []resource.Example{{Cmd: fmt.Sprintf("dxctl %s get 42", n.Singular)}}
	detail := ""
	if len(pdfFn) > 0 {
		examples = append(examples, resource.Example{Cmd: fmt.Sprintf("dxctl %s get 42 -o pdf > %s-42.pdf", n.Singular, n.Singular)})
		detail = "Supports -o pdf to render as PDF, written to stdout instead of table/json/yaml."
	}
	resource.Doc{
		Summary:  fmt.Sprintf("Get %s %s by id", article(n.Singular), n.Singular),
		Detail:   detail,
		Examples: examples,
	}.Apply(cmd)
	return cmd
}

// newListCmd builds a noun's `list` command, rendering with the noun's own
// columns. Nouns whose list takes flags (--all, --inactive) declare the flag
// on the returned command and read it inside fn.
func newListCmd(n resource.Noun, fn listFunc) *cobra.Command {
	return newTableCmd(n, "list", resource.Doc{Summary: fmt.Sprintf("List %s", n.Plural)}, n.Columns, fn)
}

// newTableCmd is newListCmd for any other no-argument verb that prints a
// list (e.g. `bank-statement unreconciled`, which lists ledger transactions
// and so renders with that noun's columns instead of its own).
func newTableCmd(n resource.Noun, verb string, doc resource.Doc, cols []resource.Column, fn listFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:  verb,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := dialRun(cmd)
			if err != nil {
				return err
			}
			defer r.conn.Close()
			items, err := fn(r)
			if err != nil {
				return err
			}
			return output.PrintList(cmd.OutOrStdout(), flagOutput, items, cols)
		},
	}
	withDefaults(doc, n, verb, "").Apply(cmd)
	return cmd
}

// newNoArgCmd builds a verb that takes no positional argument and returns
// one object: create, post, trigger, note, ... Everything it needs comes
// from flags, which the noun file declares on the returned command.
func newNoArgCmd(n resource.Noun, verb string, doc resource.Doc, fn noArgFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:  verb,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := dialRun(cmd)
			if err != nil {
				return err
			}
			defer r.conn.Close()
			obj, err := fn(r)
			if err != nil {
				return err
			}
			return output.PrintOne(cmd.OutOrStdout(), flagOutput, obj, n.Columns)
		},
	}
	withDefaults(doc, n, verb, "").Apply(cmd)
	return cmd
}

// newCreateCmd is newNoArgCmd for the `create` verb.
func newCreateCmd(n resource.Noun, doc resource.Doc, fn noArgFunc) *cobra.Command {
	return newNoArgCmd(n, "create", doc, fn)
}

// newMutateCmd builds a single-RPC, id-in/object-out verb: deactivate,
// send, cancel, accept, decline, void, reverse, ... Extra flags go on the
// returned command and are read inside fn via r.opt*.
func newMutateCmd(n resource.Noun, verb string, doc resource.Doc, fn mutateFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:  verb + " <id>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(n.Singular, args[0])
			if err != nil {
				return err
			}
			r, err := dialRun(cmd)
			if err != nil {
				return err
			}
			defer r.conn.Close()
			obj, err := fn(r, id)
			if err != nil {
				return err
			}
			return output.PrintOne(cmd.OutOrStdout(), flagOutput, obj, n.Columns)
		},
	}
	withDefaults(doc, n, verb, "42").Apply(cmd)
	return cmd
}

// newVersionedMutateCmd is newMutateCmd plus --resource-version, for verbs
// backed by an RPC that accepts the optimistic-concurrency precondition
// (update, deactivate, status transitions).
func newVersionedMutateCmd(n resource.Noun, verb string, doc resource.Doc, fn versionedMutateFunc) *cobra.Command {
	var resourceVersion int64
	cmd := newMutateCmd(n, verb, doc, func(r run, id int64) (proto.Message, error) {
		return fn(r, id, resourceVersion)
	})
	addResourceVersionFlag(cmd, &resourceVersion)
	cmd.Example += fmt.Sprintf("\n  # only if nobody has changed it since you read version 3\n  dxctl %s %s 42 --resource-version 3", n.Singular, verb)
	return cmd
}
