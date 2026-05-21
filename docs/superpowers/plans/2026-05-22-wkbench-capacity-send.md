# wkbench Capacity Send Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `wkbench capacity send`, a black-box subcommand that connects to already-running WuKongIM API nodes, discovers gateway TCP addresses through `/bench/v1/capacity-target`, and searches for maximum stable ingress send QPS with p50/p95/p99 reporting.

**Architecture:** Add a small benchmark-only capacity target endpoint in `internal/access/api`, extend the wkbench target client/model DTOs, then add `internal/bench/capacity` as a focused orchestration package around existing coordinator/worker/report primitives. Capacity attempts generate normal wkbench scenarios and run through a temporary local worker so no server internals or cluster-bypass branches are introduced.

**Tech Stack:** Go stdlib `flag`, `net/http`, `httptest`, `encoding/json`, existing `internal/bench/{coordinator,model,report,target,worker}` packages, Gin API handlers, YAML/JSON report writers.

---

## File Map

- Modify `internal/access/api/bench.go`: register and implement `GET /bench/v1/capacity-target` gated by existing bench API enablement.
- Modify `internal/access/api/bench_test.go`: add handler tests for disabled route and returned external gateway addresses.
- Modify `internal/bench/model/bench_api.go`: add `CapacityTarget` and `CapacityTargetGateway` DTOs.
- Modify `internal/bench/target/client.go`: add single-address `CapacityTarget` client helper and address-specific GET support.
- Modify `internal/bench/target/client_test.go`: test capacity target decoding, address iteration behavior, and error messages.
- Modify `internal/bench/workload/person.go`: record send success/error counters and latency with `phase`, `channel_type`, `profile`, and `traffic` labels.
- Modify `internal/bench/workload/group.go`: record group send success/error counters and latency with the same labels.
- Modify `internal/bench/workload/person_test.go` and `internal/bench/workload/group_test.go`: update or add assertions for phase-labeled metrics.
- Modify `internal/bench/report/report.go`: add measured-run send summary helpers for ingress QPS and p50/p95/p99.
- Modify `internal/bench/report/report_test.go`: test summary helpers use only `phase=run` metrics.
- Create `internal/bench/capacity/config.go`: capacity config defaults and validation.
- Create `internal/bench/capacity/discover.go`: health/readiness/capabilities/capacity-target discovery and gateway deduplication.
- Create `internal/bench/capacity/scenario.go`: generate deterministic wkbench scenarios for `person`, `group`, and `mixed` attempts.
- Create `internal/bench/capacity/search.go`: ramp plus binary search algorithm over an `AttemptRunner` interface.
- Create `internal/bench/capacity/runner.go`: temporary local worker server and coordinator attempt execution.
- Create `internal/bench/capacity/result.go`: result structs, attempt summaries, JSON/Markdown writers, console formatting.
- Create tests under `internal/bench/capacity/*_test.go`: config, discovery, scenario generation, search, and result writer tests.
- Modify `cmd/wkbench/main.go`: add `capacity send` CLI parsing and execution.
- Modify `cmd/wkbench/main_test.go`: add CLI parsing/dispatch tests.
- Modify `cmd/wkbench/README.md`: document `capacity send` usage.
- Modify `internal/bench/FLOW.md`: document capacity package and flow.

## Notes Before Starting

- Check for `FLOW.md` before reading a package. `internal/bench/FLOW.md` already applies to all `internal/bench` changes.
- Do not run long integration tests by default. Prefer targeted unit tests.
- Do not alter unrelated dirty worktree files.
- Keep comments in English for new exported types and important fields.
- Use TDD: write the failing test for each behavior before implementation.
- Avoid adding new configuration keys. This feature reuses `WK_EXTERNAL_TCPADDR`, `WK_EXTERNAL_WSADDR`, and `WK_EXTERNAL_WSSADDR`.

### Task 1: Add the capacity target API route

**Files:**
- Modify: `internal/access/api/bench.go`
- Modify: `internal/access/api/bench_test.go`

- [ ] **Step 1: Write failing test for disabled route**

Add to `internal/access/api/bench_test.go`:

```go
func TestBenchCapacityTargetDisabledReturnNotFound(t *testing.T) {
    srv := New(Options{})

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/bench/v1/capacity-target", nil)

    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusNotFound, rec.Code)
}
```

- [ ] **Step 2: Write failing test for enabled route using external addresses**

Add:

```go
func TestBenchCapacityTargetReturnsExternalGatewayAddresses(t *testing.T) {
    srv := New(Options{
        BenchEnabled: true,
        BenchData:    benchStub{},
        LegacyRouteExternal: LegacyRouteAddresses{
            TCPAddr: "127.0.0.1:15100",
            WSAddr:  "ws://127.0.0.1:15200",
            WSSAddr: "wss://127.0.0.1:15300",
        },
    })

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/bench/v1/capacity-target", nil)

    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusOK, rec.Code)
    require.JSONEq(t, `{
      "version":"bench/v1",
      "gateway":{
        "tcp_addr":"127.0.0.1:15100",
        "ws_addr":"ws://127.0.0.1:15200",
        "wss_addr":"wss://127.0.0.1:15300"
      }
    }`, rec.Body.String())
}
```

- [ ] **Step 3: Run tests and verify failure**

Run:

```bash
go test ./internal/access/api -run 'TestBenchCapacityTarget' -count=1
```

Expected: first test may pass as 404 from missing route; enabled route fails with 404.

- [ ] **Step 4: Implement route and handler**

In `internal/access/api/bench.go`, add route in `registerBenchRoutes`:

