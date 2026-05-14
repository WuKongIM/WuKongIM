# Channel Runtime Execution Virtualization Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an opt-in pooled channel replica execution mode that keeps per-channel single-writer semantics while bounding goroutine growth by configured worker pools instead of active channel count.

**Architecture:** Phase 1 introduces a `pkg/channel/replica` execution abstraction with the current dedicated loop as the default implementation and a new pooled implementation behind config. The replica public interface remains unchanged; `internal/app` owns the optional shared execution pool and passes it through `channelReplicaFactory` into `channelreplica.NewReplica`. Existing `WK_CLUSTER_MAX_CHANNELS` and idle eviction remain safety rails.

**Tech Stack:** Go, `pkg/channel/replica`, `pkg/channel/runtime`, `internal/app` config/build wiring, Prometheus metrics, existing Go unit/stress tests.

---

## Scope

This plan implements only Phase 1 from `docs/superpowers/specs/2026-05-14-channel-runtime-execution-virtualization-design.md`:

- add shared replica loop workers;
- centralize append flush scheduling to avoid per-channel timer goroutines;
- optionally pool append/checkpoint durable effect workers after loop pooling is green;
- expose low-cardinality pooled execution metrics;
- wire disabled-by-default app config;
- add pressure tests proving goroutine growth changes.

This plan does **not** implement hibernation or batch replication lane protocol changes.

## File Map

- `pkg/channel/replica/types.go`
  - Add execution mode/config types to `ReplicaConfig`.
- `pkg/channel/replica/execution_pool.go`
  - New shared worker pool, mailbox queue, ready queue, timer scheduler, metrics hooks.
- `pkg/channel/replica/loop_driver.go`
  - New internal `replicaLoopDriver` abstraction and dedicated driver implementation.
- `pkg/channel/replica/pooled_loop_driver.go`
  - New pooled driver implementation used when `ReplicaConfig.Execution.Pool` is non-nil.
- `pkg/channel/replica/loop.go`
  - Move existing loop submit/start logic behind the loop driver.
- `pkg/channel/replica/append_pipeline.go`
  - Replace per-flush goroutine with driver/pool scheduling.
- `pkg/channel/replica/checkpoint_writer.go`
  - Keep current dedicated worker first; later task can route through pooled durable workers.
- `pkg/channel/replica/replica.go`
  - Initialize and stop the chosen execution driver/pool handles.
- `pkg/channel/replica/*_test.go`
  - Add dedicated/pooled parity tests, fairness tests, close/cancel tests, and pressure tests.
- `pkg/metrics/channel.go`
  - Add low-cardinality execution queue/worker metrics.
- `internal/app/config.go`
  - Add cluster config fields and validation.
- `cmd/wukongim/config.go`
  - Parse `WK_CLUSTER_CHANNEL_EXECUTION_*` config values.
- `internal/app/channelmeta.go`
  - Pass execution config/pool through `channelReplicaFactory`.
- `internal/app/build.go`
  - Create and close the optional shared replica execution pool.
- `wukongim.conf.example`
  - Document the new config keys.
- `docs/development/PROJECT_KNOWLEDGE.md`
  - Add a short note if implementation introduces durable project rules.
- `pkg/channel/FLOW.md` and `pkg/channel/replica/FLOW.md`
  - Update after behavior changes.

## Implementation Tasks

### Task 1: Add execution config types with dedicated defaults

**Files:**
- Modify: `pkg/channel/replica/types.go`
- Test: `pkg/channel/replica/api_test.go`

- [ ] **Step 1: Write failing tests for invalid execution config**

Add tests that prove `NewReplica` rejects invalid mode and negative queue/worker values, while the zero value stays dedicated.

```go
func TestNewReplicaRejectsInvalidExecutionMode(t *testing.T) {
    env := newReplicaTestEnv(t)
    cfg := env.config()
    cfg.Execution.Mode = ExecutionMode("bogus")

    _, err := NewReplica(cfg)
    if !errors.Is(err, channel.ErrInvalidConfig) {
        t.Fatalf("NewReplica() error = %v, want invalid config", err)
    }
}

func TestNewReplicaDefaultsExecutionModeToDedicated(t *testing.T) {
    env := newReplicaTestEnv(t)
    cfg := env.config()

    got, err := NewReplica(cfg)
    if err != nil {
        t.Fatalf("NewReplica() error = %v", err)
    }
    t.Cleanup(func() { _ = got.Close() })
}
```

- [ ] **Step 2: Run tests and verify red**

Run:

