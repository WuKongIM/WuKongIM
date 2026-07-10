# Presence Expiry Index And Touch Drain Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace full-route TTL scans with a per-authority activity-bucket index and make the owner touch worker sustain at least 100,000 online routes through bounded multi-chunk flushes.

**Architecture:** Each presence authority slot owns a min-heap of unique activity-second buckets plus exact identity membership, so a fresh expiry tick visits no active routes. The owner worker drains multiple 512-route chunks up to an explicit 65,536-route flush budget and resolves each chunk through one immutable routing snapshot with aligned per-UID results. A low-cardinality observer publishes work, latency, and index pressure without UID, session, or slot labels.

**Tech Stack:** Go 1.25.11, `container/heap`, WuKongIM presence/cluster runtimes, Prometheus client, Grafana JSON dashboards, Go benchmarks and race detector.

## Global Constraints

- Preserve route fencing, conflict resolution, owner sequence, tombstones, and strict TTL equality behavior.
- A single-node deployment remains a single-node cluster; do not add local-only routing behavior.
- Use one expiry bucket per distinct seen second, not one heap object/timer/goroutine per route and not lazy duplicate heap entries.
- Bound one touch flush by `TouchMaxRoutesPerFlush`; do not start unbounded target goroutines.
- Resolve each touch chunk against one routing snapshot and preserve input/result alignment.
- Metrics labels must be bounded enums only. Never label by UID, session, hash slot, route target, or channel.
- Add detailed English comments for the new config field and keep all shipped TOML examples aligned.
- Keep unit tests short; the real 100,000-user, >2x-TTL check remains a benchmark/script acceptance step.
- Use `GOWORK=off` and explicit package roots; never run repository-root `./...`.
- Read package `FLOW.md` files before implementation and update every flow whose behavior changes.

---

## File Map

| Path | Responsibility |
| --- | --- |
| `pkg/cluster/routing/router.go` | Produce aligned partial batch routing from one table snapshot. |
| `pkg/cluster/node.go`, `pkg/cluster/api.go` | Expose public per-key batch results with authority epochs. |
| `internal/runtime/presence/expiry_index.go` | Maintain activity buckets and expire only due candidates. |
| `internal/runtime/presence/directory.go`, `types.go` | Wire the index into every route mutation and expose observations/snapshots. |
| `internal/infra/cluster/presence.go` | Convert aligned cluster routes into aligned presence targets. |
| `internal/app/presence_touch.go` | Drain multiple bounded chunks, resolve once per chunk, and requeue precisely. |
| `internal/app/config.go`, `internal/config/{schema,build}.go` | Define/default/validate/load `TouchMaxRoutesPerFlush`. |
| `pkg/metrics/presence.go`, `internal/app/observability.go` | Export low-cardinality presence work metrics. |
| `docker/observability/grafana/dashboards/wukongim-v2-runtime-ops.json` | Visualize expiry and touch capacity. |
| `wukongim.toml.example`, `cmd/wukongim/*.toml.example`, `scripts/wukongim/*.toml` | Keep shipped configuration examples consistent. |

### Task 1: Add aligned partial batch routing

**Files:**
- Modify: `pkg/cluster/routing/router.go`
- Modify: `pkg/cluster/routing/router_test.go`
- Modify: `pkg/cluster/api.go`
- Modify: `pkg/cluster/node.go`
- Modify: `pkg/cluster/api_test.go`
- Modify: `pkg/cluster/FLOW.md`

**Interfaces:**

```go
// pkg/cluster/routing
type RouteKeyResult struct {
	Route Route
	Err   error
}

func (r *Router) RouteKeysPartial(keys []string) ([]RouteKeyResult, error)

// pkg/cluster
type RouteKeyResult struct {
	Route Route
	Err   error
}

func (n *Node) RouteKeysPartial(keys []string) ([]RouteKeyResult, error)
```

The outer error is only for no installed routing table/node lifecycle failure. A missing leader or invalid route is stored in the aligned item. The existing all-or-error `RouteKeys` remains for its existing append consumer; both APIs must load one immutable table snapshot.

- [ ] **Step 1: Add a failing router alignment test**

Add `TestRouterRouteKeysPartialPreservesAlignedSuccessesAndErrors`. Install a routing table where two keys map to led slots and one key maps to a slot with no leader. Assert result length equals input length, successes stay at their original indexes, the failed item satisfies the route error, and all successful routes use the same installed revision.

