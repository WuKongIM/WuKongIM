# Channelv2 P0 Test Coverage Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the P0 tests from the channelv2 test strategy and enforce request epoch fences that are currently exposed but untested.

**Architecture:** Keep the work inside `pkg/channelv2` and use existing package-level test helpers where possible. Add deterministic unit tests for `machine`, `service`, and `transport`; only change reactor implementation for `ExpectedChannelEpoch` and `ExpectedLeaderEpoch` validation.

**Tech Stack:** Go `testing`, `testify/require`, existing `pkg/channelv2` reactor/service/store/transport packages.

---

## Scope

This plan implements only P0 from
`docs/superpowers/specs/2026-05-25-channelv2-test-strategy-design.md`.

Do not modify unrelated staged work. At plan creation time,
`pkg/channelv2/FLOW.md` already had unrelated staged changes. Avoid touching it
unless the final code behavior makes the flow document clearly inconsistent.

## Files

- Create: `pkg/channelv2/machine/invariant_test.go`
  - Covers `CheckInvariants`.
- Modify: `pkg/channelv2/machine/fetch_test.go`
  - Adds `ApplyReadCommitted` error/default/trim coverage.
- Modify: `pkg/channelv2/machine/append_test.go`
  - Adds store append error coverage.
- Modify: `pkg/channelv2/service/service_test.go`
  - Adds `HandleAck` quorum completion test.
  - Adds public append/fetch expected epoch fence tests.
- Modify: `pkg/channelv2/transport/local_test.go`
  - Adds direct Pull/Ack/Notify route, drop, and counter coverage.
- Modify: `pkg/channelv2/reactor/reactor.go`
  - Enforces `ExpectedChannelEpoch` and `ExpectedLeaderEpoch` on append/fetch.

## Task 1: Machine Invariant Tests

**Files:**
- Create: `pkg/channelv2/machine/invariant_test.go`

- [ ] **Step 1: Write the tests**

Add:

```go
package machine

import (
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/stretchr/testify/require"
)

func TestCheckInvariantsAllowsValidOrdering(t *testing.T) {
	state := &ChannelState{CheckpointHW: 2, HW: 3, LEO: 4}
	require.NoError(t, state.CheckInvariants())
}

func TestCheckInvariantsRejectsCheckpointAboveHW(t *testing.T) {
	state := &ChannelState{CheckpointHW: 4, HW: 3, LEO: 4}
	require.ErrorIs(t, state.CheckInvariants(), ch.ErrInvalidConfig)
}

func TestCheckInvariantsRejectsHWAboveLEO(t *testing.T) {
	state := &ChannelState{CheckpointHW: 2, HW: 5, LEO: 4}
	require.ErrorIs(t, state.CheckInvariants(), ch.ErrInvalidConfig)
}
```

- [ ] **Step 2: Run focused tests**

Run:

```sh
go test ./pkg/channelv2/machine -run 'TestCheckInvariants' -count=1
```

Expected: PASS. These tests cover existing behavior.

- [ ] **Step 3: Commit**

```sh
git add pkg/channelv2/machine/invariant_test.go
git commit -m "test(channelv2): cover machine invariants"
```

## Task 2: Machine Fetch Result Tests

**Files:**
- Modify: `pkg/channelv2/machine/fetch_test.go`

- [ ] **Step 1: Add `ApplyReadCommitted` tests**

Append tests:

