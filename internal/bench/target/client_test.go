package target

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/bench/model"
	"github.com/stretchr/testify/require"
)

func TestCapabilities404FailsPreflight(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	defer ts.Close()
	client := NewClient(Config{APIAddrs: []string{ts.URL}})
	_, err := client.Capabilities(context.Background())
	require.ErrorContains(t, err, "bench api")
}

func TestCapabilitiesTriesAPIAddrsInOrder(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/bench/v1/capabilities", r.URL.Path)
		http.Error(w, "not here", http.StatusServiceUnavailable)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/bench/v1/capabilities", r.URL.Path)
		writeJSON(t, w, model.BenchCapabilities{Enabled: true, Version: "bench/v1"})
	}))
	defer second.Close()
	client := NewClient(Config{APIAddrs: []string{first.URL, second.URL}})

	got, err := client.Capabilities(context.Background())

	require.NoError(t, err)
	require.True(t, got.Enabled)
	require.Equal(t, "bench/v1", got.Version)
}

func TestClientCapabilitiesFallbackDoesNotKeepStaleDecodedFields(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/bench/v1/capabilities", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"enabled":true,"version":123}`))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/bench/v1/capabilities", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"bench/v1"}`))
	}))
	defer second.Close()
	client := NewClient(Config{APIAddrs: []string{first.URL, second.URL}})

	got, err := client.Capabilities(context.Background())

	require.NoError(t, err)
	require.False(t, got.Enabled)
	require.Equal(t, "bench/v1", got.Version)
}

func TestClientCapacityTargetReadsGatewayAddresses(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/bench/v1/capacity-target", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"bench/v1","gateway":{"tcp_addr":"127.0.0.1:15100","ws_addr":"ws://127.0.0.1:15200","wss_addr":""}}`))
	}))
	defer ts.Close()

	got, err := NewClient(Config{APIAddrs: []string{ts.URL}}).CapacityTarget(context.Background())

	require.NoError(t, err)
	require.Equal(t, "bench/v1", got.Version)
	require.Equal(t, "127.0.0.1:15100", got.Gateway.TCPAddr)
	require.Equal(t, "ws://127.0.0.1:15200", got.Gateway.WSAddr)
}

