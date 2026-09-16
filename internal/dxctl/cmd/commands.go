// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"

	"github.com/silverbp/denarix/internal/dxctl/output"
	"github.com/silverbp/denarix/internal/dxctl/resource"
)

type manifestFlag struct {
	Name      string `json:"name" yaml:"name"`
	Shorthand string `json:"shorthand,omitempty" yaml:"shorthand,omitempty"`
	Type      string `json:"type" yaml:"type"`
	Default   string `json:"default" yaml:"default"`
	Usage     string `json:"usage" yaml:"usage"`
	Required  bool   `json:"required,omitempty" yaml:"required,omitempty"`
}

type manifestCommand struct {
	Path     []string       `json:"path" yaml:"path"`
	Use      string         `json:"use" yaml:"use"`
	Aliases  []string       `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	Short    string         `json:"short" yaml:"short"`
	Long     string         `json:"long,omitempty" yaml:"long,omitempty"`
	Example  string         `json:"example,omitempty" yaml:"example,omitempty"`
	Runnable bool           `json:"runnable" yaml:"runnable"`
	Flags    []manifestFlag `json:"flags,omitempty" yaml:"flags,omitempty"`
}

type manifest struct {
	GeneratedAt string            `json:"generated_at" yaml:"generated_at"`
	GlobalFlags []manifestFlag    `json:"global_flags" yaml:"global_flags"`
	Commands    []manifestCommand `json:"commands" yaml:"commands"`
}

func newCommandsCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:     "commands",
		Aliases: []string{"manifest"},
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			m := buildManifest(cmd.Root())
			format := flagOutput
			if asJSON {
				format = output.FormatJSON
			}
			switch format {
			case output.FormatJSON:
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				enc.SetEscapeHTML(false)
				return enc.Encode(m)
			case output.FormatYAML:
				return yaml.NewEncoder(cmd.OutOrStdout()).Encode(m)
			default:
				w := cmd.OutOrStdout()
				for _, c := range m.Commands {
					suffix := ""
					if !c.Runnable {
						suffix = " (group)"
					}
					fmt.Fprintf(w, "dxctl %s%s\n", strings.Join(c.Path, " "), suffix)
				}
				return nil
			}
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "shorthand for -o json")
	resource.Doc{
		Summary: "Print the full command tree as structured data",
		Detail: "Walks every command, verb, and flag dxctl supports and prints it as " +
			"one document - useful for an AI agent (or any script) to learn the CLI's " +
			"whole capability surface without running --help on every command " +
			"individually.",
		Examples: []resource.Example{
			{Cmd: "dxctl commands --json"},
			{Cmd: "dxctl commands -o yaml"},
			{Desc: "human-readable tree", Cmd: "dxctl commands"},
		},
	}.Apply(cmd)
	return cmd
}

func buildManifest(root *cobra.Command) manifest {
	m := manifest{GeneratedAt: time.Now().UTC().Format(time.RFC3339)}
	root.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" {
			return
		}
		m.GlobalFlags = append(m.GlobalFlags, flagInfo(f))
	})
	for _, c := range root.Commands() {
		walkCommand(c, nil, &m)
	}
	return m
}

func walkCommand(c *cobra.Command, parentPath []string, m *manifest) {
	if c.Name() == "completion" || c.Name() == "help" || c.Hidden {
		return
	}
	path := append(append([]string{}, parentPath...), c.Name())

	mc := manifestCommand{
		Path:     path,
		Use:      c.Use,
		Aliases:  c.Aliases,
		Short:    c.Short,
		Long:     c.Long,
		Example:  c.Example,
		Runnable: c.Runnable(),
	}
	c.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" {
			return
		}
		mc.Flags = append(mc.Flags, flagInfo(f))
	})
	m.Commands = append(m.Commands, mc)

	for _, sub := range c.Commands() {
		walkCommand(sub, path, m)
	}
}

func flagInfo(f *pflag.Flag) manifestFlag {
	_, required := f.Annotations[cobra.BashCompOneRequiredFlag]
	return manifestFlag{
		Name:      f.Name,
		Shorthand: f.Shorthand,
		Type:      f.Value.Type(),
		Default:   f.DefValue,
		Usage:     f.Usage,
		Required:  required,
	}
}