```go
s.engine.GET("/bench/v1/capacity-target", s.handleBenchCapacityTarget)
```

Add DTOs and handler:

```go
type benchCapacityTargetResponse struct {
    // Version is the benchmark API version that produced this target document.
    Version string `json:"version"`
    // Gateway contains gateway addresses published by this target node.
    Gateway benchCapacityGatewayResponse `json:"gateway"`
}

type benchCapacityGatewayResponse struct {
    // TCPAddr is the externally reachable WKProto TCP gateway address.
    TCPAddr string `json:"tcp_addr"`
    // WSAddr is the externally reachable WebSocket gateway address.
    WSAddr string `json:"ws_addr"`
    // WSSAddr is the externally reachable secure WebSocket gateway address.
    WSSAddr string `json:"wss_addr"`
}

func (s *Server) handleBenchCapacityTarget(c *gin.Context) {
    if c == nil {
        return
    }
    addr := s.legacyRouteExternal
    c.JSON(http.StatusOK, benchCapacityTargetResponse{
        Version: "bench/v1",
        Gateway: benchCapacityGatewayResponse{
            TCPAddr: addr.TCPAddr,
            WSAddr:  addr.WSAddr,
            WSSAddr: addr.WSSAddr,
        },
    })
}
```

No bench usecase call is needed; registration is already gated by `BenchEnabled`.

- [ ] **Step 5: Run tests and verify pass**

Run:

```bash
go test ./internal/access/api -run 'TestBenchCapacityTarget|TestBenchCapabilities' -count=1
```

Expected: PASS.

### Task 2: Add model and target client support for capacity target discovery

**Files:**
- Modify: `internal/bench/model/bench_api.go`
- Modify: `internal/bench/target/client.go`
- Modify: `internal/bench/target/client_test.go`

- [ ] **Step 1: Write failing client test for capacity target decoding**

Add to `internal/bench/target/client_test.go`:

```go
func TestClientCapacityTargetReadsGatewayAddresses(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        require.Equal(t, "/bench/v1/capacity-target", r.URL.Path)
        w.Header().Set("Content-Type", "application/json")
        _, _ = w.Write([]byte(`{"version":"bench/v1","gateway":{"tcp_addr":"127.0.0.1:15100","ws_addr":"ws://127.0.0.1:15200","wss_addr":""}}`))
    }))
    defer srv.Close()

    got, err := NewClient(Config{APIAddrs: []string{srv.URL}}).CapacityTarget(context.Background())

    require.NoError(t, err)
    require.Equal(t, "bench/v1", got.Version)
    require.Equal(t, "127.0.0.1:15100", got.Gateway.TCPAddr)
    require.Equal(t, "ws://127.0.0.1:15200", got.Gateway.WSAddr)
}
```

- [ ] **Step 2: Write failing test for deterministic fallback across API addresses**

Add:

```go
func TestClientCapacityTargetFallsBackAcrossAPIAddresses(t *testing.T) {
    bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        http.Error(w, "nope", http.StatusServiceUnavailable)
    }))
    defer bad.Close()
    good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        _, _ = w.Write([]byte(`{"version":"bench/v1","gateway":{"tcp_addr":"127.0.0.1:15101"}}`))
    }))
    defer good.Close()

    got, err := NewClient(Config{APIAddrs: []string{bad.URL, good.URL}}).CapacityTarget(context.Background())

    require.NoError(t, err)
    require.Equal(t, "127.0.0.1:15101", got.Gateway.TCPAddr)
}
```

- [ ] **Step 3: Run tests and verify failure**

Run:

```bash
go test ./internal/bench/target -run 'TestClientCapacityTarget' -count=1
```

Expected: compile failure for missing `CapacityTarget` method and DTOs.

- [ ] **Step 4: Add DTOs**

In `internal/bench/model/bench_api.go` add:

```go
// CapacityTarget describes the target node addresses needed by capacity tests.
type CapacityTarget struct {
    // Version is the benchmark API version that produced this target document.
    Version string `json:"version"`
    // Gateway contains gateway addresses published by this target node.
    Gateway CapacityTargetGateway `json:"gateway"`
}

// CapacityTargetGateway contains externally reachable gateway addresses.
type CapacityTargetGateway struct {
    // TCPAddr is the WKProto TCP gateway address used by wkbench workers.
    TCPAddr string `json:"tcp_addr"`
    // WSAddr is the WebSocket gateway address, reserved for future workers.
    WSAddr string `json:"ws_addr"`
    // WSSAddr is the secure WebSocket gateway address, reserved for future workers.
    WSSAddr string `json:"wss_addr"`
}
```

- [ ] **Step 5: Add client method**

In `internal/bench/target/client.go` add:

```go
// CapacityTarget reads the target node address document used by capacity tests.
func (c *Client) CapacityTarget(ctx context.Context) (model.CapacityTarget, error) {
    var out model.CapacityTarget
    if err := c.getAny(ctx, "/bench/v1/capacity-target", &out); err != nil {
        return model.CapacityTarget{}, fmt.Errorf("bench api capacity target unavailable: %w", err)
    }
    return out, nil
}
```

- [ ] **Step 6: Run tests and verify pass**

Run:

```bash
go test ./internal/bench/target -run 'TestClientCapacityTarget|TestClientCapabilities' -count=1
```

Expected: PASS.

### Task 3: Add phase-aware send metrics

**Files:**
- Modify: `internal/bench/workload/person.go`
- Modify: `internal/bench/workload/group.go`
- Modify: `internal/bench/workload/person_test.go`
- Modify: `internal/bench/workload/group_test.go`

