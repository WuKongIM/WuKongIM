package capacity

import (
	"math"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/internal/bench/model"
	"github.com/WuKongIM/WuKongIM/internal/bench/report"
	"github.com/stretchr/testify/require"
)

func TestDefaultActivateChannelsConfig(t *testing.T) {
	cfg := DefaultActivateChannelsConfig()

	require.Equal(t, "activate-channels-10k", cfg.RunID)
	require.Equal(t, 10000, cfg.Channels)
	require.Equal(t, 20000, cfg.Users)
	require.Equal(t, 10, cfg.GroupMembers)
	require.Equal(t, 2000, cfg.ActivationConcurrency)
	require.Equal(t, 10*time.Second, cfg.ActivationWindow)
	require.Equal(t, 60*time.Second, cfg.Hold)
	require.Equal(t, 10*time.Second, cfg.HoldProbeInterval)
	require.Equal(t, 1000, cfg.ProbeBatchSize)
	require.Equal(t, 200*time.Millisecond, cfg.StableP99)
	require.Equal(t, 0.0, cfg.MaxSendackErrorRate)
	require.Equal(t, 0.0, cfg.MaxConnectErrorRate)
	require.False(t, cfg.EvictAfter)
	require.Equal(t, "./tmp/wkbench-activate-channels", cfg.ReportDir)
}

func TestActivateChannelsConfigValidate(t *testing.T) {
	valid := DefaultActivateChannelsConfig()
	valid.APIAddrs = []string{"http://127.0.0.1:5011"}
	require.NoError(t, valid.Validate())

	tests := []struct {
		name   string
		mutate func(*ActivateChannelsConfig)
		want   string
	}{
		{name: "api addresses", mutate: func(c *ActivateChannelsConfig) { c.APIAddrs = nil }, want: "api"},
		{name: "run id", mutate: func(c *ActivateChannelsConfig) { c.RunID = " \t" }, want: "run-id"},
		{name: "run id padded", mutate: func(c *ActivateChannelsConfig) { c.RunID = " run-a " }, want: "run-id"},
		{name: "channels", mutate: func(c *ActivateChannelsConfig) { c.Channels = 0 }, want: "channels"},
		{name: "users", mutate: func(c *ActivateChannelsConfig) { c.Users = 0 }, want: "users"},
		{name: "group members", mutate: func(c *ActivateChannelsConfig) { c.GroupMembers = 0 }, want: "group-members"},
		{name: "activation concurrency", mutate: func(c *ActivateChannelsConfig) { c.ActivationConcurrency = 0 }, want: "activation-concurrency"},
		{name: "probe batch size", mutate: func(c *ActivateChannelsConfig) { c.ProbeBatchSize = 0 }, want: "probe-batch-size"},
		{name: "users below group members", mutate: func(c *ActivateChannelsConfig) { c.Users = c.GroupMembers - 1 }, want: "users"},
		{name: "activation window", mutate: func(c *ActivateChannelsConfig) { c.ActivationWindow = 0 }, want: "activation-window"},
		{name: "hold probe interval", mutate: func(c *ActivateChannelsConfig) { c.HoldProbeInterval = 0 }, want: "hold-probe-interval"},
		{name: "stable p99", mutate: func(c *ActivateChannelsConfig) { c.StableP99 = 0 }, want: "stable-p99"},
		{name: "hold", mutate: func(c *ActivateChannelsConfig) { c.Hold = -time.Second }, want: "hold"},
		{name: "sendack error negative", mutate: func(c *ActivateChannelsConfig) { c.MaxSendackErrorRate = -0.1 }, want: "max-sendack-error-rate"},
		{name: "sendack error nan", mutate: func(c *ActivateChannelsConfig) { c.MaxSendackErrorRate = math.NaN() }, want: "max-sendack-error-rate"},
		{name: "connect error negative", mutate: func(c *ActivateChannelsConfig) { c.MaxConnectErrorRate = -0.1 }, want: "max-connect-error-rate"},
		{name: "connect error inf", mutate: func(c *ActivateChannelsConfig) { c.MaxConnectErrorRate = math.Inf(1) }, want: "max-connect-error-rate"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			require.ErrorContains(t, cfg.Validate(), tt.want)
		})
	}
}