func TestClientCapacityTargetFallsBackAcrossAPIAddresses(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"bench/v1","gateway":{"tcp_addr":"127.0.0.1:15101"}}`))
	}))
	defer good.Close()

	got, err := NewClient(Config{APIAddrs: []string{bad.URL, good.URL}}).CapacityTarget(context.Background())

	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:15101", got.Gateway.TCPAddr)
}

func TestClientChannelRuntimeSnapshotsCallsEveryTarget(t *testing.T) {
	seen := make([]string, 0, 2)
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/bench/v1/channel-runtime/snapshot", r.URL.Path)
		require.Equal(t, "run-a", r.URL.Query().Get("run_id"))
		require.Equal(t, "activate-groups", r.URL.Query().Get("profile"))
		require.Empty(t, r.URL.Query().Get("channel_type"))
		require.Empty(t, r.URL.Query().Get("start"))
		require.Empty(t, r.URL.Query().Get("end"))
		seen = append(seen, "first")
		writeJSON(t, w, model.ChannelRuntimeSnapshot{Version: "bench/v1", NodeID: 1, RunID: "run-a", Profile: "activate-groups"})
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/bench/v1/channel-runtime/snapshot", r.URL.Path)
		require.Equal(t, "run-a", r.URL.Query().Get("run_id"))
		require.Equal(t, "activate-groups", r.URL.Query().Get("profile"))
		require.Empty(t, r.URL.Query().Get("channel_type"))
		require.Empty(t, r.URL.Query().Get("start"))
		require.Empty(t, r.URL.Query().Get("end"))
		seen = append(seen, "second")
		writeJSON(t, w, model.ChannelRuntimeSnapshot{Version: "bench/v1", NodeID: 2, RunID: "run-a", Profile: "activate-groups"})
	}))
	defer second.Close()
	client := NewClient(Config{APIAddrs: []string{first.URL, second.URL}})

	got, err := client.ChannelRuntimeSnapshots(context.Background(), model.ChannelRuntimeQuery{RunID: "run-a", Profile: "activate-groups"})

	require.NoError(t, err)
	require.Equal(t, []string{"first", "second"}, seen)
	require.Equal(t, []model.ChannelRuntimeSnapshot{
		{Version: "bench/v1", NodeID: 1, RunID: "run-a", Profile: "activate-groups"},
		{Version: "bench/v1", NodeID: 2, RunID: "run-a", Profile: "activate-groups"},
	}, got)
}

func TestClientChannelRuntimeSnapshotsTriesEveryTargetBeforeFailing(t *testing.T) {
	firstHits := 0
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/bench/v1/channel-runtime/snapshot", r.URL.Path)
		firstHits++
		http.Error(w, "snapshot unavailable", http.StatusServiceUnavailable)
	}))
	defer first.Close()
	secondHits := 0
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/bench/v1/channel-runtime/snapshot", r.URL.Path)
		secondHits++
		writeJSON(t, w, model.ChannelRuntimeSnapshot{Version: "bench/v1", NodeID: 2})
	}))
	defer second.Close()
	client := NewClient(Config{APIAddrs: []string{first.URL, second.URL}})

	got, err := client.ChannelRuntimeSnapshots(context.Background(), model.ChannelRuntimeQuery{RunID: "run-a"})

	require.Error(t, err)
	require.ErrorContains(t, err, "one or more target api addresses failed")
	require.ErrorContains(t, err, "503")
	require.Equal(t, []model.ChannelRuntimeSnapshot{{Version: "bench/v1", NodeID: 2}}, got)
	require.Equal(t, 1, firstHits)
	require.Equal(t, 1, secondHits)
}

func TestClientPresenceSnapshotsCallsEveryTarget(t *testing.T) {
	seen := make([]string, 0, 2)
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/bench/v1/presence/snapshot", r.URL.Path)
		seen = append(seen, "first")
		writeJSON(t, w, model.PresenceSnapshot{Version: "bench/v1", NodeID: 1, OwnerRoutesActive: 3})
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/bench/v1/presence/snapshot", r.URL.Path)
		seen = append(seen, "second")
		writeJSON(t, w, model.PresenceSnapshot{Version: "bench/v1", NodeID: 2, AuthorityRoutesActive: 5})
	}))
	defer second.Close()
	client := NewClient(Config{APIAddrs: []string{first.URL, second.URL}})

	got, err := client.PresenceSnapshots(context.Background())

	require.NoError(t, err)
	require.Equal(t, []string{"first", "second"}, seen)
	require.Equal(t, []model.PresenceSnapshot{
		{Version: "bench/v1", NodeID: 1, OwnerRoutesActive: 3},
		{Version: "bench/v1", NodeID: 2, AuthorityRoutesActive: 5},
	}, got)
}

func TestClientPresenceSnapshotsTriesEveryTargetBeforeFailing(t *testing.T) {
	firstHits := 0
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/bench/v1/presence/snapshot", r.URL.Path)
		firstHits++
		http.Error(w, "presence unavailable", http.StatusServiceUnavailable)
	}))
	defer first.Close()
	secondHits := 0
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/bench/v1/presence/snapshot", r.URL.Path)
		secondHits++
		writeJSON(t, w, model.PresenceSnapshot{Version: "bench/v1", NodeID: 2, OwnerRoutesPending: 1})
	}))
	defer second.Close()
	client := NewClient(Config{APIAddrs: []string{first.URL, second.URL}})

	got, err := client.PresenceSnapshots(context.Background())

	require.Error(t, err)
	require.ErrorContains(t, err, "one or more target api addresses failed")
	require.ErrorContains(t, err, "503")
	require.Equal(t, []model.PresenceSnapshot{{Version: "bench/v1", NodeID: 2, OwnerRoutesPending: 1}}, got)
	require.Equal(t, 1, firstHits)
	require.Equal(t, 1, secondHits)
}

func TestClientPresenceSnapshotsSkipsUnsupportedTargets(t *testing.T) {
	firstHits := 0
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/bench/v1/presence/snapshot", r.URL.Path)
		firstHits++
		http.Error(w, "presence snapshot is not configured", http.StatusNotImplemented)
	}))
	defer first.Close()
	secondHits := 0
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/bench/v1/presence/snapshot", r.URL.Path)
		secondHits++
		writeJSON(t, w, model.PresenceSnapshot{Version: "bench/v1", NodeID: 2, OwnerRoutesActive: 8})
	}))
	defer second.Close()
	client := NewClient(Config{APIAddrs: []string{first.URL, second.URL}})

	got, err := client.PresenceSnapshots(context.Background())

	require.NoError(t, err)
	require.Equal(t, []model.PresenceSnapshot{{Version: "bench/v1", NodeID: 2, OwnerRoutesActive: 8}}, got)
	require.Equal(t, 1, firstHits)
	require.Equal(t, 1, secondHits)
}

func TestClientProbeChannelRuntimePostsRequest(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/bench/v1/channel-runtime/probe", r.URL.Path)
		var req model.ChannelRuntimeProbeRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, model.ChannelRuntimeRange{Start: 0, End: 10}, req.Range)
		writeJSON(t, w, model.ChannelRuntimeProbeResult{Version: "bench/v1", NodeID: 1, Checked: 10})
	}))
	defer ts.Close()
	client := NewClient(Config{APIAddrs: []string{ts.URL}})

	got, err := client.ProbeChannelRuntime(context.Background(), model.ChannelRuntimeProbeRequest{
		RunID:   "run-a",
		Profile: "activate-groups",
		Range:   model.ChannelRuntimeRange{Start: 0, End: 10},
	})

	require.NoError(t, err)
	require.Equal(t, model.ChannelRuntimeProbeResult{Version: "bench/v1", NodeID: 1, Checked: 10}, got)
}

func TestClientProbeChannelRuntimeAllCallsEveryTarget(t *testing.T) {
	seen := make([]string, 0, 2)
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/bench/v1/channel-runtime/probe", r.URL.Path)
		var req model.ChannelRuntimeProbeRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, model.ChannelRuntimeRange{Start: 0, End: 10}, req.Range)
		seen = append(seen, "first")
		writeJSON(t, w, model.ChannelRuntimeProbeResult{Version: "bench/v1", NodeID: 1, Checked: 10, LoadedLeader: 4})
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/bench/v1/channel-runtime/probe", r.URL.Path)
		var req model.ChannelRuntimeProbeRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, model.ChannelRuntimeRange{Start: 0, End: 10}, req.Range)
		seen = append(seen, "second")
		writeJSON(t, w, model.ChannelRuntimeProbeResult{Version: "bench/v1", NodeID: 2, Checked: 10, LoadedLeader: 6})
	}))
	defer second.Close()
	client := NewClient(Config{APIAddrs: []string{first.URL, second.URL}})

	got, err := client.ProbeChannelRuntimeAll(context.Background(), model.ChannelRuntimeProbeRequest{
		RunID:   "run-a",
		Profile: "activate-groups",
		Range:   model.ChannelRuntimeRange{Start: 0, End: 10},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"first", "second"}, seen)
	require.Equal(t, []model.ChannelRuntimeProbeResult{
		{Version: "bench/v1", NodeID: 1, Checked: 10, LoadedLeader: 4},
		{Version: "bench/v1", NodeID: 2, Checked: 10, LoadedLeader: 6},
	}, got)
}

func TestClientProbeChannelRuntimeAllReturnsSuccessfulResultsWhenTargetFails(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/bench/v1/channel-runtime/probe", r.URL.Path)
		http.Error(w, "probe unavailable", http.StatusServiceUnavailable)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/bench/v1/channel-runtime/probe", r.URL.Path)
		writeJSON(t, w, model.ChannelRuntimeProbeResult{Version: "bench/v1", NodeID: 2, Checked: 10, LoadedLeader: 6})
	}))
	defer second.Close()
	client := NewClient(Config{APIAddrs: []string{first.URL, second.URL}})

	got, err := client.ProbeChannelRuntimeAll(context.Background(), model.ChannelRuntimeProbeRequest{
		RunID:   "run-a",
		Profile: "activate-groups",
		Range:   model.ChannelRuntimeRange{Start: 0, End: 10},
	})

	require.Error(t, err)
	require.ErrorContains(t, err, "target api addresses failed")
	require.ErrorContains(t, err, "503")
	require.Equal(t, []model.ChannelRuntimeProbeResult{
		{Version: "bench/v1", NodeID: 2, Checked: 10, LoadedLeader: 6},
	}, got)
}

func TestClientProbeChannelRuntimeFallbackDoesNotKeepStaleDecodedFields(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/bench/v1/channel-runtime/probe", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"stale","checked":99,"node_id":"bad"}`))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/bench/v1/channel-runtime/probe", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"node_id":2}`))
	}))
	defer second.Close()
	client := NewClient(Config{APIAddrs: []string{first.URL, second.URL}})

	got, err := client.ProbeChannelRuntime(context.Background(), model.ChannelRuntimeProbeRequest{
		RunID:   "run-a",
		Profile: "activate-groups",
		Range:   model.ChannelRuntimeRange{Start: 0, End: 10},
	})

	require.NoError(t, err)
	require.Equal(t, model.ChannelRuntimeProbeResult{NodeID: 2}, got)
}

