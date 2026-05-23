# Channelv2 Reactor V0 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an experimental `pkg/channelv2` end-to-end channel log loop using multiple reactors, bounded worker pools, narrow store adapters, and v0 pull/ack replication.

**Architecture:** `pkg/channelv2` keeps public DTOs in the root package, while `machine` owns pure per-channel state transitions, `reactor` owns event loops and mailbox routing, `worker` owns bounded blocking execution, `store` hides persistence behind a narrow interface, and `transport`/`replication` provide the v0 pull/ack loop. The first implementation slice supports `ApplyMeta`, `Append`, `AppendBatch`, `Fetch`, follower apply, ACK, and HW commit without migration, retention, snapshot, or leader repair.

**Tech Stack:** Go 1.23, standard `context`/`sync`/`time`/`hash/fnv`, existing project test style with `testing` and `testify/require`, existing `pkg/channel/store` only behind the final adapter.

---

## Source Spec

- Read first: `docs/superpowers/specs/2026-05-23-channelv2-reactor-design.md`
- Also read before implementing: `AGENTS.md`, `pkg/channel/FLOW.md`, `pkg/channel/replica/FLOW.md`
- Use @superpowers:test-driven-development for each code task.
- If executing with subagents, use @superpowers:subagent-driven-development. Otherwise use @superpowers:executing-plans.

## Scope Notes

- This plan implements the v0 experimental package only. Do not wire `pkg/channelv2` into `internal/app` or replace `pkg/channel`.
- Keep single-node behavior as single-node cluster quorum behavior. Do not add a bypass branch.
- V0 construction should live in `pkg/channelv2/service.New` and return `channelv2.Cluster`. This avoids a Go import cycle between the root package and subpackages. The root `pkg/channelv2` package stays the public type and interface contract.
- Only `pkg/channelv2/store/channel_adapter.go` may import old `pkg/channel` or `pkg/channel/store`.

## File Structure

- Create: `pkg/channelv2/doc.go` - package overview and v0 limitations.
- Create: `pkg/channelv2/errors.go` - typed errors shared by all subpackages.
- Create: `pkg/channelv2/types.go` - public DTOs: node/channel IDs, metadata, messages, records, requests, results, roles, status, commit mode, checkpoint.
- Create: `pkg/channelv2/channel.go` - `Cluster` interface and `Config` shell.
- Create: `pkg/channelv2/transport/types.go` - v0 `Pull`, `Ack`, client/server interfaces.
- Create: `pkg/channelv2/store/adapter.go` - narrow `Factory` and `ChannelStore` interfaces.
- Create: `pkg/channelv2/machine/channel.go` - `ChannelState`, waiter, progress, decisions, fences.
- Create: `pkg/channelv2/machine/meta.go` - apply authoritative metadata and role transitions.
- Create: `pkg/channelv2/machine/append.go` - append proposal, append stored result, waiter completion.
- Create: `pkg/channelv2/machine/fetch.go` - committed fetch view decisions.
- Create: `pkg/channelv2/machine/follower.go` - follower pull/apply decisions.
- Create: `pkg/channelv2/machine/progress.go` - ISR progress and HW calculation.
- Create: `pkg/channelv2/machine/invariant.go` - cheap invariant checks for tests and debug builds.
- Create: `pkg/channelv2/store/memory.go` - in-memory contract store for tests and benchmarks.
- Create: `pkg/channelv2/store/contract_test.go` - shared store contract tests.
- Create: `pkg/channelv2/worker/pool.go` - bounded worker pool.
- Create: `pkg/channelv2/worker/task.go` - typed task payloads.
- Create: `pkg/channelv2/worker/result.go` - typed completion results and sink.
- Create: `pkg/channelv2/reactor/future.go` - futures for synchronous facade calls.
- Create: `pkg/channelv2/reactor/mailbox.go` - bounded priority mailbox.
- Create: `pkg/channelv2/reactor/router.go` - stable channel hash routing.
- Create: `pkg/channelv2/reactor/event.go` - reactor event definitions.
- Create: `pkg/channelv2/reactor/reactor.go` - non-blocking reactor event loop.
- Create: `pkg/channelv2/reactor/group.go` - multi-reactor lifecycle and completion routing.
- Create: `pkg/channelv2/reactor/backpressure.go` - limit config and admission helpers.
- Create: `pkg/channelv2/service/service.go` - facade wiring.
- Create: `pkg/channelv2/service/meta.go` - `ApplyMeta` facade.
- Create: `pkg/channelv2/service/append.go` - `Append` and `AppendBatch` facade.
- Create: `pkg/channelv2/service/fetch.go` - `Fetch` facade.
- Create: `pkg/channelv2/transport/local.go` - in-memory transport for tests.
- Create: `pkg/channelv2/replication/follower.go` - follower pull/apply/ack planning.
- Create: `pkg/channelv2/replication/leader.go` - leader pull/ack event handling helpers.
- Create: `pkg/channelv2/testkit/cluster.go` - multi-node memory harness.
- Create: `pkg/channelv2/bench_test.go` - v0 append/fetch/replication benchmarks.
- Create: `pkg/channelv2/store/channel_adapter.go` - old `pkg/channel/store` adapter.
- Create: `pkg/channelv2/FLOW.md` - v0 architecture and flows.
- Modify: `AGENTS.md` - add `pkg/channelv2` to the directory structure once the package is added.

