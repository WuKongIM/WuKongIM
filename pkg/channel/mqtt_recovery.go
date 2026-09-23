package channel

import "context"

// MQTTReplayRecoveryStepper performs one bounded receiver-owned recovery step.
// Completion covers the requested anchor and optionally releases its original
// source prefix, never cluster readiness or shared-content GC.
type MQTTReplayRecoveryStepper interface {
	StepMQTTReplayRecovery(context.Context, MQTTReplayRecoveryRequest) (MQTTReplayRecoveryResult, error)
}

// MQTTReplayRecoveryRequest binds a replica, source, authority and exact target.
// AfterAnchor and DonorAfter are resumable hints, not durable content evidence.
type MQTTReplayRecoveryRequest struct {
	Target                    NodeID
	Source                    MQTTReplayPlanRequest
	TargetAnchor, AfterAnchor uint64
	DonorAfter                NodeID
	// ScanLimit explicitly bounds local journal work to at most 64 entries.
	ScanLimit int
	// ReleaseSource explicitly requests source release after complete coverage.
	// False preserves ordinary recovery without a cleanup-watermark mutation.
	ReleaseSource bool
}

func (q MQTTReplayRecoveryRequest) repairScan() MQTTReplayRepairScan {
	return MQTTReplayRepairScan{Generation: q.Source.Generation, TargetAnchor: q.TargetAnchor, AfterAnchor: q.AfterAnchor, Limit: q.ScanLimit}
}

// Valid rejects implicit placement, authority, source and work budgets.
func (q MQTTReplayRecoveryRequest) Valid() bool {
	return q.Target != 0 && q.Source.Valid() && q.repairScan().Valid()
}

// AcceptsPlan checks structural association and historical proof authority. The
// replica's store must independently prove journals, meters and durable content.
func (q MQTTReplayRecoveryRequest) AcceptsPlan(p MQTTReplayRepairPlan) bool {
	if !q.Valid() || !p.ValidFor(q.repairScan()) {
		return false
	}
	proofs := []MQTTReplayAnchorProof{p.Target}
	if p.HasNext {
		proofs = append(proofs, p.Next)
	}
	for _, p := range proofs {
		m := p.Manifest
		for _, pair := range [][2]uint64{{m.ChannelEpoch, q.Source.ExpectedChannelEpoch}, {m.LeaderTerm, q.Source.ExpectedLeaderEpoch}, {m.FenceVersion, q.Source.ExpectedRouteGeneration}} {
			if pair[0] < pair[1] {
				break
			}
			if pair[0] > pair[1] {
				return false
			}
		}
	}
	return true
}

// MQTTReplayRecoveryResult preserves the pre-import plan. Repaired acknowledges
// one interval; only a subsequent planning read may report target completion.
type MQTTReplayRecoveryResult struct {
	Plan     MQTTReplayRepairPlan
	Repaired bool
	// SourceReleased confirms the requested original-source prefix is released.
	// It is valid only on requested, complete recovery; it grants no shared GC.
	SourceReleased bool
	// DonorAfter advances a failed bounded round; it must name a current donor.
	DonorAfter NodeID
}

// ValidFor closes the four outcomes: complete, scan, imported interval, or retry.
// Placement freshness of a retry donor is checked by the cluster service.
func (p MQTTReplayRecoveryResult) ValidFor(q MQTTReplayRecoveryRequest) bool {
	if !q.AcceptsPlan(p.Plan) {
		return false
	}
	if p.SourceReleased != (q.ReleaseSource && p.Plan.Complete) {
		return false
	}
	if !p.Plan.HasNext {
		return !p.Repaired && p.DonorAfter == 0
	}
	if p.Repaired {
		return p.DonorAfter == 0
	}
	return p.DonorAfter != 0 && p.DonorAfter != q.Target
}
