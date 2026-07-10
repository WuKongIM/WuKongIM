# Channel Entry Lease Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reclaim all channel-indexed message runtime state after the final user closes while preserving one canonical append, LEO, and checkpoint synchronization domain for every still-referenced channel.

**Architecture:** Replace the three process-lifetime maps with a reference-counted `MessageDB` registry. Every typed/compatibility acquisition returns a distinct idempotent lease over one canonical entry; worker reads own temporary leases, Reactor state owns a transferred lifetime lease, and admitted commit work owns a background pin until a terminal coordinator finalizer runs. The final release compare-deletes the canonical entry without closing the shared Pebble engine.

**Tech Stack:** Go 1.25.11, Pebble-backed WuKongIM message DB, channel Reactor/worker runtime, atomics/mutexes/condition variables, Prometheus storage metrics, Go race detector and benchmarks.

## Global Constraints

- Do not preserve the old `MessageDB.Channel` or `Engine.ForChannel` signatures; the project is pre-launch and acquisitions must return errors explicitly.
- Preserve durable keys, message sequence semantics, retention, checkpoint fields, and commit quorum behavior.
- Exactly one live canonical `appendMu`, LEO cache, and checkpoint mutex may exist per channel while any lease or commit pin remains.
- The registry retains no zero-reference entry and uses pointer compare-delete under its mutex.
- Closing a channel lease never closes the shared Pebble engine.
- Caller context cancellation must not release an admitted coordinator pin while build/commit/publish can still run.
- Temporary acquisitions close on success, error, cancellation, and early return; successful StoreLoad transfers ownership to Reactor state.
- Eviction stays asynchronous and must not block the Reactor loop on Pebble/channel close.
- Metrics must not label by channel key or ID.
- Use `GOWORK=off` and explicit package roots; never run repository-root `./...`.
- Read package `FLOW.md` files before changing those packages and update all ownership text made inaccurate.

---

## File Map

| Path | Responsibility |
| --- | --- |
| `pkg/db/internal/commit/{coordinator,batch}.go` | Run request finalizers exactly once at true terminal completion. |
| `pkg/db/message/channel_registry.go` | Own canonical channel entries, lease counts, pins, shutdown, and snapshots. |
| `pkg/db/message/{db,channel_log}.go` | Return lease handles and keep mutable log state on canonical entries. |
| `pkg/db/message/compat.go` | Return distinct compatibility leases, pin background commits, and deduplicate canonical locks. |
| `pkg/channel/store/channel_adapter.go` | Release leases and perform checkpoint RMW through the canonical entry. |
| `pkg/channel/worker/task.go` | Close every temporary worker store and transfer only StoreLoad success. |
| `pkg/channel/reactor/*` | Release loaded/loading/pending stores on eviction, late completion, and shutdown. |
| `pkg/cluster/node.go`, `pkg/cluster/channels/service.go`, `pkg/db/transfer/importer.go` | Close temporary production acquisitions. |
| `pkg/metrics/storage.go`, `pkg/cluster/storage_metrics.go` | Export entry/lease/pin pressure without channel labels. |

### Task 1: Add terminal commit-request finalization

**Files:**
- Modify: `pkg/db/internal/commit/coordinator.go`
- Modify: `pkg/db/internal/commit/batch.go`
- Modify: `pkg/db/internal/commit/coordinator_test.go`

**Interface:**

```go
type Request struct {
	Lane      Lane
	Partition string
	Records   int
	Bytes     int
	Build     func(*engine.Batch) error
	Publish   func() error
	Finalize  func()
}

type pendingRequest struct {
	Request
	done         chan error
	finalizeOnce *sync.Once
}

func newPendingRequest(req Request) pendingRequest
func (r pendingRequest) complete(err error)
```

`complete` invokes `Finalize` exactly once and then sends to the buffered done channel. Caller cancellation after admission only stops waiting; it does not call `complete`.

- [ ] **Step 1: Add failing terminal-path tests**

Add:

```text
TestCoordinatorFinalizesRejectedRequestOnce
TestCoordinatorDoesNotFinalizeAdmittedRequestWhenCallerCancels
TestCoordinatorFinalizesAfterBuildError
TestCoordinatorFinalizesAfterCommitError
TestCoordinatorFinalizesAfterPublishError
TestCoordinatorFinalizesQueuedRequestOnClose
```

