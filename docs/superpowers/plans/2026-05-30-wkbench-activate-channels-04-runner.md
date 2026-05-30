# wkbench Activate Channels 04 Runner Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the `internal/bench/capacity` activation runner that prepares N group channels, activates them through real WKProto SEND, holds them live, probes runtime evidence, and writes activation-specific reports.

**Architecture:** Reuse the existing capacity discovery, temporary local worker, coordinator, and workload scheduler. Activation is a fixed-size experiment, not a QPS search: the generated scenario has one group profile, zero warmup, `duration = activation_window`, and `rate_per_channel = 1 / activation_window`, which schedules exactly one run-phase SEND per channel. Runtime snapshots and probes come through the Phase 01-03 bench APIs and are used only for evidence and pass/fail evaluation.

**Tech Stack:** Go, `internal/bench/capacity`, `internal/bench/model`, `internal/bench/target`, `internal/bench/coordinator`, `internal/bench/report`, `GOWORK=off go test`.

---

## Files

- Create: `internal/bench/capacity/activate_channels.go`
- Create: `internal/bench/capacity/activate_channels_test.go`
- Create: `internal/bench/capacity/activate_channels_result.go`
- Create: `internal/bench/capacity/activate_channels_result_test.go`
- Modify: `internal/bench/target/client.go`
- Modify: `internal/bench/target/client_test.go`
- Modify: `internal/bench/FLOW.md`

## Task 1: All-Node Runtime Probe Client

**Files:**

- Modify: `internal/bench/target/client.go`
- Modify: `internal/bench/target/client_test.go`

- [ ] **Step 1: Add failing tests**

Add:

```go
func TestClientProbeChannelRuntimeAllCallsEveryTarget(t *testing.T)
func TestClientEvictChannelRuntimeAllCallsEveryTarget(t *testing.T)
```

Both tests create two `httptest.Server` instances, assert the path is `/bench/v1/channel-runtime/probe` or `/bench/v1/channel-runtime/evict`, decode `model.ChannelRuntimeProbeRequest` or `model.ChannelRuntimeEvictRequest`, and assert both servers are called.

Expected probe result shape:

```go
require.Equal(t, []model.ChannelRuntimeProbeResult{
	{Version: "bench/v1", NodeID: 1, Checked: 10, LoadedLeader: 4},
	{Version: "bench/v1", NodeID: 2, Checked: 10, LoadedLeader: 6},
}, got)
```

- [ ] **Step 2: Run failing test**

```bash
GOWORK=off go test ./internal/bench/target -run 'TestClient.*ChannelRuntimeAll' -count=1
```

Expected:

```text
FAIL
... ProbeChannelRuntimeAll undefined ...
```

- [ ] **Step 3: Implement all-node POST helpers**

Add methods:

```go
// ProbeChannelRuntimeAll asks every configured target node to inspect selected generated channels.
func (c *Client) ProbeChannelRuntimeAll(ctx context.Context, req model.ChannelRuntimeProbeRequest) ([]model.ChannelRuntimeProbeResult, error) {
	var out []model.ChannelRuntimeProbeResult
	if err := c.postAll(ctx, "/bench/v1/channel-runtime/probe", req, func() any {
		out = append(out, model.ChannelRuntimeProbeResult{})
		return &out[len(out)-1]
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// EvictChannelRuntimeAll asks every configured target node to evict selected generated runtime state.
func (c *Client) EvictChannelRuntimeAll(ctx context.Context, req model.ChannelRuntimeEvictRequest) ([]model.ChannelRuntimeEvictResult, error) {
	var out []model.ChannelRuntimeEvictResult
	if err := c.postAll(ctx, "/bench/v1/channel-runtime/evict", req, func() any {
		out = append(out, model.ChannelRuntimeEvictResult{})
		return &out[len(out)-1]
	}); err != nil {
		return nil, err
	}
	return out, nil
}
```

Add helper:

