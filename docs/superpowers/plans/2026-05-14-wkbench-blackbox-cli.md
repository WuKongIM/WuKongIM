# wkbench Black-Box Load Test CLI Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `wkbench`, a distributed black-box benchmark CLI that prepares benchmark data through required `/bench/v1/*` APIs and drives real WKProto person/group traffic against an existing WuKongIM cluster.

**Architecture:** Add unauthenticated but default-disabled bench APIs to the public API server, backed by a reusable `internal/usecase/benchdata` usecase and wired only when `WK_BENCH_API_ENABLE=true`. Add a standalone `cmd/wkbench` external load-generator stack under `internal/bench/*` with strict import-boundary tests, coordinator/worker control, deterministic planning, WKProto clients, metrics, and reports.

**Tech Stack:** Go, Gin, stdlib HTTP/TCP, WuKongIM `pkg/protocol/codec` and `pkg/protocol/frame`, YAML config parsing, existing app config/Viper path, Go tests with `testify/require` where already used.

---

## Pre-Execution Notes

- Execute from repository root: `/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM`.
- Read `AGENTS.md` before implementation. Re-check for `FLOW.md` in every package before editing it; currently `internal/FLOW.md` exists and must be kept consistent if `internal` architecture documentation changes.
- Use @superpowers:test-driven-development for implementation: write narrow failing tests first, run the failing command, implement minimal code, rerun, commit.
- Do not touch unrelated dirty files. At plan time, unrelated user edits exist in `web/src/app/layout/topbar.tsx` and `web/src/app/layout/topbar.test.tsx`.
- Do not import server internals into `cmd/wkbench` or `internal/bench/*`. The plan includes an import-boundary test to enforce this.
- Keep `bench` target APIs unauthenticated as specified, but register them only when `WK_BENCH_API_ENABLE=true`.
- Update `wukongim.conf.example` when adding `WK_BENCH_API_*` keys. Add detailed English comments for new config fields.
- Update `AGENTS.md` directory structure because this work adds `cmd/wkbench`, `internal/bench`, and `internal/usecase/benchdata`.
- Keep default tests small. Large scale runs are manual, not default unit or integration tests.
- Suggested task split for @superpowers:subagent-driven-development:
  - Worker A: Tasks 1-3 server bench config/API/usecase.
  - Worker B: Tasks 4-7 wkbench config/planner/worker/coordinator control.
  - Worker C: Tasks 8-11 WKProto/workloads/metrics/report after Worker B interfaces land.
  - Worker D: Task 12 docs/integration after all public interfaces settle.

## Scope Check

The spec spans server bench APIs and an external CLI. They are kept in one implementation plan because `wkbench` v1 intentionally requires `/bench/v1/*`; the CLI cannot run a meaningful prepare phase without the server capability endpoint and batch setup APIs. Tasks are still split into independent commits so the server capability slice, CLI planner slice, and workload slices can be reviewed separately.

## File Structure

### Server bench API and usecase

- Create: `internal/usecase/benchdata/types.go`
  - Bench request/response DTOs, limits, validation helpers, and English comments.
- Create: `internal/usecase/benchdata/app.go`
  - Reusable usecase for capabilities, batch token upsert, batch channel upsert, batch subscriber add, and snapshot aggregation.
- Create: `internal/usecase/benchdata/app_test.go`
  - Unit tests with fake user/channel/gateway dependencies.
- Create: `internal/access/api/bench.go`
  - `/bench/v1/*` HTTP DTO binding, request body limits, no-auth route handlers, and error mapping.
- Create: `internal/access/api/bench_test.go`
  - Route registration, disabled 404, capabilities, validation, idempotency/error envelope tests.
- Modify: `internal/access/api/server.go`
  - Add `BenchEnabled`, `BenchMaxBatchSize`, `BenchMaxPayloadBytes`, and `BenchData` options and fields.
- Modify: `internal/access/api/routes.go`
  - Register bench routes only when enabled.
- Modify: `internal/app/config.go`
  - Add `BenchConfig` to `Config`, defaults, validation, comments.
- Modify: `cmd/wukongim/config.go`
  - Parse `WK_BENCH_API_ENABLE`, `WK_BENCH_API_MAX_BATCH_SIZE`, `WK_BENCH_API_MAX_PAYLOAD_BYTES`.
- Modify: `cmd/wukongim/config_test.go`
  - Config parsing and default tests.
- Modify: `internal/app/build.go`
  - Construct and wire `benchdata.App` into API options.
- Modify: `internal/app/build_test.go`
  - Verify bench options are forwarded.
- Modify: `wukongim.conf.example`
  - Add bench config comments and defaults.
- Modify if needed: `internal/FLOW.md`
  - Mention optional bench API component under API/usecase if architecture doc becomes stale.

### wkbench CLI, planner, and runtime

- Create: `cmd/wkbench/main.go`
  - CLI entrypoint and exit code handling.
- Create: `cmd/wkbench/import_boundary_test.go`
  - `go list -deps ./cmd/wkbench` forbidden import test.
- Create: `internal/bench/config/*.go`
  - YAML loading, env expansion, validation, rate/duration parsing.
