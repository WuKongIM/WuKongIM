# ControllerV2 Raft Performance Refactor Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace ControllerV2's synchronous Pebble-backed Raft apply path with an etcd-shaped WAL, FIFO apply scheduler, batched FSM apply, and snapshot/compaction pipeline.

**Architecture:** `pkg/controllerv2/raft.Service` becomes a facade over a responsive RawNode loop, a dedicated ControllerV2 `raftstore`, a FIFO `applyScheduler`, and a `proposalTracker`. Raft entries are stored in append-only WAL segments, apply metadata advances by batch, and `cluster-state.json` is saved once per applied command batch.

**Tech Stack:** Go, `go.etcd.io/raft/v3`, ControllerV2 `command`/`fsm`/`state`/`statefile`, standard-library file IO, existing `testify/require` tests.

---

## Implementation Notes

- Preserve the project rule that a single-node deployment is a single-node cluster.
- Do not use or stage unrelated working-tree changes. At the time this plan was written, unrelated changes existed under `pkg/clusterv2` and `AGENTS.md`; leave them untouched unless the user explicitly asks.
- The approved spec is `docs/superpowers/specs/2026-05-25-controllerv2-raft-performance-design.md`.
- This plan intentionally removes compatibility with the current ControllerV2 `pkg/raftlog.ForController()` storage shape.
- Use TDD for every task: write the smallest failing test first, run it, implement, run focused tests, then commit.

## File Structure

Create or modify these files:

- Modify: `pkg/controllerv2/fsm/fsm.go` - add `AppliedCommand`, `BatchApplyResult`, `ApplyBatch`, and make `Apply` call the batch path.
- Modify: `pkg/controllerv2/fsm/fsm_test.go` - add batch apply tests and save-count test helpers.
- Modify: `pkg/controllerv2/raft/config.go` - replace `Storage multiraft.Storage` with `RaftDir string` and performance knobs, with detailed English comments.
- Modify: `pkg/controllerv2/raft/service.go` - keep the public facade, lifecycle, `Propose`, `Step`, status, and error helpers; delegate runtime work to `raftLoop` and scheduler.
- Create: `pkg/controllerv2/raft/loop.go` - own `RawNode`, Ready handling, WAL persistence, transport ordering, conf-change barriers, and `Advance`.
- Create: `pkg/controllerv2/raft/apply_scheduler.go` - FIFO apply worker, batching, replay, snapshot trigger callbacks, and proposal completion.
- Create: `pkg/controllerv2/raft/proposal_tracker.go` - queue local proposals, bind them to appended indexes, complete/fail waiters.
- Create: `pkg/controllerv2/raft/raftstore/config.go` - store defaults and validation.
- Create: `pkg/controllerv2/raft/raftstore/record.go` - WAL record types and frame encoding.
- Create: `pkg/controllerv2/raft/raftstore/wal.go` - append/read/recover WAL segment implementation.
- Create: `pkg/controllerv2/raft/raftstore/metadata.go` - atomic `meta.json` persistence and recovery validation.
- Create: `pkg/controllerv2/raft/raftstore/snapshot.go` - snapshot envelope, atomic save/load, and release helpers.
- Create: `pkg/controllerv2/raft/raftstore/memory.go` - bounded `etcdraft.Storage` implementation with WAL read-through.
- Create: `pkg/controllerv2/raft/raftstore/store.go` - high-level store facade used by `raft.Service`.
- Create: `pkg/controllerv2/raft/raftstore/*_test.go` - WAL, metadata, snapshot, storage API, and compaction tests.
- Modify: `pkg/controllerv2/raft/service_test.go` - update test helpers to use `RaftDir`, add async apply and snapshot suffix tests, and remove `raftlog` controller seeding helpers.
- Modify: `pkg/controllerv2/FLOW.md` - document the new flow.
- Optional modify: `wukongim.conf.example` - only if `RaftDir` or performance knobs become user-facing application config in this implementation.

---

### Task 1: Add Batched FSM Apply

**Files:**
- Modify: `pkg/controllerv2/fsm/fsm.go`
- Modify: `pkg/controllerv2/fsm/fsm_test.go`

- [ ] **Step 1: Write failing batch apply test for one durable save per batch**

Add this test helper and test to `pkg/controllerv2/fsm/fsm_test.go`:

```go
type countingStore struct {
    *statefile.Store
    saves atomic.Int32
}

func (s *countingStore) Save(ctx context.Context, st state.ClusterState) error {
    s.saves.Add(1)
    return s.Store.Save(ctx, st)
}

func TestApplyBatchPersistsOnceAndReturnsPerEntryResults(t *testing.T) {
    ctx := context.Background()
    dir := t.TempDir()
    store := &countingStore{Store: statefile.New(filepath.Join(dir, "cluster-state.json"))}
    sm, err := New(store)
    require.NoError(t, err)

    init := initCommand()
    node := baseNodes()[0]
    node.Name = "node-1-batched"
    expected := uint64(1)

    result, err := sm.ApplyBatch(ctx, []AppliedCommand{
        {Index: 10, Term: 1, Command: init},
        {Index: 11, Term: 1, Command: command.Command{Kind: command.KindUpsertNode, ExpectedRevision: &expected, Node: &node}},
    })
    require.NoError(t, err)
    require.Len(t, result.Results, 2)
    require.Equal(t, uint64(1), result.Results[0].Revision)
    require.Equal(t, uint64(2), result.Results[1].Revision)
    require.Equal(t, int32(1), store.saves.Load())

    snap := sm.Snapshot(ctx)
    require.Equal(t, uint64(2), snap.Revision)
    require.Equal(t, uint64(11), snap.AppliedRaftIndex)
    var got state.Node
    for _, candidate := range snap.Nodes {
        if candidate.NodeID == 1 {
            got = candidate
            break
        }
    }
    require.Equal(t, "node-1-batched", got.Name)
}
```

Because `StateMachine` currently stores `*statefile.Store` concretely, first introduce an exported store interface so tests can count saves without changing production semantics:

```go
// Store persists and loads ControllerV2 cluster state snapshots.
type Store interface {
    Load(context.Context) (state.ClusterState, error)
    Save(context.Context, state.ClusterState) error
}
```

- [ ] **Step 2: Run the test and verify it fails to compile**

Run: `go test ./pkg/controllerv2/fsm -run TestApplyBatchPersistsOnceAndReturnsPerEntryResults -count=1`

Expected: FAIL with `undefined: AppliedCommand` or `sm.ApplyBatch undefined`.

- [ ] **Step 3: Implement `ApplyBatch` and store interface**