```bash
GOWORK=off go test ./pkg/cluster/routing -run '^TestRouterRouteKeysPartial' -count=1
```

Expected: compile failure because the result type and method do not exist.

- [ ] **Step 2: Implement one-snapshot partial routing**

Refactor the existing route calculation into a helper accepting an already loaded table. `RouteKeysPartial` loads the table pointer once, allocates exactly `len(keys)` results, and records each key-specific error without aborting later keys. Do not call `RouteKey` in a loop.

- [ ] **Step 3: Add a failing Node mapping test**

Add `TestNodeRouteKeysPartialMapsErrorsAndAuthorityEpochs`. Assert per-item errors are passed through `mapRouteError`, success items include the current local `AuthorityEpoch`, and alignment is unchanged.

```bash
GOWORK=off go test ./pkg/cluster -run '^TestNodeRouteKeysPartial' -count=1
```

- [ ] **Step 4: Expose and verify the Node API**

Implement `Node.RouteKeysPartial`, keep `RouteKeys` behavior unchanged, update compile-time API coverage, and document both contracts in `pkg/cluster/FLOW.md`.

```bash
GOWORK=off go test ./pkg/cluster/routing ./pkg/cluster -run 'RouteKeys(Partial)?' -count=1
```

- [ ] **Step 5: Commit the routing prerequisite**

```bash
git add pkg/cluster/routing/router.go pkg/cluster/routing/router_test.go pkg/cluster/api.go pkg/cluster/node.go pkg/cluster/api_test.go pkg/cluster/FLOW.md
git commit -m "feat(cluster): add aligned partial batch routing"
```

### Task 2: Replace full presence scans with authority-slot expiry buckets

**Files:**
- Create: `internal/runtime/presence/expiry_index.go`
- Create: `internal/runtime/presence/expiry_index_test.go`
- Create: `internal/runtime/presence/directory_benchmark_test.go`
- Modify: `internal/runtime/presence/directory.go`
- Modify: `internal/runtime/presence/types.go`
- Modify: `internal/runtime/presence/directory_test.go`
- Modify: `internal/runtime/presence/FLOW.md`

**Interfaces:**

```go
type expiryBucket struct {
	seenUnix  int64
	heapIndex int
	keys      map[identityKey]struct{}
}

type expiryBucketHeap []*expiryBucket

type ExpireResult struct {
	Expired     int
	DueBuckets  int
	Examined    int
	IndexRoutes int
	IndexBuckets int
}

func (d *Directory) ExpireRoutesDetailed(now time.Time, ttl time.Duration) ExpireResult
func (d *Directory) ExpireRoutes(now time.Time, ttl time.Duration) int
```

`ExpireRoutes` is a thin call returning `.Expired`; the worker/observer uses `ExpireRoutesDetailed`. Extend `Snapshot` with `ExpiryIndexRoutes` and `ExpiryIndexBuckets` for bounded diagnostics.

- [ ] **Step 1: Add failing mutation and deadline tests**

Add these tests before implementation:

```text
TestDirectoryTouchMovesRouteOutOfOldExpiryBucket
TestDirectoryUnregisterUnschedulesExpiryAndPreservesTombstone
TestDirectoryExpiredRouteCanBeRecreatedByFreshTouch
TestDirectoryExpiryRetainsExactDeadlineEquality
TestDirectoryRevisionOnlyAuthorityUpdatePreservesExpiryIndex
TestDirectoryAuthorityIdentityReplacementDropsExpiryIndex
TestDirectoryConflictReplacementUnschedulesOldRoute
```

Tests inspect `Snapshot` and expiry results, not channel/UID-labelled production metrics.

```bash
GOWORK=off go test ./internal/runtime/presence \
  -run 'TestDirectory(TouchMoves|UnregisterUnschedules|ExpiredRoute|ExpiryRetains|RevisionOnly|AuthorityIdentity|ConflictReplacement)' \
  -count=1
```

Expected: compile/assertion failures because the index and detailed result do not exist.

- [ ] **Step 2: Implement heap and exact membership helpers**

In `expiry_index.go`, implement `heap.Interface` and:

```go
func routeSeenUnix(route Route) int64
func (s *authoritySlot) scheduleExpiryLocked(key identityKey, route Route)
func (s *authoritySlot) unscheduleExpiryLocked(key identityKey)
func (s *authoritySlot) expireLocked(now time.Time, ttl time.Duration) ExpireResult
```

