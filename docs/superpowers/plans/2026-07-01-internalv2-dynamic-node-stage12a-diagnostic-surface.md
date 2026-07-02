# InternalV2 Dynamic Node Stage 12A Diagnostic Surface Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add active task proof fields plus a read-only dynamic-node diagnostic manager route and `wkcli node diagnose` command that explain one node's lifecycle blockers, related Controller tasks, retained audit summaries, and Slot evidence.

**Architecture:** Build the diagnostic read model in `internalv2/usecase/management` from existing narrow read ports and projections. Expose it through `internalv2/access/manager` as `GET /manager/nodes/:node_id/diagnostics`. Keep `cmd/wkcli/internal/nodeops` as a manager-only HTTP client that formats the diagnostic response without importing cluster internals.

**Tech Stack:** Go, Gin manager HTTP, `internalv2/usecase/management`, ControllerV2 control snapshot projection, task audit query port, `cmd/wkcli`, Cobra, `httptest`.

---

## Prerequisites

Read before editing:

- `docs/superpowers/specs/2026-07-01-internalv2-dynamic-node-stage12-diagnostics-design.md`
- `internalv2/usecase/management/FLOW.md`
- `internalv2/access/manager/FLOW.md`
- `cmd/wkcli/internal/nodeops/client.go`
- `cmd/wkcli/internal/nodeops/command.go`
- `internalv2/usecase/management/controller_tasks.go`
- `internalv2/usecase/management/task_audit.go`
- `internalv2/usecase/management/scale_in.go`
- `internalv2/usecase/management/slot_onboarding.go`
- `internalv2/usecase/management/slots.go`

## Files

- Create: `internalv2/usecase/management/dynamic_node_diagnostics.go`
- Create: `internalv2/usecase/management/dynamic_node_diagnostics_test.go`
- Modify: `internalv2/usecase/management/slots.go`
- Modify: `internalv2/usecase/management/slots_test.go`
- Modify: `internalv2/usecase/management/controller_tasks.go`
- Modify: `internalv2/usecase/management/controller_tasks_test.go`
- Create: `internalv2/access/manager/dynamic_node_diagnostics.go`
- Create: `internalv2/access/manager/dynamic_node_diagnostics_test.go`
- Modify: `internalv2/access/manager/slots.go`
- Modify: `internalv2/access/manager/slots_test.go`
- Modify: `internalv2/access/manager/controller_tasks.go`
- Modify: `internalv2/access/manager/controller_tasks_test.go`
- Modify: `internalv2/access/manager/server.go`
- Modify: `cmd/wkcli/internal/nodeops/client.go`
- Modify: `cmd/wkcli/internal/nodeops/client_test.go`
- Modify: `cmd/wkcli/internal/nodeops/command.go`
- Modify: `cmd/wkcli/internal/nodeops/command_test.go`
- Modify: `cmd/wkcli/README.md`
- Modify: `internalv2/usecase/management/FLOW.md`
- Modify: `internalv2/access/manager/FLOW.md`
- Modify: `docs/superpowers/runbooks/internalv2-dynamic-node-operations.md`

## API Contract

Manager route:

```text
GET /manager/nodes/:node_id/diagnostics
```

Query parameters:

```text
task_limit=1..50, default 20
audit_limit=1..20, default 10
slot_limit=1..256, default 256
```

Response shape:

```json
{
  "generated_at": "2026-07-01T00:00:00Z",
  "state_revision": 88,
  "node_id": 4,
  "node": {},
  "scale_in": {},
  "onboarding": {},
  "active_tasks": [{
    "task_id": "slot-7-replica-move-4-to-2",
    "kind": "slot_replica_move",
    "step": "remove_voter",
    "phase_index": 3,
    "observed_config_index": 101,
    "observed_voters": [1, 2, 4],
    "observed_learners": []
  }],
  "task_audits": [],
  "slots": [],
  "summary": {
    "safe_to_remove": false,
    "blocked_reasons": ["blocked_by_tasks"],
    "active_task_count": 1,
    "failed_task_count": 0,
    "slot_replica_count": 1,
    "slot_leader_count": 0,
    "control_revision_gap": 8,
    "slot_replica_move_state": "waiting_leader_transfer",
    "oldest_task_age_seconds": 12,
    "audit_available": true,
    "runtime_unknown": false,
    "slot_runtime_unknown": false,
    "recommended_next_action": "inspect_controller_task"
  },
  "sources": {
    "control_snapshot": {"available": true},
    "task_audit": {"available": true},
    "slot_runtime": {"available": true}
  },
  "warnings": []
}
```

