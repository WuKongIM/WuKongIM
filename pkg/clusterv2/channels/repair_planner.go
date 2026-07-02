package channels

import (
	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	replicaRepairBlockActiveMigration      = "active_migration"
	replicaRepairBlockNoHealthyReplacement = "no_healthy_replacement"
	replicaRepairBlockMinISRNotMet         = "min_isr_not_met"
)

// ReplicaRepairAction describes the planner output for one degraded channel.
type ReplicaRepairAction uint8

const (
	ReplicaRepairActionNone ReplicaRepairAction = iota
	ReplicaRepairActionCreateReplicaReplace
	ReplicaRepairActionWaitForLeaderFailover
	ReplicaRepairActionBlocked
)

// ReplicaRepairDecision is a bounded replica repair decision.
type ReplicaRepairDecision struct {
	Action      ReplicaRepairAction
	SourceNode  uint64
	TargetNode  uint64
	BlockReason string
	Degraded    bool
	Writable    bool
}

// ReplicaRepairPlanInput contains all evidence needed for follower replica repair.
type ReplicaRepairPlanInput struct {
	// Meta is the authoritative channel runtime metadata row.
	Meta metadb.ChannelRuntimeMeta
	// Nodes is the current control snapshot node list.
	Nodes []control.Node
	// ActiveTask reports that the channel already has an active migration task.
	ActiveTask bool
}

// ReplicaRepairPlanner chooses a safe replacement for an unhealthy non-leader replica.
type ReplicaRepairPlanner struct{}

// NewReplicaRepairPlanner creates a stateless replica repair planner.
func NewReplicaRepairPlanner() ReplicaRepairPlanner {
	return ReplicaRepairPlanner{}
}

// Plan chooses one unhealthy replica and one health-schedulable replacement.
func (p ReplicaRepairPlanner) Plan(input ReplicaRepairPlanInput) ReplicaRepairDecision {
	meta := metadb.NormalizeChannelRuntimeMeta(input.Meta)
	if meta.ChannelID == "" || meta.ChannelType < 0 || meta.Status != uint8(ch.StatusActive) {
		return ReplicaRepairDecision{}
	}
	healthy := failoverHealthyNodeSet(input.Nodes)
	replicas := failoverNodeSet(meta.Replicas)
	isr := failoverNodeSet(meta.ISR)
	healthyISR := countHealthyISR(meta.ISR, healthy)
	degraded := len(meta.ISR) < len(meta.Replicas)
	writable := meta.MinISR <= 0 || int64(healthyISR) >= meta.MinISR
	source := firstUnhealthyReplica(meta.Replicas, healthy)
	if source != 0 {
		degraded = true
	}
	decision := ReplicaRepairDecision{
		SourceNode: source,
		Degraded:   degraded,
		Writable:   writable,
	}
	if input.ActiveTask {
		decision.Action = ReplicaRepairActionBlocked
		decision.BlockReason = replicaRepairBlockActiveMigration
		return decision
	}
	if source == 0 {
		return decision
	}
	if source == meta.Leader {
		decision.Action = ReplicaRepairActionWaitForLeaderFailover
		return decision
	}
	if !writable {
		decision.Action = ReplicaRepairActionBlocked
		decision.BlockReason = replicaRepairBlockMinISRNotMet
		return decision
	}
	target, ok := chooseReplicaRepairTarget(meta, input.Nodes, replicas, isr)
	if !ok {
		decision.Action = ReplicaRepairActionBlocked
		decision.BlockReason = replicaRepairBlockNoHealthyReplacement
		return decision
	}
	decision.Action = ReplicaRepairActionCreateReplicaReplace
	decision.TargetNode = target
	return decision
}

func countHealthyISR(isr []uint64, healthy map[uint64]bool) int {
	count := 0
	for _, nodeID := range isr {
		if healthy[nodeID] {
			count++
		}
	}
	return count
}

func firstUnhealthyReplica(replicas []uint64, healthy map[uint64]bool) uint64 {
	for _, nodeID := range replicas {
		if nodeID != 0 && !healthy[nodeID] {
			return nodeID
		}
	}
	return 0
}

func chooseReplicaRepairTarget(meta metadb.ChannelRuntimeMeta, nodes []control.Node, replicas map[uint64]bool, isr map[uint64]bool) (uint64, bool) {
	candidates := make([]uint64, 0, len(nodes))
	for _, node := range nodes {
		if !control.NodeSchedulableForPlacement(node) || replicas[node.NodeID] || isr[node.NodeID] {
			continue
		}
		candidates = append(candidates, node.NodeID)
	}
	selected, err := selectChannelReplicas(string(ch.ChannelKeyForID(ch.ChannelID{ID: meta.ChannelID, Type: uint8(meta.ChannelType)})), candidates, 1)
	if err != nil || len(selected) == 0 {
		return 0, false
	}
	return selected[0], true
}
