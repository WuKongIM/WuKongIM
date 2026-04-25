# Slot Naming Unification Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename the slot-domain concept so the repository uses `slot` consistently instead of mixing `group` and `slot` for the same replicated partition abstraction.

**Architecture:** Treat this as a hard-cut rename with no compatibility layer. Move `pkg/group` to `pkg/slot` first, then rename the Multi-Raft core API from `Group*` to `Slot*`, and finally propagate the new terminology through metadata, cluster/controller, and app/access consumers while preserving unrelated generic grouping concepts.

**Tech Stack:** Go 1.23, `git mv`, `rg`, `gofmt`, `go test`, existing package/unit/integration tests under `pkg/` and `internal/`.

---

## File Structure Map

- `pkg/slot/multiraft/*` owns the replicated slot runtime and is the canonical source for `SlotID`, `SlotOptions`, runtime status, and transport envelopes.
- `pkg/slot/meta/*` owns slot-local Pebble metadata storage and snapshots.
- `pkg/slot/fsm/*` owns metadata command encoding and Multi-Raft state machine application for a slot.
- `pkg/slot/proxy/*` owns distributed metadata RPC/store orchestration on top of `pkg/cluster`.
- `pkg/controller/meta/*` and `pkg/controller/plane/*` own controller-side slot assignment/runtime-view/task state and planning.
- `pkg/cluster/*` owns slot routing, slot runtime lifecycle, forwarding, and controller reconciliation.
- `internal/runtime/online/*`, `internal/usecase/presence/*`, `internal/access/node/*`, and `internal/app/*` consume the slot-domain APIs and currently leak `GroupID` naming into higher layers.

### Task 1: Move `pkg/group` to `pkg/slot` and retarget imports

**Files:**
- Move: `pkg/group/fsm/command.go`, `pkg/group/fsm/statemachine.go`, `pkg/group/meta/db.go`, `pkg/group/multiraft/api.go`, `pkg/group/proxy/store.go` and the rest of the `pkg/group/*` tree
- Modify: `internal/app/build.go`, `internal/app/app.go`, `internal/access/node/options.go`, `internal/usecase/conversation/app.go`, `internal/usecase/conversation/deps.go`, `internal/usecase/conversation/projector.go`, `internal/usecase/message/app.go`, `pkg/cluster/cluster.go`, `pkg/raftlog/pebble_benchmark_test.go`
- Test: `go test ./pkg/slot/... ./internal/app ./internal/usecase/conversation ./internal/usecase/message`

- [ ] **Step 1: Create the red state by rewriting imports to the target path in a small caller set**

```bash
perl -0pi -e 's#pkg/group/#pkg/slot/#g' internal/app/build.go internal/app/app.go internal/access/node/options.go internal/usecase/conversation/app.go internal/usecase/conversation/deps.go internal/usecase/conversation/projector.go internal/usecase/message/app.go pkg/cluster/cluster.go pkg/raftlog/pebble_benchmark_test.go
```

- [ ] **Step 2: Run a focused test command to confirm the repository still needs the package move**

Run: `go test ./internal/app ./internal/usecase/conversation ./internal/usecase/message`
Expected: FAIL with missing `pkg/slot/...` packages or unresolved imports.

- [ ] **Step 3: Move the package root and normalize imports**

```bash
git mv pkg/group pkg/slot
rg -l 'pkg/group/' pkg internal cmd | xargs sed -i '' 's#pkg/group/#pkg/slot/#g'
```

- [ ] **Step 4: Run `gofmt` on the moved packages and touched importers**

Run: `gofmt -w pkg/slot internal/app internal/access/node internal/usecase/conversation internal/usecase/message pkg/cluster pkg/raftlog`
Expected: no output.

- [ ] **Step 5: Run the focused package tests again**

Run: `go test ./pkg/slot/... ./internal/app ./internal/usecase/conversation ./internal/usecase/message`
Expected: compile failures now come from old `Group*` symbol names rather than missing package paths.

- [ ] **Step 6: Commit the package-root move**

```bash
git add pkg/slot internal/app internal/access/node internal/usecase/conversation internal/usecase/message pkg/cluster pkg/raftlog
git commit -m "refactor: move group packages under slot"
```

