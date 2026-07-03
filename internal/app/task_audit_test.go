package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/observability/taskaudit"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controller"
	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func TestControllerTaskAuditRuntimeObservesTransitionsAndServesManagementReads(t *testing.T) {
	ctx := context.Background()
	audit := newControllerTaskAuditRuntime(filepath.Join(t.TempDir(), controllerTaskAuditFileName), wklog.NewNop())
	t.Cleanup(func() {
		if err := audit.Close(); err != nil {
			t.Fatalf("audit Close() error = %v", err)
		}
	})

	task := cv2.ReconcileTask{
		TaskID:           "slot-1-replica-move-1-to-4-r12",
		SlotID:           1,
		Kind:             cv2.TaskKindSlotReplicaMove,
		Step:             cv2.TaskStepOpenLearner,
		SourceNode:       1,
		TargetNode:       4,
		TargetPeers:      []uint64{2, 3, 4},
		CompletionPolicy: cv2.TaskCompletionPolicyAllTargetPeers,
		ConfigEpoch:      7,
		Status:           cv2.TaskStatusRunning,
		ParticipantProgress: []cv2.TaskParticipantProgress{{
			NodeID: 4,
			Status: cv2.TaskParticipantStatusPending,
		}},
	}
	progress := task
	progress.Step = cv2.TaskStepPromoteLearner
	progress.ParticipantProgress[0].Status = cv2.TaskParticipantStatusDone

	audit.ObserveControllerTaskTransitions([]cv2.TaskTransition{{
		AppliedRaftIndex: 12,
		AppliedRaftTerm:  3,
		CommandKind:      command.KindUpsertSlotReplicaMoveTask,
		IssuedAt:         time.Unix(12, 0),
		After:            task,
		AfterValid:       true,
	}, {
		AppliedRaftIndex: 13,
		AppliedRaftTerm:  3,
		CommandKind:      command.KindReportTaskProgress,
		IssuedAt:         time.Unix(13, 0),
		Before:           task,
		BeforeValid:      true,
		After:            progress,
		AfterValid:       true,
		ParticipantNode:  4,
	}})

	list := waitForControllerTaskAuditList(t, audit, managementusecase.ControllerTaskAuditListRequest{
		Kind:   string(cv2.TaskKindSlotReplicaMove),
		NodeID: 4,
	})
	if len(list.Items) != 1 {
		t.Fatalf("list items = %d, want 1", len(list.Items))
	}
	if list.Items[0].TaskID != task.TaskID || list.Items[0].Status != string(cv2.TaskStatusRunning) || list.Items[0].Step != string(cv2.TaskStepPromoteLearner) {
		t.Fatalf("snapshot = %+v, want running task %s at promote_learner", list.Items[0], task.TaskID)
	}

	timeline, err := audit.ControllerTaskAuditEvents(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("ControllerTaskAuditEvents() error = %v", err)
	}
	if len(timeline.Events) != 2 {
		t.Fatalf("events = %d, want 2", len(timeline.Events))
	}
	if timeline.Events[0].Type != "created" {
		t.Fatalf("first event type = %q, want created", timeline.Events[0].Type)
	}
	if timeline.Events[1].Type != "participant_progress" || timeline.Events[1].ParticipantNode != 4 {
		t.Fatalf("second event = %+v, want participant_progress from node 4", timeline.Events[1])
	}
}

func TestControllerTaskAuditPathUsesPromotedDefault(t *testing.T) {
	dir := t.TempDir()

	got := controllerTaskAuditPath(dir)
	want := filepath.Join(dir, "observability", "task-audit", controllerTaskAuditFileName)
	if got != want {
		t.Fatalf("controllerTaskAuditPath() = %q, want %q", got, want)
	}
}

func TestControllerTaskAuditPathUsesLegacyFileWhenOnlyLegacyExists(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "observability", "task-audit", controllerTaskAuditLegacyFileName)
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(legacy, nil, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if got := controllerTaskAuditPath(dir); got != legacy {
		t.Fatalf("controllerTaskAuditPath() = %q, want legacy %q", got, legacy)
	}
}