Rules: zero seen time is not indexed; moving a key first removes its exact previous bucket membership; removing the final key uses `heap.Remove`; expiry pops only while `time.Unix(seenUnix, 0).Add(ttl).Before(now)`; it deletes an identity only if `expiryByKey[key] == bucket`.

- [ ] **Step 3: Wire every active-map mutation**

Initialize all three index fields in `newAuthoritySlot`. `upsertActiveLocked` removes any old membership and schedules the normalized final route. `removeActiveLocked` unschedules before deleting active/byUID. Do not alter `ownerSeq` or `tombstoneSeq` during TTL expiry. A revision-only `BecomeAuthority` retains the slot object; a hard authority change or `LoseAuthority` drops the whole slot/index.

- [ ] **Step 4: Add the 100,000-fresh/10-due regression test**

Add `TestDirectoryExpireRoutesExaminesOnlyDueCandidates`. Build 100,000 fresh routes and 10 due routes, expire once, and assert:

```go
if result.Examined != 10 || result.Expired != 10 || result.DueBuckets == 0 {
	t.Fatalf("expire result = %#v, want examined=10 expired=10", result)
}
```

Also assert the post-expiry index route count is 100,000.

```bash
GOWORK=off go test ./internal/runtime/presence -run '^TestDirectoryExpireRoutesExaminesOnlyDueCandidates$' -count=1
```

- [ ] **Step 5: Add scale benchmarks**

Implement fresh and due sub-benchmarks at 10,000, 100,000, and 1,000,000 routes. Exclude setup with `b.StopTimer`, report `candidates/op`, `expired/op`, and allocations.

```bash
GOWORK=off go test ./internal/runtime/presence -run '^$' \
  -bench '^BenchmarkDirectoryExpireRoutes(Fresh|Due)$' -benchtime=1x -benchmem -count=5 \
  -mutexprofile=/tmp/presence-expiry-mutex.out -blockprofile=/tmp/presence-expiry-block.out
```

Acceptance: fresh `candidates/op` is zero, and fresh 1,000,000-route `ns/op` is no more than 3x the 10,000-route result.

- [ ] **Step 6: Update FLOW and commit**

Document index ownership, strict deadline equality, expected complexity, and retained fences in `internal/runtime/presence/FLOW.md`.

```bash
git add internal/runtime/presence
git commit -m "perf(presence): index route expiry by activity bucket"
```

### Task 3: Add the total flush budget and aligned presence target resolution

**Files:**
- Modify: `internal/app/config.go`
- Modify: `internal/config/schema.go`
- Modify: `internal/config/build.go`
- Modify: `internal/config/example_test.go`
- Modify: `cmd/wukongim/config_test.go`
- Modify: `internal/usecase/presence/types.go`
- Modify: `internal/infra/cluster/presence.go`
- Modify: `internal/infra/cluster/presence_test.go`
- Modify: `internal/app/presence_touch.go`
- Modify: `internal/app/app_test.go`
- Modify: `internal/app/wiring.go`
- Modify: `wukongim.toml.example`
- Modify: `cmd/wukongim/wukongim.toml.example`
- Modify: `cmd/wukongim/wukongim-node1.toml.example`
- Modify: `cmd/wukongim/wukongim-node2.toml.example`
- Modify: `cmd/wukongim/wukongim-node3.toml.example`
- Modify: `scripts/wukongim/wukongim.toml`
- Modify: `scripts/wukongim/wukongim-node1.toml`
- Modify: `scripts/wukongim/wukongim-node2.toml`
- Modify: `scripts/wukongim/wukongim-node3.toml`

**Configuration and routing interfaces:**

```go
type PresenceConfig struct {
	ActivationTimeout      time.Duration
	TouchFlushInterval     time.Duration
	TouchBatchSize         int
	TouchMaxRoutesPerFlush int
	RouteTTL               time.Duration
}

type RouteTargetResult struct {
	Target RouteTarget
	Err    error
}

func (c *PresenceAuthorityClient) ResolveRouteTargets(ctx context.Context, uids []string) []presence.RouteTargetResult
```

Config contract: TOML `presence.touch_max_routes_per_flush`, env `WK_PRESENCE_TOUCH_MAX_ROUTES_PER_FLUSH`, default 65,536, positive and greater than or equal to `TouchBatchSize`.

- [ ] **Step 1: Add failing default/validation/loader tests**

