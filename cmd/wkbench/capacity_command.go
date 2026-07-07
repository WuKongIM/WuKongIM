package main

import (
	"io"

	"github.com/WuKongIM/WuKongIM/internal/bench/capacity"
	"github.com/WuKongIM/WuKongIM/internal/bench/messageevent"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func newCapacityCommand(stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "capacity",
		Short: "Run targeted capacity searches against an existing cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmd.Help(); err != nil {
				return commandExit{code: exitInternal, message: err.Error()}
			}
			return commandExit{code: exitConfig}
		},
	}
	cmd.AddCommand(
		newCapacitySendCommand(stderr),
		newCapacityHotChannelCommand(stderr),
		newCapacityActivateChannelsCommand(stderr),
		newCapacityMessageEventCommand(stderr),
	)
	return cmd
}

func newCapacitySendCommand(stderr io.Writer) *cobra.Command {
	cfg := capacity.DefaultConfig()
	var apiCSV string
	var gatewayCSV string
	cmd := &cobra.Command{
		Use:   "send",
		Short: "Search maximum stable ingress send QPS",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := finalizeCapacitySendConfig(&cfg, apiCSV, gatewayCSV); err != nil {
				return exitConfigError(err)
			}
			return exitCodeError(runCapacitySendConfig(cfg, stderr))
		},
	}
	bindCapacitySendFlags(cmd.Flags(), &cfg, &apiCSV, &gatewayCSV)
	return cmd
}

func newCapacityHotChannelCommand(stderr io.Writer) *cobra.Command {
	cfg := capacity.DefaultHotChannelConfig()
	var apiCSV string
	var gatewayCSV string
	cmd := &cobra.Command{
		Use:   "hot-channel",
		Short: "Search send capacity for one hot group channel",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := finalizeCapacityHotChannelConfig(&cfg, apiCSV, gatewayCSV); err != nil {
				return exitConfigError(err)
			}
			return exitCodeError(runCapacityHotChannelConfig(cfg, stderr))
		},
	}
	bindCapacityHotChannelFlags(cmd.Flags(), &cfg, &apiCSV, &gatewayCSV)
	return cmd
}

func newCapacityActivateChannelsCommand(stderr io.Writer) *cobra.Command {
	cfg := capacity.DefaultActivateChannelsConfig()
	var apiCSV string
	var gatewayCSV string
	cmd := &cobra.Command{
		Use:   "activate-channels",
		Short: "Activate and hold a fixed number of group channels",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := finalizeCapacityActivateChannelsConfig(&cfg, apiCSV, gatewayCSV); err != nil {
				return exitConfigError(err)
			}
			return exitCodeError(runCapacityActivateChannelsConfig(cfg, stderr))
		},
	}
	bindCapacityActivateChannelsFlags(cmd.Flags(), &cfg, &apiCSV, &gatewayCSV)
	return cmd
}

func newCapacityMessageEventCommand(stderr io.Writer) *cobra.Command {
	cfg := messageevent.DefaultConfig()
	var apiCSV string
	cmd := &cobra.Command{
		Use:   "message-event",
		Short: "Run fixed message event stream pressure against an existing cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := finalizeCapacityMessageEventConfig(&cfg, apiCSV); err != nil {
				return exitConfigError(err)
			}
			return exitCodeError(runCapacityMessageEventConfig(cfg, stderr))
		},
	}
	bindCapacityMessageEventFlags(cmd.Flags(), &cfg, &apiCSV)
	return cmd
}

