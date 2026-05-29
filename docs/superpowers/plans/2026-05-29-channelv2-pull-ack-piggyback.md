# channelv2 Pull ACK Piggyback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move ordinary follower progress acknowledgement from a standalone ACK RPC into the next Pull request while preserving stopped-follower ACK semantics.

**Architecture:** Add an explicit `AckOffset` to `transport.PullRequest`. Followers set it to the highest locally durable offset before issuing a Pull; leaders apply it in `handleLeaderPull` before serving records. The current `AckRequest` path remains for stopped-follower lifecycle ACKs and compatibility.

**Tech Stack:** Go, `pkg/channelv2` reactor/service/transport/worker packages, existing `testify/require` tests.

---

## Current Code Structure

The current `pkg/channelv2/reactor` package has already been split by event domain:

- `pkg/channelv2/reactor/leader_replication.go`: leader-side `EventPull`, `EventAck`, store read completion, and pull response generation.
- `pkg/channelv2/reactor/follower_replication.go`: follower pull scheduling, apply scheduling, ACK scheduling, pull-hint handling, and RPC completion handling.
- `pkg/channelv2/reactor/worker_completion.go`: routes worker results to domain handlers.
- `pkg/channelv2/reactor/reactor.go`: mailbox dispatch, metadata, tick, close, and runtime loading.
- `pkg/channelv2/reactor/FLOW.md`: package-level event-domain flow documentation.
- `pkg/channelv2/FLOW.md`: channelv2 package flow documentation.

Do not implement this against the old merged reactor layout; current changes belong in the event-domain files above.

## File Structure

- Modify `pkg/channelv2/transport/types.go`: add `PullRequest.AckOffset`.
- Modify `pkg/channelv2/reactor/leader_replication.go`: apply `AckOffset` in `handleLeaderPull` after validation and before follower runtime bookkeeping / response serving.
- Modify `pkg/channelv2/reactor/follower_replication.go`: set `AckOffset` in `trySubmitPull` and replace ordinary post-apply standalone ACK submission with immediate next-pull scheduling.
- Modify `pkg/channelv2/reactor/replication_state_test.go`: update follower ACK tests and add leader ACK piggyback coverage.
- Modify `pkg/channelv2/reactor/FLOW.md`: update event-domain prose to describe Pull-carried ordinary ACKs.
- Modify `pkg/channelv2/FLOW.md`: update package sequence diagram and stopped ACK note.

## Task 1: Add Protocol Field and Leader-Side Failing Tests

**Files:**
- Modify: `pkg/channelv2/transport/types.go`
- Modify: `pkg/channelv2/reactor/replication_state_test.go`

- [ ] **Step 1: Add `AckOffset` to `PullRequest`**

Edit `pkg/channelv2/transport/types.go` so `PullRequest` has a durable-progress field after `NextOffset`:

```go
// PullRequest asks a leader for records starting at NextOffset.
type PullRequest struct {
	ChannelKey  ch.ChannelKey
	ChannelID   ch.ChannelID
	Epoch       uint64
	LeaderEpoch uint64
	Follower    ch.NodeID
	NextOffset  uint64
	// AckOffset is the highest offset the follower has durably applied before this pull.
	AckOffset uint64
	MaxBytes  int
}
```

- [ ] **Step 2: Add a leader test proving piggybacked ACK advances HW**

Append this test near the leader pull tests in `pkg/channelv2/reactor/replication_state_test.go`, after `TestLeaderPullDuplicateOpIDRejectsBeforeFollowerStateUpdates`:

```go
func TestLeaderPullAckOffsetAdvancesHWAndCompletesQuorumWaiter(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := ch.Meta{
		Key:         "1:pull-ack-complete",
		ID:          ch.ChannelID{ID: "pull-ack-complete", Type: 1},
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []ch.NodeID{1, 2},
		ISR:         []ch.NodeID{1, 2},
		MinISR:      2,
		Status:      ch.StatusActive,
	}
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, AppendBatchMaxRecords: 1})
	require.NoError(t, applyMetaDirect(t, r, meta))

	appendFuture := NewFuture()
	r.handleAppend(Event{
		Kind:   EventAppend,
		Key:    meta.Key,
		OpID:   1,
		Future: appendFuture,
		Append: ch.AppendBatchRequest{
			ChannelID:  meta.ID,
			CommitMode: ch.CommitModeQuorum,
			Messages: []ch.Message{{
				MessageID:   10,
				ChannelID:   meta.ID.ID,
				ChannelType: meta.ID.Type,
				Payload:     []byte("a"),
			}},
		},
	})
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: sink.awaitResultKind(t, worker.TaskStoreAppend)})
	requireFuturePending(t, appendFuture)

	pullFuture := NewFuture()
	r.handleLeaderPull(Event{
		Kind:   EventPull,
		Key:    meta.Key,
		OpID:   2,
		Future: pullFuture,
		Pull: transport.PullRequest{
			ChannelKey:  meta.Key,
			ChannelID:   meta.ID,
			Epoch:       meta.Epoch,
			LeaderEpoch: meta.LeaderEpoch,
			Follower:    2,
			NextOffset:  2,
			AckOffset:   1,
			MaxBytes:    1024,
		},
	})

	pullResult := awaitFutureResult(t, pullFuture)
	require.NoError(t, pullResult.Err)
	require.Empty(t, pullResult.Pull.Records)
	appendResult := awaitFutureResult(t, appendFuture)
	require.NoError(t, appendResult.Err)
	require.Len(t, appendResult.AppendBatch.Items, 1)
	require.Equal(t, uint64(1), appendResult.AppendBatch.Items[0].MessageSeq)
	rc := r.channels[meta.Key]
	require.Equal(t, uint64(1), rc.state.HW)
	require.Equal(t, uint64(1), rc.state.Progress[2].Match)
}
```

- [ ] **Step 3: Add a leader test rejecting impossible ACK offsets**

Append this test near the previous one:

```go
func TestLeaderPullRejectsAckOffsetAboveLeaderLEO(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	defer g.Close()

	meta := followerTestMeta("pull-ack-above-leo")
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	future, err := g.Submit(context.Background(), meta.Key, Event{
		Kind: EventPull,
		Key:  meta.Key,
		OpID: 301,
		Pull: transport.PullRequest{
			ChannelKey:  meta.Key,
			ChannelID:   meta.ID,
			Epoch:       meta.Epoch,
			LeaderEpoch: meta.LeaderEpoch,
			Follower:    2,
			NextOffset:  1,
			AckOffset:   1,
			MaxBytes:    1024,
		},
	})
	require.NoError(t, err)
	_, err = future.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrStaleMeta)

	rc := g.reactors[g.router.PickIndex(meta.Key)].channels[meta.Key]
	require.Equal(t, uint64(0), rc.state.HW)
	require.Equal(t, uint64(0), rc.state.Progress[2].Match)
}
```

