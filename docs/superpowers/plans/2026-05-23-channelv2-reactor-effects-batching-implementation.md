# Channelv2 Reactor Effects And Batching Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Upgrade `pkg/channelv2` from the v0 synchronous prototype into a real multiple-reactors-with-goroutine-pools validation slice with async effects, append batching, and reactor-owned follower replication.

**Architecture:** Reactors remain the single writer for each channel's `machine.ChannelState` and runtime queues, while store and RPC work run in typed bounded worker pools. Worker completions re-enter the owning reactor through high-priority `EventWorkerResult` events, where fences are validated before state is changed or futures are completed.

**Tech Stack:** Go 1.23, standard `context`/`sync`/`time`/`errors`/`runtime`, existing `testing` plus `testify/require`, existing `pkg/channelv2/store` and `pkg/channelv2/transport` contracts, old `pkg/channel` imports only in `pkg/channelv2/store/channel_adapter.go`.

---

## Source Spec

- Read first: `docs/superpowers/specs/2026-05-23-channelv2-reactor-effects-batching-design.md`
- Also read before editing: `AGENTS.md`, `pkg/channelv2/FLOW.md`, `docs/superpowers/plans/2026-05-23-channelv2-reactor-v0-implementation.md`
- Use @superpowers:test-driven-development for every code task.
- If executing with subagents, use @superpowers:subagent-driven-development. Otherwise use @superpowers:executing-plans.
- If a test fails unexpectedly, stop feature work and use @superpowers:systematic-debugging.
- Before claiming completion, use @superpowers:verification-before-completion.

## Scope Notes

- Keep all new production code under `pkg/channelv2`; do not wire it into `internal/app` and do not replace existing `pkg/channel`.
- Keep single-node behavior as single-node cluster quorum behavior with `MinISR=1`; do not add a bypass branch.
- Keep the v0 public facade shape (`ApplyMeta`, `Append`, `AppendBatch`, `Fetch`, `Tick`, `Close`). Phase 2 changes internals only unless a config field is needed for bounded resources.
- Keep short-poll `Pull` plus explicit `Ack`. Do not add long-poll lanes, snapshots, retention trim, migration cutover, or leader repair.
- Only `pkg/channelv2/store/channel_adapter.go` may import old `pkg/channel` or `pkg/channel/store`.
- Unit tests must stay fast. If a scenario needs real time sleeps above a few milliseconds or slow external systems, make it an integration test instead. Prefer fake clocks or explicit `Tick` events.

## Current Starting Point

- `pkg/channelv2/worker/task.go` has `TaskKind` constants but `Task` still executes only `RunFunc`.
- `pkg/channelv2/reactor/event.go` already defines `EventWorkerResult` and `EventTick`, but `reactor.handle` does not process them.
- `pkg/channelv2/reactor/reactor.go` still calls `AppendLeader`, `ReadCommitted`, `ReadLog`, and `ApplyFollower` synchronously inside reactor handlers.
- `pkg/channelv2/service/service.go` still performs follower replication from `cluster.Tick` by calling `Fetch`, `transport.Pull`, `EventApplyRecords`, and `transport.Ack` outside the reactor.
- `machine.ChannelState` supports only one client append op as the in-flight durable append; Phase 2 needs one batch op with many client waiters.

## File Structure

- Modify: `pkg/channelv2/channel.go` - add Phase 2 config knobs and observer field while preserving existing callers.
- Modify: `pkg/channelv2/types.go` - add field comments only if new DTO/config types are introduced.
- Modify: `pkg/channelv2/worker/task.go` - replace `RunFunc` hot path with typed task payloads and keep `TaskFunc` only for worker unit tests.
- Modify: `pkg/channelv2/worker/result.go` - add `Kind` and typed result payloads.
- Create: `pkg/channelv2/worker/deps.go` - worker dependencies: `store.Factory`, `transport.Client`, and local node id.
- Create: `pkg/channelv2/worker/pools.go` - named bounded pools for store append, store read, store apply, and RPC.
- Modify: `pkg/channelv2/worker/pool.go` - expose pool name/depth and execute typed tasks through deps.
- Create: `pkg/channelv2/worker/task_test.go` - typed task execution tests.
- Modify: `pkg/channelv2/worker/pool_test.go` - keep existing `TaskFunc` tests and add queue-depth assertions for typed task routing.
- Modify: `pkg/channelv2/reactor/event.go` - keep `EventTick` as the per-reactor low-priority tick event and ensure worker result payload is typed.
- Modify: `pkg/channelv2/reactor/group.go` - own worker pools, implement `worker.CompletionSink`, route completions by `Fence.ChannelKey`, and expose `Tick`.
- Create: `pkg/channelv2/reactor/group_test.go` - completion routing and priority tests.
- Create: `pkg/channelv2/reactor/effect.go` - convert machine/store/transport commands into worker tasks and submit them with backpressure handling.
- Create: `pkg/channelv2/reactor/append_queue.go` - per-channel append queue, byte/count limits, max wait, flush decisions, and context cleanup hooks.
- Create: `pkg/channelv2/reactor/replication_state.go` - follower pull/apply/ack state, backoff, and scheduling helpers.
- Create: `pkg/channelv2/reactor/metrics.go` - observer interface, no-op observer, and lightweight metric callbacks.
- Modify: `pkg/channelv2/reactor/backpressure.go` - expand bounded config for append queues, batch limits, worker queues, and replication intervals.
- Modify: `pkg/channelv2/reactor/reactor.go` - remove direct store/RPC calls; handle append queueing, async fetch, worker results, ticks, pull, ack, and close cleanup.
- Create: `pkg/channelv2/reactor/append_queue_test.go` - isolated append queue tests.
- Create: `pkg/channelv2/reactor/async_fetch_test.go` - slow-store non-blocking fetch tests.
- Create: `pkg/channelv2/reactor/append_batch_test.go` - append batching and stale completion tests.
- Create: `pkg/channelv2/reactor/replication_state_test.go` - reactor-owned follower replication tests.
- Modify: `pkg/channelv2/machine/channel.go` - allow one durable append batch to map to many client append waiters.
- Modify: `pkg/channelv2/machine/append.go` - add batch proposal and batch stored completion helpers.
- Modify: `pkg/channelv2/machine/append_test.go` - multi-waiter batch completion and stale batch fence tests.
- Modify: `pkg/channelv2/service/service.go` - pass worker/observer config to group and make `Tick` call `group.Tick(ctx)` only.
- Modify: `pkg/channelv2/service/append.go` - register cancellation cleanup after accepted append requests.
- Modify: `pkg/channelv2/service/fetch.go` - allocate fetch op ids for fenced async reads.
- Modify: `pkg/channelv2/service/replication.go` - keep only inbound leader RPC facade (`HandlePull`, `HandleAck`).
- Modify: `pkg/channelv2/service/service_test.go` - ensure append/fetch still work with async internals.
- Modify: `pkg/channelv2/testkit/cluster.go` - configure Phase 2 worker pools and explicit ticks.
- Modify: `pkg/channelv2/testkit/cluster_test.go` - verify three-node replication still converges.
- Modify: `pkg/channelv2/bench_test.go` - add async/batched benchmarks while retaining v0 smoke benchmarks if useful.
- Modify: `pkg/channelv2/FLOW.md` - document async effects, batching, worker pools, and reactor-owned replication.

## Shared Implementation Rules

Use these rules in every task:

```go
// Fence validation must happen on every async completion before mutating state.
func sameFence(got ch.Fence, state *machine.ChannelState, opID ch.OpID) bool {
	return got.ChannelKey == state.Key &&
		got.Generation == state.Generation &&
		got.Epoch == state.Epoch &&
		got.LeaderEpoch == state.LeaderEpoch &&
		got.OpID == opID
}
```

- Worker tasks may copy request data and call store/transport, but must never mutate `runtimeChannel` or `machine.ChannelState`.
- `runtimeChannel`, append queues, replication state, and waiters are modified only by the owning reactor goroutine.
- Worker completions must be submitted as `PriorityHigh` events.
- Tick events must be `PriorityLow` and safe to drop/coalesce.
- One channel may submit at most one append batch per reactor turn.
- A follower may have at most one pull in flight, one pending pull response, and one ack in flight.
- Accepted append requests are either completed by commit, failed by close, or failed by caller context cancellation cleanup.

## Task 0: Preflight And Baseline

**Files:**
- Read: `AGENTS.md`
- Read: `pkg/channelv2/FLOW.md`
- Read: `docs/superpowers/specs/2026-05-23-channelv2-reactor-effects-batching-design.md`
- Read: files listed in File Structure before editing each area

- [ ] **Step 1: Check worktree state**

Run:

```bash
git status --short
```

Expected: no unrelated changes in files this plan will edit. If unexpected changes exist, stop and ask the user how to proceed.

- [ ] **Step 2: Run current channelv2 tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/... -count=1
```

Expected: PASS before Phase 2 changes. If it fails, use @superpowers:systematic-debugging before continuing.

- [ ] **Step 3: Run current import boundary check**

Run:

```bash
rg 'pkg/channel"|pkg/channel/store' pkg/channelv2 -g'*.go'
```

Expected: only `pkg/channelv2/store/channel_adapter.go` and its test if the test intentionally references old types. No reactor, worker, service, or machine file may import old `pkg/channel`.

## Task 1: Typed Worker Tasks And Pool Group

**Files:**
- Modify: `pkg/channelv2/worker/task.go`
- Modify: `pkg/channelv2/worker/result.go`
- Modify: `pkg/channelv2/worker/pool.go`
- Create: `pkg/channelv2/worker/deps.go`
- Create: `pkg/channelv2/worker/pools.go`
- Create: `pkg/channelv2/worker/task_test.go`
- Modify: `pkg/channelv2/worker/pool_test.go`

- [ ] **Step 1: Write failing typed task tests**

Create `pkg/channelv2/worker/task_test.go` with focused tests for store append, committed read, raw log read, follower apply, RPC pull, and RPC ack. Keep fake stores/transports local to the test file.

Use this test file content as the starting point:

```go
package worker

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/stretchr/testify/require"
)

type workerTransportServer struct {
	lastPull transport.PullRequest
	lastAck  transport.AckRequest
}

func (s *workerTransportServer) HandlePull(ctx context.Context, req transport.PullRequest) (transport.PullResponse, error) {
	s.lastPull = req
	return transport.PullResponse{
		ChannelKey:  req.ChannelKey,
		Epoch:       req.Epoch,
		LeaderEpoch: req.LeaderEpoch,
		LeaderHW:    req.NextOffset,
		LeaderLEO:   req.NextOffset,
		Records:     []ch.Record{{ID: 99, Index: req.NextOffset, Payload: []byte("pulled"), SizeBytes: len("pulled")}},
	}, nil
}

func (s *workerTransportServer) HandleAck(ctx context.Context, req transport.AckRequest) error {
	s.lastAck = req
	return nil
}

func TestTaskRunStoreAppendUsesStoreDeps(t *testing.T) {
	key := ch.ChannelKey("1:a")
	id := ch.ChannelID{ID: "a", Type: 1}
	factory := store.NewMemoryFactory()
	deps := Deps{Stores: factory}
	fence := ch.Fence{ChannelKey: key, Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 10}

	res := Task{
		Kind:  TaskStoreAppend,
		Fence: fence,
		StoreAppend: &StoreAppendTask{
			ChannelID: id,
			Records: []ch.Record{{ID: 1, Payload: []byte("a"), SizeBytes: 1}},
			Sync: true,
		},
	}.Run(context.Background(), deps)

	require.NoError(t, res.Err)
	require.Equal(t, TaskStoreAppend, res.Kind)
	require.Equal(t, fence, res.Fence)
	require.NotNil(t, res.StoreAppend)
	require.Equal(t, uint64(1), res.StoreAppend.BaseOffset)
	require.Equal(t, uint64(1), res.StoreAppend.LastOffset)
}

