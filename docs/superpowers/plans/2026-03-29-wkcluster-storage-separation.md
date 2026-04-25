# wkcluster Storage Separation Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor `pkg/wkcluster/` into a pure cluster coordination library by extracting business storage logic into a new `pkg/wkstore/` package, and deleting `pkg/wkfsm/`.

**Architecture:** `wkcluster` accepts injected `NewStorage`/`NewStateMachine` factory functions via Config, exposes generic `Propose(ctx, groupID, cmd)`. New `wkstore` package absorbs wkfsm code and provides business APIs (Channel/User CRUD) on top of `wkcluster.Propose`. `wkfsm` package is deleted.

**Tech Stack:** Go, multiraft, wktransport, wkdb, raftstore, Pebble

**Spec:** `docs/superpowers/specs/2026-03-29-wkcluster-storage-separation-design.md`

---

## File Structure

### Files to Create
| File | Responsibility |
|------|---------------|
| `pkg/wkstore/store.go` | Store struct, New(), Channel CRUD, User CRUD |
| `pkg/wkstore/command.go` | TLV command encoding/decoding (moved from wkfsm/command.go) |
| `pkg/wkstore/statemachine.go` | BatchStateMachine impl + factory (moved from wkfsm/state_machine.go) |
| `pkg/wkstore/testutil_test.go` | Test helpers (moved from wkfsm/testutil_test.go) |
| `pkg/wkstore/statemachine_test.go` | SM unit tests (moved from wkfsm/state_machine_test.go) |
| `pkg/wkstore/integration_test.go` | Integration tests (moved from wkfsm/integration_test.go) |
| `pkg/wkstore/benchmark_test.go` | Benchmarks (moved from wkfsm/benchmark_test.go) |
| `pkg/wkstore/stress_test.go` | Stress tests (moved from wkfsm/stress_test.go) |

### Files to Modify
| File | Change |
|------|--------|
| `pkg/wkcluster/config.go` | Remove DataDir/RaftDataDir, add NewStorage/NewStateMachine factories, update validate/applyDefaults |
| `pkg/wkcluster/cluster.go` | Remove db/raftDB fields, remove wkdb/raftstore/wkfsm imports, update Start/Stop/openOrBootstrapGroup |
| `pkg/wkcluster/router.go` | Rename SlotForChannel→SlotForKey |
| `pkg/wkcluster/forward.go` | Move proposeOrForward into cluster.go as Propose (exported), remove wkdb/wkfsm imports |
| `pkg/wkcluster/config_test.go` | Update validTestConfig to use factory fields instead of DataDir |
| `pkg/wkcluster/router_test.go` | SlotForChannel→SlotForKey |
| `pkg/wkcluster/cluster_test.go` | Use wkstore.Store for business API calls, inject factories |
| `pkg/wkcluster/stress_test.go` | Use wkstore.Store for business API calls, inject factories |

### Files to Delete
| File | Reason |
|------|--------|
| `pkg/wkcluster/api.go` | Business API moved to wkstore |
| `pkg/wkfsm/command.go` | Moved to wkstore/command.go |
| `pkg/wkfsm/state_machine.go` | Moved to wkstore/statemachine.go |
| `pkg/wkfsm/state_machine_test.go` | Moved to wkstore/statemachine_test.go |
| `pkg/wkfsm/integration_test.go` | Moved to wkstore/integration_test.go |
| `pkg/wkfsm/benchmark_test.go` | Moved to wkstore/benchmark_test.go |
| `pkg/wkfsm/stress_test.go` | Moved to wkstore/stress_test.go |
| `pkg/wkfsm/testutil_test.go` | Moved to wkstore/testutil_test.go |

---

## Task 1: Create wkstore package with command encoding

Move TLV command encoding/decoding from `wkfsm/command.go` to `wkstore/command.go`. This is a pure copy with package rename — no logic changes.

**Files:**
- Create: `pkg/wkstore/command.go`

- [ ] **Step 1: Create wkstore/command.go**

Copy `pkg/wkfsm/command.go` content to `pkg/wkstore/command.go`. Only changes needed:
- `package wkfsm` → `package wkstore`
- All function/type names stay identical