```bash
go test ./pkg/channel/replica -run 'TestNewReplicaRejectsInvalidExecutionMode|TestNewReplicaDefaultsExecutionModeToDedicated' -count=1 -v
```

Expected: compile failure because `ExecutionMode` / `ReplicaConfig.Execution` do not exist.

- [ ] **Step 3: Add minimal config types**

Add to `pkg/channel/replica/types.go`:

```go
type ExecutionMode string

const (
    ExecutionModeDedicated ExecutionMode = "dedicated"
    ExecutionModePooled    ExecutionMode = "pooled"
)

type ExecutionConfig struct {
    // Mode selects how replica loop and effect work is executed.
    Mode ExecutionMode
    // Pool is the shared worker pool used when Mode is pooled.
    Pool *ExecutionPool
    // MailboxSize bounds per-replica queued loop work in pooled mode.
    MailboxSize int
    // TurnBudget limits how many loop events one worker processes for a replica before yielding.
    TurnBudget int
}
```

Extend `ReplicaConfig`:

```go
Execution ExecutionConfig
```

- [ ] **Step 4: Validate config in `NewReplica`**

In `pkg/channel/replica/replica.go`, before constructing `replica`, normalize and validate:

```go
if cfg.Execution.Mode == "" {
    cfg.Execution.Mode = ExecutionModeDedicated
}
switch cfg.Execution.Mode {
case ExecutionModeDedicated:
case ExecutionModePooled:
    if cfg.Execution.Pool == nil {
        return nil, channel.ErrInvalidConfig
    }
default:
    return nil, channel.ErrInvalidConfig
}
if cfg.Execution.MailboxSize < 0 || cfg.Execution.TurnBudget < 0 {
    return nil, channel.ErrInvalidConfig
}
```

- [ ] **Step 5: Run tests and verify green**

Run:

```bash
go test ./pkg/channel/replica -run 'TestNewReplicaRejectsInvalidExecutionMode|TestNewReplicaDefaultsExecutionModeToDedicated' -count=1 -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/channel/replica/types.go pkg/channel/replica/api_test.go pkg/channel/replica/replica.go
git commit -m "replica: add execution mode config"
```

### Task 2: Extract dedicated loop driver without behavior change

**Files:**
- Create: `pkg/channel/replica/loop_driver.go`
- Modify: `pkg/channel/replica/loop.go`
- Modify: `pkg/channel/replica/replica.go`
- Test: `pkg/channel/replica/lifecycle_test.go`

- [ ] **Step 1: Write failing lifecycle test for driver shutdown**

Add a test that proves the dedicated driver still closes `loopDone` on `Close`:

```go
func TestDedicatedLoopDriverStopsOnClose(t *testing.T) {
    env := newReplicaTestEnv(t)
    rep := env.mustReplica(t)
    r := rep.(*replica)

    if err := rep.Close(); err != nil {
        t.Fatalf("Close() error = %v", err)
    }
    requireClosed(t, r.loopDone, "replica loop did not stop")
}
```

- [ ] **Step 2: Run targeted test**

Run:

```bash
go test ./pkg/channel/replica -run TestDedicatedLoopDriverStopsOnClose -count=1 -v
```

Expected: PASS before refactor. This is a characterization test.

- [ ] **Step 3: Add loop driver interface and dedicated implementation**

Create `pkg/channel/replica/loop_driver.go`:

```go
type replicaLoopDriver interface {
    start()
    submitCommand(context.Context, machineEvent) machineResult
    submitResult(context.Context, machineEvent) error
    done() <-chan struct{}
}

type dedicatedLoopDriver struct {
    replica *replica
}

func newDedicatedLoopDriver(r *replica) *dedicatedLoopDriver { return &dedicatedLoopDriver{replica: r} }
```

Move the current `startLoop`, `submitLoopCommand`, and `submitLoopResult` logic into methods on `dedicatedLoopDriver`. Keep wrappers on `replica` so call sites do not all change at once:

```go
func (r *replica) submitLoopCommand(ctx context.Context, event machineEvent) machineResult {
    if r == nil || r.loop == nil {
        return machineResult{Err: channel.ErrNotLeader}
    }
    return r.loop.submitCommand(ctx, event)
}
```

- [ ] **Step 4: Add `loop replicaLoopDriver` to `replica`**

Modify `pkg/channel/replica/replica.go`:

```go
loop replicaLoopDriver
```

During construction:

```go
r.loop = newDedicatedLoopDriver(r)
```

Start it with:

```go
r.loop.start()
```

Close waits on:

```go
if r.loop != nil && r.loop.done() != nil {
    <-r.loop.done()
}
```