```go
func TestApplyReadCommittedReturnsStoreError(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	state.HW = 3
	fence := ch.Fence{ChannelKey: state.Key, Generation: state.Generation, Epoch: state.Epoch, LeaderEpoch: state.LeaderEpoch, OpID: 7}

	decision := state.ApplyReadCommitted(ReadCommittedResult{Fence: fence, Err: ch.ErrNotReady})

	require.Len(t, decision.Replies, 1)
	require.Equal(t, ReplyKindFetch, decision.Replies[0].Kind)
	require.Equal(t, ch.OpID(7), decision.Replies[0].OpID)
	require.ErrorIs(t, decision.Replies[0].Err, ch.ErrNotReady)
}

func TestApplyReadCommittedTrimsMessagesAboveHW(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	state.HW = 2
	fence := ch.Fence{ChannelKey: state.Key, Generation: state.Generation, Epoch: state.Epoch, LeaderEpoch: state.LeaderEpoch, OpID: 8}

	decision := state.ApplyReadCommitted(ReadCommittedResult{
		Fence: fence,
		Messages: []ch.Message{
			{MessageSeq: 1, Payload: []byte("a")},
			{MessageSeq: 3, Payload: []byte("future")},
		},
		NextSeq: 4,
	})

	require.Len(t, decision.Replies, 1)
	require.Len(t, decision.Replies[0].Fetch.Messages, 1)
	require.Equal(t, uint64(1), decision.Replies[0].Fetch.Messages[0].MessageSeq)
	require.Equal(t, uint64(4), decision.Replies[0].Fetch.NextSeq)
	require.Equal(t, uint64(2), decision.Replies[0].Fetch.CommittedSeq)
}

func TestApplyReadCommittedDefaultsNextSeqWhenStoreOmitsIt(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	state.HW = 5
	fence := ch.Fence{ChannelKey: state.Key, Generation: state.Generation, Epoch: state.Epoch, LeaderEpoch: state.LeaderEpoch, OpID: 9}

	decision := state.ApplyReadCommitted(ReadCommittedResult{Fence: fence})

	require.Len(t, decision.Replies, 1)
	require.Equal(t, uint64(6), decision.Replies[0].Fetch.NextSeq)
	require.Equal(t, uint64(5), decision.Replies[0].Fetch.CommittedSeq)
}
```

Existing `TestReadCommittedIgnoresStaleFence` already covers stale fence. Do
not duplicate it unless it misses the assertion you need.

- [ ] **Step 2: Run focused tests**

Run:

```sh
go test ./pkg/channelv2/machine -run 'TestApplyReadCommitted|TestReadCommitted' -count=1
```

Expected: PASS. These tests cover existing behavior.

- [ ] **Step 3: Commit**

```sh
git add pkg/channelv2/machine/fetch_test.go
git commit -m "test(channelv2): cover committed read results"
```

## Task 3: Machine Append Store Error Test

**Files:**
- Modify: `pkg/channelv2/machine/append_test.go`

- [ ] **Step 1: Add the failing-path test**

Append:

```go
func TestAppendStoredErrorFailsInflightWaitersAndPreservesOffsets(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1, 2}, []ch.NodeID{1, 2}, 2)
	decision := state.ProposeAppendBatch(AppendBatchCommand{
		BatchOpID: 100,
		Waiters: []AppendBatchWaiter{
			{OpID: 1, CommitMode: ch.CommitModeQuorum, Records: []ch.Record{{ID: 10, Payload: []byte("a"), SizeBytes: 1}}},
			{OpID: 2, CommitMode: ch.CommitModeLocal, Records: []ch.Record{{ID: 11, Payload: []byte("b"), SizeBytes: 1}}},
		},
	})
	require.Len(t, decision.Tasks, 1)

	decision = state.ApplyAppendStored(AppendStoredResult{
		Fence: decision.Tasks[0].Fence,
		Err:   ch.ErrNotReady,
	})

	require.Equal(t, uint64(0), state.LEO)
	require.Equal(t, uint64(0), state.HW)
	require.Nil(t, state.InflightAppend)
	require.Empty(t, state.PendingAppends)
	require.Empty(t, state.PendingAppendOrder)
	require.Len(t, decision.Replies, 2)
	require.ErrorIs(t, decision.Replies[0].Err, ch.ErrNotReady)
	require.ErrorIs(t, decision.Replies[1].Err, ch.ErrNotReady)
}
```

- [ ] **Step 2: Run focused tests**

Run:

```sh
go test ./pkg/channelv2/machine -run 'TestAppendStoredError' -count=1
```

