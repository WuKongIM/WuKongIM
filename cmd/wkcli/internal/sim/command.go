package sim

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
	"github.com/spf13/cobra"
)

var execute = executeConfig

// NewCommand builds the sim subcommand skeleton.
func NewCommand(deps command.Deps) *cobra.Command {
	cfg := Config{
		Users:            defaultUsers,
		Groups:           defaultGroups,
		GroupMembers:     defaultGroupMembers,
		RatePerGroup:     defaultRate,
		PayloadSize:      defaultPayloadSize,
		ConnectRate:      defaultConnectRate,
		Concurrency:      defaultConcurrency,
		AckTimeout:       defaultAckTimeout,
		OperationTimeout: defaultOpTimeout,
		UIDPrefix:        defaultUIDPrefix,
		DevicePrefix:     defaultDevicePrefix,
		ChannelPrefix:    defaultChannelPrefix,
		StatusListen:     defaultStatusListen,
		StatusInterval:   defaultStatusInterval,
	}
	cmd := &cobra.Command{
		Use:   "sim",
		Short: "Run a planned WuKongIM single-node cluster simulation",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps.ContextDir != nil {
				cfg.ContextDir = *deps.ContextDir
			}
			normalized, err := normalizeConfig(cfg)
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			if err := execute(cmd.Context(), deps, normalized); err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			return nil
		},
	}
	cmd.SetOut(deps.Stdout)
	cmd.SetErr(deps.Stderr)
	cmd.Flags().StringArrayVar(&cfg.Servers, "server", nil, "WuKongIM HTTP API server address; repeat or use comma-separated values")
	cmd.Flags().StringVar(&cfg.ContextName, "context", "", "Named wkcli context to use")
	cmd.Flags().StringArrayVar(&cfg.Gateways, "gateway", nil, "WKProto TCP gateway address; repeat or use comma-separated values")
	cmd.Flags().StringVar(&cfg.BenchToken, "bench-token", "", "Optional bearer token for bench-capable APIs")
	cmd.Flags().IntVar(&cfg.Users, "users", cfg.Users, "Number of simulated users")
	cmd.Flags().IntVar(&cfg.Groups, "groups", cfg.Groups, "Number of simulated group channels")
	cmd.Flags().IntVar(&cfg.GroupMembers, "group-members", cfg.GroupMembers, "Number of simulated members per group")
	cmd.Flags().StringVar(&cfg.RatePerGroup, "rate", cfg.RatePerGroup, "Per-group message rate such as 0.2/s")
	cmd.Flags().StringVar(&cfg.PayloadSize, "payload-size", cfg.PayloadSize, "Generated payload size such as 128B, 1KB, or 1MiB")
	cmd.Flags().IntVar(&cfg.ConnectRate, "connect-rate", cfg.ConnectRate, "Maximum simulated client connects per second")
	cmd.Flags().IntVar(&cfg.Concurrency, "concurrency", cfg.Concurrency, "Maximum concurrent simulation operations")
	cmd.Flags().DurationVar(&cfg.AckTimeout, "ack-timeout", cfg.AckTimeout, "SENDACK wait timeout")
	cmd.Flags().DurationVar(&cfg.OperationTimeout, "operation-timeout", cfg.OperationTimeout, "Setup operation timeout")
	cmd.Flags().StringVar(&cfg.RunID, "run-id", "", "Run identifier used to scope generated identities")
	cmd.Flags().StringVar(&cfg.UIDPrefix, "uid-prefix", cfg.UIDPrefix, "Prefix for generated user IDs")
	cmd.Flags().StringVar(&cfg.DevicePrefix, "device-prefix", cfg.DevicePrefix, "Prefix for generated device IDs")
	cmd.Flags().StringVar(&cfg.ChannelPrefix, "channel-prefix", cfg.ChannelPrefix, "Prefix for generated group channel IDs")
	cmd.Flags().StringVar(&cfg.StatusListen, "status-listen", cfg.StatusListen, "Local status endpoint listen address")
	cmd.Flags().DurationVar(&cfg.StatusInterval, "status-interval", cfg.StatusInterval, "Status snapshot interval")
	cmd.Flags().DurationVar(&cfg.MaxRuntime, "max-runtime", 0, "Optional maximum simulation runtime")
	cmd.Flags().BoolVar(&cfg.JSON, "json", false, "Print output as JSON")
	return cmd
}

func executeConfig(ctx context.Context, deps command.Deps, cfg Config) error {
	_ = ctx
	_ = cfg
	_, err := fmt.Fprintln(deps.Stdout, "wkcli sim runtime is not implemented yet")
	return err
}
