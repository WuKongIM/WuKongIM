package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServerServesHealthReadyAndBenchTargetSurface(t *testing.T) {
	srv := New(Options{
		Readyz: func(context.Context) (bool, any) {
			return true, map[string]any{"ready": true}
		},
		BenchEnabled:         true,
		BenchMaxBatchSize:    10,
		BenchMaxPayloadBytes: 1024,
		Gateway: GatewayAddresses{
			TCPAddr: "127.0.0.1:5100",
			WSAddr:  "ws://127.0.0.1:5200",
		},
	})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	resp, err := http.Get(httpSrv.URL + "/healthz")
	requireStatus(t, resp, err, http.StatusOK)
	resp, err = http.Get(httpSrv.URL + "/readyz")
	requireStatus(t, resp, err, http.StatusOK)

	var caps capabilitiesResponse
	resp, err = http.Get(httpSrv.URL + "/bench/v1/capabilities")
	decodeJSON(t, resp, err, &caps)
	if !caps.Enabled || caps.Version != "bench/v1" {
		t.Fatalf("capabilities = %+v, want enabled bench/v1", caps)
	}
	if !caps.Supports.UsersTokensBatch || !caps.Supports.ChannelsBatch || !caps.Supports.ChannelSubscribersBatch || !caps.Supports.Snapshot {
		t.Fatalf("capabilities supports = %+v, want all wkbench preparation features", caps.Supports)
	}
	if caps.Supports.ChannelRuntimeSnapshot || caps.Supports.ChannelRuntimeProbe || caps.Supports.ChannelRuntimeEvict || caps.Supports.ChannelRuntimeFaults || caps.Supports.ChannelRuntimeActivate {
		t.Fatalf("channel runtime supports = %+v, want disabled without controller", caps.Supports)
	}
	if got, want := caps.Limits.MaxBatchSize, 10; got != want {
		t.Fatalf("max_batch_size = %d, want %d", got, want)
	}

	var target capacityTargetResponse
	resp, err = http.Get(httpSrv.URL + "/bench/v1/capacity-target")
	decodeJSON(t, resp, err, &target)
	if target.Gateway.TCPAddr != "127.0.0.1:5100" || target.Gateway.WSAddr != "ws://127.0.0.1:5200" {
		t.Fatalf("capacity target = %+v, want configured gateway addresses", target)
	}

	postJSON(t, httpSrv.URL+"/bench/v1/users/tokens", `{"run_id":"run-1","batch_id":"tokens-1","users":[{"uid":"u1","token":"t1"},{"uid":"u2","token":"t2"}]}`, http.StatusOK)
	var snap snapshotResponse
	resp, err = http.Get(httpSrv.URL + "/bench/v1/snapshot")
	decodeJSON(t, resp, err, &snap)
	if got, want := snap.Counts["accepted_users"], 2; got != want {
		t.Fatalf("snapshot accepted_users = %d, want %d", got, want)
	}
}

func TestServerReturnsServiceUnavailableWhenReadyzIsFalse(t *testing.T) {
	srv := New(Options{
		Readyz: func(context.Context) (bool, any) {
			return false, map[string]any{"ready": false, "reason": "cluster write routing not ready"}
		},
	})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	resp, err := http.Get(httpSrv.URL + "/readyz")
	requireStatus(t, resp, err, http.StatusServiceUnavailable)
}

func TestBenchRoutesAreNotRegisteredWhenDisabled(t *testing.T) {
	srv := New(Options{})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	resp, err := http.Get(httpSrv.URL + "/bench/v1/capabilities")
	requireStatus(t, resp, err, http.StatusNotFound)
}

func TestServerServesMetricsWhenHandlerConfigured(t *testing.T) {
	srv := New(Options{
		MetricsHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("wukongim_gateway_async_send_queue_depth 1\n"))
		}),
	})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	resp, err := http.Get(httpSrv.URL + "/metrics")
	if err != nil {
		t.Fatalf("HTTP request error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if got := string(body); got != "wukongim_gateway_async_send_queue_depth 1\n" {
		t.Fatalf("body = %q, want metrics body", got)
	}
}

func TestServerServesPProfOnlyWhenEnabled(t *testing.T) {
	disabled := httptest.NewServer(New(Options{}).Handler())
	t.Cleanup(disabled.Close)
	resp, err := http.Get(disabled.URL + "/debug/pprof/")
	requireStatus(t, resp, err, http.StatusNotFound)

	enabled := httptest.NewServer(New(Options{PProfEnabled: true}).Handler())
	t.Cleanup(enabled.Close)
	resp, err = http.Get(enabled.URL + "/debug/pprof/")
	requireStatus(t, resp, err, http.StatusOK)
}

func TestBenchMutationsValidateHeadersBatchLimitsAndReset(t *testing.T) {
	srv := New(Options{BenchEnabled: true, BenchMaxBatchSize: 1, BenchMaxPayloadBytes: 128})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	postJSON(t, httpSrv.URL+"/bench/v1/users/tokens", `{"run_id":"","batch_id":"b1","users":[{"uid":"u1"}]}`, http.StatusBadRequest)
	postJSON(t, httpSrv.URL+"/bench/v1/users/tokens", `{"run_id":"r1","batch_id":"b1","users":[{"uid":"u1"},{"uid":"u2"}]}`, http.StatusBadRequest)
	postJSON(t, httpSrv.URL+"/bench/v1/channels/subscribers", `{"run_id":"r1","batch_id":"s1","items":[{"channel_id":"g1","channel_type":2,"reset":true,"subscribers":["u1"]}]}`, http.StatusBadRequest)
	postJSON(t, httpSrv.URL+"/bench/v1/users/tokens", `{"run_id":"r1","batch_id":"b1","users":[{"uid":"`+string(bytes.Repeat([]byte("x"), 256))+`"}]}`, http.StatusRequestEntityTooLarge)
}

func requireStatus(t *testing.T, resp *http.Response, err error, want int) {
	t.Helper()
	if err != nil {
		t.Fatalf("HTTP request error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("status = %d, want %d", resp.StatusCode, want)
	}
}

func decodeJSON(t *testing.T, resp *http.Response, err error, out any) {
	t.Helper()
	if err != nil {
		t.Fatalf("HTTP request error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
}

func postJSON(t *testing.T, url, body string, want int) {
	t.Helper()
	resp, err := http.Post(url, "application/json", bytes.NewBufferString(body))
	requireStatus(t, resp, err, want)
}