- [ ] **Step 5: Run replica tests**

Run:

```bash
go test ./pkg/channel/replica -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/channel/replica/loop_driver.go pkg/channel/replica/loop.go pkg/channel/replica/replica.go pkg/channel/replica/lifecycle_test.go
git commit -m "replica: extract loop driver"
```

### Task 3: Implement pooled loop pool and mailbox

**Files:**
- Create: `pkg/channel/replica/execution_pool.go`
- Create: `pkg/channel/replica/pooled_loop_driver.go`
- Test: `pkg/channel/replica/pooled_loop_test.go`

- [ ] **Step 1: Write failing serialization test**

Create `pkg/channel/replica/pooled_loop_test.go` with a test that submits many commands concurrently and asserts the state machine remains serialized. Use existing append/apply tests as references; keep the first test focused on ordering and no panic.

```go
func TestPooledLoopSerializesReplicaCommands(t *testing.T) {
    pool, err := NewExecutionPool(ExecutionPoolConfig{Workers: 2, MailboxSize: 64, TurnBudget: 4})
    if err != nil { t.Fatalf("NewExecutionPool() error = %v", err) }
    t.Cleanup(func() { _ = pool.Close() })

    env := newReplicaTestEnv(t)
    cfg := env.config()
    cfg.Execution = ExecutionConfig{Mode: ExecutionModePooled, Pool: pool, MailboxSize: 64, TurnBudget: 4}

    got, err := NewReplica(cfg)
    if err != nil { t.Fatalf("NewReplica() error = %v", err) }
    t.Cleanup(func() { _ = got.Close() })

    for i := 0; i < 100; i++ {
        if err := got.ApplyMeta(env.metaWithEpoch(uint64(i + 1))); err != nil {
            t.Fatalf("ApplyMeta(%d) error = %v", i+1, err)
        }
    }
}
```

- [ ] **Step 2: Run test and verify red**

Run:

```bash
go test ./pkg/channel/replica -run TestPooledLoopSerializesReplicaCommands -count=1 -v
```

Expected: compile failure because `NewExecutionPool` / `ExecutionPoolConfig` do not exist.

- [ ] **Step 3: Implement `ExecutionPool` skeleton**

Create `pkg/channel/replica/execution_pool.go`:

```go
type ExecutionPoolConfig struct {
    Workers     int
    MailboxSize int
    TurnBudget  int
    Now         func() time.Time
    Logger      wklog.Logger
}

type ExecutionPool struct {
    cfg ExecutionPoolConfig
    ready chan *pooledLoopDriver
    stopCh chan struct{}
    done chan struct{}
    closeOnce sync.Once
}
```

Defaults:

```go
if cfg.Workers <= 0 { cfg.Workers = runtime.GOMAXPROCS(0) }
if cfg.MailboxSize <= 0 { cfg.MailboxSize = 1024 }
if cfg.TurnBudget <= 0 { cfg.TurnBudget = 8 }
```

Reject negative values with `channel.ErrInvalidConfig`.

- [ ] **Step 4: Implement `pooledLoopDriver` mailbox**

Create `pkg/channel/replica/pooled_loop_driver.go`:

```go
type pooledLoopMessage struct {
    command *replicaLoopCommand
    event   machineEvent
    result  bool
}

type pooledLoopDriver struct {
    replica *replica
    pool *ExecutionPool
    mu sync.Mutex
    queue []pooledLoopMessage
    queued bool
    closed bool
    doneCh chan struct{}
    mailboxSize int
    turnBudget int
}
```

Core behavior:

- `submitCommand` enqueues a command and waits for reply/context/stop;
- `submitResult` enqueues an event;
- `enqueueLocked` fails with `channel.ErrBackpressure` or `channel.ErrNotReady` if mailbox is full;
- `drain` processes up to `turnBudget` messages under worker ownership;
- if messages remain, requeue the driver.

Use an existing channel error if appropriate; otherwise add a replica-local `ErrExecutionQueueFull` only if project style allows it.

- [ ] **Step 5: Wire `NewReplica` to use pooled loop when configured**

In `NewReplica`:

```go
if cfg.Execution.Mode == ExecutionModePooled {
    r.loop = newPooledLoopDriver(r, cfg.Execution)
} else {
    r.loop = newDedicatedLoopDriver(r)
}
```

- [ ] **Step 6: Run pooled loop tests**

Run:

```bash
go test ./pkg/channel/replica -run PooledLoop -count=1 -v
```

Expected: PASS.

- [ ] **Step 7: Run all replica tests**

Run:

```bash
go test ./pkg/channel/replica -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/channel/replica/execution_pool.go pkg/channel/replica/pooled_loop_driver.go pkg/channel/replica/replica.go pkg/channel/replica/pooled_loop_test.go
git commit -m "replica: add pooled loop execution"
```

### Task 4: Replace per-channel append flush goroutine with scheduler

**Files:**
- Modify: `pkg/channel/replica/loop_driver.go`
- Modify: `pkg/channel/replica/execution_pool.go`
- Modify: `pkg/channel/replica/append_pipeline.go`
- Test: `pkg/channel/replica/append_test.go`
- Test: `pkg/channel/replica/pooled_loop_test.go`

- [ ] **Step 1: Write failing pooled timer pressure test**

Add a test that creates many pooled replicas, queues one append per leader, and asserts goroutine growth stays below a small bound. Keep the count modest in unit tests, for example 200 replicas.

```go
func TestPooledAppendFlushDoesNotSpawnTimerGoroutinePerReplica(t *testing.T) {
    before := runtime.NumGoroutine()
    pool, err := NewExecutionPool(ExecutionPoolConfig{Workers: 4, MailboxSize: 64, TurnBudget: 4})
    if err != nil { t.Fatalf("NewExecutionPool() error = %v", err) }
    t.Cleanup(func() { _ = pool.Close() })

    reps := make([]Replica, 0, 200)
    for i := 0; i < 200; i++ {
        env := newReplicaTestEnv(t)
        cfg := env.config()
        cfg.Execution = ExecutionConfig{Mode: ExecutionModePooled, Pool: pool, MailboxSize: 64, TurnBudget: 4}
        cfg.AppendGroupCommitMaxWait = 50 * time.Millisecond
        rep, err := NewReplica(cfg)
        if err != nil { t.Fatalf("NewReplica(%d) error = %v", i, err) }
        reps = append(reps, rep)
    }
    t.Cleanup(func() { for _, rep := range reps { _ = rep.Close() } })

    // Promote and append according to existing helper patterns.
    // The assertion should allow test harness noise but fail if 200 timer goroutines are created.
    if delta := runtime.NumGoroutine() - before; delta > 80 {
        t.Fatalf("goroutines delta = %d, want <= 80", delta)
    }
}
```

- [ ] **Step 2: Run test and verify red**

Run:

```bash
go test ./pkg/channel/replica -run TestPooledAppendFlushDoesNotSpawnTimerGoroutinePerReplica -count=1 -v
```

Expected: FAIL before scheduler change if per-channel timer goroutines are still created.

- [ ] **Step 3: Add delayed scheduling to loop driver**

Extend `replicaLoopDriver`:

```go
scheduleResult(delay time.Duration, event machineEvent)
```

Dedicated mode can use `time.AfterFunc`:

```go
func (d *dedicatedLoopDriver) scheduleResult(delay time.Duration, event machineEvent) {
    time.AfterFunc(delay, func() { _ = d.submitResult(context.Background(), event) })
}
```

Pooled mode delegates to the pool timer scheduler.

- [ ] **Step 4: Add pool timer scheduler**

In `ExecutionPool`, add a single scheduler goroutine that owns a min-heap of due events or use `time.AfterFunc` callbacks that enqueue without holding per-channel goroutines. Prefer a single scheduler if pressure tests show many `AfterFunc` callbacks are noisy.

- [ ] **Step 5: Replace `scheduleAppendFlush`**

In `pkg/channel/replica/append_pipeline.go`:

```go
func (r *replica) scheduleAppendFlush() {
    delay := r.appendGroupCommit.maxWait
    if delay <= 0 { delay = time.Millisecond }
    if r.loop != nil {
        r.loop.scheduleResult(delay, machineAppendFlushEvent{})
    }
}
```

Add a local timer version if stale flushes become observable in tests.

- [ ] **Step 6: Run append and pooled tests**

Run:

```bash
go test ./pkg/channel/replica -run 'Append|Pooled' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/channel/replica/loop_driver.go pkg/channel/replica/execution_pool.go pkg/channel/replica/append_pipeline.go pkg/channel/replica/append_test.go pkg/channel/replica/pooled_loop_test.go
git commit -m "replica: schedule pooled append flushes centrally"
```

### Task 5: Pool durable append and checkpoint effect workers

**Files:**
- Modify: `pkg/channel/replica/execution_pool.go`
- Modify: `pkg/channel/replica/append_pipeline.go`
- Modify: `pkg/channel/replica/checkpoint_writer.go`
- Modify: `pkg/channel/replica/replica.go`
- Test: `pkg/channel/replica/durable_effect_fence_test.go`
- Test: `pkg/channel/replica/lifecycle_test.go`

