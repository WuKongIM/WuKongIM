package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
)

func TestLegacyRouteAcceptsAllSupportedNodeIDSpellings(t *testing.T) {
	srv := New(Options{LegacyRouteNodes: map[uint64]LegacyRouteNodeAddresses{
		7: {
			External: LegacyRouteAddresses{TCPAddr: "198.51.100.7:5100"},
			Intranet: LegacyRouteAddresses{TCPAddr: "10.0.0.7:5100"},
		},
	}})

	for _, key := range []string{"node_id", "nodeId", "nodeID"} {
		t.Run(key, func(t *testing.T) {
			rec := serveAPIRequest(t, srv, http.MethodGet, "/route?"+key+"=7&intranet=1", "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), `{"tcp_addr":"10.0.0.7:5100","ws_addr":"","wss_addr":""}`) {
				t.Fatalf("body = %s, want node-specific intranet route", rec.Body.String())
			}
		})
	}
}

func TestLegacyRouteRejectsInvalidOrUnknownNode(t *testing.T) {
	srv := New(Options{LegacyRouteNodes: map[uint64]LegacyRouteNodeAddresses{
		7: {External: LegacyRouteAddresses{TCPAddr: "198.51.100.7:5100"}},
	}})

	for _, query := range []string{
		"node_id=",
		"node_id=0",
		"node_id=-1",
		"node_id=invalid",
		"node_id=8",
		"node_id=invalid&nodeId=7",
	} {
		t.Run(query, func(t *testing.T) {
			rec := serveAPIRequest(t, srv, http.MethodGet, "/route?"+query, "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), `{"msg":"节点参数有误！","status":400}`) {
				t.Fatalf("body = %s, want stable invalid-node envelope", rec.Body.String())
			}
		})
	}

	rec := serveAPIRequest(t, srv, http.MethodPost, "/route/batch?nodeID=8", `["u1"]`)
	if rec.Code != http.StatusBadRequest || !jsonEqual(rec.Body.String(), `{"msg":"节点参数有误！","status":400}`) {
		t.Fatalf("batch status = %d body = %s, want stable invalid-node envelope", rec.Code, rec.Body.String())
	}
}

func TestDebugGoroutineSummaryIsExplicitlyOptional(t *testing.T) {
	missing := New(Options{DebugAPIEnabled: true})
	rec := serveAPIRequest(t, missing, http.MethodGet, "/debug/goroutines/summary", "")
	if rec.Code != http.StatusNotFound || !jsonEqual(rec.Body.String(), `{"error":"goroutine registry not configured"}`) {
		t.Fatalf("missing summary status = %d body = %s", rec.Code, rec.Body.String())
	}

	configured := New(Options{
		DebugAPIEnabled: true,
		GoroutineSnapshot: func() any {
			return map[string]any{"running": 3, "rejected": 1}
		},
	})
	rec = serveAPIRequest(t, configured, http.MethodGet, "/debug/goroutines/summary", "")
	if rec.Code != http.StatusOK || !jsonEqual(rec.Body.String(), `{"rejected":1,"running":3}`) {
		t.Fatalf("configured summary status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestDiagnosticsResultFilterUsesBoundedAllowlist(t *testing.T) {
	reader := &fakeDiagnosticsReader{}
	srv := New(Options{DebugAPIEnabled: true, Diagnostics: reader})

	for _, want := range []diagnostics.Result{
		"",
		diagnostics.ResultOK,
		diagnostics.ResultError,
		diagnostics.ResultTimeout,
		diagnostics.ResultCanceled,
		diagnostics.ResultPartial,
		diagnostics.ResultDropped,
		diagnostics.ResultSkipped,
	} {
		name := string(want)
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			rec := serveAPIRequest(t, srv, http.MethodGet, "/debug/diagnostics/events?result="+string(want), "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
			}
			if got := reader.lastQuery().Result; got != want {
				t.Fatalf("result = %q, want %q", got, want)
			}
		})
	}

	rec := serveAPIRequest(t, srv, http.MethodGet, "/debug/diagnostics/events?result=success-ish", "")
	if rec.Code != http.StatusBadRequest || !jsonEqual(rec.Body.String(), `{"error":"invalid result"}`) {
		t.Fatalf("invalid result status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestDiagnosticsMessageRejectsAmbiguousAndUnboundedSelectors(t *testing.T) {
	reader := &fakeDiagnosticsReader{}
	srv := New(Options{DebugAPIEnabled: true, Diagnostics: reader})

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "mixed selectors", path: "/debug/diagnostics/message?client_msg_no=c1&channel_key=g1&message_seq=1", want: "invalid message selector"},
		{name: "missing message sequence", path: "/debug/diagnostics/message?channel_key=g1", want: "missing diagnostics message lookup"},
		{name: "zero message sequence", path: "/debug/diagnostics/message?channel_key=g1&message_seq=0", want: "invalid message_seq"},
		{name: "malformed message sequence", path: "/debug/diagnostics/message?channel_key=g1&message_seq=abc", want: "invalid message_seq"},
		{name: "zero limit", path: "/debug/diagnostics/message?client_msg_no=c1&limit=0", want: "invalid limit"},
		{name: "negative limit", path: "/debug/diagnostics/message?client_msg_no=c1&limit=-1", want: "invalid limit"},
		{name: "malformed limit", path: "/debug/diagnostics/message?client_msg_no=c1&limit=many", want: "invalid limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serveAPIRequest(t, srv, http.MethodGet, tt.path, "")
			if rec.Code != http.StatusBadRequest || !jsonEqual(rec.Body.String(), `{"error":"`+tt.want+`"}`) {
				t.Fatalf("status = %d body = %s, want 400 %q", rec.Code, rec.Body.String(), tt.want)
			}
		})
	}
}

func serveAPIRequest(t *testing.T, srv *Server, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}
