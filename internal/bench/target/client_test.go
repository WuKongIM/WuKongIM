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

func TestMutationsPostSpecShapedRequestsToFirstAddress(t *testing.T) {
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
		case "/bench/v1/channels/subscribers":
			var req model.BatchSubscribersRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			require.Equal(t, []model.SubscriberItem{{ChannelID: "g1", ChannelType: 2, Subscribers: []string{"u1", "u2"}}}, req.Items)
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
	require.NoError(t, client.AddSubscribers(context.Background(), model.BatchSubscribersRequest{RunID: "run", BatchID: "b3", Items: []model.SubscriberItem{{ChannelID: "g1", ChannelType: 2, Subscribers: []string{"u1", "u2"}}}}))

	require.Equal(t, []string{"/bench/v1/users/tokens", "/bench/v1/channels", "/bench/v1/channels/subscribers"}, firstSeen)
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
