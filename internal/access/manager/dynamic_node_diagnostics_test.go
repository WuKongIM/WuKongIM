package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

func TestManagerDynamicNodeDiagnosticsRouteReturnsEvidence(t *testing.T) {
	generatedAt := time.Date(2026, 7, 1, 8, 30, 45, 123456789, time.UTC)
	var seen managementusecase.DynamicNodeDiagnosticsRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "reader",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{
			dynamicNodeDiagnosticsReqSink: &seen,
			dynamicNodeDiagnostics: managementusecase.DynamicNodeDiagnosticsResponse{
				GeneratedAt:   generatedAt,
				StateRevision: 18,
				NodeID:        4,
				Node: managementusecase.Node{
					NodeID: 4,
					Name:   "node-4",
					Addr:   "127.0.0.1:11114",
				},
				ScaleIn: &managementusecase.NodeScaleInStatusResponse{
					NodeID:        4,
					GeneratedAt:   generatedAt,
					StateRevision: 18,
				},
				ActiveTasks: []managementusecase.ControllerTask{{
					TaskID: "task-1",
					SlotID: 12,
					Kind:   "slot_replica_move",
					Status: "running",
				}},
				TaskAudits: []managementusecase.ControllerTaskAuditSnapshot{{
					TaskID:     "task-1",
					Kind:       "slot_replica_move",
					Status:     "running",
					SlotID:     12,
					EventCount: 3,
				}},
				Slots: []managementusecase.DynamicNodeDiagnosticSlot{{
					SlotID:          12,
					DesiredPeers:    []uint64{4, 5, 6},
					PreferredLeader: 5,
					ConfigEpoch:     7,
					TaskID:          "task-1",
					TaskKind:        "slot_replica_move",
					TaskStep:        "replace",
					TaskStatus:      "running",
					CurrentLeader:   5,
					CurrentVoters:   []uint64{4, 5, 6},
				}},
				Summary: managementusecase.DynamicNodeDiagnosticSummary{
					SafeToRemove:             false,
					BlockedReasons:           []string{"blocked_by_tasks"},
					ActiveTaskCount:          1,
					FailedTaskCount:          0,
					SlotReplicaCount:         1,
					SlotLeaderCount:          0,
					ControlRevisionGap:       0,
					SlotReplicaMoveState:     "waiting_leader_transfer",
					OldestTaskAgeSeconds:     90,
					AuditAvailable:           true,
					RuntimeUnknown:           false,
					SlotRuntimeUnknown:       false,
					RecommendedNextAction:    "inspect_controller_task",
					BlockedByControlRevision: true,
					BlockedBySlots:           true,
					BlockedByTasks:           true,
				},
				Sources: managementusecase.DynamicNodeDiagnosticSources{
					ControlSnapshot: managementusecase.DynamicNodeDiagnosticSource{Available: true},
					TaskAudit:       managementusecase.DynamicNodeDiagnosticSource{Available: true},
					SlotRuntime:     managementusecase.DynamicNodeDiagnosticSource{Available: true},
				},
				Warnings: []string{"task audit approaching retention window"},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/4/diagnostics?task_limit=20&audit_limit=10&slot_limit=256", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if seen != (managementusecase.DynamicNodeDiagnosticsRequest{NodeID: 4, TaskLimit: 20, AuditLimit: 10, SlotLimit: 256}) {
		t.Fatalf("request = %#v", seen)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got := body["node_id"]; got != float64(4) {
		t.Fatalf("node_id = %#v, want 4", got)
	}
	if got := body["generated_at"]; got != generatedAt.Format(time.RFC3339Nano) {
		t.Fatalf("generated_at = %#v, want %q", got, generatedAt.Format(time.RFC3339Nano))
	}
	summary, ok := body["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary = %#v, want object", body["summary"])
	}
	if got := summary["recommended_next_action"]; got != "inspect_controller_task" {
		t.Fatalf("recommended_next_action = %#v, want inspect_controller_task", got)
	}
	if got := summary["blocked_by_control_revision"]; got != true {
		t.Fatalf("blocked_by_control_revision = %#v, want true", got)
	}
	if got := summary["blocked_by_slots"]; got != true {
		t.Fatalf("blocked_by_slots = %#v, want true", got)
	}
	if got := summary["blocked_by_tasks"]; got != true {
		t.Fatalf("blocked_by_tasks = %#v, want true", got)
	}
	sources, ok := body["sources"].(map[string]any)
	if !ok {
		t.Fatalf("sources = %#v, want object", body["sources"])
	}
	taskAudit, ok := sources["task_audit"].(map[string]any)
	if !ok {
		t.Fatalf("task_audit = %#v, want object", sources["task_audit"])
	}
	if got := taskAudit["available"]; got != true {
		t.Fatalf("task_audit.available = %#v, want true", got)
	}
}

func TestManagerDynamicNodeDiagnosticsRouteValidatesLimits(t *testing.T) {
	tests := []string{
		"/manager/nodes/4/diagnostics?task_limit=0",
		"/manager/nodes/4/diagnostics?task_limit=51",
		"/manager/nodes/4/diagnostics?audit_limit=0",
		"/manager/nodes/4/diagnostics?audit_limit=21",
		"/manager/nodes/4/diagnostics?slot_limit=0",
		"/manager/nodes/4/diagnostics?slot_limit=257",
	}

	for _, target := range tests {
		t.Run(target, func(t *testing.T) {
			var seen managementusecase.DynamicNodeDiagnosticsRequest
			srv := New(Options{
				Auth: testAuthConfig([]UserConfig{{
					Username: "reader",
					Password: "secret",
					Permissions: []PermissionConfig{{
						Resource: "cluster.node",
						Actions:  []string{"r"},
					}},
				}}),
				Management: managerNodesStub{dynamicNodeDiagnosticsReqSink: &seen},
			})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, target, nil)
			req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), `{"error":"bad_request","message":"invalid dynamic node diagnostics request"}`) {
				t.Fatalf("body = %s", rec.Body.String())
			}
			if seen != (managementusecase.DynamicNodeDiagnosticsRequest{}) {
				t.Fatalf("request = %#v, want zero request", seen)
			}
		})
	}
}

func TestManagerDynamicNodeDiagnosticsRouteMapsNotFound(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "reader",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{dynamicNodeDiagnosticsErr: managementusecase.ErrDynamicNodeDiagnosticsNotFound},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/4/diagnostics", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"error":"not_found","message":"dynamic node diagnostics not found"}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerDynamicNodeDiagnosticsRouteRequiresNodeReadPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	missing := httptest.NewRecorder()
	srv.Engine().ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/manager/nodes/4/diagnostics", nil))
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want %d", missing.Code, http.StatusUnauthorized)
	}

	denied := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/4/diagnostics", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))
	srv.Engine().ServeHTTP(denied, req)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d, want %d", denied.Code, http.StatusForbidden)
	}
}
