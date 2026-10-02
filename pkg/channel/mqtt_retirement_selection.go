package channel

// MQTTReplayRetirementScan binds a reverse journal scan to the anchor captured
// before the consumer read. Through is that read's capped floor, never a new HW.
type MQTTReplayRetirementScan struct {
	Generation                            string
	CapturedAnchor, Through, BeforeAnchor uint64
	Limit                                 int
}

func (q MQTTReplayRetirementScan) Valid() bool {
	return validMQTTReplayGeneration(q.Generation) && q.CapturedAnchor > 0 && q.Through < q.CapturedAnchor && q.BeforeAnchor <= q.CapturedAnchor && q.Limit > 0 && q.Limit <= 64
}

// MQTTReplayRetirementSelection contains one eligible whole anchor, exhaustion,
// or a backward continuation. It does not prove consumer or current authority.
type MQTTReplayRetirementSelection struct {
	Captured, Candidate MQTTReplayAnchorProof
	HasCandidate, Done  bool
	BeforeAnchor        uint64
}

// ValidFor rejects mixed outcomes, upward rounding, changed captures and cursor
// regression. Storage separately verifies committed journals in its pinned view.
func (p MQTTReplayRetirementSelection) ValidFor(q MQTTReplayRetirementScan) bool {
	c := p.Captured
	if !q.Valid() || !validRepairAnchor(c, q.Generation, c.Anchor.StartAfter) || c.Manifest.LastOffset != q.CapturedAnchor || c.Anchor.Through < q.Through {
		return false
	}
	if !p.Done {
		return !p.HasCandidate && p.Candidate == (MQTTReplayAnchorProof{}) && p.BeforeAnchor > 0 && p.BeforeAnchor <= q.CapturedAnchor && (q.BeforeAnchor == 0 || p.BeforeAnchor < q.BeforeAnchor)
	}
	if p.BeforeAnchor != 0 {
		return false
	}
	if !p.HasCandidate {
		return p.Candidate == (MQTTReplayAnchorProof{})
	}
	a, m := p.Candidate.Anchor, p.Candidate.Manifest
	return validRepairAnchor(p.Candidate, q.Generation, c.Anchor.StartAfter) && m.LastOffset <= q.CapturedAnchor && (q.BeforeAnchor == 0 || m.LastOffset < q.BeforeAnchor) &&
		a.Through <= q.Through && a.TotalBytes <= c.Anchor.TotalBytes && a.TotalStoredBytes <= c.Anchor.TotalStoredBytes &&
		(a.Through != c.Anchor.Through || a == c.Anchor) && (m.LastOffset != q.CapturedAnchor || p.Candidate == c)
}
