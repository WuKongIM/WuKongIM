package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

func TestManagerControllerRaftStatusReturnsNodeScopedStatus(t *testing.T) {
	var nodeID uint64
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
			controllerRaftStatusNodeSink: &nodeID,
			controllerRaftStatus: managementusecase.ControllerRaftStatus{
				NodeID:        2,
				Role:          "leader",
				LeaderID:      2,
				Term:          7,
				Health:        "healthy",
				FirstIndex:    11,
				LastIndex:     48,
				CommitIndex:   47,
				AppliedIndex:  46,
				Voters:        []uint64{1, 2},
				Learners:      []uint64{4},
				SnapshotIndex: 30,
				SnapshotTerm:  5,
				Compaction: managementusecase.ControllerRaftCompaction{
					Enabled:           true,
					TriggerEntries:    1000,
					CheckInterval:     5 * time.Second,
					LastSnapshotIndex: 30,
					LastSnapshotAt:    time.Date(2026, 5, 7, 10, 1, 2, 0, time.UTC),
					LastCheckAt:       time.Date(2026, 5, 7, 10, 2, 3, 0, time.UTC),
					LastError:         "snapshot busy",
					LastErrorAt:       time.Date(2026, 5, 7, 10, 2, 4, 0, time.UTC),
					Degraded:          true,
				},
				Restore: managementusecase.ControllerRaftRestore{
					LastSnapshotIndex: 25,
					LastSnapshotTerm:  4,
					LastRestoredAt:    time.Date(2026, 5, 7, 9, 55, 0, 0, time.UTC),
				},
				Peers: []managementusecase.ControllerRaftPeer{{
					NodeID:               3,
					Match:                40,
					Next:                 41,
					State:                "snapshot",
					PendingSnapshot:      30,
					RecentActive:         true,
					NeedsSnapshot:        true,
					SnapshotTransferring: true,
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/2/controller-raft", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if nodeID != 2 {
		t.Fatalf("nodeID = %d, want 2", nodeID)
	}
	if !jsonEqual(rec.Body.String(), `{
		"node_id": 2,
		"role": "leader",
		"leader_id": 2,
		"term": 7,
		"health": "healthy",
		"first_index": 11,
		"last_index": 48,
		"commit_index": 47,
		"applied_index": 46,
		"voters": [1, 2],
		"learners": [4],
		"snapshot_index": 30,
		"snapshot_term": 5,
		"compaction": {
			"enabled": true,
			"trigger_entries": 1000,
			"check_interval_ms": 5000,
			"last_snapshot_index": 30,
			"last_snapshot_at": "2026-05-07T10:01:02Z",
			"last_check_at": "2026-05-07T10:02:03Z",
			"last_error": "snapshot busy",
			"last_error_at": "2026-05-07T10:02:04Z",
			"degraded": true
		},
		"restore": {
			"last_snapshot_index": 25,
			"last_snapshot_term": 4,
			"last_restored_at": "2026-05-07T09:55:00Z",
			"last_error": "",
			"last_error_at": "",
			"failed": false
		},
		"peers": [{
			"node_id": 3,
			"match": 40,
			"next": 41,
			"state": "snapshot",
			"pending_snapshot": 30,
			"recent_active": true,
			"needs_snapshot": true,
			"snapshot_transferring": true
		}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerControllerRaftNodeCompactRequiresWritePermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "reader",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.controller",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/2/controller-raft/compact", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestManagerControllerRaftNodeCompactReturnsSingleNodeSummary(t *testing.T) {
	var nodeID uint64
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.controller",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managerNodesStub{
			controllerRaftCompactNodeSink: &nodeID,
			controllerRaftCompactResult: managementusecase.ControllerRaftCompactionResult{
				NodeID:              2,
				AppliedIndex:        50,
				BeforeSnapshotIndex: 30,
				AfterSnapshotIndex:  50,
				Compacted:           true,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/2/controller-raft/compact", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if nodeID != 2 {
		t.Fatalf("nodeID = %d, want 2", nodeID)
	}
	var body ManagerControllerRaftCompactResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	if body.GeneratedAt == "" {
		t.Fatalf("generated_at is empty; body=%s", rec.Body.String())
	}
	if body.Total != 1 || body.Succeeded != 1 || body.Failed != 0 || len(body.Items) != 1 {
		t.Fatalf("summary = %#v, want one successful node item", body)
	}
	if item := body.Items[0]; item.NodeID != 2 || !item.Success || !item.Compacted || item.AppliedIndex != 50 || item.BeforeSnapshotIndex != 30 || item.AfterSnapshotIndex != 50 {
		t.Fatalf("item = %#v, want compacted node 2", item)
	}
}

func TestManagerControllerRaftCompactAllReturnsSummary(t *testing.T) {
	generatedAt := time.Date(2026, 5, 7, 11, 0, 0, 0, time.UTC)
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.controller",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managerNodesStub{
			controllerRaftCompactSummary: managementusecase.ControllerRaftCompactionSummary{
				GeneratedAt: generatedAt,
				Total:       2,
				Succeeded:   1,
				Failed:      1,
				Items: []managementusecase.ControllerRaftCompactNodeResult{{
					NodeID:              1,
					Success:             true,
					AppliedIndex:        42,
					BeforeSnapshotIndex: 30,
					AfterSnapshotIndex:  42,
					Compacted:           true,
				}, {
					NodeID: 2,
					Error:  "node stopped",
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/controller-raft/compact", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{
		"generated_at": "2026-05-07T11:00:00Z",
		"total": 2,
		"succeeded": 1,
		"failed": 1,
		"items": [{
			"node_id": 1,
			"success": true,
			"applied_index": 42,
			"before_snapshot_index": 30,
			"after_snapshot_index": 42,
			"compacted": true,
			"skipped_reason": "",
			"error": ""
		}, {
			"node_id": 2,
			"success": false,
			"applied_index": 0,
			"before_snapshot_index": 0,
			"after_snapshot_index": 0,
			"compacted": false,
			"skipped_reason": "",
			"error": "node stopped"
		}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}