func TestTaskRunStoreReadCommittedUsesStoreDeps(t *testing.T) {
	key := ch.ChannelKey("1:a")
	id := ch.ChannelID{ID: "a", Type: 1}
	factory := store.NewMemoryFactory()
	cs, err := factory.ChannelStore(key, id)
	require.NoError(t, err)
	_, err = cs.AppendLeader(context.Background(), store.AppendLeaderRequest{Records: []ch.Record{{ID: 1, Payload: []byte("a"), SizeBytes: 1}}})
	require.NoError(t, err)
	require.NoError(t, cs.StoreCheckpoint(context.Background(), ch.Checkpoint{HW: 1}))
	fence := ch.Fence{ChannelKey: key, Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 12}

	res := Task{
		Kind:  TaskStoreReadCommitted,
		Fence: fence,
		StoreReadCommitted: &StoreReadCommittedTask{
			ChannelID: id,
			FromSeq:   1,
			MaxSeq:    1,
			Limit:     10,
			MaxBytes:  1024,
		},
	}.Run(context.Background(), Deps{Stores: factory})

	require.NoError(t, res.Err)
	require.Equal(t, TaskStoreReadCommitted, res.Kind)
	require.NotNil(t, res.StoreReadCommitted)
	require.Len(t, res.StoreReadCommitted.Messages, 1)
	require.Equal(t, uint64(2), res.StoreReadCommitted.NextSeq)
}

func TestTaskRunStoreReadLogUsesStoreDeps(t *testing.T) {
	key := ch.ChannelKey("1:a")
	id := ch.ChannelID{ID: "a", Type: 1}
	factory := store.NewMemoryFactory()
	cs, err := factory.ChannelStore(key, id)
	require.NoError(t, err)
	_, err = cs.AppendLeader(context.Background(), store.AppendLeaderRequest{Records: []ch.Record{{ID: 1, Payload: []byte("a"), SizeBytes: 1}}})
	require.NoError(t, err)
	fence := ch.Fence{ChannelKey: key, Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 13}

	res := Task{
		Kind:  TaskStoreReadLog,
		Fence: fence,
		StoreReadLog: &StoreReadLogTask{
			ChannelID:  id,
			FromOffset: 1,
			MaxOffset:  1,
			MaxBytes:   1024,
		},
	}.Run(context.Background(), Deps{Stores: factory})

	require.NoError(t, res.Err)
	require.Equal(t, TaskStoreReadLog, res.Kind)
	require.NotNil(t, res.StoreReadLog)
	require.Len(t, res.StoreReadLog.Records, 1)
	require.Equal(t, uint64(1), res.StoreReadLog.Records[0].Index)
}

