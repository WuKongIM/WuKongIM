package mqttsession

import (
	"context"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ReplayRetirementChannels independently fences journal selection and typed
// retirement admission through current cluster authority. Neither port deletes
// local content; replica recovery applies the eventual committed decision.
type ReplayRetirementChannels interface {
	SelectChannelMQTTReplayRetirement(context.Context, ch.MQTTReplayRetirementSelectionRequest) (ch.MQTTReplayRetirementSelection, error)
	CommitChannelMQTTReplayRetirement(context.Context, ch.MQTTReplayRetirementRequest) (ch.MQTTReplayRetirementProof, error)
}

type ReplayRetirementOptions struct {
	Retention *ReplayRetention
	Channels  ReplayRetirementChannels
	// MessageIDs uses the app's shared server allocator, never MQTT Packet IDs.
	MessageIDs interface{ Next() uint64 }
	Now        func() time.Time
	// Timeout bounds the complete ordered planning, selection and commit turn.
	Timeout time.Duration
	// PageSize bounds one reverse journal page; default and maximum are 64.
	PageSize int
}

type ReplayRetirementCursor = contract.ReplayRetirementCursor
type ReplayRetirementResult = contract.ReplayRetirementResult

// ReplayRetirement owns ordered consumer permission, not workers or per-source
// state. Its caller serializes turns and retains only successful continuations.
type ReplayRetirement struct{ options ReplayRetirementOptions }

func NewReplayRetirement(o ReplayRetirementOptions) (*ReplayRetirement, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.PageSize == 0 {
		o.PageSize = 64
	}
	if o.Retention == nil || o.Channels == nil || o.MessageIDs == nil || o.Timeout <= 0 || o.Timeout > time.Minute || o.PageSize < 1 || o.PageSize > 64 {
		return nil, ErrInvalid
	}
	return &ReplayRetirement{options: o}, nil
}

// Step captures accepted progress before fresh consumers on EVERY turn. A
// continuation keeps a conservative finite target only while current permission
// still covers it. Lost authority or a lower floor yields for a later cold pass.
func (p *ReplayRetirement) Step(parent context.Context, source meta.MQTTBindingOwner, cursor ReplayRetirementCursor) (ReplayRetirementResult, error) {
	var empty ReplayRetirementResult
	if p == nil || parent == nil {
		return empty, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, p.options.Timeout)
	defer cancel()
	plan, err := p.options.Retention.Plan(ctx, source)
	if err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if !plan.Replay.HasAnchor || plan.Placement.WriteFence.Set() || plan.Through <= plan.Replay.Source.StartAfter {
		return empty, nil
	}
	q, _ := replaySourceRequest(source) // The ordered planner validated this source.
	m := plan.Placement
	q.ExpectedChannelEpoch, q.ExpectedLeaderEpoch, q.ExpectedRouteGeneration = m.Epoch, m.LeaderEpoch, m.RouteGeneration
	authority := ch.MQTTReplayCopyAuthority(m)
	selection := ch.MQTTReplayRetirementSelectionRequest{Source: q, Captured: plan.Replay.Anchor, Through: plan.Through, Limit: p.options.PageSize}
	if cursor != (ReplayRetirementCursor{}) {
		if cursor.Source != source || cursor.Authority != authority || cursor.Through > plan.Through {
			return empty, nil
		}
		// The historical capture must fit the fresh accepted chain, including
		// exact identity when positions coincide. Storage verifies it again.
		capture := selection
		capture.Through = plan.Replay.Anchor.Anchor.Through
		if cursor.BeforeAnchor == 0 || !capture.Accepts(ch.MQTTReplayRetirementSelection{Captured: capture.Captured, Candidate: cursor.Captured, HasCandidate: true, Done: true}) {
			return empty, ErrEvidence
		}
		selection.Captured, selection.Through, selection.BeforeAnchor = cursor.Captured, cursor.Through, cursor.BeforeAnchor
	}
	if !selection.Valid() {
		return empty, ErrEvidence
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	page, err := p.options.Channels.SelectChannelMQTTReplayRetirement(ctx, selection)
	if err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if !selection.Accepts(page) {
		return empty, ErrEvidence
	}
	if !page.Done {
		return ReplayRetirementResult{ContinueScan: true, Next: ReplayRetirementCursor{Source: source, Authority: authority, Captured: selection.Captured, Through: selection.Through, BeforeAnchor: page.BeforeAnchor}}, nil
	}
	if !page.HasCandidate {
		return empty, nil
	}
	stamp := p.options.Now()
	if stamp.IsZero() || stamp.UnixMilli() <= 0 {
		return empty, ErrClock
	}
	request := ch.MQTTReplayRetirementRequest{Meta: m, Captured: selection.Captured, Candidate: page.Candidate, ConsumerThrough: selection.Through, MessageID: p.options.MessageIDs.Next(), ServerTimestampMS: stamp.UnixMilli()}
	if !request.Valid() {
		return empty, ErrEvidence
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	proof, err := p.options.Channels.CommitChannelMQTTReplayRetirement(ctx, request)
	if err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if !request.AcceptsProof(proof) {
		return empty, ErrEvidence
	}
	return ReplayRetirementResult{Committed: true, Proof: proof}, nil
}
