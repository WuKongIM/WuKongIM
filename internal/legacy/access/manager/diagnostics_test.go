package manager

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/legacy/observability/diagnostics"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/management"
	"github.com/stretchr/testify/require"
)

func TestManagerDiagnosticsTraceReturnsAggregate(t *testing.T) {
	var received managementusecase.DiagnosticsQueryRequest
	generatedAt := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	eventAt := time.Date(2026, 5, 6, 11, 59, 59, 0, time.UTC)
	srv := newDiagnosticsTestServer(t, diagnosticsHTTPStub{
		reqSink: &received,
		response: managementusecase.DiagnosticsQueryResponse{
			Scope:       "local_node",
			Status:      managementusecase.DiagnosticsStatusOK,
			GeneratedAt: generatedAt,
			Query:       diagnostics.Query{TraceID: "tr-1", Limit: 50},
			Summary: managementusecase.DiagnosticsSummary{
				FirstFailureStage:     "channel_append",
				FirstFailureResult:    "error",
				FirstFailureErrorCode: "unknown_error",
				SlowestStage:          "replica_quorum",
				SlowestDurationMS:     42,
				InvolvedNodes:         []uint64{2},
				PeerNodes:             []uint64{3},
				SlotID:                12,
				ChannelKey:            "2:g1",
				ClientMsgNo:           "c-1",
				MessageSeq:            9,
				EventCount:            1,
			},
			Nodes: []managementusecase.DiagnosticsNodeResult{{NodeID: 2, Status: "ok", DurationMS: 3, EventCount: 1, Notes: []string{"node note"}}},
			Events: []managementusecase.DiagnosticsEvent{{
				TraceID:      "tr-1",
				SpanID:       "span-1",
				ParentSpanID: "parent-1",
				Stage:        "channel_append",
				At:           eventAt,
				DurationMS:   7,
				NodeID:       2,
				PeerNodeID:   3,
				SlotID:       12,
				ChannelKey:   "2:g1",
				ClientMsgNo:  "c-1",
				MessageSeq:   9,
				RangeStart:   9,
				RangeEnd:     10,
				Service:      "channel",
				Result:       "ok",
				ErrorCode:    "",
				Attempt:      2,
				RequestCount: 3,
				RecordCount:  17,
				ByteCount:    4096,
				QueueDepth:   5,
				ReplicaRole:  "leader",
				SampleReason: "slow",
			}},
			Notes: []string{"aggregate note"},
		},
	})

	rec := performDiagnosticsRequest(t, srv, "/manager/diagnostics/trace/tr-1?limit=50&node_id=2", "admin")

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(2), received.NodeID)
	require.Equal(t, diagnostics.Query{TraceID: "tr-1", Limit: 50}, received.Query)
	require.JSONEq(t, `{
		"scope":"local_node",
		"status":"ok",
		"generated_at":"2026-05-06T12:00:00Z",
		"query":{"trace_id":"tr-1","limit":50},
		"summary":{
			"first_failure_stage":"channel_append",
			"first_failure_result":"error",
			"first_failure_error_code":"unknown_error",
			"slowest_stage":"replica_quorum",
			"slowest_duration_ms":42,
			"involved_nodes":[2],
			"peer_nodes":[3],
			"slot_id":12,
			"channel_key":"2:g1",
			"client_msg_no":"c-1",
			"message_seq":9,
			"event_count":1
		},
		"nodes":[{"node_id":2,"status":"ok","duration_ms":3,"event_count":1,"notes":["node note"]}],
		"events":[{
			"trace_id":"tr-1",
			"span_id":"span-1",
			"parent_span_id":"parent-1",
			"stage":"channel_append",
			"at":"2026-05-06T11:59:59Z",
			"duration_ms":7,
			"node_id":2,
			"peer_node_id":3,
			"slot_id":12,
			"channel_key":"2:g1",
			"client_msg_no":"c-1",
			"message_seq":9,
			"range_start":9,
			"range_end":10,
			"service":"channel",
			"result":"ok",
			"error_code":"",
			"error":"",
			"attempt":2,
			"request_count":3,
			"record_count":17,
			"byte_count":4096,
			"queue_depth":5,
			"replica_role":"leader",
			"sample_reason":"slow"
		}],
		"notes":["aggregate note"]
	}`, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "duration\"")
}