func TestTaskRunStoreApplyUsesStoreDeps(t *testing.T) {
	key := ch.ChannelKey("1:a")
	id := ch.ChannelID{ID: "a", Type: 1}
	factory := store.NewMemoryFactory()
	fence := ch.Fence{ChannelKey: key, Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 14}

	res := Task{
		Kind:  TaskStoreApply,
		Fence: fence,
		StoreApply: &StoreApplyTask{
			ChannelID: id,
			Records:   []ch.Record{{ID: 1, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
			LeaderHW:  1,
		},
	}.Run(context.Background(), Deps{Stores: factory})

	require.NoError(t, res.Err)
	require.Equal(t, TaskStoreApply, res.Kind)
	require.NotNil(t, res.StoreApply)
	require.Equal(t, uint64(1), res.StoreApply.LEO)
}

func TestTaskRunRPCPullAndAckUseTransportDeps(t *testing.T) {
	net := transport.NewLocalNetwork()
	server := &workerTransportServer{}
	net.Register(2, server)
	deps := Deps{Transport: net.Client()}
	fence := ch.Fence{ChannelKey: "1:a", Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 11}

	pull := Task{Kind: TaskRPCPull, Fence: fence, RPCPull: &RPCPullTask{Node: 2, Request: transport.PullRequest{ChannelKey: "1:a", NextOffset: 7}}}.Run(context.Background(), deps)
	require.NoError(t, pull.Err)
	require.NotNil(t, pull.RPCPull)
	require.Equal(t, uint64(7), server.lastPull.NextOffset)

	ack := Task{Kind: TaskRPCAck, Fence: fence, RPCAck: &RPCAckTask{Node: 2, Request: transport.AckRequest{ChannelKey: "1:a", MatchOffset: 9}}}.Run(context.Background(), deps)
	require.NoError(t, ack.Err)
	require.NotNil(t, ack.RPCAck)
	require.Equal(t, uint64(9), server.lastAck.MatchOffset)
}
```

Also add tests for missing deps:

```go
func TestTaskRunMissingDepsReturnsInvalidConfig(t *testing.T) {
	res := Task{Kind: TaskStoreReadCommitted}.Run(context.Background(), Deps{})
	require.ErrorIs(t, res.Err, ch.ErrInvalidConfig)
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/worker -run 'TestTaskRun' -count=1
```

Expected: FAIL because `Deps`, typed task fields, typed result fields, and the new `Run(ctx, deps)` signature do not exist yet.

- [ ] **Step 3: Add worker deps and typed payloads**

Implement `pkg/channelv2/worker/deps.go`:

```go
package worker

import (
	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
)

// Deps are blocking dependencies used by worker tasks.
type Deps struct {
	LocalNode ch.NodeID
	Stores    store.Factory
	Transport transport.Client
}
```

Modify `task.go` to use typed payloads. Keep `TaskFunc` only for low-level pool tests:

```go
// Task describes blocking work submitted to a bounded pool.
type Task struct {
	Kind  TaskKind
	Fence ch.Fence

	StoreAppend        *StoreAppendTask
	StoreReadCommitted *StoreReadCommittedTask
	StoreReadLog       *StoreReadLogTask
	StoreApply         *StoreApplyTask
	RPCPull            *RPCPullTask
	RPCAck             *RPCAckTask

	RunFunc func(context.Context) Result
}

// StoreAppendTask asks a worker to durably append leader records.
type StoreAppendTask struct {
	ChannelID ch.ChannelID
	Records   []ch.Record
	Sync      bool
}

// StoreReadCommittedTask asks a worker to read committed messages.
type StoreReadCommittedTask struct {
	ChannelID ch.ChannelID
	FromSeq   uint64
	MaxSeq    uint64
	Limit     int
	MaxBytes  int
}

// StoreReadLogTask asks a worker to read raw records for replication.
type StoreReadLogTask struct {
	ChannelID  ch.ChannelID
	FromOffset uint64
	MaxOffset  uint64
	MaxBytes   int
}

// StoreApplyTask asks a worker to persist follower records.
type StoreApplyTask struct {
	ChannelID ch.ChannelID
	Records   []ch.Record
	LeaderHW  uint64
}

// RPCPullTask asks a remote leader for records.
type RPCPullTask struct {
	Node    ch.NodeID
	Request transport.PullRequest
}

// RPCAckTask reports follower progress to the remote leader.
type RPCAckTask struct {
	Node    ch.NodeID
	Request transport.AckRequest
}
```

Modify `result.go`:

```go
// Result is the common completion envelope for worker tasks.
type Result struct {
	Kind  TaskKind
	Fence ch.Fence
	Err   error

	StoreAppend        *StoreAppendResult
	StoreReadCommitted *StoreReadCommittedResult
	StoreReadLog       *StoreReadLogResult
	StoreApply         *StoreApplyResult
	RPCPull            *RPCPullResult
	RPCAck             *RPCAckResult
	Value              any // used only by TaskFunc tests
}
```

Add result payloads mirroring store and transport result values. Use English comments for exported types and fields.

- [ ] **Step 4: Implement typed task execution**

Update `Task.Run` to take `Deps`:

```go
func (t Task) Run(ctx context.Context, deps Deps) Result {
	if ctx == nil {
		ctx = context.Background()
	}
	switch t.Kind {
	case TaskFunc:
		if t.RunFunc == nil {
			return Result{Kind: t.Kind, Fence: t.Fence, Err: ch.ErrInvalidConfig}
		}
		res := t.RunFunc(ctx)
		res.Kind = t.Kind
		if res.Fence == (ch.Fence{}) {
			res.Fence = t.Fence
		}
		return res
	case TaskStoreAppend:
		return runStoreAppend(ctx, deps, t)
	case TaskStoreReadCommitted:
		return runStoreReadCommitted(ctx, deps, t)
	case TaskStoreReadLog:
		return runStoreReadLog(ctx, deps, t)
	case TaskStoreApply:
		return runStoreApply(ctx, deps, t)
	case TaskRPCPull:
		return runRPCPull(ctx, deps, t)
	case TaskRPCAck:
		return runRPCAck(ctx, deps, t)
	default:
		return Result{Kind: t.Kind, Fence: t.Fence, Err: ch.ErrInvalidConfig}
	}
}
```

For store tasks, call `deps.Stores.ChannelStore(t.Fence.ChannelKey, payload.ChannelID)` inside the worker. This keeps store opening out of reactor hot paths for new async effects, while `ApplyMeta` may still load initial state synchronously for this phase.

- [ ] **Step 5: Add pool group and depth helpers**

Create `pkg/channelv2/worker/pools.go`:

```go
// Pools owns all blocking worker pools used by channelv2 reactors.
type Pools struct {
	StoreAppend *Pool
	StoreRead   *Pool
	StoreApply  *Pool
	RPC         *Pool
}

// PoolsConfig defines worker and queue limits for each blocking class.
type PoolsConfig struct {
	StoreAppend PoolConfig
	StoreRead   PoolConfig
	StoreApply  PoolConfig
	RPC         PoolConfig
}
```

Add:

```go
func NewPools(cfg PoolsConfig, deps Deps, sink CompletionSink) (*Pools, error)
func (p *Pools) Submit(ctx context.Context, task Task) error
func (p *Pools) Close() error
func (p *Pools) QueueDepth(kind TaskKind) int
```

Routing must be:

```text
TaskStoreAppend -> StoreAppend
TaskStoreReadCommitted -> StoreRead
TaskStoreReadLog -> StoreRead
TaskStoreApply -> StoreApply
TaskRPCPull -> RPC
TaskRPCAck -> RPC
```

Update `Pool` to store `deps Deps` and call `task.Run(context.Background(), p.deps)` in `run`.

- [ ] **Step 6: Run worker tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/worker -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit worker task foundation**

Run:

```bash
git add pkg/channelv2/worker
git commit -m "feat: add channelv2 typed worker tasks"
```

## Task 2: Group Completion Routing And Tick Entry Point

**Files:**
- Modify: `pkg/channelv2/reactor/event.go`
- Modify: `pkg/channelv2/reactor/group.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Create: `pkg/channelv2/reactor/test_helpers_test.go`
- Create: `pkg/channelv2/reactor/group_test.go`

- [ ] **Step 1: Write failing group completion tests**

Create `pkg/channelv2/reactor/test_helpers_test.go` with helpers reused by later reactor tests:

```go
package reactor

import (
	"context"
	"strconv"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

func testMeta(id string, local ch.NodeID, leader ch.NodeID) ch.Meta {
	channelID := ch.ChannelID{ID: id, Type: 1}
	replicas := []ch.NodeID{leader}
	if local != leader {
		replicas = append(replicas, local)
	}
	return ch.Meta{
		Key:         ch.ChannelKeyForID(channelID),
		ID:          channelID,
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      leader,
		Replicas:    replicas,
		ISR:         replicas,
		MinISR:      len(replicas),
		Status:      ch.StatusActive,
	}
}

func awaitSubmit(g *Group, key ch.ChannelKey, event Event) error {
	future, err := g.Submit(context.Background(), key, event)
	if err != nil {
		return err
	}
	_, err = future.Await(context.Background())
	return err
}

func appendEvent(meta ch.Meta, id uint64, payload string) Event {
	return Event{
		Kind: EventAppend,
		Key:  meta.Key,
		OpID: ch.OpID(id),
		Append: ch.AppendBatchRequest{
			ChannelID:  meta.ID,
			CommitMode: ch.CommitModeQuorum,
			Messages: []ch.Message{{
				MessageID:   id,
				ClientMsgNo: "client-" + strconv.FormatUint(id, 10),
				Payload:     []byte(payload),
			}},
		},
	}
}

func requireFuturePending(t *testing.T, future *Future) {
	t.Helper()
	select {
	case result := <-future.ch:
		t.Fatalf("future completed early: err=%v result=%+v", result.Err, result)
	default:
	}
}
```

Create `pkg/channelv2/reactor/group_test.go`:

```go
package reactor

import (
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
	"github.com/stretchr/testify/require"
)

func TestGroupCompleteRoutesWorkerResultToOwningReactor(t *testing.T) {
	router, err := NewRouter(1)
	require.NoError(t, err)
	reactor := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: store.NewMemoryFactory(), MailboxSize: 8})
	g := &Group{router: router, reactors: []*Reactor{reactor}}
	fence := ch.Fence{ChannelKey: "1:a", Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 99}

	g.Complete(worker.Result{Kind: worker.TaskFunc, Fence: fence, Value: "probe"})

	events := reactor.mailbox.Drain(1)
	require.Len(t, events, 1)
	require.Equal(t, EventWorkerResult, events[0].Kind)
	require.Equal(t, ch.ChannelKey("1:a"), events[0].Key)
	require.Equal(t, fence, events[0].Worker.Fence)
}

func TestEventWorkerResultPriorityBeatsNormalAppendPressure(t *testing.T) {
	mailbox := NewMailbox(MailboxConfig{HighSize: 1, NormalSize: 1, LowSize: 1})
	require.NoError(t, mailbox.Submit(PriorityNormal, Event{Kind: EventAppend}))
	require.NoError(t, mailbox.Submit(PriorityHigh, Event{Kind: EventWorkerResult}))
	events := mailbox.Drain(2)
	require.Equal(t, EventWorkerResult, events[0].Kind)
}
```

This test constructs an unstarted reactor intentionally so the mailbox can be inspected without a production-only test hook.

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/reactor -run 'TestGroupComplete|TestEventWorkerResultPriority' -count=1
```

Expected: FAIL because `Group.Complete` is not implemented and `Reactor.handle` ignores `EventWorkerResult`.

- [ ] **Step 3: Wire worker pools into group config**

Extend `reactor.Config`:

```go
// Config wires a group of channel-keyed reactors.
type Config struct {
	LocalNode ch.NodeID
	ReactorCount int
	MailboxSize int
	Store store.Factory
	Transport transport.Client
	WorkerPools worker.PoolsConfig
	Observer Observer
}
```

`NewGroup` should create `worker.NewPools` with `Deps{LocalNode: cfg.LocalNode, Stores: cfg.Store, Transport: cfg.Transport}` and `sink=g`. Use sane defaults when pool configs are zero:

```text
store append: workers=max(1, reactorCount), queue=max(64, mailboxSize)
store read:   workers=max(1, reactorCount), queue=max(64, mailboxSize)
store apply:  workers=max(1, reactorCount), queue=max(64, mailboxSize)
rpc:          workers=max(1, reactorCount), queue=max(64, mailboxSize)
```

- [ ] **Step 4: Implement completion routing and group tick**

Add to `group.go`:

```go
// Complete routes worker completions back to the owning reactor.
func (g *Group) Complete(result worker.Result) {
	if g == nil || g.closed.Load() || result.Fence.ChannelKey == "" {
		return
	}
	reactor := g.reactors[g.router.PickIndex(result.Fence.ChannelKey)]
	_ = reactor.Submit(PriorityHigh, Event{Kind: EventWorkerResult, Key: result.Fence.ChannelKey, Worker: result})
}

// Tick submits a low-priority tick to each reactor.
func (g *Group) Tick(ctx context.Context) error {
	if g == nil || g.closed.Load() {
		return ch.ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	for _, r := range g.reactors {
		if err := r.Submit(PriorityLow, Event{Kind: EventTick, TickNow: now}); err != nil && !errors.Is(err, ch.ErrBackpressured) {
			return err
		}
	}
	return ctx.Err()
}
```

Update `Close` to stop reactors first, then close pools, or to set `closed` before closing pools so late completions are ignored.

- [ ] **Step 5: Add reactor handling skeletons**

In `reactor.handle`, add cases for `EventWorkerResult`, `EventTick`, and `EventClose`. For this task, `handleWorkerResult` may ignore unknown results and `handleTick` may be empty; later tasks fill them in.

- [ ] **Step 6: Run targeted tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/reactor -run 'TestGroupComplete|TestEventWorkerResultPriority' -count=1
```

Expected: PASS. Service-owned replication is intentionally left unchanged until Task 6 so existing testkit replication tests remain green between commits.

- [ ] **Step 7: Commit completion routing**

Run:

```bash
git add pkg/channelv2/reactor
git commit -m "feat: route channelv2 worker completions"
```

## Task 3: Async Fetch Path

**Files:**
- Modify: `pkg/channelv2/reactor/reactor.go`
- Create: `pkg/channelv2/reactor/effect.go`
- Create: `pkg/channelv2/reactor/async_fetch_test.go`
- Modify: `pkg/channelv2/service/fetch.go`
- Modify: `pkg/channelv2/machine/fetch.go` - keep fence validation and add op-id regression tests if implementation exposes a gap.
- Modify: `pkg/channelv2/machine/fetch_test.go`

- [ ] **Step 1: Write failing async fetch tests**

Create `pkg/channelv2/reactor/async_fetch_test.go` with a slow read store. The test should prove a fetch read does not block the reactor from processing a high-priority metadata update or worker result.

Use this test file content:

```go
package reactor

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
	"github.com/stretchr/testify/require"
)

func TestFetchSubmitsStoreReadWithoutBlockingReactor(t *testing.T) {
	factory := newBlockingReadFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	defer g.Close()

	meta := testMeta("a", 1, 1)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	appendFuture, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 1, "seed"))
	require.NoError(t, err)
	_, err = appendFuture.Await(context.Background())
	require.NoError(t, err)

	fetchFuture, err := g.Submit(context.Background(), meta.Key, Event{
		Kind:  EventFetch,
		Key:   meta.Key,
		Fetch: ch.FetchRequest{ChannelID: meta.ID, FromSeq: 1, Limit: 10, MaxBytes: 1024},
		OpID:  7,
	})
	require.NoError(t, err)
	require.Eventually(t, factory.ReadStarted, time.Second, time.Millisecond)

	// If fetch blocks the reactor, this high-priority apply-meta cannot complete.
	metaFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta})
	require.NoError(t, err)
	_, err = metaFuture.Await(context.Background())
	require.NoError(t, err)

	factory.UnblockReads()
	_, err = fetchFuture.Await(context.Background())
	require.NoError(t, err)
}

func TestFetchStoreReadPoolFullFailsFuture(t *testing.T) {
	factory := newBlockingReadFactory()
	g, err := NewGroup(Config{
		LocalNode: 1,
		ReactorCount: 1,
		MailboxSize: 16,
		Store: factory,
		WorkerPools: worker.PoolsConfig{
			StoreRead: worker.PoolConfig{Name: "store-read", Workers: 1, QueueSize: 1},
		},
	})
	require.NoError(t, err)
	defer g.Close()

	meta := testMeta("a", 1, 1)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	appendFuture, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 1, "seed"))
	require.NoError(t, err)
	_, err = appendFuture.Await(context.Background())
	require.NoError(t, err)

	first, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventFetch, Key: meta.Key, Fetch: ch.FetchRequest{ChannelID: meta.ID, FromSeq: 1, Limit: 10, MaxBytes: 1024}, OpID: 101})
	require.NoError(t, err)
	require.Eventually(t, factory.ReadStarted, time.Second, time.Millisecond)

	second, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventFetch, Key: meta.Key, Fetch: ch.FetchRequest{ChannelID: meta.ID, FromSeq: 1, Limit: 10, MaxBytes: 1024}, OpID: 102})
	require.NoError(t, err)
	requireFuturePending(t, second)

	third, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventFetch, Key: meta.Key, Fetch: ch.FetchRequest{ChannelID: meta.ID, FromSeq: 1, Limit: 10, MaxBytes: 1024}, OpID: 103})
	require.NoError(t, err)
	_, err = third.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrBackpressured)

	factory.UnblockReads()
	_, err = first.Await(context.Background())
	require.NoError(t, err)
	_, err = second.Await(context.Background())
	require.NoError(t, err)
}

func TestFetchMetadataChangeFailsPendingWaiter(t *testing.T) {
	factory := newBlockingReadFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	defer g.Close()

	meta := testMeta("a", 1, 1)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	appendFuture, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 1, "seed"))
	require.NoError(t, err)
	_, err = appendFuture.Await(context.Background())
	require.NoError(t, err)

	fetchFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventFetch, Key: meta.Key, Fetch: ch.FetchRequest{ChannelID: meta.ID, FromSeq: 1, Limit: 10, MaxBytes: 1024}, OpID: 201})
	require.NoError(t, err)
	require.Eventually(t, factory.ReadStarted, time.Second, time.Millisecond)

	meta.LeaderEpoch = 2
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	_, err = fetchFuture.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrStaleMeta)

	factory.UnblockReads()
}

type blockingReadFactory struct {
	base        *store.MemoryFactory
	readStarted chan struct{}
	unblock      chan struct{}
}

func newBlockingReadFactory() *blockingReadFactory {
	return &blockingReadFactory{base: store.NewMemoryFactory(), readStarted: make(chan struct{}, 8), unblock: make(chan struct{})}
}

func (f *blockingReadFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (store.ChannelStore, error) {
	base, err := f.base.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	return &blockingReadStore{ChannelStore: base, parent: f}, nil
}

func (f *blockingReadFactory) ReadStarted() bool {
	return len(f.readStarted) > 0
}

func (f *blockingReadFactory) UnblockReads() {
	select {
	case <-f.unblock:
	default:
		close(f.unblock)
	}
}

type blockingReadStore struct {
	store.ChannelStore
	parent *blockingReadFactory
}

func (s *blockingReadStore) ReadCommitted(ctx context.Context, req store.ReadCommittedRequest) (store.ReadCommittedResult, error) {
	select {
	case s.parent.readStarted <- struct{}{}:
	default:
	}
	select {
	case <-s.parent.unblock:
	case <-ctx.Done():
		return store.ReadCommittedResult{}, ctx.Err()
	}
	return s.ChannelStore.ReadCommitted(ctx, req)
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/reactor -run 'TestFetch' -count=1
```

Expected: FAIL because `handleFetch` still calls `ReadCommitted` synchronously or does not submit typed store read tasks.

- [ ] **Step 3: Give fetch requests unique op ids**

Modify `pkg/channelv2/service/fetch.go` to pass `OpID: c.group.NextOpID()` in the fetch event. Any direct reactor tests should also set op ids explicitly.

- [ ] **Step 4: Implement store read task submission**

In `handleFetch`:

1. Look up the runtime channel.
2. Call `rc.state.BuildFetch(machine.FetchCommand{OpID: event.OpID, FromSeq: event.Fetch.FromSeq, Limit: event.Fetch.Limit, MaxBytes: event.Fetch.MaxBytes})`.
3. If there is an immediate reply, complete the future immediately.
4. Store `event.Future` in `rc.waiters[event.OpID]` before submitting the worker task.
5. Convert `machine.ReadCommittedTask` to `worker.TaskStoreReadCommitted` and submit to `r.cfg.Pools` or a reactor `submitTask` helper.
6. If pool submission returns `ErrBackpressured`, remove the waiter and complete the future with `ErrBackpressured`.
7. On `ApplyMeta`, when generation, epoch, leader epoch, role, or status changes, fail all pending fetch waiters for that runtime channel with `ErrStaleMeta` before applying new metadata. This prevents stale read completions from leaking futures.

`effect.go` should hold helpers like:

```go
func (r *Reactor) submitStoreReadCommitted(ctx context.Context, rc *runtimeChannel, task machine.Task) error
func (r *Reactor) completeReplies(rc *runtimeChannel, replies []machine.Reply)
```

- [ ] **Step 5: Apply store read worker results inside reactor**

In `handleWorkerResult`, switch on `event.Worker.Kind`. For `worker.TaskStoreReadCommitted`:

```go
func (r *Reactor) handleStoreReadCommittedResult(rc *runtimeChannel, result worker.Result) {
	read := result.StoreReadCommitted
	decision := rc.state.ApplyReadCommitted(machine.ReadCommittedResult{
		Fence: result.Fence,
		Messages: read.Messages,
		NextSeq: read.NextSeq,
		Err: result.Err,
	})
	r.completeReplies(rc, decision.Replies)
}
```

If `result.StoreReadCommitted` is nil with nil error, treat it as `ErrInvalidConfig`. If the fence is stale and a fetch waiter still exists for `result.Fence.OpID`, remove that waiter and complete it with `ErrStaleMeta`; this is a safety net for stale completions not already failed by `ApplyMeta`.

Add this regression test to `pkg/channelv2/machine/fetch_test.go`:

```go
func TestReadCommittedIgnoresStaleFence(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	state.HW = 1
	decision := state.BuildFetch(FetchCommand{OpID: 7, FromSeq: 1, Limit: 10, MaxBytes: 1024})
	require.Len(t, decision.Tasks, 1)
	stale := decision.Tasks[0].Fence
	stale.LeaderEpoch++

	applied := state.ApplyReadCommitted(ReadCommittedResult{
		Fence:    stale,
		Messages: []ch.Message{{MessageID: 10, MessageSeq: 1}},
		NextSeq:  2,
	})

	require.Empty(t, applied.Replies)
}
```

- [ ] **Step 6: Run async fetch tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/reactor -run 'TestFetch' -count=1
```

Expected: PASS.

- [ ] **Step 7: Run service smoke test**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/service -run TestSingleNodeAppendFetchCommitted -count=1
```

Expected: PASS with async fetch.

- [ ] **Step 8: Commit async fetch**

Run:

```bash
git add pkg/channelv2/reactor pkg/channelv2/service/fetch.go pkg/channelv2/machine/fetch.go pkg/channelv2/machine/fetch_test.go
git commit -m "feat: make channelv2 fetch asynchronous"
```

## Task 4: Append Queue And Batch Machine State

**Files:**
- Create: `pkg/channelv2/reactor/append_queue.go`
- Create: `pkg/channelv2/reactor/append_queue_test.go`
- Modify: `pkg/channelv2/reactor/backpressure.go`
- Modify: `pkg/channelv2/machine/channel.go`
- Modify: `pkg/channelv2/machine/append.go`
- Modify: `pkg/channelv2/machine/append_test.go`

- [ ] **Step 1: Write failing append queue unit tests**

Create `pkg/channelv2/reactor/append_queue_test.go`:

```go
func TestAppendQueueFlushesByMaxRecords(t *testing.T) {
	q := newAppendQueue(appendQueueConfig{MaxRecords: 3, MaxBytes: 1024, MaxWait: time.Second, MaxPending: 10, MaxPendingBytes: 1024})
	now := time.Unix(1, 0)
	require.NoError(t, q.push(appendRequest{opID: 1, enqueuedAt: now, records: []ch.Record{{SizeBytes: 1}}}))
	require.False(t, q.shouldFlush(now))
	require.NoError(t, q.push(appendRequest{opID: 2, enqueuedAt: now, records: []ch.Record{{SizeBytes: 1}, {SizeBytes: 1}}}))
	require.True(t, q.shouldFlush(now))
}

func TestAppendQueueFlushesByMaxWait(t *testing.T) {
	q := newAppendQueue(appendQueueConfig{MaxRecords: 10, MaxBytes: 1024, MaxWait: 5 * time.Millisecond, MaxPending: 10, MaxPendingBytes: 1024})
	now := time.Unix(1, 0)
	require.NoError(t, q.push(appendRequest{opID: 1, enqueuedAt: now, records: []ch.Record{{SizeBytes: 1}}}))
	require.False(t, q.shouldFlush(now.Add(4*time.Millisecond)))
	require.True(t, q.shouldFlush(now.Add(5*time.Millisecond)))
}

func TestAppendQueueRejectsPendingLimits(t *testing.T) {
	q := newAppendQueue(appendQueueConfig{MaxRecords: 10, MaxBytes: 1024, MaxWait: time.Second, MaxPending: 1, MaxPendingBytes: 1})
	require.NoError(t, q.push(appendRequest{opID: 1, records: []ch.Record{{SizeBytes: 1}}}))
	err := q.push(appendRequest{opID: 2, records: []ch.Record{{SizeBytes: 1}}})
	require.ErrorIs(t, err, ch.ErrBackpressured)
}
```

- [ ] **Step 2: Run append queue tests and verify failure**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/reactor -run 'TestAppendQueue' -count=1
```

Expected: FAIL because `append_queue.go` does not exist.

- [ ] **Step 3: Implement append queue primitives**

`append_queue.go` should define unexported focused types:

```go
type appendQueueConfig struct {
	MaxRecords int
	MaxBytes int
	MaxWait time.Duration
	MaxPending int
	MaxPendingBytes int
}

type appendQueue struct {
	pending []appendRequest
	records int
	bytes int
	flushDue time.Time
	storeBlocked bool
}

type appendRequest struct {
	opID ch.OpID
	req ch.AppendBatchRequest
	future *Future
	enqueuedAt time.Time
	records []ch.Record
	commitMode ch.CommitMode
}

type appendBatch struct {
	batchOpID ch.OpID
	fence ch.Fence
	requests []appendRequest
	records []ch.Record
}
```

Methods:

```go
func newAppendQueue(cfg appendQueueConfig) appendQueue
func (q *appendQueue) push(req appendRequest) error
func (q *appendQueue) shouldFlush(now time.Time) bool
func (q *appendQueue) popBatch(batchOpID ch.OpID, state *machine.ChannelState) appendBatch
func (q *appendQueue) restoreFront(batch appendBatch)
func (q *appendQueue) remove(opID ch.OpID) (*appendRequest, bool)
func (q *appendQueue) failAll(err error)
```

- [ ] **Step 4: Write failing machine batch append tests**

Extend `pkg/channelv2/machine/append_test.go`:

```go
func TestAppendStoredCompletesMultipleWaitersFromOneBatch(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	cmd := AppendBatchCommand{
		BatchOpID: 100,
		Waiters: []AppendBatchWaiter{
			{OpID: 1, CommitMode: ch.CommitModeQuorum, Records: []ch.Record{{ID: 10, Payload: []byte("a"), SizeBytes: 1}}},
			{OpID: 2, CommitMode: ch.CommitModeQuorum, Records: []ch.Record{{ID: 20, Payload: []byte("b"), SizeBytes: 1}}},
		},
	}
	decision := state.ProposeAppendBatch(cmd)
	require.Len(t, decision.Tasks, 1)
	decision = state.ApplyAppendStored(AppendStoredResult{Fence: decision.Tasks[0].Fence, BaseOffset: 1, LastOffset: 2})
	require.Len(t, decision.Replies, 2)
	require.Equal(t, uint64(1), decision.Replies[0].AppendItems[0].MessageSeq)
	require.Equal(t, uint64(2), decision.Replies[1].AppendItems[0].MessageSeq)
}

func TestAppendStoredIgnoresStaleBatchFence(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	decision := state.ProposeAppendBatch(AppendBatchCommand{BatchOpID: 100, Waiters: []AppendBatchWaiter{{OpID: 1, Records: []ch.Record{{ID: 10, SizeBytes: 1}}}}})
	stale := decision.Tasks[0].Fence
	stale.LeaderEpoch++
	applied := state.ApplyAppendStored(AppendStoredResult{Fence: stale, BaseOffset: 1, LastOffset: 1})
	require.Empty(t, applied.Replies)
	require.NotNil(t, state.InflightAppend)
}
```

- [ ] **Step 5: Run machine tests and verify failure**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/machine -run 'TestAppendStoredCompletesMultipleWaitersFromOneBatch|TestAppendStoredIgnoresStaleBatchFence' -count=1
```

Expected: FAIL because `AppendBatchCommand` and batch waiter state do not exist.

- [ ] **Step 6: Implement batch append in machine**

Update `machine/channel.go`:

```go
// AppendBatchWaiter describes one client append request inside a durable batch.
type AppendBatchWaiter struct {
	OpID ch.OpID
	CommitMode ch.CommitMode
	Records []ch.Record
}

// AppendBatchCommand asks the leader to append multiple client requests as one durable batch.
type AppendBatchCommand struct {
	BatchOpID ch.OpID
	Waiters []AppendBatchWaiter
}

// AppendOp is the currently durable in-flight append batch for one channel.
type AppendOp struct {
	OpID ch.OpID
	Records []ch.Record
	WaiterOpIDs []ch.OpID
}
```

Add `ProposeAppendBatch`. Keep `ProposeAppend` as a thin wrapper that calls batch with one waiter so existing tests and code keep working.

`ApplyAppendStored` must:

1. Validate `res.Fence` against `InflightAppend.OpID`.
2. Assign stored offsets across all records in batch order.
3. Split assigned records back into each `AppendWaiter` by op id.
4. Set each waiter target to the last offset of that waiter's record range.
5. Advance local LEO and HW.
6. Complete all waiters covered by HW or local commit.

Ensure deterministic reply order by `InflightAppend.WaiterOpIDs`, not map iteration order.

- [ ] **Step 7: Run machine and queue tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/machine ./pkg/channelv2/reactor -run 'TestAppend' -count=1
```

Expected: PASS for append queue and machine append tests. Reactor append tests may still be synchronous until Task 5, but should not regress.

- [ ] **Step 8: Commit append queue and machine batch state**

Run:

```bash
git add pkg/channelv2/reactor/append_queue.go pkg/channelv2/reactor/append_queue_test.go pkg/channelv2/reactor/backpressure.go pkg/channelv2/machine
git commit -m "feat: add channelv2 append batch state"
```

## Task 5: Async Append Flush, Backpressure, And Cancellation

**Files:**
- Modify: `pkg/channelv2/channel.go`
- Modify: `pkg/channelv2/reactor/event.go`
- Modify: `pkg/channelv2/reactor/group.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/reactor/effect.go`
- Create: `pkg/channelv2/reactor/append_batch_test.go`
- Modify: `pkg/channelv2/service/append.go`
- Modify: `pkg/channelv2/service/service.go`
- Modify: `pkg/channelv2/service/service_test.go`

- [ ] **Step 1: Write failing async append batching tests**

Create `pkg/channelv2/reactor/append_batch_test.go`:

```go
package reactor

import (
	"context"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
	"github.com/stretchr/testify/require"
)

func TestAppendEventsBatchByMaxRecords(t *testing.T) {
	factory := newCountingStoreFactory()
	g, err := NewGroup(Config{
		LocalNode: 1,
		ReactorCount: 1,
		MailboxSize: 32,
		Store: factory,
		AppendBatchMaxRecords: 2,
		AppendBatchMaxBytes: 1024,
		AppendBatchMaxWait: time.Second,
	})
	require.NoError(t, err)
	defer g.Close()

	meta := testMeta("a", 1, 1)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	f1, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 1, "a"))
	require.NoError(t, err)
	f2, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 2, "b"))
	require.NoError(t, err)

	r1, err := f1.Await(context.Background())
	require.NoError(t, err)
	r2, err := f2.Await(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), r1.AppendBatch.Items[0].MessageSeq)
	require.Equal(t, uint64(2), r2.AppendBatch.Items[0].MessageSeq)
	require.Equal(t, 1, factory.AppendCalls(meta.Key))
	require.Equal(t, 2, factory.LastAppendRecordCount(meta.Key))
}

