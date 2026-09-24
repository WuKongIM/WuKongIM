package mqttsession

import (
	"context"
	"errors"
	"slices"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
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
	m, err := c.options.Metadata.ResolveChannelMetaFresh(ctx, q.ChannelID)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if !validReplayPlacement(m, q.ChannelID) {
		return ErrEvidence
	}
	if m.WriteFence.Set() {
		return ch.ErrWriteFenced
	}
	if m.Status != ch.StatusActive {
		return ErrReplayPending
	}
	authority := ch.MQTTReplayCopyAuthority(m)
	q.ExpectedChannelEpoch, q.ExpectedLeaderEpoch, q.ExpectedRouteGeneration = m.Epoch, m.LeaderEpoch, m.RouteGeneration
	plan, err := c.options.Channels.PlanChannelMQTTReplay(ctx, q)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if !plan.ValidFor(q) || startAfter <= plan.Source.StartAfter || startAfter > plan.Source.CommittedThrough {
		return ErrEvidence
	}
	_, hasCopy, err := plan.NextRange(c.options.PageSize, c.options.MaxBytes)
	if err != nil {
		return err
	}
	if !plan.HasAnchor || (plan.Anchor.Anchor.Through < startAfter && hasCopy) {
		// Even a successful anchor commit is not every replica's recovery proof.
		if _, err = c.Step(ctx, source, ReplayCursor{}); err != nil {
			return err
		}
		return ErrReplayPending
	}
	pending := false
	for _, target := range m.Replicas {
		if err = ctx.Err(); err != nil {
			return err
		}
		request := ch.MQTTReplayRecoveryRequest{Target: target, Source: q, TargetAnchor: plan.Anchor.Manifest.LastOffset, ScanLimit: 64, ApplyRetirement: true}
		result, e := c.options.Channels.StepChannelMQTTReplayRecovery(ctx, request)
		if e != nil {
			return e
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if !result.ValidFor(request) || result.Plan.Target != plan.Anchor || (result.DonorAfter != 0 && !slices.Contains(m.Replicas, result.DonorAfter)) {
			return ErrEvidence
		}
		pending = pending || !result.Plan.Complete || result.RetirementPending
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