### Task 2: Rename the `multiraft` public API and internal runtime from `group` to `slot`

**Files:**
- Modify: `pkg/slot/multiraft/types.go`, `pkg/slot/multiraft/errors.go`, `pkg/slot/multiraft/api.go`, `pkg/slot/multiraft/runtime.go`, `pkg/slot/multiraft/scheduler.go`, `pkg/slot/multiraft/ready.go`, `pkg/slot/multiraft/storage_adapter.go`
- Rename: `pkg/slot/multiraft/group.go` -> `pkg/slot/multiraft/slot.go`
- Test: `pkg/slot/multiraft/runtime_test.go`, `pkg/slot/multiraft/step_test.go`, `pkg/slot/multiraft/control_test.go`, `pkg/slot/multiraft/e2e_test.go`, `pkg/slot/multiraft/benchmark_test.go`, `pkg/slot/multiraft/example_test.go`, `pkg/slot/multiraft/testenv_test.go`

- [ ] **Step 1: Update one focused test file to the target API names first**

Change `pkg/slot/multiraft/runtime_test.go` and `pkg/slot/multiraft/step_test.go` to use `SlotID`, `SlotOptions`, `BootstrapSlotRequest`, `ErrSlotExists`, and `ErrSlotNotFound`.

- [ ] **Step 2: Run the focused Multi-Raft tests to capture the red state**

Run: `go test ./pkg/slot/multiraft -run 'TestRuntime|TestStep'`
Expected: FAIL because `SlotID`, `SlotOptions`, and the new runtime methods do not exist yet.

- [ ] **Step 3: Implement the core type and field rename in `types.go` and `errors.go`**

Target shape:

```go
type SlotID uint64

type SlotOptions struct {
    ID           SlotID
    Storage      Storage
    StateMachine StateMachine
}

type BootstrapSlotRequest struct {
    Slot   SlotOptions
    Voters []NodeID
}

type Envelope struct {
    SlotID  SlotID
    Message raftpb.Message
}
```

Also rename `ErrGroupExists`, `ErrGroupNotFound`, and `ErrGroupClosed` to `ErrSlotExists`, `ErrSlotNotFound`, and `ErrSlotClosed`.

- [ ] **Step 4: Rename runtime internals to `slot` terminology**

Apply the same rename through `api.go`, `runtime.go`, `scheduler.go`, `ready.go`, and `slot.go`:
- `OpenGroup` -> `OpenSlot`
- `BootstrapGroup` -> `BootstrapSlot`
- `CloseGroup` -> `CloseSlot`
- `Groups` -> `Slots`
- `processGroup` -> `processSlot`
- `groups map[GroupID]*group` -> `slots map[SlotID]*slot`

- [ ] **Step 5: Rename the concept-bearing file and any helper constructors**

Run: `git mv pkg/slot/multiraft/group.go pkg/slot/multiraft/slot.go`
Then rename helpers such as `newGroup`, `validateGroupOptions`, `groupRequestCount`, and `newInternalGroupOptions` to their `slot` equivalents.

- [ ] **Step 6: Run `gofmt` and the full Multi-Raft package tests**

Run: `gofmt -w pkg/slot/multiraft`
Run: `go test ./pkg/slot/multiraft`
Expected: PASS.

- [ ] **Step 7: Commit the Multi-Raft rename**

```bash
git add pkg/slot/multiraft
git commit -m "refactor: rename multiraft group api to slot"
```

### Task 3: Align `meta`, `fsm`, and `proxy` with the new slot-domain API

**Files:**
- Modify: `pkg/slot/fsm/command.go`, `pkg/slot/fsm/statemachine.go`, `pkg/slot/fsm/testutil_test.go`, `pkg/slot/fsm/state_machine_test.go`, `pkg/slot/fsm/fsm_stress_test.go`
- Modify: `pkg/slot/meta/db.go`, `pkg/slot/meta/shard.go`, `pkg/slot/meta/snapshot.go`, `pkg/slot/meta/stress_test.go`, `pkg/slot/meta/*_test.go`
- Modify: `pkg/slot/proxy/store.go`, `pkg/slot/proxy/runtime_meta_rpc.go`, `pkg/slot/proxy/user_conversation_state_rpc.go`, `pkg/slot/proxy/channel_update_log_rpc.go`, `pkg/slot/proxy/subscriber_rpc.go`, `pkg/slot/proxy/integration_test.go`, `pkg/slot/proxy/testutil_test.go`
- Test: `go test ./pkg/slot/fsm ./pkg/slot/meta ./pkg/slot/proxy`