func TestAppendEventsBatchByMaxWaitTick(t *testing.T) {
	factory := newCountingStoreFactory()
	g, err := NewGroup(Config{
		LocalNode: 1,
		ReactorCount: 1,
		MailboxSize: 32,
		Store: factory,
		AppendBatchMaxRecords: 10,
		AppendBatchMaxBytes: 1024,
		AppendBatchMaxWait: time.Nanosecond,
	})
	require.NoError(t, err)
	defer g.Close()

	meta := testMeta("a", 1, 1)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	future, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 1, "a"))
	require.NoError(t, err)
	requireFuturePending(t, future)
	require.NoError(t, g.Tick(context.Background()))

	result, err := future.Await(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), result.AppendBatch.Items[0].MessageSeq)
	require.Equal(t, 1, factory.AppendCalls(meta.Key))
}

func TestMetadataChangeFailsInflightAppendWaiter(t *testing.T) {
	factory := newCountingStoreFactory()
	factory.BlockAppends()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 32, Store: factory, AppendBatchMaxRecords: 1})
	require.NoError(t, err)
	defer g.Close()

	meta := testMeta("a", 1, 1)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	future, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 1, "a"))
	require.NoError(t, err)
	require.Eventually(t, factory.AppendStarted, time.Second, time.Millisecond)

	meta.LeaderEpoch = 2
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	_, err = future.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrStaleMeta)

	factory.UnblockAppends()
}
```

- [ ] **Step 2: Write failing backpressure and cancellation tests**

Add these tests to the same file:

```go
func TestAppendPoolFullKeepsAcceptedRequestPendingAndRetriesOnTick(t *testing.T) {
	factory := newCountingStoreFactory()
	factory.BlockAppends()
	g, err := NewGroup(Config{
		LocalNode: 1,
		ReactorCount: 1,
		MailboxSize: 32,
		Store: factory,
		AppendBatchMaxRecords: 1,
		AppendStoreRetryBackoff: time.Millisecond,
		WorkerPools: worker.PoolsConfig{
			StoreAppend: worker.PoolConfig{Name: "store-append", Workers: 1, QueueSize: 1},
		},
	})
	require.NoError(t, err)
	defer g.Close()

	meta := testMeta("a", 1, 1)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	first, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 1, "a"))
	require.NoError(t, err)
	require.Eventually(t, factory.AppendStarted, time.Second, time.Millisecond)
	second, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 2, "b"))
	require.NoError(t, err)
	third, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 3, "c"))
	require.NoError(t, err)
	requireFuturePending(t, third)

	factory.UnblockAppends()
	require.NoError(t, g.Tick(context.Background()))
	_, err = first.Await(context.Background())
	require.NoError(t, err)
	_, err = second.Await(context.Background())
	require.NoError(t, err)
	_, err = third.Await(context.Background())
	require.NoError(t, err)
}

