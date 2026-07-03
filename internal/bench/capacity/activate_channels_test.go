package capacity

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/coordinator"
	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/internal/bench/report"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/stretchr/testify/require"
)

func TestDefaultActivateChannelsConfig(t *testing.T) {
	cfg := DefaultActivateChannelsConfig()

	require.Equal(t, "activate-channels-10k", cfg.RunID)
	require.Equal(t, 10000, cfg.Channels)
	require.Equal(t, 1000, cfg.Users)
	require.Equal(t, 10, cfg.GroupMembers)
	require.Equal(t, 1000.0, cfg.PrepareRatePerSecond)
	require.Equal(t, 500.0, cfg.ConnectRatePerSecond)
	require.Equal(t, 512, cfg.ActivationConcurrency)
	require.Equal(t, 120*time.Second, cfg.ActivationWindow)
	require.Equal(t, 60*time.Second, cfg.Hold)
	require.Equal(t, 10*time.Second, cfg.HoldProbeInterval)
	require.Equal(t, 1000, cfg.ProbeBatchSize)
	require.Equal(t, 2*time.Second, cfg.StableP99)
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
		{name: "prepare rate negative", mutate: func(c *ActivateChannelsConfig) { c.PrepareRatePerSecond = -1 }, want: "prepare-rate"},
		{name: "prepare rate nan", mutate: func(c *ActivateChannelsConfig) { c.PrepareRatePerSecond = math.NaN() }, want: "prepare-rate"},
		{name: "connect rate negative", mutate: func(c *ActivateChannelsConfig) { c.ConnectRatePerSecond = -1 }, want: "connect-rate"},
		{name: "connect rate inf", mutate: func(c *ActivateChannelsConfig) { c.ConnectRatePerSecond = math.Inf(1) }, want: "connect-rate"},
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
	require.Equal(t, cfg.PrepareRatePerSecond, scenario.Prepare.RateLimit.PerSecond)
	require.Equal(t, cfg.Users, scenario.Online.TotalUsers)
	require.Equal(t, cfg.ConnectRatePerSecond, scenario.Online.ConnectRate.PerSecond)
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

func TestEvaluateActivateChannelsFailsWhenMultiNodeLeadersConcentrateOnSingleNode(t *testing.T) {
	cfg := activateChannelsEvalConfig()
	cfg.APIAddrs = []string{"http://node-1", "http://node-2", "http://node-3"}
	rep := activateChannelsReport(10000, 0, 30*time.Millisecond, report.Summary{})
	cold := []model.ChannelRuntimeSnapshot{
		{NodeID: 1},
		{NodeID: 2},
		{NodeID: 3},
	}
	active := []model.ChannelRuntimeSnapshot{
		{NodeID: 1, ActiveTotal: 7500, ActiveLeader: 7500},
		{NodeID: 2, ActiveTotal: 7500, ActiveFollower: 7500, FollowerParked: 7500},
		{NodeID: 3, ActiveTotal: 7500, ActiveFollower: 7500, FollowerParked: 7500},
	}
	probes := [][]model.ChannelRuntimeProbeResult{{
		{NodeID: 1, Checked: 10000, LoadedLeader: 7500, Missing: []string{"missing-1"}},
		{NodeID: 2, Checked: 10000, LoadedFollower: 7500, Missing: []string{"missing-1"}},
		{NodeID: 3, Checked: 10000, LoadedFollower: 7500, Missing: []string{"missing-1"}},
	}}

	got := EvaluateActivateChannels(cfg, rep, cold, active, probes)

	require.False(t, got.Passed)
	require.Contains(t, got.FailureReasons, "active_leader_single_node")
	require.Equal(t, 7500, got.ActiveLeaderTotal)
	require.Equal(t, 1, got.ActiveLeaderNodeCount)
	require.Equal(t, uint64(1), got.ActiveLeaderMaxNodeID)
	require.Equal(t, 1.0, got.ActiveLeaderMaxNodeShare)
	require.Equal(t, []ActivateChannelsNodeRuntime{
		{NodeID: 1, ActiveTotal: 7500, ActiveLeader: 7500},
		{NodeID: 2, ActiveTotal: 7500, ActiveFollower: 7500, FollowerParked: 7500},
		{NodeID: 3, ActiveTotal: 7500, ActiveFollower: 7500, FollowerParked: 7500},
	}, got.ActiveNodes)
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
	rep := activateChannelsReport(10000, 0, 2500*time.Millisecond, report.Summary{})
	cold, active, probes := activateChannelsRuntimeEvidence()

	got := EvaluateActivateChannels(cfg, rep, cold, active, probes)

	require.False(t, got.Passed)
	require.Contains(t, got.FailureReasons, "sendack_p99_exceeded")
	require.Equal(t, 2500*time.Millisecond, got.SendackP99)
}

func TestActivateChannelsRunnerCapturesSnapshotsAndEvaluates(t *testing.T) {
	cfg := activateChannelsRunnerConfig(t)
	cfg.Channels = 4
	cfg.Users = 8
	cfg.GroupMembers = 2
	cfg.ActivationWindow = 10 * time.Millisecond
	cfg.Hold = time.Nanosecond
	cfg.HoldProbeInterval = time.Nanosecond
	cfg.ProbeBatchSize = 2
	cfg.StableP99 = time.Second
	discovered := activateChannelsDiscoveredTarget(cfg)
	var calls []string
	target := &fakeActivateChannelsTarget{
		calls:              &calls,
		capabilitiesByAddr: activateChannelsCapabilitiesByAddr(cfg.APIAddrs, true, true),
		snapshots: [][]model.ChannelRuntimeSnapshot{
			{{Version: "bench/v1", NodeID: 1, ActivationRejectedTotal: 1}},
			{{Version: "bench/v1", NodeID: 1, ActiveLeader: 4, ActivationRejectedTotal: 1}},
			{{Version: "bench/v1", NodeID: 1, ActiveLeader: 4, ActivationRejectedTotal: 1}},
		},
		probes: [][]model.ChannelRuntimeProbeResult{
			{
				{Version: "bench/v1", NodeID: 1, Checked: 2, LoadedLeader: 2},
				{Version: "bench/v1", NodeID: 2, Checked: 2, Missing: []string{"follower-not-loaded"}},
			},
			{
				{Version: "bench/v1", NodeID: 1, Checked: 2, LoadedLeader: 2},
				{Version: "bench/v1", NodeID: 2, Checked: 2},
			},
		},
	}
	runner := NewActivateChannelsRunner(cfg, discovered)
	runner.target = target
	runner.base = &Runner{workers: []model.Worker{{ID: "fake-worker", Addr: "memory://worker", Weight: 1}}}
	runner.run = func(_ context.Context, scenario model.Scenario, workers []model.Worker, target model.Target) (coordinator.RunResult, error) {
		calls = append(calls, "coordinator-run")
		require.Equal(t, cfg.RunID, scenario.Run.ID)
		require.Equal(t, cfg.Channels, scenario.Channels.Profiles[0].Count)
		require.Equal(t, []model.Worker{{ID: "fake-worker", Addr: "memory://worker", Weight: 1}}, workers)
		require.Equal(t, discovered.Target, target)
		return coordinator.RunResult{
			RunID:  cfg.RunID,
			Status: coordinator.StatusCompleted,
			Report: activateChannelsReport(uint64(cfg.Channels), 0, 30*time.Millisecond, report.Summary{}),
		}, nil
	}

	got, err := runner.Run(context.Background())

	require.NoError(t, err)
	require.Equal(t, StatusPassed, got.Status)
	require.True(t, got.Evaluation.Passed)
	require.Equal(t, uint64(4), got.Evaluation.ActivationSuccess)
	require.Len(t, got.Cold, 1)
	require.Len(t, got.Active, 1)
	require.Len(t, got.HoldSamples, 1)
	require.Len(t, got.ProbeBatches, 2)
	require.Empty(t, got.EvictBatches)
	require.Equal(t, cfg.ReportDir, got.ReportDir)
	require.Equal(t, []string{
		"capabilities-all",
		"snapshot:0-4",
		"coordinator-run",
		"snapshot:0-4",
		"snapshot:0-4",
		"probe:0-2",
		"probe:2-4",
	}, calls)
	require.Equal(t, []model.ChannelRuntimeQuery{
		activateChannelsRuntimeQuery(cfg, model.ChannelRuntimeRange{Start: 0, End: 4}),
		activateChannelsRuntimeQuery(cfg, model.ChannelRuntimeRange{Start: 0, End: 4}),
		activateChannelsRuntimeQuery(cfg, model.ChannelRuntimeRange{Start: 0, End: 4}),
	}, target.snapshotQueries)
	require.Equal(t, []model.ChannelRuntimeProbeRequest{
		activateChannelsProbeRequest(cfg, model.ChannelRuntimeRange{Start: 0, End: 2}),
		activateChannelsProbeRequest(cfg, model.ChannelRuntimeRange{Start: 2, End: 4}),
	}, target.probeRequests)
}

func TestActivateChannelsRunnerFailsWhenCapabilitiesMissing(t *testing.T) {
	cfg := activateChannelsRunnerConfig(t)
	discovered := activateChannelsDiscoveredTarget(cfg)
	var calls []string
	target := &fakeActivateChannelsTarget{
		calls:              &calls,
		capabilitiesByAddr: activateChannelsCapabilitiesByAddr(cfg.APIAddrs, true, false),
	}
	runner := NewActivateChannelsRunner(cfg, discovered)
	runner.target = target
	runner.base = &Runner{workers: []model.Worker{{ID: "fake-worker", Addr: "memory://worker", Weight: 1}}}
	runner.run = func(context.Context, model.Scenario, []model.Worker, model.Target) (coordinator.RunResult, error) {
		t.Fatal("coordinator run must not be called when runtime probe capability is missing")
		return coordinator.RunResult{}, nil
	}

	got, err := runner.Run(context.Background())

	require.Error(t, err)
	require.Contains(t, err.Error(), "channel_runtime_probe")
	require.Equal(t, StatusFailed, got.Status)
	require.False(t, got.Evaluation.Passed)
	require.Equal(t, []string{"capabilities-all"}, calls)
}

func TestActivateChannelsRunnerFailsWhenSecondNodeRuntimeProbeCapabilityMissing(t *testing.T) {
	cfg := activateChannelsRunnerConfig(t)
	cfg.APIAddrs = []string{"http://node-1", "http://node-2"}
	discovered := activateChannelsDiscoveredTarget(cfg)
	var calls []string
	target := &fakeActivateChannelsTarget{
		calls: &calls,
		capabilitiesByAddr: map[string]model.BenchCapabilities{
			"http://node-1": activateChannelsCapabilities(true, true),
			"http://node-2": activateChannelsCapabilities(true, false),
		},
	}
	runner := NewActivateChannelsRunner(cfg, discovered)
	runner.target = target
	runner.base = &Runner{workers: []model.Worker{{ID: "fake-worker", Addr: "memory://worker", Weight: 1}}}
	runner.run = func(context.Context, model.Scenario, []model.Worker, model.Target) (coordinator.RunResult, error) {
		t.Fatal("coordinator run must not be called when a target node lacks runtime probe capability")
		return coordinator.RunResult{}, nil
	}

	got, err := runner.Run(context.Background())

	require.Error(t, err)
	require.Contains(t, err.Error(), "http://node-2")
	require.Contains(t, err.Error(), "channel_runtime_probe")
	require.Equal(t, StatusFailed, got.Status)
	require.False(t, got.Evaluation.Passed)
	require.Equal(t, []string{"capabilities-all"}, calls)
}

func TestActivateChannelsRunnerFailsWhenEvictCapabilityMissing(t *testing.T) {
	cfg := activateChannelsRunnerConfig(t)
	cfg.EvictAfter = true
	discovered := activateChannelsDiscoveredTarget(cfg)
	var calls []string
	target := &fakeActivateChannelsTarget{
		calls:              &calls,
		capabilitiesByAddr: activateChannelsCapabilitiesByAddr(cfg.APIAddrs, true, true),
	}
	runner := NewActivateChannelsRunner(cfg, discovered)
	runner.target = target
	runner.base = &Runner{workers: []model.Worker{{ID: "fake-worker", Addr: "memory://worker", Weight: 1}}}
	runner.run = func(context.Context, model.Scenario, []model.Worker, model.Target) (coordinator.RunResult, error) {
		t.Fatal("coordinator run must not be called when runtime evict capability is missing")
		return coordinator.RunResult{}, nil
	}

	got, err := runner.Run(context.Background())

	require.Error(t, err)
	require.Contains(t, err.Error(), "channel_runtime_evict")
	require.Equal(t, StatusFailed, got.Status)
	require.False(t, got.Evaluation.Passed)
	require.Equal(t, []string{"capabilities-all"}, calls)
}

func TestActivateChannelsRunnerKeepsPartialProbeEvidence(t *testing.T) {
	cfg := activateChannelsRunnerConfig(t)
	cfg.Channels = 4
	cfg.Users = 8
	cfg.GroupMembers = 2
	cfg.ActivationWindow = 10 * time.Millisecond
	cfg.ProbeBatchSize = 2
	cfg.StableP99 = time.Second
	discovered := activateChannelsDiscoveredTarget(cfg)
	target := &fakeActivateChannelsTarget{
		capabilitiesByAddr: activateChannelsCapabilitiesByAddr(cfg.APIAddrs, true, true),
		snapshots: [][]model.ChannelRuntimeSnapshot{
			{{Version: "bench/v1", NodeID: 1}},
			{{Version: "bench/v1", NodeID: 1, ActiveLeader: 4}},
		},
		probes: [][]model.ChannelRuntimeProbeResult{{
			{Version: "bench/v1", NodeID: 1, Checked: 2, LoadedLeader: 2},
		}},
		probeErrs: []error{errors.New("node-2 probe failed")},
	}
	runner := NewActivateChannelsRunner(cfg, discovered)
	runner.target = target
	runner.base = &Runner{workers: []model.Worker{{ID: "fake-worker", Addr: "memory://worker", Weight: 1}}}
	runner.run = func(context.Context, model.Scenario, []model.Worker, model.Target) (coordinator.RunResult, error) {
		return coordinator.RunResult{
			RunID:  cfg.RunID,
			Status: coordinator.StatusCompleted,
			Report: activateChannelsReport(uint64(cfg.Channels), 0, 30*time.Millisecond, report.Summary{}),
		}, nil
	}

	got, err := runner.Run(context.Background())

	require.Error(t, err)
	require.Contains(t, err.Error(), "node-2 probe failed")
	require.Equal(t, StatusFailed, got.Status)
	require.Equal(t, [][]model.ChannelRuntimeProbeResult{{
		{Version: "bench/v1", NodeID: 1, Checked: 2, LoadedLeader: 2},
	}}, got.ProbeBatches)
}

func TestActivateChannelsRunnerKeepsPartialEvictEvidenceAndFailsStatus(t *testing.T) {
	cfg := activateChannelsRunnerConfig(t)
	cfg.Channels = 4
	cfg.Users = 8
	cfg.GroupMembers = 2
	cfg.ActivationWindow = 10 * time.Millisecond
	cfg.ProbeBatchSize = 2
	cfg.StableP99 = time.Second
	cfg.EvictAfter = true
	discovered := activateChannelsDiscoveredTarget(cfg)
	target := &fakeActivateChannelsTarget{
		capabilitiesByAddr: activateChannelsCapabilitiesWithEvictByAddr(cfg.APIAddrs, true, true, true),
		snapshots: [][]model.ChannelRuntimeSnapshot{
			{{Version: "bench/v1", NodeID: 1}},
			{{Version: "bench/v1", NodeID: 1, ActiveLeader: 4}},
		},
		probes: [][]model.ChannelRuntimeProbeResult{
			{{Version: "bench/v1", NodeID: 1, Checked: 2, LoadedLeader: 2}},
			{{Version: "bench/v1", NodeID: 1, Checked: 2, LoadedLeader: 2}},
		},
		evicts: [][]model.ChannelRuntimeEvictResult{{
			{Version: "bench/v1", NodeID: 1, Requested: 2, Evicted: 2},
		}},
		evictErrs: []error{errors.New("node-2 evict failed")},
	}
	runner := NewActivateChannelsRunner(cfg, discovered)
	runner.target = target
	runner.base = &Runner{workers: []model.Worker{{ID: "fake-worker", Addr: "memory://worker", Weight: 1}}}
	runner.run = func(context.Context, model.Scenario, []model.Worker, model.Target) (coordinator.RunResult, error) {
		return coordinator.RunResult{
			RunID:  cfg.RunID,
			Status: coordinator.StatusCompleted,
			Report: activateChannelsReport(uint64(cfg.Channels), 0, 30*time.Millisecond, report.Summary{}),
		}, nil
	}

	got, err := runner.Run(context.Background())

	require.Error(t, err)
	require.Contains(t, err.Error(), "node-2 evict failed")
	require.Equal(t, StatusFailed, got.Status)
	require.False(t, got.Evaluation.Passed)
	require.Contains(t, got.Evaluation.FailureReasons, "evict_failed")
	require.Equal(t, [][]model.ChannelRuntimeEvictResult{{
		{Version: "bench/v1", NodeID: 1, Requested: 2, Evicted: 2},
	}}, got.EvictBatches)
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

func activateChannelsRunnerConfig(t *testing.T) ActivateChannelsConfig {
	t.Helper()
	cfg := DefaultActivateChannelsConfig()
	cfg.APIAddrs = []string{"http://127.0.0.1:5011"}
	cfg.GatewayTCPAddrs = []string{"127.0.0.1:5100"}
	cfg.ReportDir = t.TempDir()
	cfg.Hold = 0
	return cfg
}

func activateChannelsDiscoveredTarget(cfg ActivateChannelsConfig) DiscoveredTarget {
	return DiscoveredTarget{
		Target: model.Target{
			Name:    "activate-channels",
			API:     model.TargetAPIConfig{Addrs: cfg.APIAddrs},
			Gateway: model.TargetGatewayConfig{TCP: model.TargetGatewayTCPConfig{Addrs: cfg.GatewayTCPAddrs}},
			BenchAPI: model.BenchAPIConfig{
				Enabled: true,
				Addrs:   cfg.APIAddrs,
				Token:   cfg.BenchToken,
			},
		},
	}
}

type fakeActivateChannelsTarget struct {
	calls              *[]string
	capabilities       model.BenchCapabilities
	capabilitiesErr    error
	capabilitiesByAddr map[string]model.BenchCapabilities
	capabilitiesAllErr error
	snapshots          [][]model.ChannelRuntimeSnapshot
	snapshotErr        error
	probes             [][]model.ChannelRuntimeProbeResult
	probeErr           error
	probeErrs          []error
	evicts             [][]model.ChannelRuntimeEvictResult
	evictErr           error
	evictErrs          []error
	snapshotQueries    []model.ChannelRuntimeQuery
	probeRequests      []model.ChannelRuntimeProbeRequest
	evictRequests      []model.ChannelRuntimeEvictRequest
}

func (f *fakeActivateChannelsTarget) Capabilities(context.Context) (model.BenchCapabilities, error) {
	f.record("capabilities")
	return f.capabilities, f.capabilitiesErr
}

func (f *fakeActivateChannelsTarget) CapabilitiesAll(context.Context) (map[string]model.BenchCapabilities, error) {
	f.record("capabilities-all")
	return f.capabilitiesByAddr, f.capabilitiesAllErr
}

func (f *fakeActivateChannelsTarget) ChannelRuntimeSnapshots(_ context.Context, query model.ChannelRuntimeQuery) ([]model.ChannelRuntimeSnapshot, error) {
	f.record("snapshot:%d-%d", query.Range.Start, query.Range.End)
	f.snapshotQueries = append(f.snapshotQueries, query)
	if f.snapshotErr != nil {
		return nil, f.snapshotErr
	}
	if len(f.snapshots) == 0 {
		return nil, nil
	}
	out := f.snapshots[0]
	f.snapshots = f.snapshots[1:]
	return out, nil
}

func (f *fakeActivateChannelsTarget) ProbeChannelRuntimeAll(_ context.Context, req model.ChannelRuntimeProbeRequest) ([]model.ChannelRuntimeProbeResult, error) {
	f.record("probe:%d-%d", req.Range.Start, req.Range.End)
	f.probeRequests = append(f.probeRequests, req)
	err := f.probeErr
	if len(f.probeErrs) > 0 {
		err = f.probeErrs[0]
		f.probeErrs = f.probeErrs[1:]
	}
	if len(f.probes) == 0 {
		return nil, err
	}
	out := f.probes[0]
	f.probes = f.probes[1:]
	return out, err
}

func (f *fakeActivateChannelsTarget) EvictChannelRuntimeAll(_ context.Context, req model.ChannelRuntimeEvictRequest) ([]model.ChannelRuntimeEvictResult, error) {
	f.record("evict:%d-%d", req.Range.Start, req.Range.End)
	f.evictRequests = append(f.evictRequests, req)
	err := f.evictErr
	if len(f.evictErrs) > 0 {
		err = f.evictErrs[0]
		f.evictErrs = f.evictErrs[1:]
	}
	if len(f.evicts) == 0 {
		return nil, err
	}
	out := f.evicts[0]
	f.evicts = f.evicts[1:]
	return out, err
}

func (f *fakeActivateChannelsTarget) record(format string, args ...any) {
	if f.calls == nil {
		return
	}
	*f.calls = append(*f.calls, fmt.Sprintf(format, args...))
}

func activateChannelsCapabilitiesByAddr(addrs []string, snapshot bool, probe bool) map[string]model.BenchCapabilities {
	return activateChannelsCapabilitiesWithEvictByAddr(addrs, snapshot, probe, false)
}

func activateChannelsCapabilitiesWithEvictByAddr(addrs []string, snapshot bool, probe bool, evict bool) map[string]model.BenchCapabilities {
	out := make(map[string]model.BenchCapabilities, len(addrs))
	for _, addr := range addrs {
		out[addr] = activateChannelsCapabilitiesWithEvict(snapshot, probe, evict)
	}
	return out
}

func activateChannelsCapabilities(snapshot bool, probe bool) model.BenchCapabilities {
	return activateChannelsCapabilitiesWithEvict(snapshot, probe, false)
}

func activateChannelsCapabilitiesWithEvict(snapshot bool, probe bool, evict bool) model.BenchCapabilities {
	return model.BenchCapabilities{
		Enabled: true,
		Version: "bench/v1",
		Supports: model.BenchCapabilitiesSupports{
			ChannelRuntimeSnapshot: snapshot,
			ChannelRuntimeProbe:    probe,
			ChannelRuntimeEvict:    evict,
		},
	}
}