```go
// pkg/wkstore/command.go
// Copy entire content of pkg/wkfsm/command.go verbatim, only changing the package line:
// package wkfsm → package wkstore
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && go build ./pkg/wkstore/`
Expected: SUCCESS (no errors)

- [ ] **Step 3: Commit**

```bash
git add pkg/wkstore/command.go
git commit -m "feat(wkstore): add command encoding (moved from wkfsm)"
```

---

## Task 2: Create wkstore statemachine

Move state machine implementation from `wkfsm/state_machine.go` to `wkstore/statemachine.go`. Add `NewStateMachineFactory` function.

**Files:**
- Create: `pkg/wkstore/statemachine.go`

- [ ] **Step 1: Create wkstore/statemachine.go**

Copy `pkg/wkfsm/state_machine.go` to `pkg/wkstore/statemachine.go` with these changes:
- `package wkfsm` → `package wkstore`
- Add `NewStateMachineFactory` function

```go
// At the end of the file, add:

// NewStateMachineFactory returns a factory function suitable for wkcluster.Config.NewStateMachine.
// The returned factory creates a StateMachine for each raft group, backed by the shared wkdb.DB.
func NewStateMachineFactory(db *wkdb.DB) func(groupID multiraft.GroupID) (multiraft.StateMachine, error) {
	return func(groupID multiraft.GroupID) (multiraft.StateMachine, error) {
		return NewStateMachine(db, uint64(groupID))
	}
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && go build ./pkg/wkstore/`
Expected: SUCCESS

- [ ] **Step 3: Commit**

```bash
git add pkg/wkstore/statemachine.go
git commit -m "feat(wkstore): add state machine + factory (moved from wkfsm)"
```

---

## Task 3: Create wkstore Store with business API

Create the `Store` struct and the business API methods (Channel CRUD + User CRUD) that call `wkcluster.Propose`.

**Files:**
- Create: `pkg/wkstore/store.go`

- [ ] **Step 1: Create wkstore/store.go**

Note: This file imports `wkcluster` which doesn't yet have `Propose`/`SlotForKey`. It will NOT compile until Task 5 modifies wkcluster. That's expected — we create the file now and verify compilation after Task 5.

```go
package wkstore

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/controller/wkcluster"
	"github.com/WuKongIM/WuKongIM/pkg/controller/wkdb"
)

// Store provides business-level distributed storage APIs
// built on top of wkcluster's generic Propose mechanism.
type Store struct {
	cluster *wkcluster.Cluster
	db      *wkdb.DB
}

// New creates a Store. The cluster must already be created (but not necessarily started).
func New(cluster *wkcluster.Cluster, db *wkdb.DB) *Store {
	return &Store{cluster: cluster, db: db}
}

// --- Channel operations ---

func (s *Store) CreateChannel(ctx context.Context, channelID string, channelType int64) error {
	groupID := s.cluster.SlotForKey(channelID)
	cmd := EncodeUpsertChannelCommand(wkdb.Channel{
		ChannelID:   channelID,
		ChannelType: channelType,
	})
	return s.cluster.Propose(ctx, groupID, cmd)
}

func (s *Store) UpdateChannel(ctx context.Context, channelID string, channelType int64, ban int64) error {
	groupID := s.cluster.SlotForKey(channelID)
	cmd := EncodeUpsertChannelCommand(wkdb.Channel{
		ChannelID:   channelID,
		ChannelType: channelType,
		Ban:         ban,
	})
	return s.cluster.Propose(ctx, groupID, cmd)
}

func (s *Store) DeleteChannel(ctx context.Context, channelID string, channelType int64) error {
	groupID := s.cluster.SlotForKey(channelID)
	cmd := EncodeDeleteChannelCommand(channelID, channelType)
	return s.cluster.Propose(ctx, groupID, cmd)
}

func (s *Store) GetChannel(ctx context.Context, channelID string, channelType int64) (wkdb.Channel, error) {
	groupID := s.cluster.SlotForKey(channelID)
	return s.db.ForSlot(uint64(groupID)).GetChannel(ctx, channelID, channelType)
}

// --- User operations ---

func (s *Store) UpsertUser(ctx context.Context, u wkdb.User) error {
	groupID := s.cluster.SlotForKey(u.UID)
	cmd := EncodeUpsertUserCommand(u)
	return s.cluster.Propose(ctx, groupID, cmd)
}

func (s *Store) GetUser(ctx context.Context, uid string) (wkdb.User, error) {
	groupID := s.cluster.SlotForKey(uid)
	return s.db.ForSlot(uint64(groupID)).GetUser(ctx, uid)
}
```

