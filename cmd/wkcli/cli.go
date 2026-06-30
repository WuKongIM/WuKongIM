package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	benchcmd "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/bench"
	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
	contextcmd "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/context"
	nodeopscmd "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/nodeops"
	simcmd "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/sim"
	topcmd "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/top"
	"github.com/spf13/cobra"
)

const (
	exitOK          = command.ExitOK
	exitConfig      = command.ExitConfig
	exitUnavailable = command.ExitUnavailable
	exitInternal    = command.ExitInternal
)

func run(args []string) int {
	return runWithIO(args, os.Stdout, os.Stderr)
}

func runWithIO(args []string, stdout, stderr io.Writer) int {
	contextDir := contextcmd.DefaultStoreDir()
	deps := command.Deps{Stdout: stdout, Stderr: stderr, ContextDir: &contextDir}
	cmd := newRootCommand(deps, defaultCommandFactories())
	cmd.SetArgs(args)
	return executeCommand(cmd, stderr)
}

func executeCommand(cmd *cobra.Command, stderr io.Writer) int {
	if err := cmd.Execute(); err != nil {
		var exit command.Exit
		if errors.As(err, &exit) {
			if exit.Message != "" {
				fmt.Fprintln(stderr, exit.Message)
			}
			return exit.Code
		}
		fmt.Fprintln(stderr, err)
		return exitConfig
	}
	return exitOK
}

func newRootCommand(deps command.Deps, factories []command.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "wkcli",
		Short:         "WuKongIM operational command line",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmd.Help(); err != nil {
				return command.Exit{Code: exitInternal, Message: err.Error()}
			}
			return command.Exit{Code: exitConfig}
		},
	}
	cmd.SetOut(deps.Stdout)
	cmd.SetErr(deps.Stderr)
	if deps.ContextDir != nil {
		cmd.PersistentFlags().StringVar(deps.ContextDir, "context-dir", *deps.ContextDir, "Directory for wkcli context files")
	}
	for _, factory := range factories {
		cmd.AddCommand(factory(deps))
	}
	return cmd
}

func defaultCommandFactories() []command.Factory {
	return []command.Factory{
		contextcmd.NewCommand,
		topcmd.NewCommand,
		benchcmd.NewCommand,
		simcmd.NewCommand,
		nodeopscmd.NewCommand,
	}
}
