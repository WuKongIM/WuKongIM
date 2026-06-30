package management

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	// ErrNodeScaleInUnavailable reports that scale-in dependencies are unavailable.
	ErrNodeScaleInUnavailable = errors.New("internalv2/usecase/management: node scale-in unavailable")
	// ErrNodeScaleInConflict reports a concurrent control-state change during scale-in writes.
	ErrNodeScaleInConflict = errors.New("internalv2/usecase/management: node scale-in conflict")
	// ErrNodeScaleInUnsafe reports that final removal is blocked by scale-in safety status.
	ErrNodeScaleInUnsafe = errors.New("internalv2/usecase/management: node scale-in unsafe")
)

const (
	scaleInReasonTargetHealthMissing             = "target_health_missing"
	scaleInReasonTargetHealthStale               = "target_health_stale"
	scaleInReasonTargetHealthNotAlive            = "target_health_not_alive"
	scaleInReasonTargetRuntimeNotReady           = "target_runtime_not_ready"
	scaleInReasonEligibleNodeHealthMissing       = "eligible_node_health_missing"
	scaleInReasonEligibleNodeHealthStale         = "eligible_node_health_stale"
	scaleInReasonEligibleNodeHealthNotAlive      = "eligible_node_health_not_alive"
	scaleInReasonEligibleNodeHealthRevisionStale = "eligible_node_health_revision_stale"
	scaleInReasonEligibleNodeRuntimeNotReady     = "eligible_node_runtime_not_ready"
)

// NodeScaleInStatusRequest identifies the data node being evaluated for scale-in.
type NodeScaleInStatusRequest struct {
	// NodeID is the non-zero stable identity of the node being evaluated.
	NodeID uint64
}

// NodeScaleInPlanRequest describes a bounded Slot drain preview for a leaving node.
type NodeScaleInPlanRequest struct {
	// NodeID is the leaving data node whose Slot replicas should move away.
	NodeID uint64
	// MaxSlotMoves bounds the number of Slot replica move candidates.
	MaxSlotMoves uint32
}

// NodeScaleInAdvanceRequest describes a bounded Slot drain execute request.
type NodeScaleInAdvanceRequest struct {
	// NodeID is the leaving data node whose Slot replicas should move away.
	NodeID uint64
	// MaxSlotMoves bounds the number of Slot replica move tasks to create.
	MaxSlotMoves uint32
}

// MarkNodeRemovedRequest is the final remove intent for a fully drained node.
type MarkNodeRemovedRequest struct {
	// NodeID is the non-zero stable identity of the fully drained node.
	NodeID uint64
}

// MarkNodeRemovedResponse is returned after submitting or observing node removal.
type MarkNodeRemovedResponse struct {
	// Changed reports whether the control writer advanced cluster state.
	Changed bool
	// NodeID is the durable node identity returned by control state.
	NodeID uint64
	// JoinState is the durable membership lifecycle state.
	JoinState string
	// Revision is the control-state revision observed by the writer.
	Revision uint64
}

