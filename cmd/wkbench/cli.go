package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

type commandExit struct {
	code    int
	message string
}

func (e commandExit) Error() string {
	return e.message
}

func newRootCommand(stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "wkbench",
		Short:         "Black-box benchmark driver for WuKongIM clusters",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmd.Help(); err != nil {
				return commandExit{code: exitInternal, message: err.Error()}
			}
			return commandExit{code: exitConfig}
		},
	}
	cmd.SetOut(stderr)
	cmd.SetErr(stderr)
	cmd.AddCommand(
		newRunCommand(stderr),
		newWorkerCommand(stderr),
		newValidateCommand(stderr),
		newDoctorCommand(stderr),
		newDevSimCommand(stderr),
		newCapacityCommand(stderr),
		newMetricsCommand(stderr),
		newReportCommand(),
	)
	return cmd
}

func executeRoot(args []string, stderr io.Writer) int {
	cmd := newRootCommand(stderr)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		var exit commandExit
		if errors.As(err, &exit) {
			if exit.message != "" {
				fmt.Fprintln(stderr, exit.message)
			}
			return exit.code
		}
		fmt.Fprintln(stderr, err)
		return exitConfig
	}
	return 0
}

func exitCodeError(code int) error {
	if code == 0 {
		return nil
	}
	return commandExit{code: code}
}

func exitConfigError(err error) error {
	if err == nil {
		return nil
	}
	return commandExit{code: exitConfig, message: err.Error()}
}

func newReportCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "report",
		Short: "Render standalone benchmark reports",
		RunE: func(cmd *cobra.Command, args []string) error {
			return commandExit{code: exitInternal, message: "report is not implemented yet"}
		},
	}
}
