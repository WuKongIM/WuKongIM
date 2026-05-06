# Controller Raft Snapshot Catch-up Status Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add read-only Controller Raft snapshot catch-up, compaction, and restore diagnostics from the raft service through cluster APIs and manager HTTP.

**Architecture:** `pkg/controller/raft` owns a cached, goroutine-safe volatile status snapshot because `RawNode` remains confined to the run loop. `pkg/cluster` exposes node-local Controller Raft status, merges durable raftlog watermarks, and transports remote node-local reads through the existing Controller RPC service. `internal/usecase/management` derives operator health strings and manager DTOs, while `internal/access/manager` exposes a read-only node detail endpoint.

**Tech Stack:** Go, etcd raft `RawNode.Status`, Pebble-backed `pkg/raftlog`, custom Controller RPC codec in `pkg/cluster/codec_control.go`, Gin manager HTTP, `go test`.

---

## Spec And Discovery Context

Read first:

- `docs/superpowers/specs/2026-05-06-controller-raft-snapshot-catchup-design.md`
- `pkg/controller/FLOW.md`
- `pkg/cluster/FLOW.md`
- `pkg/controller/raft/service.go`
- `pkg/controller/raft/config.go`
- `pkg/cluster/controller_log_entries.go`
- `pkg/cluster/controller_handler.go`
- `pkg/cluster/codec_control.go`
- `internal/usecase/management/nodes.go`
- `internal/access/manager/routes.go`
- `internal/access/manager/node_detail.go`

Critical constraints from review:

- Do not add manual snapshot resend, data-dir rebuild, voter membership changes, or destructive operations.
- Do not route `ControllerRaftStatusOnNode` through leader-centric `controllerClient.call`; this is a node-local diagnostic read and must target the requested node directly.
- Keep `*raft.RawNode` confined to `Service.run`; `Service.Status()` must read a cached copy, not call `rawNode.Status()` from another goroutine.
- Do not hold `Service.mu` while doing raftlog I/O, transport sends, state-machine snapshot/restore, or observer callbacks.
- `NeedsSnapshot` requires durable `FirstIndex`; derive it in `pkg/cluster` after raftlog watermarks are merged.
- Controller log compaction remains warning-only and retryable; restore failures remain fatal where they already are, but status must record them before returning/exiting.
- No config keys change in this B phase, so `wukongim.conf.example` should not change.
- Preserve unrelated dirty worktree changes; stage commits by exact path.

## File Structure

- Create: `pkg/controller/raft/status.go` for Controller Raft service status DTOs and helpers.
- Modify: `pkg/controller/raft/service.go` to maintain cached status around Raft, compaction, and restore events.
- Modify: `pkg/controller/raft/service_test.go` for service-level status coverage.
- Create: `pkg/cluster/controller_raft_status.go` for cluster public DTOs, local/remote status reads, and durable merge logic.
- Modify: `pkg/cluster/api.go` to add `ControllerRaftStatusOnNode`.
- Modify: `pkg/cluster/controller_handler.go` to serve node-local status RPC.
- Modify: `pkg/cluster/codec_control.go` for the new Controller RPC kind and binary status codec.
- Modify: `pkg/cluster/codec_control_test.go` for request/response round-trip tests.
- Create: `pkg/cluster/controller_raft_status_test.go` for local merge and remote handler behavior.
- Modify: `pkg/cluster/FLOW.md` to document the new node-local diagnostic API.
- Create: `internal/usecase/management/controller_raft_status.go` for management DTOs and health derivation.
- Modify: `internal/usecase/management/app.go` to extend `ClusterReader`.
- Modify: `internal/usecase/management/nodes.go` to add local-only node summary fields.
- Modify: `internal/usecase/management/nodes_test.go`, `internal/usecase/management/controller_raft_status_test.go`, and any management fakes that implement `ClusterReader`.
- Create: `internal/access/manager/controller_raft_status.go` for HTTP DTOs and handler.
- Modify: `internal/access/manager/server.go` to extend the `Management` interface.
- Modify: `internal/access/manager/routes.go` to add `GET /manager/nodes/:node_id/controller-raft`.
- Modify: `internal/access/manager/nodes.go` to expose node list summary JSON fields.
- Modify: `internal/access/manager/server_test.go` for endpoint and DTO coverage.
- Optionally modify: `docs/development/PROJECT_KNOWLEDGE.md` with the node-local diagnostic routing rule if absent.

---

### Task 1: Add Cached Controller Raft Service Status

**Files:**
- Create: `pkg/controller/raft/status.go`
- Modify: `pkg/controller/raft/service.go`
- Modify: `pkg/controller/raft/service_test.go`

- [ ] **Step 1: Write failing service status tests**

Add tests in `pkg/controller/raft/service_test.go` near existing restore/compaction tests:

```go
func TestServiceStatusBeforeStartAndAfterStartReportsNodeAndRole(t *testing.T) {
    service := NewService(Config{NodeID: 1})
    before := service.Status()
    require.Equal(t, uint64(1), before.NodeID)
    require.Equal(t, RoleUnknown, before.Role)
    require.True(t, before.Compaction.Enabled)

    env := newTestEnv(t, []uint64{1})
    defer env.stopAll()
    env.startNode(t, 1, nil)
    env.waitForLeader(t, []uint64{1})

    node := env.nodes[1]
    require.Eventually(t, func() bool {
        st := node.service.Status()
        return st.Role == RoleLeader && st.LeaderID == 1 && st.Term > 0
    }, 2*time.Second, 10*time.Millisecond)
}

func TestServiceStatusRecordsCompactionFailureAndClearsOnLaterSuccess(t *testing.T) {
    env := newTestEnv(t, []uint64{1})
    defer env.stopAll()
    env.startNodeWithConfig(t, 1, nil, func(cfg *Config) {
        cfg.LogCompaction = LogCompactionConfig{Enabled: true, EnabledSet: true, TriggerEntries: 1, CheckInterval: time.Nanosecond}
    })
    env.waitForLeader(t, []uint64{1})

    sentinel := errors.New("compact once")
    setCompactControllerLogHookForTest(func() error { return sentinel })
    t.Cleanup(func() { setCompactControllerLogHookForTest(nil) })

    node := env.nodes[1]
    require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(2)))
    require.Eventually(t, func() bool {
        st := node.service.Status()
        return st.Compaction.Degraded && st.Compaction.LastError == sentinel.Error() && !st.Compaction.LastErrorAt.IsZero()
    }, 2*time.Second, 10*time.Millisecond)

    setCompactControllerLogHookForTest(nil)
    require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(3)))
    require.Eventually(t, func() bool {
        st := node.service.Status()
        return !st.Compaction.Degraded && st.Compaction.LastError == "" && st.Compaction.LastSnapshotIndex >= 3
    }, 2*time.Second, 10*time.Millisecond)
}
```

Also add restore/progress tests:

- `TestServiceStatusRecordsStartupSnapshotRestore`
- `TestServiceStatusRecordsStartupSnapshotRestoreFailure`
- `TestServiceStatusRecordsReadySnapshotRestore`
- `TestServiceStatusRecordsReadySnapshotRestoreFailure`
- `TestServiceLeaderStatusIncludesPeerProgress`

For Ready restore failure, call package-private `restoreReadySnapshot` with corrupt snapshot bytes on a test service; the behavior under test is status recording, not etcd raft transfer.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/controller/raft -run 'TestServiceStatus|TestServiceLeaderStatus' -count=1`

Expected: FAIL because `Service.Status`, status DTOs, and role constants do not exist.

- [ ] **Step 3: Add status DTOs and role/progress helpers**

Create `pkg/controller/raft/status.go`:

```go
package raft

import "time"

const (
    RoleUnknown   = "unknown"
    RoleLeader    = "leader"
    RoleFollower  = "follower"
    RoleCandidate = "candidate"
)

// Status is a goroutine-safe snapshot of one Controller Raft service.
type Status struct {
    // NodeID is the local Controller Raft node ID.
    NodeID uint64
    // Role is leader, follower, candidate, or unknown.
    Role string
    // LeaderID is the leader known to this node.
    LeaderID uint64
    // Term is the local Raft term.
    Term uint64
    // CommitIndex is the volatile commit index reported by etcd raft.
    CommitIndex uint64
    // AppliedIndex is the volatile applied index reported by etcd raft.
    AppliedIndex uint64
    // Compaction describes local Controller Raft log compaction state.
    Compaction LogCompactionStatus
    // Restore describes the latest Controller metadata snapshot restore.
    Restore SnapshotRestoreStatus
    // Peers contains leader-side follower progress. Followers return an empty list.
    Peers []PeerProgress
}

// LogCompactionStatus describes local Controller Raft snapshot compaction state.
type LogCompactionStatus struct {
    // Enabled reports whether local Controller Raft snapshot compaction is enabled.
    Enabled bool
    // TriggerEntries is the applied-entry delta required before taking another snapshot.
    TriggerEntries uint64
    // CheckInterval is the minimum interval between compaction checks.
    CheckInterval time.Duration
    // LastSnapshotIndex is the latest local snapshot index created by compaction.
    LastSnapshotIndex uint64
    // LastSnapshotAt records when the latest local snapshot was created.
    LastSnapshotAt time.Time
    // LastCheckAt records the latest compaction check attempt.
    LastCheckAt time.Time
    // LastError is the latest compaction error, if any.
    LastError string
    // LastErrorAt records when LastError was observed.
    LastErrorAt time.Time
    // Degraded reports whether the latest compaction attempt failed and has not yet been cleared.
    Degraded bool
}

// SnapshotRestoreStatus describes Controller metadata snapshot restore state.
type SnapshotRestoreStatus struct {
    // LastSnapshotIndex is the index of the latest restored snapshot.
    LastSnapshotIndex uint64
    // LastSnapshotTerm is the term of the latest restored snapshot.
    LastSnapshotTerm uint64
    // LastRestoredAt records when the latest snapshot restore succeeded.
    LastRestoredAt time.Time
    // LastError is the latest snapshot restore error, if any.
    LastError string
    // LastErrorAt records when LastError was observed.
    LastErrorAt time.Time
    // Failed reports whether the latest restore attempt failed.
    Failed bool
}

