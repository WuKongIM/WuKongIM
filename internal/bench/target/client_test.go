package target

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
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

func TestClientProbeChannelRuntimeAllPostsExplicitChannelsWithAuthAndDecodesDetails(t *testing.T) {
	const token = "probe-secret-token"
	channels := []model.ChannelRuntimeChannelIdentity{
		{ChannelID: "canonical-person-a", ChannelType: 1},
		{ChannelID: "canonical-person-b", ChannelType: 1},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/bench/v1/channel-runtime/probe", r.URL.Path)
		require.Equal(t, "Bearer "+token, r.Header.Get("Authorization"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.JSONEq(t, `{
			"run_id":"",
			"profile":"",
			"channel_type":0,
			"range":{"start":0,"end":0},
			"channels":[
				{"channel_id":"canonical-person-a","channel_type":1},
				{"channel_id":"canonical-person-b","channel_type":1}
			]
		}`, string(body))
		writeJSON(t, w, model.ChannelRuntimeProbeResult{
			Version: "bench/v1", NodeID: 2, Checked: 2, LoadedLeader: 1,
			Channels: []model.ChannelRuntimeProbeChannel{
				{ChannelID: "canonical-person-a", ChannelType: 1, Role: "leader", Status: "active", LEO: 17, HW: 16, CheckpointHW: 15, LeaderEpoch: 8, ChannelEpoch: 5},
				{ChannelID: "canonical-person-b", ChannelType: 1, Role: "missing", Status: "missing"},
			},
		})
	}))
	defer ts.Close()
	client := NewClient(Config{APIAddrs: []string{ts.URL}, Token: token})

	got, err := client.ProbeChannelRuntimeAll(context.Background(), model.ChannelRuntimeProbeRequest{Channels: channels})

	require.NoError(t, err)
	require.Equal(t, []model.ChannelRuntimeProbeResult{{
		Version: "bench/v1", NodeID: 2, Checked: 2, LoadedLeader: 1,
		Channels: []model.ChannelRuntimeProbeChannel{
			{ChannelID: "canonical-person-a", ChannelType: 1, Role: "leader", Status: "active", LEO: 17, HW: 16, CheckpointHW: 15, LeaderEpoch: 8, ChannelEpoch: 5},
			{ChannelID: "canonical-person-b", ChannelType: 1, Role: "missing", Status: "missing"},
		},
	}}, got)
}

func TestClientProbeChannelRuntimeErrorDoesNotExposeCredentialsOrRequestIDs(t *testing.T) {
	const token = "probe-secret-token"
	const channelID = "canonical-sensitive-person"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "probe rejected for "+channelID+" using "+token, http.StatusBadRequest)
	}))
	defer ts.Close()
	client := NewClient(Config{APIAddrs: []string{ts.URL}, Token: token})

	_, err := client.ProbeChannelRuntime(context.Background(), model.ChannelRuntimeProbeRequest{
		Channels: []model.ChannelRuntimeChannelIdentity{{ChannelID: channelID, ChannelType: 1}},
	})

	require.Error(t, err)
	require.NotContains(t, err.Error(), token)
	require.NotContains(t, err.Error(), channelID)
}

func TestClientProbeChannelRuntimeRejectsOversizedSuccessBody(t *testing.T) {
	const token = "probe-secret-token"
	const channelID = "canonical-sensitive-person"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"channels":"`))
		_, _ = io.WriteString(w, strings.Repeat("x", 32<<20))
		_, _ = io.WriteString(w, channelID+token+`"}`)
	}))
	defer ts.Close()
	client := NewClient(Config{APIAddrs: []string{ts.URL}, Token: token})

	_, err := client.ProbeChannelRuntime(context.Background(), model.ChannelRuntimeProbeRequest{
		Channels: []model.ChannelRuntimeChannelIdentity{{ChannelID: channelID, ChannelType: 1}},
	})

	require.ErrorContains(t, err, "channel runtime probe response exceeds byte limit")
	require.NotContains(t, err.Error(), token)
	require.NotContains(t, err.Error(), channelID)
}