func TestAppendContextCancelRemovesAcceptedWaiter(t *testing.T) {
	factory := newCountingStoreFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 32, Store: factory, AppendBatchMaxRecords: 10, AppendBatchMaxWait: time.Hour})
	require.NoError(t, err)
	defer g.Close()

	meta := testMeta("a", 1, 1)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	ctx, cancel := context.WithCancel(context.Background())
	event := appendEvent(meta, 1, "a")
	event.Context = ctx
	future, err := g.Submit(ctx, meta.Key, event)
	require.NoError(t, err)
	requireFuturePending(t, future)
	cancel()
	cancelFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventCancelWaiter, Key: meta.Key, CancelOp: 1, CancelErr: context.Canceled})
	require.NoError(t, err)
	_, err = cancelFuture.Await(context.Background())
	require.NoError(t, err)
	_, err = future.Await(context.Background())
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 0, factory.AppendCalls(meta.Key))
}

type countingStoreFactory struct {
	base          *store.MemoryFactory
	mu            sync.Mutex
	appendCalls   map[ch.ChannelKey]int
	lastRecords   map[ch.ChannelKey]int
	blockAppends  bool
	appendStarted chan struct{}
	unblockAppend chan struct{}
}

func newCountingStoreFactory() *countingStoreFactory {
	return &countingStoreFactory{
		base:          store.NewMemoryFactory(),
		appendCalls:   make(map[ch.ChannelKey]int),
		lastRecords:   make(map[ch.ChannelKey]int),
		appendStarted: make(chan struct{}, 16),
		unblockAppend: make(chan struct{}),
	}
}

func (f *countingStoreFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (store.ChannelStore, error) {
	base, err := f.base.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	return &countingStore{ChannelStore: base, key: key, parent: f}, nil
}

func (f *countingStoreFactory) AppendCalls(key ch.ChannelKey) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.appendCalls[key]
}

func (f *countingStoreFactory) LastAppendRecordCount(key ch.ChannelKey) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastRecords[key]
}

func (f *countingStoreFactory) AppendStarted() bool {
	return len(f.appendStarted) > 0
}

func (f *countingStoreFactory) BlockAppends() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blockAppends = true
	f.unblockAppend = make(chan struct{})
}

func (f *countingStoreFactory) UnblockAppends() {
	f.mu.Lock()
	ch := f.unblockAppend
	f.blockAppends = false
	f.mu.Unlock()
	select {
	case <-ch:
	default:
		close(ch)
	}
}

type countingStore struct {
	store.ChannelStore
	key    ch.ChannelKey
	parent *countingStoreFactory
}

func (s *countingStore) AppendLeader(ctx context.Context, req store.AppendLeaderRequest) (store.AppendLeaderResult, error) {
	s.parent.mu.Lock()
	blocked := s.parent.blockAppends
	ch := s.parent.unblockAppend
	s.parent.mu.Unlock()
	if blocked {
		select {
		case s.parent.appendStarted <- struct{}{}:
		default:
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return store.AppendLeaderResult{}, ctx.Err()
		}
	}
	result, err := s.ChannelStore.AppendLeader(ctx, req)
	s.parent.mu.Lock()
	s.parent.appendCalls[s.key]++
	s.parent.lastRecords[s.key] = len(req.Records)
	s.parent.mu.Unlock()
	return result, err
}
```

- [ ] **Step 3: Run tests and verify failure**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/reactor -run 'TestAppendEvents|TestMetadataChangeFailsInflightAppendWaiter|TestAppendPoolFull|TestAppendContextCancel' -count=1
```

Expected: FAIL because reactor append still performs one synchronous store append per request and has no queue/cancellation path.

- [ ] **Step 4: Add append config defaults**

Extend `reactor.Config` or `Limits` with fields:

```go
AppendBatchMaxRecords int
AppendBatchMaxBytes int
AppendBatchMaxWait time.Duration
AppendQueueMaxRequests int
AppendQueueMaxBytes int
AppendStoreRetryBackoff time.Duration
```

Default values should be small enough for tests and safe for smoke benchmarks:

```text
AppendBatchMaxRecords: 128
AppendBatchMaxBytes:   256 KiB
AppendBatchMaxWait:    1 ms
AppendQueueMaxRequests: mailboxSize or 1024
AppendQueueMaxBytes:   4 MiB
AppendStoreRetryBackoff: 1 ms
```

Propagate the Phase 2 service config fields from `service.Config` into `reactor.Config`.

Also add matching fields with English comments to the public root `channelv2.Config` in `pkg/channelv2/channel.go` and to `service.Config` in `pkg/channelv2/service/service.go`:

```go
// AppendBatchMaxRecords is the maximum records flushed in one store append batch.
AppendBatchMaxRecords int
// AppendBatchMaxBytes is the maximum payload budget flushed in one store append batch.
AppendBatchMaxBytes int
// AppendBatchMaxWait is the maximum age of the oldest queued append before a tick flushes it.
AppendBatchMaxWait time.Duration
// AppendQueueMaxRequests bounds queued append requests per channel.
AppendQueueMaxRequests int
// AppendQueueMaxBytes bounds queued append payload bytes per channel.
AppendQueueMaxBytes int
// AppendStoreRetryBackoff delays retry after store append pool backpressure.
AppendStoreRetryBackoff time.Duration
```

- [ ] **Step 5: Add append queue to runtime channel**

Update `runtimeChannel`:

```go
type runtimeChannel struct {
	state *machine.ChannelState
	store store.ChannelStore
	waiters map[ch.OpID]*Future
	appendQ appendQueue
	appendInflight *appendBatch
	appendStoreBlocked bool
	appendRetryAt time.Time
	replication replicationState
}
```

Initialize `appendQ` in `ensureChannel` with config defaults.

- [ ] **Step 6: Replace synchronous append with queue admission**

`handleAppend` should:

1. Look up channel and validate leader readiness cheaply by calling a new machine validation helper or by letting flush call `ProposeAppendBatch`.
2. Convert request messages to cloned `ch.Record` values.
3. Normalize commit mode to quorum when zero.
4. Push `appendRequest` into `rc.appendQ`; if full, complete future with `ErrBackpressured`.
5. Store the future in `rc.waiters[event.OpID]` after queue admission.
6. Register context cancellation if the service supplied one through event data; `service.AppendBatch` submits the cleanup event when `ctx.Done()` wins.
7. Call `tryFlushAppend(rc, now)`.

If validation can fail before queueing (`not leader`, `not ready`, deleted), complete future with that error and do not enqueue.

- [ ] **Step 7: Implement append flush task submission**

`tryFlushAppend` should:

1. Return if `rc.appendInflight != nil`.
2. Return if queue does not meet count/bytes/wait flush rules.
3. Return if `appendStoreBlocked && now.Before(appendRetryAt)`.
4. Pop one batch with `batchOpID := r.nextOpID()` or use a group-supplied callback.
5. Build waiters from the popped batch and call:

