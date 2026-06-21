package sim

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
	contextcmd "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/context"
	"github.com/spf13/cobra"
)

var execute = executeConfig
var runSimulation = func(ctx context.Context, cfg Config, status *statusModel) error {
	return (&Runtime{Config: cfg, Status: status}).Run(ctx)
}

// NewCommand builds the sim subcommand skeleton.
func NewCommand(deps command.Deps) *cobra.Command {
	cfg := Config{
		Users:             defaultUsers,
		Groups:            defaultGroups,
		GroupMembers:      defaultGroupMembers,
		RatePerGroup:      defaultRate,
		PayloadSize:       defaultPayloadSize,
		ConnectRate:       defaultConnectRate,
		Concurrency:       defaultConcurrency,
		HeartbeatInterval: defaultHeartbeatInterval,
		HeartbeatTimeout:  defaultHeartbeatTimeout,
		AckTimeout:        defaultAckTimeout,
		OperationTimeout:  defaultOpTimeout,
		UIDPrefix:         defaultUIDPrefix,
		DevicePrefix:      defaultDevicePrefix,
		ChannelPrefix:     defaultChannelPrefix,
		StatusListen:      defaultStatusListen,
		StatusInterval:    defaultStatusInterval,
	}
	cmd := &cobra.Command{
		Use:   "sim",
		Short: "Run a planned WuKongIM single-node cluster simulation",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps.ContextDir != nil {
				cfg.ContextDir = *deps.ContextDir
			}
			resolved, err := resolveContextConfig(cfg)
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			normalized, err := normalizeConfig(resolved)
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			if err := execute(cmd.Context(), deps, normalized); err != nil {
				return command.Exit{Code: command.ExitInternal, Message: err.Error()}
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
	cmd.Flags().DurationVar(&cfg.HeartbeatInterval, "heartbeat-interval", cfg.HeartbeatInterval, "Interval between simulated client heartbeat PING frames")
	cmd.Flags().DurationVar(&cfg.HeartbeatTimeout, "heartbeat-timeout", cfg.HeartbeatTimeout, "Timeout for one simulated client heartbeat PING")
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

func resolveContextConfig(cfg Config) (Config, error) {
	if len(splitValues(cfg.Servers)) > 0 || len(splitValues(cfg.Gateways)) > 0 {
		return cfg, nil
	}
	storeDir := strings.TrimSpace(cfg.ContextDir)
	if storeDir == "" {
		storeDir = contextcmd.DefaultStoreDir()
	}
	store := contextcmd.NewStore(storeDir)
	name := strings.TrimSpace(cfg.ContextName)
	if name == "" {
		current, err := store.Current()
		if err != nil {
			return Config{}, err
		}
		name = current
	}
	if name == "" {
		return cfg, nil
	}
	saved, err := store.Load(name)
	if err != nil {
		return Config{}, err
	}
	cfg.ContextName = name
	cfg.Servers = append([]string(nil), saved.Servers...)
	return cfg, nil
}

func executeConfig(ctx context.Context, deps command.Deps, cfg Config) error {
	status := newStatus(cfg.RunID)
	server := newStatusServer(cfg.StatusListen, status)
	serverCtx, stopServer := context.WithCancel(ctx)
	defer stopServer()
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.start(serverCtx)
	}()

	renderCtx, stopRender := context.WithCancel(ctx)
	renderDone := make(chan struct{})
	go func() {
		defer close(renderDone)
		renderLoop(renderCtx, deps.Stdout, status, cfg)
	}()

	runErr := runSimulation(ctx, cfg, status)
	stopRender()
	<-renderDone
	renderErr := renderSnapshot(deps.Stdout, status.snapshot(), cfg)
	stopServer()

	serverStopErr := waitStatusServer(serverErr)
	if runErr != nil {
		return runErr
	}
	if renderErr != nil {
		return renderErr
	}
	return serverStopErr
}

func renderLoop(ctx context.Context, w io.Writer, status *statusModel, cfg Config) {
	ticker := time.NewTicker(cfg.StatusInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = renderSnapshot(w, status.snapshot(), cfg)
		}
	}
}

func renderSnapshot(w io.Writer, snapshot Snapshot, cfg Config) error {
	if cfg.JSON {
		return renderJSON(w, snapshot)
	}
	return renderHuman(w, snapshot)
}

func waitStatusServer(errCh <-chan error) error {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case err := <-errCh:
		return err
	case <-timer.C:
		return fmt.Errorf("status server did not stop")
	}
}
