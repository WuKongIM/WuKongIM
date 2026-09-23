package channel

import (
	"context"
	"slices"

	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// MQTTReplayRetirementCommitter admits a trusted, ordered consumer-floor
// selection through the owning sequencer. Product orchestration must capture
// the anchor before reading consumers and establish fresh authority around it.
// This port cannot derive consumer permission from its request or local storage.
type MQTTReplayRetirementCommitter interface {
	CommitMQTTReplayRetirement(context.Context, MQTTReplayRetirementRequest) (MQTTReplayRetirementProof, error)
}

// MQTTReplayRetirementRequest binds a whole historical selection to current
// placement and a server identity. The owner independently reloads both anchors.
type MQTTReplayRetirementRequest struct {
	Meta                Meta
	Captured, Candidate MQTTReplayAnchorProof
	// ConsumerThrough is the capped floor read after Captured was fixed; unknown
	// obligations cannot authorize a candidate. It is never a caller-selected HW.
	ConsumerThrough   uint64
	MessageID         uint64
	ServerTimestampMS int64
}

// Valid checks bounded placement, historical proof association and downward
// whole-anchor rounding; it does not certify the caller's consumer read ordering.
func (q MQTTReplayRetirementRequest) Valid() bool {
	m := q.Meta
	gen := q.Captured.Prefix().Generation
	request := MQTTReplayPlanRequest{ChannelID: m.ID, ExpectedChannelEpoch: m.Epoch, ExpectedLeaderEpoch: m.LeaderEpoch, ExpectedRouteGeneration: m.RouteGeneration, Generation: gen}
	if !request.Valid() || q.MessageID == 0 || q.ServerTimestampMS <= 0 || (m.Key != "" && m.Key != ChannelKeyForID(m.ID)) ||
		(m.Status != StatusActive && m.Status != StatusCreating) || len(m.Replicas) == 0 || len(m.Replicas) > 256 || len(m.ISR) == 0 || len(m.ISR) > 256 || m.MinISR < 1 || m.MinISR > len(m.ISR) || m.MinISR*2 <= len(m.ISR) {
		return false
	}
	peers := make(map[NodeID]bool, len(m.Replicas))
	for _, n := range m.Replicas {
		if n == 0 {
			return false
		}
		if _, exists := peers[n]; exists {
			return false
		}
		peers[n] = false
	}
	for _, n := range m.ISR {
		voter, exists := peers[n]
		if !exists || voter {
			return false
		}
		peers[n] = true
	}
	if !peers[m.Leader] {
		return false
	}
	scan := MQTTReplayRetirementScan{Generation: gen, CapturedAnchor: q.Captured.Manifest.LastOffset, Through: q.ConsumerThrough, Limit: 1}
	selection := MQTTReplayRetirementSelection{Captured: q.Captured, Candidate: q.Candidate, HasCandidate: true, Done: true}
	if !selection.ValidFor(scan) {
		return false
	}
	for _, proof := range []MQTTReplayAnchorProof{q.Captured, q.Candidate} {
		plan := MQTTReplayPlan{Source: MQTTSourceSnapshot{Generation: gen, StartAfter: q.Captured.Anchor.StartAfter, CommittedThrough: q.Captured.Manifest.LastOffset}, Anchor: proof, HasAnchor: true}
		if !plan.ValidFor(request) {
			return false
		}
	}
	return true
}

// Clone owns every placement slice retained by asynchronous admission.
func (q MQTTReplayRetirementRequest) Clone() MQTTReplayRetirementRequest {
	q.Meta.Replicas = slices.Clone(q.Meta.Replicas)
	q.Meta.ISR = slices.Clone(q.Meta.ISR)
	return q
}

// Retirement constructs the canonical payload; callers cannot inject a different
// anchor reference or encode business bytes as this internal control.
func (q MQTTReplayRetirementRequest) Retirement() (quorumlog.MQTTReplayRetirement, error) {
	if !q.Valid() {
		return quorumlog.MQTTReplayRetirement{}, ErrInvalidConfig
	}
	p := q.Candidate
	return quorumlog.MQTTReplayRetirement{Anchor: p.Anchor, AnchorPosition: p.Manifest.LastOffset, AnchorDigest: p.Manifest.Digest}, nil
}

// AcceptsProof allows the selected decision or a later already-committed one.
// Equal prefixes require the exact reference; this check does not prove durability.
func (q MQTTReplayRetirementRequest) AcceptsProof(p MQTTReplayRetirementProof) bool {
	want, err := q.Retirement()
	if err != nil || !p.Retirement.Valid() || !p.Manifest.StructurallyValid() || p.Manifest.Version != quorumlog.MQTTReplayRetirementProposalManifestVersion || p.Retirement.AnchorPosition >= p.Manifest.LastOffset {
		return false
	}
	a, b := p.Retirement.Anchor, want.Anchor
	if a.SourceCommand != b.SourceCommand || a.StartAfter != b.StartAfter || a.Through < b.Through || a.TotalBytes < b.TotalBytes || a.TotalStoredBytes < b.TotalStoredBytes ||
		(a.Through == b.Through && p.Retirement != want) || (a.Through > b.Through && a.TotalStoredBytes <= b.TotalStoredBytes) {
		return false
	}
	for _, pair := range [][2]uint64{{p.Manifest.ChannelEpoch, q.Meta.Epoch}, {p.Manifest.LeaderTerm, q.Meta.LeaderEpoch}, {p.Manifest.FenceVersion, q.Meta.RouteGeneration}} {
		if pair[0] < pair[1] {
			return true
		}
		if pair[0] > pair[1] {
			return false
		}
	}
	return true
}