- [ ] **Step 1: Write failing worker-count test**

Add a pooled-mode test that creates many replicas and asserts creating replicas does not add two durable worker goroutines per replica.

```go
func TestPooledExecutionDoesNotStartDurableWorkerPerReplica(t *testing.T) {
    before := runtime.NumGoroutine()
    pool, err := NewExecutionPool(ExecutionPoolConfig{Workers: 4, AppendWorkers: 4, CheckpointWorkers: 2})
    if err != nil { t.Fatalf("NewExecutionPool() error = %v", err) }
    t.Cleanup(func() { _ = pool.Close() })

    reps := make([]Replica, 0, 200)
    for i := 0; i < 200; i++ {
        env := newReplicaTestEnv(t)
        cfg := env.config()
        cfg.Execution = ExecutionConfig{Mode: ExecutionModePooled, Pool: pool}
        rep, err := NewReplica(cfg)
        if err != nil { t.Fatalf("NewReplica(%d) error = %v", i, err) }
        reps = append(reps, rep)
    }
    t.Cleanup(func() { for _, rep := range reps { _ = rep.Close() } })

    if delta := runtime.NumGoroutine() - before; delta > 80 {
        t.Fatalf("goroutines delta = %d, want <= 80", delta)
    }
}
```

- [ ] **Step 2: Run test and verify red**

Run:

```bash
go test ./pkg/channel/replica -run TestPooledExecutionDoesNotStartDurableWorkerPerReplica -count=1 -v
```

Expected: FAIL if per-replica append/checkpoint workers still start in pooled mode.

- [ ] **Step 3: Add pooled effect queues**

Extend `ExecutionPoolConfig`:

```go
AppendWorkers     int
CheckpointWorkers int
EffectQueueSize   int
```

Add pool queues:

```go
type pooledAppendEffect struct { replica *replica; effect appendLeaderBatchEffect }
type pooledCheckpointEffect struct { replica *replica; effect storeCheckpointEffect }
```

Workers execute:

```go
item.replica.runAppendEffect(ctx, item.effect)
```

and:

```go
err := item.replica.storeCheckpointEffect(ctx, item.effect)
_ = item.replica.submitLoopResult(context.Background(), machineCheckpointStoredEvent{...})
```

- [ ] **Step 4: Route effect submission through pool in pooled mode**

In `emitAppendBatchLocked` and `emitCheckpointEffectLocked`, replace direct channel sends with methods:

```go
r.submitAppendEffectLocked(effect)
r.submitCheckpointEffectLocked(effect)
```

Dedicated mode keeps existing channels and workers. Pooled mode submits to `ExecutionPool`.

- [ ] **Step 5: Do not start per-replica effect workers in pooled mode**

In `NewReplica`:

```go
if cfg.Execution.Mode == ExecutionModePooled {
    close(r.appendWorkerDone)
    close(r.checkpointWorkerDone)
} else {
    r.startAppendEffectWorker()
    r.startCheckpointEffectWorker()
}
```

Only do this if tests confirm `Close` semantics remain safe. Alternatively, set done channels to already-closed helper channels.

- [ ] **Step 6: Run durable fence tests**

Run:

```bash
go test ./pkg/channel/replica -run 'Durable|Checkpoint|PooledExecutionDoesNotStartDurableWorkerPerReplica' -count=1
```

Expected: PASS.

- [ ] **Step 7: Run all replica tests**

Run:

```bash
go test ./pkg/channel/replica -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/channel/replica/execution_pool.go pkg/channel/replica/append_pipeline.go pkg/channel/replica/checkpoint_writer.go pkg/channel/replica/replica.go pkg/channel/replica/*_test.go
git commit -m "replica: pool durable effect workers"
```

### Task 6: Add app configuration and build wiring

**Files:**
- Modify: `internal/app/config.go`
- Modify: `cmd/wukongim/config.go`
- Modify: `internal/app/channelmeta.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/build_test.go`
- Modify: `cmd/wukongim/config_test.go`
- Modify: `wukongim.conf.example`

- [ ] **Step 1: Write config tests**

Add tests for parsing and validation:

```go
func TestConfigPreservesChannelExecutionPoolSettings(t *testing.T) {
    cfg := DefaultConfig()
    cfg.Cluster.ChannelExecutionMode = "pooled"
    cfg.Cluster.ChannelExecutionWorkers = 8
    cfg.Cluster.ChannelExecutionQueueSize = 4096

    require.NoError(t, cfg.ApplyDefaultsAndValidate())
    require.Equal(t, "pooled", cfg.Cluster.ChannelExecutionMode)
    require.Equal(t, 8, cfg.Cluster.ChannelExecutionWorkers)
    require.Equal(t, 4096, cfg.Cluster.ChannelExecutionQueueSize)
}
```

