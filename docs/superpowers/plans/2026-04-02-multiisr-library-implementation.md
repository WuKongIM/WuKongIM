# MultiISR Library Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first working `pkg/multiisr` library that manages many `pkg/isr` replicas on one node with generation-safe lifecycle, ordered scheduling, peer-session reuse, batching, tombstones, and centralized limits.

**Architecture:** The implementation lives in `pkg/multiisr` and layers strictly on top of the existing `pkg/isr` package. Public contracts stay in `types.go`, lifecycle and generation allocation live in `registry.go` and `generation.go`, ordered multi-group execution lives in `scheduler.go`, and node-pair transport reuse lives in `session.go`, `transport.go`, and `batching.go`. Tests use in-memory fake `isr.Replica`, fake transport, fake session manager, and a manual clock so tombstone, scheduler order, late-packet dropping, and limit behavior can be verified deterministically.

**Tech Stack:** Go 1.23, standard library `context/errors/slices/sync/testing/time`, existing `pkg/isr`

---

## File Structure

### Production files

- Create: `pkg/multiisr/doc.go`
  Responsibility: package overview and V1 contract notes.
- Create: `pkg/multiisr/types.go`
  Responsibility: public runtime config, runtime/group interfaces, transport/session contracts, envelope types, limits, tombstone policy, and factory interfaces.
- Create: `pkg/multiisr/errors.go`
  Responsibility: exported sentinel errors and small internal helpers.
- Create: `pkg/multiisr/runtime.go`
  Responsibility: concrete runtime, constructor validation, lifecycle wiring, transport handler registration, and public methods.
- Create: `pkg/multiisr/group.go`
  Responsibility: per-group runtime state, append/status path, pending-task bits, lease pre-check, and replica ownership.
- Create: `pkg/multiisr/registry.go`
  Responsibility: live-group registry, tombstone map, lookup/list APIs, and delayed resource release.
- Create: `pkg/multiisr/generation.go`
  Responsibility: durable generation allocation/loading and tombstone generation matching.
- Create: `pkg/multiisr/scheduler.go`
  Responsibility: ordered task queues and worker-side dispatch with the hard order `control -> replication -> commit -> lease -> snapshot`.
- Create: `pkg/multiisr/session.go`
  Responsibility: peer-session manager adapter, per-peer backpressure observation, and envelope fan-in/fan-out helpers.
- Create: `pkg/multiisr/transport.go`
  Responsibility: inbound envelope registration, `(GroupID, Generation)` demux, stale-epoch dropping, and runtime-to-session send helpers.
- Create: `pkg/multiisr/batching.go`
  Responsibility: per-peer batch assembly, flush policy, and `TryBatch` fallback to `Send`.
- Create: `pkg/multiisr/limits.go`
  Responsibility: central group-count, fetch-inflight, snapshot-inflight, and recovery-bandwidth limits.
- Create: `pkg/multiisr/snapshot.go`
  Responsibility: snapshot task queueing, recovery throttling, and snapshot worker bookkeeping.

### Test files

- Create: `pkg/multiisr/testenv_test.go`
  Responsibility: fake replica factory, fake replica, fake generation store, fake transport, fake peer session manager, manual clock, and reusable assertions.
- Create: `pkg/multiisr/api_test.go`
  Responsibility: constructor validation and public-surface compile tests.
- Create: `pkg/multiisr/registry_test.go`
  Responsibility: generation allocation, tombstone lifecycle, and late-packet dropping.
- Create: `pkg/multiisr/scheduler_test.go`
  Responsibility: hard task ordering, dirty requeue, and append lease pre-check coverage.
- Create: `pkg/multiisr/session_test.go`
  Responsibility: peer-session reuse, batching, backpressure behavior, and inbound demux.
- Create: `pkg/multiisr/runtime_test.go`
  Responsibility: `EnsureGroup` / `ApplyMeta` / `RemoveGroup`, append/status exposure, and no per-group goroutine explosion.
- Create: `pkg/multiisr/limits_test.go`
  Responsibility: `ErrTooManyGroups`, fetch-inflight queueing, and throttling behavior.