- [ ] **Step 2: Commit (will not compile yet — depends on Task 5)**

```bash
git add pkg/wkstore/store.go
git commit -m "feat(wkstore): add Store with channel/user business API"
```

---

## Task 4: Refactor wkcluster Config — remove storage fields, add factories

Modify `pkg/wkcluster/config.go` to remove `DataDir`/`RaftDataDir` and add factory function fields.

**Files:**
- Modify: `pkg/wkcluster/config.go`
- Modify: `pkg/wkcluster/config_test.go`

- [ ] **Step 1: Update Config struct**

In `pkg/wkcluster/config.go`:

Remove fields:
```
DataDir     string
RaftDataDir string
```

Add fields (after Groups):
```go
// Storage factories (injected by caller).
// NewStorage creates a raft log Storage for a given group.
// NewStateMachine creates the state machine for a given group.
NewStorage      func(groupID multiraft.GroupID) (multiraft.Storage, error)
NewStateMachine func(groupID multiraft.GroupID) (multiraft.StateMachine, error)
```

- [ ] **Step 2: Update validate()**

Replace the DataDir validation:
```go
// Remove:
if c.DataDir == "" {
    return fmt.Errorf("%w: DataDir must be set", ErrInvalidConfig)
}

// Add:
if c.NewStorage == nil {
    return fmt.Errorf("%w: NewStorage must be set", ErrInvalidConfig)
}
if c.NewStateMachine == nil {
    return fmt.Errorf("%w: NewStateMachine must be set", ErrInvalidConfig)
}
```

- [ ] **Step 3: Update applyDefaults()**

Remove:
```go
if c.RaftDataDir == "" {
    c.RaftDataDir = filepath.Join(c.DataDir, "raft")
}
```

Remove the `dataDir()` method at the bottom of the file.

Remove the `"path/filepath"` import (no longer needed).

- [ ] **Step 4: Update config_test.go**

In `validTestConfig()`, replace `DataDir: "/tmp/test"` with factory functions:

```go
func validTestConfig() Config {
	return Config{
		NodeID:     1,
		ListenAddr: ":9001",
		GroupCount: 1,
		Nodes: []NodeConfig{
			{NodeID: 1, Addr: "127.0.0.1:9001"},
			{NodeID: 2, Addr: "127.0.0.1:9002"},
			{NodeID: 3, Addr: "127.0.0.1:9003"},
		},
		Groups: []GroupConfig{
			{GroupID: 1, Peers: []multiraft.NodeID{1, 2, 3}},
		},
		NewStorage: func(groupID multiraft.GroupID) (multiraft.Storage, error) {
			return nil, nil // not used in config validation tests
		},
		NewStateMachine: func(groupID multiraft.GroupID) (multiraft.StateMachine, error) {
			return nil, nil
		},
	}
}
```

Remove `TestConfigValidate_DataDirEmpty` test (DataDir no longer exists). Add tests for nil factory validation:

```go
func TestConfigValidate_NewStorageNil(t *testing.T) {
	cfg := validTestConfig()
	cfg.NewStorage = nil
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestConfigValidate_NewStateMachineNil(t *testing.T) {
	cfg := validTestConfig()
	cfg.NewStateMachine = nil
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got: %v", err)
	}
}
```

Also update `TestConfigApplyDefaults`: remove the assertion for `RaftDataDir`.

- [ ] **Step 5: Verify config tests pass**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && go test ./pkg/wkcluster/ -run TestConfig -v`
Expected: All config tests PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/wkcluster/config.go pkg/wkcluster/config_test.go
git commit -m "refactor(wkcluster): replace DataDir with factory functions in Config"
```

---

## Task 5: Refactor wkcluster Cluster — remove storage, expose Propose/SlotForKey

The core refactoring task. Remove `db`/`raftDB` fields from Cluster, update `Start()`/`Stop()`/`openOrBootstrapGroup()`, rename `proposeOrForward` to `Propose`, rename `SlotForChannel` to `SlotForKey`, delete `api.go`.

