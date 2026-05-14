package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/benchdata"
	"github.com/stretchr/testify/require"
)

func TestBenchRoutesDisabledReturnNotFound(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/bench/v1/capabilities", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
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
}

func TestBenchSubscribersRejectsResetTrue(t *testing.T) {
	srv := New(Options{BenchEnabled: true, BenchData: benchStub{}, BenchMaxBatchSize: 100, BenchMaxPayloadBytes: 1024})
	body := `{"run_id":"bench-run","batch_id":"b1","items":[{"channel_id":"g1","channel_type":2,"reset":true,"subscribers":["u1"]}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bench/v1/channels/subscribers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
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

type benchStub struct{}

func (b benchStub) Capabilities(context.Context) benchdata.CapabilitiesResponse {
	return benchdata.CapabilitiesResponse{
		Enabled:  true,
		Version:  "bench/v1",
		Supports: benchdata.CapabilitiesSupports{ChannelTypes: []string{"group"}},
		Limits:   benchdata.CapabilitiesLimits{MaxBatchSize: 100, MaxPayloadBytes: 1024},
	}
}

func (b benchStub) UpsertTokens(_ context.Context, req benchdata.TokensRequest) (benchdata.MutationResponse, error) {
	return benchdata.MutationResponse{RunID: req.RunID, BatchID: req.BatchID, Accepted: len(req.Items)}, nil
}

func (b benchStub) UpsertChannels(_ context.Context, req benchdata.ChannelsRequest) (benchdata.MutationResponse, error) {
	return benchdata.MutationResponse{RunID: req.RunID, BatchID: req.BatchID, Accepted: len(req.Items)}, nil
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
