package mqttsession

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ReplayMetadata resolves exact placement after a fresh Slot quorum/apply barrier.
type ReplayMetadata interface {
	ResolveChannelMetaFresh(context.Context, ch.ChannelID) (ch.Meta, error)
}

// ReplayChannels provides independently fenced cluster operations. Planning reads
// accepted progress; explicit complete recovery can release original sources,
// while shared-content reclamation remains a separate consumer-proof protocol.
type ReplayChannels interface {
	PlanChannelMQTTReplay(context.Context, ch.MQTTReplayPlanRequest) (ch.MQTTReplayPlan, error)
	CopyChannelMQTTReplay(context.Context, ch.MQTTReplayRequest) (ch.MQTTReplayCopyReceipt, error)
	CommitChannelMQTTReplayAnchor(context.Context, ch.MQTTReplayAnchorRequest) (ch.MQTTReplayAnchorProof, error)
	StepChannelMQTTReplayRecovery(context.Context, ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error)
}

type ReplayCoordinatorOptions struct {
	Metadata ReplayMetadata
	Channels ReplayChannels
	// MessageIDs supplies server control identities from the app's shared allocator.
	MessageIDs interface{ Next() uint64 }
	Now        func() time.Time
	// Timeout bounds the complete turn, including all authority and content calls.
	Timeout time.Duration
	// PageSize and MaxBytes bound one copy to at most 256 rows and 16 MiB.
	PageSize, MaxBytes int
}

// Shared body-free scheduling DTOs avoid a runtime-to-usecase dependency.
type ReplayTargetCursor = contract.ReplayTargetCursor
type ReplayCursor = contract.ReplayCursor
type ReplayStepResult = contract.ReplayStepResult

// ReplayCoordinator owns no workers, payloads or mutable per-source state.
type ReplayCoordinator struct{ options ReplayCoordinatorOptions }

func NewReplayCoordinator(o ReplayCoordinatorOptions) (*ReplayCoordinator, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.PageSize == 0 {
		o.PageSize = 256
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = 16 << 20
	}
	if o.Metadata == nil || o.Channels == nil || o.MessageIDs == nil || o.Timeout <= 0 || o.Timeout > time.Minute || o.PageSize < 1 || o.PageSize > 256 || o.MaxBytes < 1 || o.MaxBytes > 16<<20 {
		return nil, ErrInvalid
	}
	return &ReplayCoordinator{options: o}, nil
}

