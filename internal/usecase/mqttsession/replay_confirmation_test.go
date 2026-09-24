package mqttsession

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestReplayConfirmationRequiresEveryReplicaAndFreshPlacement(t *testing.T) {
	for _, mode := range []string{"complete", "learner_incomplete", "retirement_pending", "different_anchor", "placement_changed", "fenced", "new_fence", "unavailable", "canceled", "maintenance_tail", "boundary_ahead"} {
		t.Run(mode, func(t *testing.T) {
			c, f, source := newReplayFixture(t)
			f.plan.HasAnchor, f.plan.Anchor, f.plan.Source.CommittedThrough = true, f.proof, 3
			var targets []ch.NodeID
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.repair = func(_ context.Context, q ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
				targets = append(targets, q.Target)
				require.False(t, q.ReleaseSource, "admission is not a source-release operation")
				require.True(t, q.ApplyRetirement)
				require.EqualValues(t, 3, q.TargetAnchor)
				r := ch.MQTTReplayRecoveryResult{Plan: ch.MQTTReplayRepairPlan{Current: f.proof.Prefix(), Target: f.proof, Complete: true}}
				if q.Target == 3 {
					switch mode {
					case "learner_incomplete":
						r.Plan = ch.MQTTReplayRepairPlan{Current: ch.MQTTReplayPrefix{Generation: source.Generation}, Target: f.proof, Next: f.proof, HasNext: true}
						r.Repaired = true
					case "retirement_pending":
						r.RetirementPending = true
					case "different_anchor":
						r.Plan.Target.Manifest.Digest[0]++
					case "placement_changed":
						f.m.RouteGeneration++
					case "new_fence":
						f.m.WriteFence.Version = 1
						f.m.WriteFence.Token = "new-fence"
					case "unavailable":
						return ch.MQTTReplayRecoveryResult{}, context.DeadlineExceeded
					case "canceled":
						cancel()
					}
				}
				return r, nil
			}
			start := uint64(2)
			switch mode {
			case "fenced":
				f.m.WriteFence.Version = 1
				f.m.WriteFence.Token = "migration"
			case "maintenance_tail":
				start = 4
				f.plan.Source.CommittedThrough = 4
				f.plan.MaintenanceOnly = true
			case "boundary_ahead":
				start = 5
			}
			err := c.Confirm(ctx, source, start)
			if mode == "complete" || mode == "maintenance_tail" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			if mode == "fenced" || mode == "boundary_ahead" {
				require.Empty(t, targets)
			} else {
				require.Equal(t, []ch.NodeID{1, 2, 3}, targets)
			}
			require.Zero(t, f.copies)
			require.Zero(t, f.commits)
		})
	}
}

func TestReplayConfirmationAdvancesOneCopyWithoutClaimingCompletion(t *testing.T) {
	c, f, source := newReplayFixture(t)
	err := c.Confirm(context.Background(), source, 2)
	require.ErrorIs(t, err, ErrReplayPending)
	require.Equal(t, 1, f.copies)
	require.Equal(t, 1, f.commits)
	require.Zero(t, f.repairs)
	f.repair = func(context.Context, ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
		return ch.MQTTReplayRecoveryResult{Plan: ch.MQTTReplayRepairPlan{Current: f.proof.Prefix(), Target: f.proof, Complete: true}}, nil
	}
	require.NoError(t, c.Confirm(context.Background(), source, 3), "a lone committed anchor is not an uncopied business obligation")
	require.Equal(t, 1, f.copies)
	require.Equal(t, 3, f.repairs)
}