Expected: PASS. This covers existing behavior.

- [ ] **Step 3: Commit**

```sh
git add pkg/channelv2/machine/append_test.go
git commit -m "test(channelv2): cover append store errors"
```

## Task 4: Service HandleAck Quorum Completion Test

**Files:**
- Modify: `pkg/channelv2/service/service_test.go`

- [ ] **Step 1: Add the test**

Append near the replication service tests:

```go
func TestHandleAckAdvancesHWAndCompletesQuorumAppend(t *testing.T) {
	factory := store.NewMemoryFactory()
	clusterAPI, err := New(Config{LocalNode: 1, Store: factory, ReactorCount: 1})
	require.NoError(t, err)
	defer clusterAPI.Close()
	svc := clusterAPI.(*cluster)

	meta := ch.Meta{
		Key: ch.ChannelKey("1:ack-quorum"), ID: ch.ChannelID{ID: "ack-quorum", Type: 1},
		Epoch: 1, LeaderEpoch: 1, Leader: 1,
		Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2,
		Status: ch.StatusActive,
	}
	require.NoError(t, svc.ApplyMeta(meta))

	appendDone := make(chan appendOutcome, 1)
	go func() {
		res, err := svc.Append(context.Background(), ch.AppendRequest{
			ChannelID: meta.ID,
			Message:   ch.Message{MessageID: 10, Payload: []byte("hello")},
		})
		appendDone <- appendOutcome{result: res, err: err}
	}()

	require.Eventually(t, func() bool {
		select {
		case outcome := <-appendDone:
			t.Fatalf("append completed before follower ack: result=%+v err=%v", outcome.result, outcome.err)
			return false
		default:
		}
		return awaitServiceTick(t, svc, meta.Key, time.Now())
	}, time.Second, time.Millisecond)

	require.NoError(t, svc.HandleAck(context.Background(), transport.AckRequest{
		ChannelKey: meta.Key,
		Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
		Follower: 2, MatchOffset: 1,
	}))

	select {
	case outcome := <-appendDone:
		require.NoError(t, outcome.err)
		require.Equal(t, uint64(1), outcome.result.MessageSeq)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for quorum append")
	}

	fetch, err := svc.Fetch(context.Background(), ch.FetchRequest{ChannelID: meta.ID, FromSeq: 1, Limit: 1, MaxBytes: 1024})
	require.NoError(t, err)
	require.Equal(t, uint64(1), fetch.CommittedSeq)
	require.Len(t, fetch.Messages, 1)
}
```

If `appendOutcome` is already defined in `service_test.go`, reuse it. If the
name exists in another package only, define it once in `service_test.go`.

- [ ] **Step 2: Run focused test**

Run:

```sh
go test ./pkg/channelv2/service -run TestHandleAckAdvancesHWAndCompletesQuorumAppend -count=1
```

Expected: PASS.

- [ ] **Step 3: Commit**

```sh
git add pkg/channelv2/service/service_test.go
git commit -m "test(channelv2): cover service ack quorum"
```

## Task 5: Expected Epoch Fence Tests

**Files:**
- Modify: `pkg/channelv2/service/service_test.go`

- [ ] **Step 1: Add append/fetch stale expected epoch tests**

Append:

