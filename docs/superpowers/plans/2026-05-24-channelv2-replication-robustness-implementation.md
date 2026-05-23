# Channelv2 Replication Robustness Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Harden `pkg/channelv2` short-poll `Pull` + explicit `Ack` replication so stale completions, metadata fences, worker backpressure, cancellation, and close cannot corrupt state or hang waiters.

**Architecture:** Keep reactors as the single writers for channel state and replication runtime state. Add deterministic tests first, then make minimal changes to follower replication scheduling, leader pull waiter cleanup, leader ACK validation, and FLOW documentation. Do not add new protocol features or move replication back into `service.Tick`.

**Tech Stack:** Go 1.23, `testing`, `context`, `time`, existing `testify/require`, existing `pkg/channelv2/reactor`, `pkg/channelv2/testkit`, `pkg/channelv2/store`, `pkg/channelv2/transport`, and typed `pkg/channelv2/worker` pools.

---

## Source Spec

- Read first: `docs/superpowers/specs/2026-05-24-channelv2-replication-robustness-design.md`
- Also read before editing: `AGENTS.md`, `pkg/channelv2/FLOW.md`, `pkg/channelv2/reactor/replication_state.go`, `pkg/channelv2/reactor/replication_runtime.go`, `pkg/channelv2/reactor/reactor.go`, `pkg/channelv2/reactor/replication_state_test.go`, `pkg/channelv2/testkit/cluster.go`, `pkg/channelv2/testkit/cluster_test.go`
- Use @superpowers:test-driven-development for every production code change.
- If a test fails unexpectedly, stop feature work and use @superpowers:systematic-debugging.
- Before claiming completion, use @superpowers:verification-before-completion.

## Scope Rules

- Keep all new production code under `pkg/channelv2`; docs may update `docs/superpowers` and `pkg/channelv2/FLOW.md`.
- Do not wire into `internal/app` and do not replace existing `pkg/channel`.
- Do not add long-poll, snapshots, retention, migration, leader repair, or production cutover behavior.
- Keep single-node behavior described as single-node cluster quorum behavior; do not introduce bypass branches.
- Only `pkg/channelv2/store/channel_adapter.go` may import old `pkg/channel` or `pkg/channel/store`.
- Tests must be fast and deterministic. Use explicit `EventTick.TickNow` timestamps and blocking fake stores/transports instead of long sleeps.
- Key structs, fields, and methods introduced by this phase need English comments.

## File Structure

Expected production changes:

- Modify: `pkg/channelv2/reactor/replication_state.go` - add small helper methods for stale apply/ack/pull decisions only if tests expose gaps.
- Modify: `pkg/channelv2/reactor/replication_runtime.go` - harden follower pull/apply/ack retry and stale completion handling.
- Modify: `pkg/channelv2/reactor/reactor.go` - harden leader-side pull and ACK validation/cleanup.
- Modify: `pkg/channelv2/reactor/effect.go` - adjust waiter cleanup only if close/cancel tests expose leaks.
- Modify: `pkg/channelv2/FLOW.md` - document hardened replication failure and retry semantics.

Expected test changes:

- Modify: `pkg/channelv2/reactor/replication_state_test.go` - add deterministic follower/leader robustness tests.
- Modify: `pkg/channelv2/testkit/cluster_test.go` - add memory cluster transient drop recovery tests.
- Modify: `pkg/channelv2/testkit/cluster.go` - add test helpers only if needed for explicit ticks or temporary network drops.

Avoid changes to `service.Tick`; it must keep delegating to `group.Tick(ctx)` only.

## Task 0: Preflight And Baseline

**Files:**
- Read: `AGENTS.md`
- Read: `pkg/channelv2/FLOW.md`
- Read: `docs/superpowers/specs/2026-05-24-channelv2-replication-robustness-design.md`

- [ ] **Step 1: Check branch and worktree**

Run:

```bash
git branch --show-current
git status --short
```

Expected: branch is the intended implementation branch or `main` if the user asked to work directly there; no unrelated changes in files this plan will edit. If starting from `main`, prefer creating a short-lived branch or worktree before Task 1 unless the user explicitly requests direct commits to `main`. If unexpected changes exist, stop and ask how to proceed.

