//go:build e2e

package suite

import (
	"context"
	"testing"
	"time"
)

const (
	// BackupClusterReadyTimeout allows local clusters to start with other E2E
	// packages during continuous-backup qualification.
	BackupClusterReadyTimeout = 90 * time.Second
)

// WaitNodesReady waits for every listed node to report public readiness.
func (c *StartedCluster) WaitNodesReady(
	t testing.TB,
	nodeIDs []uint64,
	timeout time.Duration,
) {
	t.Helper()
	if c.lastReadyz == nil {
		c.lastReadyz = make(map[uint64]HTTPObservation, len(nodeIDs))
	}
	for _, nodeID := range nodeIDs {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		observation, err := waitHTTPReadyDetailed(
			ctx, c.MustNode(nodeID).APIAddr(), "/readyz",
		)
		cancel()
		c.lastReadyz[nodeID] = observation
		if err != nil {
			t.Fatalf(
				"node %d did not recover readiness: %v\n%s",
				nodeID, err, c.DumpDiagnostics(),
			)
		}
	}
}