// NodeScaleInStatusResponse describes whether a leaving node can safely proceed.
type NodeScaleInStatusResponse struct {
	// NodeID is the evaluated node identity.
	NodeID uint64
	// JoinState is the durable membership lifecycle state observed in control state.
	JoinState string
	// GeneratedAt records when the status read model was generated.
	GeneratedAt time.Time
	// StateRevision is the control-state revision used for this status.
	StateRevision uint64
	// SafeToProceed reports that no Slot, task, Channel, or control blocker remains.
	SafeToProceed bool
	// SafeToRemove reports that the target has no known control, Slot, Channel, or runtime drain blockers.
	SafeToRemove bool
	// BlockedByMissingNode reports that control state no longer contains the target node.
	BlockedByMissingNode bool
	// BlockedByJoinState reports that the target node is not marked leaving.
	BlockedByJoinState bool
	// BlockedByControlRevision reports that an eligible node has not observed the current control revision.
	BlockedByControlRevision bool
	// BlockedByHealth reports that durable node health is missing, stale, unhealthy, or runtime-not-ready.
	BlockedByHealth bool
	// BlockedByStaleRevision reports that fresh durable node health has not observed the required control revision.
	BlockedByStaleRevision bool
	// BlockedByControllerRole reports that the target node still has the Controller role.
	BlockedByControllerRole bool
	// BlockedByDataRole reports that the target node does not have the Data role.
	BlockedByDataRole bool
	// BlockedBySlots reports that desired Slot assignments still include the target node.
	BlockedBySlots bool
	// BlockedBySlotLeadership reports that live Slot runtime still shows the target node as leader.
	BlockedBySlotLeadership bool
	// BlockedBySlotRuntime reports that Slot runtime is unavailable or still contains the target after desired placement moved.
	BlockedBySlotRuntime bool
	// BlockedByTasks reports that active or failed Controller tasks still reference the target node.
	BlockedByTasks bool
	// BlockedByChannels reports Channel leader, replica, ISR, or unknown inventory blockers.
	BlockedByChannels bool
	// BlockedByRuntimeDrain reports gateway admission or runtime counters still block final removal.
	BlockedByRuntimeDrain bool
	// UnknownRuntime reports that one or more eligible nodes could not provide runtime summary data.
	UnknownRuntime bool
	// RuntimeUnknown reports that the target node runtime drain counters could not be read.
	RuntimeUnknown bool
	// UnknownControlRevision reports that one or more eligible nodes did not report a control revision.
	UnknownControlRevision bool
	// UnknownChannelInventory reports that Channel inventory could not be proven.
	UnknownChannelInventory bool
	// HealthFresh reports whether the target durable health report is fresh.
	HealthFresh bool
	// HealthStatus is the target health status reported by durable node health.
	HealthStatus string
	// HealthFreshness is the target durable health freshness classification.
	HealthFreshness string
	// HealthReportAgeMS is the target health report age in milliseconds at snapshot build time.
	HealthReportAgeMS int64
	// HealthReportTTLMS is the freshness TTL in milliseconds used for the target health report.
	HealthReportTTLMS int64
	// ObservedControlRevision is the latest control revision observed by the target health report.
	ObservedControlRevision uint64
	// RequiredControlRevision is the control revision required for scale-in safety decisions.
	RequiredControlRevision uint64
	// BlockedReasons contains bounded machine-readable health blocker reasons.
	BlockedReasons []string
	// SlotReplicaCount counts Slot replicas that still block removing the target node.
	SlotReplicaCount int
	// SlotLeaderCount counts live Slot leaders still observed on the target node.
	SlotLeaderCount int
	// ActiveTaskCount counts pending or running Controller tasks that reference the target node.
	ActiveTaskCount int
	// FailedTaskCount counts failed Controller tasks that reference the target node.
	FailedTaskCount int
	// ChannelLeaderCount counts Channels led by the target node.
	ChannelLeaderCount int
	// ChannelReplicaCount counts Channels where the target is a configured replica.
	ChannelReplicaCount int
	// ChannelISRCount counts Channels where the target is in ISR.
	ChannelISRCount int
	// GatewayDraining reports whether the target gateway is in drain mode.
	GatewayDraining bool
	// AcceptingNewSessions reports whether the target gateway still admits new sessions.
	AcceptingNewSessions bool
	// GatewaySessions counts all target gateway sessions, including unauthenticated sessions.
	GatewaySessions int
	// ActiveOnline counts active authenticated online connections on the target node.
	ActiveOnline int
	// ClosingOnline counts authenticated online connections that are closing on the target node.
	ClosingOnline int
	// TotalOnline counts authenticated online connections tracked by the target node.
	TotalOnline int
	// PendingActivations counts target sessions accepted but not yet authority-active.
	PendingActivations int
}

