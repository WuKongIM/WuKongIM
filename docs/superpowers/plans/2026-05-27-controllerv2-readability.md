# ControllerV2 Readability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve `pkg/controllerv2` readability without changing public API, Raft behavior, durable state format, or cluster semantics.

**Architecture:** Keep the existing package boundaries and root facade. Make the reader path narrower by improving package documentation, splitting long files by responsibility, and adding intent comments around ControllerV2 state, Raft apply, planner, and full-file sync semantics.

**Tech Stack:** Go, etcd Raft RawNode, local ControllerV2 statefile JSON, existing `go test` unit suite.

---

## File Structure

Create:
- `pkg/controllerv2/doc.go`: root package facade documentation.
- `pkg/controllerv2/runtime_start.go`: voter and mirror startup wiring.
- `pkg/controllerv2/runtime_bootstrap.go`: bootstrap command and planner helpers.
- `pkg/controllerv2/runtime_refresh.go`: periodic control tick and state publication helpers.
- `pkg/controllerv2/runtime_sync.go`: state sync server and mirror adapter helpers.
- `pkg/controllerv2/fsm/mutation_handlers.go`: concrete command handlers.
- `pkg/controllerv2/fsm/mutation_guards.go`: expected-revision, stale-command, and revision advancement rules.
- `pkg/controllerv2/fsm/mutation_helpers.go`: find, clone, compare, upsert, and UTF-8 helpers.
- `pkg/controllerv2/raft/service_run.go`: RawNode run loop and Ready processing.
- `pkg/controllerv2/raft/service_recovery.go`: startup recovery, replay, and RawNode creation.
- `pkg/controllerv2/raft/service_snapshot.go`: ControllerV2 snapshot and compaction trigger.
- `pkg/controllerv2/raft/service_helpers.go`: status, bootstrap, peer, and conf-change helpers.
- `pkg/controllerv2/sync/contracts.go`: sync request/response and endpoint contracts.
- `pkg/controllerv2/sync/errors.go`: sync package sentinel errors.
- `pkg/controllerv2/sync/server.go`: full-file sync server.
- `pkg/controllerv2/sync/client.go`: full-file sync client.

Modify:
- `pkg/controllerv2/FLOW.md`: add a reading guide and keep flow aligned with the split.
- `pkg/controllerv2/runtime.go`: keep `Runtime`, constructor, and public methods only.
- `pkg/controllerv2/fsm/mutations.go`: keep mutation result reasons and top-level dispatch only.
- `pkg/controllerv2/raft/service.go`: keep public service lifecycle and proposal API only.
- `pkg/controllerv2/sync/sync.go`: remove after moving all declarations, or reduce to a package comment if needed.
- `pkg/controllerv2/{command,fsm,planner,raft,server,state,statefile,sync}/doc.go`: expand package comments.

Do not modify:
- Public root facade types in `pkg/controllerv2/types.go`, except comments if a wording mismatch is found.
- `pkg/clusterv2/control/import_boundary_test.go`; it already protects the intended boundary.
- Durable JSON tags, command names, state checksum behavior, or Raft store formats.

## Task 1: Document The Reading Path

**Files:**
- Create: `pkg/controllerv2/doc.go`
- Modify: `pkg/controllerv2/FLOW.md`
- Modify: `pkg/controllerv2/command/doc.go`
- Modify: `pkg/controllerv2/fsm/doc.go`
- Modify: `pkg/controllerv2/planner/doc.go`
- Modify: `pkg/controllerv2/raft/doc.go`
- Modify: `pkg/controllerv2/server/doc.go`
- Modify: `pkg/controllerv2/state/doc.go`
- Modify: `pkg/controllerv2/statefile/doc.go`
- Modify: `pkg/controllerv2/sync/doc.go`

- [ ] **Step 1: Re-read current docs before editing**

Run:

```bash
sed -n '1,240p' pkg/controllerv2/FLOW.md
sed -n '1,120p' pkg/controllerv2/docs/USAGE.md
for f in pkg/controllerv2/{command,fsm,planner,raft,server,state,statefile,sync}/doc.go; do sed -n '1,80p' "$f"; done
```

Expected: `FLOW.md` describes package responsibilities and Raft/apply flow; most `doc.go` files have one-line comments.