func TestClientProbeChannelRuntimeRejectsInvalidDetailedCardinality(t *testing.T) {
	const sentinel = "canonical-sensitive-person"
	tests := []struct {
		name string
		req  model.ChannelRuntimeProbeRequest
		rows int
	}{
		{name: "explicit over requested", req: model.ChannelRuntimeProbeRequest{Channels: []model.ChannelRuntimeChannelIdentity{{ChannelID: sentinel, ChannelType: 1}}}, rows: 2},
		{name: "explicit over bound", req: model.ChannelRuntimeProbeRequest{Channels: make([]model.ChannelRuntimeChannelIdentity, 1200)}, rows: 1201},
		{name: "generated carries details", req: model.ChannelRuntimeProbeRequest{RunID: "run-a", Profile: "person", ChannelType: 1, Range: model.ChannelRuntimeRange{Start: 0, End: 1}}, rows: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rows := make([]model.ChannelRuntimeProbeChannel, tt.rows)
				if len(rows) > 0 {
					rows[0].ChannelID = sentinel
				}
				writeJSON(t, w, model.ChannelRuntimeProbeResult{Checked: len(tt.req.Channels), Channels: rows})
			}))
			defer ts.Close()

			_, err := NewClient(Config{APIAddrs: []string{ts.URL}}).ProbeChannelRuntime(context.Background(), tt.req)

			require.ErrorContains(t, err, "invalid channel runtime probe response")
			require.NotContains(t, err.Error(), sentinel)
		})
	}
}

func TestClientProbeChannelRuntimeRejectsInvalidExplicitRequestCardinalityBeforeSending(t *testing.T) {
	const sentinel = "canonical-sensitive-request-token"
	tests := []struct {
		name     string
		channels []model.ChannelRuntimeChannelIdentity
	}{
		{name: "empty", channels: make([]model.ChannelRuntimeChannelIdentity, 0)},
		{name: "over bound", channels: make([]model.ChannelRuntimeChannelIdentity, 1201)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.channels) > 0 {
				tt.channels[0] = model.ChannelRuntimeChannelIdentity{ChannelID: sentinel, ChannelType: 1}
			}
			hits := 0
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				writeJSON(t, w, model.ChannelRuntimeProbeResult{})
			}))
			defer ts.Close()

			_, err := NewClient(Config{APIAddrs: []string{ts.URL}, Token: sentinel}).ProbeChannelRuntime(
				context.Background(),
				model.ChannelRuntimeProbeRequest{Channels: tt.channels},
			)

			require.ErrorContains(t, err, "invalid channel runtime probe request")
			require.NotContains(t, err.Error(), sentinel)
			require.Zero(t, hits)
		})
	}
}

func TestClientProbeChannelRuntimeValidatesExplicitResponseIdentityAndOrder(t *testing.T) {
	const (
		firstID  = "canonical-sensitive-first-token"
		secondID = "canonical-sensitive-second-token"
		extraID  = "canonical-sensitive-extra-token"
		auth     = "probe-sensitive-auth-token"
	)
	requested := []model.ChannelRuntimeChannelIdentity{
		{ChannelID: firstID, ChannelType: 1},
		{ChannelID: secondID, ChannelType: 2},
	}
	first := model.ChannelRuntimeProbeChannel{ChannelID: firstID, ChannelType: 1, Role: "leader", Status: "active"}
	second := model.ChannelRuntimeProbeChannel{ChannelID: secondID, ChannelType: 2, Role: "missing", Status: "missing"}
	tests := []struct {
		name    string
		rows    []model.ChannelRuntimeProbeChannel
		wantErr bool
	}{
		{name: "zero rows", rows: nil, wantErr: true},
		{name: "omission", rows: []model.ChannelRuntimeProbeChannel{first}, wantErr: true},
		{name: "duplicate substitution", rows: []model.ChannelRuntimeProbeChannel{first, first}, wantErr: true},
		{name: "unrequested substitution", rows: []model.ChannelRuntimeProbeChannel{first, {ChannelID: extraID, ChannelType: 2}}, wantErr: true},
		{name: "extra row", rows: []model.ChannelRuntimeProbeChannel{first, second, {ChannelID: extraID, ChannelType: 3}}, wantErr: true},
		{name: "reordered", rows: []model.ChannelRuntimeProbeChannel{second, first}, wantErr: true},
		{name: "valid ordered result", rows: []model.ChannelRuntimeProbeChannel{first, second}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, model.ChannelRuntimeProbeResult{Checked: len(requested), Channels: tt.rows})
			}))
			defer ts.Close()

			got, err := NewClient(Config{APIAddrs: []string{ts.URL}, Token: auth}).ProbeChannelRuntime(
				context.Background(),
				model.ChannelRuntimeProbeRequest{Channels: requested},
			)

			if !tt.wantErr {
				require.NoError(t, err)
				require.Equal(t, tt.rows, got.Channels)
				return
			}
			require.ErrorContains(t, err, "invalid channel runtime probe response")
			for _, secret := range []string{firstID, secondID, extraID, auth} {
				require.NotContains(t, err.Error(), secret)
			}
		})
	}
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

