package mqttsession

import (
	"context"
	"slices"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ReplayRetentionChannels captures accepted source progress through the freshly
// resolved Channel authority; speculative replica copying is never sufficient.
type ReplayRetentionChannels interface {
	PlanChannelMQTTReplay(context.Context, ch.MQTTReplayPlanRequest) (ch.MQTTReplayPlan, error)
}

// ReplayRetentionMetadata requires a fresh Slot barrier and strict pinned
// primary/index witnesses. Local or generic skip-on-inconsistency scans cannot
// implement this port's retention read contract.
type ReplayRetentionMetadata interface {
	ReadMQTT(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error)
}

type ReplayRetentionOptions struct {
	Metadata ReplayMetadata
	Channels ReplayRetentionChannels
	Store    ReplayRetentionMetadata
	// Timeout bounds placement, anchor, consumer and final authority reads together.
	Timeout time.Duration
}

// ReplayRetentionPlan bounds a future replicated reclamation decision. It is
// neither a committed GC certificate nor permission for replica-local deletion.
type ReplayRetentionPlan struct {
	Source    meta.MQTTBindingOwner
	Placement ch.Meta
	Replay    ch.MQTTReplayPlan
	// Through is capped by the captured anchor and the lowest live obligation.
	// Zero also represents unknown preparation or the absence of an anchor.
	Through     uint64
	HasConsumer bool
	Consumer    meta.MQTTSourceBinding
}

// ReplayRetention owns no mutable scheduling state, worker or storage mutation.
type ReplayRetention struct{ options ReplayRetentionOptions }

func NewReplayRetention(o ReplayRetentionOptions) (*ReplayRetention, error) {
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Metadata == nil || o.Channels == nil || o.Store == nil || o.Timeout <= 0 || o.Timeout > time.Minute {
		return nil, ErrInvalid
	}
	return &ReplayRetention{options: o}, nil
}

// Plan captures the anchor BEFORE the first consumer page. New bindings must
// commit unknown responsibility before selecting a fresh source tail, so later
// admission cannot start below this captured anchor. Reversing these reads is
// unsafe even if each individual read is linearizable.
func (p *ReplayRetention) Plan(parent context.Context, source meta.MQTTBindingOwner) (ReplayRetentionPlan, error) {
	var out ReplayRetentionPlan
	q, valid := replaySourceRequest(source)
	if p == nil || parent == nil || !valid {
		return out, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, p.options.Timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	m, err := p.options.Metadata.ResolveChannelMetaFresh(ctx, q.ChannelID)
	if err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if !validReplayPlacement(m, q.ChannelID) {
		return out, ErrEvidence
	}
	m.Replicas, m.ISR = slices.Clone(m.Replicas), slices.Clone(m.ISR)
	q.ExpectedChannelEpoch, q.ExpectedLeaderEpoch, q.ExpectedRouteGeneration = m.Epoch, m.LeaderEpoch, m.RouteGeneration
	replay, err := p.options.Channels.PlanChannelMQTTReplay(ctx, q)
	if err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if !replay.ValidFor(q) {
		return out, ErrEvidence
	}
	if !replay.HasAnchor {
		return ReplayRetentionPlan{Source: source, Placement: m, Replay: replay}, nil
	}
	r, err := p.options.Store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSourceRetention, Owner: source, Limit: 1})
	if err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if r.Session != nil || len(r.SourceOwners) != 0 || len(r.Sessions) != 0 || len(r.Subscriptions) != 0 || len(r.DeliveryCursors) != 0 || len(r.Inflight) != 0 || len(r.Wills) != 0 || len(r.Bindings) > 1 || (len(r.Bindings) == 0 && !r.Done) {
		return out, ErrEvidence
	}
	next := ReplayRetentionPlan{Source: source, Placement: m, Replay: replay, Through: replay.Anchor.Anchor.Through}
	var after meta.MQTTReadCursor
	if len(r.Bindings) == 1 {
		b := r.Bindings[0]
		if meta.ValidateMQTTSourceBinding(b) != nil || b.Key.Owner != source || b.Stage == meta.MQTTBindingRemoved || (b.BoundaryKnown && b.StartAfter < replay.Source.StartAfter) || (b.CompletedThrough > b.StartAfter && b.ProgressRevision == 0) {
			return out, ErrEvidence
		}
		next.HasConsumer, next.Consumer = true, b
		next.Through = min(next.Through, b.CompletedThrough)
		if !r.Done {
			after.Retention = meta.MQTTSourceBindingRetentionCursor{Key: b.Key, CompletedThrough: b.CompletedThrough}
		}
	}
	if r.After != after {
		return out, ErrEvidence
	}
	current, err := p.options.Metadata.ResolveChannelMetaFresh(ctx, q.ChannelID)
	if err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if !validReplayPlacement(current, q.ChannelID) || ch.MQTTReplayCopyAuthority(current) != ch.MQTTReplayCopyAuthority(m) || current.WriteFence != m.WriteFence {
		return out, ErrEvidence
	}
	return next, nil
}