```go
waiters := make([]machine.AppendBatchWaiter, 0, len(batch.requests))
for _, req := range batch.requests {
	waiters = append(waiters, machine.AppendBatchWaiter{
		OpID:       req.opID,
		CommitMode: req.commitMode,
		Records:    req.records,
	})
}
decision := rc.state.ProposeAppendBatch(machine.AppendBatchCommand{BatchOpID: batchOpID, Waiters: waiters})
```
6. Submit `worker.TaskStoreAppend` with the batch fence and records.
7. On submit success, set `rc.appendInflight = &batch`.
8. On `ErrBackpressured`, restore the batch to the front, set blocked/retry fields, and leave accepted futures pending.
9. On other error, fail the batch futures and remove waiters.

The reactor needs a way to allocate batch op ids. Add `NextOpID func() ch.OpID` to `ReactorConfig`, wire it from `Group.NextOpID`, and keep batch op ids distinct from client op ids.

- [ ] **Step 8: Apply store append worker results**

In `handleWorkerResult`, add `worker.TaskStoreAppend`:

```go
func (r *Reactor) handleStoreAppendResult(rc *runtimeChannel, result worker.Result) {
	payload := result.StoreAppend
	stored := machine.AppendStoredResult{Fence: result.Fence, Err: result.Err}
	if payload != nil {
		stored.BaseOffset = payload.BaseOffset
		stored.LastOffset = payload.LastOffset
	}
	decision := rc.state.ApplyAppendStored(stored)
	if rc.appendInflight != nil && rc.appendInflight.batchOpID == result.Fence.OpID {
		rc.appendInflight = nil
	}
	r.completeReplies(rc, decision.Replies)
	if hasReplicateSignal(decision.Signals) {
		rc.replication.dirty = true
	}
	r.tryFlushAppend(rc, time.Now())
}
```

If result fence is stale, do not clear a newer `appendInflight`. If result has an error for the current batch, fail all request waiters in that batch and clear inflight.

When `handleApplyMeta` observes a generation, epoch, leader epoch, role, or status change, fail queued append requests, the current append batch waiters, and matching waiter-map entries with `ErrStaleMeta` before applying the new metadata. Late store append completions from the old fence must be ignored after their client futures have been failed deterministically.

- [ ] **Step 9: Implement accepted append cancellation cleanup**

Choose the simplest robust pattern:

1. Add `Context context.Context`, `CancelOp ch.OpID`, and `CancelErr error` to `Event`, and add `EventCancelWaiter` to `EventKind`.
2. In `service.AppendBatch`, after successful `group.Submit`, wait on either `future.Await(context.Background())` or `ctx.Done()`.
3. If `ctx.Done()` wins, submit `Event{Kind: reactor.EventCancelWaiter, Key: key, CancelOp: opID, CancelErr: ctx.Err()}` as a high-priority cleanup event, then return `ctx.Err()`.
4. Reactor cancel handler removes the op from `appendQ` and `waiters`; if it was already in the current durable batch, keep the store operation but complete the future with `CancelErr` only if not already completed.

Add a short English comment explaining that context cancellation after admission is cooperative and does not cancel already-started durable writes.

- [ ] **Step 10: Run append tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/reactor ./pkg/channelv2/service -run 'TestAppend|TestSingleNodeAppendFetchCommitted' -count=1
```

Expected: PASS.

- [ ] **Step 11: Commit async append batching**

Run:

```bash
git add pkg/channelv2/channel.go pkg/channelv2/reactor pkg/channelv2/service pkg/channelv2/machine
git commit -m "feat: batch channelv2 append effects"
```

## Task 6: Reactor-Owned Follower Replication

**Files:**
- Modify: `pkg/channelv2/channel.go`
- Modify: `pkg/channelv2/reactor/event.go`
- Modify: `pkg/channelv2/reactor/group.go`
- Create: `pkg/channelv2/reactor/replication_state.go`
- Create: `pkg/channelv2/reactor/replication_state_test.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/reactor/effect.go`
- Modify: `pkg/channelv2/service/service.go`
- Modify: `pkg/channelv2/service/replication.go`
- Modify: `pkg/channelv2/testkit/cluster.go`
- Modify: `pkg/channelv2/testkit/cluster_test.go`

- [ ] **Step 1: Write failing follower pull scheduling tests**

Create `pkg/channelv2/reactor/replication_state_test.go`:

```go
package reactor

import (
	"context"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
	"github.com/stretchr/testify/require"
)

func TestFollowerTickPullsFromLocalLEOPlusOne(t *testing.T) {
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 2, ReactorCount: 1, MailboxSize: 16, Store: factory, Transport: net})
	require.NoError(t, err)
	defer g.Close()

	meta := ch.Meta{Key: "1:a", ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Unix(1, 0)}))
	require.Eventually(t, func() bool { return net.LastPull().NextOffset == 1 }, time.Second, time.Millisecond)
}

func TestFollowerPullInflightSuppressesDuplicatePull(t *testing.T) {
	net := newCapturingTransport()
	net.BlockPulls()
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 2, ReactorCount: 1, MailboxSize: 16, Store: factory, Transport: net})
	require.NoError(t, err)
	defer g.Close()

	meta := ch.Meta{Key: "1:a", ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Unix(1, 0)}))
	require.Eventually(t, func() bool { return net.PullCalls() == 1 }, time.Second, time.Millisecond)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Unix(1, 0).Add(time.Millisecond)}))
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Unix(1, 0).Add(2 * time.Millisecond)}))
	require.Equal(t, 1, net.PullCalls())
	net.UnblockPulls()
}

func TestFollowerPullErrorBacksOff(t *testing.T) {
	net := newCapturingTransport()
	net.SetPullError(ch.ErrNotReady)
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{
		LocalNode: 2,
		ReactorCount: 1,
		MailboxSize: 16,
		Store: factory,
		Transport: net,
		ReplicationMinBackoff: time.Hour,
		ReplicationMaxBackoff: time.Hour,
	})
	require.NoError(t, err)
	defer g.Close()

	meta := ch.Meta{Key: "1:a", ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	base := time.Unix(1, 0)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: base}))
	require.Eventually(t, func() bool { return net.PullCalls() == 1 }, time.Second, time.Millisecond)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: base.Add(time.Millisecond)}))
	require.Equal(t, 1, net.PullCalls())
}
```

- [ ] **Step 2: Write failing apply/ack tests**

Add these tests to the same file:

```go
func TestFollowerStoreApplyResultSendsAck(t *testing.T) {
	net := newCapturingTransport()
	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  "1:a",
		Epoch:       1,
		LeaderEpoch: 1,
		LeaderHW:    1,
		LeaderLEO:   1,
		Records:     []ch.Record{{ID: 10, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
	})
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 2, ReactorCount: 1, MailboxSize: 16, Store: factory, Transport: net})
	require.NoError(t, err)
	defer g.Close()

	meta := ch.Meta{Key: "1:a", ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Unix(1, 0)}))
	require.Eventually(t, func() bool { return net.LastAck().MatchOffset == 1 }, time.Second, time.Millisecond)

	fetch, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventFetch, Key: meta.Key, OpID: 99, Fetch: ch.FetchRequest{ChannelID: meta.ID, FromSeq: 1, Limit: 10, MaxBytes: 1024}})
	require.NoError(t, err)
	result, err := fetch.Await(context.Background())
	require.NoError(t, err)
	require.Len(t, result.Fetch.Messages, 1)
	require.Equal(t, uint64(1), result.Fetch.CommittedSeq)
}

func TestFollowerAckResultResetsBackoff(t *testing.T) {
	net := newCapturingTransport()
	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  "1:a",
		Epoch:       1,
		LeaderEpoch: 1,
		LeaderHW:    1,
		LeaderLEO:   1,
		Records:     []ch.Record{{ID: 10, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
	})
	net.SetAckError(ch.ErrNotReady)
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 2, ReactorCount: 1, MailboxSize: 16, Store: factory, Transport: net, ReplicationMinBackoff: time.Millisecond, ReplicationMaxBackoff: 2 * time.Millisecond})
	require.NoError(t, err)
	defer g.Close()

	meta := ch.Meta{Key: "1:a", ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	base := time.Unix(1, 0)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: base}))
	require.Eventually(t, func() bool { return net.AckCalls() == 1 }, time.Second, time.Millisecond)
	net.SetAckError(nil)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: base.Add(time.Millisecond)}))
	require.Equal(t, 1, net.AckCalls())
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: base.Add(3 * time.Millisecond)}))
	require.Eventually(t, func() bool { return net.AckCalls() == 2 }, time.Second, time.Millisecond)
}

func TestStoreApplyPoolFullKeepsOnePendingPullAndRetries(t *testing.T) {
	net := newCapturingTransport()
	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  "1:a",
		Epoch:       1,
		LeaderEpoch: 1,
		LeaderHW:    1,
		LeaderLEO:   1,
		Records:     []ch.Record{{ID: 10, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
	})
	factory := newBlockingApplyFactory()
	factory.BlockApplies()
	g, err := NewGroup(Config{
		LocalNode: 2,
		ReactorCount: 1,
		MailboxSize: 16,
		Store: factory,
		Transport: net,
		WorkerPools: worker.PoolsConfig{
			StoreApply: worker.PoolConfig{Name: "store-apply", Workers: 1, QueueSize: 1},
		},
	})
	require.NoError(t, err)
	defer g.Close()

	meta := ch.Meta{Key: "1:a", ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Unix(1, 0)}))
	require.Eventually(t, factory.ApplyStarted, time.Second, time.Millisecond)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Unix(1, 0).Add(time.Millisecond)}))
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Unix(1, 0).Add(2 * time.Millisecond)}))
	require.Equal(t, 1, net.PullCalls())

	factory.UnblockApplies()
	require.Eventually(t, func() bool { return net.LastAck().MatchOffset == 1 }, time.Second, time.Millisecond)
}

func TestLeaderPullUsesStoreReadLogWorkerWithoutBlockingReactor(t *testing.T) {
	factory := newBlockingReadLogFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory, AppendBatchMaxRecords: 1})
	require.NoError(t, err)
	defer g.Close()

	meta := ch.Meta{Key: "1:a", ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	appendFuture, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 1, "a"))
	require.NoError(t, err)
	requireFuturePending(t, appendFuture)

	pullFuture, err := g.Submit(context.Background(), meta.Key, Event{
		Kind: EventPull,
		Key:  meta.Key,
		OpID: 77,
		Pull: transport.PullRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1, Follower: 2, NextOffset: 1, MaxBytes: 1024},
	})
	require.NoError(t, err)
	require.Eventually(t, factory.ReadLogStarted, time.Second, time.Millisecond)

	metaFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta})
	require.NoError(t, err)
	_, err = metaFuture.Await(context.Background())
	require.NoError(t, err)

	factory.UnblockReadLogs()
	_, err = pullFuture.Await(context.Background())
	require.NoError(t, err)
}

type capturingTransport struct {
	mu        sync.Mutex
	pullCalls int
	ackCalls  int
	lastPull  transport.PullRequest
	lastAck   transport.AckRequest
	pullResp  transport.PullResponse
	pullErr   error
	ackErr    error
	blockPull chan struct{}
}

func newCapturingTransport() *capturingTransport {
	return &capturingTransport{}
}