- [ ] **Step 2: Add root package documentation**

Create `pkg/controllerv2/doc.go` with:

```go
// Package controllerv2 exposes the public ControllerV2 runtime facade.
//
// Callers should depend on this root package for ControllerV2 runtime startup,
// strongly typed cluster-state snapshots, state change events, Raft message
// stepping, proposal probes, and full-file state sync contracts. Subpackages
// contain the reusable Controller engine internals and should stay behind the
// facade for production integrations.
package controllerv2
```

- [ ] **Step 3: Expand package comments**

Replace each one-line subpackage comment with concise responsibility notes:

```go
// Package fsm applies committed ControllerV2 commands to durable cluster state.
//
// The state machine owns semantic command application. It loads the current
// cluster-state.json snapshot, applies committed Raft entries deterministically,
// saves the final state once per batch, and publishes an in-memory snapshot only
// after durable save succeeds.
package fsm
```

Use the same shape for other packages:
- `command`: versioned command envelope and JSON codec.
- `planner`: pure planning from state snapshots to optional commands.
- `raft`: RawNode, WAL, scheduled apply, snapshot, compaction.
- `server`: thin composition facade for local state, planner, proposal, sync.
- `state`: durable state model, normalization, validation, checksum, hash slots.
- `statefile`: atomic load/save for `cluster-state.json`.
- `sync`: leader full-file sync for non-controller mirror nodes.

- [ ] **Step 4: Update `FLOW.md` reading guide**

Add a short section after Package Boundaries:

```markdown
## Reading Guide

Start at the root facade when reading production behavior:

1. `runtime.go` exposes the public API and holds runtime state.
2. `runtime_start.go` wires voter or mirror mode.
3. `runtime_bootstrap.go` creates the initial ControllerV2 state through the same Raft path used by multi-node voters.
4. `raft/service.go` owns public Raft lifecycle; `raft/service_run.go` owns Ready persistence, message send order, and scheduled apply.
5. `fsm/mutations.go` dispatches commands; `fsm/mutation_handlers.go` contains the actual state changes.
6. `sync/server.go` and `sync/client.go` implement full-file sync for mirror nodes.

`Revision` is the logical cluster-state version. `AppliedRaftIndex` is the last committed Raft entry materialized into `cluster-state.json`. Probe entries may advance applied metadata without advancing `Revision`.
```

- [ ] **Step 5: Verify documentation builds with Go tooling**

Run:

```bash
go test ./pkg/controllerv2/...
```

Expected: all `pkg/controllerv2` tests pass.

## Task 2: Split Root Runtime By Lifecycle Stage

**Files:**
- Modify: `pkg/controllerv2/runtime.go`
- Create: `pkg/controllerv2/runtime_start.go`
- Create: `pkg/controllerv2/runtime_bootstrap.go`
- Create: `pkg/controllerv2/runtime_refresh.go`
- Create: `pkg/controllerv2/runtime_sync.go`
- Test: `pkg/controllerv2/runtime_test.go`

- [ ] **Step 1: Capture current function list**

Run:

```bash
rg -n "^func |^type " pkg/controllerv2/runtime.go
```

Expected: function names match the current runtime implementation before moving code.

- [ ] **Step 2: Keep public facade in `runtime.go`**

Leave only:
- imports required by the remaining declarations.
- `Runtime` struct.
- `NewRuntime`.
- `Start`.
- `Stop`.
- `LocalState`.
- `LeaderID`.
- `ProbePropose`.
- `Step`.
- `GetState`.
- `Watch`.
- `ctxErr`.

Do not change any method signature or exported comment.

- [ ] **Step 3: Move startup wiring to `runtime_start.go`**

Move these methods exactly, preserving bodies first:

```go
func (r *Runtime) startVoter(ctx context.Context) error
func (r *Runtime) startMirror(ctx context.Context) error
```

Required imports for the new file:

```go
import (
	"context"
	"errors"
	"path/filepath"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/fsm"
	cv2raft "github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/server"
	cv2sync "github.com/WuKongIM/WuKongIM/pkg/controllerv2/sync"
)
```

- [ ] **Step 4: Move bootstrap helpers to `runtime_bootstrap.go`**

Move these methods exactly, preserving bodies first:

```go
func (r *Runtime) bootstrapIfNeeded(ctx context.Context) error
func (r *Runtime) runBootstrapPlanner(ctx context.Context) error
func (r *Runtime) isLocalLeader() bool
func (r *Runtime) waitLocalLeader(ctx context.Context) error
func (r *Runtime) initCommand() command.Command
func (r *Runtime) raftPeers() []cv2raft.Peer
```

After moving, add one comment above `bootstrapIfNeeded`:

```go
// bootstrapIfNeeded creates the first state through Controller Raft, even for a single-node cluster.
```

- [ ] **Step 5: Move refresh and publication helpers to `runtime_refresh.go`**

Move these methods exactly, preserving behavior:

```go
func (r *Runtime) startRefreshLoop()
func (r *Runtime) controlTick(ctx context.Context) error
func (r *Runtime) publishIfChanged(ctx context.Context, revision uint64) error
func (r *Runtime) publishFromState(ctx context.Context) error
func (r *Runtime) publishState(st ClusterState) error
```

Add one comment above `controlTick`:

```go
// controlTick keeps bootstrap progress and local watcher state moving without bypassing Raft semantics.
```

- [ ] **Step 6: Move sync helper types to `runtime_sync.go`**

Move these declarations exactly:

```go
func (r *Runtime) newStateSyncServer() *cv2sync.Server
type noopRaftTransport struct{}
func (noopRaftTransport) Send([]raftpb.Message)
type syncClientAdapter struct { client *cv2sync.Client }
func (a syncClientAdapter) SyncOnce(ctx context.Context) (state.ClusterState, error)
```

- [ ] **Step 7: Run runtime-focused tests**

Run:

```bash
go test ./pkg/controllerv2 -run 'TestRuntime|TestNewRuntime' -count=1
```

Expected: tests pass.

- [ ] **Step 8: Run package tests**

Run:

```bash
go test ./pkg/controllerv2/... -count=1
```

Expected: all tests pass.

## Task 3: Split FSM Mutation Semantics

**Files:**
- Modify: `pkg/controllerv2/fsm/mutations.go`
- Create: `pkg/controllerv2/fsm/mutation_handlers.go`
- Create: `pkg/controllerv2/fsm/mutation_guards.go`
- Create: `pkg/controllerv2/fsm/mutation_helpers.go`
- Test: `pkg/controllerv2/fsm/fsm_test.go`

- [ ] **Step 1: Capture current mutation function list**

Run:

```bash
rg -n "^func |^const \\(" pkg/controllerv2/fsm/mutations.go
```

Expected: all current mutation helpers are visible before splitting.

- [ ] **Step 2: Keep dispatch and reason constants in `mutations.go`**

Leave these declarations in `mutations.go`:

```go
const (
	ReasonNoChange = "no_change"
	ReasonAlreadyApplied = "already_applied"
	ReasonStaleBootstrapObsolete = "stale_bootstrap_obsolete"
	ReasonStaleBootstrapMissingSlot = "stale_bootstrap_missing_slot"
	ReasonExpectedRevisionMismatch = "expected_revision_mismatch"
	ReasonInvalidState = "invalid_state"
	ReasonInvalidCommand = "invalid_command"
	ReasonInvalidTaskResult = "invalid_task_result"
	ReasonTaskMissing = "task_missing"
	ReasonTaskSlotMismatch = "task_slot_mismatch"
	ReasonInitConflict = "init_conflict"
)

const MaxTaskLastErrorBytes = 1024

func (sm *StateMachine) applyMutation(next *state.ClusterState, raftIndex uint64, cmd command.Command) ApplyResult
```

Do not change the switch order yet.

- [ ] **Step 3: Move concrete handlers to `mutation_handlers.go`**

Move these methods and helper:

```go
func (sm *StateMachine) applyInit(next *state.ClusterState, raftIndex uint64, cmd command.Command) ApplyResult
func (sm *StateMachine) applyUpsertNode(next *state.ClusterState, cmd command.Command) ApplyResult
func (sm *StateMachine) applyUpdateControllerVoters(next *state.ClusterState, cmd command.Command) ApplyResult
func (sm *StateMachine) applyReplaceHashSlotTable(next *state.ClusterState, cmd command.Command) ApplyResult
func (sm *StateMachine) applyUpsertSlotAssignmentAndTask(next *state.ClusterState, cmd command.Command) ApplyResult
func (sm *StateMachine) applyCompleteTask(next *state.ClusterState, cmd command.Command) ApplyResult
func (sm *StateMachine) applyFailTask(next *state.ClusterState, cmd command.Command) ApplyResult
func initialStateFromCommand(cmd command.Command, raftIndex uint64) (state.ClusterState, error)
```