- Create: `internal/bench/model/*.go`
  - Stable model types for target, workers, scenario, plan, shard, phases, metrics, errors.
- Create: `internal/bench/planner/*.go`
  - Identity pool expansion, profile validation, worker-weight sharding, huge-group partitioning.
- Create: `internal/bench/target/*.go`
  - HTTP clients for `/healthz`, `/readyz`, `/bench/v1/capabilities`, `/bench/v1/snapshot`, and batch setup endpoints.
- Create: `internal/bench/worker/*.go`
  - Worker HTTP server, phase state machine, assignment store, control token middleware.
- Create: `internal/bench/coordinator/*.go`
  - Preflight, assignment, phase orchestration, metrics polling, and stop handling.
- Create: `internal/bench/wkproto/*.go`
  - Production benchmark WKProto client using public protocol packages only.
- Create: `internal/bench/workload/*.go`
  - Person and group prepare/connect/run executors.
- Create: `internal/bench/metrics/*.go`
  - Counters, histograms, snapshots, bounded error samples, label-safe aggregation.
- Create: `internal/bench/report/*.go`
  - Report directory writer, JSON and Markdown summaries, limit evaluation.
- Modify: `AGENTS.md`
  - Add `cmd/wkbench`, `internal/bench`, and `internal/usecase/benchdata` to directory structure.
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`
  - Add a concise note that `wkbench` requires bench mode and does not use Manager APIs.

---

## Task 1: Add Bench API Config And Route Gating

**Files:**
- Modify: `internal/app/config.go`
- Modify: `cmd/wukongim/config.go`
- Modify: `cmd/wukongim/config_test.go`
- Modify: `internal/access/api/server.go`
- Modify: `internal/access/api/routes.go`
- Create: `internal/access/api/bench_test.go`
- Modify: `wukongim.conf.example`

- [ ] **Step 1: Read package flow and current config patterns**

Run: `cat internal/FLOW.md && rg -n "TestMode|TestData|DiagnosticsDebug|HealthDetail|WK_BENCH" internal/access/api internal/app cmd/wukongim wukongim.conf.example`

Expected: Understand API route gating and app config parsing before editing.

- [ ] **Step 2: Write failing API route gating tests**

Create the first tests in `internal/access/api/bench_test.go`:

```go
package api

import (
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/stretchr/testify/require"
)

