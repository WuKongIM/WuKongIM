# wkdb Raft Decoupling Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove all Raft storage and state-machine responsibilities from `wkdb`, introduce a standalone in-memory `raftstore` package, and replace `wkdbraft` with a focused `wkfsm` package.

**Architecture:** Build the new packages first so the repository always has a working `multiraft.Storage` and `multiraft.StateMachine` implementation during the migration. `raftstore` will directly implement `multiraft.Storage` with an in-memory backend only, while `wkfsm` will directly implement `multiraft.StateMachine` on top of `wkdb` slot-scoped business storage and reuse `wkdb` snapshot import/export without owning any Raft persistence.

**Tech Stack:** Go 1.23, `go.etcd.io/raft/v3/raftpb`, `github.com/WuKongIM/WuKongIM/pkg/consensus/multiraft`, `github.com/WuKongIM/WuKongIM/pkg/controller/wkdb`, Go testing

---

## File Structure

### Production files

- Create: `raftstore/memory.go`
  Responsibility: provide the first `multiraft.Storage` implementation as a concurrency-safe in-memory store with correct `Save`, `Entries`, `Term`, `Snapshot`, and `MarkApplied` semantics.
- Create: `wkfsm/command.go`
  Responsibility: define the JSON command envelope and move `EncodeUpsertUserCommand` / `EncodeUpsertChannelCommand` out of `wkdb`.
- Create: `wkfsm/state_machine.go`
  Responsibility: implement `multiraft.StateMachine` using `wkdb.DB`, slot-scoped CRUD, and `wkdb` business snapshot import/export.
- Modify: `AGENTS.md`
  Responsibility: update package descriptions to the new `raftstore` / `wkfsm` boundary and remove the obsolete `wkdbraft` entry.

### Test files

- Create: `raftstore/memory_test.go`
  Responsibility: verify empty-state behavior, `Save` windowing, snapshot trimming, applied-index persistence, and slice-cloning safety.
- Create: `wkfsm/testutil_test.go`
  Responsibility: share `openTestDB`, runtime helpers, and JSON helpers for `wkfsm` package tests.
- Create: `wkfsm/state_machine_test.go`
  Responsibility: verify command encoding, user/channel apply behavior, slot-scoped snapshot/restore, and `GroupID` validation.
- Create: `wkfsm/integration_test.go`
  Responsibility: verify `multiraft.Runtime` works with `raftstore.NewMemory()` + `wkfsm.New()` and explicitly capture the temporary “no restart recovery” behavior.

### Files to delete during migration

- Delete: `wkdb/raft_storage.go`
- Delete: `wkdb/raft_state_machine.go`
- Delete: `wkdb/raft_types.go`
- Delete: `wkdbraft/adapter.go`
- Delete: `wkdbraft/storage_test.go`
- Delete: `wkdbraft/state_machine_test.go`
- Delete: `wkdbraft/integration_test.go`

### Existing spec and support docs

- Reference: `docs/superpowers/specs/2026-03-27-wkdb-raft-decoupling-design.md`
  Responsibility: approved package boundaries, API surface, migration rules, and explicit short-term loss of persistent recovery.

## Implementation Notes

- Follow `@superpowers:test-driven-development` for every code-bearing task: add the test first, run it red, implement the minimum code, run it green.
- Keep `multiraft.Storage` and `multiraft.StateMachine` as the only public contracts. Do not introduce a second storage abstraction in `raftstore`.
- `raftstore.NewMemory()` must deep-copy `raftpb.Entry`, `raftpb.Snapshot.Data`, and `raftpb.ConfState` slices on both write and read paths.
- `raftstore.Save()` must preserve all entries with `index < firstNewIndex`, replace entries at and after `firstNewIndex`, and trim entries covered by a new snapshot.
- `wkfsm` should keep the existing JSON command schema unchanged for now.
- `wkfsm.Apply()` must reject `cmd.GroupID != slot` immediately instead of silently writing the wrong slot.
- Keep `wkdb` free of `multiraft` and `raftpb` imports after the migration.
- Update `AGENTS.md`, but do not rewrite historical design/plan documents under `docs/superpowers/specs/` and `docs/superpowers/plans/` that describe the old approach. They are historical records, not live package docs.
- Use frequent commits with repository-approved commit messages.

