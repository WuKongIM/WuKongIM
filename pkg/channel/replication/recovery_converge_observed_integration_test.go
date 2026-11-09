//go:build integration

package replication

import (
	"context"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// The identity round must await a slow voter already included in the frontier round.
func TestRecoveryConvergenceWaitsForEveryObservedTail(t *testing.T) {
	h, log, a := convergenceFixture(t)
	h.unavailable = 0
	first, _ := recoveryMutationAfter(t, a.Key, a.ChannelID, 41, 0, ch.EntryIdentity{})
	writeRecoveryFixture(t, h, 1, first)
	writeRecoveryFixture(t, h, 3, first)
	log.cfg.Recovery = &slowObservedRecovery{unavailableRecoveryHarness: h}
	require.NoError(t, log.convergeRecoverySuffix(context.Background(), a, recoverySelection{}, AuthorityID{}))
	got, err := loadRecoveryReplicaState(context.Background(), h.stores[2], a.Key, a.ChannelID, nil)
	require.NoError(t, err)
	require.Equal(t, uint64(1), got.State.LEO)
}

type slowObservedRecovery struct{ *unavailableRecoveryHarness }

func (h *slowObservedRecovery) submitRecoveryProbe(ctx context.Context, q recoveryProbeQuery, done func(ProbeResult, error)) error {
	if q.Voter != 3 || len(q.Indexes) == 0 {
		return h.unavailableRecoveryHarness.submitRecoveryProbe(ctx, q, done)
	}
	return h.unavailableRecoveryHarness.submitRecoveryProbe(ctx, q, func(r ProbeResult, err error) {
		go func() {
			timer := time.NewTimer(25 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
				done(r, err)
			case <-ctx.Done():
				done(ProbeResult{}, ctx.Err())
			}
		}()
	})
}
