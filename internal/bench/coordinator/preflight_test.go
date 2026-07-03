package coordinator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/stretchr/testify/require"
)

func TestPreflightSuccessChecksTargetCapabilitiesWorkerAndGateway(t *testing.T) {
	targetHits := make(map[string]int)
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits[r.URL.Path]++
		switch r.URL.Path {
		case "/healthz", "/readyz":
			w.WriteHeader(http.StatusOK)
		case "/bench/v1/capabilities":
			writeJSON(t, w, model.BenchCapabilities{
				Enabled: true,
				Version: "bench/v1",
				Supports: model.BenchCapabilitiesSupports{
					UsersTokensBatch:        true,
					ChannelsBatch:           true,
					ChannelSubscribersBatch: true,
					Snapshot:                true,
					ChannelTypes:            []string{"person", "group"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer targetSrv.Close()
	workerSawAuth := false
	workerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/info", r.URL.Path)
		workerSawAuth = r.Header.Get("Authorization") == "Bearer worker-secret"
		writeJSON(t, w, map[string]string{"worker": "wkbench"})
	}))
	defer workerSrv.Close()
	gateway := &recordingGatewayChecker{}

	err := NewPreflight(PreflightConfig{GatewayChecker: gateway}).Check(context.Background(), model.Target{
		API:      model.TargetAPIConfig{Addrs: []string{targetSrv.URL}},
		Gateway:  model.TargetGatewayConfig{TCP: model.TargetGatewayTCPConfig{Addrs: []string{"127.0.0.1:5100"}}},
		BenchAPI: model.BenchAPIConfig{Enabled: true},
	}, model.WorkerSet{Workers: []model.Worker{{ID: "w1", Addr: workerSrv.URL, Weight: 1, ControlToken: "worker-secret"}}})

	require.NoError(t, err)
	require.Equal(t, 1, targetHits["/healthz"])
	require.Equal(t, 1, targetHits["/readyz"])
	require.Equal(t, 1, targetHits["/bench/v1/capabilities"])
	require.True(t, workerSawAuth)
	require.Equal(t, []string{"127.0.0.1:5100"}, gateway.addrs)
}

func TestPreflightRequiresBenchAPIEnabledInTargetConfig(t *testing.T) {
	err := NewPreflight(PreflightConfig{}).Check(context.Background(), model.Target{}, model.WorkerSet{})

	require.ErrorContains(t, err, "bench_api.enabled")
}

func TestPreflightFailsWhenRequiredCapabilityMissing(t *testing.T) {
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz":
			w.WriteHeader(http.StatusOK)
		case "/bench/v1/capabilities":
			writeJSON(t, w, model.BenchCapabilities{
				Enabled: true,
				Version: "bench/v1",
				Supports: model.BenchCapabilitiesSupports{
					UsersTokensBatch:        true,
					ChannelsBatch:           true,
					ChannelSubscribersBatch: true,
					Snapshot:                false,
					ChannelTypes:            []string{"group"},
				},
			})
		}
	}))
	defer targetSrv.Close()

	err := NewPreflight(PreflightConfig{}).Check(context.Background(), model.Target{
		API:      model.TargetAPIConfig{Addrs: []string{targetSrv.URL}},
		BenchAPI: model.BenchAPIConfig{Enabled: true},
	}, model.WorkerSet{})

	require.ErrorContains(t, err, "snapshot")
}

func TestPreflightFailsWhenCapabilityVersionIsWrong(t *testing.T) {
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz":
			w.WriteHeader(http.StatusOK)
		case "/bench/v1/capabilities":
			writeJSON(t, w, model.BenchCapabilities{
				Enabled: true,
				Version: "bench/v0",
				Supports: model.BenchCapabilitiesSupports{
					UsersTokensBatch:        true,
					ChannelsBatch:           true,
					ChannelSubscribersBatch: true,
					Snapshot:                true,
					ChannelTypes:            []string{"group"},
				},
			})
		}
	}))
	defer targetSrv.Close()

	err := NewPreflight(PreflightConfig{}).Check(context.Background(), model.Target{
		API:      model.TargetAPIConfig{Addrs: []string{targetSrv.URL}},
		BenchAPI: model.BenchAPIConfig{Enabled: true},
	}, model.WorkerSet{})

	require.ErrorContains(t, err, "bench/v1")
	require.ErrorContains(t, err, "version")
}