- [ ] **Step 1: Write failing person metric label test**

Add to `internal/bench/workload/person_test.go` a focused test using existing test helpers in that file. If a ready helper exists for a successful send, extend it; otherwise add an assertion after an existing successful `RunWindow` test:

```go
snap := workload.Metrics().Collect()
require.Contains(t, snap.Counters, "person_send_success_total{channel_type=person,phase=run,profile=profile-a,traffic=traffic-a}")
require.Contains(t, snap.Histograms, "person_send_latency_seconds{channel_type=person,phase=run,profile=profile-a,traffic=traffic-a}")
```

Keep any existing unlabelled metric assertions until implementation is updated, then replace them.

- [ ] **Step 2: Write failing group metric label test**

Add similar assertions in `internal/bench/workload/group_test.go` after a successful group run:

```go
snap := workload.Metrics().Collect()
require.Contains(t, snap.Counters, "group_send_success_total{channel_type=group,phase=run,profile=group-profile,traffic=group-send}")
require.Contains(t, snap.Histograms, "group_send_latency_seconds{channel_type=group,phase=run,profile=group-profile,traffic=group-send}")
```

- [ ] **Step 3: Run workload tests and verify failure**

Run:

```bash
go test ./internal/bench/workload -run 'TestPerson|TestGroup' -count=1
```

Expected: tests fail because metrics are currently unlabelled.

- [ ] **Step 4: Add metric label helpers**

In `internal/bench/workload/person.go`, add helper methods near metric recording code:

```go
func (w *PersonWorkload) sendMetricLabels(phase string) metrics.Labels {
    return metrics.Labels{
        "phase":        strings.TrimSpace(phase),
        "channel_type": model.ChannelTypePerson,
        "profile":      w.cfg.ProfileName,
        "traffic":      w.cfg.TrafficName,
    }
}
```

In `internal/bench/workload/group.go`:

```go
func (w *GroupWorkload) sendMetricLabels(phase string) metrics.Labels {
    return metrics.Labels{
        "phase":        strings.TrimSpace(phase),
        "channel_type": model.ChannelTypeGroup,
        "profile":      w.cfg.ProfileName,
        "traffic":      w.cfg.TrafficName,
    }
}
```

- [ ] **Step 5: Use labels for send counters and latencies**

In `sendPairInPhase`, replace send success/error metric writes:

```go
labels := w.sendMetricLabels(phase)
...
w.metrics.IncCounter("person_send_error_total", labels)
...
w.metrics.IncCounter("person_send_success_total", labels)
w.metrics.ObserveLatency("person_send_latency_seconds", labels, time.Since(sendStart))
```

In group `sendToGroupInPhase`, do the same for `group_send_error_total`, `group_send_success_total`, and `group_send_latency_seconds`.

Do not change recv metric labels in this task unless tests require it; capacity only uses send metrics.

- [ ] **Step 6: Run workload tests and verify pass**

Run:

```bash
go test ./internal/bench/workload -run 'TestPerson|TestGroup' -count=1
```

Expected: PASS.

- [ ] **Step 7: Run summary report tests for compatibility**

Run:

```bash
go test ./internal/bench/report ./internal/bench/worker -count=1
```

Expected: may fail where old unlabelled counters were expected. Update tests to use labelled keys when appropriate, preserving legacy report aggregation behavior by metric name.

### Task 4: Add report helpers for measured-run send summary

**Files:**
- Modify: `internal/bench/report/report.go`
- Modify: `internal/bench/report/report_test.go`

- [ ] **Step 1: Write failing report summary helper test**

Add to `internal/bench/report/report_test.go`:

```go
func TestSendRunSummaryFromMetricsUsesOnlyRunPhase(t *testing.T) {
    snapshot := metrics.SnapshotData{
        Counters: map[string]uint64{
            "person_send_success_total{channel_type=person,phase=warmup,profile=p,traffic=t}": 100,
            "person_send_success_total{channel_type=person,phase=run,profile=p,traffic=t}":    200,
            "group_send_success_total{channel_type=group,phase=run,profile=g,traffic=t}":      100,
            "person_send_error_total{channel_type=person,phase=run,profile=p,traffic=t}":      1,
        },
        Histograms: map[string]metrics.HistogramSummary{
            "person_send_latency_seconds{channel_type=person,phase=warmup,profile=p,traffic=t}": {Count: 100, P50Seconds: 1, P95Seconds: 1, P99Seconds: 1},
            "person_send_latency_seconds{channel_type=person,phase=run,profile=p,traffic=t}":    {Count: 200, P50Seconds: 0.010, P95Seconds: 0.030, P99Seconds: 0.050},
            "group_send_latency_seconds{channel_type=group,phase=run,profile=g,traffic=t}":      {Count: 100, P50Seconds: 0.020, P95Seconds: 0.040, P99Seconds: 0.060},
        },
    }

    got := SendRunSummaryFromMetrics(snapshot, time.Minute)

    require.Equal(t, uint64(300), got.SendSuccess)
    require.Equal(t, uint64(1), got.SendErrors)
    require.InDelta(t, 5.0, got.IngressQPS, 0.001)
    require.Equal(t, 20*time.Millisecond, got.SendackP50)
    require.Equal(t, 40*time.Millisecond, got.SendackP95)
    require.Equal(t, 60*time.Millisecond, got.SendackP99)
}
```

- [ ] **Step 2: Run test and verify failure**

Run:

```bash
go test ./internal/bench/report -run TestSendRunSummaryFromMetricsUsesOnlyRunPhase -count=1
```

Expected: compile failure for missing helper/type.

- [ ] **Step 3: Implement summary type and helper**

Add to `internal/bench/report/report.go`:

```go
// SendRunSummary contains measured-run send throughput and latency stats.
type SendRunSummary struct {
    // SendSuccess is successful sendack count during measured run.
    SendSuccess uint64 `json:"send_success"`
    // SendErrors is failed send/sendack count during measured run.
    SendErrors uint64 `json:"send_errors"`
    // IngressQPS is SendSuccess divided by measured duration seconds.
    IngressQPS float64 `json:"ingress_qps"`
    // SendackP50 is the maximum worker-local run-phase sendack p50 latency.
    SendackP50 time.Duration `json:"sendack_p50"`
    // SendackP95 is the maximum worker-local run-phase sendack p95 latency.
    SendackP95 time.Duration `json:"sendack_p95"`
    // SendackP99 is the maximum worker-local run-phase sendack p99 latency.
    SendackP99 time.Duration `json:"sendack_p99"`
}

// SendRunSummaryFromMetrics derives measured-run send throughput and latency from metrics.
func SendRunSummaryFromMetrics(snapshot metrics.SnapshotData, duration time.Duration) SendRunSummary {
    success := counterSumWithPhase(snapshot, "run", "person_send_success_total", "group_send_success_total", "sendack_success_total")
    errors := counterSumWithPhase(snapshot, "run", "person_send_error_total", "group_send_error_total", "sendack_error_total")
    return SendRunSummary{
        SendSuccess: success,
        SendErrors:  errors,
        IngressQPS:  qps(success, duration),
        SendackP50:  maxHistogramPercentileWithPhase(snapshot, "run", 50, "person_send_latency_seconds", "group_send_latency_seconds", "sendack_latency_seconds"),
        SendackP95:  maxHistogramPercentileWithPhase(snapshot, "run", 95, "person_send_latency_seconds", "group_send_latency_seconds", "sendack_latency_seconds"),
        SendackP99:  maxHistogramPercentileWithPhase(snapshot, "run", 99, "person_send_latency_seconds", "group_send_latency_seconds", "sendack_latency_seconds"),
    }
}
```

Implement private helpers by parsing the existing series key labels. Since `parseSeriesKey` is private to `metrics`, use simple local parsing modeled after report's existing `metricName` helper:

```go
func counterSumWithPhase(snapshot metrics.SnapshotData, phase string, names ...string) uint64 { ... }
func maxHistogramPercentileWithPhase(snapshot metrics.SnapshotData, phase string, percentile int, names ...string) time.Duration { ... }
func seriesHasPhase(key, phase string) bool { ... }
func qps(count uint64, duration time.Duration) float64 { ... }
```

Keep the helper tolerant: unlabelled legacy series should be ignored for capacity run summaries because the capacity result must use measured run only.

- [ ] **Step 4: Run report tests and verify pass**

Run:

```bash
go test ./internal/bench/report -count=1
```

Expected: PASS.

### Task 5: Add capacity config defaults and validation

**Files:**
- Create: `internal/bench/capacity/config.go`
- Create: `internal/bench/capacity/config_test.go`

- [ ] **Step 1: Write failing config defaults test**

Create `internal/bench/capacity/config_test.go`:

```go
package capacity

import (
    "testing"
    "time"

    "github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
    cfg := DefaultConfig()

    require.Equal(t, ProfileMixed, cfg.Profile)
    require.Equal(t, 100.0, cfg.StartQPS)
    require.Equal(t, 5000.0, cfg.MaxQPS)
    require.Equal(t, 1.5, cfg.StepFactor)
    require.Equal(t, 30*time.Second, cfg.Duration)
    require.Equal(t, 10*time.Second, cfg.Warmup)
    require.Equal(t, 3*time.Second, cfg.Cooldown)
    require.Equal(t, 200*time.Millisecond, cfg.StableP99)
    require.Equal(t, 0.95, cfg.MinActualRatio)
    require.True(t, cfg.BinarySearch)
    require.Equal(t, 0.05, cfg.BinarySearchMinDeltaRatio)
    require.Equal(t, 10, cfg.GroupMembers)
}
```

- [ ] **Step 2: Write failing validation tests**

Add:

```go
func TestConfigValidateRequiresAPIAddrs(t *testing.T) {
    cfg := DefaultConfig()
    cfg.APIAddrs = nil

    require.ErrorContains(t, cfg.Validate(), "api")
}

func TestConfigValidateRejectsInvalidProfile(t *testing.T) {
    cfg := DefaultConfig()
    cfg.APIAddrs = []string{"http://127.0.0.1:15001"}
    cfg.Profile = "bad"

    require.ErrorContains(t, cfg.Validate(), "profile")
}
```

- [ ] **Step 3: Run tests and verify failure**

Run:

```bash
go test ./internal/bench/capacity -run 'TestDefaultConfig|TestConfigValidate' -count=1
```

Expected: package or symbol missing failure.

- [ ] **Step 4: Implement config**

Create `internal/bench/capacity/config.go`:

```go
package capacity

import (
    "fmt"
    "math"
    "strings"
    "time"
)

const (
    // ProfilePerson measures one-to-one send path capacity.
    ProfilePerson = "person"
    // ProfileGroup measures group send path capacity.
    ProfileGroup = "group"
    // ProfileMixed splits offered ingress load across person and group traffic.
    ProfileMixed = "mixed"
)

// Config controls one capacity send search.
type Config struct {
    // APIAddrs are HTTP API base addresses for already-running target nodes.
    APIAddrs []string
    // GatewayTCPAddrs optionally overrides discovered WKProto TCP gateway addresses.
    GatewayTCPAddrs []string
    // BenchToken is an optional bearer token for bench API routes.
    BenchToken string
    // Profile selects person, group, or mixed traffic.
    Profile string
    // StartQPS is the first offered ingress QPS attempted.
    StartQPS float64
    // MaxQPS is the maximum offered ingress QPS attempted.
    MaxQPS float64
    // StepFactor multiplies passing ramp attempts.
    StepFactor float64
    // Duration is the measured run duration per attempt.
    Duration time.Duration
    // Warmup is the warmup duration per attempt.
    Warmup time.Duration
    // Cooldown is the cooldown duration per attempt.
    Cooldown time.Duration
    // StableP99 is the maximum allowed run-phase sendack p99 latency.
    StableP99 time.Duration
    // MinActualRatio is the minimum actual/offered QPS ratio for pass.
    MinActualRatio float64
    // MaxSendackErrorRate is the maximum allowed run send error rate.
    MaxSendackErrorRate float64
    // MaxConnectErrorRate is the maximum allowed connect error rate.
    MaxConnectErrorRate float64
    // BinarySearch enables refinement after a ramp failure brackets capacity.
    BinarySearch bool
    // BinarySearchMinDeltaRatio stops binary search when bracket width is small enough.
    BinarySearchMinDeltaRatio float64
    // GroupMembers is the number of members per generated group channel.
    GroupMembers int
    // ReportDir is the root directory for capacity reports.
    ReportDir string
}

// DefaultConfig returns laptop-safe defaults for capacity send searches.
func DefaultConfig() Config { ... }

// Validate checks static capacity config before discovery or execution.
func (c Config) Validate() error { ... }
```

Validation rules: non-empty API addrs, supported profile, positive start/max QPS, max >= start, step factor > 1, positive duration/warmup/cooldown, stable p99 > 0, min ratio in `(0,1]`, non-negative error rates, binary min delta > 0, group members > 0.

- [ ] **Step 5: Run tests and verify pass**

Run:

```bash
go test ./internal/bench/capacity -run 'TestDefaultConfig|TestConfigValidate' -count=1
```

Expected: PASS.

### Task 6: Add capacity discovery

**Files:**
- Create: `internal/bench/capacity/discover.go`
- Create: `internal/bench/capacity/discover_test.go`

- [ ] **Step 1: Write failing discovery test with fake API servers**

Create `internal/bench/capacity/discover_test.go`:

```go
func TestDiscoverTargetCollectsCapacityGatewayAddrs(t *testing.T) {
    api1 := newCapacityTargetServer(t, "127.0.0.1:15100")
    defer api1.Close()
    api2 := newCapacityTargetServer(t, "127.0.0.1:15101")
    defer api2.Close()

    got, err := DiscoverTarget(context.Background(), Config{APIAddrs: []string{api1.URL, api2.URL}})

    require.NoError(t, err)
    require.Equal(t, []string{api1.URL, api2.URL}, got.Target.API.Addrs)
    require.Equal(t, []string{"127.0.0.1:15100", "127.0.0.1:15101"}, got.Target.Gateway.TCP.Addrs)
    require.Equal(t, []string{api1.URL, api2.URL}, got.Target.BenchAPI.Addrs)
    require.True(t, got.Target.BenchAPI.Enabled)
}
```

Add helper server that serves `/healthz`, `/readyz`, `/bench/v1/capabilities`, and `/bench/v1/capacity-target`.

- [ ] **Step 2: Write failing test for manual gateway override**

Add:

```go
func TestDiscoverTargetManualGatewayOverride(t *testing.T) {
    api := newCapacityTargetServer(t, "")
    defer api.Close()

    got, err := DiscoverTarget(context.Background(), Config{
        APIAddrs:        []string{api.URL},
        GatewayTCPAddrs: []string{"127.0.0.1:19999"},
    })

    require.NoError(t, err)
    require.Equal(t, []string{"127.0.0.1:19999"}, got.Target.Gateway.TCP.Addrs)
}
```

- [ ] **Step 3: Run tests and verify failure**

Run:

```bash
go test ./internal/bench/capacity -run TestDiscoverTarget -count=1
```

Expected: missing `DiscoverTarget`.

- [ ] **Step 4: Implement discovery**

Create `internal/bench/capacity/discover.go`:

```go
// DiscoveredTarget contains capacity target discovery output.
type DiscoveredTarget struct {
    // Target is the wkbench target config built from API and gateway discovery.
    Target model.Target `json:"target"`
    // CapacityTargets are the raw per-node capacity target documents.
    CapacityTargets []model.CapacityTarget `json:"capacity_targets"`
}

// DiscoverTarget checks target APIs and builds the wkbench target config.
func DiscoverTarget(ctx context.Context, cfg Config) (DiscoveredTarget, error) { ... }
```

Implementation details:

- Call `cfg.Validate()` first.
- For each API address, create a `target.Client` with exactly one address and optional token.
- Call `Healthz`, `Readyz`, `Capabilities`, validate version/enabled roughly using coordinator preflight semantics or minimally verify `Enabled && Version == "bench/v1"`.
- If `cfg.GatewayTCPAddrs` is empty, call `CapacityTarget` and collect non-empty `Gateway.TCPAddr`.
- Deduplicate while preserving order.
- Build `model.Target{Name: "capacity-send", API: ..., BenchAPI: ..., Gateway: ...}`.