- Create: `pkg/multiisr/snapshot_test.go`
  Responsibility: snapshot queueing and `MaxRecoveryBytesPerSecond` enforcement.

## Implementation Notes

- Follow `@superpowers:test-driven-development` for each task.
- Keep `pkg/multiisr` independent from `pkg/multiraft`; borrow scheduler/testing style, not multiraft semantics.
- V1 should implement the spec’s first phase completely and leave explicit extension seams for second-phase hot-group priority and more aggressive batching.
- `Generation` is node-local durable metadata. The runtime must persist `nextGeneration` before creating the new `isr.Replica`.
- Inbound packet demux key is always `(GroupID, Generation)`. `Epoch` is freshness validation after the correct instance is found.
- `Append` must synchronously re-check lease immediately before entering the replica append critical section, even if a prior `LeaseTask` already ran.
- Avoid one goroutine per group. The only long-lived goroutines in V1 should be runtime workers, optional batch flushers, and bounded snapshot workers.

## Task 1: Lock the public API and constructor contract

**Files:**
- Create: `pkg/multiisr/doc.go`
- Create: `pkg/multiisr/types.go`
- Create: `pkg/multiisr/errors.go`
- Create: `pkg/multiisr/runtime.go`
- Test: `pkg/multiisr/api_test.go`

- [ ] **Step 1: Write the failing public-surface tests**

```go
func TestNewRuntimeValidatesRequiredDependencies(t *testing.T) {
	_, err := multiisr.New(multiisr.Config{})
	if !errors.Is(err, multiisr.ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestRuntimeSurfaceExposesReconcileAndLookup(t *testing.T) {
	var rt multiisr.Runtime
	var meta isr.GroupMeta

	compileRuntimeSurface := func(r multiisr.Runtime) {
		_ = r.EnsureGroup(meta)
		_ = r.ApplyMeta(meta)
		_ = r.RemoveGroup(meta.GroupID)
		_, _ = r.Group(meta.GroupID)
	}

	_ = compileRuntimeSurface
	_ = rt
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/multiisr -run 'TestNewRuntimeValidatesRequiredDependencies|TestRuntimeSurfaceExposesReconcileAndLookup' -v`

Expected: FAIL because `pkg/multiisr` does not exist yet.

- [ ] **Step 3: Add the package shell and public type definitions**

```go
type Config struct {
	LocalNode       isr.NodeID
	ReplicaFactory  ReplicaFactory
	GenerationStore GenerationStore
	Transport       Transport
	PeerSessions    PeerSessionManager
	Limits          Limits
	Tombstones      TombstonePolicy
	Now             func() time.Time
}

type Runtime interface {
	EnsureGroup(meta isr.GroupMeta) error
	RemoveGroup(groupID uint64) error
	ApplyMeta(meta isr.GroupMeta) error
	Group(groupID uint64) (GroupHandle, bool)
}

type GroupHandle interface {
	ID() uint64
	Status() isr.ReplicaState
	Append(ctx context.Context, records []isr.Record) (isr.CommitResult, error)
}
```

Implementation details:
- define `ReplicaFactory`, `GenerationStore`, `Transport`, `PeerSessionManager`, `PeerSession`, `Envelope`, `MessageKind`, `BackpressureState`, `Limits`, and `TombstonePolicy`
- add constructor signature `func New(cfg Config) (Runtime, error)`
- declare exported errors including `ErrInvalidConfig`, `ErrTooManyGroups`, `ErrGroupNotFound`, `ErrGenerationMismatch`, and `ErrBackpressured`

- [ ] **Step 4: Add constructor validation and sane defaults**

Validation rules for V1:
- `LocalNode != 0`
- `ReplicaFactory != nil`
- `GenerationStore != nil`
- `Transport != nil`
- `PeerSessions != nil`
- `Limits.MaxGroups >= 0` and `0` means "no explicit cap"
- `Tombstones.TombstoneTTL > 0`
- `Now` defaults to `time.Now` when nil

- [ ] **Step 5: Re-run the targeted tests**