- [ ] **Step 4: Run leader tests and verify they fail**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestLeaderPullAckOffsetAdvancesHWAndCompletesQuorumWaiter|TestLeaderPullRejectsAckOffsetAboveLeaderLEO' -count=1
```

Expected: tests fail because `AckOffset` is not yet applied in `handleLeaderPull`.

## Task 2: Implement Leader Pull ACK Application

**Files:**
- Modify: `pkg/channelv2/reactor/leader_replication.go`

- [ ] **Step 1: Add a helper in `leader_replication.go`**

Add this helper below `handleLeaderAck` in `pkg/channelv2/reactor/leader_replication.go`:

```go
func (r *Reactor) applyLeaderPullAckOffset(rc *runtimeChannel, req transport.PullRequest) error {
	if req.AckOffset == 0 {
		return nil
	}
	if req.AckOffset > rc.state.LEO {
		return ch.ErrStaleMeta
	}
	decision := rc.state.ApplyFollowerAck(machine.FollowerAck{Follower: req.Follower, MatchOffset: req.AckOffset})
	if follower := rc.followers[req.Follower]; follower != nil && req.AckOffset > follower.Match {
		follower.Match = req.AckOffset
	}
	if follower := rc.followers[req.Follower]; follower != nil && follower.Match >= rc.state.LEO {
		retireFollowerPullHints(rc, req.Follower)
	}
	r.completeReplies(rc, decision.Replies, nil)
	return nil
}
```

- [ ] **Step 2: Call the helper from `handleLeaderPull`**

In `handleLeaderPull`, after the existing metadata/range validation and duplicate pull-waiter check, keep the existing:

```go
now := time.Now()
r.syncLeaderFollowers(rc)
```

Then immediately insert:

```go
if err := r.applyLeaderPullAckOffset(rc, event.Pull); err != nil {
	event.Future.Complete(Result{Err: err})
	return
}
```

The resulting section should be:

```go
now := time.Now()
r.syncLeaderFollowers(rc)
if err := r.applyLeaderPullAckOffset(rc, event.Pull); err != nil {
	event.Future.Complete(Result{Err: err})
	return
}
if follower := rc.followers[event.Pull.Follower]; follower != nil {
	follower.LastPullAt = now
	follower.Parked = false
	follower.Stopped = false
	follower.NextExpectedPullAt = time.Time{}
	retireFollowerPullHints(rc, event.Pull.Follower)
	match := event.Pull.NextOffset - 1
	if event.Pull.NextOffset <= rc.state.LEO+1 && match > follower.Match {
		follower.Match = match
	}
}
```

- [ ] **Step 3: Run leader tests and verify they pass**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestLeaderPullAckOffsetAdvancesHWAndCompletesQuorumWaiter|TestLeaderPullRejectsAckOffsetAboveLeaderLEO' -count=1
```

Expected: both tests pass.

## Task 3: Convert Ordinary Follower Replication to Pull-Carried ACKs

**Files:**
- Modify: `pkg/channelv2/reactor/follower_replication.go`
- Modify: `pkg/channelv2/reactor/replication_state_test.go`

- [ ] **Step 1: Update the existing follower pull test**

Replace `TestFollowerTickPullsFromLocalLEOPlusOne` in `pkg/channelv2/reactor/replication_state_test.go` with:

```go
func TestFollowerTickPullsFromLocalLEOPlusOne(t *testing.T) {
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 2, ReactorCount: 1, MailboxSize: 16, Store: factory, Transport: net})
	require.NoError(t, err)
	defer g.Close()

	meta := followerTestMeta("a")
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Unix(1, 0)}))
	require.Eventually(t, func() bool {
		pull := net.LastPull()
		return pull.NextOffset == 1 && pull.AckOffset == 0
	}, time.Second, time.Millisecond)

	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    1,
		LeaderLEO:   1,
		Records:     []ch.Record{{ID: 10, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
	})
	require.Eventually(t, func() bool {
		_ = awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(time.Hour)})
		pull := net.LastPull()
		return pull.NextOffset == 2 && pull.AckOffset == 1
	}, time.Second, time.Millisecond)
	require.Equal(t, 0, net.AckCalls())
}
```

- [ ] **Step 2: Replace the existing ordinary ACK test**

Rename `TestFollowerStoreApplyResultSendsAck` to `TestFollowerStoreApplyResultSchedulesPullAckOffset` and replace its body with:

```go
func TestFollowerStoreApplyResultSchedulesPullAckOffset(t *testing.T) {
	net := newCapturingTransport()
	meta := followerTestMeta("apply-pull-ack")
	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    1,
		LeaderLEO:   1,
		Records:     []ch.Record{{ID: 10, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
	})
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 2, ReactorCount: 1, MailboxSize: 16, Store: factory, Transport: net})
	require.NoError(t, err)
	defer g.Close()

	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now()}))
	require.Eventually(t, func() bool {
		pull := net.LastPull()
		return net.PullCalls() >= 2 && pull.NextOffset == 2 && pull.AckOffset == 1
	}, time.Second, time.Millisecond)
	require.Equal(t, 0, net.AckCalls())

	cs, err := factory.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, err)
	read, err := cs.ReadCommitted(context.Background(), store.ReadCommittedRequest{FromSeq: 1, MaxSeq: 1, Limit: 10, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, read.Messages, 1)
	require.Equal(t, uint64(1), read.Messages[0].MessageSeq)
}
```