`recommended_next_action` must be one of:

```text
inspect_controller_task
wait_control_revision
advance_slot_drain
inspect_slot_runtime
inspect_runtime
inspect_channel_inventory
ready_to_remove
no_action
```

`slot_replica_move_state` must be one of:

```text
waiting_learner_catchup
waiting_leader_transfer
phase_observation_missing
task_failed
no_active_move
unknown
```

## Task 1: Preserve Active Task Proof Fields

**Files:**
- Modify: `internalv2/usecase/management/slots.go`
- Modify: `internalv2/usecase/management/slots_test.go`
- Modify: `internalv2/usecase/management/controller_tasks.go`
- Modify: `internalv2/usecase/management/controller_tasks_test.go`
- Modify: `internalv2/access/manager/slots.go`
- Modify: `internalv2/access/manager/slots_test.go`
- Modify: `internalv2/access/manager/controller_tasks.go`
- Modify: `internalv2/access/manager/controller_tasks_test.go`

- [ ] **Step 1: Write failing usecase tests for proof fields**

Extend existing Slot/Controller task projection tests to assert that an active
`slot_replica_move` task preserves:

```go
require.Equal(t, uint32(3), task.PhaseIndex)
require.Equal(t, uint64(101), task.ObservedConfigIndex)
require.Equal(t, []uint64{1, 2, 4}, task.ObservedVoters)
require.Equal(t, []uint64{2}, task.ObservedLearners)
```

Mutate the returned slices and read the task again to prove `ObservedVoters`
and `ObservedLearners` are cloned.

- [ ] **Step 2: Run RED usecase tests**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'ControllerTaskReturnsDetail|ListSlots.*Task|SlotTask' -count=1
```

Expected: fail because `PhaseIndex`, `ObservedConfigIndex`,
`ObservedVoters`, and `ObservedLearners` do not exist in manager-facing task
DTOs.

- [ ] **Step 3: Add proof fields to usecase DTOs**

Add to `management.SlotTask` and `management.ControllerTask`:

```go
// PhaseIndex advances after each observed Slot Raft config phase.
PhaseIndex uint32
// ObservedConfigIndex is the Slot Raft config index observed for the current phase.
ObservedConfigIndex uint64
// ObservedVoters is the voter set observed for the current phase.
ObservedVoters []uint64
// ObservedLearners is the learner set observed for the current phase.
ObservedLearners []uint64
```

Update `slotTaskFromControl` and `controllerTaskFromControl` to clone the
observed slices.

- [ ] **Step 4: Write failing manager JSON tests**

Extend manager task and slot tests to assert JSON fields:

```json
"phase_index": 3
"observed_config_index": 101
"observed_voters": [1,2,4]
"observed_learners": [2]
```

- [ ] **Step 5: Add manager DTO fields**

Add the same fields to:

- `manager.SlotTaskDTO`
- `manager.ManagerControllerTask`

Use JSON keys:

```text
phase_index
observed_config_index
observed_voters
observed_learners
```

- [ ] **Step 6: Run GREEN proof field tests**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'ControllerTaskReturnsDetail|ListSlots.*Task|SlotTask' -count=1
GOWORK=off go test ./internalv2/access/manager -run 'ControllerTask|Slots' -count=1
```

Expected: PASS.

## Task 2: Add Management Diagnostic Usecase