Run: `go test ./pkg/multiisr -run 'TestNewRuntimeValidatesRequiredDependencies|TestRuntimeSurfaceExposesReconcileAndLookup' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/multiisr/doc.go pkg/multiisr/types.go pkg/multiisr/errors.go pkg/multiisr/runtime.go pkg/multiisr/api_test.go
git commit -m "feat: add multiisr runtime api shell"
```

## Task 2: Implement generation allocation, registry, and tombstone safety

**Files:**
- Modify: `pkg/multiisr/runtime.go`
- Create: `pkg/multiisr/group.go`
- Create: `pkg/multiisr/registry.go`
- Create: `pkg/multiisr/generation.go`
- Create: `pkg/multiisr/testenv_test.go`
- Create: `pkg/multiisr/registry_test.go`

- [ ] **Step 1: Write failing lifecycle tests**

```go
func TestEnsureGroupStoresNextGenerationBeforeReplicaCreation(t *testing.T) {
	env := newTestEnv(t)
	meta := testMeta(7, 1, 1, []isr.NodeID{1, 2})

	if err := env.runtime.EnsureGroup(meta); err != nil {
		t.Fatalf("EnsureGroup() error = %v", err)
	}
	if got := env.generations.stored[meta.GroupID]; got != 1 {
		t.Fatalf("expected generation 1, got %d", got)
	}
	if env.factory.created[0].generation != 1 {
		t.Fatalf("replica created with wrong generation")
	}
}

func TestRemoveGroupLeavesTombstoneThatDropsLateEnvelope(t *testing.T) {
	env := newTestEnv(t)
	meta := testMeta(9, 1, 1, []isr.NodeID{1, 2})
	mustEnsure(t, env.runtime, meta)
	mustRemove(t, env.runtime, meta.GroupID)

	env.transport.deliver(multiisr.Envelope{GroupID: 9, Generation: 1, Epoch: 1, Kind: multiisr.MessageKindAck})
	if env.factory.replicas[0].applyFetchCalls != 0 {
		t.Fatalf("late envelope should be dropped")
	}
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/multiisr -run 'TestEnsureGroupStoresNextGenerationBeforeReplicaCreation|TestRemoveGroupLeavesTombstoneThatDropsLateEnvelope' -v`

Expected: FAIL because generation persistence, registry, and tombstone behavior are not implemented.

- [ ] **Step 3: Build the fake stores and helpers**

```go
type fakeGenerationStore struct {
	mu     sync.Mutex
	values map[uint64]uint64
	stored map[uint64]uint64
}

type fakeReplicaFactory struct {
	mu      sync.Mutex
	created []createdReplica
	replicas []*fakeReplica
}

type fakeReplica struct {
	mu         sync.Mutex
	state      isr.ReplicaState
	appendErr  error
	metaCalls  []isr.GroupMeta
	appendCalls int
	applyFetchCalls int
}
```

Requirements:
- manual clock must support advancing tombstone expiry without sleeping
- fake runtime transport must expose `deliver(env Envelope)` for inbound-path tests
- `newTestEnv(t)` must create a runtime with `TombstoneTTL = 30 * time.Second`

- [ ] **Step 4: Implement durable generation allocation and the registry**

Implementation details:
- `EnsureGroup` must `Load -> next = current + 1 -> Store(next)` before calling `ReplicaFactory.New(...)`
- registry must keep active groups and tombstones separately
- tombstones must retain `(groupID, generation, expiresAt)` and reject append
- `RemoveGroup` must unregister the active group first, then move it to tombstone storage
- `Group(id)` must only return active groups, never tombstones

- [ ] **Step 5: Re-run the lifecycle tests plus API tests**

Run: `go test ./pkg/multiisr -run 'TestEnsureGroupStoresNextGenerationBeforeReplicaCreation|TestRemoveGroupLeavesTombstoneThatDropsLateEnvelope|TestNewRuntimeValidatesRequiredDependencies' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/multiisr/runtime.go pkg/multiisr/group.go pkg/multiisr/registry.go pkg/multiisr/generation.go pkg/multiisr/testenv_test.go pkg/multiisr/registry_test.go
git commit -m "feat: add multiisr registry and generation tombstones"
```

## Task 3: Implement the ordered scheduler and lease-safe append path

