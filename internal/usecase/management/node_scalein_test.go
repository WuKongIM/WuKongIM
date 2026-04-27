package management

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlanNodeScaleInBlocksWhenSlotReplicaNMissing(t *testing.T) {
	app := New(Options{Cluster: fakeClusterReader{}})

	report, err := app.PlanNodeScaleIn(context.Background(), 1, NodeScaleInPlanRequest{})

	require.NoError(t, err)
	require.False(t, report.Checks.SlotReplicaCountKnown)
	require.Contains(t, scaleInReasonCodes(report.BlockedReasons), "slot_replica_count_unknown")
}

func scaleInReasonCodes(reasons []NodeScaleInBlockedReason) []string {
	codes := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		codes = append(codes, reason.Code)
	}
	return codes
}