**Files:**
- Modify: `pkg/wkcluster/cluster.go`
- Modify: `pkg/wkcluster/router.go`
- Modify: `pkg/wkcluster/forward.go` (move proposeOrForward to cluster.go)
- Delete: `pkg/wkcluster/api.go`
- Modify: `pkg/wkcluster/router_test.go`

- [ ] **Step 1: Update router.go — rename SlotForChannel to SlotForKey**

In `pkg/wkcluster/router.go`, rename:
- Method: `SlotForChannel` → `SlotForKey`
- Comment: update "channel" references to "key"

```go
// SlotForKey maps a key to a raft group via CRC32 hashing.
// NOTE: The mapping is deterministic for a given groupCount but will change
// if groupCount changes (no consistent hashing). All nodes must use the same
// groupCount for correct routing.
func (r *Router) SlotForKey(key string) multiraft.GroupID {
	return multiraft.GroupID(crc32.ChecksumIEEE([]byte(key))%r.groupCount + 1)
}
```

- [ ] **Step 2: Update router_test.go — SlotForChannel → SlotForKey**

Replace all occurrences of `SlotForChannel` with `SlotForKey` in test names and code:
- `TestSlotForChannel_Deterministic` → `TestSlotForKey_Deterministic`
- `TestSlotForChannel_Range` → `TestSlotForKey_Range`
- `TestSlotForChannel_Distribution` → `TestSlotForKey_Distribution`
- All `r.SlotForChannel(...)` calls → `r.SlotForKey(...)`

- [ ] **Step 3: Move proposeOrForward to cluster.go as exported Propose**

In `pkg/wkcluster/api.go` (lines 45-79), the `proposeOrForward` method will become `Propose` on Cluster. But it also needs the `wktransport` import which is already in `forward.go`. Move it to `cluster.go`:

Add to `pkg/wkcluster/cluster.go`:

```go
// Propose submits a command to the specified raft group. It automatically
// detects the group's leader and either proposes locally (if this node is
// leader) or forwards the command via RPC to the current leader. Includes
// retry with exponential backoff if the leader changes during forwarding.
func (c *Cluster) Propose(ctx context.Context, groupID multiraft.GroupID, cmd []byte) error {
	if c.stopped.Load() {
		return wktransport.ErrStopped
	}
	const maxRetries = 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * 50 * time.Millisecond
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		leaderID, err := c.router.LeaderOf(groupID)
		if err != nil {
			return err
		}
		if c.router.IsLocal(leaderID) {
			future, err := c.runtime.Propose(ctx, groupID, cmd)
			if err != nil {
				return err
			}
			_, err = future.Wait(ctx)
			return err
		}
		err = c.forwardToLeader(ctx, leaderID, groupID, cmd)
		if errors.Is(err, ErrNotLeader) {
			continue
		}
		return err
	}
	return ErrLeaderNotStable
}

// SlotForKey maps a key string to a raft group ID using CRC32 hashing.
func (c *Cluster) SlotForKey(key string) multiraft.GroupID {
	return c.router.SlotForKey(key)
}

// LeaderOf returns the current leader of the specified group.
func (c *Cluster) LeaderOf(groupID multiraft.GroupID) (multiraft.NodeID, error) {
	return c.router.LeaderOf(groupID)
}

// IsLocal reports whether the given node is this cluster node.
func (c *Cluster) IsLocal(nodeID multiraft.NodeID) bool {
	return c.router.IsLocal(nodeID)
}
```

Add `"errors"` and `"time"` to cluster.go imports (if not already there).

- [ ] **Step 4: Update cluster.go — remove storage fields and imports**

Remove from Cluster struct:
```go
db     *wkdb.DB
raftDB *raftstore.DB
```

Remove imports:
```go
"github.com/WuKongIM/WuKongIM/pkg/controller/raftstore"
"github.com/WuKongIM/WuKongIM/pkg/controller/wkdb"
"github.com/WuKongIM/WuKongIM/pkg/wkfsm"
```

- [ ] **Step 5: Update Start() — use injected factories**

Replace the database opening section in `Start()`:

Remove:
```go
// 1. Open databases
var err error
c.db, err = wkdb.Open(c.cfg.dataDir())
if err != nil {
    return fmt.Errorf("open wkdb: %w", err)
}
c.raftDB, err = raftstore.Open(c.cfg.RaftDataDir)
if err != nil {
    _ = c.db.Close()
    return fmt.Errorf("open raftstore: %w", err)
}
```

Replace with:
```go
var err error
```

Update error cleanup in the server start section — remove `_ = c.raftDB.Close()` and `_ = c.db.Close()`.

Update error cleanup in the runtime creation section — remove `_ = c.raftDB.Close()` and `_ = c.db.Close()`.

- [ ] **Step 6: Update openOrBootstrapGroup() — use factories**

Replace:
```go
func (c *Cluster) openOrBootstrapGroup(ctx context.Context, g GroupConfig) error {
	storage := c.raftDB.ForGroup(uint64(g.GroupID))
	sm, err := wkfsm.NewStateMachine(c.db, uint64(g.GroupID))
	if err != nil {
		return err
	}
```

With:
```go
func (c *Cluster) openOrBootstrapGroup(ctx context.Context, g GroupConfig) error {
	storage, err := c.cfg.NewStorage(g.GroupID)
	if err != nil {
		return fmt.Errorf("create storage for group %d: %w", g.GroupID, err)
	}
	sm, err := c.cfg.NewStateMachine(g.GroupID)
	if err != nil {
		return fmt.Errorf("create state machine for group %d: %w", g.GroupID, err)
	}
```

- [ ] **Step 7: Update Stop() — remove database closes**

Remove from `Stop()`:
```go
if c.raftDB != nil {
    _ = c.raftDB.Close()
}
if c.db != nil {
    _ = c.db.Close()
}
```

- [ ] **Step 8: Delete api.go**

Delete `pkg/wkcluster/api.go` entirely. The `proposeOrForward` method is now `Propose` in `cluster.go`. The business methods (CreateChannel etc.) are now in `wkstore/store.go`.

- [ ] **Step 9: Clean up forward.go imports**

In `pkg/wkcluster/forward.go`, the file should only need:
```go
import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/consensus/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/wktransport"
)
```

This is already correct — no changes needed to `forward.go` itself (only `forwardToLeader` and `handleForwardRPC` remain, both are internal).

- [ ] **Step 10: Verify wkcluster compiles with zero storage imports**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && go build ./pkg/wkcluster/`
Expected: SUCCESS

Verify no forbidden imports:
Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && grep -r 'wkdb\|raftstore\|wkfsm' pkg/wkcluster/*.go | grep -v _test.go`
Expected: No output (zero matches in non-test files)

- [ ] **Step 11: Verify wkstore compiles**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && go build ./pkg/wkstore/`
Expected: SUCCESS

- [ ] **Step 12: Commit**

```bash
git add pkg/wkcluster/cluster.go pkg/wkcluster/router.go pkg/wkcluster/router_test.go pkg/wkcluster/forward.go
git rm pkg/wkcluster/api.go
git commit -m "refactor(wkcluster): remove storage deps, expose Propose/SlotForKey, delete api.go"
```

---

## Task 6: Update wkcluster integration tests to use wkstore

The cluster_test.go and stress_test.go files call business APIs like `c.CreateChannel`. Update them to use `wkstore.Store`.

**Files:**
- Modify: `pkg/wkcluster/cluster_test.go`
- Modify: `pkg/wkcluster/stress_test.go`

- [ ] **Step 1: Update cluster_test.go**

Key changes:
1. Add imports: `"github.com/WuKongIM/WuKongIM/pkg/controller/wkstore"`, `"github.com/WuKongIM/WuKongIM/pkg/controller/raftstore"`, `"github.com/WuKongIM/WuKongIM/pkg/controller/wkdb"`
2. Each test function that creates a Config must supply `NewStorage`/`NewStateMachine` factories
3. Each test function that calls `c.CreateChannel` etc. must create a `wkstore.Store` and use it

Replace the single-node cluster test setup pattern. For each single-node cluster test:

```go
// Before (old pattern):
cfg := Config{
    NodeID: 1, ListenAddr: "127.0.0.1:0", GroupCount: 1,
    DataDir: dir,
    Nodes: []NodeConfig{{NodeID: 1, Addr: "127.0.0.1:0"}},
    Groups: []GroupConfig{{GroupID: 1, Peers: []multiraft.NodeID{1}}},
}
c, err := NewCluster(cfg)
// ... c.CreateChannel(ctx, "ch-1", 1)