In `pkg/controllerv2/fsm/fsm.go`, add English comments and these exported types:

```go
// AppliedCommand binds a decoded ControllerV2 command to its committed Raft position.
type AppliedCommand struct {
    // Index is the committed Raft log index for Command.
    Index uint64
    // Term is the committed Raft term for Command.
    Term uint64
    // Command is the deterministic ControllerV2 mutation to apply.
    Command command.Command
}

// BatchApplyResult describes the deterministic outcome of applying a command batch.
type BatchApplyResult struct {
    // Results contains one result per input command in the same order.
    Results []ApplyResult
    // FinalState is the state published after durable save, or the current state when no state exists yet.
    FinalState state.ClusterState
}
```

Change `StateMachine.store` to `Store`, change `New` to `func New(store Store) (*StateMachine, error)`, keep nil validation, and implement:

```go
func (sm *StateMachine) Apply(ctx context.Context, raftIndex uint64, cmd command.Command) (ApplyResult, error) {
    result, err := sm.ApplyBatch(ctx, []AppliedCommand{{Index: raftIndex, Command: cmd}})
    if err != nil {
        return ApplyResult{}, err
    }
    if len(result.Results) == 0 {
        return ApplyResult{}, nil
    }
    return result.Results[0], nil
}

func (sm *StateMachine) ApplyBatch(ctx context.Context, entries []AppliedCommand) (BatchApplyResult, error) {
    sm.mu.Lock()
    defer sm.mu.Unlock()

    current := sm.state.Clone()
    next := current.Clone()
    out := BatchApplyResult{Results: make([]ApplyResult, 0, len(entries))}

    for _, entry := range entries {
        if current.Revision != 0 && entry.Index <= next.AppliedRaftIndex {
            out.Results = append(out.Results, ApplyResult{Noop: true, Reason: ReasonAlreadyApplied, Revision: next.Revision, AppliedRaftIndex: next.AppliedRaftIndex})
            continue
        }
        result := sm.applyMutation(&next, entry.Index, entry.Command)
        if next.Revision != 0 && entry.Index > next.AppliedRaftIndex {
            next.AppliedRaftIndex = entry.Index
        }
        result.Revision = next.Revision
        result.AppliedRaftIndex = next.AppliedRaftIndex
        if next.Revision == 0 && result.Rejected {
            result.AppliedRaftIndex = entry.Index
        }
        out.Results = append(out.Results, result)
    }

    if next.Revision == 0 {
        out.FinalState = next.Clone()
        return out, nil
    }
    checksum, err := state.Checksum(next)
    if err != nil {
        sm.degraded = true
        return out, err
    }
    next.Checksum = checksum
    if err := sm.store.Save(ctx, next); err != nil {
        sm.degraded = true
        return out, err
    }
    sm.state = next.Clone()
    sm.degraded = false
    out.FinalState = sm.state.Clone()
    return out, nil
}
```

- [ ] **Step 4: Run focused FSM tests**

Run: `go test ./pkg/controllerv2/fsm -count=1`

Expected: PASS.

- [ ] **Step 5: Commit batched FSM apply**

```bash
git add pkg/controllerv2/fsm/fsm.go pkg/controllerv2/fsm/fsm_test.go
git commit -m "feat(controllerv2): batch fsm apply"
```

---

### Task 2: Implement Raftstore WAL Record Codec

**Files:**
- Create: `pkg/controllerv2/raft/raftstore/record.go`
- Create: `pkg/controllerv2/raft/raftstore/record_test.go`

- [ ] **Step 1: Write failing record round-trip and CRC tests**

Create `record_test.go` with tests covering entries, hard state, applied index, snapshot marker, CRC mismatch, and truncated frame:

```go
func TestRecordRoundTripEntries(t *testing.T) {
    entries := []raftpb.Entry{{Index: 1, Term: 1, Type: raftpb.EntryNormal, Data: []byte("one")}}
    payload, err := marshalEntryRecord(entries)
    require.NoError(t, err)
    rec := walRecord{Type: recordEntries, Payload: payload}

    var buf bytes.Buffer
    require.NoError(t, writeRecord(&buf, rec, 0))
    got, crc, err := readRecord(&buf, 0)
    require.NoError(t, err)
    require.NotZero(t, crc)
    require.Equal(t, recordEntries, got.Type)
    decoded, err := unmarshalEntryRecord(got.Payload)
    require.NoError(t, err)
    require.Equal(t, entries, decoded)
}

func TestReadRecordRejectsCorruptCRC(t *testing.T) {
    var buf bytes.Buffer
    require.NoError(t, writeRecord(&buf, walRecord{Type: recordAppliedIndex, Payload: marshalUint64(9)}, 0))
    data := buf.Bytes()
    data[len(data)-1] ^= 0xff
    _, _, err := readRecord(bytes.NewReader(data), 0)
    require.ErrorIs(t, err, ErrCRCMismatch)
}
```

- [ ] **Step 2: Run record tests and verify they fail**

Run: `go test ./pkg/controllerv2/raft/raftstore -run 'TestRecord' -count=1`

Expected: FAIL because the package and record helpers do not exist.

- [ ] **Step 3: Implement record frame encoding**

Create `record.go` with this frame format:

```text
uint32 length | uint8 type | uint32 rolling_crc | payload bytes
```

Use Castagnoli CRC32 over previous CRC, record type byte, and payload. Define errors with English comments:

```go
var (
    // ErrCRCMismatch indicates that a WAL record failed rolling CRC validation.
    ErrCRCMismatch = errors.New("controllerv2/raftstore: crc mismatch")
    // ErrTruncatedRecord indicates that the WAL ended in the middle of a frame.
    ErrTruncatedRecord = errors.New("controllerv2/raftstore: truncated record")
)
```

Define record types:

```go
type recordType uint8

const (
    recordSegmentHeader recordType = iota + 1
    recordEntries
    recordHardState
    recordSnapshot
    recordAppliedIndex
)
```

Marshal payloads with protobuf `Marshal` for `raftpb.Entry`, `raftpb.HardState`, and `raftpb.SnapshotMetadata`. For entry batches, encode a `uint32` count followed by length-prefixed entry bytes.

- [ ] **Step 4: Run record tests**