## Task 1: Add the standalone in-memory `raftstore` package

**Files:**
- Create: `raftstore/memory.go`
- Create: `raftstore/memory_test.go`

- [ ] **Step 1: Write the failing `raftstore` tests**

```go
func TestMemoryInitialStateIsEmpty(t *testing.T) {
    store := raftstore.NewMemory()

    state, err := store.InitialState(context.Background())
    if err != nil {
        t.Fatalf("InitialState() error = %v", err)
    }
    if state.AppliedIndex != 0 {
        t.Fatalf("AppliedIndex = %d, want 0", state.AppliedIndex)
    }
}

func TestMemorySaveAndReadRoundTrip(t *testing.T) {
    store := raftstore.NewMemory()
    hs := raftpb.HardState{Term: 2, Vote: 1, Commit: 7}
    entries := []raftpb.Entry{
        {Index: 5, Term: 1, Data: []byte("a")},
        {Index: 6, Term: 2, Data: []byte("b")},
        {Index: 7, Term: 2, Data: []byte("c")},
    }

    if err := store.Save(context.Background(), multiraft.PersistentState{
        HardState: &hs,
        Entries:   entries,
    }); err != nil {
        t.Fatalf("Save() error = %v", err)
    }

    got, err := store.Entries(context.Background(), 6, 8, 0)
    if err != nil {
        t.Fatalf("Entries() error = %v", err)
    }
    if !reflect.DeepEqual(got, entries[1:]) {
        t.Fatalf("Entries() = %#v, want %#v", got, entries[1:])
    }
}

```

The test file should also cover:
- `Term(index)` returning the entry term or snapshot term for the exact snapshot index
- `FirstIndex()` returning `1` for a truly empty store and `snapshot.Metadata.Index + 1` when only a snapshot remains
- `LastIndex()` returning the last entry index or snapshot index if there are no entries
- `Save(snapshot)` dropping entries up to `snapshot.Metadata.Index`
- `MarkApplied(index)` updating `InitialState().AppliedIndex`
- mutating slices returned by `Entries()` or `Snapshot()` not corrupting the stored state

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./raftstore -run 'TestMemory' -count=1`

Expected: FAIL because the `raftstore` package and `NewMemory()` do not exist yet.

- [ ] **Step 3: Implement the in-memory store**

```go
type memoryStore struct {
    mu       sync.Mutex
    state    multiraft.BootstrapState
    entries  []raftpb.Entry
    snapshot raftpb.Snapshot
}

func NewMemory() multiraft.Storage {
    return &memoryStore{}
}
```

Implementation details:
- implement every `multiraft.Storage` method directly on `memoryStore`
- clone all persisted inputs before storing them
- in `Save()`, trim `entries` using `first := st.Entries[0].Index`
- when saving a snapshot, delete all persisted entries with `Index <= snapshot.Metadata.Index`
- keep the implementation self-contained inside `raftstore`; do not reach into `multiraft` tests or fake helpers

- [ ] **Step 4: Re-run the `raftstore` tests and confirm they pass**

Run: `go test ./raftstore -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add raftstore/memory.go raftstore/memory_test.go
git commit -m "feat: add in-memory raftstore"
```

## Task 2: Create the `wkfsm` package and move state-machine behavior out of `wkdb`

**Files:**
- Create: `wkfsm/command.go`
- Create: `wkfsm/state_machine.go`
- Create: `wkfsm/testutil_test.go`
- Create: `wkfsm/state_machine_test.go`

- [ ] **Step 1: Write the failing `wkfsm` unit tests**

```go
func TestStateMachineApplyUpsertsUserAndChannel(t *testing.T) {
    ctx := context.Background()
    db := openTestDB(t)
    sm := wkfsm.New(db, 11)

    if _, err := sm.Apply(ctx, multiraft.Command{
        GroupID: 11,
        Index:   1,
        Term:    1,
        Data:    wkfsm.EncodeUpsertUserCommand(wkdb.User{UID: "u1", Token: "t1"}),
    }); err != nil {
        t.Fatalf("Apply(user) error = %v", err)
    }

    got, err := db.ForSlot(11).GetUser(ctx, "u1")
    if err != nil {
        t.Fatalf("GetUser() error = %v", err)
    }
    if got.Token != "t1" {
        t.Fatalf("Token = %q, want t1", got.Token)
    }
}

