package contextcmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
	"github.com/spf13/cobra"
)

// NewCommand builds the context management command tree.
func NewCommand(deps command.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "context",
		Aliases: []string{"ctx"},
		Short:   "Manage named WuKongIM server contexts",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmd.Help(); err != nil {
				return command.Exit{Code: command.ExitInternal, Message: err.Error()}
			}
			return command.Exit{Code: command.ExitConfig}
		},
	}
	cmd.AddCommand(
		newAddCommand(deps),
		newListCommand(deps),
		newSelectCommand(deps),
		newShowCommand(deps),
		newRemoveCommand(deps),
		newCurrentCommand(deps),
	)
	return cmd
}

func newAddCommand(deps command.Deps) *cobra.Command {
	var serverValues []string
	var description string
	var selectAfterSave bool
	cmd := &cobra.Command{
		Use:   "add NAME",
		Short: "Add or update a named server context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := storeFromDeps(deps)
			ctx := Context{
				Name:        args[0],
				Description: strings.TrimSpace(description),
				Servers:     parseServerValues(serverValues),
			}
			if err := store.Save(ctx); err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			fmt.Fprintf(deps.Stdout, "saved context %s\n", ctx.Name)
			if selectAfterSave {
				if err := store.Select(ctx.Name); err != nil {
					return command.Exit{Code: command.ExitConfig, Message: err.Error()}
				}
				fmt.Fprintf(deps.Stdout, "selected context %s\n", ctx.Name)
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&serverValues, "server", nil, "WuKongIM HTTP API server address; repeat or use comma-separated values")
	cmd.Flags().StringVar(&description, "description", "", "Optional context description")
	cmd.Flags().BoolVar(&selectAfterSave, "select", false, "Select this context after saving it")
	return cmd
}

func newListCommand(deps command.Deps) *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List saved contexts",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store := storeFromDeps(deps)
			contexts, err := store.List()
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			if len(contexts) == 0 {
				fmt.Fprintln(deps.Stdout, "no contexts")
				return nil
			}
			current, err := store.Current()
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			for _, ctx := range contexts {
				marker := " "
				if ctx.Name == current {
					marker = "*"
				}
				fmt.Fprintf(deps.Stdout, "%s %s\t%s\t%s\n", marker, ctx.Name, strings.Join(ctx.Servers, ","), ctx.Description)
			}
			return nil
		},
	}
}

func newSelectCommand(deps command.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "select NAME",
		Short: "Select the default context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := storeFromDeps(deps)
			if err := store.Select(args[0]); err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			fmt.Fprintf(deps.Stdout, "selected context %s\n", args[0])
			return nil
		},
	}
}

func newShowCommand(deps command.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "show [NAME]",
		Short: "Show a saved context",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := storeFromDeps(deps)
			name := ""
			if len(args) == 1 {
				name = args[0]
			} else {
				current, err := store.Current()
				if err != nil {
					return command.Exit{Code: command.ExitConfig, Message: err.Error()}
				}
				name = current
			}
			if name == "" {
				return command.Exit{Code: command.ExitConfig, Message: "no current context selected"}
			}
			ctx, err := store.Load(name)
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			printContext(deps.Stdout, ctx)
			return nil
		},
	}
}

func newRemoveCommand(deps command.Deps) *cobra.Command {
	return &cobra.Command{
		Use:     "rm NAME",
		Aliases: []string{"remove"},
		Short:   "Remove a saved context",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := storeFromDeps(deps)
			removedCurrent, err := store.Remove(args[0])
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			fmt.Fprintf(deps.Stdout, "removed context %s\n", args[0])
			if removedCurrent {
				fmt.Fprintln(deps.Stdout, "cleared current context")
			}
			return nil
		},
	}
}

func newCurrentCommand(deps command.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Print the current context name",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			current, err := storeFromDeps(deps).Current()
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			if current == "" {
				return command.Exit{Code: command.ExitConfig, Message: "no current context selected"}
			}
			fmt.Fprintln(deps.Stdout, current)
			return nil
		},
	}
}

func storeFromDeps(deps command.Deps) *Store {
	if deps.ContextDir == nil || strings.TrimSpace(*deps.ContextDir) == "" {
		return NewStore(DefaultStoreDir())
	}
	return NewStore(*deps.ContextDir)
}

func printContext(w io.Writer, ctx Context) {
	fmt.Fprintf(w, "name: %s\n", ctx.Name)
	if ctx.Description != "" {
		fmt.Fprintf(w, "description: %s\n", ctx.Description)
	}
	fmt.Fprintln(w, "servers:")
	for _, server := range ctx.Servers {
		fmt.Fprintf(w, "  - %s\n", server)
	}
}
