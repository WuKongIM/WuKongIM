# Channel Package Refactor Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild `pkg/channel` into the target `channel/replica/store/runtime/handler/transport` architecture, remove all legacy `isr/log/node` package seams, and land the documented concurrency/performance improvements without preserving compatibility shims.

**Architecture:** The work proceeds in layers: first establish root package semantics and migration-safe tests, then move persistence and replica logic onto the new root types, then rebuild runtime coordination and handler entrypoints, and finally rewire transport and external callers before deleting legacy packages. Every layer is implemented with TDD, with targeted package tests after each task and a final full `pkg/channel` and affected `internal/...` test sweep.

**Tech Stack:** Go, Pebble, existing `wklog`, Go `sync/atomic`, `sync.Pool`, targeted `go test` unit suites.

---

## File Structure And Ownership Map

### New root package files

- Create: `pkg/channel/channel.go`
- Create: `pkg/channel/types.go`
- Create: `pkg/channel/errors.go`
- Create: `pkg/channel/doc.go`
- Create: `pkg/channel/types_test.go`
- Modify later: `pkg/channel/api_test.go`

### New `store` package files

- Create: `pkg/channel/store/engine.go`
- Create: `pkg/channel/store/channel_store.go`
- Create: `pkg/channel/store/logstore.go`
- Create: `pkg/channel/store/checkpoint.go`
- Create: `pkg/channel/store/history.go`
- Create: `pkg/channel/store/idempotency.go`
- Create: `pkg/channel/store/commit.go`
- Create: `pkg/channel/store/snapshot.go`
- Create: `pkg/channel/store/keys.go`
- Create: `pkg/channel/store/codec.go`
- Move/adapt tests from `pkg/channel/log/*store*_test.go`, `pkg/channel/log/commit_coordinator_test.go`, `pkg/channel/log/db_test.go`, `pkg/channel/log/storage_*_test.go`

### New `replica` package files

- Create: `pkg/channel/replica/replica.go`
- Create: `pkg/channel/replica/append.go`
- Create: `pkg/channel/replica/fetch.go`
- Create: `pkg/channel/replica/progress.go`
- Create: `pkg/channel/replica/replication.go`
- Create: `pkg/channel/replica/recovery.go`
- Create: `pkg/channel/replica/meta.go`
- Create: `pkg/channel/replica/history.go`
- Create: `pkg/channel/replica/pool.go`
- Create: `pkg/channel/replica/types.go`
- Move/adapt tests from `pkg/channel/isr/*_test.go`

### New `runtime` package files

- Create: `pkg/channel/runtime/runtime.go`
- Create: `pkg/channel/runtime/channel.go`
- Create: `pkg/channel/runtime/replicator.go`
- Create: `pkg/channel/runtime/scheduler.go`
- Create: `pkg/channel/runtime/backpressure.go`
- Create: `pkg/channel/runtime/session.go`
- Create: `pkg/channel/runtime/tombstone.go`
- Create: `pkg/channel/runtime/snapshot.go`
- Create: `pkg/channel/runtime/types.go`
- Move/adapt tests from `pkg/channel/node/*_test.go`

### New `handler` package files

- Create: `pkg/channel/handler/append.go`
- Create: `pkg/channel/handler/fetch.go`
- Create: `pkg/channel/handler/meta.go`
- Create: `pkg/channel/handler/codec.go`
- Create: `pkg/channel/handler/seq_read.go`
- Create: `pkg/channel/handler/apply.go`
- Create: `pkg/channel/handler/key.go`
- Move/adapt tests from `pkg/channel/log/api_test.go`, `pkg/channel/log/send_test.go`, `pkg/channel/log/fetch_test.go`, `pkg/channel/log/meta_test.go`, `pkg/channel/log/seq_read_test.go`, `pkg/channel/log/apply_test.go`, `pkg/channel/log/deleting_test.go`

### New `transport` package files

- Modify/rename: `pkg/channel/transport/adapter.go` -> `pkg/channel/transport/transport.go`
- Modify: `pkg/channel/transport/session.go`
- Modify: `pkg/channel/transport/codec.go`
- Modify tests: `pkg/channel/transport/adapter_test.go`, `pkg/channel/transport/codec_test.go`, `pkg/channel/transport/integration_test.go`

### External callers that must migrate

