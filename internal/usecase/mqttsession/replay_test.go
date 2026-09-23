package mqttsession

import (
	"context"
	"strings"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

type replayCoordinatorFixture struct {
	m                                          ch.Meta
	plan                                       ch.MQTTReplayPlan
	receipt                                    ch.MQTTReplayCopyReceipt
	proof                                      ch.MQTTReplayAnchorProof
	metaErr, planErr, copyErr, commitErr       error
	metaCalls, plans, copies, commits, repairs int
	id                                         uint64
	afterCopy                                  func()
	repair                                     func(context.Context, ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error)
}

func (f *replayCoordinatorFixture) ResolveChannelMetaFresh(context.Context, ch.ChannelID) (ch.Meta, error) {
	f.metaCalls++
	return f.m, f.metaErr
}
func (f *replayCoordinatorFixture) PlanChannelMQTTReplay(context.Context, ch.MQTTReplayPlanRequest) (ch.MQTTReplayPlan, error) {
	f.plans++
	return f.plan, f.planErr
}
func (f *replayCoordinatorFixture) CopyChannelMQTTReplay(context.Context, ch.MQTTReplayRequest) (ch.MQTTReplayCopyReceipt, error) {
	f.copies++
	if f.afterCopy != nil {
		f.afterCopy()
	}
	return f.receipt, f.copyErr
}
func (f *replayCoordinatorFixture) CommitChannelMQTTReplayAnchor(_ context.Context, q ch.MQTTReplayAnchorRequest) (ch.MQTTReplayAnchorProof, error) {
	f.commits++
	if f.commitErr != nil {
		return ch.MQTTReplayAnchorProof{}, f.commitErr
	}
	f.plan.Anchor = f.proof
	f.plan.HasAnchor = true
	f.plan.Source.CommittedThrough = f.proof.Manifest.LastOffset
	return f.proof, nil
}
func (f *replayCoordinatorFixture) StepChannelMQTTReplayRecovery(c context.Context, q ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
	f.repairs++
	return f.repair(c, q)
}
func (f *replayCoordinatorFixture) Next() uint64 { return f.id }

func newReplayFixture(t *testing.T) (*ReplayCoordinator, *replayCoordinatorFixture, meta.MQTTBindingOwner) {
	t.Helper()
	owner := meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: "2:group", Generation: "mqtt-log-v1:01" + strings.Repeat("00", 31)}
	id := ch.ChannelID{ID: "group", Type: 2}
	m := ch.Meta{ID: id, Key: ch.ChannelKeyForID(id), Epoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	before := ch.MQTTReplayPrefix{Generation: owner.Generation}
	after := ch.MQTTReplayPrefix{Generation: owner.Generation, Through: 2, TotalBytes: 4, TotalStoredBytes: 6, Digest: [32]byte{1}}
	q := ch.MQTTReplayRequest{ChannelID: id, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, Range: ch.MQTTReplayRange{Generation: owner.Generation, From: 1, Through: 2, Limit: 2, MaxBytes: 6}}
	receipt := ch.MQTTReplayCopyReceipt{Request: q, Leader: 1, Authority: ch.MQTTReplayCopyAuthority(m), WriteQuorum: 2, Before: before, After: after, Copies: []ch.NodeID{1, 2}}
	proof := ch.MQTTReplayAnchorProof{Anchor: quorumlog.MQTTReplayAnchor{SourceCommand: ch.CommandID{1}, Through: 2, TotalBytes: 4, TotalStoredBytes: 6, Digest: after.Digest}, Manifest: ch.ProposalManifest{Version: 5, ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1, CommandID: ch.CommandID{2}, BaseOffset: 2, LastOffset: 3, PreviousIndex: 2, PreviousTerm: 1, PreviousDigest: ch.EntryDigest{3}, Digest: ch.EntryDigest{4}}}
	f := &replayCoordinatorFixture{m: m, receipt: receipt, proof: proof, id: 55, plan: ch.MQTTReplayPlan{Source: ch.MQTTSourceSnapshot{Generation: owner.Generation, CommittedThrough: 2}}}
	f.repair = func(ctx context.Context, q ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), 5*time.Second)
		require.Equal(t, 64, q.ScanLimit)
		require.True(t, q.ReleaseSource)
		require.True(t, q.ApplyRetirement)
		return ch.MQTTReplayRecoveryResult{Plan: ch.MQTTReplayRepairPlan{Current: proof.Prefix(), Target: proof, Complete: true}, SourceReleased: true}, nil
	}
	c, err := NewReplayCoordinator(ReplayCoordinatorOptions{Metadata: f, Channels: f, MessageIDs: f, Now: func() time.Time { return time.UnixMilli(1000) }})
	require.NoError(t, err)
	return c, f, owner
}

