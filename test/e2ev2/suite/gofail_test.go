//go:build e2e

package suite

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGofailEndpointEnvUsesLoopbackHTTP(t *testing.T) {
	endpoint := ReserveGofailEndpoint(t)
	require.True(t, strings.HasPrefix(endpoint.Env(), "GOFAIL_HTTP=127.0.0.1:"))
	require.Equal(t, "http://"+endpoint.Addr, endpoint.BaseURL())
}

func TestGofailEndpointRejectsEmptyFailpointName(t *testing.T) {
	endpoint := GofailEndpoint{Addr: "127.0.0.1:1"}
	err := endpoint.Enable(context.Background(), "", `return("boom")`)
	require.Error(t, err)
}

func TestReserveGofailEndpointReturnsFreePort(t *testing.T) {
	endpoint := ReserveGofailEndpoint(t)
	ln, err := net.Listen("tcp", endpoint.Addr)
	require.NoError(t, err)
	require.NoError(t, ln.Close())
}

func TestGofailEndpointWaitListedFindsAllNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/", r.URL.Path)
		_, _ = w.Write([]byte("first=return\nsecond=return\n"))
	}))
	defer server.Close()

	endpoint := GofailEndpoint{Addr: strings.TrimPrefix(server.URL, "http://")}
	body, err := endpoint.WaitListed(context.Background(), "first", "second")
	require.NoError(t, err)
	require.Equal(t, "first=return\nsecond=return\n", body)
}

func TestGofailEndpointWaitListedReportsTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/", r.URL.Path)
		_, _ = w.Write([]byte("other=return\n"))
	}))
	defer server.Close()

	endpoint := GofailEndpoint{Addr: strings.TrimPrefix(server.URL, "http://")}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := endpoint.WaitListed(ctx, "missing")
	require.Error(t, err)
	require.Contains(t, err.Error(), "wait gofail list")
	require.Contains(t, err.Error(), "missing")
	require.Contains(t, err.Error(), "other=return")
}

func TestGofailEndpointEnableDisableUseHTTPMethods(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	var putBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPut {
			body, _ := io.ReadAll(r.Body)
			putBody = string(body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	endpoint := GofailEndpoint{Addr: strings.TrimPrefix(server.URL, "http://")}
	require.NoError(t, endpoint.Enable(context.Background(), "failpoint", `return("boom")`))
	require.NoError(t, endpoint.Disable(context.Background(), "failpoint"))

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"PUT /failpoint", "DELETE /failpoint"}, calls)
	require.Equal(t, `return("boom")`, putBody)
}
