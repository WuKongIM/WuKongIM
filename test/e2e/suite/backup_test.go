//go:build e2e

package suite

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWaitNodesReadyRecordsLatestObservation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ready":true}`))
	}))
	defer server.Close()

	cluster := &StartedCluster{
		Nodes:      []StartedNode{{Spec: NodeSpec{ID: 1, APIAddr: server.Listener.Addr().String()}}},
		lastReadyz: make(map[uint64]HTTPObservation),
	}
	cluster.WaitNodesReady(t, []uint64{1}, time.Second)

	require.Equal(t, HTTPObservation{StatusCode: http.StatusOK, Body: `{"ready":true}`}, cluster.lastReadyz[1])
}