func TestReplayCoordinatorKeepsCleanupPendingTargetAndRotates(t *testing.T) {
	c, f, owner := newReplayFixture(t)
	f.plan.HasAnchor, f.plan.Anchor, f.plan.Source.CommittedThrough = true, f.proof, f.proof.Manifest.LastOffset
	f.repair = func(_ context.Context, q ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
		require.True(t, q.ApplyRetirement)
		return ch.MQTTReplayRecoveryResult{Plan: ch.MQTTReplayRepairPlan{Current: f.proof.Prefix(), Target: f.proof, Complete: true}, SourceReleased: true, RetirementPending: q.Target == 1}, nil
	}
	first, err := c.Step(context.Background(), owner, ReplayCursor{})
	require.NoError(t, err)
	require.False(t, first.TargetComplete)
	require.False(t, first.ContinueScan, "cleanup yields; only advancing journal scans retain a worker visit")
	require.Equal(t, f.proof.Manifest.LastOffset, first.Next.Targets[0].AnchorPosition)
	second, err := c.Step(context.Background(), owner, first.Next)
	require.NoError(t, err)
	require.Equal(t, ch.NodeID(2), second.Target)
	require.True(t, second.TargetComplete)
}

func TestReplayCoordinatorCopiesThenRotatesPinnedRecovery(t *testing.T) {
	c, f, owner := newReplayFixture(t)
	first, err := c.Step(context.Background(), owner, ReplayCursor{})
	require.NoError(t, err)
	require.True(t, first.Anchored)
	require.True(t, first.Next.RepairNext)
	require.Equal(t, 1, f.commits)
	old := f.proof
	var seen []ch.MQTTReplayRecoveryRequest
	f.repair = func(_ context.Context, q ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
		seen = append(seen, q)
		if q.Target == 1 && q.DonorAfter == 0 {
			return ch.MQTTReplayRecoveryResult{Plan: ch.MQTTReplayRepairPlan{Current: f.receipt.Before, Target: old, Next: old, HasNext: true}, DonorAfter: 2}, nil
		}
		return ch.MQTTReplayRecoveryResult{Plan: ch.MQTTReplayRepairPlan{Current: old.Prefix(), Target: old, Complete: true}, SourceReleased: true}, nil
	}
	a, err := c.Step(context.Background(), owner, first.Next)
	require.NoError(t, err)
	require.False(t, a.TargetComplete)
	require.Equal(t, ch.NodeID(1), a.Target)
	b, err := c.Step(context.Background(), owner, a.Next)
	require.NoError(t, err)
	require.True(t, b.TargetComplete)
	require.Equal(t, ch.NodeID(2), b.Target)
	d, err := c.Step(context.Background(), owner, b.Next)
	require.NoError(t, err)
	require.Equal(t, ch.NodeID(3), d.Target)
	// A moving latest anchor must not erase the first target's continuation.
	f.plan.Source.CommittedThrough = 6
	f.plan.Anchor = old
	f.plan.Anchor.Manifest.BaseOffset = 5
	f.plan.Anchor.Manifest.LastOffset = 6
	f.plan.Anchor.Manifest.PreviousIndex = 5
	f.plan.Anchor.Anchor.Through = 5
	f.plan.Anchor.Anchor.TotalBytes = 10
	f.plan.Anchor.Anchor.TotalStoredBytes = 15
	f.plan.Anchor.Anchor.Digest = [32]byte{2}
	resumed, err := c.Step(context.Background(), owner, d.Next)
	require.NoError(t, err)
	require.True(t, resumed.TargetComplete)
	require.Equal(t, uint64(3), seen[3].TargetAnchor)
	require.Equal(t, ch.NodeID(2), seen[3].DonorAfter)
	require.Equal(t, 1, f.copies, "idle accepted tails must not produce controls")
	// Returned continuation owns its slices, independently of previous results.
	resumed.Next.Targets[0].DonorAfter = 99
	require.NotEqual(t, ch.NodeID(99), d.Next.Targets[0].DonorAfter)
}