// PeerProgress describes one follower from the leader's view.
type PeerProgress struct {
    // NodeID is the follower Controller Raft node ID.
    NodeID uint64
    // Match is the highest log index known to match on the follower.
    Match uint64
    // Next is the next log index the leader will send to the follower.
    Next uint64
    // State is the etcd raft progress state.
    State string
    // PendingSnapshot is the snapshot index currently pending for the follower.
    PendingSnapshot uint64
    // RecentActive reports whether the follower was recently active.
    RecentActive bool
    // SnapshotTransferring reports whether raft is currently transferring a snapshot.
    SnapshotTransferring bool
}
```

Add helpers in the same file:

```go
func initialStatus(cfg Config) Status
func cloneStatus(st Status) Status
func raftRoleName(state raft.StateType) string
func peerProgressFromRaft(self uint64, firstIndex uint64, progress map[uint64]tracker.Progress) []PeerProgress
```

Do not include `NeedsSnapshot` in `pkg/controller/raft.PeerProgress`; it depends on durable `FirstIndex` and is derived in `pkg/cluster`.

- [ ] **Step 4: Cache status inside `Service` without exposing `RawNode`**

In `pkg/controller/raft/service.go`, add short-lived status locking separate from lifecycle locking:

```go
type Service struct {
    cfg Config

    mu sync.Mutex
    // existing lifecycle fields...
    starting bool

    statusMu sync.RWMutex
    status   Status

    leaderID atomic.Uint64
    // existing channels...
}
```

Update `NewService`:

```go
func NewService(cfg Config) *Service {
    return &Service{cfg: cfg, status: initialStatus(cfg)}
}
```

Add public status method:

```go
// Status returns a read-only snapshot of local Controller Raft status.
func (s *Service) Status() Status {
    if s == nil {
        return Status{Role: RoleUnknown}
    }
    s.statusMu.RLock()
    st := cloneStatus(s.status)
    s.statusMu.RUnlock()
    if st.NodeID == 0 && s.cfg.NodeID != 0 {
        return initialStatus(s.cfg)
    }
    return st
}
```

Update test/helper construction so status initialization is not bypassed:

- in `pkg/controller/raft/service_test.go`, change `testEnv.startNodeErrWithConfig` from `node.service = &Service{cfg: cfg}` to `node.service = NewService(cfg)`;
- in any new status-specific tests that currently use `&Service{cfg: ...}`, use `NewService(...)` instead;
- leave old zero-value struct literal tests that intentionally exercise lifecycle edge cases unchanged, unless they call `Status()`.

Add private record helpers. They must copy slices and hold `statusMu` only while mutating the cached struct:

```go
func (s *Service) recordRaftStatus(rawNode *raft.RawNode) {
    st := rawNode.Status()
    cached := Status{
        NodeID:       s.cfg.NodeID,
        Role:         raftRoleName(st.RaftState),
        LeaderID:     st.Lead,
        Term:         st.Term,
        CommitIndex:  st.Commit,
        AppliedIndex: st.Applied,
        Peers:        peerProgressFromRaft(s.cfg.NodeID, 0, st.Progress),
    }
    s.statusMu.Lock()
    cached.Compaction = s.status.Compaction
    cached.Restore = s.status.Restore
    s.status = cached
    s.statusMu.Unlock()
}
```

Add record methods:

- `recordStartupRestoreSuccess(snap raftpb.Snapshot)`
- `recordRestoreFailure(snap raftpb.Snapshot, err error)`
- `recordReadyRestoreSuccess(snap raftpb.Snapshot)`
- `recordCompactionFailure(err error, applied uint64, at time.Time)`
- `recordCompactionSuccess(index uint64, at time.Time)`

- [ ] **Step 5: Wire record points into startup, Ready snapshot, and compaction paths**

In `Start`:

- refactor startup so `s.mu` is not held while running `storageView.load(ctx)`, `StateMachine.Restore(...)`, or other raftlog/state-machine I/O;
- use a short lifecycle critical section to reject duplicate starts, set a new `starting` flag, and clear it on every success/failure path;
- after successful I/O and `RawNode` construction, reacquire `s.mu` only long enough to install channels/transport, register handlers, clear `starting`, and set `started=true`;
- update `cleanupFailedStart()` so it also clears `starting` and preserves the cached failure/restore status;
- after normalizing compaction config, update cached compaction config;
- before returning a startup restore error, call `recordRestoreFailure(snapshot, err)`;
- after successful startup restore, call `recordStartupRestoreSuccess(snapshot)`.

In `run`:

- call `s.recordRaftStatus(rawNode)` anywhere `s.updateLeader(rawNode)` is already called;
- after successful `compactControllerLog`, call `s.recordCompactionSuccess(lastApplied, time.Now())` before `compactor.recordSnapshot(lastApplied)`;
- after failed `compactControllerLog`, call `s.recordCompactionFailure(err, lastApplied, time.Now())` before warning log.

In `restoreReadySnapshot`:

- on restore error, call `recordRestoreFailure(snap, err)` before returning;
- on success, call `recordReadyRestoreSuccess(snap)` before returning.

- [ ] **Step 6: Run controller raft tests**

Run: `go test ./pkg/controller/raft -run 'TestServiceStatus|TestServiceLeaderStatus|TestServiceCompactionFailureDoesNotFailProposalLoop|TestServiceStartRestoresSnapshot|TestServiceIncomingReadySnapshot|TestServiceLaggingFollowerRestoresControllerSnapshot' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/controller/raft/status.go pkg/controller/raft/service.go pkg/controller/raft/service_test.go
git commit -m "feat(controllerraft): expose local raft snapshot status"
```

---

### Task 2: Expose Controller Raft Status Through Cluster API And RPC

**Files:**
- Create: `pkg/cluster/controller_raft_status.go`
- Modify: `pkg/cluster/api.go`
- Modify: `pkg/cluster/controller_handler.go`
- Modify: `pkg/cluster/codec_control.go`
- Modify: `pkg/cluster/codec_control_test.go`
- Create: `pkg/cluster/controller_raft_status_test.go`

- [ ] **Step 1: Write failing cluster API and codec tests**

Create `pkg/cluster/controller_raft_status_test.go` with local merge coverage:

```go
func TestControllerRaftStatusOnNodeMergesLocalServiceStatusAndDurableIndexes(t *testing.T) {
    db, err := raftstorage.Open(filepath.Join(t.TempDir(), "controller-raft"))
    require.NoError(t, err)
    t.Cleanup(func() { _ = db.Close() })

    store := db.ForController()
    hs := raftpb.HardState{Term: 2, Commit: 4}
    snap := raftpb.Snapshot{Metadata: raftpb.SnapshotMetadata{Index: 2, Term: 1}}
    require.NoError(t, store.Save(context.Background(), multiraft.PersistentState{
        HardState: &hs,
        Snapshot:  &snap,
        Entries: []raftpb.Entry{
            {Index: 3, Term: 2, Type: raftpb.EntryNormal},
            {Index: 4, Term: 2, Type: raftpb.EntryNormal},
        },
    }))
    require.NoError(t, store.MarkApplied(context.Background(), 3))

    service := controllerraft.NewService(controllerraft.Config{NodeID: 1, LogCompaction: controllerraft.LogCompactionConfig{Enabled: true, EnabledSet: true, TriggerEntries: 10, CheckInterval: time.Second}})
    cluster := &Cluster{
        cfg:    Config{NodeID: 1},
        router: NewRouter(NewHashSlotTable(4, 1), 1, nil),
        controllerResources: controllerResources{controllerHost: &controllerHost{raftDB: db, service: service}},
    }

    got, err := cluster.ControllerRaftStatusOnNode(context.Background(), 1)
    require.NoError(t, err)
    require.Equal(t, uint64(1), got.NodeID)
    require.Equal(t, uint64(3), got.FirstIndex)
    require.Equal(t, uint64(4), got.LastIndex)
    require.Equal(t, uint64(4), got.CommitIndex)
    require.Equal(t, uint64(3), got.AppliedIndex)
    require.Equal(t, uint64(2), got.SnapshotIndex)
    require.Equal(t, uint64(1), got.SnapshotTerm)
    require.True(t, got.Compaction.Enabled)
}
```

Add peer derivation test in the same file:

```go
func TestControllerRaftStatusDerivesPeerSnapshotFlags(t *testing.T) {
    st := ControllerRaftStatus{FirstIndex: 10, Peers: []ControllerRaftPeerProgress{{NodeID: 2, Next: 9}, {NodeID: 3, Next: 10, PendingSnapshot: 12}}}
    deriveControllerRaftPeerStatus(&st)
    require.True(t, st.Peers[0].NeedsSnapshot)
    require.False(t, st.Peers[0].SnapshotTransferring)
    require.False(t, st.Peers[1].NeedsSnapshot)
    require.True(t, st.Peers[1].SnapshotTransferring)
}
```

In `pkg/cluster/codec_control_test.go`, add `TestControllerRaftStatusRPCCodecRoundTrip` near `TestControllerLogEntriesRPCCodecRoundTrip`.

Also add handler/remote error coverage in `pkg/cluster/controller_raft_status_test.go`:

- `TestControllerHandlerServesControllerRaftStatusWithoutLeaderRedirect`: build a `controllerHandler` with a local `controllerHost` and a service whose leader is not local, issue `controllerRPCControllerRaftStatus`, and assert the response contains status instead of `NotLeader=true`.
- `TestControllerRaftStatusOnNodeReturnsRemoteTargetError`: set `cluster.stopped.Store(true)`, call `ControllerRaftStatusOnNode` for a non-local node, and assert `transport.ErrStopped` is returned unchanged.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/cluster -run 'TestControllerRaftStatus|TestControllerRaftStatusRPCCodecRoundTrip' -count=1`