- Modify: `internal/app/build.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/lifecycle.go`
- Modify: `internal/app/channelmeta.go`
- Modify: `internal/app/channelmeta_test.go`
- Modify: `internal/runtime/messageid/snowflake.go`
- Modify: `internal/runtime/delivery/types.go`
- Modify: `internal/usecase/message/deps.go`
- Modify: `internal/usecase/message/send.go`
- Modify: `internal/usecase/message/retry.go`
- Modify: `internal/usecase/message/send_test.go`
- Modify: `internal/usecase/delivery/deps.go`
- Modify: `internal/usecase/delivery/submit.go`
- Modify: `internal/usecase/delivery/subscriber.go`
- Modify: `internal/usecase/conversation/deps.go`
- Modify: `internal/usecase/conversation/projector.go`
- Modify: `internal/usecase/conversation/sync.go`
- Modify: `internal/access/gateway/error_map.go`
- Modify: `internal/access/node/client.go`
- Modify: `internal/access/node/options.go`
- Modify: `internal/access/node/conversation_facts_rpc.go`
- Modify: `internal/access/node/delivery_submit_rpc.go`
- Modify: `internal/access/api/error_map.go`
- Modify: `internal/access/api/conversation_legacy_model.go`
- Update references in tests touching old `pkg/channel/log`, `pkg/channel/isr`, `pkg/channel/node`

### Legacy files that must be deleted at the end

- Delete: `pkg/channel/isr/*`
- Delete: `pkg/channel/log/*`
- Delete: `pkg/channel/node/*`
- Delete tests tied only to removed glue: `pkg/channel/log/channelkey_test.go`, `pkg/channel/log/isr_bridge_test.go`

## Task 1: Establish Root Package Semantics

**Files:**
- Create: `pkg/channel/types.go`
- Create: `pkg/channel/errors.go`
- Create: `pkg/channel/doc.go`
- Create: `pkg/channel/types_test.go`
- Modify later: `pkg/channel/api_test.go`

- [ ] **Step 1: Write failing tests for the new root semantics**

Add tests in `pkg/channel/types_test.go` that lock down the root contracts:

```go
func TestMetaCarriesRuntimeAndBusinessFields(t *testing.T) {
	meta := Meta{
		Key: ChannelKey("channel/1/dTE="),
		ID: ChannelID{ID: "u1", Type: 1},
		Leader: 100,
		Status: StatusActive,
		Features: Features{MessageSeqFormat: MessageSeqFormatU64},
	}
	require.Equal(t, ChannelID{ID: "u1", Type: 1}, meta.ID)
	require.Equal(t, StatusActive, meta.Status)
}

func TestAppendRequestCarriesBusinessChannelID(t *testing.T) {
	req := AppendRequest{
		ChannelID: ChannelID{ID: "room-1", Type: 2},
		Message: Message{Payload: []byte("hi")},
	}
	require.Equal(t, "room-1", req.ChannelID.ID)
	require.Equal(t, uint8(2), req.ChannelID.Type)
}
```

- [ ] **Step 2: Run the root tests to verify they fail for the expected reason**

Run: `go test ./pkg/channel -run 'TestMetaCarriesRuntimeAndBusinessFields|TestAppendRequestCarriesBusinessChannelID' -count=1`

Expected: FAIL with undefined root symbols such as `ChannelID`, `Meta`, `AppendRequest`, or `Message`.

- [ ] **Step 3: Implement the root package types and errors**

Create `pkg/channel/types.go` and `pkg/channel/errors.go` with the complete shared domain model:

```go
type NodeID uint64
type ChannelKey string

type ChannelID struct {
	ID   string
	Type uint8
}

type Role uint8
type Status uint8
type MessageSeqFormat uint8

type Features struct {
	MessageSeqFormat MessageSeqFormat
}

type Message struct {
	MessageID   uint64
	MessageSeq  uint64
	ChannelID   string
	ChannelType uint8
	FromUID     string
	Payload     []byte
}

type Meta struct {
	Key         ChannelKey
	ID          ChannelID
	Epoch       uint64
	LeaderEpoch uint64
	Leader      NodeID
	Replicas    []NodeID
	ISR         []NodeID
	MinISR      int
	LeaseUntil  time.Time
	Status      Status
	Features    Features
}

type AppendRequest struct {
	ChannelID            ChannelID
	Message              Message
	ExpectedChannelEpoch uint64
	ExpectedLeaderEpoch  uint64
}

type AppendResult struct {
	MessageID  uint64
	MessageSeq uint64
	Message    Message
}

type FetchRequest struct {
	ChannelID            ChannelID
	FromSeq              uint64
	Limit                int
	MaxBytes             int
	ExpectedChannelEpoch uint64
	ExpectedLeaderEpoch  uint64
}

type FetchResult struct {
	Messages     []Message
	NextSeq      uint64
	CommittedSeq uint64
}
```