func TestBuildActivateChannelsScenarioSchedulesOneSendPerChannel(t *testing.T) {
	cfg := DefaultActivateChannelsConfig()
	cfg.APIAddrs = []string{"http://127.0.0.1:5011"}
	cfg.Channels = 10000
	cfg.Users = 20000
	cfg.GroupMembers = 10
	cfg.ActivationWindow = 10 * time.Second
	cfg.ActivationConcurrency = 2000
	scenario := BuildActivateChannelsScenario(cfg)

	require.Equal(t, "wkbench/v1", scenario.Version)
	require.Equal(t, cfg.RunID, scenario.Run.ID)
	require.Equal(t, cfg.ActivationWindow, scenario.Run.Duration)
	require.Equal(t, time.Duration(0), scenario.Run.Warmup)
	require.Equal(t, time.Duration(0), scenario.Run.Cooldown)
	require.True(t, scenario.Run.FailFast)
	require.Equal(t, cfg.ReportDir, scenario.Run.ReportDir)
	require.True(t, scenario.Limits.FailOnSoft)
	require.Equal(t, 0, scenario.Limits.Hard.MaxWorkerFailed)
	require.Equal(t, cfg.MaxConnectErrorRate, scenario.Limits.Hard.MaxConnectErrorRate)
	require.Equal(t, cfg.MaxSendackErrorRate, scenario.Limits.Hard.MaxSendackErrorRate)
	require.Equal(t, 0.0, scenario.Limits.Hard.MaxRecvVerifyErrorRate)
	require.Equal(t, cfg.StableP99, scenario.Limits.Soft.MaxSendackP99)
	require.Equal(t, 8, scenario.Prepare.Concurrency)
	require.Equal(t, 1000.0, scenario.Prepare.RateLimit.PerSecond)
	require.Equal(t, cfg.Users, scenario.Online.TotalUsers)
	require.Equal(t, 1000.0, scenario.Online.ConnectRate.PerSecond)
	require.Equal(t, "round_robin", scenario.Online.GatewayBalance)
	require.True(t, scenario.Online.Heartbeat.Enabled)
	require.Equal(t, capacityHeartbeatInterval, scenario.Online.Heartbeat.Interval)
	require.Equal(t, capacityHeartbeatTimeout, scenario.Online.Heartbeat.Timeout)
	require.Len(t, scenario.Channels.Profiles, 1)
	require.Equal(t, "activate-groups", scenario.Channels.Profiles[0].Name)
	require.Equal(t, model.ChannelTypeGroup, scenario.Channels.Profiles[0].ChannelType)
	require.Equal(t, 10000, scenario.Channels.Profiles[0].Count)
	require.Equal(t, 10, scenario.Channels.Profiles[0].Members.Count)
	require.Equal(t, "allowed", scenario.Channels.Profiles[0].Members.Overlap)
	require.Equal(t, 1.0, scenario.Channels.Profiles[0].Online.MemberRatio)
	require.Equal(t, "hash", scenario.Channels.Profiles[0].Shard.Mode)
	require.Equal(t, 1000, scenario.Channels.Profiles[0].Prepare.SubscribersBatchSize)
	require.Equal(t, 128, scenario.Messages.Payload.SizeBytes)
	require.Equal(t, "deterministic", scenario.Messages.Payload.Mode)
	require.Len(t, scenario.Messages.Traffic, 1)
	require.Equal(t, "activate-send", scenario.Messages.Traffic[0].Name)
	require.Equal(t, "activate-groups", scenario.Messages.Traffic[0].ChannelRef)
	require.Equal(t, cfg.Channels, activationScheduledMessageCount(
		scenario.Run.Duration,
		scenario.Messages.Traffic[0].RatePerChannel.PerSecond,
		cfg.Channels,
	))
	require.Equal(t, 2000, scenario.Messages.Traffic[0].Concurrency)
	require.Equal(t, "round_robin", scenario.Messages.Traffic[0].SenderPick)
	require.Equal(t, "none", scenario.Messages.Traffic[0].Verify.Recv.Mode)
	require.False(t, scenario.Cleanup.Enabled)
}

