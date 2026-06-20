# Controller Task Observability V1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose active ControllerV2 tasks through read-only `wukongimv2` manager APIs so operators can list, filter, and inspect the current task set.

**Architecture:** Keep HTTP parsing, permissions, and JSON response shape in `internalv2/access/manager`. Keep task projection, filtering, deterministic sorting, limit behavior, and detail not-found semantics in `internalv2/usecase/management`, backed only by `ControlSnapshotReader.LocalControlSnapshot`. Do not change ControllerV2 state, task persistence, or `cluster-state.json` schema.

**Tech Stack:** Go, Gin manager HTTP routes, `internalv2/usecase/management`, `pkg/clusterv2/control`, `pkg/db/meta` typed errors, standard `go test`.

---

## Context

Read these before editing code:

- `docs/superpowers/specs/2026-06-20-controller-task-observability-v1-design.md`
- `internalv2/usecase/management/FLOW.md`
- `internalv2/access/manager/FLOW.md`
- `internalv2/usecase/management/slots.go`
- `internalv2/access/manager/server.go`
- `internalv2/access/manager/server_test.go`

Important existing facts:

- Active tasks are already present at `control.Snapshot.Tasks`.
- Completed tasks are absent from active state because ControllerV2 removes them through `complete_task`.
- Failed tasks remain active and carry `Status=failed`, `Attempt`, and `LastError`.
- `internalv2/usecase/management.ListSlots` already maps one active `control.ReconcileTask` into `SlotTask`; use that as the projection style reference.
- Manager read routes that inspect Controller internals use permission `cluster.controller:r`.

## Files

- Create: `internalv2/usecase/management/controller_tasks.go`
- Create: `internalv2/usecase/management/controller_tasks_test.go`
- Create: `internalv2/access/manager/controller_tasks.go`
- Create: `internalv2/access/manager/controller_tasks_test.go`
- Modify: `internalv2/access/manager/server.go`
- Modify: `internalv2/access/manager/server_test.go`
- Modify: `internalv2/usecase/management/FLOW.md`
- Modify: `internalv2/access/manager/FLOW.md`

---

### Task 1: Management Usecase Projection

**Files:**
- Create: `internalv2/usecase/management/controller_tasks_test.go`
- Create: `internalv2/usecase/management/controller_tasks.go`

- [ ] **Step 1: Write the failing usecase tests**

Create `internalv2/usecase/management/controller_tasks_test.go`:

```go
package management

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestListControllerTasksFiltersSortsAndLimits(t *testing.T) {
	app := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: control.Snapshot{Tasks: []control.ReconcileTask{
		controllerTaskFixture("slot-2-leader-transfer-9-r1", 2, control.TaskKindLeaderTransfer, control.TaskStatusPending, 2, 3, []uint64{2, 3}),
		controllerTaskFixture("slot-1-bootstrap-7", 1, control.TaskKindBootstrap, control.TaskStatusRunning, 0, 1, []uint64{1, 2, 3}),
		controllerTaskFixture("slot-1-leader-transfer-7-r9", 1, control.TaskKindLeaderTransfer, control.TaskStatusPending, 1, 2, []uint64{1, 2, 3}),
	}}}})

	resp, err := app.ListControllerTasks(context.Background(), ListControllerTasksRequest{
		Kind:   "leader_transfer",
		Status: "pending",
		NodeID: 2,
		Limit:  1,
	})
	if err != nil {
		t.Fatalf("ListControllerTasks() error = %v", err)
	}
	if resp.Total != 2 {
		t.Fatalf("Total = %d, want 2", resp.Total)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("Items len = %d, want 1: %#v", len(resp.Items), resp.Items)
	}
	if resp.Items[0].TaskID != "slot-1-leader-transfer-7-r9" {
		t.Fatalf("first task = %#v, want sorted slot 1 leader-transfer task", resp.Items[0])
	}
}

func TestListControllerTasksFiltersBySlotAndRelatedNode(t *testing.T) {
	app := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: control.Snapshot{Tasks: []control.ReconcileTask{
		{
			TaskID:      "source",
			SlotID:      1,
			Kind:        control.TaskKindLeaderTransfer,
			Step:        control.TaskStepTransferLeader,
			SourceNode:  7,
			TargetNode:  2,
			ConfigEpoch: 1,
			Status:      control.TaskStatusPending,
		},
		{
			TaskID:      "target",
			SlotID:      1,
			Kind:        control.TaskKindLeaderTransfer,
			Step:        control.TaskStepTransferLeader,
			TargetNode:  7,
			ConfigEpoch: 1,
			Status:      control.TaskStatusPending,
		},
		{
			TaskID:      "peer",
			SlotID:      1,
			Kind:        control.TaskKindBootstrap,
			Step:        control.TaskStepCreateSlot,
			TargetPeers: []uint64{1, 7, 9},
			ConfigEpoch: 1,
			Status:      control.TaskStatusPending,
		},
		{
			TaskID: "participant",
			SlotID: 2,
			Kind:   control.TaskKindBootstrap,
			Step:   control.TaskStepCreateSlot,
			ParticipantProgress: []control.TaskParticipantProgress{
				{NodeID: 7, Attempt: 1, Status: control.TaskParticipantStatusFailed, LastError: "open failed"},
			},
			ConfigEpoch: 1,
			Status:      control.TaskStatusFailed,
			LastError:   "barrier failed",
		},
		{
			TaskID:      "other",
			SlotID:      1,
			Kind:        control.TaskKindBootstrap,
			Step:        control.TaskStepCreateSlot,
			TargetPeers: []uint64{1, 2, 3},
			ConfigEpoch: 1,
			Status:      control.TaskStatusPending,
		},
	}}}})

	nodeResp, err := app.ListControllerTasks(context.Background(), ListControllerTasksRequest{NodeID: 7})
	if err != nil {
		t.Fatalf("ListControllerTasks(node) error = %v", err)
	}
	if nodeResp.Total != 4 {
		t.Fatalf("node Total = %d, want 4: %#v", nodeResp.Total, nodeResp.Items)
	}

	slotResp, err := app.ListControllerTasks(context.Background(), ListControllerTasksRequest{SlotID: 2, NodeID: 7})
	if err != nil {
		t.Fatalf("ListControllerTasks(slot,node) error = %v", err)
	}
	if slotResp.Total != 1 || slotResp.Items[0].TaskID != "participant" {
		t.Fatalf("slot/node response = %#v, want participant", slotResp)
	}
	if slotResp.Items[0].Participants[0].LastError != "open failed" {
		t.Fatalf("participant = %#v, want failed participant detail", slotResp.Items[0].Participants)
	}
}

func TestListControllerTasksLimitBounds(t *testing.T) {
	tasks := make([]control.ReconcileTask, 0, DefaultControllerTaskLimit+1)
	for i := 0; i < DefaultControllerTaskLimit+1; i++ {
		slotID := uint32(i + 1)
		tasks = append(tasks, controllerTaskFixture(fmt.Sprintf("task-%03d", i), slotID, control.TaskKindBootstrap, control.TaskStatusPending, 0, 1, []uint64{1}))
	}
	app := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: control.Snapshot{Tasks: tasks}}})

	resp, err := app.ListControllerTasks(context.Background(), ListControllerTasksRequest{})
	if err != nil {
		t.Fatalf("ListControllerTasks(default limit) error = %v", err)
	}
	if resp.Total != DefaultControllerTaskLimit+1 || len(resp.Items) != DefaultControllerTaskLimit {
		t.Fatalf("default limit response = total %d len %d, want total %d len %d", resp.Total, len(resp.Items), DefaultControllerTaskLimit+1, DefaultControllerTaskLimit)
	}

	_, err = app.ListControllerTasks(context.Background(), ListControllerTasksRequest{Limit: MaxControllerTaskLimit + 1})
	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("ListControllerTasks(over max) error = %v, want %v", err, metadb.ErrInvalidArgument)
	}
}

func TestControllerTaskReturnsDetailAndClonesSlices(t *testing.T) {
	app := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: control.Snapshot{Tasks: []control.ReconcileTask{{
		TaskID:           "slot-1-bootstrap-1",
		SlotID:           1,
		Kind:             control.TaskKindBootstrap,
		Step:             control.TaskStepCreateSlot,
		TargetNode:       1,
		TargetPeers:      []uint64{1, 2, 3},
		CompletionPolicy: control.TaskCompletionPolicyAllTargetPeers,
		ParticipantProgress: []control.TaskParticipantProgress{
			{NodeID: 1, Attempt: 2, Status: control.TaskParticipantStatusDone},
		},
		ConfigEpoch: 7,
		Attempt:     2,
		Status:      control.TaskStatusRunning,
	}}}}})

	first, err := app.ControllerTask(context.Background(), "slot-1-bootstrap-1")
	if err != nil {
		t.Fatalf("ControllerTask() error = %v", err)
	}
	first.TargetPeers[0] = 99
	first.Participants[0].NodeID = 99

	second, err := app.ControllerTask(context.Background(), "slot-1-bootstrap-1")
	if err != nil {
		t.Fatalf("ControllerTask(second) error = %v", err)
	}
	if !sameUint64Slice(second.TargetPeers, []uint64{1, 2, 3}) {
		t.Fatalf("TargetPeers = %#v, want cloned [1 2 3]", second.TargetPeers)
	}
	if second.Participants[0].NodeID != 1 {
		t.Fatalf("Participants = %#v, want cloned node 1", second.Participants)
	}
	if second.Kind != "bootstrap" || second.Step != "create_slot" || second.Status != "running" || second.ConfigEpoch != 7 || second.Attempt != 2 {
		t.Fatalf("detail = %#v, want bootstrap running detail", second)
	}
}

func TestControllerTaskNotFoundAndSnapshotError(t *testing.T) {
	notFoundApp := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: control.Snapshot{Tasks: []control.ReconcileTask{
		controllerTaskFixture("existing", 1, control.TaskKindBootstrap, control.TaskStatusPending, 0, 1, []uint64{1}),
	}}}})
	_, err := notFoundApp.ControllerTask(context.Background(), "missing")
	if !errors.Is(err, ErrControllerTaskNotFound) {
		t.Fatalf("ControllerTask(missing) error = %v, want %v", err, ErrControllerTaskNotFound)
	}

	wantErr := errors.New("control snapshot unavailable")
	errApp := New(Options{Cluster: fakeNodeSnapshotReader{err: wantErr}})
	_, err = errApp.ListControllerTasks(context.Background(), ListControllerTasksRequest{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ListControllerTasks(snapshot error) error = %v, want %v", err, wantErr)
	}
	_, err = errApp.ControllerTask(context.Background(), "existing")
	if !errors.Is(err, wantErr) {
		t.Fatalf("ControllerTask(snapshot error) error = %v, want %v", err, wantErr)
	}
}

func controllerTaskFixture(taskID string, slotID uint32, kind control.TaskKind, status control.TaskStatus, sourceNode uint64, targetNode uint64, targetPeers []uint64) control.ReconcileTask {
	step := control.TaskStepCreateSlot
	policy := control.TaskCompletionPolicyAllTargetPeers
	if kind == control.TaskKindLeaderTransfer {
		step = control.TaskStepTransferLeader
		policy = control.TaskCompletionPolicySingleObserver
	}
	return control.ReconcileTask{
		TaskID:           taskID,
		SlotID:           slotID,
		Kind:             kind,
		Step:             step,
		SourceNode:       sourceNode,
		TargetNode:       targetNode,
		TargetPeers:      append([]uint64(nil), targetPeers...),
		CompletionPolicy: policy,
		ConfigEpoch:      1,
		Status:           status,
	}
}
```

