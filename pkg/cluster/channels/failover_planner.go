package channels

import (
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	failoverBlockActiveMigration = "active_migration"
	failoverBlockNoSafeCandidate = "no_safe_candidate"
	failoverBlockTargetLagging   = "target_lagging"
)

// FailoverAction describes the planner output for one dead-leader channel.
type FailoverAction uint8

const (
	FailoverActionNone FailoverAction = iota
	FailoverActionCreateLeaderTransfer
	FailoverActionBlocked
)

// FailoverDecision is a bounded dead-leader recovery decision.
type FailoverDecision struct {
	Action        FailoverAction
	TargetNode    uint64
	BlockReason   string
	ObservedHW    uint64
	ObservedEpoch uint64
}

// FailoverCandidateProbe is the runtime proof observed for one candidate node.
type FailoverCandidateProbe struct {
	// NodeID is the candidate node that produced Probe.
	NodeID uint64
	// Probe contains the candidate's loaded Channel runtime proof.
	Probe ch.RuntimeProbeChannel
	// PendingTruncationBelowSafePrefix blocks candidates with unsafe local truncation.
	PendingTruncationBelowSafePrefix bool
}

// FailoverPlanInput contains all evidence required to plan one dead-leader failover.
type FailoverPlanInput struct {
	// Meta is the authoritative channel runtime metadata row.
	Meta metadb.ChannelRuntimeMeta
	// Nodes is the current control snapshot node list.
	Nodes []control.Node
	// Probes are runtime proofs from candidate replicas.
	Probes []FailoverCandidateProbe
	// RequiredHW is the safe prefix the new leader must prove. Zero accepts any HW.
	RequiredHW uint64
	// ActiveTask reports that the channel already has an active migration task.
	ActiveTask bool
	// LeaderSuspect documents the caller's dead-leader predicate for diagnostics.
	LeaderSuspect bool
}

// FailoverPlanner chooses a safe ISR target for dead-leader recovery.
type FailoverPlanner struct{}

// NewFailoverPlanner creates a stateless dead-leader failover planner.
func NewFailoverPlanner() FailoverPlanner {
	return FailoverPlanner{}
}

// Plan chooses the healthy ISR candidate with the highest compatible HW.
func (p FailoverPlanner) Plan(input FailoverPlanInput) FailoverDecision {
	if input.ActiveTask {
		return FailoverDecision{Action: FailoverActionBlocked, BlockReason: failoverBlockActiveMigration}
	}
	meta := metadb.NormalizeChannelRuntimeMeta(input.Meta)
	healthy := failoverHealthyNodeSet(input.Nodes)
	isr := failoverNodeSet(meta.ISR)
	var best FailoverCandidateProbe
	var haveBest bool
	var highestObservedHW uint64
	var highestObservedEpoch uint64
	for _, candidate := range input.Probes {
		if candidate.NodeID == 0 || candidate.NodeID == meta.Leader {
			continue
		}
		if !healthy[candidate.NodeID] || !isr[candidate.NodeID] {
			continue
		}
		probe := candidate.Probe
		if !failoverProbeMatchesMeta(probe, meta) {
			continue
		}
		if candidate.PendingTruncationBelowSafePrefix {
			continue
		}
		if probe.HW > highestObservedHW {
			highestObservedHW = probe.HW
			highestObservedEpoch = probe.LeaderEpoch
		}
		if input.RequiredHW > 0 && probe.HW < input.RequiredHW {
			continue
		}
		if !haveBest || probe.HW > best.Probe.HW || (probe.HW == best.Probe.HW && probe.CheckpointHW > best.Probe.CheckpointHW) {
			best = candidate
			haveBest = true
		}
	}
	if !haveBest {
		reason := failoverBlockNoSafeCandidate
		if input.RequiredHW > 0 && highestObservedHW > 0 && highestObservedHW < input.RequiredHW {
			reason = failoverBlockTargetLagging
		}
		return FailoverDecision{
			Action:        FailoverActionBlocked,
			BlockReason:   reason,
			ObservedHW:    highestObservedHW,
			ObservedEpoch: highestObservedEpoch,
		}
	}
	return FailoverDecision{
		Action:        FailoverActionCreateLeaderTransfer,
		TargetNode:    best.NodeID,
		ObservedHW:    best.Probe.HW,
		ObservedEpoch: best.Probe.LeaderEpoch,
	}
}

func failoverHealthyNodeSet(nodes []control.Node) map[uint64]bool {
	out := make(map[uint64]bool, len(nodes))
	for _, node := range nodes {
		if control.NodeSchedulableForPlacement(node) {
			out[node.NodeID] = true
		}
	}
	return out
}

func failoverNodeSet(nodes []uint64) map[uint64]bool {
	out := make(map[uint64]bool, len(nodes))
	for _, node := range nodes {
		if node != 0 {
			out[node] = true
		}
	}
	return out
}

func failoverProbeMatchesMeta(probe ch.RuntimeProbeChannel, meta metadb.ChannelRuntimeMeta) bool {
	if probe.ChannelID.ID != meta.ChannelID ||
		int64(probe.ChannelID.Type) != meta.ChannelType ||
		probe.ChannelEpoch != meta.ChannelEpoch ||
		probe.Status != ch.StatusActive {
		return false
	}
	if probe.LeaderEpoch == meta.LeaderEpoch {
		return true
	}
	return meta.LeaderEpoch > 0 && probe.LeaderEpoch+1 == meta.LeaderEpoch
}