Cover default 65,536, max below batch, zero/negative explicit values, TOML loading, environment loading, and schema supported-key coverage. The example test must require detailed English comments immediately above the field.

```bash
GOWORK=off go test ./internal/app ./internal/config ./cmd/wukongim \
  -run 'TouchMaxRoutesPerFlush|PresenceConfig|Presence.*Config' -count=1
```

- [ ] **Step 2: Implement and propagate config**

Add the field/comment/default/validation, schema entry, builder parsing, supported-key entry, and wiring:

```go
MaxRoutesPerFlush: a.cfg.Presence.TouchMaxRoutesPerFlush,
```

Update shipped examples with:

```toml
# Maximum owner-local dirty routes processed across all touch chunks in one flush.
# Must be positive and greater than or equal to touch_batch_size.
touch_max_routes_per_flush = 65536
```

Do not add a new `[presence]` block to a specialized example that currently has none.

- [ ] **Step 3: Add failing aligned target-resolution tests**

Extend `PresenceNode` with `RouteKeysPartial`. Add `TestPresenceAuthorityClientResolveRouteTargetsPreservesAlignment` with success/failure/success results and assert exactly one batch call. A batch-level error must be copied into all aligned items; item errors must affect only their own indexes.

```bash
GOWORK=off go test ./internal/infra/cluster -run '^TestPresenceAuthorityClientResolveRouteTargets' -count=1
```

- [ ] **Step 4: Implement aligned presence resolution**

Convert each successful `cluster.RouteKeyResult` with `routeTargetFromClusterRoute`; map each error through `mapPresenceRouteError`. Never fall back to looping over `ResolveRouteTarget`.

- [ ] **Step 5: Add failing multi-chunk worker tests**

Add:

```text
TestPresenceTouchWorkerDrainsMultipleChunksWithinBudget
TestPresenceTouchWorkerNeverExceedsMaxRoutesPerFlush
TestPresenceTouchWorkerRequeuesOnlyFailedRouteResults
TestPresenceTouchWorkerCancellationRequeuesAllUnsentRoutes
```

Fakes must record drain limits, batch UID calls, target groups, sent routes, and requeued exact route identities.

- [ ] **Step 6: Implement bounded multi-chunk flush**

Extend `presenceTouchAuthority` and `presenceTouchWorkerOptions`:

```go
ResolveRouteTargets(context.Context, []string) []presence.RouteTargetResult
MaxRoutesPerFlush int
```

Default max to 65,536 in the worker constructor. Each loop requests `min(BatchSize, remainingBudget)`, preserves first-seen UID order while deduplicating, resolves once, groups successful routes by the full fenced `RouteTarget`, sends groups sequentially, and requeues only failed/unsent routes. Stop on a short drain, exhausted budget, or context cancellation. Cancellation after a drain must requeue all unsent routes before return.

- [ ] **Step 7: Verify and commit the capacity change**

```bash
GOWORK=off go test ./internal/app ./internal/infra/cluster ./internal/config ./cmd/wukongim \
  -run 'Presence|TouchMaxRoutesPerFlush|RouteTargets' -count=1

git add internal/app internal/config internal/usecase/presence internal/infra/cluster \
  wukongim.toml.example cmd/wukongim/*.toml.example scripts/wukongim/*.toml
git commit -m "perf(presence): bound multi-chunk touch draining"
```

### Task 4: Export bounded expiry and touch observations

**Files:**
- Create: `pkg/metrics/presence.go`
- Create: `pkg/metrics/presence_test.go`
- Modify: `pkg/metrics/registry.go`
- Modify: `internal/app/observability.go`
- Modify: `internal/app/observability_test.go`
- Modify: `internal/app/presence_touch.go`
- Modify: `internal/app/wiring.go`
- Modify: `docker/observability/grafana/dashboard_assets_test.go`
- Modify: `docker/observability/grafana/dashboards/wukongim-v2-runtime-ops.json`

**Metrics interface:**

```go
func (m *PresenceMetrics) ObserveExpiry(result string, dur time.Duration, dueBuckets, examined, expired, indexRoutes, indexBuckets int)
func (m *PresenceMetrics) ObserveTouchFlush(result string, dur time.Duration, drained, resolved, sent, requeued, chunks, targetGroups int, budgetReached bool)
```

Export:

```text
wukongim_presence_expiry_total{result}
wukongim_presence_expiry_duration_seconds{result}
wukongim_presence_expiry_due_buckets
wukongim_presence_expiry_examined_routes
wukongim_presence_expired_routes
wukongim_presence_expiry_index_routes
wukongim_presence_expiry_index_buckets
wukongim_presence_touch_flush_total{result,budget_reached}
wukongim_presence_touch_flush_duration_seconds{result,budget_reached}
wukongim_presence_touch_flush_routes{stage}
wukongim_presence_touch_flush_chunks
wukongim_presence_touch_flush_target_groups
```

Allowed `stage` values are only `drained`, `resolved`, `sent`, and `requeued`.

- [ ] **Step 1: Add failing collector tests**

Add `TestPresenceMetricsExposeExpiryAndTouchFlush`. Gather the registry, assert every metric name and bounded label set, and fail if descriptors contain UID/session/hash-slot/target labels.

```bash
GOWORK=off go test ./pkg/metrics -run '^TestPresenceMetrics' -count=1
```

- [ ] **Step 2: Implement collectors and Registry wiring**

Add `Presence *PresenceMetrics` to `metrics.Registry`, initialize it with the standard node labels, and implement counter/histogram/gauge updates. Use gauges for latest index/work counts and counters for cumulative routes/chunks/groups.

- [ ] **Step 3: Add and implement one app observer**

Define one app observer that accepts `presence.ExpireResult` plus flush observation data and calls `Registry.Presence`. Wire it to the directory expiry call and touch worker. Observe every return path, including no work, cancellation, routing failure, RPC failure, and budget exhaustion.

- [ ] **Step 4: Add operational dashboard panels**

First extend `dashboard_assets_test.go` with semantic assertions for an expiry cost/index panel and a touch throughput/budget panel. Then add the panels to `wukongim-v2-runtime-ops.json`. Queries must reference all exported presence metric families so the global metric-assets gate passes.

```bash
GOWORK=off go test ./pkg/metrics ./internal/app ./docker/observability/grafana \
  -run 'PresenceMetrics|PresenceTouch|DashboardAssetsCoverAllExportedMetrics|Presence.*Panel' -count=1
```

- [ ] **Step 5: Commit observability**

```bash
git add pkg/metrics internal/app docker/observability/grafana
git commit -m "feat(metrics): observe presence expiry and touch work"
```

### Task 5: Validate concurrency, scaling, and real TTL stability

**Files:**
- Modify: `internal/app/FLOW.md`
- Modify: `internal/infra/cluster/FLOW.md`
- Modify: `internal/runtime/online/FLOW.md` only if its drain ownership text becomes inaccurate

- [ ] **Step 1: Update the cross-layer flows**

Document the 65,536 total budget, repeated bounded drains, aligned partial routing, sequential target dispatch, exact requeue ownership, and expiry observations. Preserve the single-node-cluster wording.

- [ ] **Step 2: Run focused race coverage**

```bash
GOWORK=off go test -race \
  ./internal/runtime/presence ./internal/runtime/online ./internal/usecase/presence \
  ./internal/infra/cluster ./pkg/cluster/routing ./pkg/cluster ./internal/app \
  -run 'Presence|RouteKeysPartial' -count=1
```

- [ ] **Step 3: Run all affected packages**

```bash
GOWORK=off go test \
  ./internal/runtime/presence ./internal/runtime/online ./internal/usecase/presence \
  ./internal/infra/cluster ./internal/app ./internal/config ./cmd/wukongim \
  ./pkg/cluster/routing ./pkg/cluster ./pkg/metrics ./docker/observability/grafana \
  -count=1
```

- [ ] **Step 4: Run the real three-node acceptance script**

```bash
WK_BENCH_PRESENCE_USERS=100000 \
WK_BENCH_PRESENCE_CONNECT_RATE=5000 \
WK_BENCH_PRESENCE_DURATION=190s \
WK_BENCH_PRESENCE_WARMUP=30s \
WK_BENCH_PRESENCE_HEARTBEAT_INTERVAL=30s \
WK_PRESENCE_TOUCH_BATCH_SIZE=512 \
WK_PRESENCE_TOUCH_MAX_ROUTES_PER_FLUSH=65536 \
bash scripts/bench-wukongim-three-nodes-presence.sh
```

Acceptance: owner/authority active counts stay at 100,000 across stable samples; expired routes remain zero after warmup; dirty touch work does not grow monotonically; fresh expiry ticks examine zero candidates; total runtime exceeds twice the 90-second default TTL.

- [ ] **Step 5: Check the patch**

```bash
git diff --check
git status --short
```