## Task 1: Scaffold Public Types And Narrow Interfaces

**Files:**
- Create: `pkg/channelv2/doc.go`
- Create: `pkg/channelv2/errors.go`
- Create: `pkg/channelv2/types.go`
- Create: `pkg/channelv2/channel.go`
- Create: `pkg/channelv2/transport/types.go`
- Create: `pkg/channelv2/store/adapter.go`

- [ ] **Step 1: Write compile-only API tests**

Create `pkg/channelv2/api_test.go`:

```go
package channelv2_test

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/stretchr/testify/require"
)

func TestPublicTypesCompile(t *testing.T) {
	meta := ch.Meta{
		Key:         ch.ChannelKey("1:demo"),
		ID:          ch.ChannelID{ID: "demo", Type: 1},
		Epoch:       2,
		LeaderEpoch: 3,
		Leader:      1,
		Replicas:    []ch.NodeID{1, 2, 3},
		ISR:         []ch.NodeID{1, 2, 3},
		MinISR:      2,
		LeaseUntil:  time.Now().Add(time.Second),
		Status:      ch.StatusActive,
	}
	var cluster ch.Cluster = nopCluster{}
	require.NoError(t, cluster.ApplyMeta(meta))
	_, err := cluster.Append(context.Background(), ch.AppendRequest{ChannelID: meta.ID})
	require.NoError(t, err)
}

type nopCluster struct{}

func (nopCluster) ApplyMeta(ch.Meta) error { return nil }
func (nopCluster) Append(context.Context, ch.AppendRequest) (ch.AppendResult, error) { return ch.AppendResult{}, nil }
func (nopCluster) AppendBatch(context.Context, ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	return ch.AppendBatchResult{}, nil
}
func (nopCluster) Fetch(context.Context, ch.FetchRequest) (ch.FetchResult, error) { return ch.FetchResult{}, nil }
func (nopCluster) Tick(context.Context) error { return nil }
func (nopCluster) Close() error { return nil }
```

- [ ] **Step 2: Run the compile test and verify it fails**

Run: `go test ./pkg/channelv2 -run TestPublicTypesCompile -count=1`

Expected: FAIL because `pkg/channelv2` does not exist yet.

- [ ] **Step 3: Add root package DTOs and errors**

Implement `errors.go`, `types.go`, and `channel.go` with this minimum shape:

```go
package channelv2

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidConfig = errors.New("channelv2: invalid config")
	ErrBackpressured = errors.New("channelv2: backpressured")
	ErrNotLeader     = errors.New("channelv2: not leader")
	ErrNotReady      = errors.New("channelv2: not ready")
	ErrStaleMeta     = errors.New("channelv2: stale meta")
	ErrChannelNotFound = errors.New("channelv2: channel not found")
	ErrClosed        = errors.New("channelv2: closed")
	ErrTooManyChannels = errors.New("channelv2: too many channels")
)

type NodeID uint64
type ChannelKey string

type ChannelID struct { ID string; Type uint8 }
type Status uint8
type Role uint8
type CommitMode uint8

const (
	StatusCreating Status = iota + 1
	StatusActive
	StatusDeleting
	StatusDeleted
)

const (
	RoleFollower Role = iota + 1
	RoleLeader
)

const (
	CommitModeQuorum CommitMode = iota + 1
	CommitModeLocal
)

type Meta struct {
	Key ChannelKey
	ID ChannelID
	Epoch uint64
	LeaderEpoch uint64
	Leader NodeID
	Replicas []NodeID
	ISR []NodeID
	MinISR int
	LeaseUntil time.Time
	Status Status
}

type Message struct {
	MessageID uint64
	MessageSeq uint64
	ChannelID string
	ChannelType uint8
	FromUID string
	ClientMsgNo string
	Payload []byte
}

type OpID uint64
type Fence struct { ChannelKey ChannelKey; Generation uint64; Epoch uint64; LeaderEpoch uint64; OpID OpID }

type Record struct {
	ID uint64
	Index uint64
	Epoch uint64
	Payload []byte
	SizeBytes int
}

type AppendRequest struct { ChannelID ChannelID; Message Message; CommitMode CommitMode; ExpectedChannelEpoch uint64; ExpectedLeaderEpoch uint64 }
type AppendResult struct { MessageID uint64; MessageSeq uint64; Message Message }
type AppendBatchRequest struct { ChannelID ChannelID; Messages []Message; CommitMode CommitMode; ExpectedChannelEpoch uint64; ExpectedLeaderEpoch uint64 }
type AppendBatchResult struct { Items []AppendBatchItemResult }
type AppendBatchItemResult struct { MessageID uint64; MessageSeq uint64; Message Message; Err error }
type FetchRequest struct { ChannelID ChannelID; FromSeq uint64; Limit int; MaxBytes int; ExpectedChannelEpoch uint64; ExpectedLeaderEpoch uint64 }
type FetchResult struct { Messages []Message; NextSeq uint64; CommittedSeq uint64 }
type Checkpoint struct { HW uint64 }

type Cluster interface {
	ApplyMeta(Meta) error
	Append(context.Context, AppendRequest) (AppendResult, error)
	AppendBatch(context.Context, AppendBatchRequest) (AppendBatchResult, error)
	Fetch(context.Context, FetchRequest) (FetchResult, error)
	Tick(context.Context) error
	Close() error
}
```

