package mqttsession

import (
	"context"
	"errors"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

// Initial copy can yield before the control is admitted. Only that typed yield
// permits the subscription request to reread its unchanged Preparing intent.
func TestReplayConfirmationClassifiesCopyYieldBeforeAnchor(t *testing.T) {
	unknown := errors.New("copy outcome unavailable")
	for _, tc := range []struct {
		name                    string
		failure                 error
		anchor, cancel, pending bool
	}{
		{name: "copy-not-ready", failure: ch.ErrNotReady, pending: true},
		{name: "copy-pressure", failure: ch.ErrBackpressured, pending: true},
		{name: "copy-unknown", failure: unknown},
		{name: "copy-conflict", failure: ch.ErrLogConflict},
		{name: "copy-stale-authority", failure: ch.ErrStaleMeta},
		{name: "copy-deadline", failure: context.DeadlineExceeded},
		{name: "copy-canceled-yield", failure: ch.ErrNotReady, cancel: true},
		{name: "anchor-not-ready", failure: ch.ErrNotReady, anchor: true},
		{name: "anchor-pressure", failure: ch.ErrBackpressured, anchor: true},
		{name: "anchor-unknown", failure: unknown, anchor: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, f, source := newReplayFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.anchor {
				f.commitErr = tc.failure
			} else {
				f.copyErr = tc.failure
			}
			if tc.cancel {
				f.afterCopy = cancel
			}
			err := c.Confirm(ctx, source, 2)
			if tc.pending {
				require.ErrorIs(t, err, ErrReplayPending)
			} else {
				require.NotErrorIs(t, err, ErrReplayPending)
			}
			if tc.cancel {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, tc.failure)
			}
			require.Equal(t, 1, f.copies)
			if tc.anchor {
				require.Equal(t, 1, f.commits)
			} else {
				require.Zero(t, f.commits)
			}
			require.False(t, f.plan.HasAnchor)
			require.Zero(t, f.repairs)
		})
	}
}

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
	f.repair = incompleteConfirmation(f)
	err := c.Confirm(context.Background(), source, 2)
	require.ErrorIs(t, err, ErrReplayPending)
	require.Equal(t, 1, f.copies)
	require.Equal(t, 1, f.commits)
	require.Equal(t, 3, f.repairs, "a committed anchor still needs independent coverage on every replica")
	f.repair = func(context.Context, ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
		return ch.MQTTReplayRecoveryResult{Plan: ch.MQTTReplayRepairPlan{Current: f.proof.Prefix(), Target: f.proof, Complete: true}}, nil
	}
	require.NoError(t, c.Confirm(context.Background(), source, 3), "a lone committed anchor is not an uncopied business obligation")
	require.Equal(t, 1, f.copies)
	require.Equal(t, 6, f.repairs)
}

func TestReplayConfirmationAwaitsReplicaCheckpointWithoutAcceptingFailure(t *testing.T) {
	for _, tc := range []struct {
		name            string
		failure         error
		pending, cancel bool
	}{
		{name: "not-ready", failure: ch.ErrNotReady, pending: true},
		{name: "pressure", failure: ch.ErrBackpressured, pending: true},
		{name: "corrupt", failure: ch.ErrLogConflict},
		{name: "unknown", failure: errors.New("unknown recovery")},
		{name: "deadline", failure: context.DeadlineExceeded},
		{name: "canceled-yield", failure: ch.ErrNotReady, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, f, source := newReplayFixture(t)
			f.plan.HasAnchor, f.plan.Anchor, f.plan.Source.CommittedThrough = true, f.proof, 3
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.repair = func(context.Context, ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
				if tc.cancel {
					cancel()
				}
				return ch.MQTTReplayRecoveryResult{}, tc.failure
			}
			err := c.Confirm(ctx, source, 2)
			if tc.pending {
				require.ErrorIs(t, err, ErrReplayPending)
			} else {
				require.NotErrorIs(t, err, ErrReplayPending)
			}
			if tc.cancel {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, tc.failure)
			}
			require.Equal(t, 1, f.repairs)
			require.Zero(t, f.copies)
			require.Zero(t, f.commits)
		})
	}
}