Define all shared errors in `pkg/channel/errors.go` with the `channel:` prefix. Keep key derivation out of the root package; it belongs in `pkg/channel/handler/key.go` later in the plan.

- [ ] **Step 4: Run the root tests and the package compile check**

Run: `go test ./pkg/channel -count=1`

Expected: PASS for the new root tests. Some broader package imports may still fail until later tasks; that is acceptable at this stage.

- [ ] **Step 5: Commit the root semantic foundation**

Run:

```bash
git add pkg/channel/types.go pkg/channel/errors.go pkg/channel/doc.go pkg/channel/types_test.go
git commit -m "refactor: define unified channel root types"
```

## Task 2: Extract And Normalize The Store Layer

**Files:**
- Create: `pkg/channel/store/engine.go`
- Create: `pkg/channel/store/channel_store.go`
- Create: `pkg/channel/store/logstore.go`
- Create: `pkg/channel/store/checkpoint.go`
- Create: `pkg/channel/store/history.go`
- Create: `pkg/channel/store/idempotency.go`
- Create: `pkg/channel/store/commit.go`
- Create: `pkg/channel/store/snapshot.go`
- Create: `pkg/channel/store/keys.go`
- Create: `pkg/channel/store/codec.go`
- Create/modify tests under `pkg/channel/store/*_test.go`
- Source material: `pkg/channel/log/db.go`, `pkg/channel/log/log_store.go`, `pkg/channel/log/checkpoint_store.go`, `pkg/channel/log/history_store.go`, `pkg/channel/log/state_store.go`, `pkg/channel/log/snapshot_store.go`, `pkg/channel/log/commit_coordinator.go`, `pkg/channel/log/commit_batch.go`, `pkg/channel/log/store_keys.go`, `pkg/channel/log/store_codec.go`, `pkg/channel/log/store.go`

- [ ] **Step 1: Write failing store tests around the direct `ChannelStore` contract**

Create `pkg/channel/store/channel_store_test.go` with tests that prove one concrete store object satisfies the needed read/write behavior:

```go
func TestChannelStoreAppendsAndReadsRecords(t *testing.T) {
	st := newTestChannelStore(t)
	base, err := st.Append([]channel.Record{{Payload: []byte("a"), SizeBytes: 1}})
	require.NoError(t, err)
	require.Equal(t, uint64(0), base)

	records, err := st.Read(0, 1024)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, []byte("a"), records[0].Payload)
}

func TestChannelStorePersistsCheckpointAndHistory(t *testing.T) {
	st := newTestChannelStore(t)
	require.NoError(t, st.StoreCheckpoint(channel.Checkpoint{Epoch: 2, HW: 8}))
	cp, err := st.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, uint64(8), cp.HW)
}
```

- [ ] **Step 2: Run the new store tests to watch them fail**

Run: `go test ./pkg/channel/store -run 'TestChannelStoreAppendsAndReadsRecords|TestChannelStorePersistsCheckpointAndHistory' -count=1`

Expected: FAIL because `pkg/channel/store` and `ChannelStore` do not exist yet.

- [ ] **Step 3: Move the storage implementation and unify it on root types**

Port storage code into the new package and normalize names:

```go
type ChannelStore struct {
	engine *Engine
	key    channel.ChannelKey
	id     channel.ChannelID

	writeMu sync.Mutex
	leo     atomic.Uint64
}

func (s *ChannelStore) Append(records []channel.Record) (uint64, error) { ... }
func (s *ChannelStore) Read(from uint64, maxBytes int) ([]channel.Record, error) { ... }
func (s *ChannelStore) LoadCheckpoint() (channel.Checkpoint, error) { ... }
func (s *ChannelStore) StoreCheckpoint(cp channel.Checkpoint) error { ... }
func (s *ChannelStore) LoadHistory() ([]channel.EpochPoint, error) { ... }
func (s *ChannelStore) AppendHistory(point channel.EpochPoint) error { ... }
```