**Files:**
- Create: `internalv2/usecase/management/dynamic_node_diagnostics_test.go`
- Create: `internalv2/usecase/management/dynamic_node_diagnostics.go`

- [ ] **Step 1: Write failing usecase tests**

Add tests that construct `management.App` with existing fake snapshot readers and fake task audit readers. Cover:

```go
func TestDynamicNodeDiagnosticsCombinesScaleInTasksAuditsAndSlots(t *testing.T)
func TestDynamicNodeDiagnosticsTreatsAuditUnavailableAsWarning(t *testing.T)
func TestDynamicNodeDiagnosticsReturnsNotFoundForMissingNode(t *testing.T)
func TestDynamicNodeDiagnosticsBoundsLimits(t *testing.T)
func TestDynamicNodeDiagnosticsRecommendedActions(t *testing.T)
```

The first test must assert these exact facts:

```go
resp, err := app.DynamicNodeDiagnostics(context.Background(), DynamicNodeDiagnosticsRequest{
	NodeID:     4,
	TaskLimit: 20,
	AuditLimit: 10,
	SlotLimit:  256,
})
require.NoError(t, err)
require.Equal(t, uint64(4), resp.NodeID)
require.Equal(t, uint64(88), resp.StateRevision)
require.Equal(t, false, resp.Summary.SafeToRemove)
require.Equal(t, 1, resp.Summary.ActiveTaskCount)
require.Equal(t, 0, resp.Summary.FailedTaskCount)
require.Equal(t, "waiting_leader_transfer", resp.Summary.SlotReplicaMoveState)
require.Equal(t, "inspect_controller_task", resp.Summary.RecommendedNextAction)
require.Len(t, resp.ActiveTasks, 1)
require.Equal(t, "slot_replica_move", resp.ActiveTasks[0].Kind)
require.Equal(t, "remove_voter", resp.ActiveTasks[0].Step)
require.Equal(t, uint32(3), resp.ActiveTasks[0].PhaseIndex)
require.Equal(t, uint64(101), resp.ActiveTasks[0].ObservedConfigIndex)
require.Equal(t, []uint64{1, 2, 4}, resp.ActiveTasks[0].ObservedVoters)
require.Len(t, resp.TaskAudits, 1)
require.Equal(t, "waiting for source leadership transfer", resp.TaskAudits[0].LastReason)
require.Len(t, resp.Slots, 1)
require.Equal(t, uint32(7), resp.Slots[0].SlotID)
```

- [ ] **Step 2: Run RED test**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management -run DynamicNodeDiagnostics -count=1
```

Expected: compile fails because the diagnostic types and method do not exist.

- [ ] **Step 3: Add diagnostic DTOs and method**

Create `internalv2/usecase/management/dynamic_node_diagnostics.go` with:

```go
const (
	DefaultDynamicNodeDiagnosticTaskLimit  = 20
	MaxDynamicNodeDiagnosticTaskLimit      = 50
	DefaultDynamicNodeDiagnosticAuditLimit = 10
	MaxDynamicNodeDiagnosticAuditLimit     = 20
	DefaultDynamicNodeDiagnosticSlotLimit  = 256
	MaxDynamicNodeDiagnosticSlotLimit      = 256
)

var ErrDynamicNodeDiagnosticsNotFound = errors.New("internalv2/usecase/management: dynamic node diagnostics not found")

type DynamicNodeDiagnosticsRequest struct {
	NodeID     uint64
	TaskLimit int
	AuditLimit int
	SlotLimit  int
}