```go
func TestAppendRejectsStaleExpectedEpochs(t *testing.T) {
	clusterAPI, err := New(Config{LocalNode: 1, Store: store.NewMemoryFactory(), ReactorCount: 1})
	require.NoError(t, err)
	defer clusterAPI.Close()

	meta := ch.Meta{
		Key: ch.ChannelKey("1:expected-append"), ID: ch.ChannelID{ID: "expected-append", Type: 1},
		Epoch: 2, LeaderEpoch: 3, Leader: 1,
		Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1,
		Status: ch.StatusActive,
	}
	require.NoError(t, clusterAPI.ApplyMeta(meta))

	_, err = clusterAPI.Append(context.Background(), ch.AppendRequest{
		ChannelID: meta.ID,
		Message: ch.Message{Payload: []byte("stale-channel")},
		ExpectedChannelEpoch: 1,
		ExpectedLeaderEpoch: meta.LeaderEpoch,
	})
	require.ErrorIs(t, err, ch.ErrStaleMeta)

	_, err = clusterAPI.Append(context.Background(), ch.AppendRequest{
		ChannelID: meta.ID,
		Message: ch.Message{Payload: []byte("stale-leader")},
		ExpectedChannelEpoch: meta.Epoch,
		ExpectedLeaderEpoch: 2,
	})
	require.ErrorIs(t, err, ch.ErrStaleMeta)

	res, err := clusterAPI.Append(context.Background(), ch.AppendRequest{
		ChannelID: meta.ID,
		Message: ch.Message{Payload: []byte("current")},
		ExpectedChannelEpoch: meta.Epoch,
		ExpectedLeaderEpoch: meta.LeaderEpoch,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.MessageSeq)
}

func TestFetchRejectsStaleExpectedEpochs(t *testing.T) {
	clusterAPI, err := New(Config{LocalNode: 1, Store: store.NewMemoryFactory(), ReactorCount: 1})
	require.NoError(t, err)
	defer clusterAPI.Close()

	meta := ch.Meta{
		Key: ch.ChannelKey("1:expected-fetch"), ID: ch.ChannelID{ID: "expected-fetch", Type: 1},
		Epoch: 2, LeaderEpoch: 3, Leader: 1,
		Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1,
		Status: ch.StatusActive,
	}
	require.NoError(t, clusterAPI.ApplyMeta(meta))
	_, err = clusterAPI.Append(context.Background(), ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{Payload: []byte("seed")}})
	require.NoError(t, err)

	_, err = clusterAPI.Fetch(context.Background(), ch.FetchRequest{
		ChannelID: meta.ID, FromSeq: 1, Limit: 1, MaxBytes: 1024,
		ExpectedChannelEpoch: 1,
		ExpectedLeaderEpoch: meta.LeaderEpoch,
	})
	require.ErrorIs(t, err, ch.ErrStaleMeta)

	_, err = clusterAPI.Fetch(context.Background(), ch.FetchRequest{
		ChannelID: meta.ID, FromSeq: 1, Limit: 1, MaxBytes: 1024,
		ExpectedChannelEpoch: meta.Epoch,
		ExpectedLeaderEpoch: 2,
	})
	require.ErrorIs(t, err, ch.ErrStaleMeta)
}
```

- [ ] **Step 2: Run tests and verify they fail first**

Run:

```sh
go test ./pkg/channelv2/service -run 'TestAppendRejectsStaleExpectedEpochs|TestFetchRejectsStaleExpectedEpochs' -count=1
```

Expected before implementation: FAIL because expected epoch fields are exposed
but not enforced.

## Task 6: Enforce Expected Epochs In Reactor

**Files:**
- Modify: `pkg/channelv2/reactor/reactor.go`

- [ ] **Step 1: Add append expected epoch validation**

In `handleAppend`, after deleted/status/role/ready checks and before records
are built, add:

```go
	if event.Append.ExpectedChannelEpoch != 0 && event.Append.ExpectedChannelEpoch != rc.state.Epoch {
		event.Future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
	if event.Append.ExpectedLeaderEpoch != 0 && event.Append.ExpectedLeaderEpoch != rc.state.LeaderEpoch {
		event.Future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
```

- [ ] **Step 2: Add fetch expected epoch validation**

In `handleFetch`, after `lookup` succeeds and before `BuildFetch`, add:

```go
	if event.Fetch.ExpectedChannelEpoch != 0 && event.Fetch.ExpectedChannelEpoch != rc.state.Epoch {
		event.Future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
	if event.Fetch.ExpectedLeaderEpoch != 0 && event.Fetch.ExpectedLeaderEpoch != rc.state.LeaderEpoch {
		event.Future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
```

- [ ] **Step 3: Run focused tests**

