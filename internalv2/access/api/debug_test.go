package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
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

func TestDebugGoroutinesRequiresDebugAPIEnable(t *testing.T) {
	disabled := httptest.NewServer(New(Options{}).Handler())
	t.Cleanup(disabled.Close)
	resp, err := http.Get(disabled.URL + "/debug/goroutines")
	requireStatus(t, resp, err, http.StatusNotFound)

	enabled := httptest.NewServer(New(Options{DebugAPIEnabled: true}).Handler())
	t.Cleanup(enabled.Close)
	resp, err = http.Get(enabled.URL + "/debug/goroutines")
	requireStatus(t, resp, err, http.StatusOK)
}

func TestDebugSnapshotRoutesRequireDebugAPIEnable(t *testing.T) {
	disabled := httptest.NewServer(New(Options{
		DebugConfig:  func() any { return map[string]any{"node_id": 1} },
		DebugCluster: func() any { return map[string]any{"cluster_id": "wk"} },
	}).Handler())
	t.Cleanup(disabled.Close)
	resp, err := http.Get(disabled.URL + "/debug/config")
	requireStatus(t, resp, err, http.StatusNotFound)

	enabled := httptest.NewServer(New(Options{
		DebugAPIEnabled: true,
		DebugConfig: func() any {
			return map[string]any{"node_id": 1}
		},
		DebugCluster: func() any {
			return map[string]any{"cluster_id": "wk"}
		},
	}).Handler())
	t.Cleanup(enabled.Close)
	resp, err = http.Get(enabled.URL + "/debug/config")
	requireStatus(t, resp, err, http.StatusOK)
	resp, err = http.Get(enabled.URL + "/debug/cluster")
	requireStatus(t, resp, err, http.StatusOK)
}

func TestDiagnosticsDebugRoutesRequireEnable(t *testing.T) {
	reader := &fakeDiagnosticsReader{}
	srv := New(Options{Diagnostics: reader})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	resp, err := http.Get(httpSrv.URL + "/debug/diagnostics/events")
	requireStatus(t, resp, err, http.StatusNotFound)
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
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	resp, err := http.Get(httpSrv.URL + "/debug/diagnostics/trace/trace-1")
	requireStatus(t, resp, err, http.StatusOK)

	query := reader.lastQuery()
	if query.TraceID != "trace-1" || query.Limit != 100 {
		t.Fatalf("diagnostics query = %#v, want trace-1 limit 100", query)
	}
}

func TestDiagnosticsMessageRouteQueriesSelectors(t *testing.T) {
	reader := &fakeDiagnosticsReader{result: diagnostics.QueryResult{Status: diagnostics.StatusNotFound}}
	srv := New(Options{DebugAPIEnabled: true, Diagnostics: reader})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	resp, err := http.Get(httpSrv.URL + "/debug/diagnostics/message?client_msg_no=client-1")
	requireStatus(t, resp, err, http.StatusOK)
	query := reader.lastQuery()
	if query.ClientMsgNo != "client-1" || query.ChannelKey != "" || query.MessageSeq != 0 || query.Limit != 100 {
		t.Fatalf("client_msg_no query = %#v, want client selector", query)
	}

	resp, err = http.Get(httpSrv.URL + "/debug/diagnostics/message?channel_key=ch-1&message_seq=42&limit=50")
	requireStatus(t, resp, err, http.StatusOK)
	query = reader.lastQuery()
	if query.ChannelKey != "ch-1" || query.MessageSeq != 42 || query.ClientMsgNo != "" || query.Limit != 50 {
		t.Fatalf("channel seq query = %#v, want channel selector", query)
	}
}

func TestDiagnosticsDebugRoutesValidateQuery(t *testing.T) {
	reader := &fakeDiagnosticsReader{}
	srv := New(Options{DebugAPIEnabled: true, Diagnostics: reader})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	resp, err := http.Get(httpSrv.URL + "/debug/diagnostics/message")
	requireStatus(t, resp, err, http.StatusBadRequest)

	resp, err = http.Get(httpSrv.URL + "/debug/diagnostics/events?limit=501")
	requireStatus(t, resp, err, http.StatusBadRequest)
}

func TestDiagnosticsEventsRouteMapsStageAndResult(t *testing.T) {
	reader := &fakeDiagnosticsReader{result: diagnostics.QueryResult{Status: diagnostics.StatusNotFound}}
	srv := New(Options{DebugAPIEnabled: true, Diagnostics: reader})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	resp, err := http.Get(httpSrv.URL + "/debug/diagnostics/events?stage=message.send_durable&result=error")
	requireStatus(t, resp, err, http.StatusOK)
	query := reader.lastQuery()
	if query.Stage != diagnostics.Stage("message.send_durable") || query.Result != diagnostics.ResultError || query.Limit != 100 {
		t.Fatalf("events query = %#v, want stage/result filters", query)
	}
}