func TestBuildActivateChannelsScenarioSchedulesExactlyOneSendPerChannelForShortWindow(t *testing.T) {
	cfg := DefaultActivateChannelsConfig()
	cfg.APIAddrs = []string{"http://127.0.0.1:5011"}
	cfg.Channels = 10000
	cfg.ActivationWindow = 13 * time.Millisecond
	scenario := BuildActivateChannelsScenario(cfg)

	require.Len(t, scenario.Messages.Traffic, 1)
	require.Equal(t, cfg.Channels, activationScheduledMessageCount(
		scenario.Run.Duration,
		scenario.Messages.Traffic[0].RatePerChannel.PerSecond,
		cfg.Channels,
	))
}

func activationScheduledMessageCount(duration time.Duration, perSecond float64, channelCount int) int {
	if duration <= 0 || perSecond <= 0 || channelCount <= 0 {
		return 0
	}
	count := int(duration.Seconds() * perSecond * float64(channelCount))
	if count < 1 {
		return 1
	}
	return count
}

func TestEvaluateActivateChannelsPasses(t *testing.T) {
	cfg := activateChannelsEvalConfig()
	rep := activateChannelsReport(10000, 0, 30*time.Millisecond, report.Summary{})
	cold, active, probes := activateChannelsRuntimeEvidence()

	got := EvaluateActivateChannels(cfg, rep, cold, active, probes)

	require.True(t, got.Passed)
	require.Empty(t, got.FailureReasons)
	require.Equal(t, uint64(10000), got.ActivationSuccess)
	require.Equal(t, uint64(0), got.ActivationErrors)
	require.Equal(t, uint64(0), got.ActivationBacklog)
	require.Equal(t, 10*time.Millisecond, got.SendackP50)
	require.Equal(t, 20*time.Millisecond, got.SendackP95)
	require.Equal(t, 30*time.Millisecond, got.SendackP99)
	require.Equal(t, 10000, got.ActiveLeaderTotal)
	require.Equal(t, uint64(0), got.ActivationRejectedDelta)
	require.Empty(t, got.ProbeMissingAllNodes)
}

func TestEvaluateActivateChannelsFailsOnSendErrors(t *testing.T) {
	cfg := activateChannelsEvalConfig()
	rep := activateChannelsReport(10000, 1, 30*time.Millisecond, report.Summary{})
	cold, active, probes := activateChannelsRuntimeEvidence()

	got := EvaluateActivateChannels(cfg, rep, cold, active, probes)

	require.False(t, got.Passed)
	require.Contains(t, got.FailureReasons, "activation_errors")
	require.Equal(t, uint64(1), got.ActivationErrors)
	require.Equal(t, uint64(0), got.ActivationBacklog)
}

func TestEvaluateActivateChannelsFailsOnMissingLeaderCount(t *testing.T) {
	cfg := activateChannelsEvalConfig()
	rep := activateChannelsReport(10000, 0, 30*time.Millisecond, report.Summary{})
	cold, active, probes := activateChannelsRuntimeEvidence()
	active[0].ActiveLeader = 9999

	got := EvaluateActivateChannels(cfg, rep, cold, active, probes)

	require.False(t, got.Passed)
	require.Contains(t, got.FailureReasons, "active_leader_below_channels")
	require.Equal(t, 9999, got.ActiveLeaderTotal)
}