Key requirements for this task:

- Convert all old `isr.*` and `log.*` domain types to root `channel.*` types
- Introduce `channel_store.go` as the single place where per-channel persistence capability is composed
- Keep commit coordination and idempotency storage internal to `store`
- Do not add bridge adapters for `replica`; the same concrete `ChannelStore` must later satisfy replica interfaces directly

- [ ] **Step 4: Run all store tests and migrate existing storage suites**

Run: `go test ./pkg/channel/store -count=1`

Expected: PASS for migrated store tests including log, checkpoint, history, commit, snapshot, and idempotency coverage.

- [ ] **Step 5: Commit the store extraction**

Run:

```bash
git add pkg/channel/store
git commit -m "refactor: extract channel store layer"
```

## Task 3: Rebuild The Replica Package On Root Types And Performance Primitives

**Files:**
- Create: `pkg/channel/replica/replica.go`
- Create: `pkg/channel/replica/append.go`
- Create: `pkg/channel/replica/fetch.go`
- Create: `pkg/channel/replica/progress.go`
- Create: `pkg/channel/replica/replication.go`
- Create: `pkg/channel/replica/recovery.go`
- Create: `pkg/channel/replica/meta.go`
- Create: `pkg/channel/replica/history.go`
- Create: `pkg/channel/replica/pool.go`
- Create: `pkg/channel/replica/types.go`
- Create/modify tests under `pkg/channel/replica/*_test.go`
- Source material: `pkg/channel/isr/*`

- [ ] **Step 1: Write failing tests for collector persistence and atomic status snapshots**

Add tests in `pkg/channel/replica/append_test.go` and `pkg/channel/replica/progress_test.go`:

```go
func TestAppendCollectorDrainsBurstsWithoutRetrigger(t *testing.T) {
	r := newLeaderReplica(t)
	ctx := context.Background()

	_, err := r.Append(ctx, []channel.Record{{Payload: []byte("a"), SizeBytes: 1}})
	require.NoError(t, err)

	_, err = r.Append(ctx, []channel.Record{{Payload: []byte("b"), SizeBytes: 1}})
	require.NoError(t, err)

	state := r.Status()
	require.GreaterOrEqual(t, state.LEO, uint64(2))
}

func TestStatusReturnsLatestReplicaSnapshot(t *testing.T) {
	r := newLeaderReplica(t)
	_, err := r.Append(context.Background(), []channel.Record{{Payload: []byte("a"), SizeBytes: 1}})
	require.NoError(t, err)
	require.Equal(t, uint64(1), r.Status().LEO)
}
```

- [ ] **Step 2: Run the replica tests to confirm they fail**

Run: `go test ./pkg/channel/replica -run 'TestAppendCollectorDrainsBurstsWithoutRetrigger|TestStatusReturnsLatestReplicaSnapshot' -count=1`

Expected: FAIL because the new package and tests are not wired yet.

- [ ] **Step 3: Port the replica state machine and replace duplicated domain types**

Implement `pkg/channel/replica` by migrating `pkg/channel/isr` and converting all public domain types to root types:

```go
type Replica interface {
	ApplyMeta(meta channel.Meta) error
	BecomeLeader(meta channel.Meta) error
	BecomeFollower(meta channel.Meta) error
	Tombstone() error
	Append(ctx context.Context, batch []channel.Record) (channel.CommitResult, error)
	Status() channel.ReplicaState
}
```

Keep `types.go` limited to replica-local configs and storage interfaces.

- [ ] **Step 4: Add the performance primitives during the migration**

Implement the documented hot-path changes inside the new package:

- `atomic.Pointer[channel.ReplicaState]` for `Status()`
- `sync.Pool` for append requests, waiters, and merged record buffers
- a persistent append collector goroutine started in replica construction
- low-allocation HW advancement using fixed-size buffers for small ISR sets

The collector logic should look structurally like:

```go
func (r *replica) startAppendCollector() {
	go func() {
		for {
			select {
			case <-r.appendSignal:
				for {
					batch := r.collectAppendBatch()
					if len(batch) == 0 {
						break
					}
					r.flushAppendBatch(batch)
				}
			case <-r.stopCh:
				return
			}
		}
	}()
}
```

