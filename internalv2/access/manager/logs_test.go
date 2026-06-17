package manager

import (
	"net/http"
	"net/http/httptest"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagerControllerLogsReturnsNodeScopedEntries(t *testing.T) {
	var reqSink managementusecase.ListControllerLogEntriesRequest
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
			controllerLogEntriesReqSink: &reqSink,
			controllerLogEntriesPage: managementusecase.ControllerLogEntriesResponse{
				NodeID:       2,
				FirstIndex:   1,
				LastIndex:    4,
				CommitIndex:  4,
				AppliedIndex: 3,
				NextCursor:   3,
				Items: []managementusecase.ControllerLogEntry{{
					Index:        4,
					Term:         2,
					Type:         "normal",
					DataSize:     12,
					DecodeStatus: "ok",
					DecodedType:  "init_cluster_state",
					Decoded:      map[string]any{"command": "init_cluster_state"},
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/controller/logs?node_id=2&limit=2&cursor=5", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if reqSink != (managementusecase.ListControllerLogEntriesRequest{NodeID: 2, Limit: 2, Cursor: 5}) {
		t.Fatalf("request = %#v, want node 2 limit 2 cursor 5", reqSink)
	}
	if !jsonEqual(rec.Body.String(), `{
		"node_id": 2,
		"first_index": 1,
		"last_index": 4,
		"commit_index": 4,
		"applied_index": 3,
		"next_cursor": 3,
		"items": [{
			"index": 4,
			"term": 2,
			"type": "normal",
			"data_size": 12,
			"decode_status": "ok",
			"decoded_type": "init_cluster_state",
			"decoded": {"command": "init_cluster_state"}
		}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerSlotLogsReturnsNodeScopedEntries(t *testing.T) {
	var reqSink managementusecase.ListSlotLogEntriesRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{
			slotLogEntriesReqSink: &reqSink,
			slotLogEntriesPage: managementusecase.SlotLogEntriesResponse{
				NodeID:       2,
				SlotID:       9,
				FirstIndex:   1,
				LastIndex:    4,
				CommitIndex:  4,
				AppliedIndex: 3,
				NextCursor:   3,
				Items: []managementusecase.SlotLogEntry{{
					Index:        4,
					Term:         2,
					Type:         "normal",
					DataSize:     12,
					DecodeStatus: "ok",
					DecodedType:  "upsert_user",
					Decoded:      map[string]any{"command": "upsert_user", "uid": "u1"},
				}, {
					Index:    3,
					Term:     2,
					Type:     "conf_change",
					DataSize: 8,
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/slots/9/logs?node_id=2&limit=2&cursor=5", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if reqSink != (managementusecase.ListSlotLogEntriesRequest{NodeID: 2, SlotID: 9, Limit: 2, Cursor: 5}) {
		t.Fatalf("request = %#v, want node 2 slot 9 limit 2 cursor 5", reqSink)
	}
	if !jsonEqual(rec.Body.String(), `{
		"node_id": 2,
		"slot_id": 9,
		"first_index": 1,
		"last_index": 4,
		"commit_index": 4,
		"applied_index": 3,
		"next_cursor": 3,
		"items": [{
			"index": 4,
			"term": 2,
			"type": "normal",
			"data_size": 12,
			"decode_status": "ok",
			"decoded_type": "upsert_user",
			"decoded": {"command": "upsert_user", "uid": "u1"}
		}, {
			"index": 3,
			"term": 2,
			"type": "conf_change",
			"data_size": 8
		}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}