- [ ] **Step 5: Run tests and verify pass**

Run:

```bash
go test ./internal/bench/capacity -run TestDiscoverTarget -count=1
```

Expected: PASS.

### Task 7: Add scenario generation for capacity attempts

**Files:**
- Create: `internal/bench/capacity/scenario.go`
- Create: `internal/bench/capacity/scenario_test.go`

- [ ] **Step 1: Write failing mixed scenario test**

Create `internal/bench/capacity/scenario_test.go`:

```go
func TestBuildScenarioMixedSplitsOfferedQPS(t *testing.T) {
    cfg := DefaultConfig()
    cfg.APIAddrs = []string{"http://127.0.0.1:15001"}
    cfg.Profile = ProfileMixed
    cfg.Duration = time.Minute
    cfg.Warmup = time.Second
    cfg.Cooldown = time.Second
    cfg.GroupMembers = 10

    s := BuildScenario(cfg, Attempt{Index: 2, OfferedQPS: 100})

    require.Equal(t, "wkbench/v1", s.Version)
    require.Contains(t, s.Run.ID, "capacity-send")
    require.Equal(t, time.Minute, s.Run.Duration)
    require.Len(t, s.Channels.Profiles, 2)
    require.Equal(t, "person-chat", s.Channels.Profiles[0].Name)
    require.Equal(t, 50, s.Channels.Profiles[0].Count)
    require.Equal(t, "small-group", s.Channels.Profiles[1].Name)
    require.Equal(t, 50, s.Channels.Profiles[1].Count)
    require.Equal(t, 10, s.Channels.Profiles[1].Members.Count)
    require.Len(t, s.Messages.Traffic, 2)
    require.Equal(t, 1.0, s.Messages.Traffic[0].RatePerChannel.PerSecond)
    require.Equal(t, 1.0, s.Messages.Traffic[1].RatePerChannel.PerSecond)
    require.Equal(t, "none", s.Messages.Traffic[0].Verify.Recv.Mode)
}
```

- [ ] **Step 2: Write failing person/group tests**

Add tests that `ProfilePerson` produces only person profile and `ProfileGroup` produces only group profile.

- [ ] **Step 3: Run tests and verify failure**

Run:

```bash
go test ./internal/bench/capacity -run TestBuildScenario -count=1
```

Expected: missing `BuildScenario` and `Attempt`.

- [ ] **Step 4: Implement scenario generation**

In `scenario.go`:

```go
// Attempt identifies one offered-QPS attempt in the capacity search.
type Attempt struct {
    // Index is the zero-based attempt index.
    Index int `json:"index"`
    // OfferedQPS is the target ingress QPS for this attempt.
    OfferedQPS float64 `json:"offered_qps"`
}

// BuildScenario creates a normal wkbench scenario for one capacity attempt.
func BuildScenario(cfg Config, attempt Attempt) model.Scenario { ... }
```

Rules:

- Set `Run.ID` to deterministic `capacity-send-<profile>-<index>-<qps>`.
- Set `Run.Duration/Warmup/Cooldown/FailFast/ReportDir` from config and attempt.
- Use token mode `bench_api`.
- Use payload size `128`, deterministic mode.
- Use `connect_rate` high enough for generated users, e.g. `1000/s`.
- Person channel count = `ceil(personQPS / 1.0)`.
- Group channel count = `ceil(groupQPS / 1.0)`.
- `online.total_users` must cover person participants and group members. Use a simple safe formula: `max(personChannels*2 + groupChannels*groupMembers, groupMembers, 1)` for disallowed overlap, or smaller if using allowed overlap. Prefer clear and safe over minimal.
- Hard limits: worker failed 0, connect/sendack/recv error rate 0. Soft p99 can mirror `cfg.StableP99`.

- [ ] **Step 5: Run tests and verify pass**

Run:

```bash
go test ./internal/bench/capacity -run TestBuildScenario -count=1
```

Expected: PASS.

### Task 8: Add capacity search algorithm

**Files:**
- Create: `internal/bench/capacity/search.go`
- Create: `internal/bench/capacity/search_test.go`

- [ ] **Step 1: Write failing ramp/binary search test**

Create `internal/bench/capacity/search_test.go`:

```go
func TestSearchFindsHighestStableQPS(t *testing.T) {
    cfg := DefaultConfig()
    cfg.APIAddrs = []string{"http://127.0.0.1:15001"}
    cfg.StartQPS = 100
    cfg.MaxQPS = 1000
    cfg.StepFactor = 2
    cfg.BinarySearch = true
    cfg.BinarySearchMinDeltaRatio = 0.10

    runner := attemptRunnerFunc(func(ctx context.Context, attempt Attempt) (AttemptResult, error) {
        passed := attempt.OfferedQPS <= 400
        return AttemptResult{
            Attempt:   attempt,
            Passed:    passed,
            ActualQPS:  attempt.OfferedQPS,
            SendackP99: 50 * time.Millisecond,
        }, nil
    })

    got, err := Search(context.Background(), cfg, runner)

    require.NoError(t, err)
    require.InDelta(t, 400, got.MaxStableQPS, 50)
    require.NotNil(t, got.StableAttempt)
    require.NotNil(t, got.FailedAttempt)
    require.NotEmpty(t, got.Attempts)
}
```

- [ ] **Step 2: Write failing test for no stable attempt**

Add a runner that always returns `Passed=false`; expect `StatusFailed` and no stable attempt.

- [ ] **Step 3: Run tests and verify failure**