**Files:**
- Modify: `pkg/multiisr/group.go`
- Create: `pkg/multiisr/scheduler.go`
- Create: `pkg/multiisr/scheduler_test.go`

- [ ] **Step 1: Write failing scheduler tests**

```go
func TestSchedulerRunsTasksInRequiredOrder(t *testing.T) {
	g := newScheduledTestGroup()
	g.markSnapshot()
	g.markLease()
	g.markCommit()
	g.markReplication()
	g.markControl()

	got := runGroupOnce(t, g)
	want := []string{"control", "replication", "commit", "lease", "snapshot"}
	if !slices.Equal(want, got) {
		t.Fatalf("task order mismatch: want %v, got %v", want, got)
	}
}

func TestAppendChecksLeaseSynchronouslyBeforeReplicaAppend(t *testing.T) {
	env := newTestEnv(t)
	meta := fencedLeaderMeta(11)
	mustEnsure(t, env.runtime, meta)

	_, err := mustGroup(t, env.runtime, 11).Append(context.Background(), []isr.Record{{Payload: []byte("x"), SizeBytes: 1}})
	if !errors.Is(err, isr.ErrLeaseExpired) {
		t.Fatalf("expected ErrLeaseExpired, got %v", err)
	}
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/multiisr -run 'TestSchedulerRunsTasksInRequiredOrder|TestAppendChecksLeaseSynchronouslyBeforeReplicaAppend' -v`

Expected: FAIL because there is no ordered scheduler or append-side lease guard yet.

- [ ] **Step 3: Implement scheduler data structures**

```go
type taskMask uint8

const (
	taskControl taskMask = 1 << iota
	taskReplication
	taskCommit
	taskLease
	taskSnapshot
)
```

Implementation details:
- keep one scheduler queue of dirty group IDs plus per-group pending task bits
- worker execution order must be hard-coded, not derived from map iteration
- if a group is marked dirty while running, requeue it once after the current pass

- [ ] **Step 4: Integrate lease-safe append**

Implementation details:
- `group.Append(...)` must read the latest state and reject tombstoned groups
- immediately before calling `replica.Append(...)`, re-check whether the current status is fenced or lease-expired
- lease task should proactively mark the group fenced so subsequent appends fail fast

- [ ] **Step 5: Re-run the scheduler tests**

Run: `go test ./pkg/multiisr -run 'TestSchedulerRunsTasksInRequiredOrder|TestAppendChecksLeaseSynchronouslyBeforeReplicaAppend' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/multiisr/group.go pkg/multiisr/scheduler.go pkg/multiisr/scheduler_test.go
git commit -m "feat: add ordered multiisr scheduler"
```

## Task 4: Implement peer-session reuse, transport demux, and batching

**Files:**
- Create: `pkg/multiisr/session.go`
- Create: `pkg/multiisr/transport.go`
- Create: `pkg/multiisr/batching.go`
- Create: `pkg/multiisr/session_test.go`
- Modify: `pkg/multiisr/runtime.go`

- [ ] **Step 1: Write failing transport/session tests**

```go
func TestManyGroupsToSamePeerReuseOneSession(t *testing.T) {
	env := newTestEnv(t)
	mustEnsure(t, env.runtime, testMeta(21, 1, 1, []isr.NodeID{1, 2}))
	mustEnsure(t, env.runtime, testMeta(22, 1, 1, []isr.NodeID{1, 2}))

	env.enqueueReplication(21, 2)
	env.enqueueReplication(22, 2)
	env.runScheduler()

	if got := env.sessions.createdFor(2); got != 1 {
		t.Fatalf("expected one session for peer 2, got %d", got)
	}
}

func TestInboundEnvelopeDemuxRequiresMatchingGeneration(t *testing.T) {
	env := newTestEnv(t)
	mustEnsure(t, env.runtime, testMeta(23, 1, 1, []isr.NodeID{1, 2}))

	env.transport.deliver(multiisr.Envelope{GroupID: 23, Generation: 99, Epoch: 1, Kind: multiisr.MessageKindFetchResponse})
	if env.factory.replicas[0].applyFetchCalls != 0 {
		t.Fatalf("unexpected apply fetch on generation mismatch")
	}
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/multiisr -run 'TestManyGroupsToSamePeerReuseOneSession|TestInboundEnvelopeDemuxRequiresMatchingGeneration' -v`