Add this file comment near the top:

```go
// Command handlers mutate only the in-memory candidate state. ApplyBatch owns durable save and publication.
```

- [ ] **Step 4: Move revision and stale-command rules to `mutation_guards.go`**

Move these functions:

```go
func handleBootstrapRevisionMismatch(current *state.ClusterState, cmd command.Command) (ApplyResult, bool)
func handleFailTaskRevisionMismatch(current *state.ClusterState, cmd command.Command) (ApplyResult, bool)
func isNonBootstrapIdempotent(current state.ClusterState, cmd command.Command) bool
func validateChanged(next *state.ClusterState, before state.ClusterState, cmd command.Command) ApplyResult
func nextUpdatedAt(previous time.Time, issuedAt time.Time) time.Time
func commandIssuedAt(issuedAt time.Time) time.Time
func changed() ApplyResult
func noop(reason string) ApplyResult
func reject(reason string) ApplyResult
```

Add this comment above `handleBootstrapRevisionMismatch`:

```go
// handleBootstrapRevisionMismatch makes stale bootstrap proposals idempotent when newer state already covers the slot.
```

- [ ] **Step 5: Move mechanical helpers to `mutation_helpers.go`**

Move these functions:

```go
func upsertNode(st *state.ClusterState, node state.Node)
func upsertAssignment(st *state.ClusterState, assignment state.SlotAssignment)
func upsertTask(st *state.ClusterState, task state.ReconcileTask)
func equivalentInit(current state.ClusterState, initial state.ClusterState) bool
func equivalentNode(a, b state.Node) bool
func equivalentAssignment(a, b state.SlotAssignment) bool
func equivalentTask(a, b state.ReconcileTask) bool
func findNode(nodes []state.Node, nodeID uint64) int
func findAssignment(assignments []state.SlotAssignment, slotID uint32) (state.SlotAssignment, bool)
func findTaskBySlot(tasks []state.ReconcileTask, slotID uint32) (state.ReconcileTask, bool)
func findTaskByID(tasks []state.ReconcileTask, taskID string) int
func truncateUTF8(s string, maxBytes int) string
func cloneNodes(nodes []state.Node) []state.Node
func cloneNode(node state.Node) state.Node
func cloneAssignment(assignment state.SlotAssignment) state.SlotAssignment
func cloneTask(task state.ReconcileTask) state.ReconcileTask
func cloneHashSlotTable(table state.HashSlotTable) state.HashSlotTable
```

- [ ] **Step 6: Run FSM tests**

Run:

```bash
go test ./pkg/controllerv2/fsm -count=1
```

Expected: tests pass.

## Task 4: Split Raft Service Flow

**Files:**
- Modify: `pkg/controllerv2/raft/service.go`
- Create: `pkg/controllerv2/raft/service_run.go`
- Create: `pkg/controllerv2/raft/service_recovery.go`
- Create: `pkg/controllerv2/raft/service_snapshot.go`
- Create: `pkg/controllerv2/raft/service_helpers.go`
- Test: `pkg/controllerv2/raft/service_test.go`
- Test: `pkg/controllerv2/raft/apply_scheduler_test.go`

- [ ] **Step 1: Capture current service function list**

Run:

```bash
rg -n "^func |^type " pkg/controllerv2/raft/service.go
```

Expected: public API, run loop, recovery, snapshot, and helpers are all in one file before splitting.

- [ ] **Step 2: Keep public lifecycle in `service.go`**

Leave these declarations:

```go
var ErrInvalidConfig
var ErrNotStarted
var ErrStopped
var ErrNotLeader
var ErrProposalRejected
type ProposalRejectedError struct
type Service struct
type proposalRequest struct
type trackedProposal struct
func NewService(cfg Config) (*Service, error)
func (s *Service) Start(ctx context.Context) error
func (s *Service) Stop() error
func (s *Service) Propose(ctx context.Context, cmd command.Command) error
func (s *Service) ProbePropose(ctx context.Context) error
func (s *Service) submitProposal(ctx context.Context, req proposalRequest) error
func (s *Service) Step(ctx context.Context, msg raftpb.Message) error
func (s *Service) LeaderID() uint64
func (s *Service) Status() Status
```

