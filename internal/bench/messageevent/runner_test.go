package messageevent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunnerWritesStreamsAndCapturesMetrics(t *testing.T) {
	var mu sync.Mutex
	var channelPosts int
	var sendPosts int
	var deltaPosts int
	var finishPosts int
	var metricsCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metrics":
			mu.Lock()
			metricsCalls++
			call := metricsCalls
			mu.Unlock()
			if call == 1 {
				_, _ = w.Write([]byte(`wukongim_message_event_append_total{path="cache",event_type="stream.delta",result="ok"} 0
wukongim_message_event_propose_total{path="finish_batch",result="ok"} 0
`))
				return
			}
			_, _ = w.Write([]byte(`wukongim_message_event_append_total{path="cache",event_type="stream.delta",result="ok"} 8
wukongim_message_event_propose_total{path="finish_batch",result="ok"} 2
`))
		case "/channel":
			mu.Lock()
			channelPosts++
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":200}`))
		case "/message/send":
			var req struct {
				Setting uint8 `json:"setting"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode send: %v", err)
			}
			if req.Setting != 2 {
				t.Errorf("send setting = %d, want stream bit", req.Setting)
			}
			mu.Lock()
			sendPosts++
			mu.Unlock()
			_, _ = w.Write([]byte(`{"message_id":99,"message_seq":7,"reason":1}`))
		case "/message/event":
			var req struct {
				EventType string `json:"event_type"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode event: %v", err)
			}
			mu.Lock()
			switch req.EventType {
			case "stream.delta":
				deltaPosts++
			case "stream.finish":
				finishPosts++
			default:
				t.Errorf("unexpected event type %q", req.EventType)
			}
			mu.Unlock()
			_, _ = w.Write([]byte(`{"status":200,"data":{"msg_event_seq":1,"stream_status":"closed"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := DefaultConfig()
	cfg.APIAddrs = []string{server.URL}
	cfg.Channels = 1
	cfg.StreamsPerChannel = 2
	cfg.LanesPerStream = 2
	cfg.DeltasPerLane = 2
	cfg.Concurrency = 2
	cfg.RequestTimeout = time.Second
	cfg.ReportDir = t.TempDir()

	result, err := NewRunner(cfg).Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != StatusPassed {
		t.Fatalf("status = %s, want %s", result.Status, StatusPassed)
	}
	if result.Requests.BaseMessages != 2 || result.Requests.DeltaEvents != 8 || result.Requests.FinishEvents != 2 {
		t.Fatalf("request summary = %+v", result.Requests)
	}
	if result.Metrics.MessageEventAppendCountByPath["cache"] != 8 {
		t.Fatalf("cache metric delta = %.0f, want 8", result.Metrics.MessageEventAppendCountByPath["cache"])
	}
	if result.Metrics.MessageEventProposeCountByPath["finish_batch"] != 2 {
		t.Fatalf("finish propose metric delta = %.0f, want 2", result.Metrics.MessageEventProposeCountByPath["finish_batch"])
	}
	if len(result.Gates) == 0 {
		t.Fatalf("expected message event hard gates")
	}
	for _, gate := range result.Gates {
		if !gate.Passed {
			t.Fatalf("gate %s failed: %+v", gate.Name, gate)
		}
	}
	if err := WriteResult(cfg.ReportDir, result); err != nil {
		t.Fatalf("WriteResult() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.ReportDir, "message_event_report.json")); err != nil {
		t.Fatalf("expected message event report: %v", err)
	}
	summary, err := os.ReadFile(filepath.Join(cfg.ReportDir, "summary.md"))
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	if !strings.Contains(string(summary), "# wkbench message-event") || !strings.Contains(ConsoleSummary(result), "wkbench message-event: passed") {
		t.Fatalf("unexpected summary/console: %q / %q", string(summary), ConsoleSummary(result))
	}

	mu.Lock()
	defer mu.Unlock()
	if channelPosts != 1 || sendPosts != 2 || deltaPosts != 8 || finishPosts != 2 || metricsCalls != 2 {
		t.Fatalf("posts/metrics = channel:%d send:%d delta:%d finish:%d metrics:%d", channelPosts, sendPosts, deltaPosts, finishPosts, metricsCalls)
	}
}

func TestRunnerWarmChannelsBeforeMeasuredMetrics(t *testing.T) {
	var mu sync.Mutex
	var channelPosts int
	var metricsCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metrics":
			mu.Lock()
			metricsCalls++
			mu.Unlock()
			_, _ = w.Write([]byte(`wukongim_message_event_append_total{path="cache",event_type="stream.delta",result="ok"} 0
wukongim_message_event_append_total{path="finish_batch",event_type="stream.finish",result="ok"} 0
wukongim_message_event_propose_total{path="finish_batch",result="ok"} 0
`))
		case "/channel":
			mu.Lock()
			channelPosts++
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":200}`))
		case "/message/send":
			_, _ = w.Write([]byte(`{"message_id":99,"message_seq":7,"reason":1}`))
		case "/message/event":
			_, _ = w.Write([]byte(`{"status":200,"data":{"msg_event_seq":1,"stream_status":"closed"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := DefaultConfig()
	cfg.APIAddrs = []string{server.URL}
	cfg.Channels = 2
	cfg.StreamsPerChannel = 1
	cfg.LanesPerStream = 1
	cfg.DeltasPerLane = 1
	cfg.Concurrency = 1
	cfg.WarmChannels = true
	cfg.RequestTimeout = time.Second
	cfg.ReportDir = t.TempDir()

	result, _ := NewRunner(cfg).Run(context.Background())

	if result.Requests.WarmChannelUpserts != 2 {
		t.Fatalf("warm channel upserts = %d, want 2", result.Requests.WarmChannelUpserts)
	}
	if result.Requests.ChannelUpserts != 0 {
		t.Fatalf("measured channel upserts = %d, want 0 when channels are warmed", result.Requests.ChannelUpserts)
	}
	mu.Lock()
	defer mu.Unlock()
	if channelPosts != 2 {
		t.Fatalf("channel posts = %d, want only warmup posts", channelPosts)
	}
	if metricsCalls != 2 {
		t.Fatalf("metrics calls = %d, want before/after around measured traffic", metricsCalls)
	}
}

func TestRunnerWarmRuntimeBeforeMeasuredMetrics(t *testing.T) {
	var mu sync.Mutex
	var warmSends int
	var measuredBaseSends int
	var metricsCalls int
	var sawMetricsBeforeMeasuredSend bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metrics":
			mu.Lock()
			metricsCalls++
			mu.Unlock()
			_, _ = w.Write([]byte(`wukongim_message_event_append_total{path="cache",event_type="stream.delta",result="ok"} 0
wukongim_message_event_append_total{path="finish_batch",event_type="stream.finish",result="ok"} 0
wukongim_message_event_propose_total{path="finish_batch",result="ok"} 0
`))
		case "/channel":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":200}`))
		case "/message/send":
			var req struct {
				ClientMsgNo string `json:"client_msg_no"`
				Setting     uint8  `json:"setting"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode send: %v", err)
			}
			mu.Lock()
			if strings.Contains(req.ClientMsgNo, "-warm-runtime-") {
				warmSends++
				if req.Setting != 0 {
					t.Errorf("warm runtime setting = %d, want non-stream SEND", req.Setting)
				}
			} else {
				measuredBaseSends++
				if metricsCalls > 0 {
					sawMetricsBeforeMeasuredSend = true
				}
				if req.Setting != 2 {
					t.Errorf("measured base setting = %d, want stream bit", req.Setting)
				}
			}
			mu.Unlock()
			_, _ = w.Write([]byte(`{"message_id":99,"message_seq":7,"reason":1}`))
		case "/message/event":
			_, _ = w.Write([]byte(`{"status":200,"data":{"msg_event_seq":1,"stream_status":"closed"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := DefaultConfig()
	cfg.APIAddrs = []string{server.URL}
	cfg.Channels = 2
	cfg.StreamsPerChannel = 1
	cfg.LanesPerStream = 1
	cfg.DeltasPerLane = 1
	cfg.Concurrency = 1
	cfg.WarmChannels = true
	cfg.WarmRuntime = true
	cfg.RequestTimeout = time.Second
	cfg.ReportDir = t.TempDir()

	result, _ := NewRunner(cfg).Run(context.Background())

	if result.Requests.WarmRuntimeMessages != 2 {
		t.Fatalf("warm runtime messages = %d, want 2", result.Requests.WarmRuntimeMessages)
	}
	if result.Requests.BaseMessages != 2 {
		t.Fatalf("base messages = %d, want 2", result.Requests.BaseMessages)
	}
	mu.Lock()
	defer mu.Unlock()
	if warmSends != 2 || measuredBaseSends != 2 {
		t.Fatalf("send counts = warm:%d measured:%d, want 2/2", warmSends, measuredBaseSends)
	}
	if !sawMetricsBeforeMeasuredSend {
		t.Fatalf("expected before metrics snapshot before measured base sends")
	}
}

func TestRunnerFailsWhenMessageEventHardGateDoesNotMatchMetrics(t *testing.T) {
	var metricsCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metrics":
			metricsCalls++
			if metricsCalls == 1 {
				_, _ = w.Write([]byte(`wukongim_message_event_append_total{path="cache",event_type="stream.delta",result="ok"} 0
wukongim_message_event_append_total{path="finish_batch",event_type="stream.finish",result="ok"} 0
wukongim_message_event_propose_total{path="finish_batch",result="ok"} 0
`))
				return
			}
			_, _ = w.Write([]byte(`wukongim_message_event_append_total{path="cache",event_type="stream.delta",result="ok"} 2
wukongim_message_event_append_total{path="finish_batch",event_type="stream.finish",result="ok"} 2
wukongim_message_event_propose_total{path="finish_batch",result="ok"} 0
`))
		case "/channel":
			_, _ = w.Write([]byte(`{"status":200}`))
		case "/message/send":
			_, _ = w.Write([]byte(`{"message_id":99,"message_seq":7,"reason":1}`))
		case "/message/event":
			_, _ = w.Write([]byte(`{"status":200,"data":{"msg_event_seq":1,"stream_status":"closed"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := DefaultConfig()
	cfg.APIAddrs = []string{server.URL}
	cfg.Channels = 1
	cfg.StreamsPerChannel = 2
	cfg.LanesPerStream = 1
	cfg.DeltasPerLane = 1
	cfg.Concurrency = 2
	cfg.RequestTimeout = time.Second
	cfg.ReportDir = t.TempDir()

	result, err := NewRunner(cfg).Run(context.Background())

	if err == nil {
		t.Fatalf("expected hard gate failure")
	}
	if result.Status != StatusFailed {
		t.Fatalf("status = %s, want failed", result.Status)
	}
	var found bool
	for _, gate := range result.Gates {
		if gate.Name == "finish_propose_count_bounded" {
			found = true
			if gate.Passed || gate.Expected != 2 || gate.Observed != 0 {
				t.Fatalf("unexpected finish propose gate: %+v", gate)
			}
		}
	}
	if !found {
		t.Fatalf("missing finish propose gate: %+v", result.Gates)
	}
}

func TestRunnerTreatsLegacyBusinessStatusAsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metrics":
			_, _ = w.Write([]byte(`wukongim_message_event_append_total{path="cache",event_type="stream.delta",result="ok"} 0`))
		case "/channel":
			_, _ = w.Write([]byte(`{"status":200}`))
		case "/message/send":
			_, _ = w.Write([]byte(`{"message_id":99,"message_seq":7,"reason":1}`))
		case "/message/event":
			_, _ = w.Write([]byte(`{"status":400,"msg":"message event stream cache miss"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := DefaultConfig()
	cfg.APIAddrs = []string{server.URL}
	cfg.Channels = 1
	cfg.StreamsPerChannel = 1
	cfg.LanesPerStream = 1
	cfg.DeltasPerLane = 1
	cfg.Concurrency = 1
	cfg.RequestTimeout = time.Second
	cfg.ReportDir = t.TempDir()

	result, err := NewRunner(cfg).Run(context.Background())

	if err == nil {
		t.Fatalf("expected business status error")
	}
	if result.Status != StatusFailed || result.Requests.Errors == 0 {
		t.Fatalf("result = %+v, want failed with request error", result)
	}
	if len(result.Errors) == 0 || !strings.Contains(result.Errors[0], "message event stream cache miss") {
		t.Fatalf("expected bounded business error sample, got %+v", result.Errors)
	}
}