- [ ] **Step 4: Add store and transport interfaces**

Add `pkg/channelv2/store/adapter.go`:

```go
package store

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

type Factory interface { ChannelStore(key ch.ChannelKey, id ch.ChannelID) (ChannelStore, error) }

type ChannelStore interface {
	Load(ctx context.Context) (InitialState, error)
	AppendLeader(ctx context.Context, req AppendLeaderRequest) (AppendLeaderResult, error)
	ApplyFollower(ctx context.Context, req ApplyFollowerRequest) (ApplyFollowerResult, error)
	ReadCommitted(ctx context.Context, req ReadCommittedRequest) (ReadCommittedResult, error)
	ReadLog(ctx context.Context, req ReadLogRequest) (ReadLogResult, error)
	StoreCheckpoint(ctx context.Context, checkpoint ch.Checkpoint) error
	Close() error
}
```

Add `pkg/channelv2/transport/types.go` with `Client`, `Server`, `PullRequest`, `PullResponse`, and `AckRequest` from the spec.

- [ ] **Step 5: Verify scaffold compiles**

Run: `go test ./pkg/channelv2/... -run TestPublicTypesCompile -count=1`

Expected: PASS.

- [ ] **Step 6: Commit scaffold**

```bash
git add pkg/channelv2
git commit -m "feat: scaffold channelv2 public contracts"
```

## Task 2: Implement Pure Machine Metadata, Progress, And Invariants

**Files:**
- Create: `pkg/channelv2/machine/channel.go`
- Create: `pkg/channelv2/machine/meta.go`
- Create: `pkg/channelv2/machine/progress.go`
- Create: `pkg/channelv2/machine/invariant.go`
- Test: `pkg/channelv2/machine/meta_test.go`
- Test: `pkg/channelv2/machine/progress_test.go`

- [ ] **Step 1: Write failing metadata tests**

Create `pkg/channelv2/machine/meta_test.go`:

```go
package machine

import (
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/stretchr/testify/require"
)

func TestApplyMetaAssignsLeaderRole(t *testing.T) {
	state := NewChannelState(ch.ChannelKey("1:a"), ch.NodeID(1), 1)
	decision := state.ApplyMeta(ch.Meta{Key: state.Key, ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive})
	require.Empty(t, decision.Replies)
	require.Equal(t, ch.RoleLeader, state.Role)
	require.True(t, state.CommitReady)
	require.Equal(t, uint64(0), state.HW)
}

func TestApplyMetaAssignsFollowerRole(t *testing.T) {
	state := NewChannelState(ch.ChannelKey("1:a"), ch.NodeID(2), 1)
	state.ApplyMeta(ch.Meta{Key: state.Key, ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive})
	require.Equal(t, ch.RoleFollower, state.Role)
}

func TestApplyMetaRejectsInvalidMinISR(t *testing.T) {
	state := NewChannelState(ch.ChannelKey("1:a"), ch.NodeID(1), 1)
	decision := state.ApplyMeta(ch.Meta{Key: state.Key, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 2, Status: ch.StatusActive})
	require.ErrorIs(t, decision.Err, ch.ErrInvalidConfig)
}
```

- [ ] **Step 2: Run metadata tests and verify failure**

Run: `go test ./pkg/channelv2/machine -run TestApplyMeta -count=1`

Expected: FAIL because `machine` does not exist.

- [ ] **Step 3: Implement `ChannelState`, `Decision`, and `ApplyMeta`**

Use this structure:

```go
type Decision struct {
	Err error
	Tasks []Task
	Replies []Reply
	Signals []Signal
}

type ChannelState struct {
	Key ch.ChannelKey
	LocalNode ch.NodeID
	Generation uint64
	ID ch.ChannelID
	Epoch uint64
	LeaderEpoch uint64
	Role ch.Role
	Status ch.Status
	Leader ch.NodeID
	Replicas []ch.NodeID
	ISR []ch.NodeID
	MinISR int
	LEO uint64
	HW uint64
	CheckpointHW uint64
	CommitReady bool
	Progress map[ch.NodeID]ReplicaProgress
}
```