Run: `go test ./pkg/controllerv2/raft/raftstore -run 'TestRecord' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit WAL record codec**

```bash
git add pkg/controllerv2/raft/raftstore/record.go pkg/controllerv2/raft/raftstore/record_test.go
git commit -m "feat(controllerv2): add raft wal record codec"
```

---

### Task 3: Implement WAL Segments and Recovery

**Files:**
- Create: `pkg/controllerv2/raft/raftstore/config.go`
- Create: `pkg/controllerv2/raft/raftstore/wal.go`
- Create: `pkg/controllerv2/raft/raftstore/wal_test.go`

- [ ] **Step 1: Write failing WAL append/reopen test**

Create `wal_test.go`:

```go
func TestWALAppendReadReopen(t *testing.T) {
    dir := filepath.Join(t.TempDir(), "wal")
    w, err := openWAL(walConfig{Dir: dir, NodeID: 1, SegmentSize: 1 << 20})
    require.NoError(t, err)
    entries := []raftpb.Entry{{Index: 1, Term: 1, Data: []byte("cmd")}}
    hs := raftpb.HardState{Term: 1, Vote: 1, Commit: 1}
    require.NoError(t, w.appendReady(context.Background(), hs, entries, raftpb.SnapshotMetadata{}))
    require.NoError(t, w.close())

    reopened, err := openWAL(walConfig{Dir: dir, NodeID: 1, SegmentSize: 1 << 20})
    require.NoError(t, err)
    defer reopened.close()
    state, err := reopened.replay()
    require.NoError(t, err)
    require.Equal(t, hs, state.HardState)
    require.Equal(t, entries, state.Entries)
}
```

- [ ] **Step 2: Write failing segment cut and truncated-tail tests**

Add:

```go
func TestWALCutsSegmentsAndRecoversCompleteRecords(t *testing.T) {
    dir := filepath.Join(t.TempDir(), "wal")
    w, err := openWAL(walConfig{Dir: dir, NodeID: 1, SegmentSize: 256})
    require.NoError(t, err)
    for i := uint64(1); i <= 20; i++ {
        hs := raftpb.HardState{Term: 1, Vote: 1, Commit: i}
        require.NoError(t, w.appendReady(context.Background(), hs, []raftpb.Entry{{Index: i, Term: 1, Data: bytes.Repeat([]byte{'x'}, 64)}}, raftpb.SnapshotMetadata{}))
    }
    require.NoError(t, w.close())

    files, err := filepath.Glob(filepath.Join(dir, "*.wal"))
    require.NoError(t, err)
    require.Greater(t, len(files), 1)

    tail := files[len(files)-1]
    f, err := os.OpenFile(tail, os.O_RDWR, 0)
    require.NoError(t, err)
    info, err := f.Stat()
    require.NoError(t, err)
    require.NoError(t, f.Truncate(info.Size()-3))
    require.NoError(t, f.Close())

    reopened, err := openWAL(walConfig{Dir: dir, NodeID: 1, SegmentSize: 256})
    require.NoError(t, err)
    defer reopened.close()
    state, err := reopened.replay()
    require.NoError(t, err)
    require.NotEmpty(t, state.Entries)
    require.LessOrEqual(t, state.HardState.Commit, uint64(20))
}
```

- [ ] **Step 3: Run WAL tests and verify failure**

Run: `go test ./pkg/controllerv2/raft/raftstore -run 'TestWAL' -count=1`

Expected: FAIL because WAL implementation does not exist.

- [ ] **Step 4: Implement WAL config and segment IO**

In `config.go` define:

```go
const (
    defaultWALSegmentSize = uint64(64 << 20)
)

type Config struct {
    // Dir is the ControllerV2 Raft storage root directory.
    Dir string
    // NodeID is the local ControllerV2 Raft node ID persisted in segment headers.
    NodeID uint64
    // SegmentSize controls WAL file rollover size in bytes.
    SegmentSize uint64
}
```

In `wal.go`, implement:

```go
type replayState struct {
    HardState    raftpb.HardState
    Entries      []raftpb.Entry
    Snapshot     raftpb.SnapshotMetadata
    AppliedIndex uint64
    ConfState    raftpb.ConfState
}

type wal struct {
    cfg walConfig
    mu sync.Mutex
    file *os.File
    seq uint64
    firstIndex uint64
    lastIndex uint64
    crc uint32
}
```

Required methods:

- `openWAL(cfg walConfig) (*wal, error)` creates the WAL directory and opens the tail segment for append.
- `appendReady(ctx, hardState, entries, snapshotMeta)` appends entries, hard state, and snapshot marker; fsyncs when `raft.MustSync` or a snapshot marker is present.
- `appendAppliedIndex(ctx, index)` appends one applied-index record and fsyncs.
- `replay() (replayState, error)` reads segments in name order and ignores only an incomplete tail record.
- `cut()` closes and fsyncs the current segment, opens the next segment, writes a header record.
- `close()` fsyncs and closes the tail.

Name segments as `%016x-%016x.wal`, where the first number is sequence and the second is first raft index in the segment.

- [ ] **Step 5: Run WAL tests**

Run: `go test ./pkg/controllerv2/raft/raftstore -run 'TestWAL' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit WAL segment implementation**

```bash
git add pkg/controllerv2/raft/raftstore/config.go pkg/controllerv2/raft/raftstore/wal.go pkg/controllerv2/raft/raftstore/wal_test.go
git commit -m "feat(controllerv2): add raft wal segments"
```

---

### Task 4: Implement Metadata, Snapshot Envelope, and Raft Storage API

**Files:**
- Create: `pkg/controllerv2/raft/raftstore/metadata.go`
- Create: `pkg/controllerv2/raft/raftstore/snapshot.go`
- Create: `pkg/controllerv2/raft/raftstore/memory.go`
- Create: `pkg/controllerv2/raft/raftstore/store.go`
- Create: `pkg/controllerv2/raft/raftstore/store_test.go`
- Create: `pkg/controllerv2/raft/raftstore/snapshot_test.go`

- [ ] **Step 1: Write failing store reopen and Storage API test**

Create `store_test.go`:

```go
func TestStoreSaveReadyReopenAndServeStorage(t *testing.T) {
    ctx := context.Background()
    dir := filepath.Join(t.TempDir(), "controller-raft")
    store, err := Open(ctx, Config{Dir: dir, NodeID: 1, SegmentSize: 1 << 20})
    require.NoError(t, err)

    hs := raftpb.HardState{Term: 2, Vote: 1, Commit: 3}
    entries := []raftpb.Entry{
        {Index: 1, Term: 1, Type: raftpb.EntryConfChange, Data: mustConfChangeData(t, 1)},
        {Index: 2, Term: 2, Type: raftpb.EntryNormal, Data: []byte("two")},
        {Index: 3, Term: 2, Type: raftpb.EntryNormal, Data: []byte("three")},
    }
    require.NoError(t, store.SaveReady(ctx, hs, entries, raftpb.Snapshot{}))
    require.NoError(t, store.MarkAppliedBatch(ctx, 2))
    require.NoError(t, store.Close())

    reopened, err := Open(ctx, Config{Dir: dir, NodeID: 1, SegmentSize: 1 << 20})
    require.NoError(t, err)
    defer reopened.Close()
    gotHS, conf, err := reopened.InitialState()
    require.NoError(t, err)
    require.Equal(t, hs, gotHS)
    require.Contains(t, conf.Voters, uint64(1))
    last, err := reopened.LastIndex()
    require.NoError(t, err)
    require.Equal(t, uint64(3), last)
    gotEntries, err := reopened.Entries(2, 4, 0)
    require.NoError(t, err)
    require.Equal(t, entries[1:], gotEntries)
    require.Equal(t, uint64(2), reopened.AppliedIndex())
}
```