// After (new pattern):
db, err := wkdb.Open(filepath.Join(dir, "data"))
if err != nil { t.Fatalf("open wkdb: %v", err) }
defer db.Close()
raftDB, err := raftstore.Open(filepath.Join(dir, "raft"))
if err != nil { t.Fatalf("open raftstore: %v", err) }
defer raftDB.Close()

cfg := Config{
    NodeID: 1, ListenAddr: "127.0.0.1:0", GroupCount: 1,
    Nodes: []NodeConfig{{NodeID: 1, Addr: "127.0.0.1:0"}},
    Groups: []GroupConfig{{GroupID: 1, Peers: []multiraft.NodeID{1}}},
    NewStorage: func(groupID multiraft.GroupID) (multiraft.Storage, error) {
        return raftDB.ForGroup(uint64(groupID)), nil
    },
    NewStateMachine: wkstore.NewStateMachineFactory(db),
}
c, err := NewCluster(cfg)
// ...
store := wkstore.New(c, db)
// ... store.CreateChannel(ctx, "ch-1", 1)
```

Apply this pattern to all 4 tests: `TestCluster_SingleNode_CreateAndGetChannel`, `TestCluster_SingleNode_UpdateChannel`, `TestCluster_SingleNode_DeleteChannel`, `TestCluster_ThreeNode_ForwardToLeader`.

For `startThreeNodeCluster`, each node needs its own `wkdb.DB` and `raftstore.DB`. Return them alongside clusters so tests can create `wkstore.Store` instances. Update the helper to return a struct:

```go
type testCluster struct {
    clusters []*Cluster
    dbs      []*wkdb.DB
    raftDBs  []*raftstore.DB
}
```

- [ ] **Step 2: Update stress_test.go**

Same pattern as cluster_test.go. Update `startSingleNodeForStress` and `startThreeNodeForStress` to:
1. Create wkdb.DB and raftstore.DB
2. Inject factory functions into Config
3. Return the DBs alongside clusters

All stress test goroutines that call `c.CreateChannel`/`c.GetChannel`/`c.UpdateChannel`/`c.DeleteChannel` change to `store.CreateChannel` etc.

Helper changes:
```go
func startSingleNodeForStress(t testing.TB, groupCount int) (*Cluster, *wkstore.Store) {
    dir := t.TempDir()
    db, err := wkdb.Open(filepath.Join(dir, "data"))
    // ...
    raftDB, err := raftstore.Open(filepath.Join(dir, "raft"))
    // ...
    cfg := Config{
        // ... same as before but with factories ...
        NewStorage: func(groupID multiraft.GroupID) (multiraft.Storage, error) {
            return raftDB.ForGroup(uint64(groupID)), nil
        },
        NewStateMachine: wkstore.NewStateMachineFactory(db),
    }
    c, err := NewCluster(cfg)
    // ...
    store := wkstore.New(c, db)
    t.Cleanup(func() {
        c.Stop()
        raftDB.Close()
        db.Close()
    })
    return c, store
}
```

- [ ] **Step 3: Run wkcluster tests**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && go test ./pkg/wkcluster/ -run 'TestCluster' -v -timeout 60s`
Expected: All 4 cluster tests PASS

- [ ] **Step 4: Commit**

```bash
git add pkg/wkcluster/cluster_test.go pkg/wkcluster/stress_test.go
git commit -m "test(wkcluster): update tests to use wkstore for business API"
```

---

## Task 7: Migrate wkfsm tests to wkstore

Move all test files from `pkg/wkfsm/` to `pkg/wkstore/`. Only change is package name.

**Files:**
- Create: `pkg/wkstore/testutil_test.go` (from wkfsm)
- Create: `pkg/wkstore/statemachine_test.go` (from wkfsm)
- Create: `pkg/wkstore/integration_test.go` (from wkfsm)
- Create: `pkg/wkstore/benchmark_test.go` (from wkfsm)
- Create: `pkg/wkstore/stress_test.go` (from wkfsm)