func TestManagerDiagnosticsMessageQueriesClientMsgNo(t *testing.T) {
	var received managementusecase.DiagnosticsQueryRequest
	srv := newDiagnosticsTestServer(t, diagnosticsHTTPStub{reqSink: &received, response: diagnosticsHTTPResponse()})

	rec := performDiagnosticsRequest(t, srv, "/manager/diagnostics/message?client_msg_no=c-1", "admin")

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, diagnostics.Query{ClientMsgNo: "c-1", Limit: 100}, received.Query)
}

func TestManagerDiagnosticsMessageQueriesChannelSeq(t *testing.T) {
	var received managementusecase.DiagnosticsQueryRequest
	srv := newDiagnosticsTestServer(t, diagnosticsHTTPStub{reqSink: &received, response: diagnosticsHTTPResponse()})

	rec := performDiagnosticsRequest(t, srv, "/manager/diagnostics/message?channel_key=2:g1&message_seq=9", "admin")

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, diagnostics.Query{ChannelKey: "2:g1", MessageSeq: 9, Limit: 100}, received.Query)
}

func TestManagerDiagnosticsMessageRejectsMixedLookupModes(t *testing.T) {
	srv := newDiagnosticsTestServer(t, diagnosticsHTTPStub{response: diagnosticsHTTPResponse()})

	rec := performDiagnosticsRequest(t, srv, "/manager/diagnostics/message?client_msg_no=c-1&channel_key=2:g1&message_seq=9", "admin")

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid message selector"}`, rec.Body.String())
}

func TestManagerDiagnosticsMessageRejectsMissingLookupMode(t *testing.T) {
	srv := newDiagnosticsTestServer(t, diagnosticsHTTPStub{response: diagnosticsHTTPResponse()})

	rec := performDiagnosticsRequest(t, srv, "/manager/diagnostics/message", "admin")

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid message selector"}`, rec.Body.String())
}

func TestManagerDiagnosticsEventsQueriesStageAndResult(t *testing.T) {
	var received managementusecase.DiagnosticsQueryRequest
	srv := newDiagnosticsTestServer(t, diagnosticsHTTPStub{reqSink: &received, response: diagnosticsHTTPResponse()})

	rec := performDiagnosticsRequest(t, srv, "/manager/diagnostics/events?stage=channel_append&result=error", "admin")

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, diagnostics.Query{Stage: diagnostics.Stage("channel_append"), Result: diagnostics.ResultError, Limit: 100}, received.Query)
}

func TestManagerDiagnosticsEventsQueriesUIDAndChannelKey(t *testing.T) {
	var received managementusecase.DiagnosticsQueryRequest
	srv := newDiagnosticsTestServer(t, diagnosticsHTTPStub{reqSink: &received, response: diagnosticsHTTPResponse()})

	rec := performDiagnosticsRequest(t, srv, "/manager/diagnostics/events?uid=u1&channel_key=channel%2F2%2FZzE", "admin")

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, diagnostics.Query{UID: "u1", ChannelKey: "channel/2/ZzE", Limit: 100}, received.Query)
}

func TestManagerDiagnosticsRejectsInvalidResult(t *testing.T) {
	srv := newDiagnosticsTestServer(t, diagnosticsHTTPStub{response: diagnosticsHTTPResponse()})

	rec := performDiagnosticsRequest(t, srv, "/manager/diagnostics/events?result=bad", "admin")

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid result"}`, rec.Body.String())
}