func TestEvaluateActivateChannelsFailsOnActivationRejectedDelta(t *testing.T) {
	cfg := activateChannelsEvalConfig()
	rep := activateChannelsReport(10000, 0, 30*time.Millisecond, report.Summary{})
	cold, active, probes := activateChannelsRuntimeEvidence()
	active[0].ActivationRejectedTotal = 4

	got := EvaluateActivateChannels(cfg, rep, cold, active, probes)

	require.False(t, got.Passed)
	require.Contains(t, got.FailureReasons, "activation_rejected_delta")
	require.Equal(t, uint64(1), got.ActivationRejectedDelta)
}

func TestEvaluateActivateChannelsFailsOnProbeMissingEverywhere(t *testing.T) {
	cfg := activateChannelsEvalConfig()
	rep := activateChannelsReport(10000, 0, 30*time.Millisecond, report.Summary{})
	cold, active, _ := activateChannelsRuntimeEvidence()
	probes := [][]model.ChannelRuntimeProbeResult{{
		{NodeID: 1, Checked: 2, Missing: []string{"ch-2", "ch-1"}},
		{NodeID: 2, Checked: 2, Missing: []string{"ch-1", "ch-3"}},
		{NodeID: 3, Checked: 2, Missing: []string{"ch-4", "ch-1"}},
	}}

	got := EvaluateActivateChannels(cfg, rep, cold, active, probes)

	require.False(t, got.Passed)
	require.Contains(t, got.FailureReasons, "probe_missing_all_nodes")
	require.Equal(t, []string{"ch-1"}, got.ProbeMissingAllNodes)
}

func TestEvaluateActivateChannelsFailsOnSendackP99(t *testing.T) {
	cfg := activateChannelsEvalConfig()
	rep := activateChannelsReport(10000, 0, 250*time.Millisecond, report.Summary{})
	cold, active, probes := activateChannelsRuntimeEvidence()

	got := EvaluateActivateChannels(cfg, rep, cold, active, probes)

	require.False(t, got.Passed)
	require.Contains(t, got.FailureReasons, "sendack_p99_exceeded")
	require.Equal(t, 250*time.Millisecond, got.SendackP99)
}

func activateChannelsEvalConfig() ActivateChannelsConfig {
	cfg := DefaultActivateChannelsConfig()
	cfg.APIAddrs = []string{"http://127.0.0.1:5011"}
	return cfg
}

func activateChannelsReport(success, errors uint64, p99 time.Duration, summary report.Summary) report.Report {
	return report.Report{
		Summary: summary,
		Metrics: metrics.SnapshotData{
			Counters: map[string]uint64{
				"group_send_success_total{channel_type=group,phase=run,profile=activate-groups,traffic=activate-send}": success,
				"group_send_error_total{channel_type=group,phase=run,profile=activate-groups,traffic=activate-send}":   errors,
			},
			Histograms: map[string]metrics.HistogramSummary{
				"group_send_latency_seconds{channel_type=group,phase=run,profile=activate-groups,traffic=activate-send}": {
					Count:      success,
					P50Seconds: 0.010,
					P95Seconds: 0.020,
					P99Seconds: p99.Seconds(),
				},
			},
		},
	}
}

func activateChannelsRuntimeEvidence() ([]model.ChannelRuntimeSnapshot, []model.ChannelRuntimeSnapshot, [][]model.ChannelRuntimeProbeResult) {
	cold := []model.ChannelRuntimeSnapshot{{NodeID: 1, ActivationRejectedTotal: 3}}
	active := []model.ChannelRuntimeSnapshot{{NodeID: 1, ActiveLeader: 10000, ActivationRejectedTotal: 3}}
	probes := [][]model.ChannelRuntimeProbeResult{{
		{NodeID: 1, Checked: 10000, LoadedLeader: 10000},
		{NodeID: 2, Checked: 10000, Missing: []string{"some-follower-not-loaded"}},
	}}
	return cold, active, probes
}
