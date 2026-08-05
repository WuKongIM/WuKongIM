package main

import (
	"io"

	"github.com/spf13/cobra"
)

func newSoakCommand(stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "soak",
		Short: "Run long-lived stability workloads against an existing cluster",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := cmd.Help(); err != nil {
				return commandExit{code: exitInternal, message: err.Error()}
			}
			return commandExit{code: exitConfig}
		},
	}
	cmd.AddCommand(newSoakChatLifecycleCommand(stderr))
	return cmd
}
