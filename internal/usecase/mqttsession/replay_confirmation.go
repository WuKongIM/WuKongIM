package mqttsession

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

// ErrReplayPending retains Preparing intent while bounded maintenance establishes
// the captured shared-content coverage. It never authorizes subscription success.
var ErrReplayPending = errors.New("mqttsession: shared replay recovery pending")

// Confirm verifies one captured anchor on every eligible replica under unchanged
// placement. Missing coverage yields after bounded work; long scans and fairness
// belong to managed maintenance, not an unbounded subscription retry loop.
func (c *ReplayCoordinator) Confirm(parent context.Context, source meta.MQTTBindingOwner, startAfter uint64) error {
	if c == nil || parent == nil || startAfter == 0 {
		return ErrInvalid
	}
	q, ok := replaySourceRequest(source)
	if !ok {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, c.options.Timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	m, plan, err := c.confirmationPlan(ctx, q, startAfter)
	if err != nil {
		return err
	}
	authority := ch.MQTTReplayCopyAuthority(m)
	q.ExpectedChannelEpoch, q.ExpectedLeaderEpoch, q.ExpectedRouteGeneration = m.Epoch, m.LeaderEpoch, m.RouteGeneration
	rangeToCopy, hasCopy, err := plan.NextRange(c.options.PageSize, c.options.MaxBytes)
	if err != nil {
		return err
	}
	if !plan.HasAnchor || (plan.Anchor.Anchor.Through < startAfter && hasCopy) {
		if !hasCopy {
			return ErrReplayPending
		}
		_, e := c.copyAndAnchor(ctx, m, plan, rangeToCopy, ReplayStepResult{})
		if e != nil {
			return e
		}
		// A positively acknowledged anchor permits one fresh coverage phase.
		// The captured plan is not reread before copy; each effect remains fenced.
		// It cannot authorize another copy, a changed start or a partial receipt.
		current, captured, e := c.confirmationPlan(ctx, q, startAfter)
		if e != nil {
			return e
		}
		if ch.MQTTReplayCopyAuthority(current) != authority {
			return ErrConflict
		}
		if captured.Source.StartAfter != plan.Source.StartAfter || captured.Source.CommittedThrough < plan.Source.CommittedThrough {
			return ErrEvidence
		}
		m, plan = current, captured
		_, hasCopy, err = plan.NextRange(c.options.PageSize, c.options.MaxBytes)
		if err != nil {
			return err
		}
		if !plan.HasAnchor || (plan.Anchor.Anchor.Through < startAfter && hasCopy) {
			return ErrReplayPending
		}
	}
	pending, err := c.confirmReplicas(ctx, m.Replicas, q, plan)
	if err != nil {
		return err
	}
	if pending {
		return ErrReplayPending
	}
	current, err := c.options.Metadata.ResolveChannelMetaFresh(ctx, q.ChannelID)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if !validReplayPlacement(current, q.ChannelID) || current.WriteFence.Set() || ch.MQTTReplayCopyAuthority(current) != authority {
		return ErrConflict
	}
	return nil
}

// confirmationPlan captures fresh placement and read-only progress, including
// after one positively acknowledged anchor. Unknown reads never gain retry proof.
func (c *ReplayCoordinator) confirmationPlan(ctx context.Context, q ch.MQTTReplayPlanRequest, startAfter uint64) (ch.Meta, ch.MQTTReplayPlan, error) {
	m, err := c.options.Metadata.ResolveChannelMetaFresh(ctx, q.ChannelID)
	if err != nil {
		return m, ch.MQTTReplayPlan{}, err
	}
	if err = ctx.Err(); err != nil {
		return m, ch.MQTTReplayPlan{}, err
	}
	if !validReplayPlacement(m, q.ChannelID) {
		return m, ch.MQTTReplayPlan{}, ErrEvidence
	}
	if m.WriteFence.Set() {
		return m, ch.MQTTReplayPlan{}, ch.ErrWriteFenced
	}
	if m.Status != ch.StatusActive {
		return m, ch.MQTTReplayPlan{}, ErrReplayPending
	}
	q.ExpectedChannelEpoch, q.ExpectedLeaderEpoch, q.ExpectedRouteGeneration = m.Epoch, m.LeaderEpoch, m.RouteGeneration
	plan, err := c.options.Channels.PlanChannelMQTTReplay(ctx, q)
	if stopped := ctx.Err(); stopped != nil {
		return m, plan, stopped
	}
	if err != nil {
		if errors.Is(err, ch.ErrNotReady) || errors.Is(err, ch.ErrBackpressured) {
			return m, plan, errors.Join(ErrReplayPending, err)
		}
		return m, plan, err
	}
	if !plan.ValidFor(q) || startAfter <= plan.Source.StartAfter || startAfter > plan.Source.CommittedThrough {
		return m, plan, ErrEvidence
	}
	return m, plan, nil
}

// confirmReplicas joins the entire admitted cohort before returning. Results are
// body-free and bounded by validated placement (at most 256 replicas); no queued
// background work or partial quorum can authorize a confirmation receipt.
func (c *ReplayCoordinator) confirmReplicas(ctx context.Context, replicas []ch.NodeID, q ch.MQTTReplayPlanRequest, plan ch.MQTTReplayPlan) (bool, error) {
	if c.options.ConfirmWorkers == 1 {
		pending := false
		for _, target := range replicas {
			wait, err := c.confirmReplica(ctx, replicas, target, q, plan)
			if err != nil {
				return false, err
			}
			pending = pending || wait
		}
		return pending, nil
	}
	type result struct {
		pending bool
		err     error
	}
	results := make([]result, len(replicas))
	var next atomic.Uint32
	var joined sync.WaitGroup
	for range min(c.options.ConfirmWorkers, len(replicas)) {
		joined.Add(1)
		goroutine.SafeGo(nil, goroutine.TaskMQTTReplayConfirmation, func() {
			defer joined.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(replicas) {
					return
				}
				results[i].pending, results[i].err = c.confirmReplica(ctx, replicas, replicas[i], q, plan)
			}
		})
	}
	joined.Wait()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	var failed, yielded error
	pending := false
	for _, r := range results {
		if r.err != nil {
			if errors.Is(r.err, ErrReplayPending) {
				yielded = errors.Join(yielded, r.err)
			} else {
				failed = errors.Join(failed, r.err)
			}
		}
		pending = pending || r.pending
	}
	// A temporary replica yield cannot make another replica's unknown or invalid
	// result retryable. Preserve hard failure without the pending classification.
	if failed != nil {
		return false, failed
	}
	return pending, yielded
}