`ApplyMeta` must copy slices, validate `MinISR > 0 && MinISR <= len(ISR)`, assign role from `meta.Leader == localNode`, and seed local leader progress to current `LEO`.

- [ ] **Step 4: Write failing HW progress tests**

Create `pkg/channelv2/machine/progress_test.go`:

```go
func TestAdvanceHWUsesISRMinISR(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1, 2, 3}, []ch.NodeID{1, 2, 3}, 2)
	state.Progress[1] = ReplicaProgress{Match: 105}
	state.Progress[2] = ReplicaProgress{Match: 104}
	state.Progress[3] = ReplicaProgress{Match: 80}
	advanced := state.AdvanceHW()
	require.True(t, advanced)
	require.Equal(t, uint64(104), state.HW)
}

func TestAdvanceHWSingleNodeCluster(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	state.Progress[1] = ReplicaProgress{Match: 7}
	require.True(t, state.AdvanceHW())
	require.Equal(t, uint64(7), state.HW)
}
```

- [ ] **Step 5: Implement progress and invariants**

Implement `AdvanceHW`, `IsReplica`, `IsISR`, and `CheckInvariants`. `AdvanceHW` sorts ISR match offsets descending and uses `MinISR-1`.

- [ ] **Step 6: Verify machine metadata/progress tests**

Run: `go test ./pkg/channelv2/machine -run 'TestApplyMeta|TestAdvanceHW' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit machine metadata/progress**

```bash
git add pkg/channelv2/machine
git commit -m "feat: add channelv2 machine metadata and progress"
```

## Task 3: Implement Machine Append And Fetch Decisions

**Files:**
- Modify: `pkg/channelv2/machine/channel.go`
- Create: `pkg/channelv2/machine/append.go`
- Create: `pkg/channelv2/machine/fetch.go`
- Test: `pkg/channelv2/machine/append_test.go`
- Test: `pkg/channelv2/machine/fetch_test.go`

- [ ] **Step 1: Write failing append decision tests**

Create `append_test.go` with tests for not-leader rejection, leader pending append, local commit, and quorum waiter completion:

```go
func TestProposeAppendRejectsFollower(t *testing.T) {
	state := followerState(t, 2, 1)
	decision := state.ProposeAppend(AppendCommand{OpID: 1, Records: []ch.Record{{ID: 10, Payload: []byte("a"), SizeBytes: 1}}})
	require.ErrorIs(t, decision.Err, ch.ErrNotLeader)
}

func TestAppendStoredAdvancesLEOAndCompletesSingleNodeQuorum(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	decision := state.ProposeAppend(AppendCommand{OpID: 1, CommitMode: ch.CommitModeQuorum, Records: []ch.Record{{ID: 10, Payload: []byte("a"), SizeBytes: 1}}})
	require.Len(t, decision.Tasks, 1)
	decision = state.ApplyAppendStored(AppendStoredResult{Fence: decision.Tasks[0].Fence, BaseOffset: 1, LastOffset: 1})
	require.Equal(t, uint64(1), state.LEO)
	require.Equal(t, uint64(1), state.HW)
	require.Len(t, decision.Replies, 1)
}
```

- [ ] **Step 2: Run append tests and verify failure**

Run: `go test ./pkg/channelv2/machine -run TestProposeAppend -count=1`

Expected: FAIL because append decisions are not implemented.

- [ ] **Step 3: Implement append commands, waiters, and stored result application**

Add `AppendCommand`, `AppendStoredResult`, `AppendWaiter`, `TaskKindStoreAppend`, and a minimal task descriptor used by the machine tests. Ensure one `InflightAppend` per channel and return `ErrNotReady` if a second flush is proposed while inflight exists.

- [ ] **Step 4: Write failing fetch decision tests**

Create `fetch_test.go`:

```go
func TestBuildFetchReturnsEmptyAboveHW(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	state.HW = 3
	decision := state.BuildFetch(FetchCommand{OpID: 1, FromSeq: 4, Limit: 10, MaxBytes: 1024})
	require.Len(t, decision.Replies, 1)
	require.Equal(t, uint64(4), decision.Replies[0].Fetch.NextSeq)
}