- [ ] **Step 2: Run baseline replication tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/reactor -run 'TestFollower|TestLeader|Test.*Ack|Test.*Pull' -count=1
GOWORK=off go test ./pkg/channelv2/testkit -run 'TestThreeNode|Test.*Replication|Test.*Ack|Test.*Pull' -count=1
```

Expected: PASS before changes. If it fails, use @superpowers:systematic-debugging before continuing.

- [ ] **Step 3: Confirm import boundary baseline**

Run:

```bash
rg 'pkg/channel"|pkg/channel/store' pkg/channelv2 -g'*.go'
```

Expected: only `pkg/channelv2/store/channel_adapter.go` imports old channel packages.

## Task 1: Follower Stale Completion And Retry Invariants

**Files:**
- Modify: `pkg/channelv2/reactor/replication_state_test.go`
- Modify: `pkg/channelv2/reactor/replication_state.go`
- Modify: `pkg/channelv2/reactor/replication_runtime.go`

- [ ] **Step 1: Add failing stale store-apply completion test**

Append this test to `pkg/channelv2/reactor/replication_state_test.go`:

```go
func TestStaleStoreApplyCompletionDoesNotClearNewerApplyInflight(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("stale-apply")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.replication.pendingPull = &transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    2,
		Records:     []ch.Record{{ID: 2, Index: 2, Payload: []byte("new"), SizeBytes: 3}},
	}
	rc.replication.applyOpID = 9

	stale := worker.Result{
		Kind:  worker.TaskStoreApply,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.state.Generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 8},
		StoreApply: &worker.StoreApplyResult{LEO: 1},
	}
	r.handleStoreApplyResult(stale)

	require.Equal(t, ch.OpID(9), rc.replication.applyOpID)
	require.NotNil(t, rc.replication.pendingPull)
	require.Equal(t, uint64(2), rc.replication.pendingPull.Records[0].Index)
	require.Zero(t, rc.state.LEO)
}
```

- [ ] **Step 2: Run test and verify RED or already-covered behavior**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/reactor -run TestStaleStoreApplyCompletionDoesNotClearNewerApplyInflight -count=1
```

Expected: FAIL if current implementation has a stale apply bug; PASS is acceptable only if the behavior is already implemented. If it passes immediately, keep the test as coverage and continue without production changes for this step.

- [ ] **Step 3: Add failing empty pull HW/idle retry test**

Add:

```go
func TestFollowerEmptyPullAdvancesHWOnlyToLocalLEOAndSchedulesIdleRetry(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("empty-pull-hw")
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16,
		ReplicationIdlePollInterval: time.Hour,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 1
	rc.replication.pullInflight = true
	rc.replication.pullOpID = 7

	result := worker.Result{
		Kind:  worker.TaskRPCPull,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.state.Generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 7},
		RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{
			ChannelKey:  meta.Key,
			Epoch:       meta.Epoch,
			LeaderEpoch: meta.LeaderEpoch,
			LeaderHW:    99,
			LeaderLEO:   99,
		}},
	}
	r.handleRPCPullResult(result)

	require.Equal(t, uint64(3), rc.state.HW)
	require.False(t, rc.replication.ackInflight)
	require.False(t, rc.replication.pendingAck)
	require.False(t, rc.replication.nextPullAt.IsZero())
	require.True(t, rc.replication.nextPullAt.After(time.Now().Add(59*time.Minute)))
}
```

