package main

import (
	"fmt"

	"github.com/coder/serpent"
)

// Version is set during build using ldflags
var Version = "dev"

func versionCmd() *serpent.Command {
	return &serpent.Command{
		Use:  "version",
		Long: "Print build version information",
		Handler: func(inv *serpent.Invocation) error {
			fmt.Fprintf(inv.Stdout, "aicommit %s\n", Version)
			return nil
		},
	}
}