func TestBuildFetchCreatesReadCommittedTask(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	state.HW = 3
	decision := state.BuildFetch(FetchCommand{OpID: 1, FromSeq: 1, Limit: 10, MaxBytes: 1024})
	require.Len(t, decision.Tasks, 1)
	require.Equal(t, uint64(3), decision.Tasks[0].ReadCommitted.MaxSeq)
}
```

- [ ] **Step 5: Implement fetch decisions**

Implement `FetchCommand` and `BuildFetch`. It must capture `HW` into `MaxSeq`, clamp `FromSeq == 0` to `1`, and return an immediate empty reply when `FromSeq > HW`.

- [ ] **Step 6: Verify machine append/fetch tests**

Run: `go test ./pkg/channelv2/machine -count=1`

Expected: PASS.

- [ ] **Step 7: Commit machine append/fetch**

```bash
git add pkg/channelv2/machine
git commit -m "feat: add channelv2 machine append and fetch decisions"
```

## Task 4: Add Memory Store And Store Contract

**Files:**
- Create: `pkg/channelv2/store/memory.go`
- Create: `pkg/channelv2/store/contract_test.go`
- Test: `pkg/channelv2/store/memory_test.go`

- [ ] **Step 1: Write failing store contract tests**

Create `contract_test.go` with a reusable test helper:

```go
func testStoreContract(t *testing.T, factory Factory) {
	ctx := context.Background()
	cs, err := factory.ChannelStore(ch.ChannelKey("1:a"), ch.ChannelID{ID: "a", Type: 1})
	require.NoError(t, err)
	initial, err := cs.Load(ctx)
	require.NoError(t, err)
	require.Zero(t, initial.LEO)

	appendRes, err := cs.AppendLeader(ctx, AppendLeaderRequest{Records: []ch.Record{{ID: 1, Payload: []byte("a"), SizeBytes: 1}, {ID: 2, Payload: []byte("b"), SizeBytes: 1}}, Sync: true})
	require.NoError(t, err)
	require.Equal(t, uint64(1), appendRes.BaseOffset)
	require.Equal(t, uint64(2), appendRes.LastOffset)

	logRes, err := cs.ReadLog(ctx, ReadLogRequest{FromOffset: 1, MaxOffset: 2, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, logRes.Records, 2)

	committed, err := cs.ReadCommitted(ctx, ReadCommittedRequest{FromSeq: 1, MaxSeq: 1, Limit: 10, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, committed.Messages, 1)
}
```

Add `TestMemoryStoreContract` that calls it.

- [ ] **Step 2: Run store tests and verify failure**

Run: `go test ./pkg/channelv2/store -run TestMemoryStoreContract -count=1`

Expected: FAIL because memory store is not implemented.

- [ ] **Step 3: Implement memory store**

Implement an in-memory `Factory` with a mutex-protected map keyed by `ChannelKey`. `AppendLeader` assigns continuous `Index` values starting at `LEO+1`, stores payload records, and returns base/last offsets. `ApplyFollower` accepts records whose first new offset is `LEO+1` and skips duplicate prefixes already present.

- [ ] **Step 4: Add duplicate-prefix apply test**

Add a contract test that applies records `[1,2]`, then applies `[2,3]`, and verifies record 3 is stored without duplicating record 2.

- [ ] **Step 5: Verify store tests**

Run: `go test ./pkg/channelv2/store -count=1`

Expected: PASS.

- [ ] **Step 6: Commit memory store**

```bash
git add pkg/channelv2/store
git commit -m "feat: add channelv2 memory store contract"
```

## Task 5: Add Bounded Worker Pools

**Files:**
- Create: `pkg/channelv2/worker/pool.go`
- Create: `pkg/channelv2/worker/task.go`
- Create: `pkg/channelv2/worker/result.go`
- Test: `pkg/channelv2/worker/pool_test.go`

- [ ] **Step 1: Write failing worker pool tests**

Create `pool_test.go`:

```go
func TestPoolRunsTaskAndReportsCompletion(t *testing.T) {
	sink := &captureSink{}
	pool, err := NewPool(PoolConfig{Name: "test", Workers: 1, QueueSize: 1}, sink)
	require.NoError(t, err)
	defer pool.Close()

	fence := ch.Fence{ChannelKey: ch.ChannelKey("1:a"), Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 1}
	err = pool.Submit(context.Background(), Task{Kind: TaskFunc, Fence: fence, RunFunc: func(context.Context) Result { return Result{Fence: fence} }})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return sink.Len() == 1 }, time.Second, time.Millisecond)
}