For cancellation, block `Build`, submit with a cancellable context, cancel after admission, assert `Submit` returns `context.Canceled` while finalize count is still zero, then unblock and assert it becomes exactly one.

```bash
GOWORK=off go test ./pkg/db/internal/commit \
  -run 'TestCoordinator(Finalizes|DoesNotFinalize)' -count=1
```

Expected: compile failures because `Finalize` does not exist.

- [ ] **Step 2: Centralize all terminal completions**

Create pending requests with a shared `sync.Once`. Replace every `req.done <- err` in batch build, commit, publish, queued-close, deferred-close, and admission rejection paths with `req.complete(err)`. A request rejected before admission also finalizes once. A context canceled after successful enqueue does not.

- [ ] **Step 3: Verify coordinator behavior and race safety**

```bash
GOWORK=off go test ./pkg/db/internal/commit -count=1
GOWORK=off go test -race ./pkg/db/internal/commit -run 'TestCoordinator(Finalizes|DoesNotFinalize)' -count=1
```

- [ ] **Step 4: Commit the prerequisite**

```bash
git add pkg/db/internal/commit/coordinator.go pkg/db/internal/commit/batch.go pkg/db/internal/commit/coordinator_test.go
git commit -m "feat(db): add terminal commit request finalization"
```

### Task 2: Introduce the canonical channel registry and typed leases

**Files:**
- Create: `pkg/db/message/channel_registry.go`
- Create: `pkg/db/message/channel_registry_test.go`
- Modify: `pkg/db/message/db.go`
- Modify: `pkg/db/message/channel_log.go`
- Modify: `pkg/db/message/append.go`
- Modify: `pkg/db/message/apply_fetch.go`
- Modify: `pkg/db/message/checkpoint.go`
- Modify: `pkg/db/message/history.go`
- Modify: `pkg/db/message/idempotency.go`
- Modify: `pkg/db/message/indexes.go`
- Modify: `pkg/db/message/read.go`
- Modify: `pkg/db/message/retention.go`
- Modify: `pkg/db/message/snapshot.go`
- Modify: `pkg/db/message/truncate.go`

**Public acquisition interface:**

```go
func (db *MessageDB) Channel(key ChannelKey, id ChannelID) (*ChannelLog, error)
func (l *ChannelLog) Close() error
```

**Core structures:**

```go
type channelRegistry struct {
	mu      sync.Mutex
	cond    *sync.Cond
	closed  bool
	entries map[ChannelKey]*channelEntry

	outstandingLeases uint64
	backgroundPins    uint64
	acquireTotal      uint64
	releaseTotal      uint64
	reclaimTotal      uint64
}

type channelEntry struct {
	db   *MessageDB
	key  ChannelKey
	id   ChannelID
	appendKeyCache appendKeyCache
	appendMu       sync.Mutex
	checkpointMu   sync.Mutex
	leo            atomic.Uint64
	loaded         atomic.Bool
	refs           uint64 // guarded by registry.mu
}

type ChannelLog struct {
	*channelEntry
	registry  *channelRegistry
	closeOnce sync.Once
	closed    atomic.Bool
}
```

- [ ] **Step 1: Add failing registry ownership tests**

Add:

```text
TestChannelRegistryFirstCloseKeepsCanonicalEntry
TestChannelRegistryLastCloseReclaimsEntry
TestChannelRegistryCloseIsIdempotent
TestChannelLeaseRejectsUseAfterClose
TestChannelRegistryRejectsMismatchedIdentity
TestChannelRegistryOlderReleaseCannotDeleteReacquiredEntry
TestChannelRegistryBackgroundPinDefersReclaim
TestChannelRegistryRejectsAcquireAfterBeginClose
```

Tests stay in package `message` and inspect canonical pointers/registry length directly; they must not introduce a test-only production API.

```bash
GOWORK=off go test ./pkg/db/message -run 'TestChannel(Registry|Lease)' -count=1
```

Expected: compile failure because the registry/lease API does not exist.