- [ ] **Step 2: Write failing snapshot and compact test**

Create `snapshot_test.go`:

```go
func TestStoreSnapshotAndCompactBoundsStartupSuffix(t *testing.T) {
    ctx := context.Background()
    dir := filepath.Join(t.TempDir(), "controller-raft")
    store, err := Open(ctx, Config{Dir: dir, NodeID: 1, SegmentSize: 512})
    require.NoError(t, err)
    for i := uint64(1); i <= 10; i++ {
        hs := raftpb.HardState{Term: 1, Vote: 1, Commit: i}
        require.NoError(t, store.SaveReady(ctx, hs, []raftpb.Entry{{Index: i, Term: 1, Data: []byte{byte(i)}}}, raftpb.Snapshot{}))
    }
    snap := raftpb.Snapshot{Data: []byte(`{"revision":1}`), Metadata: raftpb.SnapshotMetadata{Index: 8, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}
    require.NoError(t, store.SaveSnapshot(ctx, snap))
    require.NoError(t, store.Compact(ctx, 6))
    require.NoError(t, store.Close())

    reopened, err := Open(ctx, Config{Dir: dir, NodeID: 1, SegmentSize: 512})
    require.NoError(t, err)
    defer reopened.Close()
    first, err := reopened.FirstIndex()
    require.NoError(t, err)
    require.Equal(t, uint64(9), first)
    loaded, err := reopened.Snapshot()
    require.NoError(t, err)
    require.Equal(t, uint64(8), loaded.Metadata.Index)
}
```

- [ ] **Step 3: Run store tests and verify failure**

Run: `go test ./pkg/controllerv2/raft/raftstore -run 'TestStore' -count=1`

Expected: FAIL because the high-level store does not exist.

- [ ] **Step 4: Implement metadata and snapshot envelope**

Implement `meta.json` with atomic temp-file write and parent directory sync. Use this persisted structure:

```go
type metadata struct {
    Version      int              `json:"version"`
    NodeID       uint64           `json:"node_id"`
    HardState    raftpb.HardState `json:"hard_state"`
    AppliedIndex uint64           `json:"applied_index"`
    Snapshot     snapshotMeta     `json:"snapshot"`
    ConfState    raftpb.ConfState `json:"conf_state"`
}

type snapshotMeta struct {
    Index uint64 `json:"index"`
    Term  uint64 `json:"term"`
    Path  string `json:"path"`
}
```

Implement snapshot envelope:

```go
type snapshotEnvelope struct {
    Version   int                       `json:"version"`
    Metadata  raftpb.SnapshotMetadata   `json:"metadata"`
    StateJSON json.RawMessage           `json:"state_json"`
    Checksum  string                    `json:"checksum"`
}
```

The snapshot payload for now is the canonical encoded `state.ClusterState` JSON stored inside the envelope. `SaveSnapshot` writes `snap/<index>-<term>.snap.tmp`, fsyncs it, renames, and syncs the directory.

- [ ] **Step 5: Implement bounded storage facade**

`Store` must implement `go.etcd.io/raft/v3.Storage`:

```go
type Store struct {
    mu sync.RWMutex
    cfg Config
    wal *wal
    meta metadata
    snapshot raftpb.Snapshot
    entries []raftpb.Entry
}
```

Required exported methods:

```go
func Open(ctx context.Context, cfg Config) (*Store, error)
func (s *Store) Close() error
func (s *Store) SaveReady(ctx context.Context, hs raftpb.HardState, entries []raftpb.Entry, snap raftpb.Snapshot) error
func (s *Store) MarkAppliedBatch(ctx context.Context, index uint64) error
func (s *Store) SaveSnapshot(ctx context.Context, snap raftpb.Snapshot) error
func (s *Store) Compact(ctx context.Context, compactTo uint64) error
func (s *Store) AppliedIndex() uint64
func (s *Store) InitialState() (raftpb.HardState, raftpb.ConfState, error)
func (s *Store) Entries(lo, hi, maxSize uint64) ([]raftpb.Entry, error)
func (s *Store) Term(index uint64) (uint64, error)
func (s *Store) FirstIndex() (uint64, error)
func (s *Store) LastIndex() (uint64, error)
func (s *Store) Snapshot() (raftpb.Snapshot, error)
```

For this task, keep entries in memory after replay. The later service task will use snapshot/compact to bound the suffix. `Entries` should return `raft.ErrCompacted` for indexes at or before snapshot index and `raft.ErrUnavailable` for missing tail entries.

- [ ] **Step 6: Run raftstore tests**

Run: `go test ./pkg/controllerv2/raft/raftstore -count=1`

Expected: PASS.

- [ ] **Step 7: Commit store facade**

```bash
git add pkg/controllerv2/raft/raftstore
git commit -m "feat(controllerv2): add raftstore storage facade"
```

---

### Task 5: Add Proposal Tracker

**Files:**
- Create: `pkg/controllerv2/raft/proposal_tracker.go`
- Create or modify: `pkg/controllerv2/raft/service_test.go`

- [ ] **Step 1: Write failing tracker unit tests**

Add tests near the service test helpers or create `proposal_tracker_test.go`:

```go
func TestProposalTrackerBindsEntriesAndCompletesByIndex(t *testing.T) {
    tracker := newProposalTracker()
    resp1 := make(chan error, 1)
    resp2 := make(chan error, 1)
    tracker.enqueue(trackedProposal{resp: resp1})
    tracker.enqueue(trackedProposal{resp: resp2})

    tracker.bindAppended([]raftpb.Entry{
        {Index: 5, Type: raftpb.EntryNormal, Data: []byte("a")},
        {Index: 6, Type: raftpb.EntryNormal, Data: []byte("b")},
    })
    tracker.complete(5, nil)
    tracker.complete(6, ProposalRejectedError{Index: 6, Reason: "bad"})

    require.NoError(t, <-resp1)
    require.ErrorIs(t, <-resp2, ErrProposalRejected)
}

func TestProposalTrackerFailsUncommittedOnLeaderLoss(t *testing.T) {
    tracker := newProposalTracker()
    resp := make(chan error, 1)
    tracker.enqueue(trackedProposal{resp: resp})
    tracker.failUnbound(ErrNotLeader)
    require.ErrorIs(t, <-resp, ErrNotLeader)
}
```