Run:

```bash
go test ./internal/bench/capacity -run TestSearch -count=1
```

Expected: missing search types.

- [ ] **Step 4: Implement search types and algorithm**

In `search.go` define:

```go
// AttemptRunner runs one offered-QPS attempt.
type AttemptRunner interface {
    RunAttempt(ctx context.Context, attempt Attempt) (AttemptResult, error)
}

type attemptRunnerFunc func(context.Context, Attempt) (AttemptResult, error)
func (f attemptRunnerFunc) RunAttempt(ctx context.Context, a Attempt) (AttemptResult, error) { return f(ctx, a) }

// AttemptResult summarizes one capacity attempt.
type AttemptResult struct { ... }

// Result summarizes a capacity search.
type Result struct { ... }

func Search(ctx context.Context, cfg Config, runner AttemptRunner) (Result, error) { ... }
```

Use ramp then binary search exactly as in the spec. Ensure attempt indexes are monotonic.

- [ ] **Step 5: Run tests and verify pass**

Run:

```bash
go test ./internal/bench/capacity -run TestSearch -count=1
```

Expected: PASS.

### Task 9: Add capacity attempt runner with temporary worker and coordinator

**Files:**
- Create: `internal/bench/capacity/runner.go`
- Create: `internal/bench/capacity/runner_test.go`

- [ ] **Step 1: Write failing test for attempt pass/fail classification from report**

Use a fake coordinator runner interface rather than real network first. Add in `runner.go` an internal seam:

```go
type coordinatorRunner interface {
    Run(ctx context.Context, scenario model.Scenario) (coordinator.RunResult, error)
}
```

Write test in `runner_test.go` that constructs a fake result with report metrics and verifies `AttemptResult.Passed` based on criteria.

- [ ] **Step 2: Run test and verify failure**

Run:

```bash
go test ./internal/bench/capacity -run TestAttemptRunner -count=1
```

Expected: missing runner implementation.

- [ ] **Step 3: Implement attempt evaluation helper**

Add a function:

```go
func EvaluateAttempt(cfg Config, attempt Attempt, rep report.Report) AttemptResult { ... }
```

It should call `report.SendRunSummaryFromMetrics(rep.Metrics, cfg.Duration)` and set failure reason in priority order:

1. worker failed
2. connect error rate exceeded
3. sendack error rate exceeded
4. actual QPS below `offered * minActualRatio`
5. p99 exceeded

- [ ] **Step 4: Implement real Runner skeleton**

Add:

```go
// Runner executes capacity attempts against a discovered target.
type Runner struct { ... }

func NewRunner(cfg Config, discovered DiscoveredTarget) *Runner { ... }
func (r *Runner) Run(ctx context.Context) (Result, error) { ... }
func (r *Runner) RunAttempt(ctx context.Context, attempt Attempt) (AttemptResult, error) { ... }
```

Implementation requirements:

- Start a local `httptest.Server` or `net.Listen("tcp", "127.0.0.1:0")` plus `worker.NewServer` for the duration of the capacity run.
- Worker config should use `InsecureControl: true` and default workload runner.
- Build one `model.Worker{ID:"local-capacity-worker", Addr: server.URL, Weight:1, InsecureControl:true}`.
- For each attempt, call `BuildScenario`, set attempt report dir, then `coordinator.New(...).Run`.
- Use existing coordinator result report.
- Stop/close local worker server at the end.

- [ ] **Step 5: Add targeted test for temporary worker config without full traffic**

If full coordinator traffic is too heavy for unit tests, test only that `NewRunner` builds a worker set with insecure local worker and that `EvaluateAttempt` is correct. Do not write slow network load tests as unit tests.

- [ ] **Step 6: Run capacity package tests**

Run:

```bash
go test ./internal/bench/capacity -count=1
```

Expected: PASS.

### Task 10: Add result writers and console formatting

**Files:**
- Create: `internal/bench/capacity/result.go`
- Create: `internal/bench/capacity/result_test.go`

- [ ] **Step 1: Write failing result writer test**

Create `result_test.go`:

```go
func TestWriteResultWritesJSONAndSummary(t *testing.T) {
    dir := t.TempDir()
    result := Result{
        Status:       StatusPassed,
        Profile:      ProfileMixed,
        MaxStableQPS: 500,
        Attempts: []AttemptResult{{
            Attempt:   Attempt{Index: 0, OfferedQPS: 500},
            Passed:    true,
            ActualQPS: 498,
            SendackP50: 10 * time.Millisecond,
            SendackP95: 20 * time.Millisecond,
            SendackP99: 30 * time.Millisecond,
        }},
    }

    require.NoError(t, WriteResult(dir, result))
    require.FileExists(t, filepath.Join(dir, "result.json"))
    require.FileExists(t, filepath.Join(dir, "summary.md"))
}
```

- [ ] **Step 2: Run test and verify failure**

Run:

```bash
go test ./internal/bench/capacity -run TestWriteResult -count=1
```

Expected: missing writer.

- [ ] **Step 3: Implement writers**

Add in `result.go`:

```go
func WriteResult(dir string, result Result) error { ... }
func SummaryMarkdown(result Result) string { ... }
func ConsoleSummary(result Result) string { ... }
```

Use `json.MarshalIndent`, create directories with `0o755`, files with `0o644`. Keep output deterministic.

- [ ] **Step 4: Run tests and verify pass**

Run:

```bash
go test ./internal/bench/capacity -run 'TestWriteResult|TestConsoleSummary' -count=1
```

Expected: PASS.

