# Channelv2 Multiple Reactors Design

**Date:** 2026-05-23
**Status:** Proposed

## Overview

`pkg/channel` currently contains the channel log execution path, replica state machine, runtime lifecycle, replication lanes, migration fences, retention, leader repair, snapshot handling, and storage integration in one mature but difficult-to-read package tree. The design is powerful, but the responsibilities are tightly coupled. It is hard to reason about the high-concurrency hot path without also loading migration, retention, reconcile, and long-poll lane details.

This design introduces `pkg/channelv2` as an experimental package, not as an immediate replacement for `pkg/channel`. The first version validates a clearer high-concurrency architecture based on **Multiple Reactors With Bounded Worker Pools**:

- Each channel is owned by exactly one reactor selected by channel-key hash.
- The reactor owns all in-memory state for that channel.
- Blocking work such as store append, store read, follower apply, and RPC runs in bounded worker pools.
- Worker completions are routed back to the owning reactor and fenced before being applied.
- The first implementation slice provides a minimal end-to-end loop: append, fetch, follower apply, follower ACK, leader HW commit.

The package deliberately excludes migration, retention, snapshot install, leader repair, and promotion dry-run in the first slice. Those features are important, but they should be added only after the simpler architecture proves readable and performant.

## Goals

- Build a clean `pkg/channelv2` package beside the existing `pkg/channel`; do not modify the current production package in the experimental slice.
- Validate an end-to-end channel log loop: `ApplyMeta`, `Append`, `AppendBatch`, `Fetch`, leader-to-follower replication, ACK, and HW advancement.
- Preserve per-channel ordering by making one reactor the only owner of each channel's `ChannelState`.
- Enable cross-channel concurrency through multiple reactors and bounded worker pools.
- Keep reactors non-blocking; all store and RPC work must run as tasks that report completion events.
- Reuse the existing `pkg/channel/store` through a narrow adapter so the first slice does not rewrite the storage engine.
- Keep `pkg/channelv2/machine`, `pkg/channelv2/reactor`, and `pkg/channelv2/worker` independent from the old `pkg/channel` internals.
- Preserve cluster-only semantics. A single-node deployment is modeled as a single-node cluster where quorum rules naturally commit local appends.
- Provide focused tests and benchmarks that can compare memory-store behavior with the old store adapter.

## Non-Goals

- No immediate replacement of `pkg/channel` in `internal/app`.
- No compatibility promise for the full `pkg/channel.Cluster` API in the first experimental slice.
- No migration write fence, fence-and-drain, learner promotion, or cutover proof.
- No retention boundary adoption or physical trim.
- No snapshot install or snapshot catch-up path.
- No leader repair, promotion dry-run, or reconcile-based minority tail truncation.
- No full long-poll lane protocol, membership-version reset, or peer-lane batching in v0.
- No new storage format in v0.
- No bypass path for single-node deployments.

## Approaches Considered

### Approach A: Channel actor per active channel

Every active channel owns a goroutine and mailbox. Requests are sent directly to that channel actor.

Pros:

- Very easy to understand for one channel.
- Natural same-channel ordering.
- Simple local timers and pending queues.

Cons:

- High active-channel counts create many goroutines, timers, and mailboxes.
- Hot and cold channel fairness becomes scattered.
- Worker and shutdown coordination grows into a per-channel runtime again.

This approach is rejected for the high-concurrency target.

### Approach B: Shard locks with shared workers

Hash channels into shards. Workers take shard locks and update channel state directly.

Pros:

- Simple to implement.
- Similar to the current `runtimeShardCount` direction.
- Low overhead for small workloads.

Cons:

- Shard locks can couple unrelated channels.
- Blocking or slow operations can creep back into locked regions.
- State-machine boundaries are less explicit than reactor ownership.

This approach is a useful fallback but not the preferred architecture.

### Approach C: Multiple reactors with bounded worker pools

Hash channels to reactors. A reactor owns many `ChannelState` instances and runs a non-blocking event loop. Blocking store and RPC work runs in worker pools. Results are routed back to the same reactor.