type DynamicNodeDiagnosticsResponse struct {
	GeneratedAt   time.Time
	StateRevision uint64
	NodeID        uint64
	Node          Node
	ScaleIn      *NodeScaleInStatusResponse
	Onboarding   *NodeOnboardingStatusResponse
	ActiveTasks  []ControllerTask
	TaskAudits   []ControllerTaskAuditSnapshot
	Slots        []DynamicNodeDiagnosticSlot
	Summary      DynamicNodeDiagnosticSummary
	Sources      DynamicNodeDiagnosticSources
	Warnings     []string
}
```

Define `DynamicNodeDiagnosticSlot`, `DynamicNodeDiagnosticSummary`,
`DynamicNodeDiagnosticSources`, and `DynamicNodeDiagnosticSource` in the same
file. All exported types and fields need English comments.

`DynamicNodeDiagnostics` must:

- validate node ID and limits;
- read one local control snapshot;
- find the target node;
- derive node list projection for the target;
- include `NodeScaleInStatus` for `leaving` nodes;
- include `NodeOnboardingStatus` for active data nodes;
- include active Controller tasks filtered by node;
- include retained task audit summaries filtered by node when audit is wired;
- include related Slot rows from the same control snapshot;
- compute summary from the included evidence;
- return audit warnings without failing the whole route.

- [ ] **Step 4: Run GREEN usecase tests**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management -run DynamicNodeDiagnostics -count=1
```

Expected: PASS.

## Task 3: Add Manager Route

**Files:**
- Create: `internalv2/access/manager/dynamic_node_diagnostics_test.go`
- Create: `internalv2/access/manager/dynamic_node_diagnostics.go`
- Modify: `internalv2/access/manager/server.go`

- [ ] **Step 1: Write failing manager route tests**

Add tests:

```go
func TestManagerDynamicNodeDiagnosticsRouteReturnsEvidence(t *testing.T)
func TestManagerDynamicNodeDiagnosticsRouteValidatesLimits(t *testing.T)
func TestManagerDynamicNodeDiagnosticsRouteMapsNotFound(t *testing.T)
func TestManagerDynamicNodeDiagnosticsRouteRequiresNodeReadPermission(t *testing.T)
```

The success test must request:

```text
GET /manager/nodes/4/diagnostics?task_limit=20&audit_limit=10&slot_limit=256
```

and assert:

```json
"node_id": 4
"summary": {"recommended_next_action": "inspect_controller_task"}
"sources": {"task_audit": {"available": true}}
```

- [ ] **Step 2: Run RED manager tests**

Run:

```bash
GOWORK=off go test ./internalv2/access/manager -run DynamicNodeDiagnostics -count=1
```

Expected: fail because the route does not exist.

- [ ] **Step 3: Add DTO mapping and handler**

Create `internalv2/access/manager/dynamic_node_diagnostics.go` with:

```go
func (s *Server) handleDynamicNodeDiagnostics(c *gin.Context)
func parseDynamicNodeDiagnosticsRequest(c *gin.Context) (managementusecase.DynamicNodeDiagnosticsRequest, error)
func dynamicNodeDiagnosticsDTO(resp managementusecase.DynamicNodeDiagnosticsResponse) ManagerDynamicNodeDiagnosticsResponse
func writeDynamicNodeDiagnosticsError(c *gin.Context, err error)
```

Register the route in `server.go` under node read routes:

```go
nodeReads.GET("/nodes/:node_id/diagnostics", s.handleDynamicNodeDiagnostics)
```

Use the same permission as `GET /manager/nodes`: `cluster.node:r`.

- [ ] **Step 4: Run GREEN manager tests**

Run:

```bash
GOWORK=off go test ./internalv2/access/manager -run 'DynamicNodeDiagnostics|Route|Permission' -count=1
```

Expected: PASS.

## Task 4: Add `wkcli node diagnose`

**Files:**
- Modify: `cmd/wkcli/internal/nodeops/client.go`
- Modify: `cmd/wkcli/internal/nodeops/client_test.go`
- Modify: `cmd/wkcli/internal/nodeops/command.go`
- Modify: `cmd/wkcli/internal/nodeops/command_test.go`
- Modify: `cmd/wkcli/README.md`

- [ ] **Step 1: Write failing client and command tests**

Add client test:

```go
func TestClientDynamicNodeDiagnosticsCallsManagerRoute(t *testing.T)
```

Assert method/path:

```text
GET /manager/nodes/4/diagnostics?audit_limit=10&slot_limit=256&task_limit=20
```

