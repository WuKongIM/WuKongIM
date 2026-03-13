package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(contextCmd)
	contextCmd.AddCommand(contextSetCmd)
	contextCmd.AddCommand(contextShowCmd)
	contextCmd.AddCommand(contextUseCmd)

	contextSetCmd.Flags().String("server", "", "server address (e.g. http://localhost:5001)")
	contextSetCmd.Flags().String("token", "", "auth token")
	contextSetCmd.Flags().String("name", "", "context name (default: current context)")
}

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Manage connection contexts",
}

var contextSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set server address and token for a context",
	Example: `  wkcli context set --server http://localhost:5001
  wkcli context set --server http://localhost:5001 --token mytoken
  wkcli context set --server http://prod:5001 --name production`,
	RunE: func(cmd *cobra.Command, args []string) error {
		server, _ := cmd.Flags().GetString("server")
		token, _ := cmd.Flags().GetString("token")
		name, _ := cmd.Flags().GetString("name")

		if server == "" && token == "" {
			return fmt.Errorf("at least one of --server or --token is required")
		}

		if name == "" {
			name = cfg.Current
		}

		ctx, ok := cfg.Contexts[name]
		if !ok {
			ctx = &Context{}
			cfg.Contexts[name] = ctx
		}
		if server != "" {
			ctx.Server = server
		}
		if token != "" {
			ctx.Token = token
		}

		// If this is the first context, make it current.
		if cfg.Current == "" {
			cfg.Current = name
		}

		if err := saveConfig(); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		printSuccess(fmt.Sprintf("Context '%s' updated", name))
		if server != "" {
			printInfo("Server", server)
		}
		if token != "" {
			printInfo("Token", token)
		}
		return nil
	},
}

var contextShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current context",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("%sCurrent context:%s %s\n", colorBold, colorReset, cfg.Current)

		if len(cfg.Contexts) == 0 {
			fmt.Println("\nNo contexts configured. Run: wkcli context set --server <url>")
			return nil
		}

		fmt.Println()
		headers := []string{"Name", "Server", "Token"}
		var rows [][]string
		for name, ctx := range cfg.Contexts {
			marker := "  "
			if name == cfg.Current {
				marker = "* "
			}
			tokenDisplay := ""
			if ctx.Token != "" {
				if len(ctx.Token) > 8 {
					tokenDisplay = ctx.Token[:8] + "..."
				} else {
					tokenDisplay = ctx.Token
				}
			}
			rows = append(rows, []string{marker + name, ctx.Server, tokenDisplay})
		}
		printTable(headers, rows)
		return nil
	},
}

var contextUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Switch to a different context",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if _, ok := cfg.Contexts[name]; !ok {
			return fmt.Errorf("context '%s' not found. Available contexts:", name)
		}
		cfg.Current = name
		if err := saveConfig(); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		printSuccess(fmt.Sprintf("Switched to context '%s'", name))
		printInfo("Server", cfg.Contexts[name].Server)
		return nil
	},
}
