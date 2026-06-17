package sim

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTargetPreflightDiscoversGateway(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz":
			w.WriteHeader(http.StatusOK)
		case "/bench/v1/capabilities":
			writeJSON(t, w, map[string]any{
				"enabled": true,
				"version": "bench/v1",
				"supports": map[string]any{
					"channels_batch":            true,
					"channel_subscribers_batch": true,
					"snapshot":                  true,
					"channel_types":             []string{"person", "group"},
				},
				"limits": map[string]any{
					"max_batch_size": 10,
				},
			})
		case "/bench/v1/capacity-target":
			writeJSON(t, w, map[string]any{
				"version": "bench/v1",
				"gateway": map[string]any{
					"tcp_addr": "127.0.0.1:5100",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newTargetClient([]string{server.URL}, " token-1 ")
	target, err := client.preflight(context.Background())
	if err != nil {
		t.Fatalf("preflight failed: %v", err)
	}
	if got, want := target.GatewayTCPAddrs, []string{"127.0.0.1:5100"}; !equalStrings(got, want) {
		t.Fatalf("gateway TCP addresses = %#v, want %#v", got, want)
	}
	if target.MaxBatchSize != 10 {
		t.Fatalf("max batch size = %d, want 10", target.MaxBatchSize)
	}
}

func TestTargetPreflightRejectsMissingGroupSupport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz":
			w.WriteHeader(http.StatusOK)
		case "/bench/v1/capabilities":
			writeJSON(t, w, map[string]any{
				"enabled": true,
				"version": "bench/v1",
				"supports": map[string]any{
					"channels_batch":            true,
					"channel_subscribers_batch": true,
					"snapshot":                  true,
					"channel_types":             []string{"person"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newTargetClient([]string{server.URL}, "")
	_, err := client.preflight(context.Background())
	if err == nil {
		t.Fatalf("preflight succeeded, want group support error")
	}
	if !strings.Contains(err.Error(), "channel_types group") {
		t.Fatalf("preflight error = %q, want channel_types group", err.Error())
	}
}

func TestTargetSetupPostsChannelsAndSubscribers(t *testing.T) {
	var capturedChannels channelsRequest
	var capturedSubscribers subscribersRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "Bearer token-1"; got != want {
			t.Fatalf("authorization header = %q, want %q", got, want)
		}
		switch r.URL.Path {
		case "/bench/v1/channels":
			if err := json.NewDecoder(r.Body).Decode(&capturedChannels); err != nil {
				t.Fatalf("decode channels request: %v", err)
			}
			writeJSON(t, w, mutationResponse{
				RunID:    capturedChannels.RunID,
				BatchID:  capturedChannels.BatchID,
				Accepted: len(capturedChannels.Channels),
			})
		case "/bench/v1/channels/subscribers":
			if err := json.NewDecoder(r.Body).Decode(&capturedSubscribers); err != nil {
				t.Fatalf("decode subscribers request: %v", err)
			}
			writeJSON(t, w, subscribersResponse{
				RunID:               capturedSubscribers.RunID,
				BatchID:             capturedSubscribers.BatchID,
				Accepted:            len(capturedSubscribers.Items),
				AcceptedSubscribers: len(capturedSubscribers.Items[0].Subscribers),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := Config{RunID: "run-1"}
	plan := Plan{
		Groups: []Group{{
			ChannelID:   "group-1",
			Subscribers: []string{"u1", "u2"},
		}},
	}
	client := newTargetClient([]string{server.URL}, "token-1")
	err := client.setup(context.Background(), cfg, targetPreflight{MaxBatchSize: 10}, plan)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if capturedChannels.RunID != "run-1" {
		t.Fatalf("channels run id = %q, want run-1", capturedChannels.RunID)
	}
	if capturedChannels.BatchID != "channels-000000" {
		t.Fatalf("channels batch id = %q, want channels-000000", capturedChannels.BatchID)
	}
	if len(capturedChannels.Channels) != 1 {
		t.Fatalf("channels count = %d, want 1", len(capturedChannels.Channels))
	}
	channel := capturedChannels.Channels[0]
	if channel.ChannelID != "group-1" || channel.ChannelType != 2 || !channel.AllowStranger {
		t.Fatalf("channel item = %#v", channel)
	}

	if capturedSubscribers.RunID != "run-1" {
		t.Fatalf("subscribers run id = %q, want run-1", capturedSubscribers.RunID)
	}
	if capturedSubscribers.BatchID != "subscribers-000000" {
		t.Fatalf("subscribers batch id = %q, want subscribers-000000", capturedSubscribers.BatchID)
	}
	if len(capturedSubscribers.Items) != 1 {
		t.Fatalf("subscriber items = %d, want 1", len(capturedSubscribers.Items))
	}
	item := capturedSubscribers.Items[0]
	if item.ChannelID != "group-1" || item.ChannelType != 2 || !equalStrings(item.Subscribers, []string{"u1", "u2"}) {
		t.Fatalf("subscriber item = %#v", item)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode json: %v", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