func (t *capturingTransport) Pull(ctx context.Context, node ch.NodeID, req transport.PullRequest) (transport.PullResponse, error) {
	t.mu.Lock()
	t.pullCalls++
	t.lastPull = req
	block := t.blockPull
	resp := t.pullResp
	err := t.pullErr
	t.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return transport.PullResponse{}, ctx.Err()
		}
	}
	if err != nil {
		return transport.PullResponse{}, err
	}
	if resp.ChannelKey == "" {
		resp = transport.PullResponse{ChannelKey: req.ChannelKey, Epoch: req.Epoch, LeaderEpoch: req.LeaderEpoch, LeaderHW: req.NextOffset - 1, LeaderLEO: req.NextOffset - 1}
	}
	return resp, nil
}

func (t *capturingTransport) Ack(ctx context.Context, node ch.NodeID, req transport.AckRequest) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ackCalls++
	t.lastAck = req
	return t.ackErr
}

func (t *capturingTransport) LastPull() transport.PullRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastPull
}

func (t *capturingTransport) LastAck() transport.AckRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastAck
}

func (t *capturingTransport) PullCalls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pullCalls
}

func (t *capturingTransport) AckCalls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ackCalls
}

func (t *capturingTransport) SetPullResponse(resp transport.PullResponse) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pullResp = resp
}

func (t *capturingTransport) SetPullError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pullErr = err
}

func (t *capturingTransport) SetAckError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ackErr = err
}

func (t *capturingTransport) BlockPulls() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.blockPull = make(chan struct{})
}

func (t *capturingTransport) UnblockPulls() {
	t.mu.Lock()
	block := t.blockPull
	t.blockPull = nil
	t.mu.Unlock()
	if block != nil {
		close(block)
	}
}

type blockingApplyFactory struct {
	base         *store.MemoryFactory
	applyStarted chan struct{}
	unblock       chan struct{}
}

func newBlockingApplyFactory() *blockingApplyFactory {
	return &blockingApplyFactory{base: store.NewMemoryFactory(), applyStarted: make(chan struct{}, 8), unblock: make(chan struct{})}
}

func (f *blockingApplyFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (store.ChannelStore, error) {
	base, err := f.base.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	return &blockingApplyStore{ChannelStore: base, parent: f}, nil
}

func (f *blockingApplyFactory) BlockApplies() {
	f.unblock = make(chan struct{})
}

func (f *blockingApplyFactory) ApplyStarted() bool {
	return len(f.applyStarted) > 0
}

func (f *blockingApplyFactory) UnblockApplies() {
	select {
	case <-f.unblock:
	default:
		close(f.unblock)
	}
}

type blockingApplyStore struct {
	store.ChannelStore
	parent *blockingApplyFactory
}

func (s *blockingApplyStore) ApplyFollower(ctx context.Context, req store.ApplyFollowerRequest) (store.ApplyFollowerResult, error) {
	select {
	case s.parent.applyStarted <- struct{}{}:
	default:
	}
	select {
	case <-s.parent.unblock:
	case <-ctx.Done():
		return store.ApplyFollowerResult{}, ctx.Err()
	}
	return s.ChannelStore.ApplyFollower(ctx, req)
}

type blockingReadLogFactory struct {
	base    *store.MemoryFactory
	started chan struct{}
	unblock chan struct{}
}

func newBlockingReadLogFactory() *blockingReadLogFactory {
	return &blockingReadLogFactory{base: store.NewMemoryFactory(), started: make(chan struct{}, 8), unblock: make(chan struct{})}
}

func (f *blockingReadLogFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (store.ChannelStore, error) {
	base, err := f.base.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	return &blockingReadLogStore{ChannelStore: base, parent: f}, nil
}

func (f *blockingReadLogFactory) ReadLogStarted() bool {
	return len(f.started) > 0
}

func (f *blockingReadLogFactory) UnblockReadLogs() {
	select {
	case <-f.unblock:
	default:
		close(f.unblock)
	}
}

type blockingReadLogStore struct {
	store.ChannelStore
	parent *blockingReadLogFactory
}

func (s *blockingReadLogStore) ReadLog(ctx context.Context, req store.ReadLogRequest) (store.ReadLogResult, error) {
	select {
	case s.parent.started <- struct{}{}:
	default:
	}
	select {
	case <-s.parent.unblock:
	case <-ctx.Done():
		return store.ReadLogResult{}, ctx.Err()
	}
	return s.ChannelStore.ReadLog(ctx, req)
}
```

- [ ] **Step 3: Run tests and verify failure**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/reactor -run 'TestFollower|TestStoreApplyPoolFull' -count=1
```

Expected: FAIL because service still owned replication until Task 2 removed it, and reactor tick has no replication scheduler.

- [ ] **Step 4: Implement replication state helpers**

Create `replication_state.go`:

```go
type replicationState struct {
	pullInflight bool
	ackInflight bool
	dirty bool
	nextPullAt time.Time
	backoff time.Duration
	lastLeaderHW uint64
	lastError error
	pendingPull *transport.PullResponse
	applyBlocked bool
}

func (s *replicationState) markDirty(now time.Time) {
	s.dirty = true
	if s.nextPullAt.IsZero() || now.Before(s.nextPullAt) {
		s.nextPullAt = now
	}
}

func nextReplicationBackoff(current, minBackoff, maxBackoff time.Duration) time.Duration
```

Add config defaults:

```go
// ReplicationIdlePollInterval delays the next follower poll when the leader has no records.
ReplicationIdlePollInterval time.Duration // default 10 ms for tests/experimental runtime
// ReplicationMinBackoff is the first retry delay after pull/apply/ack errors.
ReplicationMinBackoff time.Duration       // default 1 ms
// ReplicationMaxBackoff caps replication retry delay after repeated errors.
ReplicationMaxBackoff time.Duration       // default 100 ms
// PullMaxBytes bounds one leader pull response.
PullMaxBytes int                          // default 64 KiB
```

Add matching fields with English comments to `service.Config` and root `channelv2.Config` when they are exposed through the public facade.

- [ ] **Step 5: Mark followers dirty on ApplyMeta**

In `handleApplyMeta`, after successful `rc.state.ApplyMeta`:

```go
if rc.state.Role == ch.RoleFollower && rc.state.Status == ch.StatusActive {
	rc.replication.markDirty(time.Now())
}
```

If role changes away from follower, clear follower inflight/pending state enough to ignore old completions through fences.

- [ ] **Step 6: Implement tick scheduling**

`handleTick` should iterate only channels owned by the reactor and call:

```go
func (r *Reactor) tickReplication(rc *runtimeChannel, now time.Time)
func (r *Reactor) trySubmitPull(rc *runtimeChannel, now time.Time)
func (r *Reactor) trySubmitPendingApply(rc *runtimeChannel, now time.Time)
```

Pull scheduling rules:

```text
if role != follower: skip
if pullInflight: skip
if pendingPull != nil: try apply, do not pull more
if now < nextPullAt: skip
if transport/pool missing: skip or back off
nextOffset = state.LEO + 1
submit TaskRPCPull to leader
pullInflight = true on success
```

Use a new operation id for each RPC pull fence.

When a direct test submits `EventTick` through `Group.Submit`, `handleTick` must complete `event.Future` after one scheduling pass. `Group.Tick(ctx)` still submits low-priority `EventTick` events to every reactor and ignores `ErrBackpressured` tick drops.

- [ ] **Step 7: Handle RPC pull worker results**

For `worker.TaskRPCPull`:

```text
error:
  pullInflight=false
  backoff=nextBackoff
  nextPullAt=now+backoff
records empty:
  pullInflight=false
  HW=min(LEO, LeaderHW)
  nextPullAt=now+idlePollInterval
records non-empty:
  pullInflight=false
  lastLeaderHW=LeaderHW
  pendingPull=response
  try submit StoreApply
```

Validate response epoch and leader epoch before using records. Ignore stale pull responses after metadata changes.

- [ ] **Step 8: Handle store apply worker results and send ACK**

For `worker.TaskStoreApply`:

```text
if stale: ignore
if error: pendingPull remains nil, backoff and retry pull later
success:
  state.LEO=result.LEO
  state.HW=min(state.LEO, lastLeaderHW)
  pendingPull=nil
  applyBlocked=false
  submit TaskRPCAck with MatchOffset=state.LEO
  ackInflight=true on submit success
  dirty=true
```

If RPC pool is full for ACK, set `ackInflight=false`, set backoff, and retry on tick. Do not lose the fact that follower should ack current LEO.

- [ ] **Step 9: Handle RPC ACK worker results**

For `worker.TaskRPCAck`:

```text
success:
  ackInflight=false
  backoff=0
  lastError=nil
  nextPullAt=now if dirty else now+idlePollInterval
error:
  ackInflight=false
  backoff=nextBackoff
  nextPullAt=now+backoff
```

Leader-side ACK still enters through `service.HandleAck -> EventAck` and calls `machine.ApplyFollowerAck` on the leader reactor. Before calling `ApplyFollowerAck`, `handleAck` must validate `event.Ack.Epoch == rc.state.Epoch`, `event.Ack.LeaderEpoch == rc.state.LeaderEpoch`, local role is leader, and `event.Ack.Follower` is a replica. Stale ACKs complete their request future with no progress change and no client append completions. Add a stale ACK test:

```go
func TestLeaderIgnoresAckAfterLeaderEpochBump(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory, AppendBatchMaxRecords: 1})
	require.NoError(t, err)
	defer g.Close()

	meta := ch.Meta{Key: "1:a", ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	staleAck := Event{
		Kind: EventAck,
		Key:  meta.Key,
		Ack:  transport.AckRequest{ChannelKey: meta.Key, Epoch: 1, LeaderEpoch: 1, Follower: 2, MatchOffset: 100},
	}
	meta.LeaderEpoch = 2
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	require.NoError(t, awaitSubmit(g, meta.Key, staleAck))

	future, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 1, "requires-current-ack"))
	require.NoError(t, err)
	requireFuturePending(t, future)
}
```

- [ ] **Step 10: Make leader pull asynchronous and remove obsolete follower apply handler**

`service.HandlePull` continues to submit `EventPull` to the leader reactor. `handlePull` must:

1. Validate local role, request epoch, leader epoch, and follower membership.
2. Build a fenced `worker.TaskStoreReadLog` with `FromOffset: event.Pull.NextOffset`, `MaxOffset: rc.state.LEO`, and `MaxBytes: event.Pull.MaxBytes`.
3. Store the pull request future in a pull-waiter map keyed by `event.OpID`.
4. Complete the pull future from the `TaskStoreReadLog` worker result as `transport.PullResponse{ChannelKey: rc.state.Key, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, LeaderHW: rc.state.HW, LeaderLEO: rc.state.LEO, Records: result.StoreReadLog.Records}` after fence validation.
5. Fail the pull future with `ErrBackpressured` if the store-read pool rejects the task.

After reactor-owned follower replication is implemented, delete or disable the direct `EventApplyRecords` path and `handleApplyRecords` so no reactor handler calls `store.ApplyFollower` synchronously.

- [ ] **Step 11: Change service Tick to group Tick only**

Modify `pkg/channelv2/service/service.go` so:

```go
func (c *cluster) Tick(ctx context.Context) error {
	return c.group.Tick(ctx)
}
```

Keep the metadata cache for `ApplyMeta` if still used by tests, but do not perform pull/apply/ack directly in service `Tick` anymore.

- [ ] **Step 12: Update testkit and service tests**

Update `pkg/channelv2/testkit/cluster.go` so the harness drives `Tick` on each node and waits for replication convergence through the reactor-owned scheduler. Remove assumptions that `service.Tick` performs direct pull/apply/ack.

- [ ] **Step 13: Run replication tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/reactor ./pkg/channelv2/testkit ./pkg/channelv2/service -run 'TestFollower|TestStoreApplyPoolFull|TestLeaderPullUsesStoreReadLogWorker|TestLeaderIgnoresAck|TestThreeNode|TestSingleNodeAppendFetchCommitted' -count=1
```

Expected: PASS.

- [ ] **Step 14: Commit reactor-owned replication**

Run:

```bash
git add pkg/channelv2/channel.go pkg/channelv2/reactor pkg/channelv2/service pkg/channelv2/testkit
git commit -m "feat: move channelv2 replication into reactors"
```

## Task 7: Metrics Observer And Benchmarks

**Files:**
- Create: `pkg/channelv2/reactor/metrics.go`
- Modify: `pkg/channelv2/reactor/group.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/worker/pool.go`
- Modify: `pkg/channelv2/worker/pools.go`
- Modify: `pkg/channelv2/service/service.go`
- Modify: `pkg/channelv2/bench_test.go`
- Modify: `pkg/channelv2/FLOW.md`
- Create: `docs/superpowers/reports/2026-05-23-channelv2-reactor-effects-batching-report.md`

- [ ] **Step 1: Write failing observer tests**

Create or extend `pkg/channelv2/reactor/group_test.go`:

```go
package reactor

import (
	"context"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
	"github.com/stretchr/testify/require"
)

func TestObserverSeesAppendBatchAndWorkerResult(t *testing.T) {
	obs := &captureObserver{}
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory, Observer: obs, AppendBatchMaxRecords: 1})
	require.NoError(t, err)
	defer g.Close()

	meta := testMeta("a", 1, 1)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	future, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 1, "a"))
	require.NoError(t, err)
	_, err = future.Await(context.Background())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return obs.AppendBatches() > 0 && obs.WorkerResults() > 0
	}, time.Second, time.Millisecond)
}