```go
func (c *Client) postAll(ctx context.Context, path string, body any, outFactory func() any) error {
	addrs := c.addrs()
	if len(addrs) == 0 {
		return fmt.Errorf("no target api addresses configured")
	}
	var errs []string
	for _, addr := range addrs {
		if err := c.doJSON(ctx, http.MethodPost, addr, path, body, outFactory()); err != nil {
			errs = append(errs, err.Error())
			continue
		}
	}
	if len(errs) != 0 {
		return fmt.Errorf("target api addresses failed: %s", strings.Join(errs, "; "))
	}
	return nil
}
```

- [ ] **Step 4: Verify**

```bash
GOWORK=off go test ./internal/bench/target -run 'TestClient.*ChannelRuntime' -count=1
```

Expected:

```text
ok  	github.com/WuKongIM/WuKongIM/internal/bench/target
```

## Task 2: Activation Config And Scenario

**Files:**

- Create: `internal/bench/capacity/activate_channels.go`
- Create: `internal/bench/capacity/activate_channels_test.go`

- [ ] **Step 1: Add config and scenario tests**

Add:

```go
func TestDefaultActivateChannelsConfig(t *testing.T)
func TestActivateChannelsConfigValidate(t *testing.T)
func TestBuildActivateChannelsScenarioSchedulesOneSendPerChannel(t *testing.T)
```

Core scenario assertions:

```go
cfg := DefaultActivateChannelsConfig()
cfg.APIAddrs = []string{"http://127.0.0.1:5011"}
cfg.Channels = 10000
cfg.Users = 20000
cfg.GroupMembers = 10
cfg.ActivationWindow = 10 * time.Second
cfg.ActivationConcurrency = 2000
scenario := BuildActivateChannelsScenario(cfg)

require.Equal(t, "wkbench/v1", scenario.Version)
require.Equal(t, cfg.ActivationWindow, scenario.Run.Duration)
require.Equal(t, time.Duration(0), scenario.Run.Warmup)
require.Len(t, scenario.Channels.Profiles, 1)
require.Equal(t, "activate-groups", scenario.Channels.Profiles[0].Name)
require.Equal(t, 10000, scenario.Channels.Profiles[0].Count)
require.Equal(t, "allowed", scenario.Channels.Profiles[0].Members.Overlap)
require.Equal(t, 1.0/cfg.ActivationWindow.Seconds(), scenario.Messages.Traffic[0].RatePerChannel.PerSecond)
require.Equal(t, 2000, scenario.Messages.Traffic[0].Concurrency)
require.Equal(t, "round_robin", scenario.Messages.Traffic[0].SenderPick)
```

- [ ] **Step 2: Implement config**

Add:

```go
const activateChannelsProfileName = "activate-groups"
const activateChannelsTrafficName = "activate-send"

// ActivateChannelsConfig controls one fixed-size live-channel activation experiment.
type ActivateChannelsConfig struct {
	APIAddrs              []string
	GatewayTCPAddrs       []string
	BenchToken            string
	RunID                 string
	Channels              int
	Users                 int
	GroupMembers          int
	ActivationConcurrency int
	ActivationWindow      time.Duration
	Hold                  time.Duration
	HoldProbeInterval     time.Duration
	ProbeBatchSize        int
	StableP99             time.Duration
	MaxSendackErrorRate   float64
	MaxConnectErrorRate   float64
	EvictAfter            bool
	ReportDir             string
}
```

Defaults:

```go
func DefaultActivateChannelsConfig() ActivateChannelsConfig {
	return ActivateChannelsConfig{
		RunID:                 "activate-channels-10k",
		Channels:              10000,
		Users:                 20000,
		GroupMembers:          10,
		ActivationConcurrency: 2000,
		ActivationWindow:      10 * time.Second,
		Hold:                  60 * time.Second,
		HoldProbeInterval:     10 * time.Second,
		ProbeBatchSize:        1000,
		StableP99:             200 * time.Millisecond,
		MaxSendackErrorRate:   0,
		MaxConnectErrorRate:   0,
		ReportDir:             "./tmp/wkbench-activate-channels",
	}
}
```

Validation rules:

- `APIAddrs` required.
- `Channels`, `Users`, `GroupMembers`, `ActivationConcurrency`, and `ProbeBatchSize` must be positive.
- `Users >= GroupMembers`.
- `ActivationWindow`, `HoldProbeInterval`, and `StableP99` must be positive.
- `Hold` must not be negative.
- error rates must be finite and non-negative.

- [ ] **Step 3: Implement scenario builder**

`BuildActivateChannelsScenario(cfg)` creates one group profile:

```go
RatePerChannel: model.Rate{PerSecond: 1.0 / cfg.ActivationWindow.Seconds()}
```

Set:

- `Run.ID = cfg.RunID`
- `Run.Duration = cfg.ActivationWindow`
- `Run.Warmup = 0`
- `Run.Cooldown = 0`
- `Run.ReportDir = cfg.ReportDir`
- `Limits.FailOnSoft = true`
- `Online.TotalUsers = cfg.Users`
- `Members.Overlap = "allowed"`
- `Cleanup.Enabled = false`
- `Verify.Recv.Mode = "none"`

- [ ] **Step 4: Verify**

```bash
GOWORK=off go test ./internal/bench/capacity -run 'Test.*ActivateChannels.*Config|TestBuildActivateChannelsScenario' -count=1
```

Expected:

```text
ok  	github.com/WuKongIM/WuKongIM/internal/bench/capacity
```

## Task 3: Activation Evaluation

**Files:**

- Modify: `internal/bench/capacity/activate_channels.go`
- Modify: `internal/bench/capacity/activate_channels_test.go`

- [ ] **Step 1: Add evaluation tests**

Add:

```go
func TestEvaluateActivateChannelsPasses(t *testing.T)
func TestEvaluateActivateChannelsFailsOnSendErrors(t *testing.T)
func TestEvaluateActivateChannelsFailsOnMissingLeaderCount(t *testing.T)
func TestEvaluateActivateChannelsFailsOnActivationRejectedDelta(t *testing.T)
func TestEvaluateActivateChannelsFailsOnProbeMissingEverywhere(t *testing.T)
func TestEvaluateActivateChannelsFailsOnSendackP99(t *testing.T)
```

The pass fixture uses:

```go
cold := []model.ChannelRuntimeSnapshot{{NodeID: 1, ActivationRejectedTotal: 3}}
active := []model.ChannelRuntimeSnapshot{{NodeID: 1, ActiveLeader: 10000, ActivationRejectedTotal: 3}}
probe := [][]model.ChannelRuntimeProbeResult{{
	{NodeID: 1, Checked: 10000, LoadedLeader: 10000},
	{NodeID: 2, Checked: 10000, Missing: []string{"some-follower-not-loaded"}},
}}
```

- [ ] **Step 2: Implement evaluation types and function**

Add:

```go
type ActivateChannelsEvaluation struct {
	Passed                  bool          `json:"passed"`
	FailureReasons          []string      `json:"failure_reasons,omitempty"`
	ActivationSuccess       uint64        `json:"activation_success"`
	ActivationErrors        uint64        `json:"activation_errors"`
	ActivationBacklog       uint64        `json:"activation_backlog"`
	SendackP50              time.Duration `json:"sendack_p50"`
	SendackP95              time.Duration `json:"sendack_p95"`
	SendackP99              time.Duration `json:"sendack_p99"`
	ActiveLeaderTotal       int           `json:"active_leader_total"`
	ActivationRejectedDelta uint64        `json:"activation_rejected_delta"`
	ProbeMissingAllNodes    []string      `json:"probe_missing_all_nodes,omitempty"`
}

func EvaluateActivateChannels(cfg ActivateChannelsConfig, rep report.Report, cold []model.ChannelRuntimeSnapshot, active []model.ChannelRuntimeSnapshot, probes [][]model.ChannelRuntimeProbeResult) ActivateChannelsEvaluation
```

Rules:

- `ActivationSuccess` and latency come from `report.SendRunSummaryFromMetrics(rep.Metrics, cfg.ActivationWindow)`.
- `ActivationBacklog = channels - min(channels, success+errors)`.
- Fail if success is not exactly `cfg.Channels`.
- Fail if errors are greater than zero.
- Fail if worker failed, connect error rate, or sendack error rate exceeds config.
- Fail if p99 exceeds `cfg.StableP99`.
- Fail if summed `ActiveLeader` across active snapshots is less than `cfg.Channels`.
- Fail if summed `ActivationRejectedTotal` increased from cold to active.
- For each probe batch, compute channels missing from every node by intersecting per-node `Missing` lists. Fail if that intersection is non-empty.

- [ ] **Step 3: Verify**

```bash
GOWORK=off go test ./internal/bench/capacity -run 'TestEvaluateActivateChannels' -count=1
```

Expected:

```text
ok  	github.com/WuKongIM/WuKongIM/internal/bench/capacity
```

## Task 4: Runner And Hold Flow

**Files:**

- Modify: `internal/bench/capacity/activate_channels.go`
- Modify: `internal/bench/capacity/activate_channels_test.go`

- [ ] **Step 1: Add runner tests with fakes**

Add:

```go
func TestActivateChannelsRunnerCapturesSnapshotsAndEvaluates(t *testing.T)
func TestActivateChannelsRunnerFailsWhenCapabilitiesMissing(t *testing.T)
```

Use a fake target client interface and fake coordinator runner function so the unit test does not open sockets. Assert order:

```text
cold snapshot -> coordinator run -> active snapshot -> hold snapshot/probe -> evaluation
```

- [ ] **Step 2: Implement runner**

Add:

```go
type ActivateChannelsRunner struct {
	cfg        ActivateChannelsConfig
	discovered DiscoveredTarget
	base       *Runner
	target     activateTargetClient
	run        func(context.Context, model.Scenario, []model.Worker, model.Target) (coordinator.RunResult, error)
	now        func() time.Time
}
```

Define the runner-local target interface:

```go
type activateTargetClient interface {
	Capabilities(context.Context) (model.BenchCapabilities, error)
	ChannelRuntimeSnapshots(context.Context, model.ChannelRuntimeQuery) ([]model.ChannelRuntimeSnapshot, error)
	ProbeChannelRuntimeAll(context.Context, model.ChannelRuntimeProbeRequest) ([]model.ChannelRuntimeProbeResult, error)
	EvictChannelRuntimeAll(context.Context, model.ChannelRuntimeEvictRequest) ([]model.ChannelRuntimeEvictResult, error)
}
```

`NewActivateChannelsRunner(cfg, discovered)` creates a runner with:

- one temporary local worker through `base.startWorker()`
- `target.NewClient(target.Config{APIAddrs: cfg.APIAddrs, Token: cfg.BenchToken})`
- `coordinator.New(...).Run`

Run flow:

1. `cfg.Validate()`
2. verify all target capabilities support `channel_runtime_snapshot` and `channel_runtime_probe`
3. start worker
4. capture cold snapshots
5. run coordinator with `BuildActivateChannelsScenario(cfg)`
6. capture active snapshots
7. hold until `cfg.Hold` expires, sampling snapshots every `HoldProbeInterval`
8. probe batches of `ProbeBatchSize`
9. evaluate
10. evict all batches only when `EvictAfter` is true
11. return `ActivateChannelsResult`

- [ ] **Step 3: Verify**

```bash
GOWORK=off go test ./internal/bench/capacity -run 'TestActivateChannelsRunner' -count=1
```

Expected:

```text
ok  	github.com/WuKongIM/WuKongIM/internal/bench/capacity
```

## Task 5: Activation Report

**Files:**

- Create: `internal/bench/capacity/activate_channels_result.go`
- Create: `internal/bench/capacity/activate_channels_result_test.go`

- [ ] **Step 1: Add report tests**

Add:

```go
func TestWriteActivateChannelsResultWritesArtifacts(t *testing.T)
func TestActivateChannelsSummaryMarkdownIncludesFailureReasons(t *testing.T)
func TestActivateChannelsConsoleSummary(t *testing.T)
```

