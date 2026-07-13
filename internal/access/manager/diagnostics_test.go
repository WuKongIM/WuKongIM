package manager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

func TestManagerDiagnosticsTraceReturnsAggregate(t *testing.T) {
	var received managementusecase.DiagnosticsQueryRequest
	generatedAt := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.diagnostics",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{
			diagnosticsReqSink: &received,
			diagnosticsResponse: managementusecase.DiagnosticsQueryResponse{
				Scope:       "cluster",
				Status:      managementusecase.DiagnosticsStatusOK,
				GeneratedAt: generatedAt,
				Query:       diagnostics.Query{TraceID: "tr-1", Limit: 50},
				Summary: managementusecase.DiagnosticsSummary{
					SlowestStage:      "channel_append",
					SlowestDurationMS: 12,
					InvolvedNodes:     []uint64{1, 2},
					EventCount:        1,
				},
				Nodes: []managementusecase.DiagnosticsNodeResult{{
					NodeID:     2,
					Status:     "ok",
					DurationMS: 4,
					EventCount: 1,
					Notes:      []string{"node note"},
				}},
				Events: []managementusecase.DiagnosticsEvent{{
					TraceID:     "tr-1",
					Stage:       "channel_append",
					At:          generatedAt.Add(-time.Second),
					DurationMS:  12,
					NodeID:      2,
					ChannelKey:  "channel/2/ZzE",
					ClientMsgNo: "client-1",
					MessageSeq:  9,
					Result:      "ok",
				}},
				Notes: []string{"aggregate note"},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/diagnostics/trace/tr-1?limit=50&node_id=2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if received.NodeID != 2 || received.Query.TraceID != "tr-1" || received.Query.Limit != 50 {
		t.Fatalf("request = %#v, want node 2 trace tr-1 limit 50", received)
	}
	if !jsonEqual(rec.Body.String(), `{
		"scope": "cluster",
		"status": "ok",
		"generated_at": "2026-06-19T10:30:00Z",
		"query": {"trace_id": "tr-1", "limit": 50},
		"summary": {
			"first_failure_stage": "",
			"first_failure_result": "",
			"first_failure_error_code": "",
			"slowest_stage": "channel_append",
			"slowest_duration_ms": 12,
			"involved_nodes": [1, 2],
			"peer_nodes": [],
			"slot_id": 0,
			"channel_key": "",
			"client_msg_no": "",
			"message_seq": 0,
			"event_count": 1
		},
		"nodes": [{
			"node_id": 2,
			"status": "ok",
			"duration_ms": 4,
			"event_count": 1,
			"notes": ["node note"]
		}],
		"events": [{
			"trace_id": "tr-1",
			"span_id": "",
			"parent_span_id": "",
			"stage": "channel_append",
			"at": "2026-06-19T10:29:59Z",
			"duration_ms": 12,
			"node_id": 2,
			"peer_node_id": 0,
			"slot_id": 0,
			"channel_key": "channel/2/ZzE",
			"client_msg_no": "client-1",
			"message_seq": 9,
			"range_start": 0,
			"range_end": 0,
			"service": "",
			"result": "ok",
			"error_code": "",
			"error": "",
			"attempt": 0,
			"request_count": 0,
			"record_count": 0,
			"byte_count": 0,
			"queue_depth": 0,
			"replica_role": "",
			"sample_reason": ""
		}],
		"notes": ["aggregate note"]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerDiagnosticsTrackingRoutesRequireWritePermission(t *testing.T) {
	var received managementusecase.DiagnosticsTrackingCreateRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "diag-viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.diagnostics",
				Actions:  []string{"r"},
			}},
		}, {
			Username: "diag-writer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.diagnostics",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managerNodesStub{
			diagnosticsTrackingCreateReqSink: &received,
			diagnosticsTrackingCreateResponse: managementusecase.DiagnosticsTrackingMutationResponse{
				Status: managementusecase.DiagnosticsTrackingStatusOK,
				Rule:   managementusecase.DiagnosticsTrackingRule{ID: "rule-1", Target: "sender_uid", UID: "u1", SampleRate: 1},
			},
		},
	})

	denied := httptest.NewRecorder()
	deniedReq := httptest.NewRequest(http.MethodPost, "/manager/diagnostics/tracking-rules", nil)
	deniedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "diag-viewer"))
	srv.Engine().ServeHTTP(denied, deniedReq)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d, want forbidden; body=%s", denied.Code, denied.Body.String())
	}

	allowed := httptest.NewRecorder()
	allowedReq := httptest.NewRequest(http.MethodPost, "/manager/diagnostics/tracking-rules", strings.NewReader(`{"node_id":2,"target":"sender_uid","uid":"u1","ttl_seconds":60,"sample_rate":1}`))
	allowedReq.Header.Set("Content-Type", "application/json")
	allowedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "diag-writer"))
	srv.Engine().ServeHTTP(allowed, allowedReq)
	if allowed.Code != http.StatusOK {
		t.Fatalf("allowed status = %d, want ok; body=%s", allowed.Code, allowed.Body.String())
	}
	if received.NodeID != 2 || received.Target != "sender_uid" || received.UID != "u1" || received.TTLSeconds != 60 || received.SampleRate != 1 {
		t.Fatalf("tracking create request = %#v, want node 2 sender u1 ttl 60 sample 1", received)
	}
}