- [ ] **Step 3: Update apply-backpressure test expectation**

In `TestStoreApplyPoolFullKeepsOnePendingPullAndRetries`, replace the post-unblock expectation:

```go
factory.UnblockApplies()
require.Eventually(t, func() bool { return net.LastAck().MatchOffset == 1 }, time.Second, time.Millisecond)
```

with:

```go
factory.UnblockApplies()
require.Eventually(t, func() bool {
	_ = awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(time.Millisecond)})
	pull := net.LastPull()
	return net.PullCalls() >= 2 && pull.NextOffset == 2 && pull.AckOffset == 1
}, time.Second, time.Millisecond)
require.Equal(t, 0, net.AckCalls())
```

- [ ] **Step 4: Replace the ordinary ACK pool test**

Rename `TestAckPoolFullKeepsPendingAckAndRetriesOnTick` to `TestOrdinaryApplyDoesNotUseAckPool` and replace its body with:

```go
func TestOrdinaryApplyDoesNotUseAckPool(t *testing.T) {
	net := newCapturingTransport()
	meta := followerTestMeta("ordinary-no-ack-pool")
	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    1,
		LeaderLEO:   1,
		Records:     []ch.Record{{ID: 10, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
	})
	factory := newBlockingApplyFactory()
	factory.BlockApplies()
	g, err := NewGroup(Config{
		LocalNode:             2,
		ReactorCount:          1,
		MailboxSize:           32,
		Store:                 factory,
		Transport:             net,
		ReplicationMinBackoff: time.Nanosecond,
		ReplicationMaxBackoff: time.Nanosecond,
		WorkerPools:           worker.PoolsConfig{RPC: worker.PoolConfig{Name: "rpc", Workers: 1, QueueSize: 1}},
	})
	require.NoError(t, err)
	defer g.Close()

	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now()}))
	require.Eventually(t, factory.ApplyStarted, time.Second, time.Millisecond)

	factory.UnblockApplies()
	require.Eventually(t, func() bool {
		_ = awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(time.Millisecond)})
		return net.PullCalls() >= 2 && net.LastPull().AckOffset == 1
	}, time.Second, time.Millisecond)
	require.Equal(t, 0, net.AckCalls())
}
```