- [ ] **Step 2: Run tracker tests and verify failure**

Run: `go test ./pkg/controllerv2/raft -run 'TestProposalTracker' -count=1`

Expected: FAIL because tracker helpers do not exist.

- [ ] **Step 3: Implement tracker**

Create `proposal_tracker.go` with:

```go
type proposalTracker struct {
    queue []trackedProposal
    byIndex map[uint64]trackedProposal
}

func newProposalTracker() *proposalTracker
func (t *proposalTracker) enqueue(p trackedProposal)
func (t *proposalTracker) bindAppended(entries []raftpb.Entry)
func (t *proposalTracker) complete(index uint64, err error)
func (t *proposalTracker) failAll(err error)
func (t *proposalTracker) failUnbound(err error)
func (t *proposalTracker) failFrom(index uint64, err error)
```

`bindAppended` only consumes queue entries for non-empty `EntryNormal` records, matching the current proposal behavior.

- [ ] **Step 4: Run tracker tests**

Run: `go test ./pkg/controllerv2/raft -run 'TestProposalTracker' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit proposal tracker**

```bash
git add pkg/controllerv2/raft/proposal_tracker.go pkg/controllerv2/raft/*_test.go
git commit -m "feat(controllerv2): track raft proposal waiters"
```

---

### Task 6: Add FIFO Apply Scheduler

**Files:**
- Create: `pkg/controllerv2/raft/apply_scheduler.go`
- Create: `pkg/controllerv2/raft/apply_scheduler_test.go`

- [ ] **Step 1: Write failing scheduler batching tests**

Create `apply_scheduler_test.go` with fake dependencies:

```go
type fakeBatchApplier struct {
    mu sync.Mutex
    batches [][]fsm.AppliedCommand
}

func (f *fakeBatchApplier) ApplyBatch(ctx context.Context, cmds []fsm.AppliedCommand) (fsm.BatchApplyResult, error) {
    f.mu.Lock()
    f.batches = append(f.batches, append([]fsm.AppliedCommand(nil), cmds...))
    f.mu.Unlock()
    results := make([]fsm.ApplyResult, len(cmds))
    for i, cmd := range cmds {
        results[i] = fsm.ApplyResult{Changed: true, Revision: uint64(i + 1), AppliedRaftIndex: cmd.Index}
    }
    return fsm.BatchApplyResult{Results: results}, nil
}

type fakeAppliedStore struct {
    marks []uint64
}

func (s *fakeAppliedStore) MarkAppliedBatch(ctx context.Context, index uint64) error {
    s.marks = append(s.marks, index)
    return nil
}

func TestApplySchedulerBatchesContiguousNormalEntries(t *testing.T) {
    applier := &fakeBatchApplier{}
    store := &fakeAppliedStore{}
    completions := make(map[uint64]error)
    sched := newApplyScheduler(applySchedulerConfig{MaxEntries: 4, MaxBytes: 1 << 20, MaxDelay: time.Hour}, applier, store, func(index uint64, err error) { completions[index] = err })

    entries := []raftpb.Entry{
        {Index: 1, Term: 1, Type: raftpb.EntryNormal, Data: mustEncodeCommandForSchedulerTest(t, testInitCommand("sched", []Peer{{NodeID: 1, Addr: "n1"}}))},
        {Index: 2, Term: 1, Type: raftpb.EntryNormal, Data: mustEncodeCommandForSchedulerTest(t, testUpsertNodeCommand(1, 1, "n1"))},
    }
    require.NoError(t, sched.applyEntries(context.Background(), entries, nil))
    require.Len(t, applier.batches, 1)
    require.Equal(t, uint64(2), store.marks[0])
    require.Contains(t, completions, uint64(1))
    require.Contains(t, completions, uint64(2))
}
```

- [ ] **Step 2: Write failing semantic reject completion test**

Add a fake applier that returns `Rejected: true` for index 2 and assert only index 2 receives `ErrProposalRejected`.

- [ ] **Step 3: Run scheduler tests and verify failure**

Run: `go test ./pkg/controllerv2/raft -run 'TestApplyScheduler' -count=1`

Expected: FAIL because scheduler does not exist.

- [ ] **Step 4: Implement scheduler interfaces and synchronous test path**

In `apply_scheduler.go`, define:

```go
type batchApplier interface {
    ApplyBatch(context.Context, []fsm.AppliedCommand) (fsm.BatchApplyResult, error)
}

type appliedMarker interface {
    MarkAppliedBatch(context.Context, uint64) error
}

type applyCompletion func(index uint64, err error)
```

Implement an `applyEntries` method that decodes normal commands, batches contiguous normal entries, marks non-command entries applied, and calls completion. Keep a synchronous method for deterministic unit tests before wiring the goroutine.

- [ ] **Step 5: Add worker lifecycle**

Add:

```go
type toApply struct {
    entries []raftpb.Entry
    snapshot raftpb.Snapshot
    confChangeC chan confChangeRequest
}

func (s *applyScheduler) start(ctx context.Context)
func (s *applyScheduler) stop() error
func (s *applyScheduler) enqueue(ctx context.Context, job toApply) error
```

The worker must process jobs FIFO. It can coalesce queued jobs by draining the channel until limits are hit.

- [ ] **Step 6: Run scheduler tests**

Run: `go test ./pkg/controllerv2/raft -run 'TestApplyScheduler' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit scheduler**

```bash
git add pkg/controllerv2/raft/apply_scheduler.go pkg/controllerv2/raft/apply_scheduler_test.go
git commit -m "feat(controllerv2): add raft apply scheduler"
```

---

### Task 7: Refactor Service Config and Startup to Use Raftstore

**Files:**
- Modify: `pkg/controllerv2/raft/config.go`
- Modify: `pkg/controllerv2/raft/service.go`
- Create: `pkg/controllerv2/raft/loop.go`
- Modify: `pkg/controllerv2/raft/service_test.go`

- [ ] **Step 1: Update config validation tests first**

Modify `TestNewServiceValidatesConfig` and add:

```go
func TestNewServiceRequiresRaftDir(t *testing.T) {
    service, err := NewService(Config{
        NodeID: 1,
        Peers: []Peer{{NodeID: 1, Addr: "n1"}},
        AllowBootstrap: true,
        StateMachine: newTestStateMachine(t, filepath.Join(t.TempDir(), "cluster-state.json")),
        Transport: newMemoryRaftTransport(),
    })
    require.Nil(t, service)
    require.ErrorIs(t, err, ErrInvalidConfig)
    require.Contains(t, err.Error(), "raft dir")
}
```

- [ ] **Step 2: Run config tests and verify failure**

Run: `go test ./pkg/controllerv2/raft -run 'TestNewService' -count=1`

Expected: FAIL because `Config` still uses `Storage`.

- [ ] **Step 3: Modify `Config`**

In `config.go`, remove `Storage multiraft.Storage` and add English-commented fields:

```go
// RaftDir is the local directory used for ControllerV2 Raft WAL segments, snapshots, and metadata.
RaftDir string
// MaxApplyBatchEntries limits how many committed command entries one FSM batch may contain.
MaxApplyBatchEntries int
// MaxApplyBatchBytes limits the total encoded command bytes in one FSM apply batch.
MaxApplyBatchBytes uint64
// MaxApplyDelay bounds how long the apply scheduler may wait to coalesce more committed entries.
MaxApplyDelay time.Duration
// WALSegmentSize controls ControllerV2 WAL segment rollover size in bytes.
WALSegmentSize uint64
// SnapshotCount controls how many newly applied entries trigger a ControllerV2 snapshot.
SnapshotCount uint64
// SnapshotCatchUpEntries controls how many entries after the last snapshot remain available for follower catch-up.
SnapshotCatchUpEntries uint64
// SnapshotMinInterval prevents repeated snapshots more frequently than this interval.
SnapshotMinInterval time.Duration
```

Normalize defaults from the spec and validate positive values.

- [ ] **Step 4: Update service construction tests/helpers**

Replace helper signatures that accept `multiraft.Storage` with helpers that accept `raftDir string`. Example:

```go
func newSingleService(id uint64, peers []Peer, raftDir string, statePath string, allowBootstrap bool) (*Service, error) {
    sm, err := fsm.New(statefile.New(statePath))
    if err != nil {
        return nil, err
    }
    transport := newMemoryRaftTransport()
    service, err := NewService(Config{
        NodeID: id,
        Peers: peers,
        AllowBootstrap: allowBootstrap,
        RaftDir: raftDir,
        StateMachine: sm,
        Transport: transport,
        TickInterval: 5 * time.Millisecond,
    })
    if err != nil {
        return nil, err
    }
    transport.register(id, service)
    return service, nil
}
```

- [ ] **Step 5: Implement `Service.Start` raftstore open path**

In `service.go`, replace `newStorageAdapter(cfg.Storage)` with:

```go
store, err := raftstore.Open(ctx, raftstore.Config{
    Dir: cfg.RaftDir,
    NodeID: cfg.NodeID,
    SegmentSize: cfg.WALSegmentSize,
})
```

Load `StateMachine`, validate state/applied boundary against `store.InitialState()` and `store.AppliedIndex()`, create `applyScheduler`, then start `raftLoop`.

- [ ] **Step 6: Create `loop.go` with a minimal RawNode loop**

Move the existing `run`, `newRawNode`, `sendReadyMessages`, and status update logic into `loop.go`, but keep synchronous apply temporarily removed. The loop should call `store.SaveReady`, `tracker.bindAppended`, `scheduler.enqueue`, and `rawNode.Advance`.

- [ ] **Step 7: Run focused service compile tests**

Run: `go test ./pkg/controllerv2/raft -run 'TestNewService|TestConcurrentStart|TestStartDuringActiveStop' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit config/startup refactor**

```bash
git add pkg/controllerv2/raft/config.go pkg/controllerv2/raft/service.go pkg/controllerv2/raft/loop.go pkg/controllerv2/raft/service_test.go
git commit -m "refactor(controllerv2): open dedicated raftstore"
```

---

### Task 8: Wire Ready Loop, Scheduler, and Proposal Completion End-to-End

**Files:**
- Modify: `pkg/controllerv2/raft/loop.go`
- Modify: `pkg/controllerv2/raft/service.go`
- Modify: `pkg/controllerv2/raft/apply_scheduler.go`
- Modify: `pkg/controllerv2/raft/service_test.go`

- [ ] **Step 1: Update existing cluster tests to the new storage**

Update `newRaftTestCluster` and node setup in `service_test.go` so each test node has:

```go
raftDir := filepath.Join(dir, "controller-raft")
statePath := filepath.Join(dir, "cluster-state.json")
```

Remove uses of `raftlog.Open` and `db.ForController()` from ControllerV2 raft tests.

- [ ] **Step 2: Write failing async apply responsiveness test**

Add a state machine wrapper that blocks `ApplyBatch`, then prove `Step` can still enqueue while apply is blocked:

```go
func TestSlowApplyDoesNotBlockStep(t *testing.T) {
    cluster := newRaftTestCluster(t, []uint64{1})
    blocker := newBlockingBatchStateMachine(cluster.nodes[0].stateMachine)
    cluster.nodes[0].stateMachine = blocker
    cluster.rebuildService(t, cluster.nodes[0])
    cluster.start(t)
    leader := cluster.waitForLeader(t)

    resultCh := make(chan error, 1)
    go func() { resultCh <- leader.service.Propose(context.Background(), testInitCommand("wk-slow-apply", cluster.peers)) }()
    <-blocker.entered

    stepCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
    defer cancel()
    require.NoError(t, leader.service.Step(stepCtx, raftpb.Message{To: leader.id, Type: raftpb.MsgHeartbeat}))

    blocker.release()
    require.NoError(t, <-resultCh)
}
```

- [ ] **Step 3: Run the async test and verify failure or hang on old loop**

Run: `go test ./pkg/controllerv2/raft -run TestSlowApplyDoesNotBlockStep -count=1 -timeout=5s`

Expected before wiring: FAIL or timeout because the old loop applies synchronously.

- [ ] **Step 4: Complete scheduler goroutine wiring**

In `Service.Start`:

- Create scheduler with config defaults.
- Start scheduler before starting raft loop.
- Pass scheduler enqueue function and proposal completion callback to `raftLoop`.
- Ensure `Stop` stops scheduler before closing raftstore.

In `loop.go`, add the conf-change barrier type owned by the raft package:

```go
type confChangeRequest struct {
    entry raftpb.Entry
    resp  chan confChangeResult
}

type confChangeResult struct {
    state raftpb.ConfState
    err   error
}
```

The Ready path should:

- Build a `toApply` job. If `ready.CommittedEntries` contains a conf-change entry, attach a `confChangeC` channel to the job.
- Enqueue `toApply` before WAL save.
- Leader sends messages before `SaveReady`.
- `SaveReady` persists hard state, entries, and snapshots.
- `proposalTracker.bindAppended(ready.Entries)` after WAL save succeeds.
- Follower sends messages after WAL save.
- If `confChangeC` is set, receive each `confChangeRequest`, call `rawNode.ApplyConfChange` on the raft loop goroutine, and return the resulting `ConfState` through `resp`.
- `rawNode.Advance(ready)` after storage and conf-change coordination finish.
- On loop error, call `tracker.failAll(err)` and scheduler stop/fail.

In `apply_scheduler.go`, when applying a conf-change entry, decode `raftpb.ConfChange` or `raftpb.ConfChangeV2`, send it to `confChangeC`, wait for `ConfState`, then mark the entry applied.

- [ ] **Step 5: Preserve proposal semantics**

Ensure `Propose` still waits on the tracked response. The response should be sent by scheduler completion after FSM apply and `MarkAppliedBatch` succeed.

- [ ] **Step 6: Run core service tests**

Run: `go test ./pkg/controllerv2/raft -run 'TestThreeControllerVoters|TestSemanticReject|TestLeaderSendsBeforePersist|TestSlowApplyDoesNotBlockStep' -count=1 -timeout=20s`

Expected: PASS.

- [ ] **Step 7: Commit end-to-end wiring**

```bash
git add pkg/controllerv2/raft/service.go pkg/controllerv2/raft/loop.go pkg/controllerv2/raft/apply_scheduler.go pkg/controllerv2/raft/service_test.go
git commit -m "feat(controllerv2): decouple raft ready from apply"
```

---

### Task 9: Implement Startup Replay, Snapshot, and Compaction

**Files:**
- Modify: `pkg/controllerv2/raft/service.go`
- Modify: `pkg/controllerv2/raft/apply_scheduler.go`
- Modify: `pkg/controllerv2/raft/raftstore/store.go`
- Modify: `pkg/controllerv2/raft/raftstore/wal.go`
- Modify: `pkg/controllerv2/raft/service_test.go`
- Modify: `pkg/controllerv2/raft/raftstore/store_test.go`

- [ ] **Step 1: Write failing startup replay test using new raftstore**

Replace old `seedControllerLog` with a new helper:

```go
func seedControllerRaftStore(t *testing.T, dir string, entries []raftpb.Entry, commit uint64, applied uint64) {
    t.Helper()
    store, err := raftstore.Open(context.Background(), raftstore.Config{Dir: dir, NodeID: 1, SegmentSize: 1 << 20})
    require.NoError(t, err)
    hs := raftpb.HardState{Term: 1, Vote: 1, Commit: commit}
    require.NoError(t, store.SaveReady(context.Background(), hs, entries, raftpb.Snapshot{}))
    if applied > 0 {
        require.NoError(t, store.MarkAppliedBatch(context.Background(), applied))
    }
    require.NoError(t, store.Close())
}
```

Update `TestStartupReplaysWhenStateBehindRaftlog` into `TestStartupReplaysWhenStateBehindWAL`.

- [ ] **Step 2: Write failing snapshot suffix startup test**

Add:

```go
func TestStartupFromSnapshotReplaysOnlySuffix(t *testing.T) {
    ctx := context.Background()
    dir := t.TempDir()
    raftDir := filepath.Join(dir, "controller-raft")
    statePath := filepath.Join(dir, "cluster-state.json")
    peers := []Peer{{NodeID: 1, Addr: "n1"}}

    store, err := raftstore.Open(ctx, raftstore.Config{Dir: raftDir, NodeID: 1, SegmentSize: 512})
    require.NoError(t, err)
    initCmd := testInitCommand("wk-snapshot-suffix", peers)
    upsertCmd := testUpsertNodeCommand(1, 1, "after-snapshot")
    require.NoError(t, store.SaveReady(ctx, raftpb.HardState{Term: 1, Vote: 1, Commit: 2}, []raftpb.Entry{testCommandEntry(t, 1, initCmd), testCommandEntry(t, 2, upsertCmd)}, raftpb.Snapshot{}))

    sm := newTestStateMachine(t, statePath)
    _, err = sm.Apply(ctx, 1, initCmd)
    require.NoError(t, err)
    snapData, err := state.Encode(sm.Snapshot(ctx))
    require.NoError(t, err)
    require.NoError(t, store.SaveSnapshot(ctx, raftpb.Snapshot{Data: snapData, Metadata: raftpb.SnapshotMetadata{Index: 1, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}))
    require.NoError(t, store.MarkAppliedBatch(ctx, 1))
    require.NoError(t, store.Close())

    service := startSingleService(t, 1, peers, raftDir, statePath, false)
    t.Cleanup(func() { require.NoError(t, service.Stop()) })
    snap := service.cfg.StateMachine.Snapshot(ctx)
    require.Equal(t, uint64(2), snap.Revision)
    require.Equal(t, "after-snapshot", findTestNode(t, snap, 1).Name)
}
```

- [ ] **Step 3: Run startup tests and verify failure**

Run: `go test ./pkg/controllerv2/raft -run 'TestStartupReplays|TestStartupFromSnapshot' -count=1 -timeout=20s`

Expected: FAIL until replay and snapshot restore are implemented.

- [ ] **Step 4: Implement startup recovery**

In `Service.Start`:

1. Open `raftstore`.
2. Load `StateMachine`.
3. Validate `statefile.AppliedRaftIndex <= hardState.Commit` and `<= store.LastIndex()`.
4. Determine `replayFrom = max(statefile.AppliedRaftIndex, store.AppliedIndex(), snapshot.Metadata.Index) + 1`.
5. Replay entries from `replayFrom` through `hardState.Commit` via scheduler synchronous replay mode before accepting proposals.
6. Mark applied to `hardState.Commit` when replay succeeds.

Remove old complete-history recovery that assumes `FirstIndex <= 1`; snapshots make complete prefix history unnecessary.

- [ ] **Step 5: Implement snapshot trigger and compaction**

After scheduler marks applied, call a service callback:

```go
func (s *Service) maybeSnapshot(ctx context.Context, applied uint64, confState raftpb.ConfState) error
```

It should:

- Respect `SnapshotCount` and `SnapshotMinInterval`.
- Encode `StateMachine.Snapshot(ctx)`.
- Save snapshot with `raftstore.SaveSnapshot`.
- Compact to `applied - SnapshotCatchUpEntries` when safe.

- [ ] **Step 6: Run startup and raftstore tests**

Run: `go test ./pkg/controllerv2/raft ./pkg/controllerv2/raft/raftstore -run 'TestStartup|TestStoreSnapshot|TestWAL' -count=1 -timeout=30s`

Expected: PASS.

- [ ] **Step 7: Commit recovery and compaction**

```bash
git add pkg/controllerv2/raft pkg/controllerv2/raft/raftstore
git commit -m "feat(controllerv2): recover from raft snapshots and wal suffix"
```

---

### Task 10: Clean Up Obsolete ControllerV2 Storage Paths and Docs

**Files:**
- Modify: `pkg/controllerv2/FLOW.md`
- Modify: `pkg/controllerv2/raft/doc.go`
- Modify: `pkg/controllerv2/raft/service_test.go`
- Optional modify: `wukongim.conf.example`

- [ ] **Step 1: Remove stale imports and helpers**

Run: `rg 'pkg/raftlog|ForController|multiraft.Storage|MarkApplied\(' pkg/controllerv2/raft pkg/controllerv2 -n`

Expected: remaining matches are either in `raftstore` tests for deliberate naming or no matches in `pkg/controllerv2/raft` service code.

Delete ControllerV2 raft test helpers that seed `multiraft.Storage`.

- [ ] **Step 2: Update FLOW documentation**

Change `pkg/controllerv2/FLOW.md` apply order from:

```text
Raft commit -> semantic apply -> save cluster-state.json -> publish in-memory state -> MarkApplied
```

to:

```text
Raft Ready -> WAL SaveReady -> FIFO apply scheduler -> batched FSM save -> ApplyMeta -> snapshot/compact
```

Add one sentence: `cluster-state.json` is the materialized ControllerV2 state snapshot; ControllerV2 Raft WAL is the authoritative committed log.

- [ ] **Step 3: Update raft package doc**

In `pkg/controllerv2/raft/doc.go`, describe the dedicated WAL and async apply scheduler. Include that single-node operation is still a single-node cluster.

- [ ] **Step 4: Run doc-adjacent package tests**

Run: `go test ./pkg/controllerv2/... -count=1 -timeout=60s`

Expected: PASS.

- [ ] **Step 5: Commit cleanup**

```bash
git add pkg/controllerv2/FLOW.md pkg/controllerv2/raft/doc.go pkg/controllerv2/raft/service_test.go wukongim.conf.example
git commit -m "docs(controllerv2): document raft wal apply flow"
```

If `wukongim.conf.example` was not changed, omit it from `git add`.

---

### Task 11: Add Benchmarks and Performance Guards

**Files:**
- Create: `pkg/controllerv2/raft/benchmark_test.go`
- Create or modify: `docs/superpowers/reports/2026-05-25-controllerv2-raft-performance.md`

- [ ] **Step 1: Add proposal and startup benchmarks**

Create `benchmark_test.go` with:

```go
func BenchmarkControllerRaftProposeSingleNode(b *testing.B) {
    cluster := newRaftBenchmarkCluster(b, []uint64{1})
    cluster.start(b)
    cluster.propose(b, testInitCommand("bench-single", cluster.peers))
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        cluster.propose(b, testUpsertNodeCommand(uint64(i+1), 1, fmt.Sprintf("node-%d", i)))
    }
}

func BenchmarkControllerRaftStartupWithLongHistory(b *testing.B) {
    for i := 0; i < b.N; i++ {
        dir := b.TempDir()
        seedBenchmarkSnapshotAndSuffix(b, filepath.Join(dir, "controller-raft"), 100_000, 1_000)
        start := time.Now()
        service := startSingleServiceForBenchmark(b, 1, []Peer{{NodeID: 1, Addr: "n1"}}, filepath.Join(dir, "controller-raft"), filepath.Join(dir, "cluster-state.json"), false)
        _ = time.Since(start)
        require.NoError(b, service.Stop())
    }
}
```

- [ ] **Step 2: Run benchmarks once**

Run: `go test ./pkg/controllerv2/raft -run '^$' -bench 'BenchmarkControllerRaft' -benchmem -benchtime=1s -count=1`

Expected: PASS and benchmark output with `ns/op`, `B/op`, and `allocs/op`.

- [ ] **Step 3: Save benchmark notes**

Create `docs/superpowers/reports/2026-05-25-controllerv2-raft-performance.md` with:

```markdown
# ControllerV2 Raft Performance Refactor Results

## Command

`go test ./pkg/controllerv2/raft -run '^$' -bench 'BenchmarkControllerRaft' -benchmem -benchtime=1s -count=1`

## Results

Record the exact benchmark output from Step 2 in this section before committing the report.

## Notes

- Startup benchmark uses a snapshot plus bounded WAL suffix.
- Proposal benchmark waits for committed local apply semantics.
```

- [ ] **Step 4: Commit benchmarks**

```bash
git add pkg/controllerv2/raft/benchmark_test.go docs/superpowers/reports/2026-05-25-controllerv2-raft-performance.md
git commit -m "test(controllerv2): benchmark raft wal apply path"
```

---

### Task 12: Final Verification

**Files:**
- No new files expected.

- [ ] **Step 1: Run focused package tests**

Run: `go test ./pkg/controllerv2/... -count=1 -timeout=90s`

Expected: PASS.

- [ ] **Step 2: Run raftstore race-sensitive focused tests**

Run: `go test -race ./pkg/controllerv2/raft ./pkg/controllerv2/raft/raftstore -count=1 -timeout=120s`

Expected: PASS.

- [ ] **Step 3: Run related broader tests**

Run: `go test ./pkg/controllerv2/... ./pkg/raftlog/... -count=1 -timeout=120s`

Expected: PASS. `pkg/raftlog` should still pass because the old storage remains used by other packages.

- [ ] **Step 4: Inspect git diff for unrelated changes**

Run: `git status --short && git diff --stat`

Expected: only files intentionally touched by this plan are modified or committed. Do not stage unrelated `pkg/clusterv2` or `AGENTS.md` changes.

- [ ] **Step 5: Commit any final fixes**

If final verification required fixes:

```bash
git add pkg/controllerv2/fsm pkg/controllerv2/raft pkg/controllerv2/FLOW.md docs/superpowers/reports/2026-05-25-controllerv2-raft-performance.md
git commit -m "fix(controllerv2): stabilize raft wal apply path"
```

- [ ] **Step 6: Produce completion summary**

Include:

- Final commit list.
- Test commands and results.
- Any remaining risks, especially around WAL corruption recovery and conf-change barriers.