- [ ] **Step 2: Implement acquire, release, pin, and close state**

`acquire` checks closed, validates a same-key ID match, increments `refs`, and returns a distinct wrapper. Last release decrements counts and compare-deletes only when `entries[key] == entry`. Background retain/release uses the same `refs`, separately tracks pins, and signals `cond` when pins reach zero. Return `dberrors.ErrClosed` after begin-close and `dberrors.ErrConflict` for a key/ID mismatch.

- [ ] **Step 3: Move all canonical mutable fields behind `channelEntry`**

Replace `MessageDB.logs` with `registry`. Embed `*channelEntry` in `ChannelLog` so existing field access remains local, but make every exported log operation start with `validateLease`; closed/nil handles return `dberrors.ErrClosed`. The first acquisition builds immutable append-key cache; a post-reclaim acquisition reconstructs LEO from durable rows.

- [ ] **Step 4: Prove durable reacquire correctness**

Add `TestChannelRegistryReacquireRestoresDurableLEOAndMessages`: acquire, append records, close to zero entries, reacquire, assert LEO and messages, append once more, and assert the next sequence is not reused.

```bash
GOWORK=off go test ./pkg/db/message \
  -run 'TestChannel(Registry|Lease|RegistryReacquire)' -count=1
```

- [ ] **Step 5: Commit the typed lease core**

```bash
git add pkg/db/message
git commit -m "feat(message): add canonical channel entry leases"
```

### Task 3: Lease compatibility stores and pin grouped commits

**Files:**
- Modify: `pkg/db/message/compat.go`
- Modify: `pkg/db/message/compat_test.go`
- Modify: `pkg/db/message/message_benchmark_test.go`

**Interfaces:**

```go
func (e *Engine) ForChannel(key channel.ChannelKey, id channel.ChannelID) (*ChannelStore, error)
func (s *ChannelStore) Close() error
func (s *ChannelStore) StoreCheckpointHWMonotonic(ctx context.Context, hw uint64) error
```

- [ ] **Step 1: Add failing compatibility lease tests**

Add:

```text
TestEngineForChannelReturnsDistinctLeasesSharingCanonicalEntry
TestEngineLastStoreCloseReclaimsEntry
TestEngineReacquireRestoresDurableLEO
TestEngineReadReleasesTemporaryLease
TestChannelStoreRejectsUseAfterClose
```

Expected API call style is `store, err := engine.ForChannel(...)`, followed by `defer store.Close()`.

- [ ] **Step 2: Remove `Engine.stores` and return one lease per call**

`ForChannel` snapshots `e.db` under `e.mu`, calls `db.Channel`, and returns a new `ChannelStore`. Remove the process cache and its initialization. `Engine.Read`/`ReadReverse` acquire with the catalog ID when available or use the message inspector's non-mutating path, and always release before return. `ChannelStore.validate` checks both engine and lease state.

- [ ] **Step 3: Add failing background-pin tests**

Add:

```text
TestCommitCoordinatorCancellationKeepsEntryPinnedUntilPublish
TestCommitCoordinatorBuildErrorReleasesChannelPins
TestCommitCoordinatorCommitErrorReleasesChannelPins
TestCommitCoordinatorCloseReleasesChannelPins
```

The cancellation test closes the caller's lease after `Submit` returns canceled and asserts the entry remains until the blocked request publishes/finalizes.

- [ ] **Step 4: Retain canonical pins for every captured commit request**

Before submitting a request whose Build or Publish captures a channel entry, retain one pin per distinct canonical entry. Set `commit.Request.Finalize` to release those pins. If preparation fails before Submit, release immediately; once Submit is called, its terminal finalizer owns release. Keep pins across caller cancellation.

- [ ] **Step 5: Deduplicate batch locks by canonical entry**

Change append/apply batch locking from `map[*ChannelStore]` to `map[*channelEntry]*ChannelStore`, sort by canonical key, and acquire each append mutex once even if the batch contains multiple lease wrappers for one channel. Add:

```text
TestStoreAppendBatchDeduplicatesCanonicalEntry
TestStoreApplyFetchBatchDeduplicatesCanonicalEntry
```

