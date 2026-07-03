package capacity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/stretchr/testify/require"
)

func TestDiscoverTargetCollectsCapacityGatewayAddrs(t *testing.T) {
	api1 := newCapacityTargetServer(t, "127.0.0.1:15100")
	defer api1.Close()
	api2 := newCapacityTargetServer(t, "127.0.0.1:15101")
	defer api2.Close()

	got, err := DiscoverTarget(context.Background(), Config{APIAddrs: []string{api1.URL, api2.URL}, Profile: ProfileMixed, StartQPS: 100, MaxQPS: 100, StepFactor: 1.5, Duration: 30, Warmup: 30, StableP99: 30, MinActualRatio: 0.95, BinarySearchMinDeltaRatio: 0.05, GroupMembers: 10})

	require.NoError(t, err)
	require.Equal(t, []string{api1.URL, api2.URL}, got.Target.API.Addrs)
	require.Equal(t, []string{"127.0.0.1:15100", "127.0.0.1:15101"}, got.Target.Gateway.TCP.Addrs)
	require.Equal(t, []string{api1.URL, api2.URL}, got.Target.BenchAPI.Addrs)
	require.True(t, got.Target.BenchAPI.Enabled)
}

func TestDiscoverTargetManualGatewayOverride(t *testing.T) {
	api := newCapacityTargetServer(t, "")
	defer api.Close()
	cfg := DefaultConfig()
	cfg.APIAddrs = []string{api.URL}
	cfg.GatewayTCPAddrs = []string{"127.0.0.1:19999"}

	got, err := DiscoverTarget(context.Background(), cfg)

	require.NoError(t, err)
	require.Equal(t, []string{"127.0.0.1:19999"}, got.Target.Gateway.TCP.Addrs)
}

func newCapacityTargetServer(t *testing.T, tcpAddr string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz":
			w.WriteHeader(http.StatusOK)
		case "/bench/v1/capabilities":
			writeCapacityJSON(t, w, model.BenchCapabilities{
				Enabled: true,
				Version: "bench/v1",
				Supports: model.BenchCapabilitiesSupports{
					UsersTokensBatch:        true,
					ChannelsBatch:           true,
					ChannelSubscribersBatch: true,
					Snapshot:                true,
					ChannelTypes:            []string{model.ChannelTypeGroup},
				},
			})
		case "/bench/v1/capacity-target":
			writeCapacityJSON(t, w, model.CapacityTarget{Version: "bench/v1", Gateway: model.CapacityTargetGateway{TCPAddr: tcpAddr}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
}

func writeCapacityJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(v))
}