- [ ] **Step 4: Run targeted tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/reactor -run 'TestStaleStoreApplyCompletion|TestFollowerEmptyPull' -count=1
```

Expected: PASS after any minimal code changes.

- [ ] **Step 5: Add or adjust minimal implementation**

Only if tests fail, update `replication_runtime.go` or `replication_state.go` minimally:

- Ensure stale `TaskStoreApply` completions compare generation, epoch, leader epoch, and `applyOpID` before clearing `applyOpID` or pending state.
- Ensure empty pull updates follower HW with `min(state.LEO, resp.LeaderHW)` and does not send ACK.
- Ensure empty pull schedules `nextPullAt` using `ReplicationIdlePollInterval`.

- [ ] **Step 6: Run follower invariant tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/reactor -run 'TestFollower|TestStale.*Completion|Test.*Ack|Test.*Pull' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit follower invariant coverage**

Run:

```bash
git add pkg/channelv2/reactor/replication_state_test.go pkg/channelv2/reactor/replication_state.go pkg/channelv2/reactor/replication_runtime.go
git commit -m "test: cover channelv2 follower replication invariants"
```

If no production code changed but tests were added, use the same commit message.

## Task 2: Leader Pull Waiter Close And Backpressure

**Files:**
- Modify: `pkg/channelv2/reactor/replication_state_test.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/reactor/effect.go`

- [ ] **Step 1: Add failing close test for blocked leader pull waiter**

Add this test near the existing leader pull tests in `pkg/channelv2/reactor/replication_state_test.go`:

```go
func TestLeaderPullWaiterFailsWithErrClosedOnGroupClose(t *testing.T) {
	factory := newNonCancelingBlockingReadLogFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	defer factory.UnblockReadLogs()

	meta := ch.Meta{Key: "1:pull-close", ID: ch.ChannelID{ID: "pull-close", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	pullFuture, err := g.Submit(context.Background(), meta.Key, Event{
		Kind:    EventPull,
		Key:     meta.Key,
		OpID:    111,
		Context: context.Background(),
		Pull:    transport.PullRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1, Follower: 2, NextOffset: 1, MaxBytes: 1024},
	})
	require.NoError(t, err)
	factory.waitReadLogStarted(t)

	closed := make(chan error, 1)
	go func() { closed <- g.Close() }()
	awaitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = pullFuture.Await(awaitCtx)
	require.ErrorIs(t, err, ch.ErrClosed)
	factory.UnblockReadLogs()
	require.NoError(t, <-closed)
}
```

- [ ] **Step 2: Run close test and verify RED or coverage**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/reactor -run TestLeaderPullWaiterFailsWithErrClosedOnGroupClose -count=1
```

Expected: FAIL if leader pull waiters are not failed during close; PASS is acceptable if already covered by existing close cleanup.

- [ ] **Step 3: Add failing read-log pool backpressure test**

Add:

```go
func TestLeaderPullReadLogPoolFullFailsFuture(t *testing.T) {
	factory := newBlockingReadLogFactory()
	g, err := NewGroup(Config{
		LocalNode:    1,
		ReactorCount: 1,
		MailboxSize:  16,
		Store:        factory,
		WorkerPools: worker.PoolsConfig{StoreRead: worker.PoolConfig{Name: "read", Workers: 1, QueueSize: 1}},
	})
	require.NoError(t, err)
	defer g.Close()
	defer factory.UnblockReadLogs()

	meta := ch.Meta{Key: "1:pull-backpressure", ID: ch.ChannelID{ID: "pull-backpressure", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	first, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventPull, Key: meta.Key, OpID: 201, Pull: transport.PullRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1, Follower: 2, NextOffset: 1, MaxBytes: 1024}})
	require.NoError(t, err)
	require.Eventually(t, factory.ReadLogStarted, time.Second, time.Millisecond)
	second, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventPull, Key: meta.Key, OpID: 202, Pull: transport.PullRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1, Follower: 2, NextOffset: 1, MaxBytes: 1024}})
	require.NoError(t, err)
	requireFuturePending(t, second)
	third, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventPull, Key: meta.Key, OpID: 203, Pull: transport.PullRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1, Follower: 2, NextOffset: 1, MaxBytes: 1024}})
	require.NoError(t, err)
	_, err = third.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrBackpressured)

	factory.UnblockReadLogs()
	_, err = first.Await(context.Background())
	require.NoError(t, err)
	_, err = second.Await(context.Background())
	require.NoError(t, err)
}
```

- [ ] **Step 4: Run leader pull tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/reactor -run 'TestLeaderPull' -count=1
```

Expected: PASS after minimal fixes.

- [ ] **Step 5: Implement minimal leader pull cleanup/backpressure fixes**

Only if tests fail:

- Ensure `failWaiters`/close cleanup completes `pullWaiters` with `ErrClosed`.
- Ensure metadata fence completes pull waiters with `ErrStaleMeta`.
- Ensure `handlePull` removes waiter and unregisters cancellation context when `submitStoreReadLog` returns `ErrBackpressured` or another error.
- Ensure late `TaskStoreReadLog` completions find no waiter and return without side effects.

- [ ] **Step 6: Commit leader pull hardening**

Run:

```bash
git add pkg/channelv2/reactor/replication_state_test.go pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/effect.go
git commit -m "fix: harden channelv2 leader pull waiters"
```

Use `test:` instead of `fix:` if only tests changed.

## Task 3: Leader ACK Monotonicity And Stale ACK Coverage

**Files:**
- Modify: `pkg/channelv2/reactor/replication_state_test.go`
- Modify: `pkg/channelv2/machine/append_test.go`
- Modify: `pkg/channelv2/machine/append.go`
- Modify: `pkg/channelv2/reactor/reactor.go`

- [ ] **Step 1: Add machine-level regressive ACK test**

Add to `pkg/channelv2/machine/append_test.go`:

```go
func TestFollowerAckDoesNotRegressProgressOrHW(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1, 2, 3}, []ch.NodeID{1, 2, 3}, 2)
	state.HW = 5
	state.Progress[1] = ReplicaProgress{Match: 5}
	state.Progress[2] = ReplicaProgress{Match: 5}

	decision := state.ApplyFollowerAck(FollowerAck{Follower: 2, MatchOffset: 3})

	require.Empty(t, decision.Replies)
	require.Equal(t, uint64(5), state.Progress[2].Match)
	require.Equal(t, uint64(5), state.HW)
}
```

- [ ] **Step 2: Run machine test**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/machine -run TestFollowerAckDoesNotRegressProgressOrHW -count=1
```