func TestReplayCoordinatorErrorYieldsAndPreservesReplicaHints(t *testing.T) {
	c, f, owner := newReplayFixture(t)
	f.copyErr = ch.ErrNotReady
	failed, err := c.Step(context.Background(), owner, ReplayCursor{})
	require.ErrorIs(t, err, ch.ErrNotReady)
	require.True(t, failed.Next.RepairNext)
	require.Zero(t, f.commits)
	f.copyErr = nil
	f.plan.HasAnchor = true
	f.plan.Anchor = f.proof
	f.plan.Source.CommittedThrough = 4
	f.repair = func(context.Context, ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
		return ch.MQTTReplayRecoveryResult{}, ch.ErrNotReady
	}
	repair, err := c.Step(context.Background(), owner, failed.Next)
	require.ErrorIs(t, err, ch.ErrNotReady)
	require.Equal(t, 1, f.repairs)
	require.Equal(t, 1, repair.Next.NextTarget)
	require.False(t, repair.Next.RepairNext)
	// Another failed copy still gives the next recovery turn to target 2.
	f.copyErr = ch.ErrNotReady
	again, err := c.Step(context.Background(), owner, repair.Next)
	require.ErrorIs(t, err, ch.ErrNotReady)
	require.True(t, again.Next.RepairNext)
	f.repair = func(_ context.Context, q ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
		require.Equal(t, ch.NodeID(2), q.Target)
		return ch.MQTTReplayRecoveryResult{Plan: ch.MQTTReplayRepairPlan{Current: f.proof.Prefix(), Target: f.proof, Complete: true}, SourceReleased: true}, nil
	}
	good, err := c.Step(context.Background(), owner, again.Next)
	require.NoError(t, err)
	require.True(t, good.TargetComplete)
}