- [ ] **Step 1: Copy test files with package rename**

For each file, copy from `pkg/wkfsm/` to `pkg/wkstore/` changing only:
- `package wkfsm` → `package wkstore`

Files to copy:
- `testutil_test.go`
- `state_machine_test.go` → `statemachine_test.go`
- `integration_test.go`
- `benchmark_test.go`
- `stress_test.go`

The stress test env var prefix should change: `WKFSM_STRESS` → `WKSTORE_STRESS` (and all related env vars). Update the constants:
```go
const (
    stressEnvKey      = "WKSTORE_STRESS"
    stressDurationEnv = "WKSTORE_STRESS_DURATION"
    stressWorkersEnv  = "WKSTORE_STRESS_WORKERS"
    stressSlotsEnv    = "WKSTORE_STRESS_SLOTS"
    stressSeedEnv     = "WKSTORE_STRESS_SEED"
)
```

- [ ] **Step 2: Run wkstore unit tests**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && go test ./pkg/wkstore/ -run 'TestStateMachine|TestApplyBatch|TestEncodeDecodeEdgeCases|TestNewStateMachineValidation|TestEncodeDecodeChannelEdgeCases' -v`
Expected: All tests PASS

- [ ] **Step 3: Run wkstore integration tests**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && go test ./pkg/wkstore/ -run 'TestMemory|TestPebble' -v -timeout 60s`
Expected: All integration tests PASS

- [ ] **Step 4: Commit**

```bash
git add pkg/wkstore/testutil_test.go pkg/wkstore/statemachine_test.go pkg/wkstore/integration_test.go pkg/wkstore/benchmark_test.go pkg/wkstore/stress_test.go
git commit -m "test(wkstore): migrate all wkfsm tests to wkstore"
```

---

## Task 8: Delete pkg/wkfsm

Now that all code and tests have been migrated to wkstore, delete the entire wkfsm package.

**Files:**
- Delete: `pkg/wkfsm/` (entire directory)

- [ ] **Step 1: Verify no remaining imports of wkfsm**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && grep -r '"github.com/WuKongIM/WuKongIM/pkg/wkfsm"' pkg/ --include='*.go'`
Expected: No output (zero references)

- [ ] **Step 2: Delete the directory**

```bash
git rm -r pkg/wkfsm/
```

- [ ] **Step 3: Verify entire project compiles**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && go build ./...`
Expected: SUCCESS

- [ ] **Step 4: Run all tests**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && go test ./pkg/wkcluster/ ./pkg/wkstore/ -v -timeout 120s`
Expected: All tests PASS

- [ ] **Step 5: Commit**

```bash
git commit -m "refactor: delete pkg/wkfsm (merged into wkstore)"
```

---

## Task 9: Final verification — acceptance criteria

Run all acceptance checks from the spec.

- [ ] **Step 1: Verify wkcluster has zero storage imports**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && grep -r 'wkdb\|raftstore\|wkfsm' pkg/wkcluster/*.go | grep -v _test.go`
Expected: No output

- [ ] **Step 2: Verify wkcluster has no business API methods**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && grep -n 'func.*Cluster.*CreateChannel\|func.*Cluster.*UpdateChannel\|func.*Cluster.*DeleteChannel\|func.*Cluster.*GetChannel' pkg/wkcluster/*.go`
Expected: No output

- [ ] **Step 3: Verify wkfsm directory is gone**

Run: `ls pkg/wkfsm/ 2>&1`
Expected: "No such file or directory"

- [ ] **Step 4: Verify wkstore has all expected exports**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && grep -n '^func.*Store.*Create\|^func.*Store.*Update\|^func.*Store.*Delete\|^func.*Store.*Get\|^func.*Store.*Upsert\|^func NewStateMachineFactory' pkg/wkstore/*.go | grep -v _test.go`
Expected: CreateChannel, UpdateChannel, DeleteChannel, GetChannel, UpsertUser, GetUser, NewStateMachineFactory

- [ ] **Step 5: Run full test suite**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && go test ./pkg/... -timeout 120s`
Expected: All packages PASS

- [ ] **Step 6: Commit verification (no code changes, just verification)**

No commit needed — this is a verification-only task.