// NodeScaleInCandidate describes one planned Slot replica move away from a leaving node.
type NodeScaleInCandidate struct {
	// SlotID is the physical Slot selected for a move.
	SlotID uint32
	// SourceNodeID is the leaving peer that will be replaced.
	SourceNodeID uint64
	// TargetNodeID is the active data node that will replace SourceNodeID.
	TargetNodeID uint64
	// DesiredPeers is the current desired peer set observed in control state.
	DesiredPeers []uint64
	// TargetPeers is the desired peer set after replacing SourceNodeID with TargetNodeID.
	TargetPeers []uint64
	// ConfigEpoch fences the move to the observed Slot assignment.
	ConfigEpoch uint64
}

// NodeScaleInPlanResponse is a bounded deterministic Slot drain preview.
type NodeScaleInPlanResponse struct {
	// NodeID is the leaving data node being drained.
	NodeID uint64
	// GeneratedAt records when the plan was assembled.
	GeneratedAt time.Time
	// StateRevision fences the plan to the observed control snapshot.
	StateRevision uint64
	// Candidates contains Slot move candidates in stable Slot ID order.
	Candidates []NodeScaleInCandidate
	// BlockedByStatus reports that scale-in safety status blocks task creation.
	BlockedByStatus bool
}

// NodeScaleInAdvanceResponse reports bounded Slot drain task creation results.
type NodeScaleInAdvanceResponse struct {
	// NodeID is the leaving data node being drained.
	NodeID uint64
	// GeneratedAt records when the response was assembled.
	GeneratedAt time.Time
	// StateRevision fences the request to the observed control snapshot.
	StateRevision uint64
	// Created is the number of newly accepted tasks.
	Created uint32
	// Skipped is the number of candidates that did not create a new task.
	Skipped uint32
	// Candidates contains the submitted Slot move candidates in stable Slot ID order.
	Candidates []NodeScaleInCandidate
}

// NodeScaleInStatus returns a fail-closed readiness view for removing a leaving node.
func (a *App) NodeScaleInStatus(ctx context.Context, req NodeScaleInStatusRequest) (NodeScaleInStatusResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return NodeScaleInStatusResponse{}, err
	}
	if req.NodeID == 0 {
		return NodeScaleInStatusResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.cluster == nil {
		return NodeScaleInStatusResponse{}, ErrNodeScaleInUnavailable
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return NodeScaleInStatusResponse{}, err
	}
	return a.nodeScaleInStatusFromSnapshot(ctx, snapshot, req.NodeID, nil), nil
}

// MarkNodeRemoved marks a fully drained leaving node as removed after fail-closed safety checks.
func (a *App) MarkNodeRemoved(ctx context.Context, req MarkNodeRemovedRequest) (MarkNodeRemovedResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return MarkNodeRemovedResponse{}, err
	}
	if req.NodeID == 0 {
		return MarkNodeRemovedResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.nodeLifecycle == nil {
		return MarkNodeRemovedResponse{}, ErrNodeLifecycleUnavailable
	}
	if a.cluster == nil {
		return MarkNodeRemovedResponse{}, ErrNodeScaleInUnavailable
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return MarkNodeRemovedResponse{}, err
	}
	if node, ok := findControlNode(snapshot, req.NodeID); ok && managerControlJoinState(node.JoinState) == control.NodeJoinStateRemoved {
		return a.markNodeRemoved(ctx, req.NodeID, 0)
	}
	status := a.nodeScaleInStatusFromSnapshot(ctx, snapshot, req.NodeID, nil)
	if !status.SafeToRemove {
		return MarkNodeRemovedResponse{}, ErrNodeScaleInUnsafe
	}
	return a.markNodeRemoved(ctx, req.NodeID, status.StateRevision)
}

func (a *App) markNodeRemoved(ctx context.Context, nodeID uint64, stateRevision uint64) (MarkNodeRemovedResponse, error) {
	result, err := a.nodeLifecycle.MarkNodeRemoved(ctx, control.MarkNodeRemovedRequest{NodeID: nodeID, StateRevision: stateRevision})
	if err != nil {
		return MarkNodeRemovedResponse{}, mapNodeLifecycleError(err)
	}
	return MarkNodeRemovedResponse{
		Changed:   result.Changed,
		NodeID:    result.Node.NodeID,
		JoinState: string(result.Node.JoinState),
		Revision:  result.Revision,
	}, nil
}