func TestClientConversationSyncPostsExactProductRequestAndDecodesRecents(t *testing.T) {
	const benchToken = "bench-secret-not-for-product-routes"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/conversation/sync", r.URL.Path)
		require.Empty(t, r.Header.Get("Authorization"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.JSONEq(t, `{
			"uid":"derived-user",
			"version":0,
			"last_msg_seqs":"",
			"msg_count":20,
			"only_unread":0,
			"limit":500
		}`, string(body))
		_, _ = io.WriteString(w, `[{
			"channel_id":"peer-user",
			"channel_type":1,
			"unread":3,
			"timestamp":1722787200,
			"last_msg_seq":19,
			"last_client_msg_no":"client-19",
			"offset_msg_seq":7,
			"readed_to_msg_seq":16,
			"version":23,
			"recents":[{
				"message_id":101,
				"message_idstr":"101",
				"message_seq":19,
				"client_msg_no":"client-19",
				"from_uid":"sender-user",
				"channel_id":"peer-user",
				"channel_type":1,
				"timestamp":1722787200,
				"payload":"bGlmZWN5Y2xlLW1hcmtlcg=="
			}]
		}]`)
	}))
	defer server.Close()

	client := NewClient(Config{APIAddrs: []string{server.URL}, Token: benchToken})
	got, err := client.ConversationSync(context.Background(), ConversationSyncRequest{
		UID: "derived-user", Version: 0, LastMsgSeqs: "", MsgCount: 20, OnlyUnread: 0, Limit: 500,
	})

	require.NoError(t, err)
	require.Equal(t, []ConversationSyncConversation{{
		ChannelID: "peer-user", ChannelType: 1, Unread: 3, Timestamp: 1722787200,
		LastMsgSeq: 19, LastClientMsgNo: "client-19", OffsetMsgSeq: 7,
		ReadedToMsgSeq: 16, Version: 23,
		Recents: []ConversationSyncMessage{{
			MessageID: 101, MessageIDStr: "101", MessageSeq: 19,
			ClientMsgNo: "client-19", FromUID: "sender-user", ChannelID: "peer-user",
			ChannelType: 1, Timestamp: 1722787200, Payload: []byte("lifecycle-marker"),
		}},
	}}, got)
}

func TestClientConversationSyncFallbackDoesNotKeepStaleDecodedRows(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/conversation/sync", r.URL.Path)
		_, _ = io.WriteString(w, `[{"channel_id":"stale","channel_type":1},{"channel_type":"bad"}]`)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/conversation/sync", r.URL.Path)
		_, _ = io.WriteString(w, `[{"channel_id":"fresh","channel_type":2}]`)
	}))
	defer second.Close()

	got, err := NewClient(Config{APIAddrs: []string{first.URL, second.URL}}).ConversationSync(
		context.Background(),
		ConversationSyncRequest{UID: "derived-user", Version: 0, LastMsgSeqs: "", MsgCount: 20, Limit: 500},
	)

	require.NoError(t, err)
	require.Equal(t, []ConversationSyncConversation{{ChannelID: "fresh", ChannelType: 2}}, got)
}

