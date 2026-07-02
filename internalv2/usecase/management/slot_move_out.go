package management

import (
	"context"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// NodeSlotMoveOutPlanRequest describes a bounded Slot move-out preview for an active data node.
type NodeSlotMoveOutPlanRequest struct {
	// NodeID is the active data node whose Slot replicas should move away.
	NodeID uint64
	// MaxSlotMoves bounds the number of Slot replica move candidates.
	MaxSlotMoves uint32
}

// NodeSlotMoveOutAdvanceRequest describes a bounded Slot move-out execute request.
type NodeSlotMoveOutAdvanceRequest struct {
	// NodeID is the active data node whose Slot replicas should move away.
	NodeID uint64
	// MaxSlotMoves bounds the number of Slot replica move tasks to create.
	MaxSlotMoves uint32
}

// NodeSlotMoveOutCandidate describes one planned Slot replica move away from an active data node.
type NodeSlotMoveOutCandidate = NodeScaleInCandidate

// NodeSlotMoveOutPlanResponse is a bounded deterministic Slot move-out preview.
type NodeSlotMoveOutPlanResponse struct {
	// NodeID is the active data node being drained of Slot replicas.
	NodeID uint64
	// GeneratedAt records when the plan was assembled.
	GeneratedAt time.Time
	// StateRevision fences the plan to the observed control snapshot.
	StateRevision uint64
	// Candidates contains Slot move candidates in stable Slot ID order.
	Candidates []NodeSlotMoveOutCandidate
	// BlockedByStatus reports that source-node state blocks task creation.
	BlockedByStatus bool
}

// NodeSlotMoveOutAdvanceResponse reports bounded Slot move-out task creation results.
type NodeSlotMoveOutAdvanceResponse struct {
	// NodeID is the active data node being drained of Slot replicas.
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
	Candidates []NodeSlotMoveOutCandidate
}

// PlanNodeSlotMoveOut returns a bounded Slot replica move preview without changing node lifecycle.
func (a *App) PlanNodeSlotMoveOut(ctx context.Context, req NodeSlotMoveOutPlanRequest) (NodeSlotMoveOutPlanResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return NodeSlotMoveOutPlanResponse{}, err
	}
	if req.NodeID == 0 {
		return NodeSlotMoveOutPlanResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.cluster == nil {
		return NodeSlotMoveOutPlanResponse{}, ErrNodeScaleInUnavailable
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return NodeSlotMoveOutPlanResponse{}, err
	}
	return a.planNodeSlotMoveOutFromSnapshot(snapshot, req.NodeID, normalizeNodeOnboardingMaxMoves(req.MaxSlotMoves)), nil
}

// AdvanceNodeSlotMoveOut creates bounded staged Slot replica move tasks without changing node lifecycle.
func (a *App) AdvanceNodeSlotMoveOut(ctx context.Context, req NodeSlotMoveOutAdvanceRequest) (NodeSlotMoveOutAdvanceResponse, error) {
	plan, err := a.PlanNodeSlotMoveOut(ctx, NodeSlotMoveOutPlanRequest{NodeID: req.NodeID, MaxSlotMoves: req.MaxSlotMoves})
	if err != nil {
		return NodeSlotMoveOutAdvanceResponse{}, err
	}
	response := NodeSlotMoveOutAdvanceResponse{
		NodeID:        plan.NodeID,
		GeneratedAt:   plan.GeneratedAt,
		StateRevision: plan.StateRevision,
		Candidates:    make([]NodeSlotMoveOutCandidate, 0, len(plan.Candidates)),
	}
	if len(plan.Candidates) == 0 || plan.BlockedByStatus {
		return response, nil
	}
	if a == nil || a.slotReplicaMove == nil {
		return NodeSlotMoveOutAdvanceResponse{}, ErrNodeScaleInUnavailable
	}
	retryBudget := len(plan.Candidates) * 2
	for _, candidate := range plan.Candidates {
		for {
			fresh, stateRevision, ok, blocked, err := a.refreshNodeSlotMoveOutCandidate(ctx, req.NodeID, candidate, responseOwnTaskSlots(response.Candidates))
			if err != nil {
				return NodeSlotMoveOutAdvanceResponse{}, err
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
						return NodeSlotMoveOutAdvanceResponse{}, ErrNodeScaleInConflict
					}
					retryBudget--
					continue
				}
				return NodeSlotMoveOutAdvanceResponse{}, err
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

func (a *App) planNodeSlotMoveOutFromSnapshot(snapshot control.Snapshot, sourceNodeID uint64, maxMoves uint32) NodeSlotMoveOutPlanResponse {
	response := NodeSlotMoveOutPlanResponse{
		NodeID:        sourceNodeID,
		GeneratedAt:   a.now(),
		StateRevision: snapshot.Revision,
		Candidates:    make([]NodeSlotMoveOutCandidate, 0, maxMoves),
	}
	if !nodeSlotMoveOutSourceEligible(snapshot.Nodes, sourceNodeID) {
		response.BlockedByStatus = true
		return response
	}
	activeDataNodes := scaleInActiveDataNodeIDs(snapshot.Nodes)
	assignments := append([]control.SlotAssignment(nil), snapshot.Slots...)
	sort.Slice(assignments, func(i, j int) bool { return assignments[i].SlotID < assignments[j].SlotID })
	for _, assignment := range assignments {
		if uint32(len(response.Candidates)) >= maxMoves {
			break
		}
		if !containsUint64(assignment.DesiredPeers, sourceNodeID) {
			continue
		}
		if scaleInSlotHasBlockingTask(snapshot.Tasks, assignment.SlotID) {
			continue
		}
		targetNodeID, ok := scaleInReplacementNodeID(activeDataNodes, assignment.DesiredPeers)
		if !ok {
			continue
		}
		response.Candidates = append(response.Candidates, NodeSlotMoveOutCandidate{
			SlotID:       assignment.SlotID,
			SourceNodeID: sourceNodeID,
			TargetNodeID: targetNodeID,
			DesiredPeers: append([]uint64(nil), assignment.DesiredPeers...),
			TargetPeers:  replaceNodeOnboardingPeer(assignment.DesiredPeers, sourceNodeID, targetNodeID),
			ConfigEpoch:  assignment.ConfigEpoch,
		})
	}
	response.Candidates = cloneNodeScaleInCandidates(response.Candidates)
	return response
}

func (a *App) refreshNodeSlotMoveOutCandidate(ctx context.Context, sourceNode uint64, candidate NodeSlotMoveOutCandidate, ownTaskSlots map[uint32]struct{}) (NodeSlotMoveOutCandidate, uint64, bool, bool, error) {
	if a == nil || a.cluster == nil {
		return NodeSlotMoveOutCandidate{}, 0, false, false, ErrNodeScaleInUnavailable
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return NodeSlotMoveOutCandidate{}, 0, false, false, err
	}
	if !nodeSlotMoveOutSourceEligible(snapshot.Nodes, sourceNode) {
		return NodeSlotMoveOutCandidate{}, snapshot.Revision, false, true, nil
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
	if !found ||
		!containsUint64(assignment.DesiredPeers, sourceNode) ||
		scaleInSlotHasBlockingTaskWithIgnore(snapshot.Tasks, assignment.SlotID, ownTaskSlots) {
		return NodeSlotMoveOutCandidate{}, snapshot.Revision, false, false, nil
	}
	activeDataNodes := scaleInActiveDataNodeIDs(snapshot.Nodes)
	targetNodeID, ok := scaleInReplacementNodeID(activeDataNodes, assignment.DesiredPeers)
	if !ok {
		return NodeSlotMoveOutCandidate{}, snapshot.Revision, false, false, nil
	}
	return NodeSlotMoveOutCandidate{
		SlotID:       assignment.SlotID,
		SourceNodeID: sourceNode,
		TargetNodeID: targetNodeID,
		DesiredPeers: append([]uint64(nil), assignment.DesiredPeers...),
		TargetPeers:  replaceNodeOnboardingPeer(assignment.DesiredPeers, sourceNode, targetNodeID),
		ConfigEpoch:  assignment.ConfigEpoch,
	}, snapshot.Revision, true, false, nil
}

func nodeSlotMoveOutSourceEligible(nodes []control.Node, nodeID uint64) bool {
	for _, node := range nodes {
		if node.NodeID != nodeID {
			continue
		}
		return hasRole(node.Roles, control.RoleData) &&
			managerControlJoinState(node.JoinState) == control.NodeJoinStateActive
	}
	return false
}

func scaleInSlotHasBlockingTaskWithIgnore(tasks []control.ReconcileTask, slotID uint32, ignoreSlots map[uint32]struct{}) bool {
	if _, ok := ignoreSlots[slotID]; ok {
		return false
	}
	return scaleInSlotHasBlockingTask(tasks, slotID)
}