- [ ] **Step 6: Implement monotonic checkpoint update on the canonical entry**

Under `entry.checkpointMu`, load the current checkpoint, ignore `hw <= current.HW`, update only HW, preserve epoch/log-start fields, and store. Add concurrent high/low update coverage proving HW never regresses.

- [ ] **Step 7: Make Engine shutdown reject, drain, detach, close**

Under `Engine.Close`: mark the registry closed; close/drain the coordinator; wait for background pins; detach remaining entries/leases from the registry; nil engine fields; close Pebble exactly once. Outstanding external lease operations after detach fail closed and lease Close remains safe.

```bash
GOWORK=off go test ./pkg/db/message \
  -run 'Test(Engine|ChannelStore|CommitCoordinator|StoreAppendBatch|StoreApplyFetchBatch)' -count=1
```

- [ ] **Step 8: Commit compatibility/commit ownership**

```bash
git add pkg/db/message
git commit -m "feat(message): lease compatibility stores and commit pins"
```

### Task 4: Make the Channel store adapter own and release leases

**Files:**
- Modify: `pkg/channel/store/channel_adapter.go`
- Modify: `pkg/channel/store/channel_adapter_test.go`
- Modify: `pkg/channel/store/metrics.go`

- [ ] **Step 1: Add failing adapter lifecycle tests**

Add:

```text
TestMessageDBAdapterCloseReclaimsEntry
TestMessageDBAdapterDoubleCloseIsSafe
TestMessageDBAdapterRejectsUseAfterClose
TestMessageDBAdapterCheckpointPreservesFields
TestMessageDBAdapterConcurrentCheckpointCannotRegress
TestMessageDBAppendBatchReleasesAllLeasesOnError
TestMessageDBApplyBatchReleasesAllLeasesOnError
```

```bash
GOWORK=off go test ./pkg/channel/store -run 'TestMessageDB(Adapter|AppendBatch|ApplyBatch)' -count=1
```

- [ ] **Step 2: Remove factory checkpoint maps and adopt the new error API**

Delete `MessageDBFactory.mu`, `checkpointLocks`, and `checkpointLock`. `ChannelStore` propagates `Engine.ForChannel` errors. `StoreCheckpoint` calls the message-layer monotonic method so all wrappers share `entry.checkpointMu`.

- [ ] **Step 3: Close the adapter exactly once**

`messageDBChannelStoreAdapter.Close` calls its compatibility store `Close`. All adapter operations rely on compatibility validation and map `dberrors.ErrClosed` through the existing Channel error mapper to `channel.ErrClosed`.

- [ ] **Step 4: Release batch acquisitions on every path**

Collect each temporary store in a local slice and close all after `StoreAppendBatch`/`StoreApplyFetchTrustedBatch`, including result-length mismatch and context/error paths. Combine close errors only when no stronger item error exists; never leak a store due to an early return.

- [ ] **Step 5: Verify and commit**

```bash
GOWORK=off go test ./pkg/channel/store -count=1

git add pkg/channel/store/channel_adapter.go pkg/channel/store/channel_adapter_test.go pkg/channel/store/metrics.go
git commit -m "feat(channel-store): release message leases from adapters"
```

### Task 5: Close every temporary production acquisition

**Files:**
- Modify: `pkg/channel/worker/task.go`
- Modify: `pkg/channel/worker/task_test.go`
- Modify: `pkg/cluster/node.go`
- Modify: `pkg/cluster/node_test.go`
- Modify: `pkg/cluster/channels/service.go`
- Modify: `pkg/cluster/channels/channels_test.go`
- Modify: `pkg/db/transfer/importer.go`
- Modify: `pkg/db/transfer/importer_test.go`
- Modify: all `pkg/db/message/*_test.go` and benchmarks required by the new acquisition signature

- [ ] **Step 1: Add failing worker ownership tests**

Add:

```text
TestStoreTasksReleaseTemporaryStoreOnSuccess
TestStoreTasksReleaseTemporaryStoreOnError
TestStoreTasksReleaseTemporaryStoreOnCancellation
TestStoreLoadTransfersLeaseOnSuccess
TestStoreLoadClosesLeaseOnLoadOrRetentionError
```