func TestBenchRoutesDisabledReturnNotFound(t *testing.T) {
    srv := New(Options{})

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/bench/v1/capabilities", nil)
    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestBenchCapabilitiesRequiresUsecaseWhenEnabled(t *testing.T) {
    srv := New(Options{BenchEnabled: true, BenchMaxBatchSize: 10, BenchMaxPayloadBytes: 1024})

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/bench/v1/capabilities", nil)
    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
```

- [ ] **Step 3: Run test to verify it fails to compile**

Run: `go test ./internal/access/api -run 'TestBenchRoutes' -count=1`

Expected: FAIL because `Options.BenchEnabled` and bench route handling do not exist.

- [ ] **Step 4: Add config structs and API options minimally**

Add to `internal/app/config.go`:

```go
// BenchConfig controls unauthenticated benchmark-only APIs used by wkbench.
type BenchConfig struct {
    // APIEnabled registers /bench/v1/* routes for controlled benchmark environments.
    APIEnabled bool
    // APIMaxBatchSize limits top-level records accepted by a bench API request.
    APIMaxBatchSize int
    // APIMaxPayloadBytes limits HTTP request body size accepted by bench APIs.
    APIMaxPayloadBytes int64
}
```

Add `Bench BenchConfig` to `Config`, defaults in `ApplyDefaultsAndValidate`, and validation that enabled values are positive. Use constants:

```go
const (
    defaultBenchAPIMaxBatchSize    = 10000
    defaultBenchAPIMaxPayloadBytes = int64(10 << 20)
)
```

Add matching fields to `internal/access/api.Options` and `Server`.

- [ ] **Step 5: Parse config keys and update example**

In `cmd/wukongim/config.go`, parse:

```go
benchAPIEnabled, err := parseBool(v, "WK_BENCH_API_ENABLE")
benchAPIMaxBatchSize, err := parseInt(v, "WK_BENCH_API_MAX_BATCH_SIZE")
benchAPIMaxPayloadBytes, err := parseInt(v, "WK_BENCH_API_MAX_PAYLOAD_BYTES")
```

Wire into `app.Config.Bench`. Add tests in `cmd/wukongim/config_test.go`:

```go
func TestLoadConfigParsesBenchAPIConfig(t *testing.T) {
    path := writeTempConfig(t, []string{
        "WK_NODE_ID=1",
        "WK_CLUSTER_LISTEN_ADDR=127.0.0.1:11110",
        `WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:5100","transport":"stdnet","protocol":"wkproto"}]`,
        "WK_BENCH_API_ENABLE=true",
        "WK_BENCH_API_MAX_BATCH_SIZE=123",
        "WK_BENCH_API_MAX_PAYLOAD_BYTES=456789",
    })

    cfg, err := loadConfig(path)
    require.NoError(t, err)
    require.True(t, cfg.Bench.APIEnabled)
    require.Equal(t, 123, cfg.Bench.APIMaxBatchSize)
    require.Equal(t, int64(456789), cfg.Bench.APIMaxPayloadBytes)
}
```

Add English comments to `wukongim.conf.example`.

- [ ] **Step 6: Add disabled route gating implementation**

Add placeholder `registerBenchRoutes()` in `internal/access/api/bench.go` and call it from `routes.go` only when `s.benchEnabled`. Return 503 when enabled without usecase.

- [ ] **Step 7: Run focused tests**

Run:

```bash
go test ./internal/access/api -run 'TestBenchRoutes|TestBenchCapabilities' -count=1
go test ./cmd/wukongim -run 'TestLoadConfigParsesBenchAPIConfig' -count=1
go test ./internal/app -run 'TestConfig.*Bench|TestBuild' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/app/config.go cmd/wukongim/config.go cmd/wukongim/config_test.go internal/access/api/server.go internal/access/api/routes.go internal/access/api/bench.go internal/access/api/bench_test.go wukongim.conf.example
git commit -m "feat: gate bench api routes"
```

## Task 2: Implement BenchData Usecase And Bench API DTOs

**Files:**
- Create: `internal/usecase/benchdata/types.go`
- Create: `internal/usecase/benchdata/app.go`
- Create: `internal/usecase/benchdata/app_test.go`
- Modify: `internal/access/api/bench.go`
- Modify: `internal/access/api/bench_test.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/build_test.go`

- [ ] **Step 1: Write failing usecase tests**

Create `internal/usecase/benchdata/app_test.go` with fake dependencies:

```go
package benchdata

import (
    "context"
    "testing"

    "github.com/stretchr/testify/require"
)

func TestCapabilitiesReportsV1GroupSupport(t *testing.T) {
    app := New(Config{MaxBatchSize: 100, MaxPayloadBytes: 1024})

    got := app.Capabilities(context.Background())

    require.True(t, got.Enabled)
    require.Equal(t, "bench/v1", got.Version)
    require.Contains(t, got.Supports.ChannelTypes, "group")
    require.Equal(t, 100, got.Limits.MaxBatchSize)
}

func TestBatchSubscribersRejectsResetTrue(t *testing.T) {
    app := New(Config{Channels: fakeChannels{}, MaxBatchSize: 100, MaxPayloadBytes: 1024})

    _, err := app.AddSubscribers(context.Background(), SubscribersRequest{
        RunID:   "bench-run",
        BatchID: "batch-1",
        Items: []SubscriberItem{{
            ChannelID: "g1", ChannelType: 2, Reset: true, Subscribers: []string{"u1"},
        }},
    })

    require.ErrorContains(t, err, "reset=true")
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/usecase/benchdata -count=1`

Expected: FAIL because package does not exist.

- [ ] **Step 3: Define usecase DTOs and interfaces**

In `internal/usecase/benchdata/types.go`, add English comments and stable DTOs:

```go
package benchdata

// Config defines bench data preparation dependencies and request limits.
type Config struct {
    Users UserWriter
    Channels ChannelWriter
    Snapshot SnapshotReader
    MaxBatchSize int
    MaxPayloadBytes int64
}

// UserWriter updates benchmark user tokens through the normal user usecase boundary.
type UserWriter interface { UpdateToken(ctx context.Context, cmd UserTokenCommand) error }

// ChannelWriter mutates benchmark channel metadata and subscribers.
type ChannelWriter interface {
    UpsertChannel(ctx context.Context, ch ChannelRecord) error
    AddSubscribers(ctx context.Context, channelID string, channelType uint8, uids []string) error
}
```

Use local command types in `benchdata` so `access/api` does not depend on channel/user DTOs directly.

- [ ] **Step 4: Implement validation and capabilities**

Implement:

```go
func (a *App) Capabilities(context.Context) CapabilitiesResponse
func (a *App) UpsertTokens(context.Context, TokensRequest) (MutationResponse, error)
func (a *App) UpsertChannels(context.Context, ChannelsRequest) (MutationResponse, error)
func (a *App) AddSubscribers(context.Context, SubscribersRequest) (SubscribersResponse, error)
func (a *App) Snapshot(context.Context) (SnapshotResponse, error)
```

Rules:

- `run_id` and `batch_id` required for mutating APIs.
- Batch size limit checked before mutation.
- v1 channel/subscriber APIs accept only `channel_type=2`.
- Subscriber `reset=true` is always rejected.
- Re-adding existing data is success from the bench API perspective.

- [ ] **Step 5: Add HTTP route tests for DTOs**

Extend `internal/access/api/bench_test.go` with handler tests:

```go
func TestBenchSubscribersRejectsResetTrue(t *testing.T) {
    srv := New(Options{BenchEnabled: true, BenchData: benchStub{}, BenchMaxBatchSize: 100, BenchMaxPayloadBytes: 1024})
    body := `{"run_id":"bench-run","batch_id":"b1","items":[{"channel_id":"g1","channel_type":2,"reset":true,"subscribers":["u1"]}]}`

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodPost, "/bench/v1/channels/subscribers", strings.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusBadRequest, rec.Code)
}
```

- [ ] **Step 6: Wire app build dependencies**

In `internal/app/build.go`, construct a `benchdata.App` when `cfg.Bench.APIEnabled` is true and pass it into `accessapi.Options`. Add adapter methods around existing user/channel usecases; do not bypass usecases or stores from `access/api`.

- [ ] **Step 7: Run focused tests**

Run:

```bash
go test ./internal/usecase/benchdata -count=1
go test ./internal/access/api -run 'TestBench' -count=1
go test ./internal/app -run 'TestBuild.*Bench|TestConfig.*Bench' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/usecase/benchdata internal/access/api/bench.go internal/access/api/bench_test.go internal/app/build.go internal/app/build_test.go
git commit -m "feat: add bench data api"
```

## Task 3: Add wkbench CLI Skeleton, Config Parser, And Import Boundary

**Files:**
- Create: `cmd/wkbench/main.go`
- Create: `cmd/wkbench/import_boundary_test.go`
- Create: `internal/bench/config/config.go`
- Create: `internal/bench/config/config_test.go`
- Create: `internal/bench/model/config.go`
- Create: `internal/bench/model/rate.go`
- Create: `internal/bench/model/rate_test.go`

- [ ] **Step 1: Write failing config and rate tests**

Create `internal/bench/model/rate_test.go`:

```go
package model

import (
    "testing"
    "github.com/stretchr/testify/require"
)

func TestParseRatePerSecond(t *testing.T) {
    got, err := ParseRate("5000/s")
    require.NoError(t, err)
    require.Equal(t, 5000.0, got.PerSecond)
}

func TestParseRateRejectsZero(t *testing.T) {
    _, err := ParseRate("0/s")
    require.Error(t, err)
}
```

Create `internal/bench/config/config_test.go` with a minimal scenario requiring bench API:

```go
func TestLoadScenarioRequiresBenchAPIByTarget(t *testing.T) {
    target := Target{BenchAPI: BenchAPIConfig{Enabled: false}}
    scenario := Scenario{Version: "wkbench/v1", Run: RunConfig{ID: "bench-run"}}

    err := ValidateTargetScenario(target, scenario)

    require.ErrorContains(t, err, "bench_api.enabled")
}
```

- [ ] **Step 2: Add import-boundary failing test**

Create `cmd/wkbench/import_boundary_test.go`:

```go
package main

import (
    "os/exec"
    "strings"
    "testing"
)

func TestWkbenchDoesNotImportServerInternals(t *testing.T) {
    out, err := exec.Command("go", "list", "-deps", "./cmd/wkbench").CombinedOutput()
    if err != nil { t.Fatalf("go list: %v\n%s", err, out) }
    forbidden := []string{
        "/internal/app", "/internal/access/", "/internal/usecase/", "/internal/runtime/", "/internal/gateway/",
        "/pkg/slot/", "/pkg/controller/", "/pkg/cluster/",
    }
    deps := string(out)
    for _, item := range forbidden {
        if strings.Contains(deps, item) { t.Fatalf("wkbench imports forbidden dependency %s\n%s", item, deps) }
    }
}
```

- [ ] **Step 3: Run tests to verify failure**

Run: `go test ./internal/bench/... ./cmd/wkbench -count=1`

Expected: FAIL because packages do not exist.

- [ ] **Step 4: Implement CLI skeleton and config models**

Implement a stdlib `flag` based CLI first; avoid adding a third-party CLI framework until needed.

`cmd/wkbench/main.go`:

```go
package main

import (
    "fmt"
    "os"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
    if len(args) == 0 { fmt.Fprintln(os.Stderr, "usage: wkbench <run|worker|validate|doctor|report>"); return 1 }
    switch args[0] {
    case "validate", "doctor", "run", "worker", "report":
        fmt.Fprintf(os.Stderr, "%s is not implemented yet\n", args[0])
        return 6
    default:
        fmt.Fprintf(os.Stderr, "unknown command %q\n", args[0])
        return 1
    }
}
```

Implement YAML loading with `gopkg.in/yaml.v3` only if already acceptable; otherwise use JSON-compatible YAML after adding dependency intentionally. Keep models in `internal/bench/model` and parsing in `internal/bench/config`.

- [ ] **Step 5: Run focused tests**

Run:

```bash
go test ./internal/bench/model ./internal/bench/config ./cmd/wkbench -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/wkbench internal/bench/config internal/bench/model
git commit -m "feat: add wkbench config skeleton"
```

## Task 4: Implement Deterministic Planner And Sharding

**Files:**
- Create: `internal/bench/planner/planner.go`
- Create: `internal/bench/planner/planner_test.go`
- Modify: `internal/bench/model/config.go`
- Create: `internal/bench/model/plan.go`

- [ ] **Step 1: Write failing planner tests**

Create tests for identity pool, person sharding, many-group sharding, and huge-group sharding:

```go
func TestPlanPersonPairsByWorkerWeight(t *testing.T) {
    scenario := scenarioWithPersonCount(500000)
    workers := []model.Worker{{ID: "a", Weight: 1}, {ID: "b", Weight: 1}, {ID: "c", Weight: 2}}

    plan, err := Build(scenario, workers)

    require.NoError(t, err)
    require.Equal(t, model.Range{Start: 0, End: 125000}, plan.Workers["a"].Profiles["person-chat"].ChannelRange)
    require.Equal(t, model.Range{Start: 250000, End: 500000}, plan.Workers["c"].Profiles["person-chat"].ChannelRange)
}

func TestPlanHugeGroupSplitsMembersAndTraffic(t *testing.T) {
    scenario := scenarioWithHugeGroup(10000, "20/s")
    workers := []model.Worker{{ID: "a", Weight: 1}, {ID: "b", Weight: 1}, {ID: "c", Weight: 2}}

    plan, err := Build(scenario, workers)

    require.NoError(t, err)
    require.Equal(t, model.Range{Start: 0, End: 2500}, plan.Workers["a"].Profiles["huge-group"].MemberRange)
    require.InDelta(t, 5.0, plan.Workers["a"].Profiles["huge-group"].LocalRate.PerSecond, 0.001)
    require.InDelta(t, 10.0, plan.Workers["c"].Profiles["huge-group"].LocalRate.PerSecond, 0.001)
}
```

- [ ] **Step 2: Run planner tests to verify failure**

Run: `go test ./internal/bench/planner -count=1`

Expected: FAIL because planner does not exist.

- [ ] **Step 3: Implement planner models and Build**

Implement:

```go
func Build(s model.Scenario, workers []model.Worker) (model.Plan, error)
```

Rules:

- Validate profile names are unique.
- Validate v1 channel types are `person` and `group` only.
- Compute shared identity pool and fail if person profiles require more distinct participants than `online.total_users` under v1 no-reuse person semantics.
- Split ranges by worker weights.
- For single huge group with `split_members_and_traffic`, split member ranges and traffic partitions by weights.
- Compute worker-local rate from global `rate_per_channel`.
- Record deterministic channel owner with hash.

- [ ] **Step 4: Run planner tests**

Run: `go test ./internal/bench/planner ./internal/bench/model -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bench/model internal/bench/planner
git commit -m "feat: plan wkbench workload shards"
```

## Task 5: Implement Worker Control Server And Assignment State

**Files:**
- Create: `internal/bench/worker/server.go`
- Create: `internal/bench/worker/state.go`
- Create: `internal/bench/worker/server_test.go`
- Create: `internal/bench/worker/state_test.go`
- Modify: `cmd/wkbench/main.go`

- [ ] **Step 1: Write failing worker control tests**

Tests to add:

```go
func TestWorkerRequiresControlToken(t *testing.T) {
    srv := NewServer(Config{ControlToken: "secret"})
    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/v1/info", nil)

    srv.ServeHTTP(rec, req)

    require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestWorkerRejectsDifferentActiveRun(t *testing.T) {
    srv := NewServer(Config{ControlToken: "secret"})
    assign(t, srv, "secret", "run-a")

    rec := assignRecorder(t, srv, "secret", "run-b")

    require.Equal(t, http.StatusConflict, rec.Code)
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/bench/worker -count=1`

Expected: FAIL because worker package does not exist.

- [ ] **Step 3: Implement server endpoints**

Implement endpoints:

```text
GET /healthz
GET /v1/info
POST /v1/assign
POST /v1/phase/prepare
POST /v1/phase/connect
POST /v1/phase/warmup
POST /v1/phase/run
POST /v1/phase/cooldown
POST /v1/stop
GET /v1/status
GET /v1/metrics
GET /v1/report
```

Use a small phase enum and monotonic transition validator. Persist assignment JSON under `work-dir/current-run.json` after assignment.

- [ ] **Step 4: Wire `wkbench worker` command**

Add flag parsing in `cmd/wkbench/main.go`:

```bash
wkbench worker --listen 192.168.10.21:19090 --work-dir ./wkbench-data --control-token "$WK_BENCH_WORKER_TOKEN"
```

Require `--control-token` unless `--insecure-control=true`.

- [ ] **Step 5: Run worker tests**

Run: `go test ./internal/bench/worker ./cmd/wkbench -run 'TestWorker|TestWkbenchDoesNotImport' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/bench/worker cmd/wkbench/main.go
git commit -m "feat: add wkbench worker control server"
```

## Task 6: Implement Target Bench API Client And Coordinator Preflight

**Files:**
- Create: `internal/bench/target/client.go`
- Create: `internal/bench/target/client_test.go`
- Create: `internal/bench/coordinator/preflight.go`
- Create: `internal/bench/coordinator/preflight_test.go`
- Modify: `cmd/wkbench/main.go`

- [ ] **Step 1: Write failing target client tests**

Use `httptest.Server`:

```go
func TestCapabilities404FailsPreflight(t *testing.T) {
    ts := httptest.NewServer(http.NotFoundHandler())
    defer ts.Close()

    client := NewClient(Config{APIAddrs: []string{ts.URL}})
    _, err := client.Capabilities(context.Background())

    require.ErrorContains(t, err, "bench api")
}
```

- [ ] **Step 2: Implement target client**

Implement methods:

```go
Healthz(ctx context.Context) error
Readyz(ctx context.Context) error
Capabilities(ctx context.Context) (model.BenchCapabilities, error)
Snapshot(ctx context.Context) (model.BenchSnapshot, error)
UpsertTokens(ctx context.Context, req model.BatchTokensRequest) error
UpsertChannels(ctx context.Context, req model.BatchChannelsRequest) error
AddSubscribers(ctx context.Context, req model.BatchSubscribersRequest) error
```

- [ ] **Step 3: Implement coordinator preflight**

Preflight checks:

- Target `bench_api.enabled=true`.
- API `/healthz` and `/readyz`.
- Required `/bench/v1/capabilities` includes token/channel/subscriber/snapshot support.
- Worker `/v1/info` succeeds with control token.
- Gateway TCP handshake can be done later in Task 8; add a placeholder check interface now.

- [ ] **Step 4: Wire `wkbench doctor` and `validate`**

`validate` loads configs and planner only. `doctor` runs preflight without assigning work.

- [ ] **Step 5: Run tests**

Run:

```bash
go test ./internal/bench/target ./internal/bench/coordinator ./cmd/wkbench -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/bench/target internal/bench/coordinator cmd/wkbench/main.go
git commit -m "feat: add wkbench target preflight"
```

## Task 7: Implement Coordinator Assignment And Fake Phase Orchestration

**Files:**
- Create: `internal/bench/coordinator/run.go`
- Create: `internal/bench/coordinator/run_test.go`
- Modify: `internal/bench/worker/server.go`
- Modify: `cmd/wkbench/main.go`

- [ ] **Step 1: Write failing orchestration test**

Use `httptest` worker servers and fake plan:

```go
func TestCoordinatorAssignsWorkersAndRunsPhases(t *testing.T) {
    workers := newFakeWorkers(t, 2)
    coord := New(CoordinatorConfig{Workers: workers.ClientConfigs(), Target: fakeTargetOK()})

    result, err := coord.Run(context.Background(), fakeScenario())

    require.NoError(t, err)
    require.Equal(t, StatusCompleted, result.Status)
    require.Equal(t, []Phase{PhasePrepare, PhaseConnect, PhaseWarmup, PhaseRun, PhaseCooldown}, workers[0].ObservedPhases())
}
```

- [ ] **Step 2: Implement coordinator run loop**

Implement:

- Build plan.
- POST `/v1/assign`.
- POST phase endpoints in order.
- Poll `/v1/status` until each worker reaches target phase or failed.
- Honor `fail_fast`.
- Stop all workers on context cancellation.

Use fake workload hooks inside worker for now; real prepare/connect/run lands later.

- [ ] **Step 3: Wire `wkbench run` with fake workload**

The first `run` command can execute all control phases but should print that real workloads are not implemented until Tasks 8-10 land.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/bench/coordinator ./internal/bench/worker ./cmd/wkbench -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bench/coordinator internal/bench/worker cmd/wkbench/main.go
git commit -m "feat: orchestrate wkbench worker phases"
```

## Task 8: Implement Production WKProto Client And Connection Manager

**Files:**
- Create: `internal/bench/wkproto/client.go`
- Create: `internal/bench/wkproto/client_test.go`
- Create: `internal/bench/workload/connections.go`
- Create: `internal/bench/workload/connections_test.go`
- Create or Move: `pkg/protocol/wkprotoenc/*` if crypto helpers must be extracted from `internal/gateway/wkprotoenc`
- Modify: `cmd/wkbench/import_boundary_test.go`

- [ ] **Step 1: Check WKProto crypto import boundary**

Current e2e client uses `internal/gateway/testkit` and `internal/gateway/wkprotoenc`, which `wkbench` must not import. If encryption is required for real gateway handshakes, extract the minimal crypto helper to a public protocol package such as `pkg/protocol/wkprotoenc` and update existing internal gateway imports in a separate small commit.

- [ ] **Step 2: Write failing WKProto client tests**

Use a fake `net.Listener` server that reads a Connect frame and writes Connack:

```go
func TestClientConnectWritesConnectAndReadsConnack(t *testing.T) {
    addr, gotConnect := startFakeWKProtoServer(t, frame.ReasonSuccess)
    c := NewClient(Config{Addr: addr, Timeout: time.Second})

    err := c.Connect(context.Background(), "u1", "u1-device")

    require.NoError(t, err)
    require.Equal(t, "u1", (<-gotConnect).UID)
}
```

- [ ] **Step 3: Implement minimal WKProto client**

Implement:

```go
Connect(ctx, uid, deviceID string) error
Send(ctx context.Context, pkt *frame.SendPacket) error
ReadFrame(ctx context.Context) (frame.Frame, error)
RecvAck(ctx context.Context, messageID int64, messageSeq uint64) error
Close() error
```

Use `pkg/protocol/codec` and `pkg/protocol/frame` only plus any extracted public crypto helper.

- [ ] **Step 4: Implement connection manager**

`internal/bench/workload/connections.go` manages UID to client sessions, connect rate limiting, gateway selection, and active count. Keep reconnect disabled initially but shape config for later.

- [ ] **Step 5: Run tests and boundary**

Run:

```bash
go test ./internal/bench/wkproto ./internal/bench/workload ./cmd/wkbench -count=1
```

Expected: PASS and import-boundary test still passes.

- [ ] **Step 6: Commit**

```bash
git add internal/bench/wkproto internal/bench/workload pkg/protocol/wkprotoenc cmd/wkbench/import_boundary_test.go
git commit -m "feat: add wkbench wkproto client"
```

## Task 9: Implement Person Workload

**Files:**
- Create: `internal/bench/workload/person.go`
- Create: `internal/bench/workload/person_test.go`
- Modify: `internal/bench/worker/server.go`
- Modify: `internal/bench/metrics/*`

- [ ] **Step 1: Write failing person workload unit tests**

Use fake WKProto clients:

```go
func TestPersonWorkloadSendsToRecipientUID(t *testing.T) {
    clients := newFakeClientPool("u1", "u2")
    wl := NewPersonWorkload(PersonConfig{SenderUID: "u1", RecipientUID: "u2", ClientMsgPrefix: "bench-msg"}, clients)

    err := wl.SendOne(context.Background(), 7)

    require.NoError(t, err)
    sent := clients.Client("u1").SentSendPacket()
    require.Equal(t, frame.ChannelTypePerson, sent.ChannelType)
    require.Equal(t, "u2", sent.ChannelID)
    require.Contains(t, sent.ClientMsgNo, "bench-msg")
}
```

- [ ] **Step 2: Implement person executor**

Implement:

- Connect sender/recipient sessions for assigned person pairs.
- Generate deterministic payload with run/profile/traffic/channel/message indexes.
- Send `ChannelTypePerson` to recipient UID.
- Match sendack by client sequence/message number.
- Verify recv on recipient when `verify.recv.mode=full`.
- Emit counters/histograms/error samples.

- [ ] **Step 3: Integrate into worker phases**

Worker `connect`, `warmup`, `run`, and `cooldown` call workload hooks for assigned person shards.

- [ ] **Step 4: Run focused tests**

Run:

```bash
go test ./internal/bench/workload ./internal/bench/worker ./internal/bench/metrics -run 'TestPerson|TestWorker' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bench/workload internal/bench/worker internal/bench/metrics
git commit -m "feat: add wkbench person workload"
```

## Task 10: Implement Group Prepare And Group Workload

**Files:**
- Create: `internal/bench/workload/group.go`
- Create: `internal/bench/workload/group_test.go`
- Modify: `internal/bench/target/client.go`
- Modify: `internal/bench/worker/server.go`
- Modify: `internal/bench/planner/planner.go`

- [ ] **Step 1: Write failing group prepare tests**

Use fake target client:

```go
func TestHugeGroupPrepareOwnerCreatesChannelAndAllWorkersAddSubscribers(t *testing.T) {
    target := newFakeBenchTarget()
    shard := hugeGroupShard(workerID: "a", owner: true, members: model.Range{Start: 0, End: 2500})
    wl := NewGroupWorkload(target, fakeClients(), shard)

    err := wl.Prepare(context.Background())

    require.NoError(t, err)
    require.Len(t, target.UpsertedChannels, 1)
    require.Equal(t, "bench-run-subs-huge-group-a-0-2500", target.SubscriberBatches[0].BatchID)
}
```

- [ ] **Step 2: Implement group prepare**

Rules:

- Owner worker calls `/bench/v1/channels` for owned group channels.
- Workers wait for coordinator `channel_prepared` barrier before subscriber add for huge groups.
- All relevant workers call `/bench/v1/channels/subscribers` with deterministic `batch_id` and `reset=false`.
- Batch subscribers are chunked by `subscribers_batch_size`.

- [ ] **Step 3: Write failing group traffic tests**

Test that local rate uses partition share and does not multiply global `rate_per_channel`:

```go
func TestHugeGroupLocalRateUsesOwnedPartitions(t *testing.T) {
    shard := model.ProfileShard{TrafficPartitionCount: 4, OwnedTrafficPartitions: []int{0}, GlobalRate: model.Rate{PerSecond: 20}}

    got := LocalTrafficRate(shard)

    require.Equal(t, 5.0, got.PerSecond)
}
```

- [ ] **Step 4: Implement group traffic and sampled recv verification**

- Select sender from online members.
- Send `ChannelTypeGroup` to group ID.
- Track sendack.
- For `verify.recv.mode=full`, verify all assigned online members.
- For `sampled`, deterministically pick sample members from assigned online members.
- Send recvack when configured.

- [ ] **Step 5: Run focused tests**

Run:

```bash
go test ./internal/bench/workload ./internal/bench/planner ./internal/bench/target -run 'TestGroup|TestHuge' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/bench/workload internal/bench/target internal/bench/worker internal/bench/planner
git commit -m "feat: add wkbench group workload"
```

## Task 11: Implement Metrics, Limits, Reports, And Exit Codes

**Files:**
- Create: `internal/bench/metrics/metrics.go`
- Create: `internal/bench/metrics/metrics_test.go`
- Create: `internal/bench/report/report.go`
- Create: `internal/bench/report/report_test.go`
- Modify: `internal/bench/coordinator/run.go`
- Modify: `cmd/wkbench/main.go`

- [ ] **Step 1: Write failing metrics cardinality tests**

```go
func TestMetricLabelsRejectHighCardinalityKeys(t *testing.T) {
    labels := Labels{"worker_id": "w1", "uid": "u1"}

    err := labels.Validate()

    require.ErrorContains(t, err, "uid")
}
```

- [ ] **Step 2: Implement metrics aggregation**

Implement worker-local counters, histograms with simple bucket or reservoir summaries, gauges, and bounded error samples. Keep `run_id` in report metadata, not default labels.

- [ ] **Step 3: Write failing report tests**

```go
func TestReportHardLimitFailureSetsExitCode3(t *testing.T) {
    report := BuildReport(inputWithSendAckErrorRate(0.02), Limits{Hard: HardLimits{MaxSendAckErrorRate: 0.001}})

    require.Equal(t, StatusFailed, report.Status)
    require.Equal(t, 3, report.ExitCode)
}
```

- [ ] **Step 4: Implement report writer**

Write:

```text
scenario.yaml
target.yaml
workers.yaml
plan.json
summary.md
report.json
coordinator.log
workers/*.report.json
metrics/worker-1s.jsonl
metrics/target-snapshots.jsonl
errors/samples.jsonl
```

- [ ] **Step 5: Wire exit codes**

Map:

```text
0 success
1 config validation failed
2 preflight failed
3 hard limit failed
4 worker failed/unreachable
5 target unavailable
6 internal error
```

- [ ] **Step 6: Run tests**

Run:

```bash
go test ./internal/bench/metrics ./internal/bench/report ./internal/bench/coordinator ./cmd/wkbench -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/bench/metrics internal/bench/report internal/bench/coordinator cmd/wkbench/main.go
git commit -m "feat: report wkbench results"
```

## Task 12: Add Focused End-to-End Coverage And Docs

**Files:**
- Create: `test/e2e/bench/AGENTS.md`
- Create: `test/e2e/bench/wkbench_smoke/AGENTS.md`
- Create: `test/e2e/bench/wkbench_smoke/wkbench_smoke_test.go`
- Modify: `test/e2e/AGENTS.md`
- Modify: `AGENTS.md`
- Modify: `internal/FLOW.md` if needed
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`

- [ ] **Step 1: Read e2e AGENTS before adding scenario**

Run: `cat test/e2e/AGENTS.md`

Expected: Confirm black-box e2e constraints and directory catalog rules.

- [ ] **Step 2: Write e2e smoke test**

Create a small e2e package that:

- Starts a single-node cluster with `WK_BENCH_API_ENABLE=true`.
- Starts one in-process `wkbench worker` on `127.0.0.1:0` or runs the worker command as a subprocess if easier.
- Runs a tiny scenario: 2 person pairs, 1 group with 3 members, duration under a few seconds.
- Requires successful report and non-zero sendack success.

Keep it behind e2e build tags and do not run as part of unit tests.

- [ ] **Step 3: Add disabled bench API preflight test**

Add a focused test where target has `bench_api.enabled=true` in `target.yaml` but the server lacks `WK_BENCH_API_ENABLE=true`; `wkbench doctor` or coordinator preflight must fail before traffic.

- [ ] **Step 4: Update docs and AGENTS**

- `AGENTS.md`: add new directories to project structure.
- `test/e2e/AGENTS.md`: add bench smoke scenario catalog entry.
- `docs/development/PROJECT_KNOWLEDGE.md`: concise note that wkbench requires server bench mode and never uses Manager APIs.
- `internal/FLOW.md`: add bench API/usecase component if the architecture sections become stale.

- [ ] **Step 5: Run focused tests**

Run:

```bash
go test ./internal/bench/... ./cmd/wkbench -count=1
go test ./internal/usecase/benchdata ./internal/access/api -run 'TestBench' -count=1
go test ./cmd/wukongim -run 'TestLoadConfigParsesBenchAPIConfig' -count=1
```

Optional e2e smoke:

```bash
go test -tags=e2e ./test/e2e/bench/wkbench_smoke -count=1
```

Expected: Unit tests pass; e2e smoke passes when run explicitly.

- [ ] **Step 6: Commit**

```bash
git add test/e2e/bench test/e2e/AGENTS.md AGENTS.md internal/FLOW.md docs/development/PROJECT_KNOWLEDGE.md
git commit -m "test: add wkbench smoke coverage"
```

## Final Verification

- [ ] **Step 1: Run all narrow unit suites**

```bash
go test ./internal/bench/... ./cmd/wkbench ./internal/usecase/benchdata ./internal/access/api ./internal/app ./cmd/wukongim -count=1
```

Expected: PASS.

- [ ] **Step 2: Run relevant broader tests**

```bash
go test ./internal/... ./pkg/... -count=1
```

Expected: PASS. If this is too slow locally, record the narrower commands that passed and why the broader command was deferred.

- [ ] **Step 3: Run e2e smoke if environment allows**

```bash
go test -tags=e2e ./test/e2e/bench/wkbench_smoke -count=1
```

Expected: PASS.

- [ ] **Step 4: Check import boundary explicitly**

```bash
go test ./cmd/wkbench -run TestWkbenchDoesNotImportServerInternals -count=1
```

Expected: PASS.

- [ ] **Step 5: Check git state**

```bash
git status --short
```

Expected: only intended changes, no unrelated web topbar files staged or modified by this work.