func TestManagerDiagnosticsRejectsInvalidLimit(t *testing.T) {
	srv := newDiagnosticsTestServer(t, diagnosticsHTTPStub{response: diagnosticsHTTPResponse()})
	for _, path := range []string{
		"/manager/diagnostics/events?limit=0",
		"/manager/diagnostics/events?limit=bad",
		"/manager/diagnostics/events?limit=501",
	} {
		t.Run(path, func(t *testing.T) {
			rec := performDiagnosticsRequest(t, srv, path, "admin")
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.JSONEq(t, `{"error":"bad_request","message":"invalid limit"}`, rec.Body.String())
		})
	}
}

func TestManagerDiagnosticsRejectsInvalidNodeID(t *testing.T) {
	srv := newDiagnosticsTestServer(t, diagnosticsHTTPStub{response: diagnosticsHTTPResponse()})
	for _, path := range []string{
		"/manager/diagnostics/events?node_id=0",
		"/manager/diagnostics/events?node_id=bad",
	} {
		t.Run(path, func(t *testing.T) {
			rec := performDiagnosticsRequest(t, srv, path, "admin")
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.JSONEq(t, `{"error":"bad_request","message":"invalid node_id"}`, rec.Body.String())
		})
	}
}

func TestManagerDiagnosticsRequiresPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{
			{Username: "node-viewer", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.node", Actions: []string{"r"}}}},
			{Username: "diag-viewer", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.diagnostics", Actions: []string{"r"}}}},
		}),
		Management: diagnosticsHTTPStub{response: diagnosticsHTTPResponse()},
	})

	denied := performDiagnosticsRequest(t, srv, "/manager/diagnostics/events", "node-viewer")
	require.Equal(t, http.StatusForbidden, denied.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, denied.Body.String())

	allowed := performDiagnosticsRequest(t, srv, "/manager/diagnostics/events", "diag-viewer")
	require.Equal(t, http.StatusOK, allowed.Code)
}

func TestManagerDiagnosticsReturnsUnavailableWhenManagementMissing(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username:    "admin",
			Password:    "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.diagnostics", Actions: []string{"r"}}},
		}}),
	})

	rec := performDiagnosticsRequest(t, srv, "/manager/diagnostics/events", "admin")

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.JSONEq(t, `{"error":"service_unavailable","message":"management not configured"}`, rec.Body.String())
}

func newDiagnosticsTestServer(t *testing.T, management Management) *Server {
	t.Helper()
	return New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username:    "admin",
			Password:    "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.diagnostics", Actions: []string{"r"}}},
		}}),
		Management: management,
	})
}

func performDiagnosticsRequest(t *testing.T, srv *Server, path, username string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, username))
	srv.Engine().ServeHTTP(rec, req)
	return rec
}

type diagnosticsHTTPStub struct {
	managementStub
	reqSink  *managementusecase.DiagnosticsQueryRequest
	response managementusecase.DiagnosticsQueryResponse
	err      error
}

func (s diagnosticsHTTPStub) QueryDiagnostics(_ context.Context, req managementusecase.DiagnosticsQueryRequest) (managementusecase.DiagnosticsQueryResponse, error) {
	if s.reqSink != nil {
		*s.reqSink = req
	}
	return s.response, s.err
}

func diagnosticsHTTPResponse() managementusecase.DiagnosticsQueryResponse {
	return managementusecase.DiagnosticsQueryResponse{
		Scope:       "cluster",
		Status:      managementusecase.DiagnosticsStatusOK,
		GeneratedAt: time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC),
	}
}

func TestManagerDiagnosticsUnexpectedUsecaseErrorReturnsInternalError(t *testing.T) {
	srv := newDiagnosticsTestServer(t, diagnosticsHTTPStub{err: errors.New("boom")})

	rec := performDiagnosticsRequest(t, srv, "/manager/diagnostics/events", "admin")

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.JSONEq(t, `{"error":"internal_error","message":"diagnostics query failed"}`, rec.Body.String())
}
