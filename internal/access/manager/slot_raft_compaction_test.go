package manager

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

func TestManagerSlotRaftCompactReturnsNodeSlotSummary(t *testing.T) {
	generatedAt := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	var nodeID uint64
	var slotID uint32
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managerNodesStub{
			slotRaftCompactNodeSink: &nodeID,
			slotRaftCompactSlotSink: &slotID,
			slotRaftCompactSummary: managementusecase.SlotRaftCompactionSummary{
				GeneratedAt: generatedAt,
				Total:       1,
				Succeeded:   1,
				Items: []managementusecase.SlotRaftCompactNodeResult{{
					NodeID:              2,
					SlotID:              9,
					Success:             true,
					AppliedIndex:        50,
					BeforeSnapshotIndex: 30,
					AfterSnapshotIndex:  50,
					Compacted:           true,
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/2/slots/9/compact", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if nodeID != 2 || slotID != 9 {
		t.Fatalf("target = node %d slot %d, want 2/9", nodeID, slotID)
	}
	if !jsonEqual(rec.Body.String(), `{
		"generated_at": "2026-06-19T10:00:00Z",
		"total": 1,
		"succeeded": 1,
		"failed": 0,
		"items": [{
			"node_id": 2,
			"slot_id": 9,
			"success": true,
			"applied_index": 50,
			"before_snapshot_index": 30,
			"after_snapshot_index": 50,
			"compacted": true,
			"skipped_reason": "",
			"error": ""
		}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerSlotRaftCompactRequiresSlotWritePermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "reader",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/2/slots/9/compact", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}