```

The tests should use `multiraft.Command` and `multiraft.Snapshot` directly. Do not recreate the deleted `wkdb.RaftCommand` / `wkdb.RaftSnapshot` wrapper types.

The test file should also cover:
- snapshot -> restore round-trip into a fresh `wkdb.DB`
- snapshot export staying slot-scoped when another slot has the same business key
- `Apply()` returning an error when `cmd.GroupID` does not match the constructor slot
- malformed JSON and unknown `type` values returning a stable error

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./wkfsm -run 'TestStateMachine' -count=1`

Expected: FAIL because the `wkfsm` package does not exist yet.

- [ ] **Step 3: Implement `wkfsm` command encoding and state machine**

```go
type commandEnvelope struct {
    Type    string        `json:"type"`
    User    *wkdb.User    `json:"user,omitempty"`
    Channel *wkdb.Channel `json:"channel,omitempty"`
}

func New(db *wkdb.DB, slot uint64) multiraft.StateMachine {
    return &stateMachine{db: db, slot: slot}
}
```

Implementation details:
- keep the existing JSON payload shape from the old `wkdb` state-machine code
- use `wkdb.ErrInvalidArgument` and `wkdb.ErrCorruptValue` for invalid slot / malformed payload cases to preserve current sentinel style
- implement upsert semantics by checking `GetUser` / `GetChannel` first, then `Create*` or `Update*`
- `Snapshot()` should wrap `wkdb.ExportSlotSnapshot(ctx, slot)` into `multiraft.Snapshot{Data: snap.Data}`
- `Restore()` should call `wkdb.ImportSlotSnapshot()` with the stored slot
- reject `cmd.GroupID != multiraft.GroupID(slot)` before decoding the payload

- [ ] **Step 4: Re-run the `wkfsm` unit tests and confirm they pass**

Run: `go test ./wkfsm -run 'TestStateMachine' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wkfsm/command.go wkfsm/state_machine.go wkfsm/testutil_test.go wkfsm/state_machine_test.go
git commit -m "feat: add wkdb-backed state machine package"
```

## Task 3: Migrate the runtime integration path to `raftstore` + `wkfsm`

**Files:**
- Create: `wkfsm/integration_test.go`
- Modify: `wkfsm/testutil_test.go`
- Modify: `AGENTS.md`

- [ ] **Step 1: Write the failing integration tests for the new package boundary**

```go
func TestMemoryBackedGroupAppliesProposalToWKDB(t *testing.T) {
    ctx := context.Background()
    db := openTestDB(t)
    rt := newStartedRuntime(t)

    err := rt.BootstrapGroup(ctx, multiraft.BootstrapGroupRequest{
        Group: multiraft.GroupOptions{
            ID:           51,
            Storage:      raftstore.NewMemory(),
            StateMachine: wkfsm.New(db, 51),
        },
        Voters: []multiraft.NodeID{1},
    })
    if err != nil {
        t.Fatalf("BootstrapGroup() error = %v", err)
    }

    // propose upsert_user and assert wkdb slot 51 contains the row
}

func TestMemoryBackedGroupDoesNotRecoverDeletedSlotDataAfterReopen(t *testing.T) {
    // prove the temporary limitation explicitly:
    // close runtime, delete business slot data, reopen with a fresh in-memory store,
    // and assert the user is still missing.
}
```

The second test is important. It should document the temporary capability loss from the spec instead of letting it remain an implicit behavior change.

- [ ] **Step 2: Run the targeted integration tests and confirm they fail**