Expected: PASS if existing machine monotonicity is correct; keep as explicit coverage.

- [ ] **Step 3: Add reactor-level unknown follower ACK test**

Add to `pkg/channelv2/reactor/replication_state_test.go`:

```go
func TestLeaderIgnoresAckFromUnknownFollower(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory, AppendBatchMaxRecords: 1})
	require.NoError(t, err)
	defer g.Close()

	meta := ch.Meta{Key: "1:ack-unknown", ID: ch.ChannelID{ID: "ack-unknown", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	future, err := g.Submit(context.Background(), meta.Key, appendQuorumEvent(meta, 1, "requires-known-follower"))
	require.NoError(t, err)
	requireFuturePending(t, future)

	ackFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventAck, Key: meta.Key, Ack: transport.AckRequest{ChannelKey: meta.Key, Epoch: 1, LeaderEpoch: 1, Follower: 99, MatchOffset: 1}})
	require.NoError(t, err)
	_, err = ackFuture.Await(context.Background())
	require.NoError(t, err)
	requireFuturePending(t, future)
}
```

- [ ] **Step 4: Run ACK coverage tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/machine -run 'TestFollowerAck' -count=1
GOWORK=off go test ./pkg/channelv2/reactor -run 'TestLeaderIgnoresAck' -count=1
```

Expected: PASS after any minimal fixes.

- [ ] **Step 5: Implement minimal ACK fixes if needed**

Only if tests fail:

- Keep `ApplyFollowerAck` monotonic: update progress only when `MatchOffset` is greater than current match.
- Keep `handleAck` ignoring stale epoch, stale leader epoch, non-leader role, and unknown follower without completing quorum waiters.
- Do not return a new externally visible error from `HandleAck` for stale ACKs unless an existing test/spec already requires it.

- [ ] **Step 6: Commit ACK robustness**

Run:

```bash
git add pkg/channelv2/reactor/replication_state_test.go pkg/channelv2/machine/append_test.go pkg/channelv2/machine/append.go pkg/channelv2/reactor/reactor.go
git commit -m "test: cover channelv2 replication ack monotonicity"
```

Use `fix:` if production code changed.

## Task 4: Testkit Transient Replication Recovery

**Files:**
- Modify: `pkg/channelv2/testkit/cluster_test.go`
- Modify: `pkg/channelv2/testkit/cluster.go` if helper methods are needed

- [ ] **Step 1: Add pull drop recovery test**

Add to `pkg/channelv2/testkit/cluster_test.go`:

```go
func TestThreeNodeClusterCatchesUpAfterTemporaryPullDrop(t *testing.T) {
	h := NewClusterHarness(t, []ch.NodeID{1, 2, 3})
	defer h.Close()
	meta := ch.Meta{Key: ch.ChannelKey("1:pull-drop"), ID: ch.ChannelID{ID: "pull-drop", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
	h.ApplyMetaToAll(meta)

	h.Network.DropPull[1] = true
	_, err := h.Nodes[1].Append(context.Background(), ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{Payload: []byte("hello")}})
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		_ = h.Nodes[2].Tick(context.Background())
		_ = h.Nodes[3].Tick(context.Background())
	}

	h.Network.DropPull[1] = false
	h.WaitCommitted(t, 2, meta.ID, 1, time.Second)
}
```

- [ ] **Step 2: Add ACK drop recovery test**

Add:

```go
func TestThreeNodeClusterCatchesUpAfterTemporaryAckDrop(t *testing.T) {
	h := NewClusterHarness(t, []ch.NodeID{1, 2, 3})
	defer h.Close()
	meta := ch.Meta{Key: ch.ChannelKey("1:ack-drop"), ID: ch.ChannelID{ID: "ack-drop", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
	h.ApplyMetaToAll(meta)

	h.Network.DropAck[1] = true
	future := make(chan error, 1)
	go func() {
		_, err := h.Nodes[1].Append(context.Background(), ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{Payload: []byte("hello")}})
		future <- err
	}()
	time.Sleep(5 * time.Millisecond)
	h.Network.DropAck[1] = false
	h.WaitCommitted(t, 2, meta.ID, 1, time.Second)

	select {
	case err := <-future:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("append did not complete after ACK drop recovered")
	}
}
```

- [ ] **Step 3: Run testkit tests and verify RED or coverage**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/testkit -run 'TestThreeNodeClusterCatchesUpAfterTemporary(Pull|Ack)Drop' -count=1
```

Expected: PASS if Phase 2 recovery already works; if flaky or failing, use @superpowers:systematic-debugging to identify whether test timing or replication retry is wrong.

- [ ] **Step 4: Improve testkit helpers if needed**

Only if tests are flaky because background ticker timing is unclear, add a helper to `ClusterHarness`:

```go
// TickAll submits one maintenance tick to every node in the harness.
func (h *ClusterHarness) TickAll(ctx context.Context) {
	for _, node := range h.Nodes {
		_ = node.Tick(ctx)
	}
}
```

Then use `TickAll` in the new tests instead of repeated per-node calls. Keep background ticker unchanged unless it causes deterministic failure.

- [ ] **Step 5: Commit testkit recovery coverage**

Run:

```bash
git add pkg/channelv2/testkit/cluster.go pkg/channelv2/testkit/cluster_test.go
git commit -m "test: cover channelv2 replication transient recovery"
```

## Task 5: Document Hardened Replication Semantics

**Files:**
- Modify: `pkg/channelv2/FLOW.md`
- Create: `docs/superpowers/reports/2026-05-24-channelv2-replication-robustness-report.md`

- [ ] **Step 1: Update FLOW replication section**

Edit `pkg/channelv2/FLOW.md` under `## Replication` and `## Backpressure` to include:

- follower stores exactly one pending pull response,
- stale pull/apply/ack completions are ignored by generation/epoch/leader epoch/op id fences,
- apply errors and apply backpressure retain the pending pull for retry,
- ACK errors and ACK backpressure retain the exact match offset for retry,
- leader-side pull waiters complete with `ErrStaleMeta`, caller context error, or `ErrClosed`,
- stale/regressive ACKs do not advance leader HW.

Keep language concise and consistent with the current file.

- [ ] **Step 2: Create robustness report**

Create `docs/superpowers/reports/2026-05-24-channelv2-replication-robustness-report.md`:

```markdown
# Channelv2 Replication Robustness Report

## Commands

- `GOWORK=off go test ./pkg/channelv2/reactor -run 'TestFollower|TestLeader|Test.*Replication|Test.*Ack|Test.*Pull' -count=1`
- `GOWORK=off go test ./pkg/channelv2/testkit -run 'TestThreeNode|Test.*Replication|Test.*Ack|Test.*Pull' -count=1`
- `GOWORK=off go test ./pkg/channelv2/... -count=1`
- `GOWORK=off go test -race ./pkg/channelv2/... -count=1`
- `rg 'pkg/channel"|pkg/channel/store' pkg/channelv2 -g'*.go'`
- `git diff --check`

## Results

To be filled during final verification.

## Notes

- The phase keeps the existing short-poll `Pull` plus explicit `Ack` protocol.
- The phase does not add snapshot, retention, migration, leader repair, or `internal/app` integration.
```

- [ ] **Step 3: Verify docs are ASCII and formatted**

Run:

```bash
python3 - <<'PY'
from pathlib import Path
paths = [
    Path('pkg/channelv2/FLOW.md'),
    Path('docs/superpowers/reports/2026-05-24-channelv2-replication-robustness-report.md'),
]
for path in paths:
    text = path.read_text()
    bad = [c for c in text if ord(c) > 127]
    print(path, 'non_ascii', len(bad))
    if bad:
        raise SystemExit(1)
PY
```

Expected: `non_ascii 0` for the new report. `FLOW.md` may already be ASCII; if not, do not rewrite unrelated existing text just for this check unless the new changes introduced non-ASCII.

- [ ] **Step 4: Commit docs**

Run:

```bash
git add pkg/channelv2/FLOW.md docs/superpowers/reports/2026-05-24-channelv2-replication-robustness-report.md
git commit -m "docs: document channelv2 replication robustness"
```

## Task 6: Final Verification And Hardening

**Files:**
- Modify only files changed in previous tasks when verification exposes a specific defect.
- Update: `docs/superpowers/reports/2026-05-24-channelv2-replication-robustness-report.md`

- [ ] **Step 1: Run targeted reactor replication tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/reactor -run 'TestFollower|TestLeader|Test.*Replication|Test.*Ack|Test.*Pull' -count=1
```

Expected: PASS.

- [ ] **Step 2: Run targeted testkit replication tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/testkit -run 'TestThreeNode|Test.*Replication|Test.*Ack|Test.*Pull' -count=1
```

Expected: PASS.

- [ ] **Step 3: Run all channelv2 tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/... -count=1
```

Expected: PASS.

- [ ] **Step 4: Run channelv2 race tests**

Run:

```bash
GOWORK=off go test -race ./pkg/channelv2/... -count=1
```

Expected: PASS. On macOS, linker warnings are acceptable if tests pass.

- [ ] **Step 5: Check import boundary**

Run:

```bash
rg 'pkg/channel"|pkg/channel/store' pkg/channelv2 -g'*.go'
```

Expected: only `pkg/channelv2/store/channel_adapter.go` imports old channel packages.

- [ ] **Step 6: Run old and new channel package tests together**

Run:

```bash
GOWORK=off go test ./pkg/channel/... ./pkg/channelv2/... -count=1
```

Expected: PASS.

- [ ] **Step 7: Run formatting and diff checks**

Run:

```bash
gofmt -w pkg/channelv2
git diff --check
git status --short
```

Expected: no whitespace errors. `git status --short` should show only intentional Phase 3 files before final commit, or no output after commit.

- [ ] **Step 8: Fill report results**

Update `docs/superpowers/reports/2026-05-24-channelv2-replication-robustness-report.md` with exact PASS results and any accepted race-test warnings.

- [ ] **Step 9: Final commit if verification changed report or code**

Run:

```bash
git add pkg/channelv2 docs/superpowers/reports/2026-05-24-channelv2-replication-robustness-report.md
git commit -m "test: verify channelv2 replication robustness"
```

If no changes were needed after the previous commits, do not create an empty commit.

## Implementation Checklist

- [ ] Follower stale pull completions cannot clear newer pull inflight state.
- [ ] Follower stale store-apply completions cannot clear newer apply state.
- [ ] Follower stale ACK completions cannot clear newer ACK state.
- [ ] Follower empty pulls advance HW only to local LEO and schedule idle retry.
- [ ] Follower store apply backpressure retains exactly one pending pull for retry.
- [ ] Follower store apply errors retry the same pending pull.
- [ ] Follower ACK errors and ACK pool backpressure retry the same match offset.
- [ ] Follower pending ACK blocks new pulls until ACK succeeds or metadata fences state.
- [ ] Leader pull waiters complete on store read success, store read error, metadata fence, caller cancel, and close.
- [ ] Late leader read-log completions after waiter removal are ignored.
- [ ] Leader stale ACKs and unknown follower ACKs do not complete quorum append waiters.
- [ ] Regressive ACKs do not lower follower progress or HW.
- [ ] Testkit proves recovery after temporary pull and ACK drops.
- [ ] `service.Tick` remains a low-priority reactor tick entry point only.
- [ ] `pkg/channelv2/FLOW.md` documents hardened replication failure semantics.
- [ ] Old `pkg/channel` import boundary is preserved.

## Risk Notes

- Some proposed tests may already pass because Phase 2 implemented part of the behavior. Keep passing tests as explicit regression coverage and avoid unnecessary production changes.
- Blocking fake stores/transports must unblock in `defer` paths to avoid hanging test processes.
- The background ticker in `testkit.ClusterHarness` can make transient-drop tests timing-sensitive. Prefer explicit ticks or helper methods if flakiness appears.
- Do not convert this phase into protocol work. If long-poll, snapshot, retention, migration, or leader repair becomes necessary, stop and write a separate spec.