Pros:

- Clear state ownership: one channel, one reactor.
- Cross-channel concurrency scales with reactor and worker count.
- Bounded queues provide explicit backpressure.
- Worker-result fencing protects role and epoch transitions.
- The architecture can later absorb retention, snapshot, and repair without changing the top-level concurrency model.

Cons:

- More moving parts than direct synchronous code.
- Requires disciplined future, cancellation, fencing, mailbox, and shutdown handling.
- Tail latency can suffer if batch windows or reactor turns are too large.

This is the recommended approach.

## Package Layout

```text
pkg/channelv2/
  doc.go
  errors.go
  types.go
  channel.go

  service/
    service.go
    meta.go
    append.go
    fetch.go

  reactor/
    reactor.go
    group.go
    mailbox.go
    router.go
    event.go
    future.go
    backpressure.go

  machine/
    channel.go
    append.go
    fetch.go
    follower.go
    progress.go
    meta.go
    invariant.go

  worker/
    pool.go
    task.go
    result.go
    group_commit.go

  store/
    adapter.go
    channel_adapter.go
    memory.go

  transport/
    types.go
    local.go
    rpc_adapter.go

  replication/
    planner.go
    leader.go
    follower.go
    session.go

  testkit/
    cluster.go
    store.go
    transport.go
```

### Boundary Rules

- `service` is a thin synchronous facade. It validates request shape, creates futures, submits reactor events, and waits for results.
- `reactor` owns event loops, routing, mailbox policy, future completion, and task dispatch. It does not contain channel business rules.
- `machine` owns pure channel state transitions. It does not perform store I/O, RPC, sleeps, or goroutine management.
- `worker` executes blocking tasks and reports typed completion results.
- `store` exposes a narrow persistence interface. Only `pkg/channelv2/store/channel_adapter.go` may import the old `pkg/channel` or `pkg/channel/store`.
- `transport` exposes the minimal v0 replication RPCs.
- `replication` owns follower pull and leader ACK decisions.

## Public API

The first slice should keep the facade close to the old package, but it should not promise full compatibility:

```go
type Cluster interface {
	ApplyMeta(meta Meta) error
	Append(ctx context.Context, req AppendRequest) (AppendResult, error)
	AppendBatch(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error)
	Fetch(ctx context.Context, req FetchRequest) (FetchResult, error)
	Tick(ctx context.Context) error
	Close() error
}
```

`Tick` is optional. It is useful in tests and benchmarks because it can drive flush timers, replication retries, and timeout scans deterministically. A productionized version can replace it with internal reactor tickers.

### ApplyMeta Meaning

`ApplyMeta(meta Meta)` applies the authoritative runtime view for a channel. It is not a local election or repair API. It tells the channel runtime:

- Which node is the current leader.
- Which nodes are replicas.
- Which replicas are in ISR.
- Which `Epoch` and `LeaderEpoch` fence the channel.
- Whether the channel is active, deleting, or deleted.
- Whether appends must be rejected by a future write fence extension.

`channelv2` executes this projected control-plane state. It must not infer a different leader or membership by itself in v0.

## Core Runtime Model

```text
client/api
  -> channelv2.Service
  -> Router(hash channel -> Reactor)
  -> owning Reactor
  -> machine.ChannelState
  -> worker pools for store/RPC
  -> worker completion back to owning Reactor
  -> machine.ChannelState applies fenced result
```

Rules:

- A `ChannelState` is modified only inside its owning reactor.
- Worker tasks must never hold a pointer that lets them mutate `ChannelState`.
- Every worker result must carry a fence.
- A reactor must validate the fence before applying a result.
- Same-channel appends are ordered; different channels can progress concurrently.

## Channel State Machine

`machine.ChannelState` is the single-channel aggregate root:

```go
type ChannelState struct {
	Key          ChannelKey
	ID           ChannelID
	Generation   uint64
	Epoch        uint64
	LeaderEpoch  uint64
	Role         Role
	Status       Status
	Leader       NodeID
	Replicas     []NodeID
	ISR          []NodeID
	MinISR       int
	LEO          uint64
	HW           uint64
	CheckpointHW uint64
	CommitReady  bool
	Progress     map[NodeID]ReplicaProgress
	PendingAppends map[OpID]*AppendWaiter
	InflightAppend *AppendOp
}
```