func bindCapacitySendFlags(flags *pflag.FlagSet, cfg *capacity.Config, apiCSV *string, gatewayCSV *string) {
	flags.StringVar(apiCSV, "api", "", "comma-separated target HTTP API base addresses")
	flags.StringVar(gatewayCSV, "gateway", "", "optional comma-separated WKProto TCP gateway addresses")
	flags.StringVar(&cfg.BenchToken, "bench-token", cfg.BenchToken, "optional bearer token for bench API routes")
	flags.StringVar(&cfg.Profile, "profile", cfg.Profile, "traffic profile: person, group, or mixed")
	flags.Float64Var(&cfg.StartQPS, "start-qps", cfg.StartQPS, "first offered ingress QPS")
	flags.Float64Var(&cfg.MaxQPS, "max-qps", cfg.MaxQPS, "maximum offered ingress QPS")
	flags.Float64Var(&cfg.StepFactor, "step-factor", cfg.StepFactor, "ramp multiplier after passing attempts")
	flags.DurationVar(&cfg.Duration, "duration", cfg.Duration, "measured run duration per attempt")
	flags.DurationVar(&cfg.Warmup, "warmup", cfg.Warmup, "warmup duration per attempt")
	flags.DurationVar(&cfg.Cooldown, "cooldown", cfg.Cooldown, "cooldown duration per attempt")
	flags.DurationVar(&cfg.StableP99, "stable-p99", cfg.StableP99, "maximum stable sendack p99 latency")
	flags.Float64Var(&cfg.MinActualRatio, "min-actual-ratio", cfg.MinActualRatio, "minimum actual/offered QPS ratio")
	flags.Float64Var(&cfg.MaxSendackErrorRate, "max-sendack-error-rate", cfg.MaxSendackErrorRate, "maximum allowed sendack error rate")
	flags.Float64Var(&cfg.MaxConnectErrorRate, "max-connect-error-rate", cfg.MaxConnectErrorRate, "maximum allowed connect error rate")
	flags.BoolVar(&cfg.BinarySearch, "binary-search", cfg.BinarySearch, "enable binary search after first failed ramp attempt")
	flags.Float64Var(&cfg.BinarySearchMinDeltaRatio, "binary-search-min-delta-ratio", cfg.BinarySearchMinDeltaRatio, "binary search stop ratio")
	flags.IntVar(&cfg.GroupMembers, "group-members", cfg.GroupMembers, "members per generated group channel")
	flags.StringVar(&cfg.ReportDir, "report-dir", cfg.ReportDir, "capacity report output directory")
}

func bindCapacityHotChannelFlags(flags *pflag.FlagSet, cfg *capacity.HotChannelConfig, apiCSV *string, gatewayCSV *string) {
	flags.StringVar(apiCSV, "api", "", "comma-separated target HTTP API base addresses")
	flags.StringVar(gatewayCSV, "gateway", "", "optional comma-separated WKProto TCP gateway addresses")
	flags.StringVar(&cfg.BenchToken, "bench-token", cfg.BenchToken, "optional bearer token for bench API routes")
	flags.IntVar(&cfg.Senders, "senders", cfg.Senders, "number of online senders fanning into the one hot group channel")
	flags.Float64Var(&cfg.StartQPS, "start-qps", cfg.StartQPS, "first offered ingress QPS")
	flags.Float64Var(&cfg.MaxQPS, "max-qps", cfg.MaxQPS, "maximum offered ingress QPS")
	flags.Float64Var(&cfg.StepFactor, "step-factor", cfg.StepFactor, "ramp multiplier after passing attempts")
	flags.DurationVar(&cfg.Duration, "duration", cfg.Duration, "measured run duration per attempt")
	flags.DurationVar(&cfg.Warmup, "warmup", cfg.Warmup, "warmup duration per attempt")
	flags.DurationVar(&cfg.Cooldown, "cooldown", cfg.Cooldown, "cooldown duration per attempt")
	flags.DurationVar(&cfg.StableP99, "stable-p99", cfg.StableP99, "maximum stable sendack p99 latency")
	flags.Float64Var(&cfg.MinActualRatio, "min-actual-ratio", cfg.MinActualRatio, "minimum actual/offered QPS ratio")
	flags.Float64Var(&cfg.MaxSendackErrorRate, "max-sendack-error-rate", cfg.MaxSendackErrorRate, "maximum allowed sendack error rate")
	flags.Float64Var(&cfg.MaxConnectErrorRate, "max-connect-error-rate", cfg.MaxConnectErrorRate, "maximum allowed connect error rate")
	flags.BoolVar(&cfg.BinarySearch, "binary-search", cfg.BinarySearch, "enable binary search after first failed ramp attempt")
	flags.Float64Var(&cfg.BinarySearchMinDeltaRatio, "binary-search-min-delta-ratio", cfg.BinarySearchMinDeltaRatio, "binary search stop ratio")
	flags.StringVar(&cfg.ReportDir, "report-dir", cfg.ReportDir, "capacity report output directory")
}

