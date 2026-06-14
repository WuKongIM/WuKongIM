package top

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
	contextcmd "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/context"
	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
	"github.com/spf13/cobra"
)

// NewCommand builds the top subcommand.
func NewCommand(deps command.Deps) *cobra.Command {
	cfg := config{}
	cmd := &cobra.Command{
		Use:   "top",
		Short: "Inspect live WuKongIM runtime pressure",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.ContextDir = contextDirFromDeps(deps)
			normalized, err := normalizeConfig(cfg)
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			normalized.Servers, err = resolveServers(normalized)
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			if normalized.Once {
				err = runOnce(cmd.Context(), deps.Stdout, normalized)
			} else {
				err = runInteractive(cmd.Context(), deps.Stdout, normalized)
			}
			if err != nil {
				return command.Exit{Code: command.ExitInternal, Message: err.Error()}
			}
			return nil
		},
	}
	cmd.SetOut(deps.Stdout)
	cmd.SetErr(deps.Stderr)
	cmd.Flags().StringVar(&cfg.ContextName, "context", "", "Named wkcli context to read server addresses from")
	cmd.Flags().StringArrayVar(&cfg.Servers, "server", nil, "WuKongIM HTTP API server address; repeat or use comma-separated values")
	cmd.Flags().DurationVar(&cfg.Window, "window", 0, "Top aggregation window")
	cmd.Flags().StringVar(&cfg.View, "view", "", "Top snapshot view")
	cmd.Flags().IntVar(&cfg.Limit, "limit", 0, "Maximum pressure items to request")
	cmd.Flags().DurationVar(&cfg.Interval, "interval", 0, "Interactive refresh interval")
	cmd.Flags().BoolVar(&cfg.Once, "once", false, "Fetch and render one snapshot")
	cmd.Flags().BoolVar(&cfg.JSON, "json", false, "Render JSON output")
	cmd.Flags().IntVar(&cfg.MaxRefresh, "max-refresh", 0, "Maximum refreshes before exiting; 0 refreshes until interrupted")
	return cmd
}

func contextDirFromDeps(deps command.Deps) string {
	if deps.ContextDir == nil {
		return ""
	}
	return *deps.ContextDir
}

func resolveServers(cfg config) ([]string, error) {
	if len(cfg.Servers) > 0 {
		return dedupeValues(splitValues(cfg.Servers)), nil
	}
	store := contextcmd.NewStore(cfg.ContextDir)
	name := strings.TrimSpace(cfg.ContextName)
	if name == "" {
		current, err := store.Current()
		if err != nil {
			return nil, err
		}
		name = current
	}
	if name == "" {
		return nil, fmt.Errorf("no server or current context selected")
	}
	ctx, err := store.Load(name)
	if err != nil {
		return nil, err
	}
	return dedupeValues(splitValues(ctx.Servers)), nil
}

func runOnce(ctx context.Context, w io.Writer, cfg config) error {
	if len(cfg.Servers) == 0 {
		return fmt.Errorf("at least one --server or context server is required")
	}
	c := newClient()
	snapshots := make([]accessapi.TopSnapshot, 0, len(cfg.Servers))
	for _, server := range cfg.Servers {
		snapshot, err := c.snapshot(ctx, server, cfg)
		if err != nil {
			return err
		}
		snapshots = append(snapshots, snapshot)
	}
	agg := aggregate(snapshots)
	if cfg.JSON {
		return renderJSON(w, agg)
	}
	return renderHuman(w, agg)
}

func splitValues(values []string) []string {
	var split []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				split = append(split, part)
			}
		}
	}
	return split
}

func dedupeValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	deduped := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		deduped = append(deduped, value)
	}
	return deduped
}