Expected: FAIL because session reuse, demux, and batching are not implemented.

- [ ] **Step 3: Implement session and envelope plumbing**

Implementation details:
- `Transport.RegisterHandler(...)` should be called from `New(...)`
- outbound traffic must go through `PeerSessionManager.Session(peer)`
- `Envelope` must include `Peer`, `GroupID`, `Epoch`, `Generation`, `RequestID`, `Kind`, and `Payload`
- transport handler must first resolve `(GroupID, Generation)` and only then inspect `Epoch`

- [ ] **Step 4: Implement V1 batching and backpressure handling**

Implementation details:
- try `PeerSession.TryBatch(env)` first for fetch/ack/truncate traffic
- fall back to `PeerSession.Send(env)` when batching rejects
- on `BackpressureSoft`, keep queueing but stop generating unlimited new work in the same pass
- on `BackpressureHard`, leave the group dirty and retry on a later pass

- [ ] **Step 5: Re-run the session tests**

Run: `go test ./pkg/multiisr -run 'TestManyGroupsToSamePeerReuseOneSession|TestInboundEnvelopeDemuxRequiresMatchingGeneration' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/multiisr/session.go pkg/multiisr/transport.go pkg/multiisr/batching.go pkg/multiisr/session_test.go pkg/multiisr/runtime.go
git commit -m "feat: add multiisr peer session reuse and batching"
```

## Task 5: Wire reconcile flow, public operations, and central limits

**Files:**
- Modify: `pkg/multiisr/runtime.go`
- Modify: `pkg/multiisr/group.go`
- Create: `pkg/multiisr/limits.go`
- Create: `pkg/multiisr/runtime_test.go`
- Create: `pkg/multiisr/limits_test.go`

- [ ] **Step 1: Write failing reconcile and limit tests**

```go
func TestRuntimeReconcileFlowEnsureApplyRemoveEnsure(t *testing.T) {
	env := newTestEnv(t)
	meta1 := testMeta(31, 1, 1, []isr.NodeID{1, 2})
	meta2 := testMeta(31, 2, 2, []isr.NodeID{1, 2})

	mustEnsure(t, env.runtime, meta1)
	if err := env.runtime.ApplyMeta(meta2); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}
	mustRemove(t, env.runtime, 31)
	if err := env.runtime.EnsureGroup(meta2); err != nil {
		t.Fatalf("EnsureGroup() after remove error = %v", err)
	}
}

func TestEnsureGroupReturnsErrTooManyGroups(t *testing.T) {
	env := newTestEnv(t, withMaxGroups(1))
	mustEnsure(t, env.runtime, testMeta(41, 1, 1, []isr.NodeID{1, 2}))
	err := env.runtime.EnsureGroup(testMeta(42, 1, 1, []isr.NodeID{1, 2}))
	if !errors.Is(err, multiisr.ErrTooManyGroups) {
		t.Fatalf("expected ErrTooManyGroups, got %v", err)
	}
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/multiisr -run 'TestRuntimeReconcileFlowEnsureApplyRemoveEnsure|TestEnsureGroupReturnsErrTooManyGroups' -v`

Expected: FAIL because reconcile wiring and centralized limits are incomplete.

- [ ] **Step 3: Implement reconcile flow and public handles**

Implementation details:
- `EnsureGroup(meta)` creates a new group only when absent, allocates generation, registers scheduler, and applies the initial meta
- `ApplyMeta(meta)` only targets an existing active group; return `ErrGroupNotFound` otherwise
- `RemoveGroup(groupID)` offlines from scheduling first, then tombstones, then delays final cleanup until TTL expiry
- `GroupHandle.Status()` proxies the underlying replica state safely

- [ ] **Step 4: Implement centralized limits**

Implementation details:
- `MaxGroups` must gate `EnsureGroup`
- `MaxFetchInflightPeer` must be tracked per peer session and push excess work into a peer-local queue
- hard limit breaches must not spin; they must leave work queued for later scheduler passes

