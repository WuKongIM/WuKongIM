package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