func (a *App) nodeScaleInStatusFromSnapshot(ctx context.Context, snapshot control.Snapshot, nodeID uint64, ignoreTask func(control.ReconcileTask) bool) NodeScaleInStatusResponse {
	response := NodeScaleInStatusResponse{
		NodeID:                  nodeID,
		GeneratedAt:             a.now(),
		StateRevision:           snapshot.Revision,
		RequiredControlRevision: snapshot.Revision,
	}
	node, ok := findControlNode(snapshot, nodeID)
	if !ok {
		response.BlockedByMissingNode = true
		return response
	}
	joinState := managerControlJoinState(node.JoinState)
	response.JoinState = string(joinState)
	if hasRole(node.Roles, control.RoleController) {
		response.BlockedByControllerRole = true
	}
	if !hasRole(node.Roles, control.RoleData) {
		response.BlockedByDataRole = true
	}
	if joinState != control.NodeJoinStateLeaving {
		response.BlockedByJoinState = true
	}
	markScaleInHealthBlockers(snapshot, node, &response)
	a.markScaleInRuntimeRevisionBlockers(ctx, snapshot, &response)
	a.markScaleInSlotBlockers(ctx, snapshot.Slots, nodeID, &response)
	markScaleInTaskBlockersWithFilter(snapshot.Tasks, nodeID, &response, ignoreTask)
	if !response.BlockedByJoinState && !response.BlockedByControllerRole && !response.BlockedByDataRole {
		a.markScaleInChannelBlockers(ctx, snapshot, nodeID, &response)
	}
	a.markScaleInRuntimeDrainBlockers(ctx, nodeID, &response)
	response.SafeToProceed = !response.BlockedByMissingNode &&
		!response.BlockedByJoinState &&
		!response.BlockedByControlRevision &&
		!response.BlockedByHealth &&
		!response.BlockedByStaleRevision &&
		!response.BlockedByControllerRole &&
		!response.BlockedByDataRole &&
		!response.BlockedBySlots &&
		!response.BlockedBySlotLeadership &&
		!response.BlockedBySlotRuntime &&
		!response.BlockedByTasks &&
		!response.BlockedByChannels &&
		!response.UnknownRuntime &&
		!response.UnknownChannelInventory &&
		response.ChannelLeaderCount == 0 &&
		response.ChannelReplicaCount == 0 &&
		response.ChannelISRCount == 0
	response.SafeToRemove = response.SafeToProceed &&
		!response.BlockedByRuntimeDrain &&
		!response.RuntimeUnknown
	return response
}