func TestPoolReturnsBackpressureWhenQueueFull(t *testing.T) { /* one worker blocked, queue size one, second queued, third returns ErrBackpressured */ }
```

If a test-only `TaskFunc` kind feels too public, put it in `_test.go` helpers and keep production task kinds explicit.

- [ ] **Step 2: Run worker tests and verify failure**

Run: `go test ./pkg/channelv2/worker -run TestPool -count=1`

Expected: FAIL because worker package is not implemented.

- [ ] **Step 3: Implement bounded pool**

Implement:

```go
type CompletionSink interface { Complete(Result) }
type Pool struct { queue chan Task; stop chan struct{}; wg sync.WaitGroup }
func NewPool(cfg PoolConfig, sink CompletionSink) (*Pool, error)
func (p *Pool) Submit(ctx context.Context, task Task) error
func (p *Pool) Close() error
```

`Submit` must return `channelv2.ErrBackpressured` on a full queue and `channelv2.ErrClosed` after close.

- [ ] **Step 4: Add typed task payloads**

Add task kinds for store append, store apply, store read committed, store read log, RPC pull, and RPC ack. Do not wire dependencies yet; the reactor will choose the correct runner.

- [ ] **Step 5: Verify worker tests**

Run: `go test ./pkg/channelv2/worker -count=1`

Expected: PASS.

- [ ] **Step 6: Commit worker pools**

```bash
git add pkg/channelv2/worker
git commit -m "feat: add channelv2 bounded worker pools"
```

## Task 6: Add Reactor Primitives: Future, Router, Mailbox

**Files:**
- Create: `pkg/channelv2/reactor/future.go`
- Create: `pkg/channelv2/reactor/router.go`
- Create: `pkg/channelv2/reactor/mailbox.go`
- Create: `pkg/channelv2/reactor/event.go`
- Test: `pkg/channelv2/reactor/future_test.go`
- Test: `pkg/channelv2/reactor/router_test.go`
- Test: `pkg/channelv2/reactor/mailbox_test.go`

- [ ] **Step 1: Write failing future tests**

Create `future_test.go` covering complete once, await success, await context cancellation, and double complete ignored.

- [ ] **Step 2: Implement future**

Implement a small `Future` with a buffered result channel and `sync.Once`:

```go
type Future struct { once sync.Once; ch chan Result }
func NewFuture() *Future
func (f *Future) Complete(Result)
func (f *Future) Await(ctx context.Context) (Result, error)
```

- [ ] **Step 3: Write failing router tests**

Create `router_test.go` proving the same channel key always maps to the same reactor index and all indexes are in bounds.

- [ ] **Step 4: Implement FNV router**

Implement `PickIndex(key channelv2.ChannelKey) int` using `hash/fnv` and configured reactor count.

- [ ] **Step 5: Write failing priority mailbox tests**

Create `mailbox_test.go` proving high-priority events drain before normal, normal queue returns backpressure when full, and low ticks can be coalesced or dropped.

- [ ] **Step 6: Implement bounded priority mailbox**

Implement `Submit(priority, event)` and `Drain(max int) []Event`. Use three buffered channels or mutex-protected ring queues. Keep the implementation simple and deterministic for tests.

- [ ] **Step 7: Verify reactor primitive tests**

Run: `go test ./pkg/channelv2/reactor -run 'TestFuture|TestRouter|TestMailbox' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit reactor primitives**

```bash
git add pkg/channelv2/reactor
git commit -m "feat: add channelv2 reactor primitives"
```

## Task 7: Add Reactor Group And Single-Node Service Loop

**Files:**
- Create: `pkg/channelv2/reactor/reactor.go`
- Create: `pkg/channelv2/reactor/group.go`
- Create: `pkg/channelv2/reactor/backpressure.go`
- Create: `pkg/channelv2/service/service.go`
- Create: `pkg/channelv2/service/meta.go`
- Create: `pkg/channelv2/service/append.go`
- Create: `pkg/channelv2/service/fetch.go`
- Test: `pkg/channelv2/service/service_test.go`
- Test: `pkg/channelv2/reactor/group_test.go`

- [ ] **Step 1: Write failing single-node service test**

Create `pkg/channelv2/service/service_test.go`:

```go
func TestSingleNodeAppendFetchCommitted(t *testing.T) {
	factory := store.NewMemoryFactory()
	cluster, err := New(Config{LocalNode: 1, Store: factory, ReactorCount: 1})
	require.NoError(t, err)
	defer cluster.Close()

	meta := ch.Meta{Key: ch.ChannelKey("1:a"), ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	require.NoError(t, cluster.ApplyMeta(meta))

	appendRes, err := cluster.Append(context.Background(), ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{Payload: []byte("hello")}})
	require.NoError(t, err)
	require.Equal(t, uint64(1), appendRes.MessageSeq)

	fetchRes, err := cluster.Fetch(context.Background(), ch.FetchRequest{ChannelID: meta.ID, FromSeq: 1, Limit: 10, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, fetchRes.Messages, 1)
	require.Equal(t, uint64(1), fetchRes.CommittedSeq)
}
```

- [ ] **Step 2: Run service test and verify failure**

Run: `go test ./pkg/channelv2/service -run TestSingleNodeAppendFetchCommitted -count=1`

Expected: FAIL because service/reactor loop is not implemented.

- [ ] **Step 3: Implement reactor group config and lifecycle**

Implement `reactor.Group` with:

```go
type Config struct {
	LocalNode ch.NodeID
	ReactorCount int
	MailboxSize int
	Store store.Factory
}
func NewGroup(cfg Config) (*Group, error)
func (g *Group) Submit(ctx context.Context, key ch.ChannelKey, event Event) (*Future, error)
func (g *Group) Complete(worker.Result)
func (g *Group) Close() error
```

- [ ] **Step 4: Implement minimal reactor event loop**