- [ ] **Step 1: Update the FSM tests to the new command field names**

Rewrite `pkg/slot/fsm/state_machine_test.go` so all `multiraft.Command{GroupID: ...}` cases become `multiraft.Command{SlotID: ...}` and mismatched test names become `...SlotID`.

- [ ] **Step 2: Run the package tests to confirm the red state at the boundary**

Run: `go test ./pkg/slot/fsm ./pkg/slot/proxy`
Expected: FAIL with unresolved `GroupID` or `BootstrapGroupRequest` references.

- [ ] **Step 3: Update FSM constructors and state machine checks**

Apply these renames in `pkg/slot/fsm/statemachine.go`:

```go
func NewStateMachineFactory(db *metadb.DB) func(slotID multiraft.SlotID) (multiraft.StateMachine, error) {
    return func(slotID multiraft.SlotID) (multiraft.StateMachine, error) {
        return NewStateMachine(db, uint64(slotID))
    }
}
```

And make the runtime check compare `cmd.SlotID` with `multiraft.SlotID(m.slot)`.

- [ ] **Step 4: Propagate `SlotID` through proxy request/response types and helpers**

Rename slot-domain fields and parameters in `pkg/slot/proxy/*.go`, for example:
- request JSON field `group_id` -> `slot_id`
- helper parameters `groupID multiraft.GroupID` -> `slotID multiraft.SlotID`
- map builders like `groupUserConversationActivePatchesByGroup` -> `groupUserConversationActivePatchesBySlot`

- [ ] **Step 5: Keep `meta` behavior unchanged but sweep imports, fixtures, and comments**

Use `rg -n '\bgroup\b|GroupID' pkg/slot/meta pkg/slot/fsm pkg/slot/proxy` to review remaining slot-domain mentions and rename only the ones that still refer to the slot abstraction.

- [ ] **Step 6: Run `gofmt` and the package tests**

Run: `gofmt -w pkg/slot/fsm pkg/slot/meta pkg/slot/proxy`
Run: `go test ./pkg/slot/fsm ./pkg/slot/meta ./pkg/slot/proxy`
Expected: PASS.

- [ ] **Step 7: Commit the slot storage/FSM/proxy alignment**

```bash
git add pkg/slot/fsm pkg/slot/meta pkg/slot/proxy
git commit -m "refactor: align slot metadata packages with slot api"
```

### Task 4: Rename slot-domain terminology through `pkg/controller` and `pkg/cluster`

**Files:**
- Modify: `pkg/controller/meta/types.go`, `pkg/controller/meta/store.go`, `pkg/controller/meta/codec.go`, `pkg/controller/meta/snapshot.go`, `pkg/controller/meta/store_test.go`
- Modify: `pkg/controller/plane/types.go`, `pkg/controller/plane/commands.go`, `pkg/controller/plane/planner.go`, `pkg/controller/plane/controller.go`, `pkg/controller/plane/statemachine.go`, `pkg/controller/plane/controller_test.go`
- Modify: `pkg/controller/raft/service.go`, `pkg/controller/raft/service_test.go`
- Modify: `pkg/cluster/config.go`, `pkg/cluster/router.go`, `pkg/cluster/cluster.go`, `pkg/cluster/agent.go`, `pkg/cluster/managed_groups.go`, `pkg/cluster/operator.go`, `pkg/cluster/readiness.go`, `pkg/cluster/codec.go`, `pkg/cluster/*_test.go`
- Test: `go test ./pkg/controller/... ./pkg/cluster/...`

- [ ] **Step 1: Update one controller and one cluster test file to the target terminology first**

Change `pkg/controller/plane/controller_test.go` and `pkg/cluster/config_test.go` to use `SlotAssignment`, `SlotRuntimeView`, `SlotCount`, `SlotReplicaN`, and `multiraft.SlotID`.

- [ ] **Step 2: Run the controller/cluster tests to capture the red state**

