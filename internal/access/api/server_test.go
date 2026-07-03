package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
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
	if !caps.Supports.UsersTokensBatch || !caps.Supports.Snapshot {
		t.Fatalf("capabilities supports = %+v, want token and snapshot features", caps.Supports)
	}
	if caps.Supports.ChannelsBatch || caps.Supports.ChannelSubscribersBatch {
		t.Fatalf("capabilities supports = %+v, want channel mutations disabled without bench data writer", caps.Supports)
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

func TestBenchChannelMutationsRequireConfiguredBenchData(t *testing.T) {
	srv := New(Options{BenchEnabled: true})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	postJSON(t, httpSrv.URL+"/bench/v1/channels", `{
		"run_id":"run-1",
		"batch_id":"channels-1",
		"channels":[{"channel_id":"g1","channel_type":2}]
	}`, http.StatusNotImplemented)
	postJSON(t, httpSrv.URL+"/bench/v1/channels/subscribers", `{
		"run_id":"run-1",
		"batch_id":"subs-1",
		"items":[{"channel_id":"g1","channel_type":2,"subscribers":["u1"]}]
	}`, http.StatusNotImplemented)
}

func TestBenchMutationRoutesWriteConfiguredBenchData(t *testing.T) {
	writer := &fakeBenchData{acceptedChannels: 1, acceptedSubscribers: 3}
	srv := New(Options{
		BenchEnabled: true,
		BenchData:    writer,
	})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	var channelResp mutationResponse
	resp, err := http.Post(httpSrv.URL+"/bench/v1/channels", "application/json", bytes.NewBufferString(`{
		"run_id":"run-1",
		"batch_id":"channels-1",
		"channels":[{"channel_id":"g1","channel_type":2,"allow_stranger":true}]
	}`))
	decodeJSON(t, resp, err, &channelResp)
	if channelResp.Accepted != 1 {
		t.Fatalf("channel response = %#v, want accepted channel", channelResp)
	}
	if len(writer.channels) != 1 || writer.channels[0].ChannelID != "g1" || !writer.channels[0].AllowStranger {
		t.Fatalf("writer channels = %#v, want persisted channel mutation", writer.channels)
	}

	resp, err = http.Post(httpSrv.URL+"/bench/v1/channels/subscribers", "application/json", bytes.NewBufferString(`{
		"run_id":"run-1",
		"batch_id":"subs-1",
		"items":[
			{"channel_id":"g1","channel_type":2,"subscribers":["u1","u2"]},
			{"channel_id":"g2","channel_type":2,"subscribers":["u3"]}
		]
	}`))
	var got subscribersResponse
	decodeJSON(t, resp, err, &got)
	if got.Accepted != 2 || got.AcceptedSubscribers != 3 {
		t.Fatalf("subscribers response = %#v, want accepted items and writer subscriber count", got)
	}
	if len(writer.subscribers) != 2 {
		t.Fatalf("writer subscriber mutations = %d, want 2", len(writer.subscribers))
	}
	if writer.subscribers[0].ChannelID != "g1" || writer.subscribers[0].ChannelType != 2 ||
		len(writer.subscribers[0].Subscribers) != 2 || writer.subscribers[0].Subscribers[0] != "u1" || writer.subscribers[0].Subscribers[1] != "u2" {
		t.Fatalf("first subscriber mutation = %#v, want g1 subscribers", writer.subscribers[0])
	}
	var snap snapshotResponse
	resp, err = http.Get(httpSrv.URL + "/bench/v1/snapshot")
	decodeJSON(t, resp, err, &snap)
	if snap.Counts["accepted_channels"] != 1 || snap.Counts["accepted_subscriber_items"] != 2 || snap.Counts["accepted_subscribers"] != 3 {
		t.Fatalf("snapshot counts = %#v, want bench writer counts", snap.Counts)
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

func TestRouteReturnsLegacyExternalAddresses(t *testing.T) {
	srv := New(Options{
		LegacyRouteExternal: LegacyRouteAddresses{
			TCPAddr: "198.51.100.10:5100",
			WSAddr:  "ws://198.51.100.10:5200",
			WSSAddr: "wss://198.51.100.10:5210",
		},
		LegacyRouteIntranet: LegacyRouteAddresses{
			TCPAddr: "10.0.0.10:5100",
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/route?uid=u1", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !jsonEqual(rec.Body.String(), `{"tcp_addr":"198.51.100.10:5100","ws_addr":"ws://198.51.100.10:5200","wss_addr":"wss://198.51.100.10:5210"}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestRouteReturnsLegacyIntranetAddresses(t *testing.T) {
	srv := New(Options{
		LegacyRouteExternal: LegacyRouteAddresses{
			TCPAddr: "198.51.100.10:5100",
			WSAddr:  "ws://198.51.100.10:5200",
			WSSAddr: "wss://198.51.100.10:5210",
		},
		LegacyRouteIntranet: LegacyRouteAddresses{
			TCPAddr: "10.0.0.10:5100",
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/route?uid=u1&intranet=1", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !jsonEqual(rec.Body.String(), `{"tcp_addr":"10.0.0.10:5100","ws_addr":"","wss_addr":""}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestRouteBatchReturnsSpecifiedNodeIntranetAddresses(t *testing.T) {
	srv := New(Options{
		LegacyRouteNodes: map[uint64]LegacyRouteNodeAddresses{
			2: {
				External: LegacyRouteAddresses{
					TCPAddr: "198.51.100.20:5100",
					WSAddr:  "ws://198.51.100.20:5200",
					WSSAddr: "wss://198.51.100.20:5210",
				},
				Intranet: LegacyRouteAddresses{
					TCPAddr: "10.0.0.20:5100",
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/route/batch?node_id=2&intranet=1", bytes.NewBufferString(`["u1","u2"]`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !jsonEqual(rec.Body.String(), `[{"uids":["u1","u2"],"tcp_addr":"10.0.0.20:5100","ws_addr":"","wss_addr":""}]`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestRouteBatchReturnsLegacyInvalidRequestError(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/route/batch", bytes.NewBufferString(`{"uids":["u1"]}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !jsonEqual(rec.Body.String(), `{"msg":"数据格式有误！","status":400}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestCORSHeadersAddedToNormalResponses(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "http://localhost:5175")

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got, want := rec.Header().Get("Access-Control-Allow-Origin"), "http://localhost:5175"; got != want {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, want)
	}
	if !hasHeaderValue(rec.Header().Values("Vary"), "Origin") {
		t.Fatalf("Vary = %#v, want Origin", rec.Header().Values("Vary"))
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodGet) {
		t.Fatalf("Access-Control-Allow-Methods = %q, want %s", got, http.MethodGet)
	}
}

func TestCORSPreflightHandlesUserTokenRoute(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/user/token", nil)
	req.Header.Set("Origin", "http://localhost:5175")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "content-type,authorization")

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got, want := rec.Header().Get("Access-Control-Allow-Origin"), "http://localhost:5175"; got != want {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, want)
	}
	if !hasHeaderValue(rec.Header().Values("Vary"), "Origin") {
		t.Fatalf("Vary = %#v, want Origin", rec.Header().Values("Vary"))
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodPost) {
		t.Fatalf("Access-Control-Allow-Methods = %q, want %s", got, http.MethodPost)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Content-Type") || !strings.Contains(got, "Authorization") {
		t.Fatalf("Access-Control-Allow-Headers = %q, want Content-Type and Authorization", got)
	}
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

func TestServerServesPProfOnlyWhenDebugAPIEnabled(t *testing.T) {
	disabled := httptest.NewServer(New(Options{}).Handler())
	t.Cleanup(disabled.Close)
	resp, err := http.Get(disabled.URL + "/debug/pprof/")
	requireStatus(t, resp, err, http.StatusNotFound)

	enabled := httptest.NewServer(New(Options{DebugAPIEnabled: true}).Handler())
	t.Cleanup(enabled.Close)
	resp, err = http.Get(enabled.URL + "/debug/pprof/")
	requireStatus(t, resp, err, http.StatusOK)
}

func TestBenchPresenceSnapshotRequiresController(t *testing.T) {
	disabled := httptest.NewServer(New(Options{BenchEnabled: true}).Handler())
	t.Cleanup(disabled.Close)
	resp, err := http.Get(disabled.URL + "/bench/v1/presence/snapshot")
	requireStatus(t, resp, err, http.StatusNotImplemented)

	controller := &fakePresenceBenchController{
		snapshot: model.PresenceSnapshot{
			Version:                   "bench/v1",
			NodeID:                    1,
			OwnerRoutesActive:         3,
			OwnerRoutesPending:        1,
			OwnerTouchedDirty:         2,
			AuthorityRoutesActive:     3,
			AuthorityRoutesByHashSlot: map[uint16]int{9: 3},
			TouchRoutesTotal:          4,
			ExpiredRoutesTotal:        1,
		},
	}
	enabled := httptest.NewServer(New(Options{BenchEnabled: true, BenchPresence: controller}).Handler())
	t.Cleanup(enabled.Close)

	var caps capabilitiesResponse
	resp, err = http.Get(enabled.URL + "/bench/v1/capabilities")
	decodeJSON(t, resp, err, &caps)
	if !caps.Supports.PresenceSnapshot {
		t.Fatalf("presence snapshot support = false, want true")
	}

	var snap model.PresenceSnapshot
	resp, err = http.Get(enabled.URL + "/bench/v1/presence/snapshot")
	decodeJSON(t, resp, err, &snap)
	if snap.OwnerRoutesActive != 3 || snap.OwnerTouchedDirty != 2 || snap.AuthorityRoutesByHashSlot[9] != 3 {
		t.Fatalf("presence snapshot = %+v, want controller payload", snap)
	}
	if !controller.called {
		t.Fatalf("presence controller was not called")
	}
}

func TestBenchMutationsValidateHeadersBatchLimitsAndReset(t *testing.T) {
	srv := New(Options{BenchEnabled: true, BenchMaxBatchSize: 1, BenchMaxPayloadBytes: 128, BenchData: &fakeBenchData{}})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	postJSON(t, httpSrv.URL+"/bench/v1/users/tokens", `{"run_id":"","batch_id":"b1","users":[{"uid":"u1"}]}`, http.StatusBadRequest)
	postJSON(t, httpSrv.URL+"/bench/v1/users/tokens", `{"run_id":"r1","batch_id":"b1","users":[{"uid":"u1"},{"uid":"u2"}]}`, http.StatusBadRequest)
	postJSON(t, httpSrv.URL+"/bench/v1/channels/subscribers", `{"run_id":"r1","batch_id":"s1","items":[{"channel_id":"g1","channel_type":2,"reset":true,"subscribers":["u1"]}]}`, http.StatusBadRequest)
	postJSON(t, httpSrv.URL+"/bench/v1/users/tokens", `{"run_id":"r1","batch_id":"b1","users":[{"uid":"`+string(bytes.Repeat([]byte("x"), 256))+`"}]}`, http.StatusRequestEntityTooLarge)
}

type fakePresenceBenchController struct {
	called   bool
	snapshot model.PresenceSnapshot
	err      error
}

type fakeBenchData struct {
	acceptedChannels    int
	acceptedSubscribers int
	channels            []BenchChannelMutation
	subscribers         []BenchSubscriberMutation
	err                 error
}

func (f *fakeBenchData) UpsertChannels(_ context.Context, mutations []BenchChannelMutation) (int, error) {
	f.channels = append(f.channels, mutations...)
	if f.err != nil {
		return 0, f.err
	}
	return f.acceptedChannels, nil
}

func (f *fakeBenchData) AddSubscribers(_ context.Context, mutations []BenchSubscriberMutation) (int, error) {
	f.subscribers = append(f.subscribers, mutations...)
	if f.err != nil {
		return 0, f.err
	}
	return f.acceptedSubscribers, nil
}

func (f *fakePresenceBenchController) Snapshot(context.Context) (model.PresenceSnapshot, error) {
	f.called = true
	if f.err != nil {
		return model.PresenceSnapshot{}, f.err
	}
	return f.snapshot, nil
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

func hasHeaderValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
