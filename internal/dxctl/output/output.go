// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

// Package output renders resources as a table, JSON, or YAML — dxctl's
// -o flag, matching kubectl.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"

	"github.com/silverbp/denarix/internal/dxctl/resource"
)

const (
	FormatTable = "table"
	FormatJSON  = "json"
	FormatYAML  = "yaml"
	// FormatPDF is handled specially by commands that support it (writing
	// raw bytes straight to stdout for shell redirection) — never routed
	// through PrintOne/PrintList, which only know table/json/yaml.
	FormatPDF = "pdf"
)

func PrintOne(w io.Writer, format string, v proto.Message, cols []resource.Column) error {
	switch format {
	case FormatJSON:
		return printJSON(w, protoToAny(v))
	case FormatYAML:
		return printYAML(w, protoToAny(v))
	default:
		return printTable(w, []proto.Message{v}, cols)
	}
}

func PrintList(w io.Writer, format string, items []proto.Message, cols []resource.Column) error {
	switch format {
	case FormatJSON:
		var all []any
		for _, it := range items {
			all = append(all, protoToAny(it))
		}
		return printJSON(w, all)
	case FormatYAML:
		var all []any
		for _, it := range items {
			all = append(all, protoToAny(it))
		}
		return printYAML(w, all)
	default:
		return printTable(w, items, cols)
	}
}

func protoToAny(v proto.Message) any {
	data, err := protojson.Marshal(v)
	if err != nil {
		return nil
	}
	var m any
	_ = json.Unmarshal(data, &m)
	return m
}

func printJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

func printYAML(w io.Writer, v any) error {
	return yaml.NewEncoder(w).Encode(v)
}

func printTable(w io.Writer, items []proto.Message, cols []resource.Column) error {
	if len(cols) == 0 {
		return fmt.Errorf("this resource does not define table columns; use -o json or -o yaml")
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)

	headers := make([]string, len(cols))
	for i, c := range cols {
		headers[i] = c.Header
	}
	fmt.Fprintln(tw, strings.Join(headers, "\t"))

	for _, it := range items {
		row := make([]string, len(cols))
		for i, c := range cols {
			row[i] = c.Value(it)
		}
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	return tw.Flush()
}
