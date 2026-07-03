package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/legacy/usecase/benchdata"
	"github.com/stretchr/testify/require"
)

func TestBenchRoutesDisabledReturnNotFound(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/bench/v1/capabilities", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestBenchCapacityTargetDisabledReturnNotFound(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/bench/v1/capacity-target", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestBenchCapacityTargetReturnsExternalGatewayAddresses(t *testing.T) {
	srv := New(Options{
		BenchEnabled: true,
		BenchData:    benchStub{},
		LegacyRouteExternal: LegacyRouteAddresses{
			TCPAddr: "127.0.0.1:15100",
			WSAddr:  "ws://127.0.0.1:15200",
			WSSAddr: "wss://127.0.0.1:15300",
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/bench/v1/capacity-target", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"version":"bench/v1",
		"gateway":{
			"tcp_addr":"127.0.0.1:15100",
			"ws_addr":"ws://127.0.0.1:15200",
			"wss_addr":"wss://127.0.0.1:15300"
		}
	}`, rec.Body.String())
}

func TestBenchCapabilitiesRequiresUsecaseWhenEnabled(t *testing.T) {
	srv := New(Options{BenchEnabled: true, BenchMaxBatchSize: 10, BenchMaxPayloadBytes: 1024})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/bench/v1/capabilities", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestBenchCapabilitiesSuccess(t *testing.T) {
	srv := New(Options{BenchEnabled: true, BenchData: benchStub{}, BenchMaxBatchSize: 100, BenchMaxPayloadBytes: 1024})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/bench/v1/capabilities", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got benchdata.CapabilitiesResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.True(t, got.Enabled)
	require.Equal(t, "bench/v1", got.Version)
	require.Contains(t, got.Supports.ChannelTypes, "group")
	require.True(t, got.Supports.UsersTokensBatch)
	require.True(t, got.Supports.ChannelsBatch)
	require.True(t, got.Supports.ChannelSubscribersBatch)
	require.True(t, got.Supports.Snapshot)
	require.JSONEq(t, `{"enabled":true,"version":"bench/v1","supports":{"users_tokens_batch":true,"channels_batch":true,"channel_subscribers_batch":true,"snapshot":true,"channel_types":["group"]},"limits":{"max_batch_size":100,"max_payload_bytes":1024}}`, rec.Body.String())
}

func TestBenchSubscribersRejectsResetTrue(t *testing.T) {
	srv := New(Options{
		BenchEnabled:         true,
		BenchData:            benchdata.New(benchdata.Config{Channels: benchAPIChannels{}, MaxBatchSize: 100, MaxPayloadBytes: 1024}),
		BenchMaxBatchSize:    100,
		BenchMaxPayloadBytes: 1024,
	})
	body := `{"run_id":"bench-run","batch_id":"b1","items":[{"channel_id":"g1","channel_type":2,"reset":true,"subscribers":["u1"]}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bench/v1/channels/subscribers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBenchMutationOversizedBodyReturnsPayloadTooLarge(t *testing.T) {
	srv := New(Options{BenchEnabled: true, BenchData: benchStub{}, BenchMaxBatchSize: 100, BenchMaxPayloadBytes: 16})
	body := `{"run_id":"bench-run","batch_id":"b1","items":[{"uid":"u1","token":"token-that-is-too-large"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bench/v1/users/tokens", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	require.Contains(t, rec.Body.String(), "payload too large")
}

func TestBenchMissingDependencyReturnsServiceUnavailable(t *testing.T) {
	srv := New(Options{
		BenchEnabled:         true,
		BenchData:            benchdata.New(benchdata.Config{MaxBatchSize: 100, MaxPayloadBytes: 1024}),
		BenchMaxBatchSize:    100,
		BenchMaxPayloadBytes: 1024,
	})
	body := `{"run_id":"bench-run","batch_id":"b1","items":[{"uid":"u1","token":"t1"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bench/v1/users/tokens", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestBenchBackendErrorReturnsInternalServerError(t *testing.T) {
	srv := New(Options{BenchEnabled: true, BenchData: benchFailingStub{}, BenchMaxBatchSize: 100, BenchMaxPayloadBytes: 1024})
	body := `{"run_id":"bench-run","batch_id":"b1","items":[{"uid":"u1","token":"t1"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bench/v1/users/tokens", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestBenchTokensMutationSuccess(t *testing.T) {
	srv := New(Options{BenchEnabled: true, BenchData: benchStub{}, BenchMaxBatchSize: 100, BenchMaxPayloadBytes: 1024})
	body := `{"run_id":"bench-run","batch_id":"b1","items":[{"uid":"u1","token":"t1"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bench/v1/users/tokens", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got benchdata.MutationResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, 1, got.Accepted)
}

func TestBenchTokensSpecFormMutationSuccess(t *testing.T) {
	users := &benchAPIUsers{}
	srv := New(Options{
		BenchEnabled:         true,
		BenchData:            benchdata.New(benchdata.Config{Users: users, MaxBatchSize: 100, MaxPayloadBytes: 1024}),
		BenchMaxBatchSize:    100,
		BenchMaxPayloadBytes: 1024,
	})
	body := `{"run_id":"bench-run","batch_id":"b1","upsert":true,"users":[{"uid":"u1","token":"t1"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bench/v1/users/tokens", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got benchdata.MutationResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, 1, got.Accepted)
	require.Equal(t, []benchdata.UserTokenCommand{{UID: "u1", Token: "t1"}}, users.updated)
}

func TestBenchChannelsSpecFormMutationSuccess(t *testing.T) {
	channels := &benchAPIChannelsRecorder{}
	srv := New(Options{
		BenchEnabled:         true,
		BenchData:            benchdata.New(benchdata.Config{Channels: channels, MaxBatchSize: 100, MaxPayloadBytes: 1024}),
		BenchMaxBatchSize:    100,
		BenchMaxPayloadBytes: 1024,
	})
	body := `{"run_id":"bench-run","batch_id":"b1","upsert":true,"channels":[{"channel_id":"g1","channel_type":2}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bench/v1/channels", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got benchdata.MutationResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, 1, got.Accepted)
	require.Equal(t, []benchdata.ChannelRecord{{ChannelID: "g1", ChannelType: 2}}, channels.upserted)
}

type benchStub struct{}

func (b benchStub) Capabilities(context.Context) benchdata.CapabilitiesResponse {
	return benchdata.CapabilitiesResponse{
		Enabled: true,
		Version: "bench/v1",
		Supports: benchdata.CapabilitiesSupports{
			UsersTokensBatch:        true,
			ChannelsBatch:           true,
			ChannelSubscribersBatch: true,
			Snapshot:                true,
			ChannelTypes:            []string{"group"},
		},
		Limits: benchdata.CapabilitiesLimits{MaxBatchSize: 100, MaxPayloadBytes: 1024},
	}
}

func (b benchStub) UpsertTokens(_ context.Context, req benchdata.TokensRequest) (benchdata.MutationResponse, error) {
	accepted := len(req.Items)
	if len(req.Users) > 0 {
		accepted = len(req.Users)
	}
	return benchdata.MutationResponse{RunID: req.RunID, BatchID: req.BatchID, Accepted: accepted}, nil
}

func (b benchStub) UpsertChannels(_ context.Context, req benchdata.ChannelsRequest) (benchdata.MutationResponse, error) {
	accepted := len(req.Items)
	if len(req.Channels) > 0 {
		accepted = len(req.Channels)
	}
	return benchdata.MutationResponse{RunID: req.RunID, BatchID: req.BatchID, Accepted: accepted}, nil
}

func (b benchStub) AddSubscribers(_ context.Context, req benchdata.SubscribersRequest) (benchdata.SubscribersResponse, error) {
	for _, item := range req.Items {
		if item.Reset {
			return benchdata.SubscribersResponse{RunID: req.RunID, BatchID: req.BatchID}, errors.New("reset=true unsupported")
		}
	}
	return benchdata.SubscribersResponse{RunID: req.RunID, BatchID: req.BatchID, Accepted: len(req.Items)}, nil
}

func (b benchStub) Snapshot(context.Context) (benchdata.SnapshotResponse, error) {
	return benchdata.SnapshotResponse{Version: "bench/v1"}, nil
}

type benchAPIChannels struct{}

func (benchAPIChannels) UpsertChannel(context.Context, benchdata.ChannelRecord) error { return nil }

func (benchAPIChannels) AddSubscribers(context.Context, string, uint8, []string) error { return nil }

type benchAPIUsers struct {
	updated []benchdata.UserTokenCommand
}

func (u *benchAPIUsers) UpdateToken(_ context.Context, cmd benchdata.UserTokenCommand) error {
	u.updated = append(u.updated, cmd)
	return nil
}

type benchAPIChannelsRecorder struct {
	upserted []benchdata.ChannelRecord
}

func (c *benchAPIChannelsRecorder) UpsertChannel(_ context.Context, ch benchdata.ChannelRecord) error {
	c.upserted = append(c.upserted, ch)
	return nil
}

func (c *benchAPIChannelsRecorder) AddSubscribers(context.Context, string, uint8, []string) error {
	return nil
}

type benchFailingStub struct{ benchStub }

func (b benchFailingStub) UpsertTokens(context.Context, benchdata.TokensRequest) (benchdata.MutationResponse, error) {
	return benchdata.MutationResponse{}, errors.New("backend failed")
}