func TestClientEvictChannelRuntimePostsRequest(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/bench/v1/channel-runtime/evict", r.URL.Path)
		var req model.ChannelRuntimeEvictRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, model.ChannelRuntimeRange{Start: 0, End: 10}, req.Range)
		writeJSON(t, w, model.ChannelRuntimeEvictResult{Version: "bench/v1", NodeID: 1, Requested: 10, Evicted: 10})
	}))
	defer ts.Close()
	client := NewClient(Config{APIAddrs: []string{ts.URL}})

	got, err := client.EvictChannelRuntime(context.Background(), model.ChannelRuntimeEvictRequest{
		RunID:   "run-a",
		Profile: "activate-groups",
		Range:   model.ChannelRuntimeRange{Start: 0, End: 10},
	})

	require.NoError(t, err)
	require.Equal(t, model.ChannelRuntimeEvictResult{Version: "bench/v1", NodeID: 1, Requested: 10, Evicted: 10}, got)
}

func TestClientEvictChannelRuntimeAllCallsEveryTarget(t *testing.T) {
	seen := make([]string, 0, 2)
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/bench/v1/channel-runtime/evict", r.URL.Path)
		var req model.ChannelRuntimeEvictRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, model.ChannelRuntimeRange{Start: 0, End: 10}, req.Range)
		seen = append(seen, "first")
		writeJSON(t, w, model.ChannelRuntimeEvictResult{Version: "bench/v1", NodeID: 1, Requested: 10, Evicted: 9})
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/bench/v1/channel-runtime/evict", r.URL.Path)
		var req model.ChannelRuntimeEvictRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, model.ChannelRuntimeRange{Start: 0, End: 10}, req.Range)
		seen = append(seen, "second")
		writeJSON(t, w, model.ChannelRuntimeEvictResult{Version: "bench/v1", NodeID: 2, Requested: 10, Evicted: 10})
	}))
	defer second.Close()
	client := NewClient(Config{APIAddrs: []string{first.URL, second.URL}})

	got, err := client.EvictChannelRuntimeAll(context.Background(), model.ChannelRuntimeEvictRequest{
		RunID:   "run-a",
		Profile: "activate-groups",
		Range:   model.ChannelRuntimeRange{Start: 0, End: 10},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"first", "second"}, seen)
	require.Equal(t, []model.ChannelRuntimeEvictResult{
		{Version: "bench/v1", NodeID: 1, Requested: 10, Evicted: 9},
		{Version: "bench/v1", NodeID: 2, Requested: 10, Evicted: 10},
	}, got)
}