Use a counting store/factory. Retention, append, read-log, lookup, apply, and checkpoint must close once; successful load must remain open in the result until its consumer closes it.

- [ ] **Step 2: Add `defer Close` to temporary worker tasks**

Immediately after every successful non-load `ChannelStore` acquisition, `defer cs.Close()`. In `runStoreLoad`, close on nil/Load/retention errors and transfer ownership only in a successful `StoreLoadResult`.

- [ ] **Step 3: Close cluster and service read helpers**

In `Node.ReadChannelCommitted`, `Node.LookupChannelIdempotency`, and `channels.Service` last-visible/local read helpers, defer Close immediately after acquire so meta/route/read errors cannot leak.

- [ ] **Step 4: Close transfer/import and direct typed acquisitions**

Update `pkg/db/transfer/importer.go` and every production `MessageDB.Channel`/`Engine.ForChannel` call to handle errors and close. Import flushes acquire for one batch, append, and close; use `errors.Join` only where both operation and close errors must be preserved.

- [ ] **Step 5: Verify production caller coverage**

```bash
rg -n '\.Channel\(|ForChannel\(' --glob '*.go' --glob '!**/*_test.go' --glob '!learn_project/**'
GOWORK=off go test ./pkg/channel/worker ./pkg/cluster/channels ./pkg/cluster ./pkg/db/transfer -count=1
```

Manually account for every listed acquisition as temporary-close, Reactor transfer, or coordinator pin.

- [ ] **Step 6: Commit caller migration**

```bash
git add pkg/channel/worker pkg/cluster pkg/db/transfer pkg/db/message
git commit -m "fix(storage): close temporary channel store leases"
```

### Task 6: Close Reactor lifetime leases on eviction and shutdown

**Files:**
- Modify: `pkg/channel/reactor/reactor_loop.go`
- Modify: `pkg/channel/reactor/runtime_channel.go`
- Modify: `pkg/channel/reactor/worker_completion.go`
- Modify: `pkg/channel/reactor/waiter_cancellation.go`
- Modify: `pkg/channel/reactor/reactor.go`
- Modify: `pkg/channel/reactor/group.go`
- Modify: `pkg/channel/reactor/reactor_nonblocking_test.go`
- Modify: `pkg/channel/reactor/replication_state_test.go`
- Modify: `pkg/channel/reactor/group_test.go`

- [ ] **Step 1: Add MessageDB-backed eviction tests**

Add:

```text
TestRuntimeEvictReleasesMessageDBEntry
TestRuntimeEvictReloadPreservesLEOHWAndMessages
```

Keep and rerun `TestRuntimeEvictDoesNotBlockOnStoreClose`; ordinary eviction must submit asynchronous StoreClose and return before the close finishes.

- [ ] **Step 2: Add shutdown and late-result tests**

Add:

```text
TestGroupCloseReleasesLoadedStoreHandles
TestGroupCloseReleasesPendingMetaStoreHandles
TestClosedGroupReleasesLateStoreLoadResult
```

The final test blocks StoreLoad until after group close begins, then releases it and asserts its returned store closes instead of being dropped.

- [ ] **Step 3: Define explicit store-detach ownership**

After a Reactor stops admission and its single-writer loop, collect/detach `runtimeChannel.store` from loaded, loading-completion, and pending-meta states. Fail futures first. If a StoreLoad result cannot be delivered to a stopped Reactor, close `result.StoreLoad.Store` in the completion/drop path.

- [ ] **Step 4: Order Group shutdown safely**

Stop and join reactors so no new tasks are admitted, close worker pools so accepted work completes/cancels, then close every detached lifetime store. Do not close the shared factory/Pebble from Group. Ensure each store is detached/closed at most once.

- [ ] **Step 5: Verify and commit**

```bash
GOWORK=off go test ./pkg/channel/reactor \
  -run 'Test(RuntimeEvict|GroupClose|ClosedGroup|FollowerStopEviction)' -count=1

git add pkg/channel/reactor
git commit -m "fix(channel): release store leases during reactor shutdown"
```

### Task 7: Expose lifecycle pressure and gate 100,000-channel reclamation