func TestReplayCoordinatorRejectsUnsafeCopyAndContinuation(t *testing.T) {
	for _, mode := range []string{"cancel_before", "cancel_after_copy", "meta_error", "plan_error", "bad_meta", "weak_quorum", "bad_plan", "foreign_receipt", "copy_ahead", "bad_receipt", "bad_proof", "future_proof", "zero_id", "zero_clock", "lost_commit", "oversize_cursor", "future_cursor"} {
		t.Run(mode, func(t *testing.T) {
			c, f, owner := newReplayFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cursor := ReplayCursor{}
			switch mode {
			case "cancel_before":
				cancel()
			case "cancel_after_copy":
				f.afterCopy = cancel
			case "meta_error":
				f.metaErr = ch.ErrNotReady
			case "plan_error":
				f.planErr = ch.ErrNotReady
			case "bad_meta":
				f.m.Replicas = []ch.NodeID{1, 1}
			case "weak_quorum":
				f.m.MinISR = 1
			case "bad_plan":
				f.plan.Source.Generation = "foreign"
			case "foreign_receipt":
				f.receipt.Request.ChannelID.ID = "foreign"
			case "copy_ahead":
				f.receipt.Request.Range.Through = 3
			case "bad_receipt":
				f.receipt.Copies = []ch.NodeID{1}
			case "bad_proof":
				f.proof.Anchor.Digest = [32]byte{9}
			case "future_proof":
				f.proof.Manifest.ChannelEpoch++
			case "zero_id":
				f.id = 0
			case "zero_clock":
				c.options.Now = func() time.Time { return time.Time{} }
			case "lost_commit":
				f.commitErr = context.DeadlineExceeded
			case "oversize_cursor":
				cursor.Targets = make([]ReplayTargetCursor, 257)
			case "future_cursor":
				var e error
				seed, e := c.Step(ctx, owner, cursor)
				require.NoError(t, e)
				cursor = seed.Next
				cursor.Targets[0].AnchorPosition = 99
			}
			out, err := c.Step(ctx, owner, cursor)
			require.Error(t, err)
			require.False(t, out.Anchored)
			require.False(t, out.Repaired)
			require.False(t, out.TargetComplete)
			if mode == "cancel_before" || mode == "meta_error" || mode == "bad_meta" || mode == "weak_quorum" || mode == "oversize_cursor" {
				require.Zero(t, f.copies)
			}
			if mode == "cancel_after_copy" || mode == "foreign_receipt" || mode == "copy_ahead" || mode == "bad_receipt" || mode == "zero_id" || mode == "zero_clock" {
				require.Zero(t, f.commits)
			}
		})
	}
}

func TestReplayCoordinatorWriteFenceOnlyPermitsExistingAnchorRecovery(t *testing.T) {
	c, f, owner := newReplayFixture(t)
	f.m.WriteFence = ch.WriteFence{Token: "moving", Version: 1}
	idle, err := c.Step(context.Background(), owner, ReplayCursor{})
	require.NoError(t, err)
	require.False(t, idle.Anchored)
	require.Zero(t, f.copies)
	require.Zero(t, f.commits)
	require.Zero(t, f.repairs)
	require.Equal(t, 1, f.plans)
	f.plan.HasAnchor, f.plan.Anchor = true, f.proof
	f.plan.Source.CommittedThrough = 4 // New uncopied content must not trigger copying under the fence.
	step := idle
	for _, target := range []ch.NodeID{1, 2, 3, 1} {
		step, err = c.Step(context.Background(), owner, step.Next)
		require.NoError(t, err)
		require.Equal(t, target, step.Target)
		require.True(t, step.TargetComplete)
		require.False(t, step.Anchored)
	}
	require.Zero(t, f.copies)
	require.Zero(t, f.commits)
	require.Equal(t, 4, f.repairs)
	f.repair = func(context.Context, ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
		return ch.MQTTReplayRecoveryResult{}, ch.ErrNotReady
	}
	failed, err := c.Step(context.Background(), owner, step.Next)
	require.ErrorIs(t, err, ch.ErrNotReady)
	require.Equal(t, ch.NodeID(2), failed.Target)
	require.Equal(t, 2, failed.Next.NextTarget)
	require.False(t, failed.TargetComplete)
}