Expected: FAIL because the cluster status API and RPC codec do not exist.

- [ ] **Step 3: Add cluster DTOs and local/remote status read**

Create `pkg/cluster/controller_raft_status.go`:

```go
// ControllerRaftStatus is a manager-facing read-only status snapshot for one Controller Raft node.
type ControllerRaftStatus struct {
    // NodeID is the node whose local Controller Raft status was read.
    NodeID uint64
    // Role is leader, follower, candidate, or unknown.
    Role string
    // LeaderID is the leader known to the queried node.
    LeaderID uint64
    // Term is the queried node's current Raft term.
    Term uint64
    // FirstIndex is the first available local Controller Raft log index.
    FirstIndex uint64
    // LastIndex is the last available local Controller Raft log index.
    LastIndex uint64
    // CommitIndex is the queried node's durable committed index watermark.
    CommitIndex uint64
    // AppliedIndex is the queried node's durable applied index watermark.
    AppliedIndex uint64
    // SnapshotIndex is the latest persisted Controller Raft snapshot index.
    SnapshotIndex uint64
    // SnapshotTerm is the latest persisted Controller Raft snapshot term.
    SnapshotTerm uint64
    // Compaction describes local Controller Raft log compaction state.
    Compaction ControllerRaftCompactionStatus
    // Restore describes local Controller metadata snapshot restore state.
    Restore ControllerRaftRestoreStatus
    // Peers contains leader-side follower progress.
    Peers []ControllerRaftPeerProgress
}
```

Add matching `ControllerRaftCompactionStatus`, `ControllerRaftRestoreStatus`, and `ControllerRaftPeerProgress` with English comments. `ControllerRaftPeerProgress` must include both `NeedsSnapshot` and `SnapshotTransferring`.

Implement:

```go
func (c *Cluster) ControllerRaftStatusOnNode(ctx context.Context, nodeID uint64) (ControllerRaftStatus, error) {
    if c == nil {
        return ControllerRaftStatus{}, ErrNotStarted
    }
    if c.IsLocal(multiraft.NodeID(nodeID)) {
        return c.localControllerRaftStatus(ctx, nodeID)
    }
    return c.remoteControllerRaftStatus(ctx, nodeID)
}
```

Local path:

- require `c.controllerHost`, `c.controllerHost.service`, and `c.controllerHost.raftDB`;
- read `controllerHost.service.Status()`;
- read `storage.InitialState(ctx)`, `FirstIndex(ctx)`, `LastIndex(ctx)`, and `Snapshot(ctx)` from `c.controllerHost.raftDB.ForController()`;
- merge durable watermarks, overriding volatile commit/applied values with durable values;
- call `deriveControllerRaftPeerStatus(&status)` after `FirstIndex` is known.

Remote path:

- encode `controllerRPCRequest{Kind: controllerRPCControllerRaftStatus}`;
- call `c.controllerRPCService(ctx, multiraft.NodeID(nodeID), body)` directly;
- decode `resp.ControllerRaftStatus` and return it.

- [ ] **Step 4: Extend the public cluster API**

In `pkg/cluster/api.go`, add near Controller log entries:

```go
// ControllerRaftStatusOnNode returns one node's local Controller Raft status.
ControllerRaftStatusOnNode(ctx context.Context, nodeID uint64) (ControllerRaftStatus, error)
```

- [ ] **Step 5: Add node-local Controller RPC handler**

In `pkg/cluster/controller_handler.go`, add a case next to `controllerRPCControllerLogs`, before leader-only cases:

```go
case controllerRPCControllerRaftStatus:
    status, err := c.localControllerRaftStatus(ctx, uint64(c.NodeID()))
    if err != nil {
        return nil, err
    }
    return encodeControllerResponse(req.Kind, controllerRPCResponse{ControllerRaftStatus: &status})
```

This case must not check local leadership and must not redirect to the Controller leader.

- [ ] **Step 6: Add Controller RPC codec support**

In `pkg/cluster/codec_control.go`:

- add string kind `controllerRPCControllerRaftStatus = "controller_raft_status"`;
- add byte enum `controllerKindControllerRaftStatus` at the end for compatibility with existing codes;
- update `controllerKindCode` and `controllerKindName`;
- update request payload encode/decode as an empty-payload request;
- add `ControllerRaftStatus *ControllerRaftStatus` to `controllerRPCResponse`;
- update response payload encode/decode.

Use a stable binary format:

```text
NodeID, Role, LeaderID, Term,
FirstIndex, LastIndex, CommitIndex, AppliedIndex, SnapshotIndex, SnapshotTerm,
Compaction.Enabled, TriggerEntries, CheckIntervalNanos, LastSnapshotIndex, LastSnapshotAtNanos, LastCheckAtNanos, LastError, LastErrorAtNanos, Degraded,
Restore.LastSnapshotIndex, LastSnapshotTerm, LastRestoredAtNanos, LastError, LastErrorAtNanos, Failed,
PeerCount, repeated(NodeID, Match, Next, State, PendingSnapshot, RecentActive, NeedsSnapshot, SnapshotTransferring)
```

Use existing helpers `appendString`, `readString`, `appendInt64`, `readInt64`, and single bytes for booleans. Add small local helpers if useful:

```go
func appendBool(dst []byte, value bool) []byte
func readBool(src []byte) (bool, []byte, error)
```

- [ ] **Step 7: Run cluster tests**

Run: `go test ./pkg/cluster -run 'TestControllerRaftStatus|TestControllerRaftStatusRPCCodecRoundTrip|TestControllerHandler.*ControllerRaftStatus' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/cluster/controller_raft_status.go pkg/cluster/api.go pkg/cluster/controller_handler.go pkg/cluster/codec_control.go pkg/cluster/codec_control_test.go pkg/cluster/controller_raft_status_test.go
git commit -m "feat(cluster): expose controller raft status"
```

---

### Task 3: Map Controller Raft Status Into Management Usecases

**Files:**
- Create: `internal/usecase/management/controller_raft_status.go`
- Create: `internal/usecase/management/controller_raft_status_test.go`
- Modify: `internal/usecase/management/app.go`
- Modify: `internal/usecase/management/nodes.go`
- Modify: `internal/usecase/management/nodes_test.go`
- Modify any management test fakes that implement `ClusterReader`, including `internal/usecase/management/node_operator_test.go` if required by compilation.

- [ ] **Step 1: Write failing management status tests**

Create `internal/usecase/management/controller_raft_status_test.go`:

```go
func TestGetControllerRaftStatusMapsFullStatus(t *testing.T) {
    restoredAt := time.Unix(1715000000, 0).UTC()
    checkedAt := restoredAt.Add(time.Second)
    app := New(Options{Cluster: fakeClusterReader{controllerRaftStatus: map[uint64]raftcluster.ControllerRaftStatus{
        2: {
            NodeID: 2, Role: "leader", LeaderID: 2, Term: 4,
            FirstIndex: 10, LastIndex: 30, CommitIndex: 29, AppliedIndex: 28, SnapshotIndex: 9, SnapshotTerm: 3,
            Compaction: raftcluster.ControllerRaftCompactionStatus{Enabled: true, TriggerEntries: 100, CheckInterval: time.Second, LastSnapshotIndex: 9, LastSnapshotAt: restoredAt, LastCheckAt: checkedAt},
            Restore: raftcluster.ControllerRaftRestoreStatus{LastSnapshotIndex: 9, LastSnapshotTerm: 3, LastRestoredAt: restoredAt},
            Peers: []raftcluster.ControllerRaftPeerProgress{{NodeID: 3, Match: 20, Next: 21, State: "StateReplicate", RecentActive: true}},
        },
    }}})

    got, err := app.GetControllerRaftStatus(context.Background(), 2)
    require.NoError(t, err)
    require.Equal(t, uint64(2), got.NodeID)
    require.Equal(t, "leader", got.Role)
    require.Equal(t, "append_catchup", got.Health)
    require.Len(t, got.Peers, 1)
}
```

Add health derivation tests:

- `TestControllerRaftHealthDerivesHealthy`
- `TestControllerRaftHealthDerivesAppendCatchup`
- `TestControllerRaftHealthDerivesSnapshotRequired`
- `TestControllerRaftHealthDerivesSnapshotTransferring`
- `TestControllerRaftHealthPrefersRestoreFailed`
- `TestControllerRaftHealthPrefersCompactionDegraded`

In `internal/usecase/management/nodes_test.go`, add:

- `TestListNodesIncludesLocalControllerRaftSummary`
- `TestListNodesDoesNotFanOutControllerRaftStatus`

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/usecase/management -run 'TestGetControllerRaftStatus|TestControllerRaftHealth|TestListNodesIncludesLocalControllerRaftSummary|TestListNodesDoesNotFanOutControllerRaftStatus' -count=1`

Expected: FAIL because management DTOs and `ClusterReader.ControllerRaftStatusOnNode` do not exist.

- [ ] **Step 3: Extend `ClusterReader`**

In `internal/usecase/management/app.go`, add near Controller log entries:

```go
// ControllerRaftStatusOnNode returns one node's local Controller Raft status.
ControllerRaftStatusOnNode(ctx context.Context, nodeID uint64) (raftcluster.ControllerRaftStatus, error)
```

Update all fake cluster readers in management tests with a stub method returning zero status unless the test configures a value.

- [ ] **Step 4: Add management DTOs and health derivation**

Create `internal/usecase/management/controller_raft_status.go`:

```go
const (
    ControllerRaftHealthHealthy              = "healthy"
    ControllerRaftHealthAppendCatchup        = "append_catchup"
    ControllerRaftHealthSnapshotRequired     = "snapshot_required"
    ControllerRaftHealthSnapshotTransferring = "snapshot_transferring"
    ControllerRaftHealthRestoreFailed        = "restore_failed"
    ControllerRaftHealthCompactionDegraded   = "compaction_degraded"
    ControllerRaftHealthUnknown              = "unknown"
)