**Files:**
- Create: `pkg/db/message/channel_registry_metrics.go`
- Modify: `pkg/db/message/channel_registry_test.go`
- Modify: `pkg/db/message/message_benchmark_test.go`
- Modify: `pkg/channel/store/metrics.go`
- Modify: `pkg/cluster/storage_metrics.go`
- Modify: `pkg/metrics/storage.go`
- Modify: `pkg/metrics/registry_test.go`
- Modify: `docker/observability/grafana/dashboard_assets_test.go`
- Modify: `docker/observability/grafana/dashboards/wukongim-v2-runtime-ops.json`
- Modify: `pkg/db/message/FLOW.md`
- Modify: `pkg/channel/store/FLOW.md` if present
- Modify: `pkg/channel/worker/FLOW.md`
- Modify: `pkg/channel/reactor/FLOW.md`
- Modify: `pkg/cluster/FLOW.md`

**Snapshot:**

```go
type ChannelEntryMetricsSnapshot struct {
	ActiveEntries     uint64
	OutstandingLeases uint64
	BackgroundPins    uint64
	AcquireTotal      uint64
	ReleaseTotal      uint64
	ReclaimTotal      uint64
}

func (db *MessageDB) ChannelEntryMetricsSnapshot() ChannelEntryMetricsSnapshot
func (e *Engine) ChannelEntryMetricsSnapshot() ChannelEntryMetricsSnapshot
func (f *MessageDBFactory) ChannelEntryMetricsSnapshot() messagedb.ChannelEntryMetricsSnapshot
```

- [ ] **Step 1: Add failing metrics and 100,000-key tests**

Add `TestChannelEntryMetricsTrackAcquireReleaseAndReclaim` and `TestChannelRegistryReclaimsOneHundredThousandDistinctChannels`. The large test acquires/closes 100,000 keys without writes and ends with zero entries, leases, and pins; it remains a fast unit test.

```bash
GOWORK=off go test ./pkg/db/message \
  -run 'TestChannel(EntryMetrics|RegistryReclaimsOneHundredThousand)' -count=1
```

- [ ] **Step 2: Propagate snapshots to storage metrics**

Extend the existing factory/cluster storage snapshots and `StorageMetrics` with gauges/counters for active entries, outstanding leases, background pins, acquire, release, and reclaim totals. Use only the fixed `store="channel_log"` label. Add registry gather tests and operational dashboard panels; update the dashboard-assets gate for every new metric family.

- [ ] **Step 3: Add steady and cold benchmarks**

Add:

```text
BenchmarkChannelLeaseSteadyAppend
BenchmarkChannelLeaseColdReacquire
```

```bash
GOWORK=off go test ./pkg/db/message -run '^$' \
  -bench 'BenchmarkChannelLease(SteadyAppend|ColdReacquire)$' -benchmem -count=5
```

Record both results; do not conflate expected cold LEO recovery cost with steady lease overhead.

- [ ] **Step 4: Update ownership flows**

Document canonical entry/lease/pin ownership, finalizer timing, batch lock deduplication, StoreLoad transfer, temporary worker closes, asynchronous eviction, late result disposal, and factory shutdown order. Do not mention the deleted process-lifetime maps as supported behavior.

- [ ] **Step 5: Run focused race and package verification**

```bash
GOWORK=off go test -race \
  ./pkg/db/internal/commit ./pkg/db/message ./pkg/channel/store ./pkg/channel/worker ./pkg/channel/reactor \
  -count=1

GOWORK=off go test \
  ./pkg/db/internal/commit ./pkg/db/message ./pkg/channel/store ./pkg/channel/worker ./pkg/channel/reactor \
  ./pkg/cluster ./pkg/cluster/channels ./pkg/db/transfer ./pkg/metrics ./docker/observability/grafana \
  -count=1

git diff --check
```

- [ ] **Step 6: Commit lifecycle gates and documentation**

```bash
git add pkg/db/message pkg/channel/store pkg/channel/worker pkg/channel/reactor pkg/cluster \
  pkg/metrics docker/observability/grafana
git commit -m "test(storage): gate channel lease reclamation and performance"
```