Run:

```sh
go test ./pkg/channelv2/service -run 'TestAppendRejectsStaleExpectedEpochs|TestFetchRejectsStaleExpectedEpochs' -count=1
```

Expected after implementation: PASS.

- [ ] **Step 4: Run reactor/service focused tests**

Run:

```sh
go test ./pkg/channelv2/reactor ./pkg/channelv2/service -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add pkg/channelv2/reactor/reactor.go pkg/channelv2/service/service_test.go
git commit -m "fix(channelv2): enforce expected epoch fences"
```

## Task 7: LocalNetwork Transport Tests

**Files:**
- Modify: `pkg/channelv2/transport/local_test.go`

- [ ] **Step 1: Add a recording server helper**

If no equivalent helper exists, add:

```go
type recordingServer struct {
	pullReqs     []PullRequest
	ackReqs      []AckRequest
	pullHintReqs []PullHintRequest
	notifyReqs   []NotifyRequest
}

func (s *recordingServer) HandlePull(ctx context.Context, req PullRequest) (PullResponse, error) {
	s.pullReqs = append(s.pullReqs, req)
	return PullResponse{ChannelKey: req.ChannelKey, Epoch: req.Epoch, LeaderEpoch: req.LeaderEpoch, LeaderHW: req.NextOffset - 1, LeaderLEO: req.NextOffset - 1}, nil
}

func (s *recordingServer) HandleAck(ctx context.Context, req AckRequest) error {
	s.ackReqs = append(s.ackReqs, req)
	return nil
}

func (s *recordingServer) HandlePullHint(ctx context.Context, req PullHintRequest) error {
	s.pullHintReqs = append(s.pullHintReqs, req)
	return nil
}

func (s *recordingServer) HandleNotify(ctx context.Context, req NotifyRequest) error {
	s.notifyReqs = append(s.notifyReqs, req)
	return nil
}
```

If concurrent tests reuse this helper, protect slices with a mutex. For direct
single-threaded tests, simple slices are enough.

- [ ] **Step 2: Add route and missing-server tests**

Add tests covering:

```go
func TestLocalNetworkClientReturnsUsableClient(t *testing.T) {
	network := NewLocalNetwork()
	require.Same(t, network, network.Client())
}

func TestLocalNetworkRoutesPullAckPullHintAndNotify(t *testing.T) {
	network := NewLocalNetwork()
	server := &recordingServer{}
	network.Register(2, server)
	client := network.Client()
	key := ch.ChannelKey("1:route")
	id := ch.ChannelID{ID: "route", Type: 1}

	_, err := client.Pull(context.Background(), 2, PullRequest{ChannelKey: key, ChannelID: id, Epoch: 1, LeaderEpoch: 2, Follower: 3, NextOffset: 1, MaxBytes: 1024})
	require.NoError(t, err)
	require.NoError(t, client.Ack(context.Background(), 2, AckRequest{ChannelKey: key, Epoch: 1, LeaderEpoch: 2, Follower: 3, MatchOffset: 1}))
	require.NoError(t, client.PullHint(context.Background(), 2, PullHintRequest{ChannelKey: key, ChannelID: id, Epoch: 1, LeaderEpoch: 2, Leader: 1, LeaderLEO: 1, ActivityVersion: 1, Reason: PullHintReasonAppend}))
	require.NoError(t, client.Notify(context.Background(), 2, NotifyRequest{ChannelKey: key, ChannelID: id, Epoch: 1, LeaderEpoch: 2, Leader: 1, LeaderLEO: 1}))

	require.Len(t, server.pullReqs, 1)
	require.Len(t, server.ackReqs, 1)
	require.Len(t, server.pullHintReqs, 1)
	require.Len(t, server.notifyReqs, 1)
}

func TestLocalNetworkMissingServerReturnsChannelNotFound(t *testing.T) {
	network := NewLocalNetwork()
	_, err := network.Pull(context.Background(), 99, PullRequest{NextOffset: 1})
	require.ErrorIs(t, err, ch.ErrChannelNotFound)
	require.ErrorIs(t, network.Ack(context.Background(), 99, AckRequest{}), ch.ErrChannelNotFound)
	require.ErrorIs(t, network.PullHint(context.Background(), 99, PullHintRequest{}), ch.ErrChannelNotFound)
	require.ErrorIs(t, network.Notify(context.Background(), 99, NotifyRequest{}), ch.ErrChannelNotFound)
}
```