// PlanNodeScaleIn returns a deterministic bounded Slot replica drain preview.
func (a *App) PlanNodeScaleIn(ctx context.Context, req NodeScaleInPlanRequest) (NodeScaleInPlanResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return NodeScaleInPlanResponse{}, err
	}
	if req.NodeID == 0 {
		return NodeScaleInPlanResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.cluster == nil {
		return NodeScaleInPlanResponse{}, ErrNodeScaleInUnavailable
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return NodeScaleInPlanResponse{}, err
	}
	maxMoves := normalizeNodeOnboardingMaxMoves(req.MaxSlotMoves)
	response := NodeScaleInPlanResponse{
		NodeID:        req.NodeID,
		GeneratedAt:   a.now(),
		StateRevision: snapshot.Revision,
		Candidates:    make([]NodeScaleInCandidate, 0, maxMoves),
	}
	node, ok := findControlNode(snapshot, req.NodeID)
	if !ok || managerControlJoinState(node.JoinState) != control.NodeJoinStateLeaving || hasRole(node.Roles, control.RoleController) {
		response.BlockedByStatus = true
		return response, nil
	}
	status, err := a.NodeScaleInStatus(ctx, NodeScaleInStatusRequest{NodeID: req.NodeID})
	if err != nil {
		return NodeScaleInPlanResponse{}, err
	}
	if scaleInStatusBlocksPlan(status) {
		response.BlockedByStatus = true
		return response, nil
	}

	activeDataNodes := scaleInActiveDataNodeIDs(snapshot.Nodes)
	assignments := append([]control.SlotAssignment(nil), snapshot.Slots...)
	sort.Slice(assignments, func(i, j int) bool { return assignments[i].SlotID < assignments[j].SlotID })
	for _, assignment := range assignments {
		if uint32(len(response.Candidates)) >= maxMoves {
			break
		}
		if !containsUint64(assignment.DesiredPeers, req.NodeID) {
			continue
		}
		if scaleInSlotHasBlockingTask(snapshot.Tasks, assignment.SlotID) {
			continue
		}
		targetNodeID, ok := scaleInReplacementNodeID(activeDataNodes, assignment.DesiredPeers)
		if !ok {
			continue
		}
		response.Candidates = append(response.Candidates, NodeScaleInCandidate{
			SlotID:       assignment.SlotID,
			SourceNodeID: req.NodeID,
			TargetNodeID: targetNodeID,
			DesiredPeers: append([]uint64(nil), assignment.DesiredPeers...),
			TargetPeers:  replaceNodeOnboardingPeer(assignment.DesiredPeers, req.NodeID, targetNodeID),
			ConfigEpoch:  assignment.ConfigEpoch,
		})
	}
	response.Candidates = cloneNodeScaleInCandidates(response.Candidates)
	return response, nil
}

// AdvanceNodeScaleIn creates a bounded set of staged Slot replica move tasks.
func (a *App) AdvanceNodeScaleIn(ctx context.Context, req NodeScaleInAdvanceRequest) (NodeScaleInAdvanceResponse, error) {
	plan, err := a.PlanNodeScaleIn(ctx, NodeScaleInPlanRequest{NodeID: req.NodeID, MaxSlotMoves: req.MaxSlotMoves})
	if err != nil {
		return NodeScaleInAdvanceResponse{}, err
	}
	response := NodeScaleInAdvanceResponse{
		NodeID:        plan.NodeID,
		GeneratedAt:   plan.GeneratedAt,
		StateRevision: plan.StateRevision,
		Candidates:    make([]NodeScaleInCandidate, 0, len(plan.Candidates)),
	}
	if len(plan.Candidates) == 0 {
		return response, nil
	}
	if a == nil || a.slotReplicaMove == nil {
		return NodeScaleInAdvanceResponse{}, ErrNodeScaleInUnavailable
	}
	retryBudget := len(plan.Candidates) * 2
	for _, candidate := range plan.Candidates {
		for {
			fresh, stateRevision, ok, blocked, err := a.refreshScaleInCandidate(ctx, req.NodeID, candidate, responseOwnTaskSlots(response.Candidates))
			if err != nil {
				return NodeScaleInAdvanceResponse{}, err
			}
			response.StateRevision = stateRevision
			if blocked {
				return response, nil
			}
			if !ok {
				response.Skipped++
				break
			}
			result, err := a.slotReplicaMove.RequestSlotReplicaMove(ctx, control.SlotReplicaMoveRequest{
				SlotID:        fresh.SlotID,
				SourceNode:    fresh.SourceNodeID,
				TargetNode:    fresh.TargetNodeID,
				TargetPeers:   append([]uint64(nil), fresh.TargetPeers...),
				ConfigEpoch:   fresh.ConfigEpoch,
				StateRevision: stateRevision,
			})
			if err != nil {
				if scaleInRetryableWriteError(err) {
					if retryBudget <= 0 {
						if response.Created > 0 {
							response.Skipped++
							return response, nil
						}
						return NodeScaleInAdvanceResponse{}, ErrNodeScaleInConflict
					}
					retryBudget--
					continue
				}
				return NodeScaleInAdvanceResponse{}, err
			}
			response.Candidates = append(response.Candidates, fresh)
			if result.Created {
				response.Created++
			} else {
				response.Skipped++
			}
			break
		}
	}
	return response, nil
}