Expect files:

```text
activation_report.json
summary.md
```

- [ ] **Step 2: Implement report types and writers**

Add:

```go
type ActivateChannelsReportConfig struct {
	RunID                 string        `json:"run_id"`
	Channels              int           `json:"channels"`
	Users                 int           `json:"users"`
	GroupMembers          int           `json:"group_members"`
	ActivationConcurrency int           `json:"activation_concurrency"`
	ActivationWindow      time.Duration `json:"activation_window"`
	Hold                  time.Duration `json:"hold"`
	ProbeBatchSize        int           `json:"probe_batch_size"`
	EvictAfter            bool          `json:"evict_after"`
}

type ActivateChannelsResult struct {
	Status       Status                       `json:"status"`
	Config       ActivateChannelsReportConfig `json:"config"`
	Evaluation   ActivateChannelsEvaluation   `json:"evaluation"`
	Cold         []model.ChannelRuntimeSnapshot `json:"cold_snapshots,omitempty"`
	Active       []model.ChannelRuntimeSnapshot `json:"active_snapshots,omitempty"`
	HoldSamples  [][]model.ChannelRuntimeSnapshot `json:"hold_samples,omitempty"`
	ProbeBatches [][]model.ChannelRuntimeProbeResult `json:"probe_batches,omitempty"`
	EvictBatches [][]model.ChannelRuntimeEvictResult `json:"evict_batches,omitempty"`
	ReportDir    string `json:"report_dir,omitempty"`
}

func (r ActivateChannelsResult) ExitCode() int
func WriteActivateChannelsResult(dir string, result ActivateChannelsResult) error
func ActivateChannelsSummaryMarkdown(result ActivateChannelsResult) string
func ActivateChannelsConsoleSummary(result ActivateChannelsResult) string
```

`ActivateChannelsReportConfig` intentionally excludes `BenchToken`.

Exit mapping:

- passed -> `ExitSuccess`
- failed evaluation -> `ExitNoStableAttempt`
- worker/coordinator error with no report -> `ExitWorkerFailed`

- [ ] **Step 3: Verify**

```bash
GOWORK=off go test ./internal/bench/capacity -run 'Test.*ActivateChannels.*Summary|TestWriteActivateChannelsResult' -count=1
```

Expected:

```text
ok  	github.com/WuKongIM/WuKongIM/internal/bench/capacity
```

## Task 6: FLOW Update

**Files:**

- Modify: `internal/bench/FLOW.md`

- [ ] **Step 1: Document activate-channels flow**

Add a section after `Capacity Send Flow`:

```markdown
## Capacity Activate-Channels Flow

`capacity activate-channels` is a fixed-size evidence run, not a QPS search. It discovers an already-running target, starts one temporary local worker, builds a group scenario whose run phase schedules exactly one SEND per generated channel, captures cold and active ChannelV2 runtime snapshots, holds the cluster without new sends, probes generated channel ranges through the bench runtime API, and writes `activation_report.json` plus `summary.md`.
```

- [ ] **Step 2: Verify docs**

```bash
rg -n "Capacity Activate-Channels Flow" internal/bench/FLOW.md
```

Expected: one match.

## Phase 04 Verification And Commit

- [ ] **Step 1: Run all phase tests**

```bash
GOWORK=off go test ./internal/bench/target ./internal/bench/capacity -count=1
```

Expected:

```text
ok  	github.com/WuKongIM/WuKongIM/internal/bench/target
ok  	github.com/WuKongIM/WuKongIM/internal/bench/capacity
```

- [ ] **Step 2: Commit**

```bash
git add internal/bench/target/client.go internal/bench/target/client_test.go internal/bench/capacity/activate_channels.go internal/bench/capacity/activate_channels_test.go internal/bench/capacity/activate_channels_result.go internal/bench/capacity/activate_channels_result_test.go internal/bench/FLOW.md
git commit -m "feat(wkbench): add activate channels runner"
```