func TestClientConversationSyncErrorsDoNotExposeProductIdentitiesOrBenchToken(t *testing.T) {
	const uid = "sensitive-derived-user"
	const payload = "sensitive-payload"
	const benchToken = "sensitive-bench-token"
	statusServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Empty(t, r.Header.Get("Authorization"))
		http.Error(w, uid+" "+payload+" "+benchToken, http.StatusBadRequest)
	}))
	defer statusServer.Close()
	invalidBase64Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[{"channel_id":"peer","channel_type":1,"recents":[{"payload":"%%%not-base64%%%"}]}]`)
	}))
	defer invalidBase64Server.Close()

	_, err := NewClient(Config{
		APIAddrs: []string{statusServer.URL, invalidBase64Server.URL},
		Token:    benchToken,
	}).ConversationSync(context.Background(), ConversationSyncRequest{UID: uid, MsgCount: 20, Limit: 500})

	require.Error(t, err)
	require.ErrorContains(t, err, "decode")
	require.NotContains(t, err.Error(), uid)
	require.NotContains(t, err.Error(), payload)
	require.NotContains(t, err.Error(), benchToken)
}

func TestClientConversationSyncRejectsMalformedRecentJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[{"channel_id":"peer","channel_type":1,"recents":[{"message_seq":1,]}]`)
	}))
	defer server.Close()

	_, err := NewClient(Config{APIAddrs: []string{server.URL}}).ConversationSync(
		context.Background(),
		ConversationSyncRequest{UID: "derived-user", MsgCount: 20, Limit: 500},
	)

	require.ErrorContains(t, err, "decode")
	require.NotContains(t, err.Error(), "derived-user")
}

func TestClientConversationSyncRejectsOversizedSuccessBody(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        make(http.Header),
			Body:          io.NopCloser(strings.NewReader("")),
			ContentLength: maxConversationSyncResponseBytes + 1,
			Request:       req,
		}, nil
	})}

	_, err := NewClient(Config{APIAddrs: []string{"http://api.example.test"}, HTTPClient: httpClient}).ConversationSync(
		context.Background(),
		ConversationSyncRequest{UID: "derived-user", MsgCount: 20, Limit: 500},
	)

	require.ErrorContains(t, err, "conversation sync response exceeds byte limit")
}

func TestClientConversationSyncPreservesContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("transport must not start for a canceled conversation sync")
		return nil, nil
	})}

	_, err := NewClient(Config{
		APIAddrs:   []string{"http://api-1.example.test", "http://api-2.example.test"},
		HTTPClient: httpClient,
	}).ConversationSync(ctx, ConversationSyncRequest{UID: "derived-user", MsgCount: 20, Limit: 500})

	require.ErrorIs(t, err, context.Canceled)
}

func TestDecodeJSONLimitedRejectsStreamingLimitPlusOne(t *testing.T) {
	const body = `{"value":1}`
	var got struct {
		Value int `json:"value"`
	}

	err := decodeJSONLimited(strings.NewReader(body), &got, int64(len(body)-1), "response exceeds byte limit")

	require.EqualError(t, err, "response exceeds byte limit")
}

func TestDecodeJSONLimitedRejectsSecondJSONValue(t *testing.T) {
	const body = `{"value":1} {"value":2}`
	var got struct {
		Value int `json:"value"`
	}

	err := decodeJSONLimited(strings.NewReader(body), &got, int64(len(body)), "response exceeds byte limit")

	require.EqualError(t, err, "multiple JSON values in response")
}

func TestDecodeJSONLimitedAcceptsExactLimit(t *testing.T) {
	const body = `{"value":1}`
	var got struct {
		Value int `json:"value"`
	}

	err := decodeJSONLimited(strings.NewReader(body), &got, int64(len(body)), "response exceeds byte limit")

	require.NoError(t, err)
	require.Equal(t, 1, got.Value)
}

