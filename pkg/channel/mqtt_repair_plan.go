package channel

import "github.com/WuKongIM/WuKongIM/pkg/quorumlog"

// MQTTReplayRepairScan bounds replica-local journal selection. AfterAnchor is a
// hint that storage must prove already covered, never an accepted content cursor.
type MQTTReplayRepairScan struct {
	Generation                string
	TargetAnchor, AfterAnchor uint64
	Limit                     int
}

// Valid requires one exact target and an explicit finite journal budget.
func (q MQTTReplayRepairScan) Valid() bool {
	return validMQTTReplayGeneration(q.Generation) && q.TargetAnchor > 0 && q.AfterAnchor <= q.TargetAnchor && q.Limit > 0 && q.Limit <= 64
}

// MQTTReplayRepairPlan has exactly one outcome: a next interval, complete local
// coverage of Target, or an advanced scan cursor. It confers no cluster readiness.
type MQTTReplayRepairPlan struct {
	Current           MQTTReplayPrefix
	Target, Next      MQTTReplayAnchorProof
	HasNext, Complete bool
	ScanAfter         uint64
}

func validRepairAnchor(p MQTTReplayAnchorProof, generation string, start uint64) bool {
	m := p.Manifest
	return p.Anchor.Valid() && m.StructurallyValid() && m.Version == quorumlog.MQTTReplayAnchorProposalManifestVersion && p.Anchor.Through < m.LastOffset && p.Prefix().Generation == generation && p.Anchor.StartAfter == start
}

// ValidFor checks the association and closed outcome shape of a trusted storage
// result; only storage can prove the journals and the covered-prefix meters.
func (p MQTTReplayRepairPlan) ValidFor(q MQTTReplayRepairScan) bool {
	c := p.Current
	if !q.Valid() || c.Generation != q.Generation || c.Through < c.StartAfter || !validRepairAnchor(p.Target, q.Generation, c.StartAfter) || p.Target.Manifest.LastOffset != q.TargetAnchor {
		return false
	}
	if c.Through == c.StartAfter {
		if c.TotalBytes != 0 || c.TotalStoredBytes != 0 || c.Digest != [32]byte{} {
			return false
		}
	} else if c.TotalStoredBytes == 0 || c.TotalBytes > c.TotalStoredBytes || c.Digest == [32]byte{} {
		return false
	}
	target := p.Target.Prefix()
	if p.Complete {
		return !p.HasNext && p.Next == (MQTTReplayAnchorProof{}) && p.ScanAfter == 0 && c.Through >= target.Through && c.TotalBytes >= target.TotalBytes && c.TotalStoredBytes >= target.TotalStoredBytes && (c.Through != target.Through || c == target)
	}
	if c.Through >= target.Through || p.ScanAfter < q.AfterAnchor || p.ScanAfter >= q.TargetAnchor {
		return false
	}
	if !p.HasNext {
		return p.Next == (MQTTReplayAnchorProof{}) && p.ScanAfter > q.AfterAnchor && p.ScanAfter > c.Through
	}
	if !validRepairAnchor(p.Next, q.Generation, c.StartAfter) || p.Next.Manifest.LastOffset <= max(p.ScanAfter, c.Through) || p.Next.Manifest.LastOffset > q.TargetAnchor {
		return false
	}
	next := p.Next.Prefix()
	if next.Through > target.Through || next.TotalBytes > target.TotalBytes || next.TotalStoredBytes > target.TotalStoredBytes ||
		(next.Through == target.Through && next != target) || (p.Next.Manifest.LastOffset == q.TargetAnchor && p.Next != p.Target) {
		return false
	}
	return next.Through > c.Through && next.Through-c.Through <= 256 && next.TotalStoredBytes > c.TotalStoredBytes && next.TotalStoredBytes-c.TotalStoredBytes <= 16<<20 &&
		next.TotalBytes >= c.TotalBytes && next.TotalBytes-c.TotalBytes <= next.TotalStoredBytes-c.TotalStoredBytes
}

// NextRange derives an exact, authenticated interval from local durable progress.
// A scan continuation returns no range; the caller must resume that bounded scan.
func (p MQTTReplayRepairPlan) NextRange() (MQTTReplayRange, bool, error) {
	if !p.ValidFor(MQTTReplayRepairScan{Generation: p.Current.Generation, TargetAnchor: p.Target.Manifest.LastOffset, Limit: 64}) {
		return MQTTReplayRange{}, false, ErrLogConflict
	}
	if !p.HasNext {
		return MQTTReplayRange{}, false, nil
	}
	end := p.Next.Prefix()
	return MQTTReplayRange{Generation: p.Current.Generation, From: p.Current.Through + 1, Through: end.Through, Limit: int(end.Through - p.Current.Through), MaxBytes: int(end.TotalStoredBytes - p.Current.TotalStoredBytes)}, true, nil
}