Important fields:

- `LEO` is the local log end offset.
- `HW` is the committed high watermark.
- `CheckpointHW` is the durable checkpoint high watermark. In v0 it can initially follow `HW` or be stored asynchronously.
- `Progress` tracks leader-side replica match offsets.
- `PendingAppends` tracks client futures waiting for local or quorum completion.
- `InflightAppend` ensures one durable append batch per channel at a time in v0.

### State Transition Shape

State methods return decisions rather than executing effects:

```go
func (c *ChannelState) ApplyMeta(meta Meta) Decision
func (c *ChannelState) ProposeAppend(req AppendCommand) Decision
func (c *ChannelState) ApplyAppendStored(res StoreAppendResult) Decision
func (c *ChannelState) ApplyFollowerAck(ack FollowerAck) Decision
func (c *ChannelState) BuildFetch(req FetchCommand) Decision
```

```go
type Decision struct {
	Tasks      []worker.Task
	Replies    []Reply
	Broadcasts []ReplicationSignal
	WakeAfter  time.Duration
}
```

This makes `machine` easy to test without goroutines, channels, store, or RPC.

## Reactor Events

Reactors process typed events:

```go
type ApplyMetaEvent struct {
	Meta  Meta
	Reply Future
}

type AppendEvent struct {
	Req   AppendRequest
	Reply Future
}

type FetchEvent struct {
	Req   FetchRequest
	Reply Future
}

type WorkerResultEvent struct {
	Result worker.Result
}

type ReplicationEvent struct {
	Message replication.Message
}

type TickEvent struct {
	Now time.Time
}
```

Loop shape:

```text
for event := range mailbox:
  channel := getOrCreateChannel(event.ChannelKey)
  decision := channel.Handle(event)
  dispatch(decision.Tasks)
  complete(decision.Replies)
  signalReplication(decision.Broadcasts)
```

Reactors should drain multiple events per turn, for example 256 or 512, to reduce channel overhead and improve append batching.

## Fencing

All asynchronous work must carry a fence:

```go
type Fence struct {
	ChannelKey  ChannelKey
	Generation  uint64
	Epoch       uint64
	LeaderEpoch uint64
	OpID        uint64
}
```

When a worker result returns, the reactor applies it only if:

```text
result.ChannelKey == current.Key
result.Generation == current.Generation
result.Epoch == current.Epoch
result.LeaderEpoch == current.LeaderEpoch
result.OpID matches the current inflight operation when required
```

Stale results are ignored or recorded as diagnostics. They must not resurrect deleted channels, complete old append waiters, or publish old leader state after `ApplyMeta` changes the role or epoch.

## Append Flow

```text
Append(ctx, req)
  -> service validates request shape
  -> service routes AppendEvent to owning reactor
  -> reactor loads ChannelState
  -> machine validates active leader state and expected epoch
  -> append enters per-channel pending queue
  -> reactor batches pending records
  -> StoreAppendTask runs in StoreAppend pool
  -> StoreAppendResult returns to reactor
  -> machine advances LEO and local progress
  -> machine recomputes HW
  -> completed waiters receive replies
  -> replication wake is emitted for followers
```

Batching policy:

```text
maxRecords: 64 by default
maxBytes:   64 KB by default
maxWait:    0.5 ms to 1 ms by default
```

Commit behavior:

- `CommitModeQuorum` waits until `HW` covers the batch range.
- `CommitModeLocal` can return after leader local durable append succeeds.
- Single-message `Append` is a wrapper over the same append-batch path.

V0 should allow idempotency to be disabled for benchmarks. If idempotency is kept, it should live in `service` or the store adapter, not in reactor scheduling code.

## Fetch Flow

Fetch reads a committed view:

```text
Fetch(ctx, req)
  -> service validates Limit and MaxBytes
  -> FetchEvent enters owning reactor
  -> reactor captures current HW
  -> if FromSeq > HW, return empty result
  -> otherwise schedule StoreReadCommittedTask with MaxSeq = captured HW
  -> StoreReadCommittedResult returns to reactor
  -> reactor revalidates fence and clamps records by captured/current HW
  -> future receives FetchResult
```

Semantics:

- Fetch returns records committed at or below the captured high watermark.
- Fetch does not promise to include records committed after the request was captured.
- Fetch must never return uncommitted records.

## Replication V0 Protocol

The first replication protocol is intentionally small:

```go
type Client interface {
	Pull(ctx context.Context, node NodeID, req PullRequest) (PullResponse, error)
	Ack(ctx context.Context, node NodeID, req AckRequest) error
}

type Server interface {
	HandlePull(ctx context.Context, req PullRequest) (PullResponse, error)
	HandleAck(ctx context.Context, req AckRequest) error
}
```

```go
type PullRequest struct {
	ChannelKey  ChannelKey
	ChannelID   ChannelID
	Epoch       uint64
	LeaderEpoch uint64
	Follower    NodeID
	NextOffset  uint64
	MaxBytes    int
}

type PullResponse struct {
	ChannelKey  ChannelKey
	Epoch       uint64
	LeaderEpoch uint64
	LeaderHW    uint64
	LeaderLEO   uint64
	Records     []Record
}

type AckRequest struct {
	ChannelKey  ChannelKey
	Epoch       uint64
	LeaderEpoch uint64
	Follower    NodeID
	MatchOffset uint64
}
```

### Follower Flow

```text
ApplyMeta or Tick marks follower replication dirty
  -> if no pull is inflight, submit RPCPullTask
  -> PullResponse returns to follower reactor
  -> if records are empty, HW = min(LEO, LeaderHW)
  -> if records exist, submit StoreApplyTask
  -> StoreApplyResult advances follower LEO
  -> follower HW = min(LEO, last LeaderHW)
  -> submit AckTask to leader
  -> mark replication dirty for next pull
```

Follower pull backoff:

```text
initial: 10 ms
max:     1 s
factor:  2
jitter:  20%
```

Successful pulls with data reset backoff and immediately schedule the next pull. Successful empty pulls use a short idle wait.

### Leader Pull Handling

```text
HandlePull
  -> route PullRequestEvent to leader reactor
  -> validate role == leader
  -> validate epoch and leader epoch
  -> validate follower is in Replicas
  -> read records from NextOffset through current LEO
  -> return records, current HW, and current LEO
```

V0 can use short polling with backoff. Long-poll wait queues and peer-lane batching are later optimizations.

### Leader ACK Handling

```text
HandleAck
  -> route AckEvent to leader reactor
  -> validate role == leader
  -> validate epoch and leader epoch
  -> validate follower is in Replicas
  -> Progress[follower].Match = max(old, MatchOffset)
  -> recompute HW from ISR only
  -> complete append waiters covered by HW
```

Replicas may receive log data. Only ISR members participate in HW quorum.

## HW Commit Calculation

Leader commit uses ISR progress:

```text
matches = Progress[replica].Match for replica in ISR
sort matches descending
quorumOffset = matches[MinISR-1]
HW = max(HW, quorumOffset)
```

Example:

```text
ISR = [1,2,3], MinISR = 2
matches = [105,104,80]
HW advances to 104
```

Single-node cluster:

```text
Replicas = [local]
ISR = [local]
MinISR = 1
```

After local durable append, local progress equals `LEO`, so quorum calculation advances `HW` to `LEO`. This is still cluster semantics; it is not a bypass branch.

## Store Adapter

`pkg/channelv2/store` exposes a narrow interface:

```go
type Factory interface {
	ChannelStore(key ChannelKey, id ChannelID) (ChannelStore, error)
}

type ChannelStore interface {
	Load(ctx context.Context) (InitialState, error)
	AppendLeader(ctx context.Context, req AppendLeaderRequest) (AppendLeaderResult, error)
	ApplyFollower(ctx context.Context, req ApplyFollowerRequest) (ApplyFollowerResult, error)
	ReadCommitted(ctx context.Context, req ReadCommittedRequest) (ReadCommittedResult, error)
	ReadLog(ctx context.Context, req ReadLogRequest) (ReadLogResult, error)
	StoreCheckpoint(ctx context.Context, checkpoint Checkpoint) error
	Close() error
}
```

DTOs:

```go
type InitialState struct {
	LEO          uint64
	HW           uint64
	CheckpointHW uint64
}

type AppendLeaderRequest struct {
	Records []Record
	Sync    bool
}

type AppendLeaderResult struct {
	BaseOffset uint64
	LastOffset uint64
}

type ApplyFollowerRequest struct {
	Records  []Record
	LeaderHW uint64
}

type ApplyFollowerResult struct {
	LEO uint64
}

type ReadCommittedRequest struct {
	FromSeq  uint64
	MaxSeq   uint64
	Limit    int
	MaxBytes int
}

type ReadCommittedResult struct {
	Messages []Message
	NextSeq  uint64
}

type ReadLogRequest struct {
	FromOffset uint64
	MaxOffset  uint64
	MaxBytes   int
}

type ReadLogResult struct {
	Records []Record
}
```

Adapter guarantees:

- `AppendLeader` appends a continuous record batch and returns the base and last offsets.
- `ApplyFollower` applies leader records in order and may handle duplicate prefixes idempotently.
- `ReadCommitted` never returns records above `MaxSeq`.
- `ReadLog` respects `MaxOffset` and `MaxBytes`.
- `Load` restores enough state to initialize `LEO`, `HW`, and `CheckpointHW`.

The old store adapter may import:

```go
oldchannel "github.com/WuKongIM/WuKongIM/pkg/channel"
oldstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
```

No other `channelv2` package should import old `pkg/channel` internals in v0.

## Worker Pools

The first slice should use separate bounded pools:

```go
type Pools struct {
	StoreAppend *Pool
	StoreRead   *Pool
	RPC         *Pool
}
```

Reasons:

- Append I/O must not starve fetch reads.
- Fetch scans must not starve follower apply or append completion.
- Slow RPCs must not occupy store workers.

Pool config:

```go
type PoolConfig struct {
	Workers   int
	QueueSize int
	Name      string
}
```

Task shape:

```go
type TaskKind uint8

const (
	TaskStoreAppend TaskKind = iota + 1
	TaskStoreApply
	TaskStoreReadCommitted
	TaskStoreReadLog
	TaskRPCPull
	TaskRPCAck
)

type Task struct {
	Kind  TaskKind
	Fence Fence
	// one typed payload field per task kind
}
```

Workers report results to a `CompletionSink`. The sink routes by `Fence.ChannelKey` back to the owning reactor.

## Group Commit Strategy

V0 uses two levels of batching:

1. Reactor-level per-channel append batching for ordering and request coalescing.
2. Store-level cross-channel sync batching if the old store adapter already provides it.

`channelv2` should not implement a complex cross-channel sync coordinator in the first slice. The adapter can reuse the existing store flush window. After benchmarks, a `worker.SyncCoordinator` can be introduced if store sync remains the bottleneck.

## Reactor Group, Routing, and Mailbox

`reactor.Group` owns all reactors:

```go
type Group struct {
	reactors []*Reactor
	router   Router
	workers  *worker.Pools
	store    store.Factory
	transport transport.Client
}
```

Suggested config defaults:

```text
ReactorCount = GOMAXPROCS
MailboxSize = 4096 or 8192
MaxPendingPerChannel = 1024
AppendBatchMaxRecords = 64
AppendBatchMaxBytes = 64 KB
AppendBatchMaxWait = 1 ms
```

Routing:

```text
reactorIndex = hash(ChannelKey) % ReactorCount
```

The hash only needs to be stable in v0. FNV is acceptable initially; faster hashes can be considered after profiling.

### Priority Mailbox

Use bounded priority queues:

```text
High:
  ApplyMeta
  WorkerResult
  Close/Tombstone

Normal:
  Append
  Fetch
  Replication request/response

Low:
  Tick
  Background replication poll
  Checkpoint
```

`WorkerResult` is high priority so completed store work can release waiters and reduce queue pressure under append load.

Mailbox behavior:

- High full: return backpressure or block briefly for shutdown-critical events.
- Normal full: return `ErrBackpressured`.
- Low full: drop or coalesce tick/background events.

### Event Coalescing

V0 should coalesce:

- Reactor tick events.
- Per-channel replication wake events.
- Append flush wakeups.

For append flush timing, use a reactor-level ticker and dirty set in the first slice. Do not start with per-channel timers or a complex timing wheel.

## Backpressure

Backpressure limits:

```go
type Limits struct {
	MailboxHigh                    int
	MailboxNormal                  int
	MailboxLow                     int
	MaxChannels                    int
	MaxPendingAppendPerChannel     int
	MaxPendingAppendBytesPerChannel int
	MaxInflightStoreTasksPerReactor int
	MaxInflightRPCTasksPerPeer      int
}
```

Behavior:

- Full normal mailbox: `Append` and `Fetch` return `ErrBackpressured`.
- Full per-channel pending queue: the new append returns `ErrBackpressured`.
- Full store worker queue: either retry on the next tick or fail the affected waiters with `ErrBackpressured`. V0 should prefer a lightweight retry for append tasks.
- Full per-peer RPC inflight: follower skips the new pull and retries later.
- Max channel count reached: `ApplyMeta` returns `ErrTooManyChannels`.

Hot-channel fairness:

- A channel may flush at most one append batch per reactor turn.
- A reactor drains multiple events per turn but should not let one channel consume the whole turn forever.
- Worker results remain high priority to avoid completion starvation.

## Activation and Loading

When `ApplyMeta` creates a new channel:

```text
Reactor creates ChannelState in Loading
  -> submits StoreLoadTask
  -> Append returns ErrNotReady while loading
  -> Fetch returns ErrNotReady while loading
  -> replication waits
  -> LoadResult initializes LEO, HW, and CheckpointHW
  -> role from current Meta is applied
  -> CommitReady = true in v0
```

The v0 simplification is explicit: it assumes no unproven minority tail on startup. Later leader repair work can change this path to `Loading -> Reconciling -> Ready`.

## Shutdown

`Close()` should:

1. Stop accepting new service requests.
2. Submit close events to reactors.
3. Fail all pending futures.
4. Stop processing new worker results except for logging stale completions.
5. Close worker pools and transport.

V0 does not need a graceful drain that waits for all pending appends to commit. A productionized version can add `Drain(timeout)`.

## Test Plan

### Machine unit tests

- `ApplyMeta` creates leader and follower roles.
- Leader append proposal is rejected when local node is not leader.
- Leader append proposal creates pending waiter and store task.
- `StoreAppendResult` advances `LEO`.
- Single-node cluster advances `HW` immediately after durable append.
- Follower ACK advances `HW` by `MinISR`.
- Append waiter completes only when `HW` covers its last offset.
- Stale epoch or leader-epoch result is ignored.
- Deleted status rejects append and fetch.

### Store adapter contract tests

Run the same contract against memory store and old store adapter:

- Empty load returns zero `LEO` and `HW`.
- `AppendLeader` returns continuous base and last offsets.
- `AppendLeader` records are readable by `ReadLog`.
- `ApplyFollower` applies continuous records.
- `ApplyFollower` duplicate prefix is idempotent or explicitly rejected with a typed error.
- `ReadCommitted` clamps by `MaxSeq`.
- `ReadLog` respects `MaxBytes`.
- `StoreCheckpoint` then `Load` restores checkpoint state.

### Reactor tests

- `AppendEvent` routes to the correct channel.
- Multiple append events batch into one `StoreAppendTask`.
- Worker results release waiters under mailbox pressure.
- Stale `StoreAppendResult` is ignored after newer `ApplyMeta`.
- Channel pending queue full returns `ErrBackpressured`.
- Fetch captures `HW` and returns only committed data.
- Close fails all pending futures.