func TestDecodeJSONLimitedAcceptsTrailingWhitespace(t *testing.T) {
	const body = "{\"value\":1} \n\t"
	var got struct {
		Value int `json:"value"`
	}

	err := decodeJSONLimited(strings.NewReader(body), &got, int64(len(body)), "response exceeds byte limit")

	require.NoError(t, err)
	require.Equal(t, 1, got.Value)
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

func TestRemoveSubscribersPostsToRemovalEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/bench/v1/channels/subscribers/remove", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(Config{APIAddrs: []string{server.URL}})
	require.NoError(t, client.RemoveSubscribers(context.Background(), model.BatchSubscribersRequest{
		RunID: "run", BatchID: "remove-1",
		Items: []model.SubscriberItem{{ChannelID: "g1", ChannelType: 2, Subscribers: []string{"u1"}}},
	}))
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

func TestObserverReadsBoundedProtectedDebugAndMetrics(t *testing.T) {
	const token = "observer-secret-token"
	seen := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.RequestURI()]++
		require.Equal(t, "Bearer "+token, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/debug/config":
			_, _ = io.WriteString(w, `{"node_id":1,"node_data_dir":"/srv/wukongim/node-1","initial_slot_count":12,"hash_slot_count":256,"slot_replica_count":3,"channel_replica_count":3,"channel_max_loaded_count":50000,"future_field":"ignored"}`)
		case "/debug/goroutines/summary":
			_, _ = io.WriteString(w, `{"generated_at":"2030-03-17T17:46:41Z","process_started_at":"2030-03-14T17:46:40Z","boot_id":"process-1","process_total":42,"future_field":"ignored"}`)
		case "/debug/cluster":
			_, _ = io.WriteString(w, `{"node_id":1,"state_revision":9,"slots":[{"slot_id":1,"leader_id":1,"replicas":[1,2,3],"voters":[1,2,3],"term":7,"commit_index":100,"applied_index":100,"replica_progress":[{"node_id":1,"match_index":100,"lag_entries":0,"state":"StateReplicate"},{"node_id":2,"match_index":99,"lag_entries":1,"state":"StateReplicate"},{"node_id":3,"match_index":98,"lag_entries":2,"state":"StateProbe"}],"future_field":true}],"future_field":"ignored"}`)
		case "/metrics":
			_, _ = io.WriteString(w, strings.Join([]string{
				`unrelated_future_metric{description="value with spaces"} 1`,
				"go_goroutines 42",
				"go_memstats_heap_alloc_bytes 1024",
				"process_resident_memory_bytes 2048",
				`wukongim_runtime_pool_queue_depth{pool="append"} 3`,
				`wukongim_runtime_pool_queue_capacity{pool="append"} 10`,
				`wukongim_runtime_pool_inflight{pool="append"} 2`,
				`wukongim_channelv2_worker_queue_depth{worker="meta"} 4`,
				`wukongim_channelv2_worker_queue_capacity{worker="meta"} 40`,
				`wukongim_channelv2_activation_rejected_total{reason="capacity"} 5`,
				`wukongim_channelv2_meta_created_total{slot_id="1",result="created"} 6`,
				`wukongim_channelv2_meta_created_total{slot_id="1",result="already_existing"} 7`,
				`wukongim_channelv2_meta_created_total{slot_id="1",result="error"} 8`,
			}, "\n")+"\n")
		case "/debug/pprof/heap":
			require.Equal(t, "1", r.URL.Query().Get("gc"))
			_, _ = io.WriteString(w, "bounded-profile")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient(Config{APIAddrs: []string{server.URL}, Token: token})

	config, err := client.DebugConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, DebugConfig{NodeID: 1, NodeDataDir: "/srv/wukongim/node-1", InitialSlotCount: 12, HashSlotCount: 256, SlotReplicaCount: 3, ChannelReplicaCount: 3, MaxChannels: 50000}, config)
	goroutines, err := client.DebugGoroutineSummary(context.Background())
	require.NoError(t, err)
	require.Equal(t, DebugGoroutineSummary{
		GeneratedAt:      time.Date(2030, time.March, 17, 17, 46, 41, 0, time.UTC),
		ProcessStartedAt: time.Date(2030, time.March, 14, 17, 46, 40, 0, time.UTC),
		BootID:           "process-1",
	}, goroutines)
	cluster, err := client.DebugCluster(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(9), cluster.StateRevision)
	require.Equal(t, []ClusterSlot{{
		SlotID: 1, LeaderID: 1, Replicas: []uint64{1, 2, 3}, Voters: []uint64{1, 2, 3}, Term: 7, CommitIndex: 100, AppliedIndex: 100,
		ReplicaProgress: []ReplicaProgress{{NodeID: 1, MatchIndex: 100, State: "StateReplicate"}, {NodeID: 2, MatchIndex: 99, LagEntries: 1, State: "StateReplicate"}, {NodeID: 3, MatchIndex: 98, LagEntries: 2, State: "StateProbe"}},
	}}, cluster.Slots)
	metrics, err := client.Metrics(context.Background())
	require.NoError(t, err)
	require.Equal(t, float64(42), metrics.GoGoroutines)
	require.Equal(t, float64(1024), metrics.GoHeapAllocBytes)
	require.Equal(t, float64(2048), metrics.ProcessResidentMemoryBytes)
	require.Equal(t, float64(3), metrics.RuntimeQueueDepth)
	require.Equal(t, float64(10), metrics.RuntimeQueueCapacity)
	require.Equal(t, float64(30), metrics.RuntimeQueueMaxPercent)
	require.Equal(t, float64(2), metrics.RuntimeInflight)
	require.Equal(t, float64(4), metrics.ChannelWorkerQueueDepth)
	require.Equal(t, float64(40), metrics.ChannelWorkerQueueCapacity)
	require.Equal(t, float64(10), metrics.ChannelWorkerQueueMaxPercent)
	require.Equal(t, float64(5), metrics.ActivationRejectedTotal)
	require.Equal(t, MetaCreateSlotCounters{Created: 6, AlreadyExisting: 7, Errors: 8}, metrics.MetaCreatedBySlot[0])
	require.Equal(t, map[string]float64{"created": 6, "already_existing": 7, "error": 8}, metrics.MetaCreatedTotal)
	require.NoError(t, metrics.ValidateRequired())
	require.NoError(t, client.ForceGC(context.Background()))
	require.Equal(t, 1, seen["/debug/config"])
	require.Equal(t, 1, seen["/debug/goroutines/summary"])
	require.Equal(t, 1, seen["/debug/cluster"])
	require.Equal(t, 1, seen["/metrics"])
	require.Equal(t, 1, seen["/debug/pprof/heap?gc=1"])
}

