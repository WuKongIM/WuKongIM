package channel

import "context"

// MQTTReplayRetirementSelector reads one bounded page under fresh Channel
// authority. The caller owns consumer read ordering and continuation lifetime.
type MQTTReplayRetirementSelector interface {
	SelectMQTTReplayRetirement(context.Context, MQTTReplayRetirementSelectionRequest) (MQTTReplayRetirementSelection, error)
}

// MQTTReplayRetirementSelectionRequest preserves the immutable capture and
// capped consumer floor across a finite reverse scan. It grants no write permit.
type MQTTReplayRetirementSelectionRequest struct {
	Source                MQTTReplayPlanRequest
	Captured              MQTTReplayAnchorProof
	Through, BeforeAnchor uint64
	Limit                 int
}

func (q MQTTReplayRetirementSelectionRequest) Scan() MQTTReplayRetirementScan {
	return MQTTReplayRetirementScan{Generation: q.Source.Generation, CapturedAnchor: q.Captured.Manifest.LastOffset, Through: q.Through, BeforeAnchor: q.BeforeAnchor, Limit: q.Limit}
}

func (q MQTTReplayRetirementSelectionRequest) Valid() bool {
	p := MQTTReplayPlan{Source: MQTTSourceSnapshot{Generation: q.Source.Generation, StartAfter: q.Captured.Anchor.StartAfter, CommittedThrough: q.Captured.Manifest.LastOffset}, Anchor: q.Captured, HasAnchor: true}
	return q.Scan().Valid() && q.Through <= q.Captured.Anchor.Through && p.ValidFor(q.Source)
}

// Accepts checks association only; the store independently proves its journals.
func (q MQTTReplayRetirementSelectionRequest) Accepts(p MQTTReplayRetirementSelection) bool {
	if !q.Valid() || p.Captured != q.Captured || !p.ValidFor(q.Scan()) {
		return false
	}
	if !p.HasCandidate {
		return true
	}
	plan := MQTTReplayPlan{Source: MQTTSourceSnapshot{Generation: q.Source.Generation, StartAfter: q.Captured.Anchor.StartAfter, CommittedThrough: q.Captured.Manifest.LastOffset}, Anchor: p.Candidate, HasAnchor: true}
	return plan.ValidFor(q.Source)
}