Add CLI parse test with env/config keys:

```go
require.Equal(t, "pooled", cfg.Cluster.ChannelExecutionMode)
require.Equal(t, 8, cfg.Cluster.ChannelExecutionWorkers)
require.Equal(t, 4096, cfg.Cluster.ChannelExecutionQueueSize)
```

- [ ] **Step 2: Run tests and verify red**

Run:

```bash
go test ./internal/app ./cmd/wukongim -run 'ChannelExecution|BuildLongPoll' -count=1 -v
```

Expected: compile failures for missing fields.

- [ ] **Step 3: Add config fields with English comments**

In `internal/app/config.go` `ClusterConfig`:

```go
// ChannelExecutionMode selects how local channel replica execution is scheduled.
// "dedicated" keeps the legacy per-replica workers; "pooled" uses shared worker pools.
ChannelExecutionMode string
// ChannelExecutionWorkers is the number of shared workers used by pooled channel execution.
// A zero value lets the runtime derive a default from GOMAXPROCS.
ChannelExecutionWorkers int
// ChannelExecutionQueueSize bounds pooled execution queues to avoid unbounded memory growth.
ChannelExecutionQueueSize int
```

Validate:

```go
switch c.Cluster.ChannelExecutionMode {
case "", "dedicated", "pooled":
default:
    return fmt.Errorf("%w: channel execution mode must be dedicated or pooled", ErrInvalidConfig)
}
if c.Cluster.ChannelExecutionWorkers < 0 || c.Cluster.ChannelExecutionQueueSize < 0 {
    return fmt.Errorf("%w: channel execution worker and queue limits must be >= 0", ErrInvalidConfig)
}
if c.Cluster.ChannelExecutionMode == "" { c.Cluster.ChannelExecutionMode = "dedicated" }
```

- [ ] **Step 4: Parse config keys**

In `cmd/wukongim/config.go`, parse:

- `WK_CLUSTER_CHANNEL_EXECUTION_MODE`
- `WK_CLUSTER_CHANNEL_EXECUTION_WORKERS`
- `WK_CLUSTER_CHANNEL_EXECUTION_QUEUE_SIZE`

- [ ] **Step 5: Wire app build**

In `internal/app/build.go`, create the pool when mode is pooled:

```go
var replicaExecutionPool *channelreplica.ExecutionPool
if cfg.Cluster.ChannelExecutionMode == "pooled" {
    replicaExecutionPool, err = channelreplica.NewExecutionPool(channelreplica.ExecutionPoolConfig{
        Workers: cfg.Cluster.ChannelExecutionWorkers,
        MailboxSize: cfg.Cluster.ChannelExecutionQueueSize,
        Logger: app.logger.Named("channel.replica.execution"),
    })
    if err != nil { return nil, fmt.Errorf("app: create channel replica execution pool: %w", err) }
    cleanup.Push("channel replica execution pool", replicaExecutionPool.Close)
}
```

Pass into `newChannelReplicaFactory`, store on `channelReplicaFactory`, and set `ReplicaConfig.Execution` in `New`.

- [ ] **Step 6: Update config example**

Add comments and defaults in `wukongim.conf.example`:

```conf
# Selects local channel replica execution scheduling. dedicated preserves legacy per-replica workers; pooled uses shared workers.
WK_CLUSTER_CHANNEL_EXECUTION_MODE=dedicated
# Shared worker count for pooled channel execution. 0 derives a default from GOMAXPROCS.
WK_CLUSTER_CHANNEL_EXECUTION_WORKERS=0
# Per-replica mailbox queue size in pooled execution. 0 uses the runtime default.
WK_CLUSTER_CHANNEL_EXECUTION_QUEUE_SIZE=0
```

- [ ] **Step 7: Run app/cmd tests**

Run:

```bash
go test ./internal/app ./cmd/wukongim -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/app/config.go cmd/wukongim/config.go internal/app/channelmeta.go internal/app/build.go internal/app/build_test.go cmd/wukongim/config_test.go wukongim.conf.example
git commit -m "app: wire channel replica pooled execution config"
```

### Task 7: Add pooled execution metrics

**Files:**
- Modify: `pkg/metrics/channel.go`
- Modify: `pkg/metrics/registry_test.go`
- Modify: `internal/app/build.go`
- Modify: `docker/observability/grafana/dashboards/wukongim-runtime-storage.json`
- Modify: `docker/observability/prometheus/alerts/wukongim-channel-runtime.yml` if alerts are desired