- [ ] **Step 3: Add drop/counter tests**

Add tests covering:

```go
func TestLocalNetworkDropPullAndAckCounters(t *testing.T) {
	network := NewLocalNetwork()
	network.Register(1, &recordingServer{})
	network.SetDropPull(1, true)
	network.SetDropAck(1, true)

	_, err := network.Pull(context.Background(), 1, PullRequest{NextOffset: 1})
	require.ErrorIs(t, err, ch.ErrNotReady)
	require.ErrorIs(t, network.Ack(context.Background(), 1, AckRequest{}), ch.ErrNotReady)
	require.Equal(t, 1, network.DroppedPulls(1))
	require.Equal(t, 1, network.DroppedAcks(1))

	network.SetDropPull(1, false)
	network.SetDropAck(1, false)
	_, err = network.Pull(context.Background(), 1, PullRequest{NextOffset: 1})
	require.NoError(t, err)
	require.NoError(t, network.Ack(context.Background(), 1, AckRequest{}))
}

func TestLocalNetworkDropPullHintAndLegacyNotifyCounters(t *testing.T) {
	network := NewLocalNetwork()
	network.Register(1, &recordingServer{})
	network.SetDropPullHint(1, true)
	require.ErrorIs(t, network.PullHint(context.Background(), 1, PullHintRequest{}), ch.ErrNotReady)
	require.ErrorIs(t, network.Notify(context.Background(), 1, NotifyRequest{}), ch.ErrNotReady)
	require.Equal(t, 2, network.DroppedPullHints(1))

	network.SetDropPullHint(1, false)
	network.SetDropNotify(1, true)
	require.ErrorIs(t, network.Notify(context.Background(), 1, NotifyRequest{}), ch.ErrNotReady)
	require.Equal(t, 3, network.DroppedPullHints(1))

	network.SetDropNotify(1, false)
	require.NoError(t, network.PullHint(context.Background(), 1, PullHintRequest{}))
	require.NoError(t, network.Notify(context.Background(), 1, NotifyRequest{}))
}
```

- [ ] **Step 4: Run focused transport tests**

Run:

```sh
go test ./pkg/channelv2/transport -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add pkg/channelv2/transport/local_test.go
git commit -m "test(channelv2): cover local transport routes"
```

## Task 8: Full P0 Verification

**Files:**
- No new files unless test failures require narrow fixes.

- [ ] **Step 1: Run full channelv2 tests**

Run:

```sh
go test ./pkg/channelv2/... -count=1
```

Expected: PASS.

- [ ] **Step 2: Run coverage summary**

Run:

```sh
go test -coverprofile=/tmp/channelv2.cover ./pkg/channelv2/...
go tool cover -func=/tmp/channelv2.cover | sort -k3,3n | sed -n '1,80p'
```

Expected: PASS. Confirm the previously listed P0 uncovered functions are no
longer at 0% where this plan added direct coverage.

- [ ] **Step 3: Inspect git status**

Run:

```sh
git status --short
```

Expected: only intentional files from this plan, plus any unrelated preexisting
changes such as `pkg/channelv2/FLOW.md`.

- [ ] **Step 4: Final commit if needed**

If any verification-only cleanup remains:

```sh
git add <intentional-files>
git commit -m "test(channelv2): complete p0 coverage"
```

## Final Report

Report:

- tests added by package,
- whether expected epoch enforcement changed runtime behavior,
- exact verification commands and results,
- any intentionally ignored unrelated changes,
- any remaining P1/P2 follow-up recommendations.