A reactor must handle `ApplyMetaEvent`, `AppendEvent`, `FetchEvent`, and `WorkerResultEvent`. For v0, store tasks may run through the worker pool or a direct fake runner only if the next step replaces it before tests pass. Prefer using the real worker pool immediately.

- [ ] **Step 5: Implement service facade**

`service.New` validates config, creates worker pools and reactor group, and returns a struct implementing `channelv2.Cluster`. `ApplyMeta` computes `meta.Key` if empty using `ChannelID`, then submits `ApplyMetaEvent`. `Append` wraps `AppendBatch` with one message. `Fetch` submits `FetchEvent`.

- [ ] **Step 6: Add backpressure test**

Add `TestServiceAppendReturnsBackpressureWhenMailboxFull` with a tiny mailbox and blocked reactor. Expected error: `channelv2.ErrBackpressured`.

- [ ] **Step 7: Verify service and reactor tests**

Run: `go test ./pkg/channelv2/reactor ./pkg/channelv2/service -count=1`

Expected: PASS.

- [ ] **Step 8: Commit single-node service loop**

```bash
git add pkg/channelv2/reactor pkg/channelv2/service
git commit -m "feat: add channelv2 single-node reactor service"
```

## Task 8: Add Replication V0 And Memory Transport

**Files:**
- Create: `pkg/channelv2/transport/local.go`
- Create: `pkg/channelv2/replication/follower.go`
- Create: `pkg/channelv2/replication/leader.go`
- Create: `pkg/channelv2/testkit/cluster.go`
- Test: `pkg/channelv2/testkit/cluster_test.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/service/service.go`

- [ ] **Step 1: Write failing three-node commit test**

Create `pkg/channelv2/testkit/cluster_test.go`:

```go
func TestThreeNodeClusterCommitsWithMinISR2(t *testing.T) {
	h := NewClusterHarness(t, []ch.NodeID{1, 2, 3})
	defer h.Close()
	meta := ch.Meta{Key: ch.ChannelKey("1:a"), ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
	h.ApplyMetaToAll(meta)

	res, err := h.Nodes[1].Append(context.Background(), ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{Payload: []byte("hello")}})
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.MessageSeq)
	h.WaitCommitted(t, 2, meta.ID, 1, time.Second)
}
```

- [ ] **Step 2: Run three-node test and verify failure**

Run: `go test ./pkg/channelv2/testkit -run TestThreeNodeClusterCommitsWithMinISR2 -count=1`

Expected: FAIL because replication/testkit is not implemented.

- [ ] **Step 3: Implement local transport**

`transport.LocalNetwork` maps `NodeID` to `transport.Server`. `Client.Pull` and `Client.Ack` call the registered server. Include failure injection fields for dropped pull and dropped ack, but only wire the happy path first.

- [ ] **Step 4: Add server methods to service**

Make `service.Cluster` implement `transport.Server` with `HandlePull` and `HandleAck`. Each method submits a reactor event to the leader node and waits for the reply.

- [ ] **Step 5: Implement follower pull/apply/ack loop**

On `ApplyMeta` for follower role, mark replication dirty. On `Tick` or a dirty wake, submit `RPCPullTask` if no pull is inflight. On `PullResponse`, submit `StoreApplyTask`. On `StoreApplyResult`, update follower `LEO/HW` and submit `AckTask`.

- [ ] **Step 6: Implement leader pull and ack handling**

Leader `PullRequestEvent` validates role/epoch/follower membership and reads log records through store. Leader `AckEvent` updates progress, recomputes `HW`, and completes waiters.

- [ ] **Step 7: Add failure/backoff tests**

Add tests for dropped pull recovery, stale epoch pull rejection, and non-ISR ACK not advancing HW.

- [ ] **Step 8: Verify replication tests**

Run: `go test ./pkg/channelv2/... -run 'TestThreeNode|Test.*Replication|Test.*Ack|Test.*Pull' -count=1`

Expected: PASS.

- [ ] **Step 9: Commit replication v0**

```bash
git add pkg/channelv2/transport pkg/channelv2/replication pkg/channelv2/testkit pkg/channelv2/reactor pkg/channelv2/service
git commit -m "feat: add channelv2 v0 replication loop"
```

## Task 9: Add Old Store Adapter Contract

**Files:**
- Create: `pkg/channelv2/store/channel_adapter.go`
- Test: `pkg/channelv2/store/channel_adapter_test.go`

- [ ] **Step 1: Inspect old store construction**

Read `pkg/channel/store/engine.go`, `pkg/channel/store/channel_store.go`, `pkg/channel/store/message_log.go`, and existing tests such as `pkg/channel/store/channel_store_test.go`.

- [ ] **Step 2: Write failing old adapter contract test**

Create `channel_adapter_test.go` that builds a temporary old store engine and calls the same `testStoreContract(t, adapterFactory)` helper used by memory store.

Run: `go test ./pkg/channelv2/store -run TestOldStoreAdapterContract -count=1`