Run: `go test ./pkg/controller/... ./pkg/cluster/...`
Expected: FAIL with missing `SlotAssignment`, `SlotCount`, or `multiraft.SlotID` references.

- [ ] **Step 3: Rename controller metadata records from `Group*` to `Slot*`**

Target shape in `pkg/controller/meta/types.go`:

```go
type SlotAssignment struct {
    SlotID         uint32
    DesiredPeers   []uint64
    ConfigEpoch    uint64
    BalanceVersion uint64
}

type SlotRuntimeView struct {
    SlotID              uint32
    CurrentPeers        []uint64
    LeaderID            uint64
    HealthyVoters       uint32
    HasQuorum           bool
    ObservedConfigEpoch uint64
    LastReportAt        time.Time
}
```

Then rename `GetAssignment` -> `GetSlotAssignment`, `ListAssignments` -> `ListSlotAssignments`, and related codec/snapshot helpers.

- [ ] **Step 4: Rename planner/controller terminology to `slot`**

In `pkg/controller/plane/*`, apply `GroupCount` -> `SlotCount`, `ReconcileGroup` -> `ReconcileSlot`, `Decision.GroupID` -> `Decision.SlotID`, and adjust all planner-state maps to key by `SlotID`/`SlotAssignment`.

- [ ] **Step 5: Rename cluster configuration and runtime methods to `slot`**

In `pkg/cluster/*`, apply these changes consistently:
- `GroupCount` -> `SlotCount`
- `GroupReplicaN` -> `SlotReplicaN`
- `GroupConfig` -> `SlotConfig`
- `runtimePeers map[multiraft.GroupID]...` -> `runtimePeers map[multiraft.SlotID]...`
- `managedGroup*` RPC structs/helpers -> `managedSlot*`
- `ErrGroupNotFound` in the slot-control path -> `ErrSlotNotFound` where it refers to slot management

- [ ] **Step 6: Keep routing method names coherent**

Do not rename `Router.SlotForKey`, but do rename its return type and neighboring APIs so it returns `multiraft.SlotID`, `LeaderOf(slotID multiraft.SlotID)`, `PeersForSlot(slotID multiraft.SlotID)`, and related request payload fields named `slot_id`.

- [ ] **Step 7: Run `gofmt` and the controller/cluster tests**

Run: `gofmt -w pkg/controller pkg/cluster`
Run: `go test ./pkg/controller/... ./pkg/cluster/...`
Expected: PASS.

- [ ] **Step 8: Commit the control-plane slot rename**

```bash
git add pkg/controller pkg/cluster
git commit -m "refactor: rename control plane group terminology to slot"
```

### Task 5: Propagate `slot` terminology through app, access, presence, and online runtime consumers

**Files:**
- Modify: `internal/runtime/online/types.go`, `internal/runtime/online/registry.go`, `internal/runtime/online/registry_test.go`
- Modify: `internal/usecase/presence/types.go`, `internal/usecase/presence/app.go`
- Modify: `internal/access/node/options.go`, `internal/access/node/client.go`, `internal/access/node/presence_rpc.go`, `internal/access/node/presence_rpc_test.go`, `internal/access/node/test_helpers_test.go`
- Modify: `internal/app/build.go`, `internal/app/config.go`, `internal/app/presenceauthority.go`, `internal/app/multinode_integration_test.go`, `internal/app/*_test.go`
- Modify: `internal/usecase/conversation/*.go`, `internal/usecase/message/*.go` only where slot-domain imports or names changed
- Test: `go test ./internal/runtime/online ./internal/usecase/presence ./internal/access/node ./internal/app ./internal/usecase/conversation ./internal/usecase/message`

- [ ] **Step 1: Update the online/presence tests to the target field names first**

Change `internal/runtime/online/registry_test.go` and `internal/access/node/presence_rpc_test.go` so slot-domain fields become `SlotID`, `SlotSnapshot`, `ActiveConnectionsBySlot`, `ActiveSlots`, and RPC request field `slot_id`.

- [ ] **Step 2: Run the consumer tests to capture the red state**

Run: `go test ./internal/runtime/online ./internal/access/node ./internal/app`
Expected: FAIL with unresolved `GroupID`, `PeersForGroup`, or `multiraft.GroupID` references.