func (a *App) refreshScaleInCandidate(ctx context.Context, sourceNode uint64, candidate NodeScaleInCandidate, ownTaskSlots map[uint32]struct{}) (NodeScaleInCandidate, uint64, bool, bool, error) {
	if a == nil || a.cluster == nil {
		return NodeScaleInCandidate{}, 0, false, false, ErrNodeScaleInUnavailable
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return NodeScaleInCandidate{}, 0, false, false, err
	}
	status := a.nodeScaleInStatusFromSnapshot(ctx, snapshot, sourceNode, func(task control.ReconcileTask) bool {
		_, ok := ownTaskSlots[task.SlotID]
		return ok
	})
	if scaleInStatusBlocksPlan(status) {
		return NodeScaleInCandidate{}, snapshot.Revision, false, true, nil
	}
	var assignment control.SlotAssignment
	found := false
	for _, item := range snapshot.Slots {
		if item.SlotID == candidate.SlotID {
			assignment = item
			found = true
			break
		}
	}
	if !found || !containsUint64(assignment.DesiredPeers, sourceNode) || scaleInSlotHasBlockingTask(snapshot.Tasks, assignment.SlotID) {
		return NodeScaleInCandidate{}, snapshot.Revision, false, false, nil
	}
	activeDataNodes := scaleInActiveDataNodeIDs(snapshot.Nodes)
	targetNodeID, ok := scaleInReplacementNodeID(activeDataNodes, assignment.DesiredPeers)
	if !ok {
		return NodeScaleInCandidate{}, snapshot.Revision, false, false, nil
	}
	return NodeScaleInCandidate{
		SlotID:       assignment.SlotID,
		SourceNodeID: sourceNode,
		TargetNodeID: targetNodeID,
		DesiredPeers: append([]uint64(nil), assignment.DesiredPeers...),
		TargetPeers:  replaceNodeOnboardingPeer(assignment.DesiredPeers, sourceNode, targetNodeID),
		ConfigEpoch:  assignment.ConfigEpoch,
	}, snapshot.Revision, true, false, nil
}

func (a *App) markScaleInRuntimeRevisionBlockers(ctx context.Context, snapshot control.Snapshot, response *NodeScaleInStatusResponse) {
	for _, node := range snapshot.Nodes {
		if node.Status != control.NodeAlive && node.Status != control.NodeSuspect {
			continue
		}
		summary := a.nodeRuntimeSummary(ctx, node.NodeID)
		if summary.Unknown {
			response.UnknownRuntime = true
			continue
		}
		if summary.ControlRevision == 0 {
			response.UnknownControlRevision = true
			response.BlockedByControlRevision = true
			continue
		}
		if summary.ControlRevision < snapshot.Revision {
			response.BlockedByControlRevision = true
		}
	}
}

func markScaleInHealthBlockers(snapshot control.Snapshot, targetNode control.Node, response *NodeScaleInStatusResponse) {
	targetHealth := targetNode.Health
	response.HealthFresh = targetHealth.Freshness == control.NodeHealthFresh
	response.HealthStatus = string(targetHealth.Status)
	response.HealthFreshness = string(targetHealth.Freshness)
	response.HealthReportAgeMS = targetHealth.ReportAge.Milliseconds()
	response.HealthReportTTLMS = targetHealth.ReportTTL.Milliseconds()
	response.ObservedControlRevision = targetHealth.ObservedControlRevision

	if !markScaleInNodeHealthFreshness(response, targetHealth, scaleInReasonTargetHealthMissing, scaleInReasonTargetHealthStale) {
		return
	}
	if targetHealth.Status != control.NodeAlive {
		response.BlockedByHealth = true
		markBlockedReason(response, scaleInReasonTargetHealthNotAlive)
	}
	if !targetHealth.RuntimeReady {
		response.BlockedByHealth = true
		markBlockedReason(response, scaleInReasonTargetRuntimeNotReady)
	}

	for _, node := range snapshot.Nodes {
		if node.NodeID == targetNode.NodeID || !isActiveDataNode(node) {
			continue
		}
		health := node.Health
		if !markScaleInNodeHealthFreshness(response, health, scaleInReasonEligibleNodeHealthMissing, scaleInReasonEligibleNodeHealthStale) {
			continue
		}
		if health.Status != control.NodeAlive {
			response.BlockedByHealth = true
			markBlockedReason(response, scaleInReasonEligibleNodeHealthNotAlive)
			continue
		}
		if !health.RuntimeReady {
			response.BlockedByHealth = true
			markBlockedReason(response, scaleInReasonEligibleNodeRuntimeNotReady)
			continue
		}
		if health.ObservedControlRevision < snapshot.Revision {
			response.BlockedByHealth = true
			response.BlockedByStaleRevision = true
			markBlockedReason(response, scaleInReasonEligibleNodeHealthRevisionStale)
		}
	}
}