Run: `go test ./wkfsm -run 'TestMemoryBackedGroup' -count=1`

Expected: FAIL because the new integration tests do not exist yet and the helpers are not wired for `raftstore` / `wkfsm`.

- [ ] **Step 3: Port the old runtime helpers and update live package docs**

```go
func newStartedRuntime(t *testing.T) *multiraft.Runtime { /* port from old wkdbraft test */ }
func mustJSON(t *testing.T, value any) []byte { /* helper reused by integration tests */ }
```

Implementation details:
- move the useful runtime helper code from `wkdbraft/integration_test.go` into `wkfsm/testutil_test.go`
- update `AGENTS.md` package list to:
  - `multiraft`
  - `wkdb`
  - `raftstore`
  - `wkfsm`
- describe `raftstore` as in-memory-only for now
- do not edit older historical docs that mention `wkdbraft`

- [ ] **Step 4: Re-run the `wkfsm` integration tests and confirm they pass**

Run: `go test ./wkfsm -run 'TestMemoryBackedGroup' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wkfsm/integration_test.go wkfsm/testutil_test.go AGENTS.md
git commit -m "refactor: migrate wkdb raft runtime integration"
```

## Task 4: Remove the obsolete `wkdb` Raft surface and delete `wkdbraft`

**Files:**
- Delete: `wkdb/raft_storage.go`
- Delete: `wkdb/raft_state_machine.go`
- Delete: `wkdb/raft_types.go`
- Delete: `wkdbraft/adapter.go`
- Delete: `wkdbraft/storage_test.go`
- Delete: `wkdbraft/state_machine_test.go`
- Delete: `wkdbraft/integration_test.go`

- [ ] **Step 1: Run a reference scan to capture the old surface before deleting it**

Run: `rg -n 'NewRaftStorage\\(|wkdb\\.NewStateMachine\\(|EncodeUpsert(User|Channel)Command\\(|RaftPersistentState|package wkdbraft' --glob '*.go' wkdb wkdbraft`

Expected: MATCHES in the soon-to-be-deleted files.

- [ ] **Step 2: Delete the old Raft files and remove any remaining Go references**

Delete the files listed above and make sure no remaining Go file imports or references `wkdbraft`, `wkdb.NewStateMachine`, `wkdb.NewRaftStorage`, `wkdb.RaftPersistentState`, or the deleted wrapper types.

- [ ] **Step 3: Re-run the reference scan and confirm the old Go surface is gone**

Run: `rg -n 'NewRaftStorage\\(|wkdb\\.NewStateMachine\\(|EncodeUpsert(User|Channel)Command\\(|RaftPersistentState|package wkdbraft' --glob '*.go' .`

Expected: no matches

- [ ] **Step 4: Run the full repository test suite**

Run: `go test ./...`
Expected: PASS

Run: `go test -race ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: decouple wkdb from raft"
```

## Task 5: Final verification and cleanup

**Files:**
- Verify only; no new files expected

- [ ] **Step 1: Confirm the final package layout matches the spec**

Run: `find wkdb raftstore wkfsm -maxdepth 2 -type f | sort`

Expected:
- `wkdb` contains only business storage files
- `raftstore` contains the in-memory storage implementation and tests
- `wkfsm` contains the state-machine implementation and tests

- [ ] **Step 2: Confirm `wkdb` no longer depends on Raft packages**

Run: `rg -n 'multiraft|raftpb' wkdb`

Expected: no matches

- [ ] **Step 3: Confirm only live docs changed**

Run: `git diff --name-only --cached HEAD~1..HEAD`

Expected:
- package files under `wkdb`, `raftstore`, `wkfsm`
- `AGENTS.md`
- no historical `docs/superpowers/specs/2026-03-26-*` or `docs/superpowers/plans/2026-03-26-*` rewrites

- [ ] **Step 4: Prepare execution handoff notes**

Record:
- `raftstore` is memory-only
- restart recovery is intentionally absent for now
- the next follow-up feature is a persistent `raftstore` backend, not more changes in `wkdb`