func TestObservationMetricsRejectsInvalidMetadataCreateSlotSeries(t *testing.T) {
	complete := strings.Join([]string{
		`wukongim_channelv2_meta_created_total{slot_id="1",result="created"} 1`,
		`wukongim_channelv2_meta_created_total{slot_id="1",result="already_existing"} 2`,
		`wukongim_channelv2_meta_created_total{slot_id="1",result="error"} 0`,
	}, "\n") + "\n"
	tests := []struct {
		name   string
		scrape string
	}{
		{name: "missing slot", scrape: `wukongim_channelv2_meta_created_total{result="created"} 1`},
		{name: "extra label", scrape: `wukongim_channelv2_meta_created_total{slot_id="1",result="created",node="1"} 1`},
		{name: "slot zero", scrape: `wukongim_channelv2_meta_created_total{slot_id="0",result="created"} 1`},
		{name: "slot above twelve", scrape: `wukongim_channelv2_meta_created_total{slot_id="13",result="created"} 1`},
		{name: "non canonical slot", scrape: `wukongim_channelv2_meta_created_total{slot_id="01",result="created"} 1`},
		{name: "unknown result", scrape: `wukongim_channelv2_meta_created_total{slot_id="1",result="retried"} 1`},
		{name: "fractional counter", scrape: `wukongim_channelv2_meta_created_total{slot_id="1",result="created"} 1.5`},
		{name: "counter beyond exact integer", scrape: `wukongim_channelv2_meta_created_total{slot_id="1",result="created"} 9007199254740993`},
		{name: "duplicate slot result", scrape: complete + `wukongim_channelv2_meta_created_total{slot_id="1",result="created"} 1`},
		{name: "missing global result", scrape: strings.Join([]string{
			`wukongim_channelv2_meta_created_total{slot_id="1",result="created"} 1`,
			`wukongim_channelv2_meta_created_total{slot_id="1",result="already_existing"} 2`,
		}, "\n")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseObservationMetrics([]byte(test.scrape + "\n"))
			require.Error(t, err)
		})
	}
}

