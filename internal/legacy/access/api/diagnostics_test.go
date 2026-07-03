package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/legacy/observability/diagnostics"
	"github.com/stretchr/testify/require"
)

type fakeDiagnosticsReader struct {
	mu     sync.Mutex
	query  diagnostics.Query
	result diagnostics.QueryResult
}

func (f *fakeDiagnosticsReader) QueryDiagnostics(_ context.Context, query diagnostics.Query) diagnostics.QueryResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.query = query
	return f.result
}

func (f *fakeDiagnosticsReader) lastQuery() diagnostics.Query {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.query
}

func TestDiagnosticsRoutesRequireEnable(t *testing.T) {
	reader := &fakeDiagnosticsReader{}
	srv := New(Options{Diagnostics: reader})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/diagnostics/events", nil)
	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDiagnosticsTraceRouteReturnsEvents(t *testing.T) {
	reader := &fakeDiagnosticsReader{result: diagnostics.QueryResult{
		Scope:      "local_node",
		NodeID:     1,
		TraceID:    "trace-1",
		Query:      diagnostics.Query{TraceID: "trace-1", Limit: 100},
		Status:     diagnostics.StatusOK,
		StartedAt:  time.Unix(1700000000, 0).UTC(),
		DurationMS: 3,
		Summary: diagnostics.QuerySummary{
			SlowestStage:      "message.send_durable",
			SlowestDurationMS: 12,
		},
		Events: []diagnostics.Event{{
			TraceID:  "trace-1",
			Stage:    diagnostics.Stage("message.send_durable"),
			At:       time.Unix(1700000000, 0).UTC(),
			Duration: 12 * time.Millisecond,
			NodeID:   1,
			Result:   diagnostics.ResultOK,
		}},
	}}
	srv := New(Options{DebugAPIEnabled: true, Diagnostics: reader})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/diagnostics/trace/trace-1", nil)
	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	require.Equal(t, "trace-1", reader.lastQuery().TraceID)
	require.Equal(t, 100, reader.lastQuery().Limit)
	body := rec.Body.String()
	require.Contains(t, body, "message.send_durable")
	require.Contains(t, body, "local_node")
	require.Contains(t, body, "trace_id")
	require.Contains(t, body, "node_id")
	require.Contains(t, body, "started_at")
	require.Contains(t, body, "duration_ms")
	require.Contains(t, body, "slowest_duration_ms")
}

func TestDiagnosticsMessageRouteQueriesClientMsgNo(t *testing.T) {
	reader := &fakeDiagnosticsReader{result: diagnostics.QueryResult{Status: diagnostics.StatusNotFound}}
	srv := New(Options{DebugAPIEnabled: true, Diagnostics: reader})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/diagnostics/message?client_msg_no=client-1", nil)
	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	query := reader.lastQuery()
	require.Equal(t, "client-1", query.ClientMsgNo)
	require.Empty(t, query.ChannelKey)
	require.Zero(t, query.MessageSeq)
	require.Equal(t, 100, query.Limit)
}

func TestDiagnosticsMessageRouteQueriesChannelSeq(t *testing.T) {
	reader := &fakeDiagnosticsReader{result: diagnostics.QueryResult{Status: diagnostics.StatusNotFound}}
	srv := New(Options{DebugAPIEnabled: true, Diagnostics: reader})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/diagnostics/message?channel_key=ch-1&message_seq=42", nil)
	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	query := reader.lastQuery()
	require.Equal(t, "ch-1", query.ChannelKey)
	require.Equal(t, uint64(42), query.MessageSeq)
	require.Empty(t, query.ClientMsgNo)
	require.Equal(t, 100, query.Limit)
}

func TestDiagnosticsMessageRouteValidatesQuery(t *testing.T) {
	reader := &fakeDiagnosticsReader{}
	srv := New(Options{DebugAPIEnabled: true, Diagnostics: reader})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/diagnostics/message", nil)
	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "error")
}

func TestDiagnosticsNotFoundReturnsStableBody(t *testing.T) {
	reader := &fakeDiagnosticsReader{result: diagnostics.QueryResult{
		Scope:  "local_node",
		NodeID: 2,
		Query:  diagnostics.Query{TraceID: "missing", Limit: 100},
		Status: diagnostics.StatusNotFound,
		Events: []diagnostics.Event{},
	}}
	srv := New(Options{DebugAPIEnabled: true, Diagnostics: reader})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/diagnostics/trace/missing", nil)
	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "not_found")
}

func TestDiagnosticsEventsLimit(t *testing.T) {
	reader := &fakeDiagnosticsReader{result: diagnostics.QueryResult{Status: diagnostics.StatusNotFound}}
	srv := New(Options{DebugAPIEnabled: true, Diagnostics: reader})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/diagnostics/events?stage=message.send_durable&limit=50", nil)
	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	query := reader.lastQuery()
	require.Equal(t, diagnostics.Stage("message.send_durable"), query.Stage)
	require.Equal(t, 50, query.Limit)
}

func TestDiagnosticsEventsRejectsInvalidLimit(t *testing.T) {
	reader := &fakeDiagnosticsReader{}
	srv := New(Options{DebugAPIEnabled: true, Diagnostics: reader})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/diagnostics/events?limit=bad", nil)
	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "error")
}

func TestDiagnosticsEventsRejectsOverMaxLimit(t *testing.T) {
	reader := &fakeDiagnosticsReader{}
	srv := New(Options{DebugAPIEnabled: true, Diagnostics: reader})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/diagnostics/events?limit=501", nil)
	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "error")
}