### Task 11: Add CLI command parsing and dispatch

**Files:**
- Modify: `cmd/wkbench/main.go`
- Modify: `cmd/wkbench/main_test.go`

- [ ] **Step 1: Write failing unknown/usage tests**

In `cmd/wkbench/main_test.go`, add tests matching existing style:

```go
func TestRunCapacityRequiresSubcommand(t *testing.T) {
    var stderr bytes.Buffer
    code := runWithStderr([]string{"capacity"}, &stderr)

    require.Equal(t, exitConfig, code)
    require.Contains(t, stderr.String(), "usage: wkbench capacity <send>")
}
```

- [ ] **Step 2: Write failing config validation CLI test**

Add:

```go
func TestRunCapacitySendRequiresAPI(t *testing.T) {
    var stderr bytes.Buffer
    code := runWithStderr([]string{"capacity", "send"}, &stderr)

    require.Equal(t, exitConfig, code)
    require.Contains(t, stderr.String(), "--api is required")
}
```

- [ ] **Step 3: Run tests and verify failure**

Run:

```bash
go test ./cmd/wkbench -run 'TestRunCapacity' -count=1
```

Expected: unknown command failure or missing usage.

- [ ] **Step 4: Implement command dispatch**

In `cmd/wkbench/main.go`:

- Import `github.com/WuKongIM/WuKongIM/internal/bench/capacity`.
- Add `capacity` to usage string.
- Add case:

```go
case "capacity":
    return runCapacity(args[1:], stderr)
```

Implement:

```go
func runCapacity(args []string, stderr io.Writer) int { ... }
func runCapacitySend(args []string, stderr io.Writer) int { ... }
func parseCapacitySendConfig(args []string, stderr io.Writer) (capacity.Config, int) { ... }
```

Parse comma-separated `--api` and `--gateway`, durations, floats, bools. If no `--api`, print `--api is required` and return `exitConfig`.

- [ ] **Step 5: Wire execution**

`runCapacitySend` should:

```go
cfg, code := parseCapacitySendConfig(...)
if code != 0 { return code }
discovered, err := capacity.DiscoverTarget(context.Background(), cfg)
if err != nil { fmt.Fprintf(stderr, "capacity preflight failed: %v\n", err); return exitPreflight }
runner := capacity.NewRunner(cfg, discovered)
result, err := runner.Run(context.Background())
if writeErr := capacity.WriteResult(result.ReportDir, result); writeErr != nil { ... }
fmt.Fprint(stderr, capacity.ConsoleSummary(result))
return result.ExitCode()
```

If `Result.ExitCode()` does not exist yet, add it in capacity result types.

- [ ] **Step 6: Run CLI tests**

Run:

```bash
go test ./cmd/wkbench -run 'TestRunCapacity|TestRunWithNoArgs' -count=1
```

Expected: PASS.

### Task 12: End-to-end package validation and docs

**Files:**
- Modify: `cmd/wkbench/README.md`
- Modify: `internal/bench/FLOW.md`

- [ ] **Step 1: Update README command table**

In `cmd/wkbench/README.md`, add `capacity` to the command table:

```markdown
| `capacity send` | Searches maximum stable ingress send QPS against already-running target APIs. |
```

Add usage section after existing `run` or dev-sim docs:

```markdown
## Capacity Send

`capacity send` connects to already-running WuKongIM API nodes, discovers gateway TCP addresses through `/bench/v1/capacity-target`, starts a temporary local worker, and searches for maximum stable ingress send QPS.

```bash
wkbench capacity send \
  --api http://127.0.0.1:15001,http://127.0.0.1:15002,http://127.0.0.1:15003
```

The command does not start Docker Compose services. Enable `WK_BENCH_API_ENABLE=true` and configure `WK_EXTERNAL_TCPADDR` on each target node.
```

- [ ] **Step 2: Update internal bench flow**

In `internal/bench/FLOW.md`:

- Add `capacity` to Package Roles.
- Add a `Capacity Send Flow` section with:

```text
cmd/wkbench capacity send
  -> capacity.DiscoverTarget
       -> /healthz, /readyz, /bench/v1/capabilities, /bench/v1/capacity-target
  -> start temporary local worker
  -> capacity.Search
       -> BuildScenario per offered QPS
       -> coordinator.Run
       -> report.SendRunSummaryFromMetrics
  -> write result.json and summary.md
```

- [ ] **Step 3: Run targeted test suite**

Run:

```bash
go test ./internal/access/api ./internal/bench/model ./internal/bench/target ./internal/bench/workload ./internal/bench/report ./internal/bench/capacity ./cmd/wkbench -count=1
```

Expected: PASS.

- [ ] **Step 4: Run broader bench tests**

Run:

```bash
go test ./internal/bench/... ./cmd/wkbench -count=1
```

Expected: PASS. If this is too slow or fails for unrelated existing dirty-worktree reasons, capture the failure details and continue only after determining whether it is related.

- [ ] **Step 5: Update implementation status in final response**

Summarize changed files, tests run, and any known limitations such as max QPS being environment-specific and group fanout not being reported as primary QPS.

## Commit Guidance

The worktree is currently dirty with unrelated changes. Do not commit unless the user explicitly asks. If committing later, commit only files touched by this plan.

## Suggested Execution Order

1. Tasks 1-2: server endpoint and client DTO.
2. Tasks 3-4: measured-run metrics and summary extraction.
3. Tasks 5-8: capacity config, discovery, scenario generation, and search.
4. Tasks 9-10: real runner and result output.
5. Tasks 11-12: CLI and docs.
