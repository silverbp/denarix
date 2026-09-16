// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package resource

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// Example is one literal command line in a Doc's Example block, with an
// optional one-line caption rendered as a "# caption" comment above it.
type Example struct {
	Desc string
	Cmd  string
}

// Doc is the uniform shape every dxctl command's help text is built
// from — generic verbs (get/list/deactivate/...) and hand-written ones
// (create/update/post/...) alike render through Apply, so both --help
// output and `dxctl commands --json` read identically across the whole
// command tree.
type Doc struct {
	Summary  string // one sentence, becomes cobra.Command.Short
	Detail   string // optional extra paragraph, appended to Long
	Examples []Example
}

// Apply sets cmd.Short/Long/Example from d.
func (d Doc) Apply(cmd *cobra.Command) {
	cmd.Short = d.Summary

	var long strings.Builder
	long.WriteString(wordWrap(d.Summary, 78))
	if d.Detail != "" {
		long.WriteString("\n\n")
		long.WriteString(wordWrap(d.Detail, 78))
	}
	cmd.Long = long.String()

	var ex strings.Builder
	for i, e := range d.Examples {
		if i > 0 {
			ex.WriteString("\n")
		}
		if e.Desc != "" {
			fmt.Fprintf(&ex, "  # %s\n", e.Desc)
		}
		fmt.Fprintf(&ex, "  %s\n", e.Cmd)
	}
	cmd.Example = strings.TrimRight(ex.String(), "\n")
}

// wordWrap hard-wraps s to width, treating existing blank lines as
// paragraph breaks and collapsing all other whitespace within a paragraph.
func wordWrap(s string, width int) string {
	paragraphs := strings.Split(s, "\n\n")
	for i, p := range paragraphs {
		paragraphs[i] = wrapParagraph(strings.Join(strings.Fields(p), " "), width)
	}
	return strings.Join(paragraphs, "\n\n")
}

func wrapParagraph(s string, width int) string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	lineLen := 0
	for i, w := range words {
		if i > 0 {
			if lineLen+1+len(w) > width {
				b.WriteString("\n")
				lineLen = 0
			} else {
				b.WriteString(" ")
				lineLen++
			}
		}
		b.WriteString(w)
		lineLen += len(w)
	}
	return b.String()
}