func TestPreflightAllowsInsecureWorkerWithoutToken(t *testing.T) {
	targetSrv := goodTargetServer(t)
	defer targetSrv.Close()
	workerSawAuth := false
	workerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workerSawAuth = r.Header.Get("Authorization") != ""
		writeJSON(t, w, map[string]string{"worker": "wkbench"})
	}))
	defer workerSrv.Close()

	err := NewPreflight(PreflightConfig{}).Check(context.Background(), model.Target{
		API:      model.TargetAPIConfig{Addrs: []string{targetSrv.URL}},
		BenchAPI: model.BenchAPIConfig{Enabled: true},
	}, model.WorkerSet{Workers: []model.Worker{{ID: "w1", Addr: workerSrv.URL, Weight: 1, ControlToken: "ignored", InsecureControl: true}}})

	require.NoError(t, err)
	require.False(t, workerSawAuth)
}

func TestPreflightRejectsSecureWorkerWithoutTokenBeforeHTTP(t *testing.T) {
	targetSrv := goodTargetServer(t)
	defer targetSrv.Close()
	workerHit := false
	workerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workerHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer workerSrv.Close()

	err := NewPreflight(PreflightConfig{}).Check(context.Background(), model.Target{
		API:      model.TargetAPIConfig{Addrs: []string{targetSrv.URL}},
		BenchAPI: model.BenchAPIConfig{Enabled: true},
	}, model.WorkerSet{Workers: []model.Worker{{ID: "w1", Addr: workerSrv.URL, Weight: 1}}})

	require.ErrorContains(t, err, "control_token")
	require.False(t, workerHit)
}

func TestPreflightFailsWhenGroupChannelTypeUnsupported(t *testing.T) {
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz":
			w.WriteHeader(http.StatusOK)
		case "/bench/v1/capabilities":
			writeJSON(t, w, model.BenchCapabilities{
				Enabled: true,
				Version: "bench/v1",
				Supports: model.BenchCapabilitiesSupports{
					UsersTokensBatch:        true,
					ChannelsBatch:           true,
					ChannelSubscribersBatch: true,
					Snapshot:                true,
					ChannelTypes:            []string{"person"},
				},
			})
		}
	}))
	defer targetSrv.Close()

	err := NewPreflight(PreflightConfig{}).Check(context.Background(), model.Target{
		API:      model.TargetAPIConfig{Addrs: []string{targetSrv.URL}},
		BenchAPI: model.BenchAPIConfig{Enabled: true},
	}, model.WorkerSet{})

	require.ErrorContains(t, err, "group")
}

func TestPreflightFailsWhenWorkerInfoRejectsToken(t *testing.T) {
	targetSrv := goodTargetServer(t)
	defer targetSrv.Close()
	workerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad token", http.StatusUnauthorized)
	}))
	defer workerSrv.Close()

	err := NewPreflight(PreflightConfig{}).Check(context.Background(), model.Target{
		API:      model.TargetAPIConfig{Addrs: []string{targetSrv.URL}},
		BenchAPI: model.BenchAPIConfig{Enabled: true},
	}, model.WorkerSet{Workers: []model.Worker{{ID: "w1", Addr: workerSrv.URL, Weight: 1, ControlToken: "wrong"}}})

	require.ErrorContains(t, err, "worker w1")
	require.ErrorContains(t, err, "401")
}

func goodTargetServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz":
			w.WriteHeader(http.StatusOK)
		case "/bench/v1/capabilities":
			writeJSON(t, w, model.BenchCapabilities{
				Enabled: true,
				Version: "bench/v1",
				Supports: model.BenchCapabilitiesSupports{
					UsersTokensBatch:        true,
					ChannelsBatch:           true,
					ChannelSubscribersBatch: true,
					Snapshot:                true,
					ChannelTypes:            []string{"group"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(v))
}

type recordingGatewayChecker struct {
	addrs []string
}

func (g *recordingGatewayChecker) Check(ctx context.Context, addrs []string) error {
	g.addrs = append(g.addrs, addrs...)
	return nil
}
