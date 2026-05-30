# wkbench Activate Channels 01 API Contract Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the shared bench API contract, target client methods, and internalv2 HTTP routes needed by the 10k channel activation benchmark.

**Architecture:** Keep JSON DTOs in `internal/bench/model` because they are wkbench/HTTP contract types. Keep `internalv2/access/api` as a thin adapter: it validates request shape, advertises capabilities, and calls an injected `ChannelRuntimeBenchController`. This phase uses fake controllers only; real ChannelV2 runtime wiring is Phase 02.

**Tech Stack:** Go stdlib HTTP/JSON, `internal/bench/model`, `internal/bench/target`, `internalv2/access/api`, `httptest`, `GOWORK=off go test`.

---

## Files

- Modify: `internal/bench/model/bench_api.go`
- Modify: `internal/bench/target/client.go`
- Modify: `internal/bench/target/client_test.go`
- Modify: `internalv2/access/api/server.go`
- Modify: `internalv2/access/api/server_test.go`
- Create: `internalv2/access/api/bench_runtime.go`
- Create: `internalv2/access/api/bench_runtime_test.go`

## Task 1: Shared DTOs

**Files:**

- Modify: `internal/bench/model/bench_api.go`

- [ ] **Step 1: Add runtime capability fields**

Add these fields to `BenchCapabilitiesSupports`:

```go
// ChannelRuntimeSnapshot indicates support for local ChannelV2 runtime snapshots.
ChannelRuntimeSnapshot bool `json:"channel_runtime_snapshot"`
// ChannelRuntimeProbe indicates support for bounded ChannelV2 runtime probes.
ChannelRuntimeProbe bool `json:"channel_runtime_probe"`
// ChannelRuntimeEvict indicates support for bounded ChannelV2 runtime eviction.
ChannelRuntimeEvict bool `json:"channel_runtime_evict"`
// ChannelRuntimeFaults indicates support for runtime fault injection controls.
ChannelRuntimeFaults bool `json:"channel_runtime_faults"`
// ChannelRuntimeActivate indicates support for server-side diagnostic activation.
ChannelRuntimeActivate bool `json:"channel_runtime_activate"`
```

- [ ] **Step 2: Add runtime request/response DTOs**

Add these types below `BenchSnapshot`:

```go
// ChannelRuntimeRange selects generated channel indexes in [start,end).
type ChannelRuntimeRange struct {
	// Start is the first generated channel index included by the selector.
	Start int `json:"start"`
	// End is one past the last generated channel index included by the selector.
	End int `json:"end"`
}

// ChannelRuntimeQuery selects one generated benchmark channel set.
type ChannelRuntimeQuery struct {
	// RunID identifies the benchmark run that generated the channels.
	RunID string `json:"run_id"`
	// Profile identifies the generated channel profile.
	Profile string `json:"profile"`
	// ChannelType is the WuKong channel type, group is currently 2.
	ChannelType uint8 `json:"channel_type"`
	// Range selects generated channel indexes in [start,end).
	Range ChannelRuntimeRange `json:"range"`
}

type ChannelRuntimeSnapshot struct {
	Version                 string                          `json:"version"`
	NodeID                  uint64                          `json:"node_id"`
	RunID                   string                          `json:"run_id,omitempty"`
	Profile                 string                          `json:"profile,omitempty"`
	ActiveTotal             int                             `json:"active_total"`
	ActiveLeader            int                             `json:"active_leader"`
	ActiveFollower          int                             `json:"active_follower"`
	FollowerParked          int                             `json:"follower_parked"`
	ActivationRejectedTotal uint64                          `json:"activation_rejected_total"`
	Reactors                []ChannelRuntimeReactorSnapshot `json:"reactors,omitempty"`
	WorkerQueues            []ChannelRuntimeWorkerQueue     `json:"worker_queues,omitempty"`
}

type ChannelRuntimeReactorSnapshot struct {
	ReactorID    int `json:"reactor_id"`
	Leader       int `json:"leader"`
	Follower     int `json:"follower"`
	Parked       int `json:"parked"`
	MailboxDepth int `json:"mailbox_depth"`
}

type ChannelRuntimeWorkerQueue struct {
	Pool  string `json:"pool"`
	Depth int    `json:"depth"`
}

type ChannelRuntimeProbeRequest struct {
	RunID       string              `json:"run_id"`
	Profile     string              `json:"profile"`
	ChannelType uint8               `json:"channel_type"`
	Range       ChannelRuntimeRange `json:"range"`
}

type ChannelRuntimeProbeResult struct {
	Version        string   `json:"version"`
	NodeID         uint64   `json:"node_id"`
	RunID          string   `json:"run_id"`
	Profile        string   `json:"profile"`
	Checked        int      `json:"checked"`
	LoadedLeader   int      `json:"loaded_leader"`
	LoadedFollower int      `json:"loaded_follower"`
	Missing        []string `json:"missing,omitempty"`
}

type ChannelRuntimeEvictRequest struct {
	RunID       string              `json:"run_id"`
	Profile     string              `json:"profile"`
	ChannelType uint8               `json:"channel_type"`
	Range       ChannelRuntimeRange `json:"range"`
}

type ChannelRuntimeEvictResult struct {
	Version     string `json:"version"`
	NodeID      uint64 `json:"node_id"`
	RunID       string `json:"run_id"`
	Profile     string `json:"profile"`
	Requested   int    `json:"requested"`
	Evicted     int    `json:"evicted"`
	SkippedBusy int    `json:"skipped_busy"`
	Missing     int    `json:"missing"`
}
```