func TestClientEvictChannelRuntimeAllReturnsSuccessfulResultsWhenTargetDecodeFails(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/bench/v1/channel-runtime/evict", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"stale","requested":99,"node_id":"bad"}`))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/bench/v1/channel-runtime/evict", r.URL.Path)
		writeJSON(t, w, model.ChannelRuntimeEvictResult{Version: "bench/v1", NodeID: 2, Requested: 10, Evicted: 10})
	}))
	defer second.Close()
	client := NewClient(Config{APIAddrs: []string{first.URL, second.URL}})

	got, err := client.EvictChannelRuntimeAll(context.Background(), model.ChannelRuntimeEvictRequest{
		RunID:   "run-a",
		Profile: "activate-groups",
		Range:   model.ChannelRuntimeRange{Start: 0, End: 10},
	})

	require.Error(t, err)
	require.ErrorContains(t, err, "target api addresses failed")
	require.ErrorContains(t, err, "decode")
	require.Equal(t, []model.ChannelRuntimeEvictResult{
		{Version: "bench/v1", NodeID: 2, Requested: 10, Evicted: 10},
	}, got)
}

func TestHealthAndReadyUseConfiguredAPIAddress(t *testing.T) {
	seen := make(map[string]int)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path]++
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	client := NewClient(Config{APIAddrs: []string{ts.URL}})

	require.NoError(t, client.Healthz(context.Background()))
	require.NoError(t, client.Readyz(context.Background()))

	require.Equal(t, 1, seen["/healthz"])
	require.Equal(t, 1, seen["/readyz"])
}

