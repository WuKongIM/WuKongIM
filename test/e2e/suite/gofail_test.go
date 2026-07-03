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

type gofailRequest struct {
	Method string
	Path   string
	Body   string
}

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
	requests := make(chan gofailRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- gofailRequest{Method: r.Method, Path: r.URL.Path}
		_, _ = w.Write([]byte("first=return\nsecond=return\n"))
	}))
	defer server.Close()

	endpoint := GofailEndpoint{Addr: strings.TrimPrefix(server.URL, "http://")}
	body, err := endpoint.WaitListed(context.Background(), "first", "second")
	require.NoError(t, err)
	require.Equal(t, "first=return\nsecond=return\n", body)
	require.Equal(t, gofailRequest{Method: http.MethodGet, Path: "/"}, <-requests)
}

func TestGofailEndpointWaitListedReportsTimeout(t *testing.T) {
	requests := make(chan gofailRequest, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requests <- gofailRequest{Method: r.Method, Path: r.URL.Path}:
		default:
		}
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
	require.Equal(t, gofailRequest{Method: http.MethodGet, Path: "/"}, <-requests)
}

func TestGofailEndpointWaitListedDoesNotMatchOverlappingNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("foobar=return\n"))
	}))
	defer server.Close()

	endpoint := GofailEndpoint{Addr: strings.TrimPrefix(server.URL, "http://")}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := endpoint.WaitListed(ctx, "bar")
	require.Error(t, err)
	require.Contains(t, err.Error(), "wait gofail list")
	require.Contains(t, err.Error(), "bar")
	require.Contains(t, err.Error(), "foobar=return")
}

func TestGofailEndpointEnableDisableUseHTTPMethods(t *testing.T) {
	var mu sync.Mutex
	var requests []gofailRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, gofailRequest{Method: r.Method, Path: r.URL.Path, Body: string(body)})
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	endpoint := GofailEndpoint{Addr: strings.TrimPrefix(server.URL, "http://")}
	require.NoError(t, endpoint.Enable(context.Background(), "failpoint", `return("boom")`))
	require.NoError(t, endpoint.Disable(context.Background(), "failpoint"))

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []gofailRequest{
		{Method: http.MethodPut, Path: "/failpoint", Body: `return("boom")`},
		{Method: http.MethodDelete, Path: "/failpoint"},
	}, requests)
}

func TestGofailEndpointCountReadsExecutionCount(t *testing.T) {
	requests := make(chan gofailRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- gofailRequest{Method: r.Method, Path: r.URL.Path}
		_, _ = w.Write([]byte("7"))
	}))
	defer server.Close()

	endpoint := GofailEndpoint{Addr: strings.TrimPrefix(server.URL, "http://")}
	count, err := endpoint.Count(context.Background(), "failpoint")
	require.NoError(t, err)
	require.Equal(t, 7, count)
	require.Equal(t, gofailRequest{Method: http.MethodGet, Path: "/failpoint/count"}, <-requests)
}
