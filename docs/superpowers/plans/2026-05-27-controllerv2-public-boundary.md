# ControllerV2 Public Boundary Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `pkg/clusterv2` consume only the root `pkg/controllerv2` facade for ControllerV2 runtime, network, and strongly typed `ClusterState` change events.

**Architecture:** Add a root `pkg/controllerv2` runtime facade that owns internal Raft/FSM/planner/sync wiring and emits `StateEvent{State: ClusterState}`. Keep `pkg/clusterv2/control` as the adapter from `controllerv2.ClusterState` to `control.Snapshot`, and make `clusterv2.Node` apply state changes through explicit node/slot/task/hash-slot domain checks.

**Tech Stack:** Go, etcd raft `raftpb.Message`, existing ControllerV2 subpackages, existing clusterv2 typed RPC transport, `go test`.

---

## File Structure

- Create: `pkg/controllerv2/types.go`
  Root public aliases for strong state, node/slot/task types, state sync request/response types, runtime role/config types, and public errors.
- Create: `pkg/controllerv2/runtime.go`
  Root runtime facade that wraps current ControllerV2 voter and mirror behavior and publishes strongly typed state events.
- Create: `pkg/controllerv2/runtime_test.go`
  Tests for root runtime bootstrap, mirror sync, event payloads, and proposal probe behavior.
- Modify: `pkg/clusterv2/control/controllerv2.go`
  Map root `controllerv2.ClusterState` to `control.Snapshot`.
- Modify: `pkg/clusterv2/control/runtime.go`
  Replace direct ControllerV2 subpackage assembly with a thin adapter around `controllerv2.Runtime`.
- Modify: `pkg/clusterv2/control/codec.go`
  Encode/decode root facade state sync request and response types.
- Modify: `pkg/clusterv2/control/transport.go`
  Use root facade endpoint and peer picker contracts.
- Modify: `pkg/clusterv2/control/*_test.go`
  Update tests to import root `pkg/controllerv2` for production-facing ControllerV2 state and network contracts.
- Create: `pkg/clusterv2/snapshot_changes.go`
  Domain comparison helpers for nodes, slots, tasks, and hash-slot table.
- Modify: `pkg/clusterv2/node.go`
  Use domain comparison helpers before updating discovery, routing, and Slot runtime.
- Modify: `pkg/clusterv2/node_test.go`
  Add tests proving node-only changes do not reconcile slots, slot-only changes reconcile slots, task changes are detected, and hash-slot changes rebuild routing.
- Modify: `pkg/controllerv2/FLOW.md`
  Document that the root package facade is the external boundary.
- Modify: `pkg/clusterv2/FLOW.md`
  Document that clusterv2 listens to ControllerV2 state events and owns domain reconciliation.

## Task 1: Add Root ControllerV2 Public Types

**Files:**
- Create: `pkg/controllerv2/types.go`
- Test: `pkg/controllerv2/runtime_test.go`

- [ ] **Step 1: Write the compile-facing test**

```go
package controllerv2

import "testing"

func TestPublicClusterStateAliasSupportsStrongCompositeLiterals(t *testing.T) {
	st := ClusterState{
		SchemaVersion: CurrentSchemaVersion,
		ClusterID:     "cluster-a",
		Revision:      1,
		Config:        ClusterConfig{SlotCount: 1, HashSlotCount: 4, ReplicaCount: 1},
		Controllers:   []ControllerVoter{{NodeID: 1, Addr: "n1", Role: ControllerRoleVoter}},
		Nodes: []Node{{
			NodeID:         1,
			Addr:           "n1",
			Roles:          []NodeRole{NodeRoleControllerVoter, NodeRoleData},
			JoinState:      NodeJoinStateActive,
			Status:         NodeStatusAlive,
			CapacityWeight: 1,
		}},
		Slots:     []SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1}, ConfigEpoch: 1, PreferredLeader: 1}},
		HashSlots: HashSlotTable{Version: CurrentHashSlotTableVersion, SlotCount: 4, Ranges: []HashSlotRange{{From: 0, To: 3, SlotID: 1}}},
	}
	if err := st.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run: `go test ./pkg/controllerv2`

Expected: fail to compile because the root package does not define `ClusterState`.

- [ ] **Step 3: Add root public aliases**

Create `pkg/controllerv2/types.go` with aliases for `state.ClusterState`, nested state types/constants, sync request/response/endpoint/peer picker contracts, runtime roles/config, voter config, public transport, and public Raft errors.

- [ ] **Step 4: Run the test and verify it passes**

Run: `go test ./pkg/controllerv2`

Expected: pass.

## Task 2: Move ControllerV2 Runtime Facade To Root Package

**Files:**
- Create: `pkg/controllerv2/runtime.go`
- Test: `pkg/controllerv2/runtime_test.go`

- [ ] **Step 1: Write root runtime behavior tests**

Add tests named:

```go
func TestRuntimeSingleVoterBootstrapsStateEvent(t *testing.T) {}
func TestRuntimeMirrorSyncsStateEvent(t *testing.T) {}
func TestRuntimeProbeProposeDoesNotMutateRevision(t *testing.T) {}
```

Each test should assert against `StateEvent.State` and `Runtime.LocalState`, not `clusterv2/control.Snapshot`.

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./pkg/controllerv2`

Expected: fail because `NewRuntime`, `StateEvent`, and `Runtime.LocalState` do not exist in the root package yet.

- [ ] **Step 3: Implement root `Runtime`**