func TestUserAndChannelMutationsPostSpecShapedRequestsToFirstAddress(t *testing.T) {
	firstSeen := make([]string, 0)
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstSeen = append(firstSeen, r.URL.Path)
		require.Equal(t, "Bearer bench-secret", r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/bench/v1/users/tokens":
			var req model.BatchTokensRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			require.Equal(t, []model.UserTokenItem{{UID: "u1", Token: "t1"}}, req.Users)
		case "/bench/v1/channels":
			var req model.BatchChannelsRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			require.Equal(t, []model.ChannelItem{{ChannelID: "g1", ChannelType: 2}}, req.Channels)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("mutation should use first address, got %s", r.URL.Path)
	}))
	defer second.Close()
	client := NewClient(Config{APIAddrs: []string{first.URL, second.URL}, Token: "bench-secret"})

	require.NoError(t, client.UpsertTokens(context.Background(), model.BatchTokensRequest{RunID: "run", BatchID: "b1", Upsert: true, Users: []model.UserTokenItem{{UID: "u1", Token: "t1"}}}))
	require.NoError(t, client.UpsertChannels(context.Background(), model.BatchChannelsRequest{RunID: "run", BatchID: "b2", Upsert: true, Channels: []model.ChannelItem{{ChannelID: "g1", ChannelType: 2}}}))

	require.Equal(t, []string{"/bench/v1/users/tokens", "/bench/v1/channels"}, firstSeen)
}

func TestAddSubscribersPostsToFirstHealthyAPIAddress(t *testing.T) {
	hits := make(map[string]int)
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/bench/v1/channels/subscribers", r.URL.Path)
		hits["first"]++
		w.WriteHeader(http.StatusOK)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("AddSubscribers should use the first healthy address, got %s", r.URL.Path)
	}))
	defer second.Close()
	client := NewClient(Config{APIAddrs: []string{first.URL, second.URL}})

	err := client.AddSubscribers(context.Background(), model.BatchSubscribersRequest{
		RunID:   "run",
		BatchID: "b3",
		Items: []model.SubscriberItem{{
			ChannelID:   "g1",
			ChannelType: 2,
			Subscribers: []string{"u1", "u2"},
		}},
	})

	require.NoError(t, err)
	require.Equal(t, 1, hits["first"])
}

func TestMutationsFallBackToNextAPIAddress(t *testing.T) {
	firstHits := 0
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstHits++
		http.Error(w, "temporary unavailable", http.StatusServiceUnavailable)
	}))
	defer first.Close()
	secondHits := make(map[string]int)
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondHits[r.URL.Path]++
		w.WriteHeader(http.StatusOK)
	}))
	defer second.Close()
	client := NewClient(Config{APIAddrs: []string{first.URL, second.URL}})

	require.NoError(t, client.UpsertTokens(context.Background(), model.BatchTokensRequest{RunID: "run", BatchID: "b1", Users: []model.UserTokenItem{{UID: "u1", Token: "t1"}}}))
	require.NoError(t, client.UpsertChannels(context.Background(), model.BatchChannelsRequest{RunID: "run", BatchID: "b2", Channels: []model.ChannelItem{{ChannelID: "g1", ChannelType: 2}}}))
	require.NoError(t, client.AddSubscribers(context.Background(), model.BatchSubscribersRequest{RunID: "run", BatchID: "b3", Items: []model.SubscriberItem{{ChannelID: "g1", ChannelType: 2, Subscribers: []string{"u1"}}}}))

	require.Equal(t, 3, firstHits)
	require.Equal(t, 1, secondHits["/bench/v1/users/tokens"])
	require.Equal(t, 1, secondHits["/bench/v1/channels"])
	require.Equal(t, 1, secondHits["/bench/v1/channels/subscribers"])
}

func TestSnapshotMapsNon2xxStatusAndBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/bench/v1/snapshot", r.URL.Path)
		http.Error(w, "database unavailable with a long body that should be clipped", http.StatusServiceUnavailable)
	}))
	defer ts.Close()
	client := NewClient(Config{APIAddrs: []string{ts.URL}})

	_, err := client.Snapshot(context.Background())

	require.ErrorContains(t, err, "503")
	require.ErrorContains(t, err, "database unavailable")
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(v))
}