Expected: FAIL because adapter is not implemented.

- [ ] **Step 3: Implement adapter with strict import boundary**

Only `channel_adapter.go` may import old packages. Convert between `channelv2.Record` and `pkg/channel.Record`, and between old messages and `channelv2.Message`. Prefer existing old-store APIs over duplicating storage logic.

- [ ] **Step 4: Add boundary check script command to plan verification**

Use this command manually after implementation:

```bash
rg 'pkg/channel"|pkg/channel/store' pkg/channelv2 -g'*.go'
```

Expected: only `pkg/channelv2/store/channel_adapter.go` and its test mention old imports.

- [ ] **Step 5: Verify old store adapter tests**

Run: `go test ./pkg/channelv2/store -run 'TestMemoryStoreContract|TestOldStoreAdapterContract' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit old store adapter**

```bash
git add pkg/channelv2/store
git commit -m "feat: add channelv2 old store adapter"
```

## Task 10: Add Benchmarks And Documentation

**Files:**
- Create: `pkg/channelv2/bench_test.go`
- Create: `pkg/channelv2/FLOW.md`
- Modify: `AGENTS.md`

- [ ] **Step 1: Add benchmark scaffolding**

Create benchmarks:

```go
func BenchmarkAppendSingleNodeManyChannels(b *testing.B) { /* N channels, memory store, CommitModeQuorum */ }
func BenchmarkAppendSingleNodeHotChannel(b *testing.B) { /* one channel, memory store */ }
func BenchmarkAppendThreeNodeManyChannelsMemoryTransport(b *testing.B) { /* testkit cluster, MinISR=2 */ }
func BenchmarkFetchCommittedManyChannels(b *testing.B) { /* preloaded committed records */ }
```

- [ ] **Step 2: Run smoke benchmark**

Run: `go test ./pkg/channelv2 -run '^$' -bench BenchmarkAppendSingleNodeHotChannel -benchtime=1s -count=1`

Expected: PASS and report `ns/op`.

- [ ] **Step 3: Write `pkg/channelv2/FLOW.md`**

Document:

- v0 responsibilities and non-goals.
- Package boundaries.
- `ApplyMeta`, append, fetch, pull, ACK, and HW flows.
- Store adapter import boundary.
- Backpressure behavior.

- [ ] **Step 4: Update `AGENTS.md` directory structure**

Add:

```text
pkg/
  channelv2/             Experimental multiple-reactor channel log runtime for v0 append/fetch/replication validation
```

Keep the entry short and consistent with existing directory structure text.

- [ ] **Step 5: Verify package tests**

Run: `go test ./pkg/channelv2/... -count=1`

Expected: PASS.

- [ ] **Step 6: Verify targeted broader tests**

Run: `go test ./pkg/channel/... ./pkg/channelv2/... -count=1`

Expected: PASS. If old `pkg/channel` tests are too slow or unrelated failures appear, record exact failures and still keep `go test ./pkg/channelv2/...` as the gating command for this experimental package.

- [ ] **Step 7: Commit docs and benchmarks**

```bash
git add pkg/channelv2 AGENTS.md
git commit -m "test: add channelv2 benchmarks and flow docs"
```

## Final Verification

- [ ] **Step 1: Check old import boundary**

Run:

```bash
rg 'pkg/channel"|pkg/channel/store' pkg/channelv2 -g'*.go'
```

Expected: old imports appear only in `pkg/channelv2/store/channel_adapter.go` and `pkg/channelv2/store/channel_adapter_test.go`.

- [ ] **Step 2: Run all channelv2 tests**

Run: `go test ./pkg/channelv2/... -count=1`

Expected: PASS.

- [ ] **Step 3: Run race tests for channelv2**

Run: `go test -race ./pkg/channelv2/... -count=1`

Expected: PASS. If race tests are too slow on the current machine, run at least `go test -race ./pkg/channelv2/reactor ./pkg/channelv2/service ./pkg/channelv2/testkit -count=1` and record the skipped packages.

- [ ] **Step 4: Run benchmark smoke**

Run: `go test ./pkg/channelv2 -run '^$' -bench 'BenchmarkAppendSingleNodeHotChannel|BenchmarkAppendThreeNodeManyChannelsMemoryTransport' -benchtime=1s -count=1`

Expected: PASS with benchmark output.

- [ ] **Step 5: Check working tree**

Run: `git status --short`

Expected: only intentional changes, or clean after final commit.

## Handoff Notes

- Do not optimize the mailbox or group commit before the correctness tests pass.
- Do not copy old `pkg/channel/replica` or `pkg/channel/runtime` code into `pkg/channelv2`.
- If a feature needs migration, retention, snapshot, or leader repair semantics, stop and write a follow-up design rather than expanding v0 scope.
- If the old store adapter exposes hidden assumptions that make the interface too narrow, update `docs/superpowers/specs/2026-05-23-channelv2-reactor-design.md` before widening the interface.