- [ ] **Step 5: Re-run the reconcile and limit tests**

Run: `go test ./pkg/multiisr -run 'TestRuntimeReconcileFlowEnsureApplyRemoveEnsure|TestEnsureGroupReturnsErrTooManyGroups' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/multiisr/runtime.go pkg/multiisr/group.go pkg/multiisr/limits.go pkg/multiisr/runtime_test.go pkg/multiisr/limits_test.go
git commit -m "feat: wire multiisr reconcile flow and limits"
```

## Task 6: Add snapshot throttling and finish with package-level verification

**Files:**
- Create: `pkg/multiisr/snapshot.go`
- Create: `pkg/multiisr/snapshot_test.go`
- Modify: `pkg/multiisr/runtime_test.go`
- Modify: `pkg/multiisr/doc.go`

- [ ] **Step 1: Write failing snapshot-throttle tests**

```go
func TestSnapshotTasksRespectMaxSnapshotInflight(t *testing.T) {
	env := newTestEnv(t, withMaxSnapshotInflight(1))
	mustEnsure(t, env.runtime, snapshotMeta(51))
	mustEnsure(t, env.runtime, snapshotMeta(52))

	env.queueSnapshot(51)
	env.queueSnapshot(52)
	env.runScheduler()

	if got := env.snapshots.maxConcurrent(); got != 1 {
		t.Fatalf("expected single inflight snapshot, got %d", got)
	}
}

func TestRecoveryBandwidthLimiterThrottlesSnapshotChunks(t *testing.T) {
	env := newTestEnv(t, withRecoveryBytesPerSecond(128))
	mustEnsure(t, env.runtime, snapshotMeta(53))

	env.queueSnapshotChunk(53, 256)
	env.runScheduler()
	if env.clock.now().Sub(env.startedAt) < time.Second {
		t.Fatalf("expected throttling delay")
	}
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/multiisr -run 'TestSnapshotTasksRespectMaxSnapshotInflight|TestRecoveryBandwidthLimiterThrottlesSnapshotChunks' -v`

Expected: FAIL because snapshot queueing and bandwidth throttling are not implemented.

- [ ] **Step 3: Implement bounded snapshot execution**

Implementation details:
- use a semaphore or token counter for `MaxSnapshotInflight`
- queue excess snapshot work without dropping it
- rate-limit snapshot/recovery bytes with a deterministic testable limiter that uses the injected clock

- [ ] **Step 4: Add final package verification coverage**

Run and make green:
- `go test ./pkg/multiisr -v`
- `go test ./pkg/isr ./pkg/multiisr ./pkg/multiraft -count=1`

Expected:
- `pkg/multiisr` package passes all unit tests
- related `pkg/isr` and `pkg/multiraft` tests remain green, confirming the new library did not require semantic regressions in existing packages

- [ ] **Step 5: Update package docs with V1 scope and explicit deferrals**

Document in `pkg/multiisr/doc.go`:
- V1 guarantees
- generation/tombstone safety rules
- deferred second-phase items: hot-group priority, more aggressive batching, finer-grained resource isolation

- [ ] **Step 6: Commit**

```bash
git add pkg/multiisr/snapshot.go pkg/multiisr/snapshot_test.go pkg/multiisr/runtime_test.go pkg/multiisr/doc.go
git commit -m "feat: add multiisr snapshot throttling"
```

## Final Verification

Before marking the plan complete, follow `@superpowers:verification-before-completion`:

- Run: `go test ./pkg/multiisr -count=1`
- Run: `go test ./pkg/isr ./pkg/multiisr -count=1`
- Run: `go test ./...`

Expected:
- targeted package tests pass before widening scope
- `pkg/isr` compatibility stays green
- full repository tests pass or any unrelated failures are recorded explicitly before handoff

## Deferred Follow-Ups

Do not expand V1 scope during execution. Track these as separate future specs/plans:

- hot-group priority scheduling
- more aggressive multi-message batching heuristics
- finer-grained per-group memory isolation
- deeper large-scale stress and benchmark suites beyond the required correctness tests