type captureObserver struct {
	mu            sync.Mutex
	appendBatches int
	workerResults int
}

func (o *captureObserver) SetReactorMailboxDepth(reactorID int, priority string, depth int) {}

func (o *captureObserver) SetWorkerQueueDepth(pool string, depth int) {}

func (o *captureObserver) ObserveAppendBatch(records int, bytes int, wait time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.appendBatches++
}

func (o *captureObserver) ObserveAppendLatency(mode ch.CommitMode, d time.Duration) {}

func (o *captureObserver) ObserveWorkerResult(kind worker.TaskKind, err error, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.workerResults++
}

func (o *captureObserver) AppendBatches() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.appendBatches
}

func (o *captureObserver) WorkerResults() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.workerResults
}
```

- [ ] **Step 2: Run observer test and verify failure**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/reactor -run TestObserverSeesAppendBatchAndWorkerResult -count=1
```

Expected: FAIL because observer interface and callbacks do not exist.

- [ ] **Step 3: Implement no-op observer**

Create `metrics.go`:

```go
// Observer receives lightweight runtime metrics from the reactor hot path.
type Observer interface {
	SetReactorMailboxDepth(reactorID int, priority string, depth int)
	SetWorkerQueueDepth(pool string, depth int)
	ObserveAppendBatch(records int, bytes int, wait time.Duration)
	ObserveAppendLatency(mode ch.CommitMode, d time.Duration)
	ObserveWorkerResult(kind worker.TaskKind, err error, d time.Duration)
}

type noopObserver struct{}
```

Make all methods no-op in `noopObserver`. Store observer in `Group` or `ReactorConfig`; never require nil checks in hot-path code after construction.

- [ ] **Step 4: Emit minimal metrics**

Emit:

- append batch records/bytes/wait when a batch is submitted to `StoreAppend`.
- append latency when an append future completes successfully or with error.
- worker result kind/error when `Group.Complete` routes a result.
- worker queue depth after `Pool.Submit` attempts.
- mailbox depths opportunistically after drain or submit if cheap.

Do not add expensive allocations solely for metrics.

- [ ] **Step 5: Add async/batched benchmarks**

Update `pkg/channelv2/bench_test.go` with:

```go
func BenchmarkAppendSingleNodeHotChannelBatched(b *testing.B)
func BenchmarkAppendSingleNodeManyChannelsAsync(b *testing.B)
func BenchmarkAppendThreeNodeManyChannelsAsync(b *testing.B)
func BenchmarkAppendOldStoreAdapterAsync(b *testing.B)
```

Benchmark rules:

- Call `b.ReportAllocs()`.
- Use `GOMAXPROCS` default; do not change global scheduler settings.
- Pre-create metadata before `b.ResetTimer()`.
- Use bounded concurrency with `b.RunParallel` or fixed goroutines and `atomic` counters.
- Keep payloads small and deterministic.
- Capture p50/p95/p99 append latency in an internal helper if it does not add too much overhead; otherwise record average latency and batch distribution through the observer.
- Record `runtime.NumGoroutine()` before and after and fail only on obvious leaks in tests, not benchmarks.

- [ ] **Step 6: Run targeted benchmark smoke**

Run:

```bash
GOWORK=off go test ./pkg/channelv2 -run '^$' -bench 'BenchmarkAppendSingleNodeHotChannelBatched|BenchmarkAppendSingleNodeManyChannelsAsync|BenchmarkAppendThreeNodeManyChannelsAsync' -benchtime=1s -count=1
```

Expected: PASS and print benchmark rows. Do not compare absolute numbers in tests.

- [ ] **Step 7: Update FLOW.md**

Modify `pkg/channelv2/FLOW.md` sections:

- Purpose: Phase 2 validates multiple reactors plus worker pools.
- Append: service routes to reactor, reactor enqueues per-channel append requests, flushes batches, store append worker completes via high-priority worker result, machine completes waiters.
- Fetch: reactor captures HW and uses `StoreReadCommitted` worker task.
- Replication: `Tick` submits low-priority reactor tick, follower state pulls from local `LEO+1`, applies records through store apply worker, sends ack through RPC worker.
- Backpressure: describe mailbox, append queue, worker pool, fetch fail-fast, replication pending-pull behavior.
- Import boundary: keep only `store/channel_adapter.go` importing old channel packages.

- [ ] **Step 8: Write benchmark report**

Create `docs/superpowers/reports/2026-05-23-channelv2-reactor-effects-batching-report.md` after the benchmark smoke command runs. The report must be short and factual:

- Title: `# Channelv2 Reactor Effects And Batching Report`
- Commands: list the exact benchmark command from Step 6 and any verification command run in this task.
- Results: paste the benchmark rows printed by `go test` for `BenchmarkAppendSingleNodeHotChannelBatched`, `BenchmarkAppendSingleNodeManyChannelsAsync`, and `BenchmarkAppendThreeNodeManyChannelsAsync`.
- Observations: write two to four bullets describing batch sizes, allocations, and whether worker queue/backpressure metrics were emitted. Do not write placeholder rows or invented numbers.

- [ ] **Step 9: Commit metrics, benchmarks, and report**

Run:

```bash
git add pkg/channelv2 docs/superpowers/reports/2026-05-23-channelv2-reactor-effects-batching-report.md
git commit -m "test: add channelv2 async batching benchmarks"
```

## Task 8: Final Hardening And Verification

**Files:**
- Modify only files changed in previous tasks when verification exposes a specific defect.
- Ensure no generated temporary files remain.

- [ ] **Step 1: Run channelv2 unit tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/... -count=1
```

Expected: PASS.

- [ ] **Step 2: Run channelv2 race tests**

Run:

```bash
GOWORK=off go test -race ./pkg/channelv2/... -count=1
```

Expected: PASS. On macOS, linker warnings are acceptable if tests pass.

- [ ] **Step 3: Run benchmark smoke**

Run:

```bash
GOWORK=off go test ./pkg/channelv2 -run '^$' -bench 'BenchmarkAppendSingleNodeHotChannelBatched|BenchmarkAppendSingleNodeManyChannelsAsync|BenchmarkAppendThreeNodeManyChannelsAsync' -benchtime=1s -count=1
```

Expected: PASS with benchmark rows.

- [ ] **Step 4: Check import boundary**

Run:

```bash
rg 'pkg/channel"|pkg/channel/store' pkg/channelv2 -g'*.go'
```

Expected: only `pkg/channelv2/store/channel_adapter.go` and an adapter-specific test that directly exercises old-store conversion. If any new channelv2 file imports old `pkg/channel`, refactor behind the store adapter.

- [ ] **Step 5: Run old and new channel package tests together**

Run:

```bash
GOWORK=off go test ./pkg/channel/... ./pkg/channelv2/... -count=1
```

Expected: PASS.

- [ ] **Step 6: Run formatting and diff checks**

Run:

```bash
gofmt -w pkg/channelv2
git diff --check
git status --short
```

Expected: no whitespace errors. `git status --short` should show only intentional Phase 2 files.

- [ ] **Step 7: Review close semantics manually**

Inspect these code paths:

```bash
rg 'func \(.*Close\)|ErrClosed|EventClose|failAll' pkg/channelv2/reactor pkg/channelv2/worker pkg/channelv2/service
```

Confirm:

- New service requests after close return `ErrClosed`.
- Pending append and fetch futures complete with `ErrClosed` during shutdown.
- Late worker results after close are ignored.
- Worker pools close after reactors stop accepting new work or completions are safely ignored.

- [ ] **Step 8: Final commit if hardening changed code**

If Task 8 required code changes:

```bash
git add pkg/channelv2 docs/superpowers/reports
git commit -m "fix: harden channelv2 async reactor shutdown"
```

If no changes were needed, do not create an empty commit.

## Implementation Checklist

- [ ] Worker tasks/results are typed for store append, store committed read, store raw log read, store apply, RPC pull, and RPC ack.
- [ ] `reactor.Group` owns worker pools and implements `worker.CompletionSink`.
- [ ] Worker completions route by `Fence.ChannelKey` as high-priority `EventWorkerResult` events.
- [ ] Fetch no longer performs blocking store reads in reactor handlers.
- [ ] Append uses per-channel queues and batch flush by max records, max bytes, and max wait.
- [ ] One channel has at most one store append in flight.
- [ ] Store append batch op ids are distinct from client request op ids.
- [ ] Store append completion can complete multiple client futures.
- [ ] Stale async completions are ignored by fence validation.
- [ ] Store append pool backpressure keeps accepted appends pending and retries on tick.
- [ ] Accepted append context cancellation removes queued waiters or completes futures cooperatively.
- [ ] `service.Tick` only submits low-priority reactor ticks.
- [ ] Follower replication state lives in `runtimeChannel`.
- [ ] Follower pull offset uses local `state.LEO + 1`.
- [ ] Pull/apply/ack use typed worker tasks.
- [ ] Store apply pool backpressure keeps one pending pull response and retries.
- [ ] Observer hooks exist and default to no-op.
- [ ] Benchmarks exercise async/batched paths.
- [ ] `pkg/channelv2/FLOW.md` matches the new flow.
- [ ] Old `pkg/channel` import boundary is preserved.

## Risk Notes

- The largest correctness risk is losing or double-completing accepted append futures when a store append result races with context cancellation. Keep future completion idempotent and test both queue-only and already-inflight cancellation.
- The largest performance risk is doing too much work per reactor turn for one hot channel. Enforce one append flush per channel per turn and keep `MaxEventsPerTurn` bounded.
- The largest replication risk is using committed HW instead of local LEO for follower pull offset. Tests must assert the exact `NextOffset` sent to transport.
- The old store adapter may serialize internally. That is acceptable for Phase 2 as long as the blocking call is outside the reactor and queue pressure is observable.
