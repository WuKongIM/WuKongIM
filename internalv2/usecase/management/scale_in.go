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
)

// NodeScaleInStatusRequest identifies the data node being evaluated for scale-in.
type NodeScaleInStatusRequest struct {
	// NodeID is the non-zero stable identity of the node being evaluated.
	NodeID uint64
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
	// SafeToProceed reports that no known or unknown blocker remains.
	SafeToProceed bool
	// BlockedByMissingNode reports that control state no longer contains the target node.
	BlockedByMissingNode bool
	// BlockedByJoinState reports that the target node is not marked leaving.
	BlockedByJoinState bool
	// BlockedByControlRevision reports that an eligible node has not observed the current control revision.
	BlockedByControlRevision bool
	// BlockedByControllerRole reports that the target node still has the Controller role.
	BlockedByControllerRole bool
	// BlockedBySlots reports that desired Slot assignments still include the target node.
	BlockedBySlots bool
	// BlockedBySlotLeadership reports that live Slot runtime still shows the target node as leader.
	BlockedBySlotLeadership bool
	// BlockedBySlotRuntime reports that Slot runtime is unavailable or still contains the target after desired placement moved.
	BlockedBySlotRuntime bool
	// BlockedByTasks reports that active or failed Controller tasks still reference the target node.
	BlockedByTasks bool
	// UnknownRuntime reports that one or more eligible nodes could not provide runtime summary data.
	UnknownRuntime bool
	// UnknownControlRevision reports that one or more eligible nodes did not report a control revision.
	UnknownControlRevision bool
	// SlotReplicaCount counts Slot replicas that still block removing the target node.
	SlotReplicaCount int
	// SlotLeaderCount counts live Slot leaders still observed on the target node.
	SlotLeaderCount int
	// ActiveTaskCount counts pending or running Controller tasks that reference the target node.
	ActiveTaskCount int
	// FailedTaskCount counts failed Controller tasks that reference the target node.
	FailedTaskCount int
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
	response := NodeScaleInStatusResponse{
		NodeID:        req.NodeID,
		GeneratedAt:   a.now(),
		StateRevision: snapshot.Revision,
	}
	node, ok := findControlNode(snapshot, req.NodeID)
	if !ok {
		response.BlockedByMissingNode = true
		return response, nil
	}
	joinState := managerControlJoinState(node.JoinState)
	response.JoinState = string(joinState)
	if hasRole(node.Roles, control.RoleController) {
		response.BlockedByControllerRole = true
	}
	if joinState != control.NodeJoinStateLeaving {
		response.BlockedByJoinState = true
	}
	a.markScaleInRuntimeRevisionBlockers(ctx, snapshot, &response)
	a.markScaleInSlotBlockers(ctx, snapshot.Slots, req.NodeID, &response)
	markScaleInTaskBlockers(snapshot.Tasks, req.NodeID, &response)
	response.SafeToProceed = !response.BlockedByMissingNode &&
		!response.BlockedByJoinState &&
		!response.BlockedByControlRevision &&
		!response.BlockedByControllerRole &&
		!response.BlockedBySlots &&
		!response.BlockedBySlotLeadership &&
		!response.BlockedBySlotRuntime &&
		!response.BlockedByTasks &&
		!response.UnknownRuntime
	return response, nil
}

func (a *App) markScaleInRuntimeRevisionBlockers(ctx context.Context, snapshot control.Snapshot, response *NodeScaleInStatusResponse) {
	for _, node := range snapshot.Nodes {
		if node.Status != control.NodeAlive && node.Status != control.NodeSuspect {
			continue
		}
		summary := a.nodeRuntimeSummary(ctx, node.NodeID)
		if summary.Unknown {
			response.UnknownRuntime = true
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
	for _, task := range tasks {
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

func scaleInTaskReferencesNode(task control.ReconcileTask, targetNode uint64) bool {
	return task.SourceNode == targetNode ||
		task.TargetNode == targetNode ||
		containsUint64(task.TargetPeers, targetNode)
}
