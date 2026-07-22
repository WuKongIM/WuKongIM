//go:build e2e

package suite

import (
	"fmt"
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

func TestWaitBackupActiveOrPublishedAcceptsCompletedRestorePoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/manager/backups/status":
			_, _ = fmt.Fprint(w, `{"health":"healthy","active":null}`)
		case "/manager/backups/restore-points":
			_, _ = fmt.Fprint(w, `{"items":[{"id":"restore-1","kind":"incremental","primary_verified":true,"secondary_verified":true}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	manager := &ManagerClient{baseURL: server.URL, cluster: &StartedCluster{}}
	active, published := manager.WaitBackupActiveOrPublished(t, "job-1", "restore-1", time.Second)

	require.Nil(t, active)
	require.NotNil(t, published)
	require.Equal(t, "restore-1", published.ID)
}