### Replication E2E tests

Use `testkit.MemoryTransport` and memory store:

- Single-node cluster append then fetch committed.
- Three-node cluster append waits for follower ACK with `MinISR=2`.
- Follower catches up across multiple pull batches.
- Leader `HW` advances when `MinISR` is satisfied.
- Non-ISR replica ACK does not advance `HW`.
- Dropped pull recovers after backoff.
- Stale epoch pull and ACK are rejected.
- Leader transfer meta causes old ACKs to be ignored.

## Benchmark Plan

Benchmarks:

- `BenchmarkAppendSingleNodeManyChannels`
- `BenchmarkAppendSingleNodeHotChannel`
- `BenchmarkAppendThreeNodeManyChannelsMemoryTransport`
- `BenchmarkFetchCommittedManyChannels`
- `BenchmarkReactorMailboxContention`
- `BenchmarkStoreAdapterAppendOldStore`

Metrics:

- Append throughput in messages per second.
- Append p50, p95, and p99 latency.
- HW commit latency.
- Reactor mailbox depth.
- Worker queue depth.
- Pending append count per channel.
- Allocations per operation.
- Goroutine count.

Scenarios:

- One reactor versus `GOMAXPROCS` reactors.
- One hot channel versus 10,000 distributed channels.
- `CommitModeLocal` versus `CommitModeQuorum`.
- Memory store versus old store adapter.
- `MinISR=1` versus `MinISR=2`.

## Implementation Phases

### Phase 0: Scaffolding and types

Add:

```text
pkg/channelv2/doc.go
pkg/channelv2/types.go
pkg/channelv2/errors.go
pkg/channelv2/channel.go
pkg/channelv2/store/adapter.go
pkg/channelv2/transport/types.go
```

Done when public DTOs, errors, store interface, and transport interface are defined.

### Phase 1: Pure machine state

Add:

```text
pkg/channelv2/machine/*
```

Done when `ApplyMeta`, `ProposeAppend`, `ApplyAppendStored`, `ApplyFollowerAck`, fetch-view building, and invariant tests pass without goroutines or I/O.

### Phase 2: Worker and memory store

Add:

```text
pkg/channelv2/worker/*
pkg/channelv2/store/memory.go
```

Done when memory store passes the store contract and worker tasks can run store append, apply, and read operations.

### Phase 3: Reactor group and service facade

Add:

```text
pkg/channelv2/reactor/*
pkg/channelv2/service/*
```

Done when a single-node cluster can `ApplyMeta`, `Append`, and `Fetch` committed records end-to-end.

### Phase 4: Replication v0

Add:

```text
pkg/channelv2/replication/*
pkg/channelv2/transport/local.go
pkg/channelv2/testkit/*
```

Done when a three-node memory cluster commits with `MinISR=2`, followers catch up, ACK advances HW, and RPC failure backoff recovers.

### Phase 5: Old store adapter and benchmark

Add:

```text
pkg/channelv2/store/channel_adapter.go
pkg/channelv2/*_benchmark_test.go
```

Done when old store adapter passes the store contract and benchmark output compares memory store with old store adapter.

### Phase 6: Productionization decision

Use benchmark and complexity results to decide:

- If the architecture is clearly faster and simpler, add leader reconcile, retention, snapshot, and migration in separate designs.
- If the bottleneck is the store, optimize the old store adapter or sync coordinator first.
- If reactors are the bottleneck, optimize mailbox batching, event coalescing, and peer-lane batching.

## Open Questions

- Should `Tick(ctx)` be public only in test builds, or should it remain part of the experimental facade?
- Should idempotency be supported in v0 benchmarks, or should it be an explicit second slice?
- Should `StoreApplyFollower` tolerate duplicate prefixes or return a typed duplicate error for the machine to handle?
- Should follower ACK use a separate RPC or be piggybacked on the next pull request after the first benchmarks?
- What benchmark threshold is sufficient to justify productionizing `channelv2` rather than only extracting lessons back into `pkg/channel`?