// Step alternates one copy/anchor admission with one exact replica recovery.
// Every effect uses a bounded context and authoritative storage progress; errors
// preserve detached scheduling hints without claiming an unconfirmed result.
func (c *ReplayCoordinator) Step(parent context.Context, source meta.MQTTBindingOwner, cursor ReplayCursor) (out ReplayStepResult, err error) {
	if c == nil || parent == nil || len(cursor.Targets) > 256 {
		return out, ErrInvalid
	}
	out.Next = cursor
	out.Next.Targets = slices.Clone(cursor.Targets)
	q, ok := replaySourceRequest(source)
	if !ok {
		return out, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, c.options.Timeout)
	defer cancel()
	if err = ctx.Err(); err != nil {
		return out, err
	}
	m, err := c.options.Metadata.ResolveChannelMetaFresh(ctx, q.ChannelID)
	if err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if !validReplayPlacement(m, q.ChannelID) {
		return out, ErrEvidence
	}
	q.ExpectedChannelEpoch, q.ExpectedLeaderEpoch, q.ExpectedRouteGeneration = m.Epoch, m.LeaderEpoch, m.RouteGeneration
	plan, err := c.options.Channels.PlanChannelMQTTReplay(ctx, q)
	if err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if !plan.ValidFor(q) {
		return out, ErrEvidence
	}
	if err = bindReplayCursor(&out.Next, source, m, plan); err != nil {
		return out, err
	}
	// Migration may wait for shared content while business admission is fenced.
	// Recover only already committed anchors; no fence can authorize new copying
	// or anchor controls, including when this source has not accepted one yet.
	if m.WriteFence.Set() {
		if plan.HasAnchor {
			return c.recover(ctx, q, plan, out)
		}
		return out, nil
	}
	rangeToCopy, hasCopy, err := plan.NextRange(c.options.PageSize, c.options.MaxBytes)
	if err != nil {
		return out, err
	}
	if plan.HasAnchor && (out.Next.RepairNext || !hasCopy) {
		return c.recover(ctx, q, plan, out)
	}
	if !hasCopy {
		return out, nil
	}
	out.Next.RepairNext = true
	request := ch.MQTTReplayRequest{ChannelID: q.ChannelID, ExpectedChannelEpoch: q.ExpectedChannelEpoch, ExpectedLeaderEpoch: q.ExpectedLeaderEpoch, ExpectedRouteGeneration: q.ExpectedRouteGeneration, Range: rangeToCopy}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	copy, err := c.options.Channels.CopyChannelMQTTReplay(ctx, request)
	if err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	before := ch.MQTTReplayPrefix{Generation: source.Generation, StartAfter: plan.Source.StartAfter, Through: plan.Source.StartAfter}
	if plan.HasAnchor {
		before = plan.Anchor.Prefix()
	}
	r := copy.Request.Range
	if !copy.ValidFor(m) || copy.Before != before || r.Generation != rangeToCopy.Generation || r.From != rangeToCopy.From || r.Through > rangeToCopy.Through || r.Limit > rangeToCopy.Limit || r.MaxBytes > rangeToCopy.MaxBytes {
		return out, ErrEvidence
	}
	stamp := c.options.Now()
	if stamp.IsZero() || stamp.UnixMilli() <= 0 {
		return out, ErrClock
	}
	messageID := c.options.MessageIDs.Next()
	if messageID == 0 {
		return out, ErrEvidence
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	proof, err := c.options.Channels.CommitChannelMQTTReplayAnchor(ctx, ch.MQTTReplayAnchorRequest{Meta: m, Copy: copy, MessageID: messageID, ServerTimestampMS: stamp.UnixMilli()})
	if err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	// The control may follow intervening appends. Validate its historical authority
	// and full prefix association without treating the earlier captured HW as current.
	accepted := ch.MQTTReplayPlan{Source: plan.Source, Anchor: proof, HasAnchor: true}
	accepted.Source.CommittedThrough = max(accepted.Source.CommittedThrough, proof.Manifest.LastOffset)
	prefix := proof.Prefix()
	idle := prefix == copy.Before && prefix.Through+1 == proof.Manifest.LastOffset && proof.Manifest.LastOffset == copy.After.Through
	if !accepted.ValidFor(q) || (prefix != copy.After && !idle) {
		return out, ErrEvidence
	}
	out.Anchored = true
	return out, nil
}

// recover rotates before the RPC so a failed target cannot monopolize the source.
// A successful import clears scan hints but still requires another coverage read.
func (c *ReplayCoordinator) recover(ctx context.Context, source ch.MQTTReplayPlanRequest, plan ch.MQTTReplayPlan, out ReplayStepResult) (ReplayStepResult, error) {
	i := out.Next.NextTarget
	target := &out.Next.Targets[i]
	if target.AnchorPosition == 0 {
		target.AnchorPosition = plan.Anchor.Manifest.LastOffset
	}
	out.Target = target.NodeID
	out.Next.NextTarget = (i + 1) % len(out.Next.Targets)
	out.Next.RepairNext = false
	q := ch.MQTTReplayRecoveryRequest{Target: target.NodeID, Source: source, TargetAnchor: target.AnchorPosition, AfterAnchor: target.AfterAnchor, DonorAfter: target.DonorAfter, ScanLimit: 64, ReleaseSource: true, ApplyRetirement: true}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	r, err := c.options.Channels.StepChannelMQTTReplayRecovery(ctx, q)
	if err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if !r.ValidFor(q) || (r.DonorAfter != 0 && !slices.ContainsFunc(out.Next.Targets, func(t ReplayTargetCursor) bool { return t.NodeID == r.DonorAfter })) {
		return out, ErrEvidence
	}
	switch {
	case r.Plan.Complete && r.RetirementPending:
		// Cleanup has a durable store cursor. Yield this worker visit instead of
		// pretending to advance a journal scan; a later pass resumes bounded work.
		target.AfterAnchor, target.DonorAfter = 0, 0
	case r.Plan.Complete:
		out.TargetComplete = true
		*target = ReplayTargetCursor{NodeID: target.NodeID}
	case r.Repaired:
		out.Repaired = true
		target.AfterAnchor, target.DonorAfter = 0, 0
	default:
		out.ContinueScan = !r.Plan.HasNext
		target.AfterAnchor, target.DonorAfter = r.Plan.ScanAfter, r.DonorAfter
	}
	return out, nil
}

func replaySourceRequest(source meta.MQTTBindingOwner) (ch.MQTTReplayPlanRequest, bool) {
	if source.Kind != meta.MQTTBindingChannel || len(source.ID) > 1028 {
		return ch.MQTTReplayPlanRequest{}, false
	}
	kind, id, ok := strings.Cut(source.ID, ":")
	n, err := strconv.ParseUint(kind, 10, 8)
	if !ok || err != nil || n == 0 || strconv.FormatUint(n, 10) != kind {
		return ch.MQTTReplayPlanRequest{}, false
	}
	// Unit fences validate the complete source before making any authority call.
	q := ch.MQTTReplayPlanRequest{ChannelID: ch.ChannelID{ID: id, Type: uint8(n)}, Generation: source.Generation, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1}
	return q, q.Valid()
}

func validReplayPlacement(m ch.Meta, id ch.ChannelID) bool {
	if m.ID != id || (m.Key != "" && m.Key != ch.ChannelKeyForID(id)) || m.Epoch == 0 || m.LeaderEpoch == 0 || m.RouteGeneration == 0 || (m.Status != ch.StatusActive && m.Status != ch.StatusCreating) ||
		len(m.Replicas) == 0 || len(m.Replicas) > 256 || len(m.ISR) == 0 || len(m.ISR) > 256 || m.MinISR < 1 || m.MinISR > len(m.ISR) || m.MinISR*2 <= len(m.ISR) {
		return false
	}
	members := make(map[ch.NodeID]bool, len(m.Replicas))
	for _, n := range m.Replicas {
		if n == 0 {
			return false
		}
		if _, exists := members[n]; exists {
			return false
		}
		members[n] = false
	}
	for _, n := range m.ISR {
		if voter, exists := members[n]; !exists || voter {
			return false
		}
		members[n] = true
	}
	return members[m.Leader]
}

// bindReplayCursor discards hints only when source or complete authority changes.
// Same-authority malformed continuations fail closed instead of skipping work.
func bindReplayCursor(c *ReplayCursor, source meta.MQTTBindingOwner, m ch.Meta, plan ch.MQTTReplayPlan) error {
	authority := ch.MQTTReplayCopyAuthority(m)
	if c.Source != source || c.Authority != authority {
		pass, count := c.Pass, uint64(len(m.Replicas))
		*c = ReplayCursor{Pass: pass, Source: source, Authority: authority, Targets: make([]ReplayTargetCursor, len(m.Replicas)), NextTarget: int((pass / 2) % count), RepairNext: pass%2 == 1}
		for i, n := range m.Replicas {
			c.Targets[i].NodeID = n
		}
		// Cold visits must eventually reach donors beyond the first bounded round.
		// Only the current plan supplies the target; the pass supplies no proof.
		if cycle := pass / (2 * count); cycle > 0 && plan.HasAnchor {
			donor := m.Replicas[(cycle-1)%count]
			target := &c.Targets[c.NextTarget]
			if donor != target.NodeID {
				target.AnchorPosition, target.DonorAfter = plan.Anchor.Manifest.LastOffset, donor
			}
		}
		return nil
	}
	if len(c.Targets) != len(m.Replicas) || c.NextTarget < 0 || c.NextTarget >= len(c.Targets) {
		return ErrEvidence
	}
	latest := plan.Anchor.Manifest.LastOffset
	for i, target := range c.Targets {
		if target.NodeID != m.Replicas[i] || target.AnchorPosition > latest || target.AfterAnchor > target.AnchorPosition ||
			(target.AnchorPosition == 0 && target.DonorAfter != 0) || (target.DonorAfter != 0 && (target.DonorAfter == target.NodeID || !slices.Contains(m.Replicas, target.DonorAfter))) {
			return ErrEvidence
		}
	}
	return nil
}