func TestObservationMetricsTreatsAbsentPerSlotResultsAsZero(t *testing.T) {
	scrape := strings.Join([]string{
		`wukongim_channelv2_meta_created_total{slot_id="1",result="created"} 0`,
		`wukongim_channelv2_meta_created_total{slot_id="1",result="already_existing"} 0`,
		`wukongim_channelv2_meta_created_total{slot_id="1",result="error"} 0`,
		`wukongim_channelv2_meta_created_total{slot_id="2",result="created"} 7`,
	}, "\n") + "\n"
	snapshot, err := parseObservationMetrics([]byte(scrape))
	require.NoError(t, err)
	require.Equal(t, MetaCreateSlotCounters{Created: 7}, snapshot.MetaCreatedBySlot[1])
	require.Equal(t, map[string]float64{"created": 7, "already_existing": 0, "error": 0}, snapshot.MetaCreatedTotal)
}

func TestObservationMetricsUsesNodeRSSWhenProcessCollectorIsUnavailable(t *testing.T) {
	common := []string{
		"go_goroutines 42",
		"go_memstats_heap_alloc_bytes 1024",
		`wukongim_runtime_pool_queue_depth{pool="append"} 0`,
		`wukongim_runtime_pool_queue_capacity{pool="append"} 10`,
		`wukongim_runtime_pool_inflight{pool="append"} 0`,
		`wukongim_channelv2_worker_queue_depth{worker="meta"} 0`,
		`wukongim_channelv2_worker_queue_capacity{worker="meta"} 40`,
		`wukongim_channelv2_activation_rejected_total{reason="max_channels"} 0`,
		`wukongim_channelv2_meta_created_total{slot_id="1",result="created"} 0`,
		`wukongim_channelv2_meta_created_total{slot_id="1",result="already_existing"} 0`,
		`wukongim_channelv2_meta_created_total{slot_id="1",result="error"} 0`,
	}
	for _, test := range []struct {
		name string
		rss  []string
		want float64
	}{
		{name: "node fallback", rss: []string{`wukongim_node_memory_rss_bytes{node_id="1",node_name="node-1"} 4096`}, want: 4096},
		{name: "process preferred", rss: []string{"process_resident_memory_bytes 2048", `wukongim_node_memory_rss_bytes{node_id="1",node_name="node-1"} 4096`}, want: 2048},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot, err := parseObservationMetrics([]byte(strings.Join(append(common, test.rss...), "\n") + "\n"))
			require.NoError(t, err)
			require.NoError(t, snapshot.ValidateRequired())
			require.Equal(t, test.want, snapshot.ProcessResidentMemoryBytes)
		})
	}
}

func TestObservationMetricsRetainsMaximumPerQueueSeriesUtilization(t *testing.T) {
	scrape := strings.Join([]string{
		`wukongim_runtime_pool_queue_depth{pool="busy"} 81`,
		`wukongim_runtime_pool_queue_capacity{pool="busy"} 100`,
		`wukongim_runtime_pool_queue_depth{pool="idle"} 0`,
		`wukongim_runtime_pool_queue_capacity{pool="idle"} 900`,
		`wukongim_channelv2_worker_queue_depth{pool="busy"} 91`,
		`wukongim_channelv2_worker_queue_capacity{pool="busy"} 100`,
		`wukongim_channelv2_worker_queue_depth{pool="idle"} 0`,
		`wukongim_channelv2_worker_queue_capacity{pool="idle"} 900`,
	}, "\n") + "\n"
	snapshot, err := parseObservationMetrics([]byte(scrape))
	require.NoError(t, err)
	require.Equal(t, float64(81), snapshot.RuntimeQueueMaxPercent)
	require.Equal(t, float64(91), snapshot.ChannelWorkerQueueMaxPercent)
	require.Equal(t, float64(81), snapshot.RuntimeQueueDepth)
	require.Equal(t, float64(1_000), snapshot.RuntimeQueueCapacity)
	require.Equal(t, float64(91), snapshot.ChannelWorkerQueueDepth)
	require.Equal(t, float64(1_000), snapshot.ChannelWorkerQueueCapacity)
}