Do not change channel sizes, errors, or public method behavior.

- [ ] **Step 3: Move run loop to `service_run.go`**

Move:

```go
func (s *Service) run(store *raftstore.Store, startup runStartupState, stopCh <-chan struct{}, doneCh chan struct{}, stepCh <-chan raftpb.Message, proposalCh <-chan proposalRequest, initCh chan<- error)
```

Then extract the current `processReady` closure into a private helper only if it reduces indentation without changing ordering:

```go
func (s *Service) processReady(ctx context.Context, rawNode *etcdraft.RawNode, store *raftstore.Store, scheduler *applyScheduler, tracker *proposalTracker, trackerMu *sync.Mutex) error
```

The helper must preserve this order:
1. leader sends Ready messages before local WAL save.
2. `SaveReady` persists HardState, entries, and snapshots before `Advance`.
3. tracker binds appended entries after WAL save.
4. follower sends Ready messages after local persistence.
5. committed entries are enqueued before `Advance`.
6. conf changes are applied before `Advance`.
7. status updates and leader-loss failures happen after `Advance`.

- [ ] **Step 4: Move recovery to `service_recovery.go`**

Move:

```go
type runStartupState struct
func (s *Service) newRawNode(store etcdraft.Storage, startup runStartupState) (*etcdraft.RawNode, error)
func (s *Service) recoverStartup(ctx context.Context, store *raftstore.Store) (runStartupState, error)
```

Add this comment above `recoverStartup`:

```go
// recoverStartup rebuilds the materialized state file from snapshots and committed WAL entries before RawNode starts.
```

- [ ] **Step 5: Move snapshots to `service_snapshot.go`**

Move:

```go
func (s *Service) maybeSnapshot(ctx context.Context, store *raftstore.Store, applied uint64) error
```

Add this comment above `maybeSnapshot`:

```go
// maybeSnapshot snapshots only materialized ControllerV2 state, then compacts WAL entries already covered by catch-up retention.
```

- [ ] **Step 6: Move small helpers to `service_helpers.go`**

Move:

```go
func (s *Service) sendReadyMessages(messages []raftpb.Message)
func countConfChanges(entries []raftpb.Entry) int
func applyConfChange(rawNode *etcdraft.RawNode, entry raftpb.Entry) (raftpb.ConfState, error)
func (s *Service) setRunError(err error)
func (s *Service) currentError() error
func (s *Service) recordDegraded(err error)
func (s *Service) updateStatus(rawNode *etcdraft.RawNode, err error)
func shouldBootstrap(startup runStartupState) bool
func isSmallestPeer(nodeID uint64, peers []Peer) bool
func raftPeers(peers []Peer) []etcdraft.Peer
func isZeroConfState(state raftpb.ConfState) bool
```

- [ ] **Step 7: Run Raft tests**

Run:

```bash
go test ./pkg/controllerv2/raft -count=1
```

Expected: tests pass.

## Task 5: Split Sync Server And Client

**Files:**
- Modify: `pkg/controllerv2/sync/sync.go`
- Create: `pkg/controllerv2/sync/contracts.go`
- Create: `pkg/controllerv2/sync/errors.go`
- Create: `pkg/controllerv2/sync/server.go`
- Create: `pkg/controllerv2/sync/client.go`
- Test: `pkg/controllerv2/sync/sync_test.go`

- [ ] **Step 1: Capture current sync declarations**

Run:

```bash
rg -n "^var \\(|^type |^func " pkg/controllerv2/sync/sync.go
```

Expected: errors, contracts, server, and client are all in one file before splitting.

- [ ] **Step 2: Move contracts to `contracts.go`**

Move:

```go
type GetStateRequest struct
type GetStateResponse struct
type Endpoint interface
type PeerPicker interface
```

Keep all field names unchanged.

- [ ] **Step 3: Move sentinel errors to `errors.go`**

Move:

```go
var (
	ErrClusterIDMismatch = errors.New("controllerv2/sync: cluster id mismatch")
	ErrStalePayload = errors.New("controllerv2/sync: stale payload")
	ErrHeaderMismatch = errors.New("controllerv2/sync: response header mismatch")
	ErrNoReachablePeer = errors.New("controllerv2/sync: no reachable peer")
)
```

- [ ] **Step 4: Move server code to `server.go`**

Move:

```go
type ServerConfig struct
type Server struct
func NewServer(cfg ServerConfig) *Server
func (s *Server) GetState(ctx context.Context, req GetStateRequest) (GetStateResponse, error)
func callUint64(fn func() uint64) uint64
```

Add this comment above `GetState`:

```go
// GetState serves a canonical full-file payload only when this node is the ready Controller leader.
```

- [ ] **Step 5: Move client code to `client.go`**

Move:

```go
type ClientConfig struct
type Client struct
func NewClient(cfg ClientConfig) *Client
func (c *Client) LeaderID() uint64
func (c *Client) LocalState() (state.ClusterState, bool)
func (c *Client) SyncOnce(ctx context.Context) error
func (c *Client) loadLocal(ctx context.Context) (state.ClusterState, bool, error)
func (c *Client) candidateIDs() []uint64
func (c *Client) installPayload(ctx context.Context, resp GetStateResponse, compareRevision uint64, compareChecksum string) error
func (c *Client) publishSnapshot(st state.ClusterState)
func (c *Client) setLeaderID(leaderID uint64)
func (c *Client) hasLocalSnapshot() bool
```

Add this comment above `SyncOnce`:

```go
// SyncOnce prefers the known leader, follows redirects, and installs the first validated non-stale state file.
```

- [ ] **Step 6: Remove empty `sync.go`**

If `sync.go` contains no declarations after the move, delete it. Keep `doc.go` as the package comment.

- [ ] **Step 7: Run sync tests**

Run:

```bash
go test ./pkg/controllerv2/sync -count=1
```

Expected: tests pass.

## Task 6: Polish Intent Comments And Names

**Files:**
- Modify: `pkg/controllerv2/runtime_*.go`
- Modify: `pkg/controllerv2/fsm/mutation_*.go`
- Modify: `pkg/controllerv2/raft/service_*.go`
- Modify: `pkg/controllerv2/sync/*.go`
- Modify: `pkg/controllerv2/FLOW.md`

- [ ] **Step 1: Add comments only at semantic branch points**

Add comments for these specific cases:
- single-node cluster bootstrap still goes through Raft.
- probe proposal applies an empty normal entry and does not advance logical `Revision`.
- `ExpectedRevision` mismatch may be idempotent for stale bootstrap commands.
- sync `NotModified` is accepted only when local file metadata matches the response header.
- `cluster-state.json` is materialized state; Controller Raft WAL and applied metadata remain authoritative for recovery.

- [ ] **Step 2: Avoid broad renames**

Do not rename exported types, exported methods, JSON fields, command kinds, or public constants. Private names may be changed only when the new name removes ambiguity in the same file.

- [ ] **Step 3: Check generated diff by file**

Run:

```bash
git diff -- pkg/controllerv2
```

Expected: diff is mostly file moves, comments, and small helper extraction. No durable JSON tags, command names, public facade signatures, or test expectations change.

## Task 7: Final Verification

**Files:**
- Test: `pkg/controllerv2/...`
- Test: `pkg/clusterv2/control`

- [ ] **Step 1: Run ControllerV2 tests**

Run:

```bash
go test ./pkg/controllerv2/... -count=1
```

Expected: all tests pass.

- [ ] **Step 2: Run control import-boundary tests**

Run:

```bash
go test ./pkg/clusterv2/control -run TestProductionImportsOnlyControllerV2Facade -count=1
```

Expected: test passes, confirming production integration still imports only the root facade.

- [ ] **Step 3: Review public API surface**

Run:

```bash
git diff -- pkg/controllerv2/types.go pkg/controllerv2/runtime.go
```

Expected: exported type aliases, constants, interfaces, and method signatures are unchanged except documentation comments.

- [ ] **Step 4: Review working tree scope**

Run:

```bash
git status --short
```

Expected: changed files are limited to `pkg/controllerv2` docs/code split files and this plan, unless unrelated pre-existing user changes remain in the working tree.