- [ ] **Step 2: Run the new usecase tests and confirm the expected failure**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'ControllerTask|ListControllerTasks' -count=1
```

Expected: FAIL because `ListControllerTasksRequest`, `ListControllerTasksResponse`, `ControllerTask`, `ErrControllerTaskNotFound`, and the `App` methods do not exist yet.

- [ ] **Step 3: Add the usecase projection**

Create `internalv2/usecase/management/controller_tasks.go`:

```go
package management

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	// DefaultControllerTaskLimit is used when the manager task list omits limit or passes zero.
	DefaultControllerTaskLimit = 100
	// MaxControllerTaskLimit bounds one active Controller task list response.
	MaxControllerTaskLimit = 500
)

// ErrControllerTaskNotFound reports that an active Controller task is absent from the local control snapshot.
var ErrControllerTaskNotFound = errors.New("internalv2/usecase/management: controller task not found")

// ListControllerTasksRequest contains active Controller task filters.
type ListControllerTasksRequest struct {
	// Kind filters by task kind when set.
	Kind string
	// Status filters by task status when set.
	Status string
	// SlotID filters by physical Slot when non-zero.
	SlotID uint32
	// NodeID filters by any task-related node when non-zero.
	NodeID uint64
	// Limit caps returned items; zero uses DefaultControllerTaskLimit.
	Limit int
}

// ListControllerTasksResponse contains an ordered active Controller task page.
type ListControllerTasksResponse struct {
	// Total is the number of matching active tasks before limit is applied.
	Total int
	// Items contains the limited matching active tasks.
	Items []ControllerTask
}

// ControllerTask is the manager-facing active Controller task DTO.
type ControllerTask struct {
	// TaskID is the durable task identity.
	TaskID string
	// SlotID is the affected physical Slot.
	SlotID uint32
	// Kind is the reconcile workflow kind.
	Kind string
	// Step is the current workflow step.
	Step string
	// Status is the active task status.
	Status string
	// SourceNode is the optional source node for move-like tasks.
	SourceNode uint64
	// TargetNode is the primary task target when set.
	TargetNode uint64
	// TargetPeers are the peers expected to participate.
	TargetPeers []uint64
	// CompletionPolicy describes how participant progress gates completion.
	CompletionPolicy string
	// ConfigEpoch ties the task to a Slot assignment epoch.
	ConfigEpoch uint64
	// Attempt is the global task attempt.
	Attempt uint32
	// LastError is the latest task-level error.
	LastError string
	// Participants contains per-node task progress.
	Participants []ControllerTaskParticipant
}

// ControllerTaskParticipant is one node's task progress summary.
type ControllerTaskParticipant struct {
	// NodeID is the participant node identity.
	NodeID uint64
	// Attempt is the participant-local attempt.
	Attempt uint32
	// Status is the participant progress state.
	Status string
	// LastError is the latest participant-level error.
	LastError string
}