- [ ] **Step 5: Run follower tests and verify they fail**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestFollowerTickPullsFromLocalLEOPlusOne|TestFollowerStoreApplyResultSchedulesPullAckOffset|TestStoreApplyPoolFullKeepsOnePendingPullAndRetries|TestOrdinaryApplyDoesNotUseAckPool' -count=1
```

Expected: tests fail because `trySubmitPull` does not set `AckOffset` and `handleStoreApplyResult` still submits ordinary standalone ACKs.

- [ ] **Step 6: Set `AckOffset` in `trySubmitPull`**

In `pkg/channelv2/reactor/follower_replication.go`, update the `PullRequest` in `trySubmitPull`:

```go
req := transport.PullRequest{
	ChannelKey:  rc.state.Key,
	ChannelID:   rc.state.ID,
	Epoch:       rc.state.Epoch,
	LeaderEpoch: rc.state.LeaderEpoch,
	Follower:    r.cfg.LocalNode,
	NextOffset:  rc.state.LEO + 1,
	AckOffset:   rc.state.LEO,
	MaxBytes:    r.cfg.PullMaxBytes,
}
```

- [ ] **Step 7: Replace ordinary post-apply ACK with immediate pull scheduling**

In `handleStoreApplyResult` in `pkg/channelv2/reactor/follower_replication.go`, replace:

```go
rc.replication.dirty = true
if !r.submitAck(rc, rc.state.LEO, now) {
	return
}
```

with:

```go
rc.replication.markDirty(now)
rc.replication.nextPullAt = now
```

This keeps stopped ACKs intact because `handleStoreCheckpointResult` still calls `submitAckPayload(..., stopped=true, ...)`.

- [ ] **Step 8: Rewrite ACK retry test to seed compatibility ACK state directly**

After removing ordinary ACK submission, `TestFollowerAckResultResetsBackoff` must no longer create an ACK by running ordinary pull/apply. Replace the whole test with:

```go
func TestFollowerAckResultResetsBackoff(t *testing.T) {
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()

	meta := followerTestMeta("compat-ack-retry")
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16,
		ReplicationMinBackoff: time.Hour, ReplicationMaxBackoff: time.Hour,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]

	net.SetAckError(ch.ErrNotReady)
	rc.replication.pendingAck = true
	rc.replication.pendingAckMatch = 1

	r.handleTick(Event{Kind: EventTick, Key: meta.Key, TickNow: time.Unix(1, 0)})
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: sink.awaitResultKind(t, worker.TaskRPCAck)})
	require.Equal(t, 1, net.AckCalls())
	require.True(t, rc.replication.pendingAck)
	require.False(t, rc.replication.nextAckAt.IsZero())
	nextAckAt := rc.replication.nextAckAt

	net.SetAckError(nil)
	r.handleTick(Event{Kind: EventTick, Key: meta.Key, TickNow: nextAckAt.Add(-time.Millisecond)})
	require.Equal(t, 1, net.AckCalls())
	r.handleTick(Event{Kind: EventTick, Key: meta.Key, TickNow: nextAckAt.Add(time.Millisecond)})
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: sink.awaitResultKind(t, worker.TaskRPCAck)})
	require.Equal(t, 2, net.AckCalls())
	require.False(t, rc.replication.pendingAck)
	require.Zero(t, rc.replication.backoff)
	require.True(t, rc.replication.nextAckAt.IsZero())
}
```

`TestStaleRPCAckCompletionDoesNotClearNewerAckInflight` and `TestAckErrorRetryKeepsSameMatchOffset` can remain as lower-level ACK state tests because they construct `replicationState` directly.

- [ ] **Step 9: Run follower tests and verify they pass**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestFollowerTickPullsFromLocalLEOPlusOne|TestFollowerStoreApplyResultSchedulesPullAckOffset|TestStoreApplyPoolFullKeepsOnePendingPullAndRetries|TestOrdinaryApplyDoesNotUseAckPool|TestFollowerAckResultResetsBackoff|TestStaleRPCAckCompletionDoesNotClearNewerAckInflight|TestAckErrorRetryKeepsSameMatchOffset' -count=1
```

Expected: all selected tests pass.

## Task 4: Preserve Stopped ACK Lifecycle Behavior

**Files:**
- Modify: `pkg/channelv2/reactor/replication_state_test.go`

- [ ] **Step 1: Add a stopped ACK regression test**

Append this test near the stopped follower lifecycle tests:

```go
func TestFollowerStoppedAckStillUsesStandaloneAck(t *testing.T) {
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()

	meta := followerTestMeta("stopped-ack-standalone")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 1
	rc.state.HW = 1
	rc.replication.stopping = true
	rc.replication.stopActivityVersion = 1
	rc.replication.deleteAfterStoppedAck = true
	rc.replication.checkpointInflight = true
	rc.replication.checkpointOpID = 7
	rc.runtimeLifecycle.FollowerPhase = FollowerLifecycleStopCheckpointing

	r.handleStoreCheckpointResult(worker.Result{
		Kind: worker.TaskStoreCheckpoint,
		Fence: ch.Fence{
			ChannelKey:  meta.Key,
			Generation:  rc.state.Generation,
			Epoch:       meta.Epoch,
			LeaderEpoch: meta.LeaderEpoch,
			OpID:        7,
		},
		StoreCheckpoint: &worker.StoreCheckpointResult{},
	})
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: sink.awaitResultKind(t, worker.TaskRPCAck)})

	require.Equal(t, 1, net.AckCalls())
	ack := net.LastAck()
	require.True(t, ack.Stopped)
	require.Equal(t, uint64(1), ack.MatchOffset)
	require.Equal(t, uint64(1), ack.ActivityVersion)
}
```

- [ ] **Step 2: Run stopped ACK tests**

Run:

```bash
go test ./pkg/channelv2/reactor -run 'TestFollowerStoppedAckStillUsesStandaloneAck|TestLeaderStoppedAck|TestLeaderEvictsOnlyAfterAllFollowersStoppedAck|TestStoppedAckRetiresObsoletePullHintInflightAndAllowsLeaderEviction' -count=1
```