func TestControllerTaskAuditParticipantFailureEmitsFailedEvent(t *testing.T) {
	before := cv2.ReconcileTask{
		TaskID:     "slot-1-replica-move-2-to-4-r9",
		Kind:       cv2.TaskKindSlotReplicaMove,
		SlotID:     1,
		SourceNode: 2,
		TargetNode: 4,
		Status:     cv2.TaskStatusRunning,
		ParticipantProgress: []cv2.TaskParticipantProgress{{
			NodeID: 4,
			Status: cv2.TaskParticipantStatusPending,
		}},
	}
	after := before
	after.Status = cv2.TaskStatusFailed
	after.ParticipantProgress[0].Status = cv2.TaskParticipantStatusFailed
	after.ParticipantProgress[0].LastError = "learner apply timed out"

	event, ok := controllerTaskAuditEventForTransition(cv2.TaskTransition{
		AppliedRaftIndex: 18,
		AppliedRaftTerm:  3,
		CommandKind:      command.KindReportTaskProgress,
		Before:           before,
		BeforeValid:      true,
		After:            after,
		AfterValid:       true,
		ParticipantNode:  4,
	})

	if !ok {
		t.Fatal("controllerTaskAuditEventForTransition() ok = false, want true")
	}
	if event.Type != taskaudit.EventFailed {
		t.Fatalf("event type = %q, want failed", event.Type)
	}
	if event.Status != string(cv2.TaskStatusFailed) || event.ParticipantNode != 4 {
		t.Fatalf("event = %+v, want failed status from participant node 4", event)
	}
	if event.Reason != "learner apply timed out" {
		t.Fatalf("event reason = %q, want participant failure reason", event.Reason)
	}
}

func TestWireControllerTaskAuditConfiguresObserverAndManagerReader(t *testing.T) {
	app := &App{
		cfg: Config{
			DataDir: t.TempDir(),
			Cluster: cluster.Config{
				NodeID: 1,
			},
		},
		logger: wklog.NewNop(),
	}
	clusterCfg := defaultClusterConfig(app.cfg)

	app.wireControllerTaskAudit(&clusterCfg)

	if app.controllerTaskAudit == nil {
		t.Fatal("controller task audit was not wired")
	}
	t.Cleanup(func() {
		if err := app.controllerTaskAudit.Close(); err != nil {
			t.Fatalf("audit Close() error = %v", err)
		}
	})
	if clusterCfg.Control.TaskTransitionObserver == nil {
		t.Fatal("cluster task transition observer was not configured")
	}
	app.cluster = &fakeManagerCluster{nodeID: 1}
	management := app.newManagerManagement()
	if management == nil {
		t.Fatal("manager management was not wired")
	}
	if _, err := management.ListControllerTaskAudits(context.Background(), managementusecase.ControllerTaskAuditListRequest{}); err != nil {
		t.Fatalf("manager task audit reader returned error = %v", err)
	}
}