func TestReplayCoordinatorResetsPlacementHintsAndRetainsScan(t *testing.T) {
	c, f, owner := newReplayFixture(t)
	seed, err := c.Step(context.Background(), owner, ReplayCursor{})
	require.NoError(t, err)
	seed.Next.Targets[0].AnchorPosition = 3
	seed.Next.Targets[0].DonorAfter = 2
	f.m.Replicas = []ch.NodeID{3, 1, 2}
	f.repair = func(_ context.Context, q ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
		require.Equal(t, ch.NodeID(3), q.Target)
		require.Zero(t, q.DonorAfter)
		return ch.MQTTReplayRecoveryResult{Plan: ch.MQTTReplayRepairPlan{Current: f.receipt.Before, Target: f.proof, ScanAfter: 1}}, nil
	}
	step, err := c.Step(context.Background(), owner, seed.Next)
	require.NoError(t, err)
	require.Equal(t, uint64(1), step.Next.Targets[0].AfterAnchor)
	f.repair = func(_ context.Context, q ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
		if q.Target == 3 {
			require.Equal(t, uint64(1), q.AfterAnchor)
			return ch.MQTTReplayRecoveryResult{Plan: ch.MQTTReplayRepairPlan{Current: f.receipt.Before, Target: f.proof, Next: f.proof, HasNext: true, ScanAfter: 1}, Repaired: true}, nil
		}
		return ch.MQTTReplayRecoveryResult{Plan: ch.MQTTReplayRepairPlan{Current: f.proof.Prefix(), Target: f.proof, Complete: true}, SourceReleased: true}, nil
	}
	for range 3 {
		step, err = c.Step(context.Background(), owner, step.Next)
		require.NoError(t, err)
	}
	require.True(t, step.Repaired)
	require.False(t, step.TargetComplete)
	require.Zero(t, step.Next.Targets[0].AfterAnchor)
	require.Equal(t, uint64(3), step.Next.Targets[0].AnchorPosition)
}

func TestReplayCoordinatorRejectsInvalidConstructionAndSources(t *testing.T) {
	c, f, owner := newReplayFixture(t)
	for _, mutate := range []func(*ReplayCoordinatorOptions){func(o *ReplayCoordinatorOptions) { o.Metadata = nil }, func(o *ReplayCoordinatorOptions) { o.Channels = nil }, func(o *ReplayCoordinatorOptions) { o.MessageIDs = nil }, func(o *ReplayCoordinatorOptions) { o.Timeout = 2 * time.Minute }, func(o *ReplayCoordinatorOptions) { o.PageSize = 257 }, func(o *ReplayCoordinatorOptions) { o.MaxBytes = 17 << 20 }} {
		options := c.options
		mutate(&options)
		_, err := NewReplayCoordinator(options)
		require.Error(t, err)
	}
	for _, bad := range []meta.MQTTBindingOwner{{Kind: meta.MQTTBindingUID, ID: "alice"}, {Kind: meta.MQTTBindingChannel, ID: "02:group", Generation: owner.Generation}, {Kind: meta.MQTTBindingChannel, ID: "2:group", Generation: "foreign"}, {Kind: meta.MQTTBindingChannel, ID: "0:group", Generation: owner.Generation}} {
		_, err := c.Step(context.Background(), bad, ReplayCursor{})
		require.Error(t, err)
	}
	require.Zero(t, f.metaCalls)
}

func TestReplayCoordinatorRejectsUnassociatedRecovery(t *testing.T) {
	for _, mode := range []string{"foreign_donor", "foreign_target", "future_authority", "false_complete", "unreleased", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			c, f, owner := newReplayFixture(t)
			seed, err := c.Step(context.Background(), owner, ReplayCursor{})
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.repair = func(_ context.Context, q ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
				result := ch.MQTTReplayRecoveryResult{Plan: ch.MQTTReplayRepairPlan{Current: f.proof.Prefix(), Target: f.proof, Complete: true}, SourceReleased: true}
				switch mode {
				case "foreign_donor":
					result.Plan = ch.MQTTReplayRepairPlan{Current: f.receipt.Before, Target: f.proof, Next: f.proof, HasNext: true}
					result.DonorAfter = 99
				case "foreign_target":
					result.Plan.Target.Manifest.LastOffset++
				case "future_authority":
					result.Plan.Target.Manifest.ChannelEpoch++
				case "unreleased":
					result.SourceReleased = false
				case "false_complete":
					result.Repaired = true
				case "cancel":
					cancel()
				}
				return result, nil
			}
			result, err := c.Step(ctx, owner, seed.Next)
			require.Error(t, err)
			require.False(t, result.TargetComplete)
			require.False(t, result.Repaired)
			require.Equal(t, 1, result.Next.NextTarget)
		})
	}
}