// confirmReplica checks the same captured anchor and donor membership for each
// target. Dependency panic is a failed result and never escapes a joined worker.
func (c *ReplayCoordinator) confirmReplica(ctx context.Context, replicas []ch.NodeID, target ch.NodeID, q ch.MQTTReplayPlanRequest, plan ch.MQTTReplayPlan) (pending bool, err error) {
	defer func() {
		if recover() != nil {
			pending, err = false, ErrSubscriptionCallback
		}
	}()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	request := ch.MQTTReplayRecoveryRequest{Target: target, Source: q, TargetAnchor: plan.Anchor.Manifest.LastOffset, ScanLimit: 64, ApplyRetirement: true}
	// Inert outside temporary gofail builds: independent hard and temporary
	// replica failures cannot combine into retry authority or a partial receipt.
	// gofail: var wkMQTTReplayConfirmationMixedResult string
	// if wkMQTTReplayConfirmationMixedResult != "" {
	//  if target == 1 {
	//   if wkMQTTReplayConfirmationMixedResult == "evidence" { return false, ErrEvidence }
	//   return false, ErrSubscriptionCallback
	//  }
	//  if target == 2 { return false, errors.Join(ErrReplayPending, ch.ErrNotReady) }
	// }
	result, err := c.options.Channels.StepChannelMQTTReplayRecovery(ctx, request)
	if stopped := ctx.Err(); stopped != nil {
		return false, stopped
	}
	if err != nil {
		if errors.Is(err, ch.ErrNotReady) || errors.Is(err, ch.ErrBackpressured) {
			return false, errors.Join(ErrReplayPending, err)
		}
		return false, err
	}
	if !result.ValidFor(request) || result.Plan.Target != plan.Anchor || (result.DonorAfter != 0 && !slices.Contains(replicas, result.DonorAfter)) {
		return false, ErrEvidence
	}
	return !result.Plan.Complete || result.RetirementPending, nil
}
