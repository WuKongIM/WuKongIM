package mqttsession

import (
	"context"
	"errors"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

// Confirmation checks fresh progress after its bounded copy and anchor commit.
// A rejected replica retains intent without granting proof or repeating copy.
func TestReplayRecoveryYieldRetainsIntentAcrossRoutes(t *testing.T) {
	for _, route := range []string{"post-anchor-confirm", "repair-next", "idle-tail", "write-fenced"} {
		for _, tc := range []struct {
			name            string
			failure         error
			pending, cancel bool
		}{
			{name: "not-ready", failure: ch.ErrNotReady, pending: true},
			{name: "pressure", failure: ch.ErrBackpressured, pending: true},
			{name: "conflict", failure: ch.ErrLogConflict},
			{name: "stale", failure: ch.ErrStaleMeta},
			{name: "fenced", failure: ch.ErrWriteFenced},
			{name: "invalid", failure: ch.ErrInvalidConfig},
			{name: "unknown", failure: errors.New("recovery result unavailable")},
			{name: "deadline", failure: context.DeadlineExceeded},
			{name: "canceled-readiness", failure: ch.ErrNotReady, cancel: true},
			{name: "canceled-pressure", failure: ch.ErrBackpressured, cancel: true},
		} {
			t.Run(route+"/"+tc.name, func(t *testing.T) {
				c, f, source := newReplayFixture(t)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				cursor := ReplayCursor{}
				if route == "post-anchor-confirm" {
					f.planRead = func(context.Context, ch.MQTTReplayPlanRequest) (ch.MQTTReplayPlan, error) {
						if f.plans == 2 {
							f.plan.HasAnchor, f.plan.Anchor, f.plan.Source.CommittedThrough = true, f.proof, 3
						}
						return f.plan, nil
					}
				} else {
					f.plan.HasAnchor, f.plan.Anchor, f.plan.Source.CommittedThrough = true, f.proof, 3
					switch route {
					case "repair-next":
						f.plan.Source.CommittedThrough = 4
						cursor.Pass = 1
					case "write-fenced":
						f.m.WriteFence.Version, f.m.WriteFence.Token = 1, "migration"
					}
				}
				f.repair = func(context.Context, ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
					if tc.cancel {
						cancel()
					}
					return ch.MQTTReplayRecoveryResult{}, tc.failure
				}
				var err error
				if route == "post-anchor-confirm" {
					err = c.Confirm(ctx, source, 2)
					require.Equal(t, 2, f.plans)
				} else {
					var out ReplayStepResult
					out, err = c.Step(ctx, source, cursor)
					require.False(t, out.Anchored || out.Repaired || out.TargetComplete)
					require.Equal(t, ch.NodeID(1), out.Target)
					require.Equal(t, 1, out.Next.NextTarget, "a rejected receiver cannot monopolize the next turn")
					require.Equal(t, f.proof.Manifest.LastOffset, out.Next.Targets[0].AnchorPosition)
				}
				if tc.cancel {
					require.ErrorIs(t, err, context.Canceled)
				} else {
					require.ErrorIs(t, err, tc.failure)
				}
				if tc.pending {
					require.ErrorIs(t, err, ErrReplayPending)
				} else {
					require.NotErrorIs(t, err, ErrReplayPending)
				}
				require.Equal(t, 1, f.repairs, "each turn admits at most one recovery call")
				copies := 0
				if route == "post-anchor-confirm" {
					copies = 1
				}
				require.Equal(t, copies, f.copies)
				require.Equal(t, copies, f.commits)
				if route != "post-anchor-confirm" || !tc.pending {
					return
				}
				f.planRead = nil
				f.repair = func(_ context.Context, q ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
					require.False(t, q.ReleaseSource)
					return ch.MQTTReplayRecoveryResult{Plan: ch.MQTTReplayRepairPlan{Current: f.proof.Prefix(), Target: f.proof, Complete: true}}, nil
				}
				require.NoError(t, c.Confirm(ctx, source, 2))
				require.Equal(t, 4, f.repairs, "a later confirmation must still prove every replica")
				require.Equal(t, 1, f.copies, "the next attempt must reuse the committed anchor")
				require.Equal(t, 1, f.commits)
			})
		}
	}
}