- [ ] **Step 5: Run all replica tests**

Run: `go test ./pkg/channel/replica -count=1`

Expected: PASS for migrated API, append, fetch, replication, recovery, progress, and snapshot tests.

- [ ] **Step 6: Commit the replica rebuild**

Run:

```bash
git add pkg/channel/replica
git commit -m "refactor: migrate channel replica package"
```

## Task 4: Rebuild Runtime Registry, Sharding, And Tombstones

**Files:**
- Create: `pkg/channel/runtime/runtime.go`
- Create: `pkg/channel/runtime/channel.go`
- Create: `pkg/channel/runtime/tombstone.go`
- Create: `pkg/channel/runtime/types.go`
- Create/modify tests: `pkg/channel/runtime/runtime_test.go`, `pkg/channel/runtime/tombstone_test.go`, `pkg/channel/runtime/registry_test.go`
- Source material: `pkg/channel/node/runtime.go`, `pkg/channel/node/channel.go`, `pkg/channel/node/generation.go`, `pkg/channel/node/registry.go`, `pkg/channel/node/types.go`

- [ ] **Step 1: Write failing tests for shard-based lookups and tombstone cleanup**

Add tests:

```go
func TestChannelLookupUsesReadOnlyPath(t *testing.T) {
	rt := newTestRuntime(t)
	meta := testMeta("room-1")
	require.NoError(t, rt.EnsureChannel(meta))

	ch, ok := rt.Channel(meta.Key)
	require.True(t, ok)
	require.Equal(t, meta.Key, ch.ID())
}

func TestExpiredTombstonesDoNotBlockChannelLookup(t *testing.T) {
	rt := newTestRuntime(t)
	rt.tombstones.add(testMeta("room-2").Key, 1, time.Now().Add(-time.Second))
	rt.tombstones.dropExpired(time.Now())
	_, ok := rt.Channel(testMeta("room-2").Key)
	require.False(t, ok)
}
```

- [ ] **Step 2: Run runtime lookup tests to verify red state**

Run: `go test ./pkg/channel/runtime -run 'TestChannelLookupUsesReadOnlyPath|TestExpiredTombstonesDoNotBlockChannelLookup' -count=1`

Expected: FAIL because the new runtime package does not exist yet.

- [ ] **Step 3: Migrate runtime shell and rename `group` to `channel`**

Implement the new runtime shell:

```go
type Runtime interface {
	EnsureChannel(meta channel.Meta) error
	RemoveChannel(key channel.ChannelKey) error
	ApplyMeta(meta channel.Meta) error
	Channel(key channel.ChannelKey) (ChannelHandle, bool)
}

type channel struct {
	key     channel.ChannelKey
	gen     uint64
	replica replica.Replica
	meta    atomic.Pointer[channel.Meta]
}
```

Use a root-package import alias such as `core` inside `pkg/channel/runtime` to avoid name collisions with `type channel struct`.

- [ ] **Step 4: Replace the global registry lock with shards and async tombstones**

Implement:

- a fixed shard array such as `[64]shard`
- `shardFor(key)` hashing
- `Channel()` read-only lookup on the shard
- `tombstoneManager` with a cleanup goroutine or test-driven manual `dropExpired` entrypoint

Do not carry over `dropExpiredTombstonesLocked()` inside `Channel()`.

- [ ] **Step 5: Run the runtime core tests**

Run: `go test ./pkg/channel/runtime -run 'TestChannelLookupUsesReadOnlyPath|TestExpiredTombstonesDoNotBlockChannelLookup|TestEnsureChannel|TestRemoveChannel' -count=1`

Expected: PASS for registry and tombstone behavior; other runtime suites may still fail until scheduling and replication tasks land.

- [ ] **Step 6: Commit the runtime shell migration**

Run:

```bash
git add pkg/channel/runtime/runtime.go pkg/channel/runtime/channel.go pkg/channel/runtime/tombstone.go pkg/channel/runtime/types.go pkg/channel/runtime/*_test.go
git commit -m "refactor: migrate channel runtime core"
```

## Task 5: Split Runtime Scheduling, Replication, Backpressure, And Snapshot Flow