- [ ] **Step 1: Write metrics tests**

In `pkg/metrics/registry_test.go`, add expected metrics:

- `wukongim_channel_execution_queue_depth`
- `wukongim_channel_execution_enqueue_total{result="ok|queue_full"}`
- `wukongim_channel_execution_worker_busy_ratio`
- `wukongim_channel_execution_mailbox_wait_duration_seconds`

- [ ] **Step 2: Run test and verify red**

Run:

```bash
go test ./pkg/metrics -run TestChannelMetricsTrackAppendFetchAndActiveChannels -count=1 -v
```

Expected: FAIL because metrics do not exist.

- [ ] **Step 3: Add low-cardinality metric methods**

In `pkg/metrics/channel.go`, add methods:

```go
func (m *ChannelMetrics) SetExecutionQueueDepth(v int)
func (m *ChannelMetrics) ObserveExecutionEnqueue(result string)
func (m *ChannelMetrics) SetExecutionWorkerBusyRatio(v float64)
func (m *ChannelMetrics) ObserveExecutionMailboxWait(d time.Duration)
```

- [ ] **Step 4: Connect pool observer to metrics**

Define a lightweight observer in `pkg/channel/replica`:

```go
type ExecutionObserver interface {
    SetQueueDepth(int)
    ObserveEnqueue(result string)
    SetWorkerBusyRatio(float64)
    ObserveMailboxWait(time.Duration)
}
```

Set it from `internal/app/build.go` when app metrics are enabled.

- [ ] **Step 5: Update Grafana dashboard coverage**

Add a panel to `docker/observability/grafana/dashboards/wukongim-runtime-storage.json` that references the new metrics so `TestDashboardAssetsCoverAllExportedMetrics` remains green.

- [ ] **Step 6: Run observability tests**

Run:

```bash
go test ./pkg/metrics ./docker/observability/grafana ./docker/observability/prometheus ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/metrics/channel.go pkg/metrics/registry_test.go pkg/channel/replica/types.go internal/app/build.go docker/observability/grafana/dashboards/wukongim-runtime-storage.json docker/observability/prometheus/alerts/wukongim-channel-runtime.yml
git commit -m "observability: add channel execution pool metrics"
```

### Task 8: Add pooled-mode runtime and pressure tests

**Files:**
- Modify: `pkg/channel/runtime/activation_stress_test.go`
- Modify: `pkg/channel/runtime/pressure_testutil_test.go`
- Test: `pkg/channel/runtime/activation_stress_test.go`
- Create or modify: `pkg/channel/replica/execution_pressure_test.go`

- [ ] **Step 1: Extend stress config**

Add environment variables:

- `MULTIISR_ACTIVATION_EXECUTION_MODE=dedicated|pooled`
- `MULTIISR_ACTIVATION_EXECUTION_WORKERS=16`
- `MULTIISR_ACTIVATION_EXECUTION_QUEUE_SIZE=1024`

- [ ] **Step 2: Add pooled activation pressure test**

The existing activation stress should create a shared `channelreplica.ExecutionPool` when pooled mode is requested and pass it to `activationReplicaFactory`.

- [ ] **Step 3: Add light-active pressure test**

Create a skipped-by-default test:

```go
func TestReplicaStressPooledLightActiveChannels(t *testing.T) {
    if os.Getenv("WK_REPLICA_POOLED_STRESS") != "1" { t.Skip("...") }
    // Create N pooled replicas, promote leaders, periodically append/fetch across M active channels.
    // Log goroutines_delta, heap_alloc_delta, enqueue latency, throughput.
}
```

- [ ] **Step 4: Verify stress tests stay skipped by default**

Run:

```bash
go test ./pkg/channel/runtime ./pkg/channel/replica -run 'Stress|Pressure' -count=1 -v
```

Expected: stress tests skip unless env vars are set.

- [ ] **Step 5: Run a small enabled pooled stress**

Run a small count suitable for local development:

```bash
MULTIISR_ACTIVATION_STRESS=1 \
MULTIISR_ACTIVATION_CHANNELS=1000 \
MULTIISR_ACTIVATION_EXECUTION_MODE=pooled \
MULTIISR_ACTIVATION_EXECUTION_WORKERS=8 \
go test ./pkg/channel/runtime -run TestRuntimeStressChannelActivationFootprint -count=1 -v
```

Expected: PASS and goroutine delta near pool size plus fixed overhead, not near `3 * channels`.

