//go:build integration

package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

type attemptRPCReader struct {
	mu      sync.Mutex
	queries []diagnostics.Query
	reply   diagnostics.QueryResult
}

func (r *attemptRPCReader) QueryDiagnostics(_ context.Context, q diagnostics.Query) diagnostics.QueryResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queries = append(r.queries, q)
	return r.reply
}
func attemptPOST(t *testing.T, server *httptest.Server, body, token string) (int, []byte) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, server.URL+"/debug/diagnostics/send-attempts/query", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	request.Header.Set("Content-Type", "application/json")
	client := server.Client()
	client.Timeout = 2 * time.Second
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (256<<10)+1))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 256<<10 {
		t.Fatalf("RPC branch exceeded fixed diagnostic cap: %d", len(raw))
	}
	return response.StatusCode, raw
}

const attemptValidKeyJSON = `{"message_fingerprint":"00112233445566778899aabbccddeeff","client_seq":1,"connect_packet_fingerprint":"ffeeddccbbaa99887766554433221100"}`
const attemptValidRPCJSON = `{"trace_id":"0123456789abcdef0123456789abcdef/8000000000000001","side":"server","service_id":21}`

func TestAttemptSelectorAuthenticationShapeAndCombined16SelectorBound(t *testing.T) {
	reader := &attemptRPCReader{}
	server := httptest.NewServer(New(Options{DebugAPIEnabled: true, BenchToken: "attempt-token", Diagnostics: reader}).Handler())
	defer server.Close()
	valid := `{"version":1,"keys":[` + attemptValidKeyJSON + `]}`
	for _, token := range []string{"", "wrong"} {
		status, _ := attemptPOST(t, server, valid, token)
		if status != http.StatusUnauthorized {
			t.Fatalf("missing/wrong capability status=%d", status)
		}
	}
	for _, body := range []string{
		`{}`, `{"version":2,"keys":[` + attemptValidKeyJSON + `]}`, `{"version":1,"keys":[]}`, `{"version":1,"keys":[` + attemptValidKeyJSON + `],"latest":true}`,
		`{"version":1,"keys":[` + strings.Replace(attemptValidKeyJSON, `"client_seq":1`, `"client_seq":0`, 1) + `]}`,
		`{"version":1,"keys":[` + strings.Replace(attemptValidKeyJSON, "00112233445566778899aabbccddeeff", "RAW-PRIVATE-IDENTITY", 1) + `]}`,
		`{"version":1,"rpc_selectors":[` + strings.Replace(attemptValidRPCJSON, `"side":"server"`, `"side":"latest"`, 1) + `]}`,
		`{"version":1,"rpc_selectors":[` + strings.Replace(attemptValidRPCJSON, `"service_id":21`, `"service_id":0`, 1) + `]}`,
		`{"version":1,"keys":[` + strings.TrimSuffix(strings.Repeat(attemptValidKeyJSON+",", 16), ",") + `],"rpc_selectors":[` + attemptValidRPCJSON + `]}`,
		valid + `{}`,
	} {
		status, raw := attemptPOST(t, server, body, "attempt-token")
		if status != http.StatusBadRequest {
			t.Fatalf("invalid selector shape status=%d body=%s", status, raw)
		}
		if strings.Contains(string(raw), "RAW-PRIVATE-IDENTITY") {
			t.Fatal("request identity reflected in error")
		}
	}
	oversized := `{"version":1,"keys":[` + attemptValidKeyJSON + `]}` + strings.Repeat(" ", 8<<10)
	status, _ := attemptPOST(t, server, oversized, "attempt-token")
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("8KiB request cap status=%d", status)
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if len(reader.queries) != 0 {
		t.Fatal("rejected request reached local storage query")
	}
}
func TestAttemptSelectorExactRPCJSONSlashSideServiceAndDetachedProjection(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef/8000000000000001"
	sample := &rpctrace.Sample{TraceID: id, Side: rpctrace.SideServer, ServiceID: 21, At: time.Unix(10, 0), Duration: time.Second, Result: rpctrace.ResultOK, Spans: []rpctrace.SpanSample{{Stage: rpctrace.Stage("runtime.future_wait"), Duration: time.Millisecond, Result: rpctrace.ResultOK}}}
	reader := &attemptRPCReader{reply: diagnostics.QueryResult{Events: []diagnostics.Event{{TraceID: id, Stage: diagnostics.StageRPCServer, RPC: sample, ClientMsgNo: "private-message", FromUID: "private-user", ChannelKey: "private-channel", Error: "private-error"}}}}
	server := httptest.NewServer(New(Options{DebugAPIEnabled: true, BenchToken: "attempt-token", Diagnostics: reader}).Handler())
	defer server.Close()
	status, raw := attemptPOST(t, server, `{"version":1,"rpc_selectors":[`+attemptValidRPCJSON+`]}`, "attempt-token")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	var response struct {
		Version    uint64 `json:"version"`
		RPCResults []struct {
			Status string           `json:"status"`
			Sample *rpctrace.Sample `json:"sample"`
		} `json:"rpc_results"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response.Version != 1 || len(response.RPCResults) != 1 || response.RPCResults[0].Status != "retained" || response.RPCResults[0].Sample == nil || response.RPCResults[0].Sample.TraceID != id {
		t.Fatalf("exact RPC selector lost retained sample: %s", raw)
	}
	reader.mu.Lock()
	queries := append([]diagnostics.Query(nil), reader.queries...)
	reader.mu.Unlock()
	if len(queries) != 1 || queries[0].TraceID != id || queries[0].Stage != diagnostics.StageRPCServer || queries[0].Limit != 500 {
		t.Fatalf("selector was not exact local trace+stage: %+v", queries)
	}
	for _, secret := range []string{"private-message", "private-user", "private-channel", "private-error"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("RPC projection leaked event metadata: %s", secret)
		}
	}
	response.RPCResults[0].Sample.Spans[0].Duration = time.Hour
	if sample.Spans[0].Duration != time.Millisecond {
		t.Fatal("query exposed store-owned sample backing")
	}
}
func TestAttemptSelectorRPCUnknownAmbiguousUnsupportedAndRingOverwriteDoNotUseLatest(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef/8000000000000001"
	base := diagnostics.Event{TraceID: id, Stage: diagnostics.StageRPCServer, RPC: &rpctrace.Sample{TraceID: id, Side: rpctrace.SideServer, ServiceID: 21, At: time.Unix(10, 0), Result: rpctrace.ResultOK}}
	for _, tc := range []struct {
		name   string
		events []diagnostics.Event
		want   string
	}{
		{"not retained", nil, "not_retained"}, {"missing typed sample", []diagnostics.Event{{TraceID: id, Stage: diagnostics.StageRPCServer}}, "unknown"},
		{"wrong event ID", []diagnostics.Event{{TraceID: "other", Stage: diagnostics.StageRPCServer, RPC: base.RPC}}, "unknown"},
		{"duplicate candidate", []diagnostics.Event{base, base}, "ambiguous"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &attemptRPCReader{reply: diagnostics.QueryResult{Events: tc.events}}
			server := httptest.NewServer(New(Options{DebugAPIEnabled: true, BenchToken: "attempt-token", Diagnostics: reader}).Handler())
			defer server.Close()
			_, raw := attemptPOST(t, server, `{"version":1,"rpc_selectors":[`+attemptValidRPCJSON+`]}`, "attempt-token")
			var got struct {
				RPCResults []struct {
					Status string          `json:"status"`
					Sample json.RawMessage `json:"sample"`
				} `json:"rpc_results"`
			}
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if len(got.RPCResults) != 1 || got.RPCResults[0].Status != tc.want || len(got.RPCResults[0].Sample) > 0 && string(got.RPCResults[0].Sample) != "null" {
				t.Fatalf("false RPC attribution: %s", raw)
			}
		})
	}
	store := diagnostics.NewStore(diagnostics.StoreOptions{Capacity: 2})
	store.Record(base)
	for i := 0; i < 3; i++ {
		store.Record(diagnostics.Event{TraceID: "latest-unrelated", Stage: diagnostics.StageRPCServer, RPC: base.RPC})
	}
	server := httptest.NewServer(New(Options{DebugAPIEnabled: true, BenchToken: "attempt-token", Diagnostics: attemptStoreReader{store}}).Handler())
	defer server.Close()
	_, raw := attemptPOST(t, server, `{"version":1,"rpc_selectors":[`+attemptValidRPCJSON+`]}`, "attempt-token")
	if !strings.Contains(string(raw), `"status":"not_retained"`) || strings.Contains(string(raw), "latest-unrelated") {
		t.Fatalf("global ring overwrite was hidden: %s", raw)
	}
	_ = sendtrace.ResultOK
}

type attemptStoreReader struct{ store *diagnostics.Store }

func (r attemptStoreReader) QueryDiagnostics(ctx context.Context, q diagnostics.Query) diagnostics.QueryResult {
	return r.store.Query(ctx, q)
}