**Files:**
- Create: `pkg/channel/runtime/replicator.go`
- Create: `pkg/channel/runtime/scheduler.go`
- Create: `pkg/channel/runtime/backpressure.go`
- Create: `pkg/channel/runtime/session.go`
- Create: `pkg/channel/runtime/snapshot.go`
- Modify tests: `pkg/channel/runtime/scheduler_test.go`, `pkg/channel/runtime/session_test.go`, `pkg/channel/runtime/snapshot_test.go`, `pkg/channel/runtime/benchmark_test.go`, `pkg/channel/runtime/stress_test.go`
- Source material: `pkg/channel/node/scheduler.go`, `pkg/channel/node/session.go`, `pkg/channel/node/snapshot.go`, `pkg/channel/node/fetch_service.go`, `pkg/channel/node/batching.go`, `pkg/channel/node/limits.go`, `pkg/channel/node/transport.go`

- [ ] **Step 1: Write failing tests for priority scheduling and same-key single execution**

Add tests in `pkg/channel/runtime/scheduler_test.go`:

```go
func TestSchedulerPrefersHighPriorityWork(t *testing.T) {
	s := newScheduler()
	s.enqueue("channel/1/YQ==", PriorityLow)
	s.enqueue("channel/1/Yg==", PriorityHigh)

	first, ok := s.popReady()
	require.True(t, ok)
	require.Equal(t, PriorityHigh, first.priority)
}

func TestSchedulerDoesNotRunSameKeyConcurrently(t *testing.T) {
	s := newScheduler()
	key := channel.ChannelKey("channel/1/YQ==")
	s.begin(key)
	s.enqueue(key, PriorityNormal)
	require.True(t, s.isDirty(key))
}
```

- [ ] **Step 2: Run scheduler tests to verify red state**

Run: `go test ./pkg/channel/runtime -run 'TestSchedulerPrefersHighPriorityWork|TestSchedulerDoesNotRunSameKeyConcurrently' -count=1`

Expected: FAIL because priority-aware scheduling is not implemented yet.

- [ ] **Step 3: Implement the runtime subcomponents and explicit delegate flow**

Rebuild the runtime internals with focused files:

- `scheduler.go`: multi-priority queues with `high`, `normal`, `low`
- `replicator.go`: follower replication, retries, peer validation, request draining
- `backpressure.go`: inflight accounting and peer/channel queue limits
- `session.go`: peer session cache
- `snapshot.go`: snapshot throttling and bytes accounting

Replace callback closures with an explicit delegate:

```go
type ChannelDelegate interface {
	OnReplication(key channel.ChannelKey)
	OnSnapshot(key channel.ChannelKey)
}
```

- [ ] **Step 4: Run the focused runtime suites**

Run:

```bash
go test ./pkg/channel/runtime -run 'TestScheduler|TestSession|TestSnapshot|TestLimits' -count=1
```

Expected: PASS for runtime scheduling, session, snapshot, and limit behavior.

- [ ] **Step 5: Commit the runtime coordination split**

Run:

```bash
git add pkg/channel/runtime
git commit -m "refactor: split channel runtime coordination"
```

## Task 6: Build The Handler Layer And Business Key Mapping

**Files:**
- Create: `pkg/channel/handler/append.go`
- Create: `pkg/channel/handler/fetch.go`
- Create: `pkg/channel/handler/meta.go`
- Create: `pkg/channel/handler/codec.go`
- Create: `pkg/channel/handler/seq_read.go`
- Create: `pkg/channel/handler/apply.go`
- Create: `pkg/channel/handler/key.go`
- Create/modify tests under `pkg/channel/handler/*_test.go`
- Source material: `pkg/channel/log/send.go`, `pkg/channel/log/fetch.go`, `pkg/channel/log/meta.go`, `pkg/channel/log/codec.go`, `pkg/channel/log/seq_read.go`, `pkg/channel/log/apply.go`, `pkg/channel/log/cluster.go`, `pkg/channel/log/types.go`

- [ ] **Step 1: Write failing handler tests for `ChannelID` -> `ChannelKey` flow and append/fetch behavior**

Create tests such as:

```go
func TestKeyFromChannelID(t *testing.T) {
	key := KeyFromChannelID(channel.ChannelID{ID: "u1", Type: 1})
	require.Equal(t, channel.ChannelKey("channel/1/dTE="), key)
}

func TestAppendUsesRuntimeKeyAndReturnsMessageSeq(t *testing.T) {
	h := newTestHandler(t)
	res, err := h.Append(context.Background(), channel.AppendRequest{
		ChannelID: channel.ChannelID{ID: "u1", Type: 1},
		Message: channel.Message{Payload: []byte("hi")},
	})
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.MessageSeq)
}
```

- [ ] **Step 2: Run handler tests to establish failure**

Run: `go test ./pkg/channel/handler -run 'TestKeyFromChannelID|TestAppendUsesRuntimeKeyAndReturnsMessageSeq' -count=1`

Expected: FAIL because `pkg/channel/handler` is not implemented yet.

- [ ] **Step 3: Move business entrypoint logic from `log` into the new handler package**

Implement:

- key derivation in `key.go`
- metadata cache keyed by `channel.ChannelKey`
- append and fetch orchestration against `runtime.Runtime` and `store.ChannelStore`
- message and fetch codecs based on root package message types

The handler-facing contracts should resemble:

```go
type Service interface {
	ApplyMeta(meta channel.Meta) error
	Append(ctx context.Context, req channel.AppendRequest) (channel.AppendResult, error)
	Fetch(ctx context.Context, req channel.FetchRequest) (channel.FetchResult, error)
	Status(id channel.ChannelID) (channel.ChannelRuntimeStatus, error)
}
```

Do not preserve the old `log.ChannelKey` struct. All business entrypoints must accept `channel.ChannelID` and derive the runtime key internally.

- [ ] **Step 4: Run all handler tests**

Run: `go test ./pkg/channel/handler -count=1`

Expected: PASS for append, fetch, codec, seq-read, apply, delete, and API tests migrated from the old `log` package.

- [ ] **Step 5: Commit the handler layer**

Run:

```bash
git add pkg/channel/handler
git commit -m "refactor: create channel handler layer"
```

## Task 7: Rewire Transport And Root Assembly

**Files:**
- Create: `pkg/channel/channel.go`
- Create/modify: `pkg/channel/api_test.go`
- Rename/modify: `pkg/channel/transport/adapter.go` -> `pkg/channel/transport/transport.go`
- Modify: `pkg/channel/transport/codec.go`
- Modify: `pkg/channel/transport/session.go`
- Modify tests: `pkg/channel/transport/adapter_test.go`, `pkg/channel/transport/codec_test.go`, `pkg/channel/transport/integration_test.go`

- [ ] **Step 1: Write failing integration tests for root construction and transport envelopes**

Add tests:

```go
func TestNewBuildsClusterWithRuntimeHandlerAndTransport(t *testing.T) {
	cfg := Config{
		Runtime: testRuntime(t),
		StoreFactory: testStoreFactory(t),
		MessageIDs: fixedMessageIDGenerator{next: 1},
		Now: func() time.Time { return time.Unix(1700000000, 0) },
		Logger: wklog.NewNop(),
	}
	_, err := New(cfg)
	require.NoError(t, err)
}

func TestTransportCodecUsesRootTypes(t *testing.T) {
	env := runtime.Envelope{
		ChannelKey: channel.ChannelKey("channel/1/dTE="),
	}
	data, err := transport.EncodeEnvelope(env)
	require.NoError(t, err)
	decoded, err := transport.DecodeEnvelope(data)
	require.NoError(t, err)
	require.Equal(t, env.ChannelKey, decoded.ChannelKey)
}
```

- [ ] **Step 2: Run the root and transport tests to verify they fail**

Run:

```bash
go test ./pkg/channel ./pkg/channel/transport -run 'TestNewBuildsClusterWithRuntimeHandlerAndTransport|TestTransportCodecUsesRootTypes' -count=1
```

Expected: FAIL because root assembly and transport type wiring are incomplete.

- [ ] **Step 3: Implement `channel.New()` and migrate transport imports**

Wire the final composition:

- root `Config` owns the dependencies needed to build `store`, `replica`, `runtime`, `handler`, and `transport`
- `channel.New()` returns the top-level business `Cluster`
- `transport` imports switch from `isr`/`node` to root `channel` and `runtime`

Keep the root package thin. All substantive logic belongs to subpackages.

- [ ] **Step 4: Run root and transport suites**

