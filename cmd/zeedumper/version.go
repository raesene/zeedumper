package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Build metadata, injected at link time:
//
//	go build -ldflags "-X main.version=$(git describe --tags) \
//	  -X main.commit=$(git rev-parse --short HEAD) -X main.date=$(date -u +%FT%TZ)"
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "zeedumper %s (commit %s, built %s)\n", version, commit, date)
		},
	}
}