func TestReplayCoordinatorRejectsCopyOutsidePlannedPrefixAndBudget(t *testing.T) {
	for _, mode := range []string{"before_digest", "page_limit", "byte_limit"} {
		t.Run(mode, func(t *testing.T) {
			c, f, owner := newReplayFixture(t)
			switch mode {
			case "before_digest":
				f.plan.HasAnchor = true
				f.plan.Anchor = f.proof
				f.plan.Source.CommittedThrough = 4
				f.receipt.Before = f.proof.Prefix()
				f.receipt.Before.Digest = [32]byte{99}
				f.receipt.After = ch.MQTTReplayPrefix{Generation: owner.Generation, Through: 4, TotalBytes: 8, TotalStoredBytes: 12, Digest: [32]byte{2}}
				f.receipt.Request.Range.From = 3
				f.receipt.Request.Range.Through = 4
			case "page_limit":
				c.options.PageSize = 1
			case "byte_limit":
				c.options.MaxBytes = 5
			}
			require.True(t, f.receipt.ValidFor(f.m), "receipt is structurally valid but does not match this turn")
			_, err := c.Step(context.Background(), owner, ReplayCursor{})
			require.Error(t, err)
			require.Zero(t, f.commits)
		})
	}
}

func TestReplayCoordinatorColdPassRotatesReplicaPhaseAndDonor(t *testing.T) {
	c, f, owner := newReplayFixture(t)
	f.plan.HasAnchor = true
	f.plan.Anchor = f.proof
	f.plan.Source.CommittedThrough = 4
	// Every visit is cold: there is no per-source cache to preserve NextTarget.
	f.copyErr = ch.ErrNotReady
	var requests []ch.MQTTReplayRecoveryRequest
	f.repair = func(_ context.Context, q ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
		requests = append(requests, q)
		return ch.MQTTReplayRecoveryResult{}, ch.ErrNotReady
	}
	for pass := uint64(0); pass < 24; pass++ {
		_, err := c.Step(context.Background(), owner, ReplayCursor{Pass: pass})
		require.ErrorIs(t, err, ch.ErrNotReady)
	}
	require.Equal(t, 12, f.copies)
	require.Len(t, requests, 12)
	seen := map[ch.NodeID]map[ch.NodeID]bool{}
	for _, q := range requests {
		if seen[q.Target] == nil {
			seen[q.Target] = map[ch.NodeID]bool{}
		}
		seen[q.Target][q.DonorAfter] = true
		require.Equal(t, uint64(3), q.TargetAnchor)
	}
	for _, target := range f.m.Replicas {
		require.Contains(t, seen, target)
		for _, donor := range f.m.Replicas {
			if donor != target {
				require.True(t, seen[target][donor], "target %d never advances past donor %d", target, donor)
			}
		}
	}
}

func TestReplayCoordinatorOnlyScanRequestsImmediateContinuation(t *testing.T) {
	c, f, owner := newReplayFixture(t)
	seed, err := c.Step(context.Background(), owner, ReplayCursor{})
	require.NoError(t, err)
	require.False(t, seed.ContinueScan)
	f.repair = func(context.Context, ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
		return ch.MQTTReplayRecoveryResult{Plan: ch.MQTTReplayRepairPlan{Current: f.receipt.Before, Target: f.proof, ScanAfter: 1}}, nil
	}
	scan, err := c.Step(context.Background(), owner, seed.Next)
	require.NoError(t, err)
	require.True(t, scan.ContinueScan)
	f.repair = func(context.Context, ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
		return ch.MQTTReplayRecoveryResult{Plan: ch.MQTTReplayRepairPlan{Current: f.receipt.Before, Target: f.proof, Next: f.proof, HasNext: true}, DonorAfter: 1}, nil
	}
	retry, err := c.Step(context.Background(), owner, scan.Next)
	require.NoError(t, err)
	require.False(t, retry.ContinueScan)
}