// Initial and post-anchor queries are planning reads. A confirmed anchor still
// requires fresh coverage; unknown reads never authorize another write.
func TestReplayConfirmationAwaitsPlanningReadiness(t *testing.T) {
	for _, stage := range []string{"initial", "nested"} {
		for _, tc := range []struct {
			name              string
			failure           error
			pending, canceled bool
		}{
			{name: "not-ready", failure: ch.ErrNotReady, pending: true},
			{name: "pressure", failure: ch.ErrBackpressured, pending: true},
			{name: "stale", failure: ch.ErrStaleMeta},
			{name: "corrupt", failure: ch.ErrLogConflict},
			{name: "invalid", failure: ch.ErrInvalidConfig},
			{name: "unknown", failure: errors.New("planning result unavailable")},
			{name: "deadline", failure: context.DeadlineExceeded},
			{name: "canceled", failure: ch.ErrNotReady, canceled: true},
		} {
			t.Run(stage+"/"+tc.name, func(t *testing.T) {
				c, f, source := newReplayFixture(t)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				failingRead := 1
				if stage == "nested" {
					failingRead = 2
				}
				f.planRead = func(context.Context, ch.MQTTReplayPlanRequest) (ch.MQTTReplayPlan, error) {
					if f.plans == failingRead {
						if tc.canceled {
							cancel()
						}
						return ch.MQTTReplayPlan{}, tc.failure
					}
					return f.plan, nil
				}
				err := c.Confirm(ctx, source, 2)
				if tc.pending {
					require.ErrorIs(t, err, ErrReplayPending)
				} else {
					require.NotErrorIs(t, err, ErrReplayPending)
				}
				if tc.canceled {
					require.ErrorIs(t, err, context.Canceled)
				} else {
					require.ErrorIs(t, err, tc.failure)
				}
				require.Equal(t, failingRead, f.plans)
				require.Equal(t, failingRead-1, f.copies)
				require.Equal(t, failingRead-1, f.commits)
				require.Zero(t, f.repairs)
				require.Equal(t, stage == "nested", f.plan.HasAnchor)
				if !tc.pending {
					return
				}
				f.planRead = nil
				f.repair = incompleteConfirmation(f)
				require.ErrorIs(t, c.Confirm(ctx, source, 2), ErrReplayPending, "a fresh copy/anchor still does not prove every replica")
				require.Equal(t, 1, f.copies)
				require.Equal(t, 1, f.commits)
				require.Equal(t, 3, f.repairs)
				f.repair = func(context.Context, ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
					return ch.MQTTReplayRecoveryResult{Plan: ch.MQTTReplayRepairPlan{Current: f.proof.Prefix(), Target: f.proof, Complete: true}}, nil
				}
				require.NoError(t, c.Confirm(ctx, source, 2))
				require.Equal(t, 6, f.repairs)
			})
		}
	}
}

// A positive anchor commit permits one fresh coverage phase, not another copy
// or acceptance of stale, incomplete or unknown evidence.
func TestReplayConfirmationContinuesOnlyConfirmedAnchor(t *testing.T) {
	for _, mode := range []string{"complete", "incomplete", "read_pending", "read_unknown", "read_canceled", "boundary_changed", "progress_regressed", "placement_changed", "new_fence"} {
		t.Run(mode, func(t *testing.T) {
			c, f, source := newReplayFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			unknown := errors.New("fresh coverage unavailable")
			f.repair = func(_ context.Context, q ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
				require.False(t, q.ReleaseSource)
				require.EqualValues(t, 3, q.TargetAnchor)
				if mode == "incomplete" {
					return incompleteConfirmation(f)(ctx, q)
				}
				return ch.MQTTReplayRecoveryResult{Plan: ch.MQTTReplayRepairPlan{Current: f.proof.Prefix(), Target: f.proof, Complete: true}}, nil
			}
			f.planRead = func(context.Context, ch.MQTTReplayPlanRequest) (ch.MQTTReplayPlan, error) {
				p := f.plan
				if f.plans == 2 {
					switch mode {
					case "read_pending":
						return ch.MQTTReplayPlan{}, ch.ErrNotReady
					case "read_unknown":
						return ch.MQTTReplayPlan{}, unknown
					case "read_canceled":
						cancel()
						return p, nil
					case "boundary_changed":
						p.Source.StartAfter++
					case "progress_regressed":
						p.Source.CommittedThrough = 1
					case "placement_changed":
						f.m.RouteGeneration++
					case "new_fence":
						f.m.WriteFence.Version, f.m.WriteFence.Token = 1, "changed"
					}
				}
				return p, nil
			}
			err := c.Confirm(ctx, source, 2)
			switch mode {
			case "complete":
				require.NoError(t, err)
			case "incomplete", "read_pending":
				require.ErrorIs(t, err, ErrReplayPending)
			case "read_unknown":
				require.ErrorIs(t, err, unknown)
				require.NotErrorIs(t, err, ErrReplayPending)
			case "read_canceled":
				require.ErrorIs(t, err, context.Canceled)
			default:
				require.Error(t, err)
				require.NotErrorIs(t, err, ErrReplayPending)
			}
			require.Equal(t, 1, f.copies)
			require.Equal(t, 1, f.commits)
		})
	}
}

func incompleteConfirmation(f *replayCoordinatorFixture) func(context.Context, ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
	return func(context.Context, ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
		return ch.MQTTReplayRecoveryResult{Plan: ch.MQTTReplayRepairPlan{Current: ch.MQTTReplayPrefix{Generation: f.plan.Source.Generation}, Target: f.proof, Next: f.proof, HasNext: true}, Repaired: true}, nil
	}
}

func TestReplayConfirmationRereadsBackgroundAnchorAfterCopyYield(t *testing.T) {
	c, f, source := newReplayFixture(t)
	f.afterCopy = func() { f.plan.HasAnchor, f.plan.Anchor, f.plan.Source.CommittedThrough = true, f.proof, 3 }
	f.copyErr = ch.ErrNotReady
	f.repair = func(_ context.Context, q ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
		require.False(t, q.ReleaseSource)
		require.EqualValues(t, 3, q.TargetAnchor)
		return ch.MQTTReplayRecoveryResult{Plan: ch.MQTTReplayRepairPlan{Current: f.proof.Prefix(), Target: f.proof, Complete: true}}, nil
	}
	require.ErrorIs(t, c.Confirm(context.Background(), source, 2), ErrReplayPending)
	require.Equal(t, 1, f.plans, "the captured fresh plan should not be reread before copying")
	require.NoError(t, c.Confirm(context.Background(), source, 2))
	require.Equal(t, 1, f.copies)
	require.Zero(t, f.commits)
	require.Equal(t, 3, f.repairs)
}