// ListControllerTasks returns active Controller tasks from the local control snapshot.
func (a *App) ListControllerTasks(ctx context.Context, req ListControllerTasksRequest) (ListControllerTasksResponse, error) {
	limit, err := normalizeControllerTaskLimit(req.Limit)
	if err != nil {
		return ListControllerTasksResponse{}, err
	}
	if a == nil || a.cluster == nil {
		return ListControllerTasksResponse{Items: []ControllerTask{}}, nil
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return ListControllerTasksResponse{}, err
	}
	items := make([]ControllerTask, 0, len(snapshot.Tasks))
	for _, task := range snapshot.Tasks {
		item := controllerTaskFromControl(task)
		if !controllerTaskMatches(item, req) {
			continue
		}
		items = append(items, item)
	}
	sortControllerTasks(items)
	total := len(items)
	if len(items) > limit {
		items = items[:limit]
	}
	return ListControllerTasksResponse{Total: total, Items: items}, nil
}

// ControllerTask returns one active Controller task by exact task ID.
func (a *App) ControllerTask(ctx context.Context, taskID string) (ControllerTask, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ControllerTask{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.cluster == nil {
		return ControllerTask{}, ErrControllerTaskNotFound
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return ControllerTask{}, err
	}
	for _, task := range snapshot.Tasks {
		if task.TaskID == taskID {
			return controllerTaskFromControl(task), nil
		}
	}
	return ControllerTask{}, ErrControllerTaskNotFound
}

func normalizeControllerTaskLimit(limit int) (int, error) {
	if limit < 0 || limit > MaxControllerTaskLimit {
		return 0, metadb.ErrInvalidArgument
	}
	if limit == 0 {
		return DefaultControllerTaskLimit, nil
	}
	return limit, nil
}

func controllerTaskFromControl(task control.ReconcileTask) ControllerTask {
	participants := make([]ControllerTaskParticipant, 0, len(task.ParticipantProgress))
	for _, item := range task.ParticipantProgress {
		participants = append(participants, ControllerTaskParticipant{
			NodeID:    item.NodeID,
			Attempt:   item.Attempt,
			Status:    string(item.Status),
			LastError: item.LastError,
		})
	}
	return ControllerTask{
		TaskID:           task.TaskID,
		SlotID:           task.SlotID,
		Kind:             string(task.Kind),
		Step:             string(task.Step),
		Status:           string(task.Status),
		SourceNode:       task.SourceNode,
		TargetNode:       task.TargetNode,
		TargetPeers:      append([]uint64(nil), task.TargetPeers...),
		CompletionPolicy: string(task.CompletionPolicy),
		ConfigEpoch:      task.ConfigEpoch,
		Attempt:          task.Attempt,
		LastError:        task.LastError,
		Participants:     participants,
	}
}

func controllerTaskMatches(task ControllerTask, req ListControllerTasksRequest) bool {
	if req.Kind != "" && task.Kind != req.Kind {
		return false
	}
	if req.Status != "" && task.Status != req.Status {
		return false
	}
	if req.SlotID != 0 && task.SlotID != req.SlotID {
		return false
	}
	if req.NodeID != 0 && !controllerTaskRelatedToNode(task, req.NodeID) {
		return false
	}
	return true
}

func controllerTaskRelatedToNode(task ControllerTask, nodeID uint64) bool {
	if task.SourceNode == nodeID || task.TargetNode == nodeID {
		return true
	}
	if containsUint64(task.TargetPeers, nodeID) {
		return true
	}
	for _, participant := range task.Participants {
		if participant.NodeID == nodeID {
			return true
		}
	}
	return false
}

func sortControllerTasks(items []ControllerTask) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].SlotID != items[j].SlotID {
			return items[i].SlotID < items[j].SlotID
		}
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].TaskID < items[j].TaskID
	})
}
```

- [ ] **Step 4: Run the usecase tests and format**

Run:

```bash
gofmt -w internalv2/usecase/management/controller_tasks.go internalv2/usecase/management/controller_tasks_test.go
GOWORK=off go test ./internalv2/usecase/management -run 'ControllerTask|ListControllerTasks|ListSlots' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the usecase projection**

```bash
git add internalv2/usecase/management/controller_tasks.go internalv2/usecase/management/controller_tasks_test.go
git commit -m "feat: add controller task read model"
```

---

### Task 2: Manager HTTP Routes

**Files:**
- Create: `internalv2/access/manager/controller_tasks_test.go`
- Create: `internalv2/access/manager/controller_tasks.go`
- Modify: `internalv2/access/manager/server.go`
- Modify: `internalv2/access/manager/server_test.go`

- [ ] **Step 1: Write the failing manager HTTP tests**

Create `internalv2/access/manager/controller_tasks_test.go`:

```go
package manager

import (
	"net/http"
	"net/http/httptest"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagerControllerTasksReturnsFilteredList(t *testing.T) {
	var seen managementusecase.ListControllerTasksRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.controller",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{
			controllerTasksReqSink: &seen,
			controllerTasksResponse: managementusecase.ListControllerTasksResponse{
				Total: 1,
				Items: []managementusecase.ControllerTask{{
					TaskID:           "slot-1-leader-transfer-7-r9",
					SlotID:           1,
					Kind:             "leader_transfer",
					Step:             "transfer_leader",
					Status:           "pending",
					SourceNode:       1,
					TargetNode:       2,
					TargetPeers:      []uint64{1, 2, 3},
					CompletionPolicy: "single_observer",
					ConfigEpoch:      7,
					Attempt:          1,
					LastError:        "not leader",
					Participants: []managementusecase.ControllerTaskParticipant{
						{NodeID: 2, Attempt: 1, Status: "failed", LastError: "not leader"},
					},
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/controller/tasks?kind=leader_transfer&status=pending&slot_id=1&node_id=2&limit=20", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if seen != (managementusecase.ListControllerTasksRequest{Kind: "leader_transfer", Status: "pending", SlotID: 1, NodeID: 2, Limit: 20}) {
		t.Fatalf("request = %#v, want parsed filters", seen)
	}
	if !jsonEqual(rec.Body.String(), `{
		"total": 1,
		"items": [{
			"task_id": "slot-1-leader-transfer-7-r9",
			"slot_id": 1,
			"kind": "leader_transfer",
			"step": "transfer_leader",
			"status": "pending",
			"source_node": 1,
			"target_node": 2,
			"target_peers": [1,2,3],
			"completion_policy": "single_observer",
			"config_epoch": 7,
			"attempt": 1,
			"last_error": "not leader",
			"participants": [{
				"node_id": 2,
				"attempt": 1,
				"status": "failed",
				"last_error": "not leader"
			}]
		}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerControllerTaskReturnsDetail(t *testing.T) {
	var seen string
	srv := New(Options{Management: managerNodesStub{
		controllerTaskIDSink: &seen,
		controllerTask: managementusecase.ControllerTask{
			TaskID:           "slot-1-bootstrap-1",
			SlotID:           1,
			Kind:             "bootstrap",
			Step:             "create_slot",
			Status:           "running",
			TargetNode:       1,
			TargetPeers:      []uint64{1, 2, 3},
			CompletionPolicy: "all_target_peers",
			ConfigEpoch:      1,
			Participants:     []managementusecase.ControllerTaskParticipant{},
		},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/controller/tasks/slot-1-bootstrap-1", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if seen != "slot-1-bootstrap-1" {
		t.Fatalf("task id = %q, want slot-1-bootstrap-1", seen)
	}
	if !jsonEqual(rec.Body.String(), `{
		"task_id": "slot-1-bootstrap-1",
		"slot_id": 1,
		"kind": "bootstrap",
		"step": "create_slot",
		"status": "running",
		"source_node": 0,
		"target_node": 1,
		"target_peers": [1,2,3],
		"completion_policy": "all_target_peers",
		"config_epoch": 1,
		"attempt": 0,
		"last_error": "",
		"participants": []
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerControllerTasksRejectInvalidQueries(t *testing.T) {
	tests := []string{
		"/manager/controller/tasks?kind=rebalance",
		"/manager/controller/tasks?status=done",
		"/manager/controller/tasks?slot_id=0",
		"/manager/controller/tasks?slot_id=bad",
		"/manager/controller/tasks?node_id=0",
		"/manager/controller/tasks?node_id=bad",
		"/manager/controller/tasks?limit=-1",
		"/manager/controller/tasks?limit=bad",
		"/manager/controller/tasks?limit=501",
	}
	for _, target := range tests {
		t.Run(target, func(t *testing.T) {
			srv := New(Options{Management: managerNodesStub{}})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, target, nil)

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), `{"error":"bad_request","message":"invalid controller task query"}`) {
				t.Fatalf("body = %s", rec.Body.String())
			}
		})
	}
}

func TestManagerControllerTaskMapsErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
		body   string
	}{
		{name: "invalid", err: metadb.ErrInvalidArgument, status: http.StatusBadRequest, code: "bad_request", body: `{"error":"bad_request","message":"invalid controller task request"}`},
		{name: "missing", err: managementusecase.ErrControllerTaskNotFound, status: http.StatusNotFound, code: "not_found", body: `{"error":"not_found","message":"controller task not found"}`},
		{name: "snapshot unavailable", err: clusterv2.ErrNotStarted, status: http.StatusServiceUnavailable, code: "service_unavailable", body: `{"error":"service_unavailable","message":"controller task read unavailable"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{Management: managerNodesStub{controllerTaskErr: tt.err}})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/manager/controller/tasks/missing", nil)

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.status, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), tt.body) {
				t.Fatalf("body = %s, want %s", rec.Body.String(), tt.body)
			}
		})
	}
}