Run: `go test ./pkg/channel ./pkg/channel/transport -count=1`

Expected: PASS for root package tests and transport codec/adapter integration tests.

- [ ] **Step 5: Commit the final internal wiring**

Run:

```bash
git add pkg/channel/channel.go pkg/channel/api_test.go pkg/channel/transport
git commit -m "refactor: wire channel root assembly"
```

## Task 8: Migrate External Callers, Delete Legacy Packages, And Run Regression Tests

**Files:**
- Modify all external callers listed in the ownership map
- Delete: `pkg/channel/isr/*`
- Delete: `pkg/channel/log/*`
- Delete: `pkg/channel/node/*`
- Update docs referencing old package layout:
  - `docs/wiki/architecture/README.md`
  - `docs/raw/logging-architecture.md`

- [ ] **Step 1: Write or update one failing cross-layer test that exercises the new imports**

Prefer an existing app-level test such as `internal/app/build_test.go` or `internal/app/channelmeta_test.go`. Update it to import the new packages and assert the new types:

```go
func TestBuildWiresChannelSubsystem(t *testing.T) {
	app, err := Build(testConfig(t))
	require.NoError(t, err)
	require.NotNil(t, app.ChannelCluster())
}
```

- [ ] **Step 2: Run the affected external test to verify red state**

Run: `go test ./internal/app -run 'TestBuildWiresChannelSubsystem|TestApplyChannelMeta' -count=1`

Expected: FAIL on old imports or mismatched type names until callers are migrated.

- [ ] **Step 3: Migrate all external imports and type usage**

Apply the package changes across `internal/...`:

- replace `pkg/channel/log` business imports with `pkg/channel` and `pkg/channel/handler`-backed root APIs
- replace `pkg/channel/isr` imports with root `pkg/channel` or `pkg/channel/replica` only where runtime/internal replica interfaces are truly needed
- replace `pkg/channel/node` imports with `pkg/channel/runtime`

Use `rg -l 'pkg/channel/(isr|log|node)' internal pkg/channel/transport pkg/channel` as the source of truth while editing.

- [ ] **Step 4: Delete legacy packages once all call sites compile**

Run:

```bash
rm -rf pkg/channel/isr pkg/channel/log pkg/channel/node
```

Immediately follow with:

```bash
go test ./pkg/channel/... -count=1
```

Expected: PASS, proving no lingering compile-time dependency on removed packages.

- [ ] **Step 5: Run the full targeted regression suite**

Run:

```bash
go test ./pkg/channel/... -count=1
go test ./internal/... ./pkg/... -count=1
```

Expected: PASS. If the second command is too slow for every iteration, use it only after the first command is green and the last external imports have been migrated.

- [ ] **Step 6: Commit the package migration cleanup**

Run:

```bash
git add pkg/channel internal docs/wiki/architecture/README.md docs/raw/logging-architecture.md
git commit -m "refactor: replace legacy channel package layout"
```

## Final Verification Checklist

- [ ] `pkg/channel/types.go` is the only place defining shared domain types
- [ ] No file imports `pkg/channel/isr`, `pkg/channel/log`, or `pkg/channel/node`
- [ ] `pkg/channel/log/isr_bridge.go` and `pkg/channel/log/channelkey.go` are gone
- [ ] `store.ChannelStore` directly satisfies replica persistence needs
- [ ] `runtime.Channel()` no longer performs tombstone cleanup
- [ ] Append collector is persistent and covered by tests
- [ ] Scheduler distinguishes high/normal/low priority work
- [ ] `go test ./pkg/channel/... -count=1` passes
- [ ] `go test ./internal/... ./pkg/... -count=1` passes

## Review Notes For The Implementer

- Keep package boundaries strict: `handler -> runtime/store`, `runtime -> replica/store/transport`, `replica -> store interfaces`, root package only composes.
- When `runtime` needs a root import alias, use `core` consistently to avoid `type channel struct` collisions.
- Copy slice fields before publishing `Meta` or `ReplicaState` snapshots through `atomic.Pointer`; do not share mutable backing arrays across goroutines.
- Do not carry bridge code “temporarily.” If a task seems to require an adapter, stop and collapse the interface instead.
- Favor one commit per task. If a task grows too large, split it before implementation rather than batching multiple architectural moves into one review unit.