Add `pkg/controllerv2/runtime.go` with:

```go
type Runtime struct {
	cfg RuntimeConfig
	watch chan StateEvent
	// internal fields for statefile store, FSM, Raft service, server facade, and refresh loop
}
```

Port the existing voter, mirror, bootstrap, planner tick, state sync, `Step`, `GetState`, `ProbePropose`, `LeaderID`, and bounded watch publish behavior from `pkg/clusterv2/control/runtime.go`. `publishState` must validate `ClusterState`, store a clone in memory, and emit `StateEvent{State: clone}`.

- [ ] **Step 4: Run root tests**

Run: `go test ./pkg/controllerv2`

Expected: pass.

## Task 3: Switch clusterv2/control To The Root Facade

**Files:**
- Modify: `pkg/clusterv2/control/controllerv2.go`
- Modify: `pkg/clusterv2/control/runtime.go`
- Modify: `pkg/clusterv2/control/codec.go`
- Modify: `pkg/clusterv2/control/transport.go`
- Modify: `pkg/clusterv2/control/*_test.go`

- [ ] **Step 1: Add import-boundary guard**

Add a test or scriptable test helper in `pkg/clusterv2/control` that scans non-test `.go` files and fails if they import:

```text
github.com/WuKongIM/WuKongIM/pkg/controllerv2/command
github.com/WuKongIM/WuKongIM/pkg/controllerv2/fsm
github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft
github.com/WuKongIM/WuKongIM/pkg/controllerv2/server
github.com/WuKongIM/WuKongIM/pkg/controllerv2/state
github.com/WuKongIM/WuKongIM/pkg/controllerv2/statefile
github.com/WuKongIM/WuKongIM/pkg/controllerv2/sync
```

- [ ] **Step 2: Run the boundary test and verify it fails**

Run: `go test ./pkg/clusterv2/control`

Expected: fail because production files currently import ControllerV2 subpackages.

- [ ] **Step 3: Replace production imports**

Change production code so it imports only:

```go
cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
```

Keep `go.etcd.io/raft/v3/raftpb` in transport code for message transport. Update state mapping constants, sync codecs, peer picker, and runtime adapter to use root facade types.

- [ ] **Step 4: Run clusterv2/control tests**

Run: `go test ./pkg/clusterv2/control`

Expected: pass.

## Task 4: Make clusterv2 Snapshot Domain Changes Explicit

**Files:**
- Create: `pkg/clusterv2/snapshot_changes.go`
- Modify: `pkg/clusterv2/node.go`
- Modify: `pkg/clusterv2/node_test.go`

- [ ] **Step 1: Write domain-change tests**

Add tests named:

```go
func TestNodeControlWatchNodeOnlyChangeSkipsSlotReconcile(t *testing.T) {}
func TestNodeControlWatchSlotChangeReconcilesSlots(t *testing.T) {}
func TestControlSnapshotChangesDetectTasks(t *testing.T) {}
func TestControlSnapshotChangesDetectHashSlots(t *testing.T) {}
```

Use `control.NewStaticController` and `recordingReconciler` to verify slot reconcile call counts.

- [ ] **Step 2: Run tests and verify at least the node-only case fails**

Run: `go test ./pkg/clusterv2 -run 'TestNodeControlWatch(NodeOnly|SlotChange)|TestControlSnapshotChanges'`

Expected: node-only case fails because current `applySnapshot` reconciles slots on every revision.

- [ ] **Step 3: Implement domain comparison helpers**

Create `pkg/clusterv2/snapshot_changes.go` with an unexported `snapshotChanges(previous, next control.Snapshot) controlSnapshotChanges` helper. Compare nodes, slots, tasks, and hash-slot table by value after cloning.

- [ ] **Step 4: Gate apply paths by domain**

Update `Node.applySnapshot` so:

- discovery updates only when nodes changed or no prior state is installed.
- routing updates when slots or hash-slot table changed or no route table exists.
- Slot reconcile runs when slots changed or no prior state is installed.
- task changes are detected and stored for future task handlers without adding a task executor in this slice.

- [ ] **Step 5: Run clusterv2 tests**

Run: `go test ./pkg/clusterv2`

Expected: pass.

## Task 5: Update Flow Docs And Verify Package Set

**Files:**
- Modify: `pkg/controllerv2/FLOW.md`
- Modify: `pkg/clusterv2/FLOW.md`

- [ ] **Step 1: Update docs**

Document the new boundary:

- `pkg/controllerv2` root facade is the external API.
- ControllerV2 subpackages are implementation details.
- `pkg/clusterv2` listens for `ClusterState` events and owns node/slot/task/hash-slot reconciliation decisions.

- [ ] **Step 2: Run targeted tests**

Run: `go test ./pkg/controllerv2 ./pkg/clusterv2/control ./pkg/clusterv2`

Expected: pass.

- [ ] **Step 3: Run broader package tests**

Run: `go test ./pkg/controllerv2/... ./pkg/clusterv2/...`

Expected: pass.

## Self-Review

- Spec coverage: Tasks cover root facade, strong `ClusterState` events, clusterv2 import boundary, network request/response contracts, domain-level apply logic, and FLOW documentation.
- Placeholder scan: The plan has no empty implementation slots or unspecified tests.
- Type consistency: The root facade names are consistently `ClusterState`, `StateEvent`, `Runtime`, `RuntimeConfig`, `RuntimeRoleVoter`, `RuntimeRoleMirror`, `Voter`, `GetStateRequest`, and `GetStateResponse`.
