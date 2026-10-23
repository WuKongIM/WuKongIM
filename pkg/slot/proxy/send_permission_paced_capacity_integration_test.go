//go:build integration

package proxy

import (
	"context"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

// Failure cases: small cohorts consume every execution position during paced
// arrivals behind slow fresh barriers, although call/byte ownership still fits;
// batching must retain aligned facts and release ownership after completion.
// This controlled-port test is not a three-node throughput qualification.
func TestSendPermissionPacedCallsShareSlowFreshBarriers(t *testing.T) {
	db := boundaryDB(t)
	wb := db.NewWriteBatch()
	_, err := wb.ApplySendBan(1, metadb.SendBanMutation{UID: "u", SendBan: 1})
	require.NoError(t, err)
	require.NoError(t, wb.Commit())
	require.NoError(t, wb.Close())
	owner := newBoundaryCluster(2)
	gate := boundaryGate(t)
	owner.barrier = func(ctx context.Context, _ multiraft.SlotID) error {
		owner.barriers.Add(1)
		select {
		case <-gate:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	caller := New(owner, db)
	t.Cleanup(caller.CloseSendPermissionReads)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	const rounds, callsPerRound = 100, 4
	results := make(chan []PermissionMetadataReadResult, rounds*callsPerRound)
	// Three aligned entries also exercise deduplication without the idle
	// one/two-fact fast path. No private collection-window override is used.
	reads := []PermissionMetadataRead{
		{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"},
		{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"},
		{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"},
	}
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	for round := range rounds {
		if round > 0 {
			select {
			case <-ticker.C:
			case <-ctx.Done():
				t.Fatal("paced arrivals exceeded their bounded deadline")
			}
		}
		for range callsPerRound {
			go func() { results <- caller.ReadSendPermissionMetadataBatch(ctx, reads) }()
		}
	}
	closeBoundaryGate(gate)
	failed := 0
	for range rounds * callsPerRound {
		select {
		case out := <-results:
			require.Len(t, out, len(reads))
			for _, result := range out {
				if result.Err != nil {
					failed++
					break
				}
				require.True(t, result.Found)
				require.EqualValues(t, 1, result.UserPolicy.SendBan)
			}
		case <-ctx.Done():
			t.Fatal("paced permission calls did not join")
		}
	}
	t.Logf("calls=%d failed=%d fresh_barriers=%d", rounds*callsPerRound, failed, owner.barriers.Load())
	require.Zero(t, failed, "paced calls fitting existing ownership budgets must share fresh work before cohort admission saturates")
	requireCohortDrained(t, caller)
}