func markScaleInNodeHealthFreshness(response *NodeScaleInStatusResponse, health control.NodeHealth, missingReason string, staleReason string) bool {
	switch health.Freshness {
	case control.NodeHealthFresh:
		return true
	case "", control.NodeHealthMissing:
		response.BlockedByHealth = true
		markBlockedReason(response, missingReason)
	default:
		response.BlockedByHealth = true
		markBlockedReason(response, staleReason)
	}
	return false
}

func markBlockedReason(response *NodeScaleInStatusResponse, reason string) {
	if response == nil || reason == "" {
		return
	}
	for _, existing := range response.BlockedReasons {
		if existing == reason {
			return
		}
	}
	response.BlockedReasons = append(response.BlockedReasons, reason)
}

func (a *App) markScaleInSlotBlockers(ctx context.Context, assignments []control.SlotAssignment, targetNode uint64, response *NodeScaleInStatusResponse) {
	slots := append([]control.SlotAssignment(nil), assignments...)
	sort.Slice(slots, func(i, j int) bool { return slots[i].SlotID < slots[j].SlotID })
	for _, assignment := range slots {
		targetInDesired := containsUint64(assignment.DesiredPeers, targetNode)
		if targetInDesired {
			response.SlotReplicaCount++
			response.BlockedBySlots = true
		}
		if a == nil || a.slotRuntimeStatus == nil {
			response.BlockedBySlotRuntime = true
			continue
		}
		status, err := a.slotRuntimeStatus.SlotRuntimeStatus(ctx, assignment.SlotID, append([]uint64(nil), assignment.DesiredPeers...))
		if err != nil {
			response.BlockedBySlotRuntime = true
			continue
		}
		if status.LeaderID == targetNode {
			response.SlotLeaderCount++
			response.BlockedBySlotLeadership = true
		}
		if !targetInDesired && containsUint64(status.CurrentVoters, targetNode) {
			response.SlotReplicaCount++
			response.BlockedBySlotRuntime = true
		}
	}
}

func markScaleInTaskBlockers(tasks []control.ReconcileTask, targetNode uint64, response *NodeScaleInStatusResponse) {
	markScaleInTaskBlockersWithFilter(tasks, targetNode, response, nil)
}

func markScaleInTaskBlockersWithFilter(tasks []control.ReconcileTask, targetNode uint64, response *NodeScaleInStatusResponse, ignore func(control.ReconcileTask) bool) {
	for _, task := range tasks {
		if ignore != nil && ignore(task) {
			continue
		}
		if !scaleInTaskReferencesNode(task, targetNode) {
			continue
		}
		switch task.Status {
		case control.TaskStatusPending, control.TaskStatusRunning:
			response.ActiveTaskCount++
			response.BlockedByTasks = true
		case control.TaskStatusFailed:
			response.FailedTaskCount++
			response.BlockedByTasks = true
		}
	}
}