func bindCapacityActivateChannelsFlags(flags *pflag.FlagSet, cfg *capacity.ActivateChannelsConfig, apiCSV *string, gatewayCSV *string) {
	flags.StringVar(apiCSV, "api", "", "comma-separated target HTTP API base addresses")
	flags.StringVar(gatewayCSV, "gateway", "", "optional comma-separated WKProto TCP gateway addresses")
	flags.StringVar(&cfg.BenchToken, "bench-token", cfg.BenchToken, "optional bearer token for bench API routes")
	flags.StringVar(&cfg.RunID, "run-id", cfg.RunID, "stable benchmark run identifier")
	flags.IntVar(&cfg.Channels, "channels", cfg.Channels, "number of group channels to activate")
	flags.IntVar(&cfg.Users, "users", cfg.Users, "number of online users prepared for activation")
	flags.IntVar(&cfg.GroupMembers, "group-members", cfg.GroupMembers, "members per generated group channel")
	flags.Float64Var(&cfg.PrepareRatePerSecond, "prepare-rate", cfg.PrepareRatePerSecond, "bench API preparation rate limit per second; 0 means unlimited")
	flags.Float64Var(&cfg.ConnectRatePerSecond, "connect-rate", cfg.ConnectRatePerSecond, "gateway connect attempt rate limit per second; 0 means unlimited")
	flags.IntVar(&cfg.ActivationConcurrency, "activation-concurrency", cfg.ActivationConcurrency, "maximum in-flight SEND operations during activation")
	flags.DurationVar(&cfg.ActivationWindow, "activation-window", cfg.ActivationWindow, "active window used to schedule one SEND per channel")
	flags.DurationVar(&cfg.Hold, "hold", cfg.Hold, "post-activation observation duration")
	flags.DurationVar(&cfg.HoldProbeInterval, "hold-probe-interval", cfg.HoldProbeInterval, "interval between hold runtime snapshots")
	flags.IntVar(&cfg.ProbeBatchSize, "probe-batch-size", cfg.ProbeBatchSize, "generated channels checked per runtime probe batch")
	flags.DurationVar(&cfg.StableP99, "stable-p99", cfg.StableP99, "maximum stable sendack p99 latency")
	flags.Float64Var(&cfg.MaxSendackErrorRate, "max-sendack-error-rate", cfg.MaxSendackErrorRate, "maximum allowed sendack error rate")
	flags.Float64Var(&cfg.MaxConnectErrorRate, "max-connect-error-rate", cfg.MaxConnectErrorRate, "maximum allowed connect error rate")
	flags.BoolVar(&cfg.EvictAfter, "evict-after", cfg.EvictAfter, "evict generated channel runtime state after probing")
	flags.StringVar(&cfg.ReportDir, "report-dir", cfg.ReportDir, "activation report output directory")
}

func bindCapacityMessageEventFlags(flags *pflag.FlagSet, cfg *messageevent.Config, apiCSV *string) {
	flags.StringVar(apiCSV, "api", "", "comma-separated target HTTP API base addresses")
	flags.StringVar(&cfg.RunID, "run-id", cfg.RunID, "stable benchmark run identifier")
	flags.IntVar(&cfg.Channels, "channels", cfg.Channels, "number of generated group channels")
	flags.IntVar(&cfg.StreamsPerChannel, "streams-per-channel", cfg.StreamsPerChannel, "stream base messages generated per channel")
	flags.IntVar(&cfg.LanesPerStream, "lanes-per-stream", cfg.LanesPerStream, "event keys updated before each stream finish")
	flags.IntVar(&cfg.DeltasPerLane, "deltas-per-lane", cfg.DeltasPerLane, "stream.delta updates sent to each event key")
	flags.IntVar(&cfg.PayloadBytes, "payload-bytes", cfg.PayloadBytes, "approximate stream.delta payload bytes")
	flags.IntVar(&cfg.Concurrency, "concurrency", cfg.Concurrency, "maximum in-flight stream workflows")
	flags.DurationVar(&cfg.RequestTimeout, "request-timeout", cfg.RequestTimeout, "timeout for each public HTTP API request")
	flags.BoolVar(&cfg.WarmChannels, "warm-channels", cfg.WarmChannels, "create generated channels before measured message-event metrics snapshots")
	flags.BoolVar(&cfg.WarmRuntime, "warm-runtime", cfg.WarmRuntime, "send one normal message per generated channel before measured message-event metrics snapshots")
	flags.StringVar(&cfg.ReportDir, "report-dir", cfg.ReportDir, "message event report output directory")
}