- [ ] **Step 6: Commit**

```bash
git add pkg/channel/runtime/activation_stress_test.go pkg/channel/runtime/pressure_testutil_test.go pkg/channel/replica/execution_pressure_test.go
git commit -m "test: add pooled channel execution pressure coverage"
```

### Task 9: Update FLOW and project knowledge docs

**Files:**
- Modify: `pkg/channel/replica/FLOW.md`
- Modify: `pkg/channel/FLOW.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`

- [ ] **Step 1: Update replica flow**

In `pkg/channel/replica/FLOW.md`, document:

- dedicated mode keeps legacy per-replica workers;
- pooled mode uses shared workers;
- per-channel single-writer semantics are preserved;
- durable effects remain fenced and ordered.

- [ ] **Step 2: Update channel flow**

In `pkg/channel/FLOW.md`, add a short note near runtime/idle eviction describing pooled execution as an optional execution mode, not a different cluster semantic.

- [ ] **Step 3: Add durable project knowledge**

In `docs/development/PROJECT_KNOWLEDGE.md`, add one concise bullet:

```markdown
- Channel replica pooled execution must preserve per-channel single-writer ordering and all generation/epoch/fence checks; it is an execution-cache optimization, not a cluster semantic change.
```

- [ ] **Step 4: Commit**

```bash
git add pkg/channel/replica/FLOW.md pkg/channel/FLOW.md docs/development/PROJECT_KNOWLEDGE.md
git commit -m "docs: document pooled channel replica execution"
```

### Task 10: Final verification and rollout evidence

**Files:**
- No required code changes unless verification finds issues.

- [ ] **Step 1: Run targeted packages**

Run:

```bash
go test -count=1 ./pkg/channel/replica ./pkg/channel/runtime ./internal/app ./cmd/wukongim ./pkg/metrics ./docker/observability/grafana ./docker/observability/prometheus
```

Expected: PASS.

- [ ] **Step 2: Run all unit tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Run small pooled pressure test**

Run:

```bash
MULTIISR_ACTIVATION_STRESS=1 \
MULTIISR_ACTIVATION_CHANNELS=5000 \
MULTIISR_ACTIVATION_EXECUTION_MODE=pooled \
MULTIISR_ACTIVATION_EXECUTION_WORKERS=16 \
MULTIISR_ACTIVATION_REPORT_EVERY=1000 \
go test ./pkg/channel/runtime -run TestRuntimeStressChannelActivationFootprint -count=1 -v
```

Expected: PASS and goroutine delta bounded by worker count/fixed overhead. Record the final log line in the implementation summary.

- [ ] **Step 4: Compare dedicated baseline on small count**

Run:

```bash
MULTIISR_ACTIVATION_STRESS=1 \
MULTIISR_ACTIVATION_CHANNELS=5000 \
MULTIISR_ACTIVATION_EXECUTION_MODE=dedicated \
MULTIISR_ACTIVATION_REPORT_EVERY=1000 \
go test ./pkg/channel/runtime -run TestRuntimeStressChannelActivationFootprint -count=1 -v
```

Expected: PASS. Record goroutine delta and compare with pooled.

- [ ] **Step 5: Final commit if verification fixes were needed**

Only if fixes were required:

```bash
git add <fixed files>
git commit -m "test: stabilize pooled channel execution"
```

## Rollout Notes

Initial production rollout should keep:

```conf
WK_CLUSTER_CHANNEL_EXECUTION_MODE=dedicated
```

Then staging can enable:

```conf
WK_CLUSTER_CHANNEL_EXECUTION_MODE=pooled
WK_CLUSTER_CHANNEL_EXECUTION_WORKERS=16
WK_CLUSTER_CHANNEL_EXECUTION_QUEUE_SIZE=1024
WK_CLUSTER_MAX_CHANNELS=50000
WK_CLUSTER_CHANNEL_IDLE_TIMEOUT=30m
WK_CLUSTER_CHANNEL_IDLE_SCAN_INTERVAL=1m
```

Do not enable pooled mode in production until staging pressure confirms:

- goroutine delta is bounded;
- queue wait latency is acceptable;
- no append/fetch/replication correctness regressions;
- rollback to dedicated mode has been tested.

## Implementation Order Summary

1. Config types in `pkg/channel/replica`.
2. Dedicated loop driver extraction.
3. Pooled loop driver.
4. Central append flush scheduling.
5. Pooled durable effect workers.
6. App config and build wiring.
7. Metrics and dashboards.
8. Pressure tests.
9. FLOW/project knowledge docs.
10. Final verification.