- [ ] **Step 3: Test**

Run:

```bash
GOWORK=off go test ./internal/bench/model -count=1
```

Expected:

```text
ok  	github.com/WuKongIM/WuKongIM/internal/bench/model
```

## Task 2: Target Client Runtime Calls

**Files:**

- Modify: `internal/bench/target/client.go`
- Modify: `internal/bench/target/client_test.go`

- [ ] **Step 1: Add tests**

Add tests for:

```go
func TestClientChannelRuntimeSnapshotsCallsEveryTarget(t *testing.T)
func TestClientProbeChannelRuntimePostsRequest(t *testing.T)
func TestClientEvictChannelRuntimePostsRequest(t *testing.T)
```

The first test must create two `httptest.Server` instances and assert both receive `GET /bench/v1/channel-runtime/snapshot?run_id=run-a&profile=activate-groups`. The POST tests must assert the path is `/bench/v1/channel-runtime/probe` or `/bench/v1/channel-runtime/evict` and the decoded body contains `Range{Start: 0, End: 10}`.

- [ ] **Step 2: Run failing test**

Run:

```bash
GOWORK=off go test ./internal/bench/target -count=1
```

Expected:

```text
FAIL
... ChannelRuntimeSnapshots undefined ...
```

- [ ] **Step 3: Implement client methods**

Add methods:

```go
func (c *Client) ChannelRuntimeSnapshots(ctx context.Context, query model.ChannelRuntimeQuery) ([]model.ChannelRuntimeSnapshot, error)
func (c *Client) ProbeChannelRuntime(ctx context.Context, req model.ChannelRuntimeProbeRequest) (model.ChannelRuntimeProbeResult, error)
func (c *Client) EvictChannelRuntime(ctx context.Context, req model.ChannelRuntimeEvictRequest) (model.ChannelRuntimeEvictResult, error)
```

Implementation rules:

- Snapshot calls every configured API address and fails if any address fails.
- Probe and evict use first-success semantics like existing mutation calls.
- Snapshot query params are `run_id`, `profile`, `channel_type`, `start`, and `end`; omit zero-valued optional params.
- Add `postAnyOut` rather than changing existing `postAny` behavior.

- [ ] **Step 4: Test**

Run:

```bash
GOWORK=off go test ./internal/bench/target -count=1
```

Expected:

```text
ok  	github.com/WuKongIM/WuKongIM/internal/bench/target
```

## Task 3: internalv2 HTTP Surface

**Files:**

- Create: `internalv2/access/api/bench_runtime.go`
- Create: `internalv2/access/api/bench_runtime_test.go`
- Modify: `internalv2/access/api/server.go`
- Modify: `internalv2/access/api/server_test.go`

- [ ] **Step 1: Add controller interface and options**

In `server.go`, import `internal/bench/model` and add:

```go
// ChannelRuntimeBenchController exposes benchmark-only ChannelV2 runtime controls.
type ChannelRuntimeBenchController interface {
	Snapshot(context.Context, model.ChannelRuntimeQuery) (model.ChannelRuntimeSnapshot, error)
	Probe(context.Context, model.ChannelRuntimeQuery) (model.ChannelRuntimeProbeResult, error)
	Evict(context.Context, model.ChannelRuntimeQuery) (model.ChannelRuntimeEvictResult, error)
}
```

Add `BenchRuntime ChannelRuntimeBenchController` to `Options`, add `benchRuntime ChannelRuntimeBenchController` to `Server`, and initialize it in `New`.

- [ ] **Step 2: Register routes and capabilities**

When `BenchEnabled` is true, register:

```go
s.mux.HandleFunc("/bench/v1/channel-runtime/snapshot", s.method(http.MethodGet, s.handleBenchChannelRuntimeSnapshot))
s.mux.HandleFunc("/bench/v1/channel-runtime/probe", s.method(http.MethodPost, s.handleBenchChannelRuntimeProbe))
s.mux.HandleFunc("/bench/v1/channel-runtime/evict", s.method(http.MethodPost, s.handleBenchChannelRuntimeEvict))
```

Extend `capabilitiesSupports` with the five runtime fields from Task 1. Set snapshot/probe/evict to `s.benchRuntime != nil`, and set faults/activate to `false`.

- [ ] **Step 3: Implement handlers**

Create `bench_runtime.go` with these handlers and helpers:

```go
func (s *Server) handleBenchChannelRuntimeSnapshot(w http.ResponseWriter, r *http.Request)
func (s *Server) handleBenchChannelRuntimeProbe(w http.ResponseWriter, r *http.Request)
func (s *Server) handleBenchChannelRuntimeEvict(w http.ResponseWriter, r *http.Request)
func runtimeQueryFromSnapshotRequest(r *http.Request) (model.ChannelRuntimeQuery, error)
func validateRuntimeQuery(query model.ChannelRuntimeQuery, requireRange bool) (model.ChannelRuntimeQuery, error)
```

Validation rules:

- Return `501` when `s.benchRuntime == nil`.
- Snapshot accepts an empty range for aggregate snapshots.
- Probe and evict require non-empty `run_id`, non-empty `profile`, non-zero `channel_type`, and `0 <= start < end`.
- Reject ranges larger than `100000`.
- Set `Version` to `bench/v1` when the controller returns an empty version.

- [ ] **Step 4: Add handler tests**

Add tests for:

```go
func TestBenchCapabilitiesAdvertiseChannelRuntimeWhenControllerConfigured(t *testing.T)
func TestBenchChannelRuntimeSnapshot(t *testing.T)
func TestBenchChannelRuntimeProbeRejectsInvalidRange(t *testing.T)
func TestBenchChannelRuntimeEvict(t *testing.T)
func TestBenchChannelRuntimeRoutesDisabledWithoutBenchAPI(t *testing.T)
func TestBenchChannelRuntimeRoutesUnavailableWithoutController(t *testing.T)
```

Use a fake controller that records `model.ChannelRuntimeQuery` and returns deterministic `model.ChannelRuntimeSnapshot`, `model.ChannelRuntimeProbeResult`, and `model.ChannelRuntimeEvictResult`.

- [ ] **Step 5: Test**

Run:

```bash
GOWORK=off go test ./internalv2/access/api -count=1
```

Expected:

```text
ok  	github.com/WuKongIM/WuKongIM/internalv2/access/api
```

## Phase 01 Verification And Commit

- [ ] **Step 1: Run all phase tests**

```bash
GOWORK=off go test ./internal/bench/model ./internal/bench/target ./internalv2/access/api -count=1
```

Expected:

```text
ok  	github.com/WuKongIM/WuKongIM/internal/bench/model
ok  	github.com/WuKongIM/WuKongIM/internal/bench/target
ok  	github.com/WuKongIM/WuKongIM/internalv2/access/api
```

- [ ] **Step 2: Commit**

```bash
git add internal/bench/model/bench_api.go internal/bench/target/client.go internal/bench/target/client_test.go internalv2/access/api/server.go internalv2/access/api/server_test.go internalv2/access/api/bench_runtime.go internalv2/access/api/bench_runtime_test.go
git commit -m "feat(wkbench): add channel runtime bench api contract"
```
