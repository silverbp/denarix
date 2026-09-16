// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/silverbp/denarix/internal/dxctl/resource"
	"github.com/silverbp/denarix/internal/version"
)

func newVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:  "version",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "dxctl %s\n", version.Version)
			fmt.Fprintf(w, "  git commit: %s\n", version.GitCommit)
			fmt.Fprintf(w, "  built:      %s\n", version.BuildDate)
			fmt.Fprintf(w, "  go version: %s\n", runtime.Version())
			fmt.Fprintf(w, "  platform:   %s/%s\n", runtime.GOOS, runtime.GOARCH)
			return nil
		},
	}
	resource.Doc{
		Summary:  "Print version and build information",
		Detail:   "Same version `dxctl --version` prints, plus the git commit, build date, Go version, and platform it was built with.",
		Examples: []resource.Example{{Cmd: "dxctl version"}},
	}.Apply(cmd)
	return cmd
}