func (a *App) markScaleInChannelBlockers(ctx context.Context, snapshot control.Snapshot, targetNode uint64, response *NodeScaleInStatusResponse) {
	inventory := a.nodeChannelDrainInventoryFromSnapshot(ctx, snapshot, NodeChannelDrainInventoryRequest{NodeID: targetNode})
	response.UnknownChannelInventory = inventory.Unknown
	response.ChannelLeaderCount = inventory.LeaderCount
	response.ChannelReplicaCount = inventory.ReplicaCount
	response.ChannelISRCount = inventory.ISRCount
	response.BlockedByChannels = inventory.Unknown ||
		inventory.LeaderCount > 0 ||
		inventory.ReplicaCount > 0 ||
		inventory.ISRCount > 0
}

func (a *App) markScaleInRuntimeDrainBlockers(ctx context.Context, targetNode uint64, response *NodeScaleInStatusResponse) {
	summary := a.nodeRuntimeSummary(ctx, targetNode)
	if summary.Unknown {
		response.UnknownRuntime = true
		response.RuntimeUnknown = true
		response.BlockedByRuntimeDrain = true
		return
	}
	response.GatewayDraining = summary.Draining
	response.AcceptingNewSessions = summary.AcceptingNewSessions
	response.GatewaySessions = summary.GatewaySessions
	response.ActiveOnline = summary.ActiveOnline
	response.ClosingOnline = summary.ClosingOnline
	response.TotalOnline = summary.TotalOnline
	response.PendingActivations = summary.PendingActivations
	response.BlockedByRuntimeDrain = !summary.Draining ||
		summary.AcceptingNewSessions ||
		summary.GatewaySessions > 0 ||
		summary.ActiveOnline > 0 ||
		summary.ClosingOnline > 0 ||
		summary.TotalOnline > 0 ||
		summary.PendingActivations > 0
}

func responseOwnTaskSlots(candidates []NodeScaleInCandidate) map[uint32]struct{} {
	if len(candidates) == 0 {
		return nil
	}
	out := make(map[uint32]struct{}, len(candidates))
	for _, candidate := range candidates {
		out[candidate.SlotID] = struct{}{}
	}
	return out
}

func scaleInTaskReferencesNode(task control.ReconcileTask, targetNode uint64) bool {
	return task.SourceNode == targetNode ||
		task.TargetNode == targetNode ||
		containsUint64(task.TargetPeers, targetNode)
}

func scaleInStatusBlocksPlan(status NodeScaleInStatusResponse) bool {
	return status.BlockedByMissingNode ||
		status.BlockedByJoinState ||
		status.BlockedByControlRevision ||
		status.BlockedByHealth ||
		status.BlockedByStaleRevision ||
		status.BlockedByControllerRole ||
		status.BlockedByDataRole ||
		status.BlockedBySlotRuntime ||
		status.BlockedByTasks ||
		status.UnknownRuntime
}

func scaleInActiveDataNodeIDs(nodes []control.Node) []uint64 {
	out := make([]uint64, 0, len(nodes))
	for _, node := range nodes {
		if control.NodeSchedulableForPlacement(node) {
			out = append(out, node.NodeID)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func scaleInReplacementNodeID(activeDataNodes []uint64, desiredPeers []uint64) (uint64, bool) {
	for _, nodeID := range activeDataNodes {
		if !containsUint64(desiredPeers, nodeID) {
			return nodeID, true
		}
	}
	return 0, false
}

func scaleInSlotHasBlockingTask(tasks []control.ReconcileTask, slotID uint32) bool {
	for _, task := range tasks {
		if task.SlotID != slotID {
			continue
		}
		switch task.Status {
		case control.TaskStatusPending, control.TaskStatusRunning, control.TaskStatusFailed:
			return true
		}
	}
	return false
}

func scaleInRetryableWriteError(err error) bool {
	return nodeOnboardingRetryableWriteError(err)
}

func cloneNodeScaleInCandidates(items []NodeScaleInCandidate) []NodeScaleInCandidate {
	out := make([]NodeScaleInCandidate, len(items))
	for i, item := range items {
		out[i] = item
		out[i].DesiredPeers = append([]uint64(nil), item.DesiredPeers...)
		out[i].TargetPeers = append([]uint64(nil), item.TargetPeers...)
	}
	return out
}