- [ ] **Step 3: Rename slot-domain runtime fields and interfaces**

Apply the slot rename through these packages:
- `internal/runtime/online.Conn.GroupID` -> `SlotID`
- `internal/runtime/online.GroupSnapshot` -> `SlotSnapshot`
- `ActiveConnectionsByGroup` -> `ActiveConnectionsBySlot`
- `ActiveGroups` -> `ActiveSlots`
- `internal/usecase/presence.GatewayLease.GroupID` -> `SlotID`
- `RegisterAuthoritativeCommand.GroupID` -> `SlotID`

- [ ] **Step 4: Update node-access interfaces and RPC payloads**

In `internal/access/node/options.go`, `client.go`, and `presence_rpc.go`, rename slot-domain method signatures and payloads:
- `LeaderOf(slotID multiraft.SlotID)`
- `RPCService(..., slotID multiraft.SlotID, ...)`
- `PeersForSlot(slotID multiraft.SlotID)`
- JSON field/tag `group_id` -> `slot_id`

- [ ] **Step 5: Update app config and orchestration names where they refer to slots**

In `internal/app/config.go` and `internal/app/build.go`, rename `Cluster.GroupCount`/`GroupReplicaN`/`GroupConfig` to `SlotCount`/`SlotReplicaN`/`SlotConfig`, and update factory signatures like `newStorageFactory` to accept `slotID multiraft.SlotID`.

- [ ] **Step 6: Run `gofmt` and the affected internal package tests**

Run: `gofmt -w internal/runtime/online internal/usecase/presence internal/access/node internal/app internal/usecase/conversation internal/usecase/message`
Run: `go test ./internal/runtime/online ./internal/usecase/presence ./internal/access/node ./internal/app ./internal/usecase/conversation ./internal/usecase/message`
Expected: PASS.

- [ ] **Step 7: Commit the consumer rename**

```bash
git add internal/runtime/online internal/usecase/presence internal/access/node internal/app internal/usecase/conversation internal/usecase/message
git commit -m "refactor: propagate slot terminology through app and access layers"
```

### Task 6: Sweep residual terminology, rename remaining concept-bearing files, and verify the repository

**Files:**
- Rename if still present: any remaining slot-domain files named `group*.go` under `pkg/slot`, `pkg/cluster`, `pkg/controller`, `internal/runtime/online`, `internal/access/node`, `internal/app`
- Modify: touched test files and comments across `pkg/slot`, `pkg/controller`, `pkg/cluster`, `internal/`
- Verify: whole repository

- [ ] **Step 1: Run the naming sweep queries and record the remaining matches**

Run:

```bash
rg -n 'pkg/group|\bGroupID\b|\bGroupOptions\b|\bBootstrapGroupRequest\b|\bErrGroup(Exists|NotFound|Closed)\b' pkg internal cmd
rg -n '\bgroup\b' pkg/slot pkg/controller pkg/cluster internal/runtime/online internal/usecase/presence internal/access/node internal/app
```

Expected: the first query returns no results; the second query returns only non-slot generic grouping usages or fixture strings explicitly chosen to keep.

- [ ] **Step 2: Clean up the remaining slot-domain names in tests, comments, and benchmark text**

Rename slot-domain test names like `TestStateMachineRejectsMismatchedGroupID` to `TestStateMachineRejectsMismatchedSlotID`, update comments such as `SlotForKey maps a key to a raft group` to `raft slot`, and rename any remaining slot-domain helper variables from `groupID` to `slotID`.

- [ ] **Step 3: Run the focused regression suite**

Run: `go test ./pkg/slot/... ./pkg/controller/... ./pkg/cluster/... ./internal/runtime/online ./internal/usecase/presence ./internal/access/node ./internal/app ./internal/usecase/conversation ./internal/usecase/message`
Expected: PASS.

- [ ] **Step 4: Run the full repository test suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Review the final diff for accidental over-renames**

Run: `git diff --stat HEAD~5..HEAD` and `git diff`
Expected: slot-domain code is renamed, but unrelated gateway/listener/runtime grouping names remain intact.

- [ ] **Step 6: Commit the sweep and verification pass**

```bash
git add -A
git commit -m "refactor: complete slot naming unification"
```