// ControllerRaftStatusResponse is the manager-facing full Controller Raft status.
type ControllerRaftStatusResponse struct {
    NodeID uint64
    Role string
    LeaderID uint64
    Term uint64
    Health string
    FirstIndex uint64
    LastIndex uint64
    CommitIndex uint64
    AppliedIndex uint64
    SnapshotIndex uint64
    SnapshotTerm uint64
    Compaction ControllerRaftCompactionStatus
    Restore ControllerRaftRestoreStatus
    Peers []ControllerRaftPeerProgress
}
```

Implement:

```go
func (a *App) GetControllerRaftStatus(ctx context.Context, nodeID uint64) (ControllerRaftStatusResponse, error) {
    if a == nil || a.cluster == nil {
        return ControllerRaftStatusResponse{}, nil
    }
    status, err := a.cluster.ControllerRaftStatusOnNode(ctx, nodeID)
    if err != nil {
        return ControllerRaftStatusResponse{}, err
    }
    return controllerRaftStatusResponse(status), nil
}
```

Implement health priority:

```go
func controllerRaftHealth(status raftcluster.ControllerRaftStatus) string {
    if status.NodeID == 0 || status.Role == "" || status.Role == "unknown" {
        return ControllerRaftHealthUnknown
    }
    if status.Restore.Failed {
        return ControllerRaftHealthRestoreFailed
    }
    if status.Compaction.Degraded {
        return ControllerRaftHealthCompactionDegraded
    }
    transferring := false
    required := false
    appendCatchup := false
    for _, peer := range status.Peers {
        if peer.SnapshotTransferring {
            transferring = true
        }
        if peer.NeedsSnapshot {
            required = true
        }
        if peer.Match < status.CommitIndex && !peer.NeedsSnapshot && !peer.SnapshotTransferring {
            appendCatchup = true
        }
    }
    switch {
    case transferring:
        return ControllerRaftHealthSnapshotTransferring
    case required:
        return ControllerRaftHealthSnapshotRequired
    case appendCatchup:
        return ControllerRaftHealthAppendCatchup
    default:
        return ControllerRaftHealthHealthy
    }
}
```

- [ ] **Step 5: Add local-only node list summary fields**

In `internal/usecase/management/nodes.go`, extend `NodeController`:

```go
// RaftHealth is the summarized local Controller Raft health state.
RaftHealth string
// FirstIndex is the first available local Controller Raft log index.
FirstIndex uint64
// AppliedIndex is the queried node's applied index watermark.
AppliedIndex uint64
// SnapshotIndex is the latest persisted Controller Raft snapshot index.
SnapshotIndex uint64
```

Add helper:

```go
func (a *App) localControllerRaftSummary(ctx context.Context, nodeID uint64) NodeController {
    summary := NodeController{RaftHealth: ControllerRaftHealthUnknown}
    if a == nil || a.cluster == nil || nodeID == 0 || nodeID != a.localNodeID || !a.isControllerPeer(nodeID) {
        return summary
    }
    status, err := a.cluster.ControllerRaftStatusOnNode(ctx, nodeID)
    if err != nil {
        return summary
    }
    summary.RaftHealth = controllerRaftHealth(status)
    summary.FirstIndex = status.FirstIndex
    summary.AppliedIndex = status.AppliedIndex
    summary.SnapshotIndex = status.SnapshotIndex
    return summary
}
```

In `managerNode`, build the existing role/voter/leader fields first, then merge in the local summary only for the local Controller voter. Do not fan out to remote nodes from list views.

- [ ] **Step 6: Run management tests**

Run: `go test ./internal/usecase/management -run 'TestGetControllerRaftStatus|TestControllerRaftHealth|TestListNodes|TestGetNode' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/usecase/management/controller_raft_status.go internal/usecase/management/controller_raft_status_test.go internal/usecase/management/app.go internal/usecase/management/nodes.go internal/usecase/management/nodes_test.go internal/usecase/management/node_operator_test.go
git commit -m "feat(management): map controller raft health"
```

If `node_operator_test.go` did not require changes, omit it from `git add`.

---

### Task 4: Add Manager HTTP Endpoint And DTO Fields

**Files:**
- Create: `internal/access/manager/controller_raft_status.go`
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Modify: `internal/access/manager/nodes.go`
- Modify: `internal/access/manager/server_test.go`

- [ ] **Step 1: Write failing manager HTTP tests**

In `internal/access/manager/server_test.go`, add tests near existing node and controller log tests:

```go
func TestManagerNodeControllerRaftReturnsStatus(t *testing.T) {
    restoredAt := time.Unix(1715000000, 0).UTC()
    var nodeIDSink uint64
    srv := New(Options{Management: managementStub{
        controllerRaftStatusNodeIDSink: &nodeIDSink,
        controllerRaftStatus: managementusecase.ControllerRaftStatusResponse{
            NodeID: 2, Role: "leader", LeaderID: 2, Term: 3, Health: "healthy",
            FirstIndex: 10, LastIndex: 20, CommitIndex: 19, AppliedIndex: 18, SnapshotIndex: 9, SnapshotTerm: 2,
            Restore: managementusecase.ControllerRaftRestoreStatus{LastSnapshotIndex: 9, LastSnapshotTerm: 2, LastRestoredAt: restoredAt},
            Peers: []managementusecase.ControllerRaftPeerProgress{{NodeID: 3, Match: 18, Next: 19, State: "StateReplicate", RecentActive: true}},
        },
    }})

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/manager/nodes/2/controller-raft", nil)
    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusOK, rec.Code)
    require.Equal(t, uint64(2), nodeIDSink)
    var body ControllerRaftStatusDTO
    require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
    require.Equal(t, uint64(2), body.NodeID)
    require.Equal(t, "leader", body.Role)
    require.Equal(t, "healthy", body.Health)
    require.Equal(t, uint64(10), body.FirstIndex)
    require.Equal(t, restoredAt, body.Restore.LastRestoredAt)
    require.Len(t, body.Peers, 1)
    require.Equal(t, uint64(3), body.Peers[0].NodeID)
}
```

Also add:

- `TestManagerNodeControllerRaftRejectsInvalidNodeID`
- `TestManagerNodeControllerRaftReturnsServiceUnavailableWhenManagementMissing`
- `TestManagerNodeControllerRaftReturnsServiceUnavailableWhenStatusUnavailable`
- `TestManagerNodeControllerRaftReturnsServiceUnavailableWhenTargetUnavailable`
- if auth tests exist for route permissions, add `TestManagerNodeControllerRaftRejectsInsufficientPermission`

Update existing node list JSON tests so `controller` includes `raft_health`, `first_index`, `applied_index`, and `snapshot_index`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/access/manager -run 'TestManagerNodeControllerRaft|TestManagerNodes|TestManagerNodeDetail' -count=1`

Expected: FAIL because endpoint, interface method, and DTO fields do not exist.

- [ ] **Step 3: Extend manager `Management` interface**

In `internal/access/manager/server.go`, add near Controller log entries:

```go
// GetControllerRaftStatus returns one node-local Controller Raft status snapshot.
GetControllerRaftStatus(ctx context.Context, nodeID uint64) (managementusecase.ControllerRaftStatusResponse, error)
```

Update `managementStub` in `internal/access/manager/server_test.go` with fields and method.

- [ ] **Step 4: Register the read-only route with the right permissions**

In `internal/access/manager/routes.go`, add a group near node reads or controller reads:

```go
controllerRaft := s.engine.Group("/manager")
if s.auth.enabled() {
    controllerRaft.Use(s.requirePermission("cluster.node", "r"))
    controllerRaft.Use(s.requirePermission("cluster.controller", "r"))
}
controllerRaft.GET("/nodes/:node_id/controller-raft", s.handleNodeControllerRaft)
```

- [ ] **Step 5: Add handler and HTTP DTO mapping**

Create `internal/access/manager/controller_raft_status.go`:

```go
// ControllerRaftStatusDTO is the manager Controller Raft status response body.
type ControllerRaftStatusDTO struct {
    NodeID uint64 `json:"node_id"`
    Role string `json:"role"`
    LeaderID uint64 `json:"leader_id"`
    Term uint64 `json:"term"`
    Health string `json:"health"`
    FirstIndex uint64 `json:"first_index"`
    LastIndex uint64 `json:"last_index"`
    CommitIndex uint64 `json:"commit_index"`
    AppliedIndex uint64 `json:"applied_index"`
    SnapshotIndex uint64 `json:"snapshot_index"`
    SnapshotTerm uint64 `json:"snapshot_term"`
    Compaction ControllerRaftCompactionDTO `json:"compaction"`
    Restore ControllerRaftRestoreDTO `json:"restore"`
    Peers []ControllerRaftPeerDTO `json:"peers"`
}
```

Implement handler:

```go
func (s *Server) handleNodeControllerRaft(c *gin.Context) {
    if s.management == nil {
        jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
        return
    }
    nodeID, err := parseNodeIDParam(c.Param("node_id"))
    if err != nil {
        jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
        return
    }
    status, err := s.management.GetControllerRaftStatus(c.Request.Context(), nodeID)
    if err != nil {
        if controllerRaftStatusUnavailable(err) {
            jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller raft status unavailable")
            return
        }
        jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
        return
    }
    c.JSON(http.StatusOK, controllerRaftStatusDTO(status))
}

func controllerRaftStatusUnavailable(err error) bool {
    return leaderConsistentReadUnavailable(err) ||
        errors.Is(err, transport.ErrNodeNotFound) ||
        errors.Is(err, transport.ErrStopped)
}
```

- [ ] **Step 6: Add node summary JSON fields**

In `internal/access/manager/nodes.go`, extend `NodeControllerDTO`:

```go
// RaftHealth is the summarized local Controller Raft health state.
RaftHealth string `json:"raft_health"`
// FirstIndex is the first available local Controller Raft log index.
FirstIndex uint64 `json:"first_index"`
// AppliedIndex is the queried node's applied index watermark.
AppliedIndex uint64 `json:"applied_index"`
// SnapshotIndex is the latest persisted Controller Raft snapshot index.
SnapshotIndex uint64 `json:"snapshot_index"`
```

Update `nodeDTO` to map the fields.

- [ ] **Step 7: Run manager tests**

Run: `go test ./internal/access/manager -run 'TestManagerNodeControllerRaft|TestManagerNodes|TestManagerNodeDetail|TestManagerControllerLogs' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/access/manager/controller_raft_status.go internal/access/manager/server.go internal/access/manager/routes.go internal/access/manager/nodes.go internal/access/manager/server_test.go
git commit -m "feat(manager): expose controller raft status endpoint"
```

---

### Task 5: Update Flow Docs And Run Focused Verification

**Files:**
- Modify: `pkg/controller/FLOW.md`
- Modify: `pkg/cluster/FLOW.md`
- Optional modify: `docs/development/PROJECT_KNOWLEDGE.md`

- [ ] **Step 1: Update Controller flow documentation**

In `pkg/controller/FLOW.md`, update the `raft/` row and section 7 to mention:

- Controller Raft service now exposes node-local status via a cached snapshot;
- status includes role/leader/term, compaction state, restore state, and leader-side follower progress;
- `RawNode` remains confined to the run loop.

- [ ] **Step 2: Update Cluster flow documentation**

In `pkg/cluster/FLOW.md`, update the public API list near Controller log entries:

```text
API.ControllerRaftStatusOnNode(ctx, nodeID)  // 读取某节点本地 Controller Raft role/index/compaction/restore/peer progress；本地走 controllerHost.service + raftlog，远程走 node-local Controller RPC
```

Also document that the read is node-local and must not use leader-centric controller client routing.

- [ ] **Step 3: Record concise project knowledge if absent**

If `docs/development/PROJECT_KNOWLEDGE.md` does not already contain this rule, add one short bullet:

```text
- Controller Raft status/log diagnostics are node-local reads; remote reads must target the requested node directly instead of using leader-centric Controller client routing.
```

- [ ] **Step 4: Run package verification**

Run focused tests first:

```bash
go test ./pkg/controller/raft -count=1
go test ./pkg/cluster -run 'TestControllerRaftStatus|TestControllerRaftStatusRPCCodecRoundTrip|TestControllerHandler.*ControllerRaftStatus|TestControllerLogEntries' -count=1
go test ./internal/usecase/management -count=1
go test ./internal/access/manager -count=1
```

Expected: PASS.

Then run broader affected packages:

```bash
go test ./pkg/controller/... ./pkg/cluster ./internal/usecase/management ./internal/access/manager -count=1
```

Expected: PASS.

Do not run `go test -tags=integration ./...` unless explicitly requested.

- [ ] **Step 5: Commit docs**

```bash
git add pkg/controller/FLOW.md pkg/cluster/FLOW.md docs/development/PROJECT_KNOWLEDGE.md
git commit -m "docs(controller): document raft status diagnostics"
```

If `docs/development/PROJECT_KNOWLEDGE.md` was not changed, omit it from `git add`.

---

## Final Verification Before Handoff

- [ ] Run: `git status --short`
- [ ] Run: `go test ./pkg/controller/raft -count=1`
- [ ] Run: `go test ./pkg/cluster -count=1`
- [ ] Run: `go test ./internal/usecase/management -count=1`
- [ ] Run: `go test ./internal/access/manager -count=1`
- [ ] Confirm no unrelated dirty files were staged.
- [ ] Summarize implemented endpoint: `GET /manager/nodes/:node_id/controller-raft`.
