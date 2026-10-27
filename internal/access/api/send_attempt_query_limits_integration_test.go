//go:build integration

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

func TestAttemptRPCResponseOverCapDegradesUnknownWithoutExpandingCap(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef/8000000000000001"
	sample := &rpctrace.Sample{TraceID: id, Side: rpctrace.SideServer, ServiceID: 21, At: time.Unix(10, 0), Result: rpctrace.ResultOK, Spans: []rpctrace.SpanSample{{Stage: rpctrace.Stage(strings.Repeat("private-oversize-content", 20000)), Result: rpctrace.ResultOK}}}
	reader := &attemptRPCReader{reply: diagnostics.QueryResult{Events: []diagnostics.Event{{TraceID: id, Stage: diagnostics.StageRPCServer, RPC: sample}}}}
	server := httptest.NewServer(New(Options{DebugAPIEnabled: true, BenchToken: "attempt-token", Diagnostics: reader}).Handler())
	defer server.Close()
	status, raw := attemptPOST(t, server, `{"version":1,"rpc_selectors":[`+attemptValidRPCJSON+`]}`, "attempt-token")
	if status != http.StatusOK || len(raw) > 256<<10 || strings.Contains(string(raw), "private-oversize-content") {
		t.Fatalf("oversized diagnostic was emitted or expanded cap: status=%d bytes=%d", status, len(raw))
	}
	var got struct {
		RPCResults []struct {
			Status string          `json:"status"`
			Sample json.RawMessage `json:"sample"`
		} `json:"rpc_results"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.RPCResults) != 1 || got.RPCResults[0].Status != "unknown" || len(got.RPCResults[0].Sample) > 0 && string(got.RPCResults[0].Sample) != "null" {
		t.Fatalf("oversize did not fail closed independently: %s", raw)
	}
}
func TestAttemptRPCUnsupportedModeHasNoFallbackAndNoEmptyBearerCompatibility(t *testing.T) {
	body := `{"version":1,"rpc_selectors":[` + attemptValidRPCJSON + `]}`
	server := httptest.NewServer(New(Options{DebugAPIEnabled: true, BenchToken: "attempt-token"}).Handler())
	defer server.Close()
	status, raw := attemptPOST(t, server, body, "attempt-token")
	if status != http.StatusOK || !strings.Contains(string(raw), `"status":"unsupported"`) {
		t.Fatalf("missing local capability not explicit: %d/%s", status, raw)
	}
	unconfigured := httptest.NewServer(New(Options{DebugAPIEnabled: true, Diagnostics: &attemptRPCReader{}}).Handler())
	defer unconfigured.Close()
	status, _ = attemptPOST(t, unconfigured, body, "")
	if status != http.StatusNotFound && status != http.StatusUnauthorized {
		t.Fatalf("new selector exposed without a bearer capability: %d", status)
	}
}
func TestAttemptRPCChunkedRequestCannotBypassEightKiBReadCap(t *testing.T) {
	reader := &attemptRPCReader{}
	server := httptest.NewServer(New(Options{DebugAPIEnabled: true, BenchToken: "attempt-token", Diagnostics: reader}).Handler())
	defer server.Close()
	request, err := http.NewRequest(http.MethodPost, server.URL+"/debug/diagnostics/send-attempts/query", strings.NewReader(`{"version":1,"rpc_selectors":[`+attemptValidRPCJSON+`]}`+strings.Repeat(" ", 8<<10)))
	if err != nil {
		t.Fatal(err)
	}
	request.ContentLength = -1
	request.TransferEncoding = []string{"chunked"}
	request.Header.Set("Authorization", "Bearer attempt-token")
	client := server.Client()
	client.Timeout = 2 * time.Second
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("chunked cap status=%d", response.StatusCode)
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if len(reader.queries) != 0 {
		t.Fatal("over-cap chunked request reached local diagnostics")
	}
}
