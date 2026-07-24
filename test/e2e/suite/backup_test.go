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

func TestWaitBackupVerificationSucceededPollsDurableTask(t *testing.T) {
	statusCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/manager/backups/status" {
			http.NotFound(w, r)
			return
		}
		statusCalls++
		if statusCalls == 1 {
			_, _ = fmt.Fprint(w, `{"verification":{"id":"verify-1","restore_point_id":"restore-1","status":"running","primary_verified":false,"secondary_verified":false}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"verification":{"id":"verify-1","restore_point_id":"restore-1","status":"succeeded","primary_verified":true,"secondary_verified":true,"manifest_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`)
	}))
	defer server.Close()

	manager := &ManagerClient{baseURL: server.URL, cluster: &StartedCluster{}}
	verification := manager.WaitBackupVerificationSucceeded(t, "verify-1", time.Second)

	require.GreaterOrEqual(t, statusCalls, 2)
	require.Equal(t, "restore-1", verification.RestorePointID)
	require.True(t, verification.PrimaryVerified)
	require.True(t, verification.SecondaryVerified)
	require.Len(t, verification.ManifestSHA256, 64)
}