func TestWireManagerTaskAuditRPCRegistersHandler(t *testing.T) {
	cluster := &fakeManagerCluster{nodeID: 1}
	_, err := newTestApp(t, Config{DataDir: t.TempDir()}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, ok := cluster.registeredHandlers[accessnode.ManagerTaskAuditRPCServiceID]; !ok {
		t.Fatalf("manager task audit RPC handler was not registered")
	}
}

func TestControllerTaskAuditBackfillsActiveTasksFromSnapshot(t *testing.T) {
	cluster := &fakeManagerCluster{
		nodeID: 1,
		snapshot: control.Snapshot{
			Revision: 99,
			Tasks: []control.ReconcileTask{{
				TaskID:      "slot-2-bootstrap-1",
				SlotID:      2,
				Kind:        control.TaskKindBootstrap,
				Step:        control.TaskStepCreateSlot,
				TargetNode:  1,
				TargetPeers: []uint64{1, 2, 3},
				ConfigEpoch: 5,
				Status:      control.TaskStatusPending,
			}},
		},
	}
	app := &App{
		cluster:             cluster,
		controllerTaskAudit: newControllerTaskAuditRuntime(filepath.Join(t.TempDir(), controllerTaskAuditFileName), wklog.NewNop()),
		logger:              wklog.NewNop(),
	}
	t.Cleanup(func() {
		if err := app.controllerTaskAudit.Close(); err != nil {
			t.Fatalf("audit Close() error = %v", err)
		}
	})

	if err := app.backfillControllerTaskAudit(context.Background()); err != nil {
		t.Fatalf("backfillControllerTaskAudit() error = %v", err)
	}
	timeline, err := app.controllerTaskAudit.ControllerTaskAuditEvents(context.Background(), "slot-2-bootstrap-1")
	if err != nil {
		t.Fatalf("ControllerTaskAuditEvents() error = %v", err)
	}
	if len(timeline.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(timeline.Events))
	}
	if timeline.Events[0].Type != "snapshot" || timeline.Events[0].AppliedRaftIndex != 99 {
		t.Fatalf("backfill event = %+v, want revision snapshot", timeline.Events[0])
	}
}

func TestControllerTaskAuditRuntimeReportsOldestTaskAgeMetric(t *testing.T) {
	ctx := context.Background()
	reg := obsmetrics.New(1, "node-1")
	now := time.Unix(100, 0).UTC()
	audit := newControllerTaskAuditRuntime(filepath.Join(t.TempDir(), controllerTaskAuditFileName), wklog.NewNop())
	audit.metrics = reg
	audit.opts.Now = func() time.Time { return now }
	t.Cleanup(func() {
		if err := audit.Close(); err != nil {
			t.Fatalf("audit Close() error = %v", err)
		}
	})

	err := audit.AppendSnapshotTasks(ctx, control.Snapshot{
		Revision: 7,
		Tasks: []control.ReconcileTask{{
			TaskID: "slot-1-replica-move-2-to-4-r7",
			Kind:   control.TaskKindSlotReplicaMove,
			Status: control.TaskStatusRunning,
			Step:   control.TaskStepRemoveVoter,
			SlotID: 1,
		}},
	})
	if err != nil {
		t.Fatalf("AppendSnapshotTasks() error = %v", err)
	}
	now = now.Add(42 * time.Second)
	if _, err := audit.ListControllerTaskAudits(ctx, managementusecase.ControllerTaskAuditListRequest{}); err != nil {
		t.Fatalf("ListControllerTaskAudits() error = %v", err)
	}

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	age := requireAppMetricFamily(t, families, "wukongim_controller_task_oldest_age_seconds")
	if got := findAppMetricByLabels(t, age, map[string]string{
		"node_id":   "1",
		"node_name": "node-1",
		"kind":      "slot_replica_move",
		"status":    "running",
		"step":      "remove_voter",
		"source":    "audit",
	}).GetGauge().GetValue(); got < 42 || got > 42.1 {
		t.Fatalf("oldest task age = %v, want about 42", got)
	}
}

func waitForControllerTaskAuditList(t *testing.T, audit *controllerTaskAuditRuntime, req managementusecase.ControllerTaskAuditListRequest) managementusecase.ControllerTaskAuditListResponse {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	var last managementusecase.ControllerTaskAuditListResponse
	var lastErr error
	for time.Now().Before(deadline) {
		last, lastErr = audit.ListControllerTaskAudits(context.Background(), req)
		if lastErr == nil && len(last.Items) > 0 {
			return last
		}
		time.Sleep(10 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("ListControllerTaskAudits() error = %v", lastErr)
	}
	t.Fatalf("ListControllerTaskAudits() items = %d, want > 0", len(last.Items))
	return managementusecase.ControllerTaskAuditListResponse{}
}