Expected: stopped ACK tests pass and standalone `Ack` remains used for stopped lifecycle completion.

## Task 5: Update Flow Documentation and Run Package Tests

**Files:**
- Modify: `pkg/channelv2/reactor/FLOW.md`
- Modify: `pkg/channelv2/FLOW.md`

- [ ] **Step 1: Update `pkg/channelv2/reactor/FLOW.md`**

In the leader-side section, replace:

```text
remote follower Ack RPC
  -> Group.Submit(EventAck)
  -> handleLeaderAck
  -> ApplyFollowerAck
  -> complete quorum append waiters when HW advances
  -> record stopped follower state for stopped ACKs
  -> schedule leader lifecycle
```

with:

```text
remote follower Pull RPC with AckOffset
  -> Group.Submit(EventPull)
  -> handleLeaderPull
  -> ApplyFollowerAck when AckOffset > 0
  -> complete quorum append waiters when HW advances
  -> serve records or idle pull control

remote follower stopped Ack RPC
  -> Group.Submit(EventAck)
  -> handleLeaderAck
  -> record stopped follower state
  -> schedule leader lifecycle
```

In the follower-side section, replace:

```text
tickFollowerReplication
  -> retry pending ACK before new pulls
  -> apply a pending pull before new pulls
  -> checkpoint and send stopped ACK after accepted stop control
  -> honor retry backoff and leader-provided park delay
  -> submit RPC Pull when eligible
```

with:

```text
tickFollowerReplication
  -> retry pending stopped/compatibility ACK before new pulls
  -> apply a pending pull before new pulls
  -> checkpoint and send stopped ACK after accepted stop control
  -> honor retry backoff and leader-provided park delay
  -> submit RPC Pull with AckOffset when eligible
```

Also replace:

```text
The follower keeps at most one pull RPC, one pending pull response, one store
apply, and one ACK RPC in flight.
```

with:

```text
The follower keeps at most one pull RPC, one pending pull response, one store
apply, and one stopped/compatibility ACK RPC in flight.
```

- [ ] **Step 2: Update `pkg/channelv2/FLOW.md`**

In the append sequence, replace:

```text
Follower->>Workers: TaskStoreApply(records)
Workers-->>Follower: apply result
Follower->>Workers: TaskRPCAck(match offset)
Workers->>Reactor: EventAck
Reactor->>Reactor: AdvanceHW
```

with:

```text
Follower->>Workers: TaskStoreApply(records)
Workers-->>Follower: apply result
Follower->>Workers: TaskRPCPull(leader, local LEO + 1, AckOffset=local LEO)
Workers->>Reactor: EventPull with AckOffset
Reactor->>Reactor: ApplyFollowerAck and AdvanceHW before serving records
```

Then add this sentence after the diagram:

```text
Ordinary follower progress is piggybacked on PullRequest.AckOffset after StoreApply succeeds. The separate Ack RPC remains for stopped-follower lifecycle acknowledgement before idle runtime eviction.
```

- [ ] **Step 3: Run targeted channelv2 tests**

Run:

```bash
go test ./pkg/channelv2/...
```

Expected: all `pkg/channelv2` tests pass.

- [ ] **Step 4: Run broader related tests if channelv2 package tests pass**

Run:

```bash
go test ./pkg/... ./internalv2/...
```

Expected: tests pass. If unrelated packages fail, capture the exact package and failing test name before deciding whether the failure is related.

## Self-Review

- Spec coverage: protocol field, leader ACK application, follower hot path, stopped ACK preservation, docs, and tests are covered.
- Current structure coverage: the plan now targets `leader_replication.go`, `follower_replication.go`, `worker_completion.go`, and the two current `FLOW.md` files.
- Placeholder scan: no placeholder tasks remain; each code change step includes concrete snippets.
- Type consistency: the plan consistently uses `PullRequest.AckOffset`, `handleLeaderPull`, `handleLeaderAck`, `tickFollowerReplication`, `machine.FollowerAck`, and existing test helpers.