Add command tests:

```go
func TestNodeDiagnoseCommandPrintsRootCauseSummary(t *testing.T)
func TestNodeDiagnoseCommandRendersJSON(t *testing.T)
func TestNodeDiagnoseCommandRejectsInvalidLimits(t *testing.T)
```

The human output test must require these substrings:

```text
node=4
join_state=leaving
safe_to_remove=false
recommended_next_action=inspect_controller_task
active_tasks=1
task=slot-7-replica-move-4-to-2 kind=slot_replica_move step=remove_voter status=running phase_index=3 observed_config_index=101 observed_voters=[1 2 4]
audit=slot-7-replica-move-4-to-2 last_reason=waiting for source leadership transfer
slot=7 desired_peers=[1 2 4] preferred_leader=1
```

- [ ] **Step 2: Run RED wkcli tests**

Run:

```bash
GOWORK=off go test ./cmd/wkcli/internal/nodeops -run Diagnose -count=1
```

Expected: fail because the client and command do not exist.

- [ ] **Step 3: Add client method**

Add to `cmd/wkcli/internal/nodeops/client.go`:

```go
type DiagnosticRequest struct {
	TaskLimit  int
	AuditLimit int
	SlotLimit  int
}

func (c *Client) DynamicNodeDiagnostics(ctx context.Context, nodeID uint64, req DiagnosticRequest, out any) error
```

The method must build query parameters in deterministic key order:

```text
audit_limit
slot_limit
task_limit
```

- [ ] **Step 4: Add Cobra command**

Add `diagnose NODE_ID` under `node` with flags:

```text
--task-limit 20
--audit-limit 10
--slot-limit 256
--json
```

Human output prints one summary line, then active task lines, audit lines, slot
lines, and warning lines. Missing optional sections print counts as zero and do
not invent field values.

- [ ] **Step 5: Run GREEN wkcli tests**

Run:

```bash
GOWORK=off go test ./cmd/wkcli/internal/nodeops -run 'Diagnose|ImportBoundary' -count=1
GOWORK=off go test ./cmd/wkcli ./cmd/wkcli/internal/... -count=1
```

Expected: PASS and import boundary still proves `nodeops` is manager-only.

## Task 5: Update Docs And FLOW

**Files:**
- Modify: `internalv2/usecase/management/FLOW.md`
- Modify: `internalv2/access/manager/FLOW.md`
- Modify: `cmd/wkcli/README.md`
- Modify: `docs/superpowers/runbooks/internalv2-dynamic-node-operations.md`

- [ ] **Step 1: Document the read path**

Add this flow to both relevant `FLOW.md` files:

```text
manager diagnostics route
  -> management.App.DynamicNodeDiagnostics
  -> control snapshot
  -> existing scale-in/onboarding/task/audit/slot projections
  -> bounded diagnostic DTO
```

- [ ] **Step 2: Document CLI usage**

Add examples:

```bash
go run ./cmd/wkcli node diagnose 4 --context prod-a
go run ./cmd/wkcli node diagnose 4 --context prod-a --json
```

- [ ] **Step 3: Run documentation checks**

Run:

```bash
git diff --check
```

Expected: PASS.

## Stage 12A Verification

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'DynamicNodeDiagnostics|ControllerTask|NodeScaleIn|NodeOnboarding' -count=1
GOWORK=off go test ./internalv2/access/manager -run 'DynamicNodeDiagnostics|ControllerTask|ScaleIn|Onboarding|Route' -count=1
GOWORK=off go test ./cmd/wkcli/internal/nodeops -run 'Diagnose|ControllerTask|ImportBoundary' -count=1
GOWORK=off go test ./cmd/wkcli ./cmd/wkcli/internal/... -count=1
git diff --check
```

Expected: all commands pass.

## Commit

```bash
git add internalv2/usecase/management internalv2/access/manager cmd/wkcli docs/superpowers/runbooks/internalv2-dynamic-node-operations.md
git commit -m "feat: add dynamic node diagnostics surface"
```