func TestObservationMetricsIgnoresInactiveZeroCapacityQueueSeries(t *testing.T) {
	scrape := strings.Join([]string{
		`wukongim_runtime_pool_queue_depth{pool="bounded"} 8`,
		`wukongim_runtime_pool_queue_capacity{pool="bounded"} 10`,
		`wukongim_runtime_pool_queue_depth{pool="inactive"} 0`,
		`wukongim_runtime_pool_queue_capacity{pool="inactive"} 0`,
	}, "\n") + "\n"
	snapshot, err := parseObservationMetrics([]byte(scrape))
	require.NoError(t, err)
	require.Equal(t, float64(80), snapshot.RuntimeQueueMaxPercent)
	require.Equal(t, float64(8), snapshot.RuntimeQueueDepth)
	require.Equal(t, float64(10), snapshot.RuntimeQueueCapacity)
}

func TestObservationMetricsIgnoresByteBoundedQueueWithoutItemCapacity(t *testing.T) {
	scrape := strings.Join([]string{
		`wukongim_runtime_pool_queue_depth{pool="bounded"} 8`,
		`wukongim_runtime_pool_queue_capacity{pool="bounded"} 10`,
		`wukongim_runtime_pool_queue_depth{pool="byte-bounded"} 3`,
		`wukongim_runtime_pool_queue_capacity{pool="byte-bounded"} 0`,
	}, "\n") + "\n"
	snapshot, err := parseObservationMetrics([]byte(scrape))
	require.NoError(t, err)
	require.Equal(t, float64(80), snapshot.RuntimeQueueMaxPercent)
	require.Equal(t, float64(11), snapshot.RuntimeQueueDepth)
	require.Equal(t, float64(10), snapshot.RuntimeQueueCapacity)
}

func TestObserverRejectsOversizedAndRedactsProtectedResponses(t *testing.T) {
	const token = "observer-redaction-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/debug/config":
			http.Error(w, "rejected "+token, http.StatusUnauthorized)
		case "/debug/goroutines/summary":
			http.Error(w, "rejected "+token, http.StatusUnauthorized)
		case "/debug/cluster":
			_, _ = io.WriteString(w, `{"node_id":1,"slots":[`+strings.Repeat(`{"slot_id":1},`, 300)+`{}]}`)
		case "/metrics":
			_, _ = io.WriteString(w, strings.Repeat("# padding\n", 900000))
		case "/debug/pprof/heap":
			_, _ = io.WriteString(w, strings.Repeat("x", int(maxObservationProfileResponseBytes)+1))
		}
	}))
	defer server.Close()
	client := NewClient(Config{APIAddrs: []string{server.URL}, Token: token})

	_, err := client.DebugConfig(context.Background())
	require.Error(t, err)
	require.ErrorContains(t, err, "401")
	require.NotContains(t, err.Error(), token)
	_, err = client.DebugGoroutineSummary(context.Background())
	require.Error(t, err)
	require.ErrorContains(t, err, "401")
	require.NotContains(t, err.Error(), token)
	_, err = client.DebugCluster(context.Background())
	require.ErrorContains(t, err, "cardinality")
	_, err = client.Metrics(context.Background())
	require.ErrorContains(t, err, "limit")
	err = client.ForceGC(context.Background())
	require.ErrorContains(t, err, "limit")
	require.NotContains(t, err.Error(), token)
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(v))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