func TestManagerControllerTasksMapsListSnapshotUnavailable(t *testing.T) {
	srv := New(Options{Management: managerNodesStub{controllerTasksErr: clusterv2.ErrNotStarted}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/controller/tasks", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"error":"service_unavailable","message":"controller task read unavailable"}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerControllerTasksRequireControllerReadPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "writer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.controller",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/controller/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "writer"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}
```

- [ ] **Step 2: Run the manager tests and confirm the expected failure**

Run:

```bash
GOWORK=off go test ./internalv2/access/manager -run 'ControllerTask|ControllerTasks' -count=1
```

Expected: FAIL because the routes, DTOs, interface methods, and test stub fields do not exist yet.

- [ ] **Step 3: Add manager task DTOs, parsing, handlers, and error mapping**

Create `internalv2/access/manager/controller_tasks.go`:

```go
package manager

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

// ManagerControllerTasksResponse is the active Controller task list response body.
type ManagerControllerTasksResponse struct {
	// Total is the number of matching active tasks before limit is applied.
	Total int `json:"total"`
	// Items contains matching active Controller tasks.
	Items []ManagerControllerTask `json:"items"`
}

// ManagerControllerTask is one active Controller task visible to operators.
type ManagerControllerTask struct {
	// TaskID is the durable task identity.
	TaskID string `json:"task_id"`
	// SlotID is the affected physical Slot.
	SlotID uint32 `json:"slot_id"`
	// Kind is the reconcile workflow kind.
	Kind string `json:"kind"`
	// Step is the current workflow step.
	Step string `json:"step"`
	// Status is the active task status.
	Status string `json:"status"`
	// SourceNode is the optional source node for move-like tasks.
	SourceNode uint64 `json:"source_node"`
	// TargetNode is the primary task target when set.
	TargetNode uint64 `json:"target_node"`
	// TargetPeers are the peers expected to participate.
	TargetPeers []uint64 `json:"target_peers"`
	// CompletionPolicy describes how participant progress gates completion.
	CompletionPolicy string `json:"completion_policy"`
	// ConfigEpoch ties the task to a Slot assignment epoch.
	ConfigEpoch uint64 `json:"config_epoch"`
	// Attempt is the global task attempt.
	Attempt uint32 `json:"attempt"`
	// LastError is the latest task-level error.
	LastError string `json:"last_error"`
	// Participants contains per-node task progress.
	Participants []ManagerControllerTaskParticipant `json:"participants"`
}

// ManagerControllerTaskParticipant is one node's task progress summary.
type ManagerControllerTaskParticipant struct {
	// NodeID is the participant node identity.
	NodeID uint64 `json:"node_id"`
	// Attempt is the participant-local attempt.
	Attempt uint32 `json:"attempt"`
	// Status is the participant progress state.
	Status string `json:"status"`
	// LastError is the latest participant-level error.
	LastError string `json:"last_error"`
}

func (s *Server) handleControllerTasks(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	req, err := parseControllerTasksRequest(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid controller task query")
		return
	}
	resp, err := s.management.ListControllerTasks(c.Request.Context(), req)
	if err != nil {
		writeControllerTaskError(c, err)
		return
	}
	c.JSON(http.StatusOK, ManagerControllerTasksResponse{
		Total: resp.Total,
		Items: controllerTaskDTOs(resp.Items),
	})
}

func (s *Server) handleControllerTask(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	taskID := strings.TrimSpace(c.Param("task_id"))
	if taskID == "" {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid controller task query")
		return
	}
	task, err := s.management.ControllerTask(c.Request.Context(), taskID)
	if err != nil {
		writeControllerTaskError(c, err)
		return
	}
	c.JSON(http.StatusOK, controllerTaskDTO(task))
}

func parseControllerTasksRequest(c *gin.Context) (managementusecase.ListControllerTasksRequest, error) {
	kind := strings.TrimSpace(c.Query("kind"))
	if kind != "" && !validControllerTaskKind(kind) {
		return managementusecase.ListControllerTasksRequest{}, errors.New("invalid kind")
	}
	status := strings.TrimSpace(c.Query("status"))
	if status != "" && !validControllerTaskStatus(status) {
		return managementusecase.ListControllerTasksRequest{}, errors.New("invalid status")
	}
	slotID, err := parseOptionalControllerTaskSlotID(c.Query("slot_id"))
	if err != nil {
		return managementusecase.ListControllerTasksRequest{}, err
	}
	nodeID, err := parseOptionalControllerTaskNodeID(c.Query("node_id"))
	if err != nil {
		return managementusecase.ListControllerTasksRequest{}, err
	}
	limit, err := parseControllerTaskLimit(c.Query("limit"))
	if err != nil {
		return managementusecase.ListControllerTasksRequest{}, err
	}
	return managementusecase.ListControllerTasksRequest{
		Kind:   kind,
		Status: status,
		SlotID: slotID,
		NodeID: nodeID,
		Limit:  limit,
	}, nil
}

func validControllerTaskKind(kind string) bool {
	switch kind {
	case string(control.TaskKindBootstrap), string(control.TaskKindLeaderTransfer):
		return true
	default:
		return false
	}
}

func validControllerTaskStatus(status string) bool {
	switch status {
	case string(control.TaskStatusPending), string(control.TaskStatusRunning), string(control.TaskStatusFailed):
		return true
	default:
		return false
	}
}

func parseOptionalControllerTaskSlotID(raw string) (uint32, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || value == 0 {
		return 0, strconv.ErrSyntax
	}
	return uint32(value), nil
}

func parseOptionalControllerTaskNodeID(raw string) (uint64, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func parseControllerTaskLimit(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 || value > managementusecase.MaxControllerTaskLimit {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func controllerTaskDTOs(tasks []managementusecase.ControllerTask) []ManagerControllerTask {
	out := make([]ManagerControllerTask, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, controllerTaskDTO(task))
	}
	return out
}

func controllerTaskDTO(task managementusecase.ControllerTask) ManagerControllerTask {
	return ManagerControllerTask{
		TaskID:           task.TaskID,
		SlotID:           task.SlotID,
		Kind:             task.Kind,
		Step:             task.Step,
		Status:           task.Status,
		SourceNode:       task.SourceNode,
		TargetNode:       task.TargetNode,
		TargetPeers:      uint64ListDTO(task.TargetPeers),
		CompletionPolicy: task.CompletionPolicy,
		ConfigEpoch:      task.ConfigEpoch,
		Attempt:          task.Attempt,
		LastError:        task.LastError,
		Participants:     controllerTaskParticipantDTOs(task.Participants),
	}
}

func controllerTaskParticipantDTOs(participants []managementusecase.ControllerTaskParticipant) []ManagerControllerTaskParticipant {
	out := make([]ManagerControllerTaskParticipant, 0, len(participants))
	for _, participant := range participants {
		out = append(out, ManagerControllerTaskParticipant{
			NodeID:    participant.NodeID,
			Attempt:   participant.Attempt,
			Status:    participant.Status,
			LastError: participant.LastError,
		})
	}
	return out
}

func uint64ListDTO(items []uint64) []uint64 {
	if len(items) == 0 {
		return []uint64{}
	}
	return append([]uint64(nil), items...)
}

func writeControllerTaskError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid controller task request")
	case errors.Is(err, managementusecase.ErrControllerTaskNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "controller task not found")
	case controlSnapshotUnavailable(err):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller task read unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
```

- [ ] **Step 4: Extend the manager interface and register routes**

Modify `internalv2/access/manager/server.go`.

Add these methods to the `Management` interface after `ListControllerLogEntries`:

```go
	// ListControllerTasks returns active Controller task rows.
	ListControllerTasks(ctx context.Context, req managementusecase.ListControllerTasksRequest) (managementusecase.ListControllerTasksResponse, error)
	// ControllerTask returns one active Controller task by ID.
	ControllerTask(ctx context.Context, taskID string) (managementusecase.ControllerTask, error)
```

Replace the two read groups for controller logs/status with one `controllerReads` group:

```go
	controllerReads := s.engine.Group("/manager")
	if s.auth.enabled() {
		controllerReads.Use(s.requirePermission("cluster.controller", "r"))
	}
	controllerReads.GET("/controller/logs", s.handleControllerLogs)
	controllerReads.GET("/controller/tasks", s.handleControllerTasks)
	controllerReads.GET("/controller/tasks/:task_id", s.handleControllerTask)
	controllerReads.GET("/nodes/:node_id/controller-raft", s.handleControllerRaftStatus)
```

Keep `controllerRaftWrites` unchanged.

- [ ] **Step 5: Extend the shared manager test stub**

Modify `internalv2/access/manager/server_test.go`.

Add these fields to `managerNodesStub` near the existing Controller fields:

```go
	controllerTasksResponse          managementusecase.ListControllerTasksResponse
	controllerTask                   managementusecase.ControllerTask
	controllerTasksReqSink           *managementusecase.ListControllerTasksRequest
	controllerTaskIDSink             *string
	controllerTasksErr               error
	controllerTaskErr                error
```

Add these methods near `ListControllerLogEntries`:

```go
func (s managerNodesStub) ListControllerTasks(_ context.Context, req managementusecase.ListControllerTasksRequest) (managementusecase.ListControllerTasksResponse, error) {
	if s.controllerTasksReqSink != nil {
		*s.controllerTasksReqSink = req
	}
	return s.controllerTasksResponse, s.controllerTasksErr
}

func (s managerNodesStub) ControllerTask(_ context.Context, taskID string) (managementusecase.ControllerTask, error) {
	if s.controllerTaskIDSink != nil {
		*s.controllerTaskIDSink = taskID
	}
	return s.controllerTask, s.controllerTaskErr
}
```

- [ ] **Step 6: Run manager tests and format**

Run:

```bash
gofmt -w internalv2/access/manager/controller_tasks.go internalv2/access/manager/controller_tasks_test.go internalv2/access/manager/server.go internalv2/access/manager/server_test.go
GOWORK=off go test ./internalv2/access/manager -run 'ControllerTask|ControllerTasks|ControllerLogs|ControllerRaft' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit the manager routes**

```bash
git add internalv2/access/manager/controller_tasks.go internalv2/access/manager/controller_tasks_test.go internalv2/access/manager/server.go internalv2/access/manager/server_test.go
git commit -m "feat: expose controller task manager api"
```

---

### Task 3: Flow Documentation and Focused Verification

**Files:**
- Modify: `internalv2/usecase/management/FLOW.md`
- Modify: `internalv2/access/manager/FLOW.md`

- [ ] **Step 1: Update management FLOW**

In `internalv2/usecase/management/FLOW.md`, update the responsibility route list to include:

```text
`GET /manager/controller/tasks`, `GET /manager/controller/tasks/:task_id`,
```

Add this section after "Slot Leader Transfer Flow" and before "Distributed Log Flow":

````markdown
## Controller Task Read Flow

```text
manager HTTP handler
  -> management.App.ListControllerTasks/ControllerTask
  -> ControlSnapshotReader.LocalControlSnapshot
  -> clusterv2 control snapshot Tasks
  -> sorted active Controller task DTO rows or one active task
```

The task read model exposes active ControllerV2 tasks only. Completed tasks are
absent because ControllerV2 removes them from active cluster state, while failed
tasks remain visible with status, attempt, task error, and participant progress.
List filtering is performed in the management usecase for kind, status,
physical Slot, and related node membership across source, target, target peers,
and participant rows. The usecase does not read Controller Raft logs, persist
history, mutate task state, or provide cancellation and retry operations.
````

- [ ] **Step 2: Update manager FLOW**

In `internalv2/access/manager/FLOW.md`, add these route lines near the existing Controller routes:

```text
GET  /manager/controller/tasks (active Controller task list; requires cluster.controller:r when Auth.On=true)
GET  /manager/controller/tasks/:task_id (active Controller task detail; requires cluster.controller:r when Auth.On=true)
```

Add this paragraph after the `/manager/controller/logs` paragraph:

```markdown
`/manager/controller/tasks*` exposes active ControllerV2 task state from the
local control snapshot. The HTTP layer validates `kind`, `status`, `slot_id`,
`node_id`, and `limit`, requires `cluster.controller:r` when manager auth is
enabled, and delegates projection/filtering to `internalv2/usecase/management`.
The route is read-only and intentionally omits completed task history because
completed tasks are removed from active cluster state.
```

- [ ] **Step 3: Run focused verification**

Run:

```bash
gofmt -w internalv2/usecase/management/controller_tasks.go internalv2/usecase/management/controller_tasks_test.go internalv2/access/manager/controller_tasks.go internalv2/access/manager/controller_tasks_test.go internalv2/access/manager/server.go internalv2/access/manager/server_test.go
GOWORK=off go test ./internalv2/usecase/management ./internalv2/access/manager -run 'ControllerTask|ControllerTasks|ListSlots|ControllerLogs|ControllerRaft' -count=1
git diff --check
```

Expected: PASS for both packages and no whitespace errors.

- [ ] **Step 4: Commit documentation and verification state**

```bash
git add internalv2/usecase/management/FLOW.md internalv2/access/manager/FLOW.md
git commit -m "docs: describe controller task manager reads"
```

---

## Final Verification

Run these after all tasks are committed:

```bash
GOWORK=off go test ./internalv2/usecase/management -run 'ControllerTask|ListControllerTasks|ListSlots|SlotLeaderTransfer' -count=1
GOWORK=off go test ./internalv2/access/manager -run 'ControllerTask|ControllerTasks|ControllerLogs|ControllerRaft|SlotLeaderTransfer' -count=1
GOWORK=off go test ./internalv2/usecase/management ./internalv2/access/manager -run 'ControllerTask|ControllerTasks|ListSlots|SlotLeaderTransfer|ControllerLogs|ControllerRaft' -count=1
git diff --check
git status --short
```

Expected:

- all targeted Go tests pass
- `git diff --check` prints nothing
- `git status --short` is clean after the final commit

No e2e test is required for this v1 because it is a read-only projection over an existing manager control snapshot port and does not touch composition wiring or task execution.

## Implementation Notes

- Keep this v1 active-state only. Do not add task history, completed task persistence, cursor pagination, cancellation, retry, pause, resume, batch transfer, or node-failure automation.
- Keep the detail lookup as a direct snapshot scan by task ID, not as a limited list query, so future batch task cardinality cannot cause false `404` responses.
- Keep `node_id` filter semantics broad: source node, target node, target peers, and participant progress all count as related nodes.
- Preserve deterministic list ordering: `slot_id asc`, `kind asc`, `task_id asc`.
- Keep HTTP limit validation strict: empty or zero means default; negative, unparsable, and greater than `500` return `400`.
