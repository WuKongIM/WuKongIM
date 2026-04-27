package management

import "context"

// NodeScaleInStatus describes the current manager-driven scale-in state.
type NodeScaleInStatus string

const (
	// NodeScaleInStatusBlocked means one or more safety checks currently block scale-in.
	NodeScaleInStatusBlocked NodeScaleInStatus = "blocked"
)

// NodeScaleInPlanRequest carries operator confirmation needed for scale-in safety checks.
type NodeScaleInPlanRequest struct {
	// ConfirmStatefulSetTail confirms the operator has verified the target is the Kubernetes StatefulSet tail.
	ConfirmStatefulSetTail bool
	// ExpectedTailNodeID is the node ID the operator expects Kubernetes scale-down to remove.
	ExpectedTailNodeID uint64
}

// NodeScaleInReport contains safety checks, progress counters, and the current scale-in status.
type NodeScaleInReport struct {
	// NodeID is the target node being evaluated for scale-in.
	NodeID uint64
	// Status is the current manager-computed scale-in status.
	Status NodeScaleInStatus
	// SafeToRemove is true only when the node is ready for external removal.
	SafeToRemove bool
	// ConnectionSafetyVerified reports whether runtime connection counters were known.
	ConnectionSafetyVerified bool
	// Checks contains boolean safety check results.
	Checks NodeScaleInChecks
	// Progress contains scale-in progress counters.
	Progress NodeScaleInProgress
	// BlockedReasons lists blocking safety reasons.
	BlockedReasons []NodeScaleInBlockedReason
}

// NodeScaleInChecks records individual scale-in safety checks.
type NodeScaleInChecks struct {
	// SlotReplicaCountKnown reports whether configured SlotReplicaN is available to the usecase.
	SlotReplicaCountKnown bool
}

// NodeScaleInProgress records counters used to explain scale-in progress.
type NodeScaleInProgress struct{}

// NodeScaleInBlockedReason describes one safety condition blocking scale-in.
type NodeScaleInBlockedReason struct {
	// Code is the stable machine-readable reason code.
	Code string
	// Message is a human-readable explanation.
	Message string
	// Count is an optional number related to the blocked condition.
	Count int
	// SlotID is the optional slot related to the blocked condition.
	SlotID uint32
	// NodeID is the optional node related to the blocked condition.
	NodeID uint64
}

// PlanNodeScaleIn returns a fail-closed scale-in report for the target node.
func (a *App) PlanNodeScaleIn(_ context.Context, nodeID uint64, _ NodeScaleInPlanRequest) (NodeScaleInReport, error) {
	report := NodeScaleInReport{NodeID: nodeID, Status: NodeScaleInStatusBlocked}
	if a == nil || a.slotReplicaN <= 0 {
		report.BlockedReasons = append(report.BlockedReasons, NodeScaleInBlockedReason{
			Code:    "slot_replica_count_unknown",
			Message: "slot replica count is not configured",
		})
		return report, nil
	}
	report.Checks.SlotReplicaCountKnown = true
	return report, nil
}
