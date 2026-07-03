package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controller"
)

// LocalControlSnapshot returns the latest locally visible control snapshot.
func (n *Node) LocalControlSnapshot(ctx context.Context) (control.Snapshot, error) {
	if err := ctxErr(ctx); err != nil {
		return control.Snapshot{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return control.Snapshot{}, err
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	snapshot := n.controlSnapshot.Clone()
	lease := n.channelDataPlaneLease.snapshot()
	snapshot.ChannelDataPlaneLease = control.ChannelDataPlaneLease{
		LastVisibleAt: lease.lastVisibleAt,
		TTL:           lease.ttl,
		Ready:         lease.ready,
	}
	return snapshot, nil
}

// RequestSlotLeaderTransfer submits a Controller-backed Slot leader transfer intent.
func (n *Node) RequestSlotLeaderTransfer(ctx context.Context, req control.SlotLeaderTransferRequest) (control.SlotLeaderTransferResult, error) {
	if err := ctxErr(ctx); err != nil {
		return control.SlotLeaderTransferResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return control.SlotLeaderTransferResult{}, err
	}
	if n.control == nil {
		return control.SlotLeaderTransferResult{}, ErrNotStarted
	}
	writer, ok := n.control.(interface {
		RequestSlotLeaderTransfer(context.Context, control.SlotLeaderTransferRequest) (control.SlotLeaderTransferResult, error)
	})
	if !ok {
		return control.SlotLeaderTransferResult{}, ErrNotStarted
	}
	return writer.RequestSlotLeaderTransfer(ctx, req)
}

// RequestSlotReplicaMove submits a Controller-backed staged Slot replica move intent.
func (n *Node) RequestSlotReplicaMove(ctx context.Context, req control.SlotReplicaMoveRequest) (control.SlotReplicaMoveResult, error) {
	if err := ctxErr(ctx); err != nil {
		return control.SlotReplicaMoveResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return control.SlotReplicaMoveResult{}, err
	}
	if n.control == nil {
		return control.SlotReplicaMoveResult{}, ErrNotStarted
	}
	writer, ok := n.control.(interface {
		RequestSlotReplicaMove(context.Context, control.SlotReplicaMoveRequest) (control.SlotReplicaMoveResult, error)
	})
	if !ok {
		return control.SlotReplicaMoveResult{}, ErrNotStarted
	}
	return writer.RequestSlotReplicaMove(ctx, req)
}

// JoinNode submits a Controller-backed data-node join intent.
func (n *Node) JoinNode(ctx context.Context, req control.JoinNodeRequest) (control.JoinNodeResult, error) {
	if err := ctxErr(ctx); err != nil {
		return control.JoinNodeResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return control.JoinNodeResult{}, err
	}
	if n.control == nil {
		return control.JoinNodeResult{}, ErrNotStarted
	}
	writer, ok := n.control.(interface {
		JoinNode(context.Context, control.JoinNodeRequest) (control.JoinNodeResult, error)
	})
	if !ok {
		return control.JoinNodeResult{}, ErrNotStarted
	}
	return writer.JoinNode(ctx, req)
}

// ActivateNode submits a Controller-backed node activation intent.
func (n *Node) ActivateNode(ctx context.Context, req control.ActivateNodeRequest) (control.ActivateNodeResult, error) {
	if err := ctxErr(ctx); err != nil {
		return control.ActivateNodeResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return control.ActivateNodeResult{}, err
	}
	if n.control == nil {
		return control.ActivateNodeResult{}, ErrNotStarted
	}
	writer, ok := n.control.(interface {
		ActivateNode(context.Context, control.ActivateNodeRequest) (control.ActivateNodeResult, error)
	})
	if !ok {
		return control.ActivateNodeResult{}, ErrNotStarted
	}
	return writer.ActivateNode(ctx, req)
}

// MarkNodeLeaving submits a Controller-backed node leaving intent.
func (n *Node) MarkNodeLeaving(ctx context.Context, req control.MarkNodeLeavingRequest) (control.MarkNodeLeavingResult, error) {
	if err := ctxErr(ctx); err != nil {
		return control.MarkNodeLeavingResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return control.MarkNodeLeavingResult{}, err
	}
	if n.control == nil {
		return control.MarkNodeLeavingResult{}, ErrNotStarted
	}
	writer, ok := n.control.(interface {
		MarkNodeLeaving(context.Context, control.MarkNodeLeavingRequest) (control.MarkNodeLeavingResult, error)
	})
	if !ok {
		return control.MarkNodeLeavingResult{}, ErrNotStarted
	}
	return writer.MarkNodeLeaving(ctx, req)
}

// MarkNodeRemoved submits a Controller-backed node removed intent.
func (n *Node) MarkNodeRemoved(ctx context.Context, req control.MarkNodeRemovedRequest) (control.MarkNodeRemovedResult, error) {
	if err := ctxErr(ctx); err != nil {
		return control.MarkNodeRemovedResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return control.MarkNodeRemovedResult{}, err
	}
	if n.control == nil {
		return control.MarkNodeRemovedResult{}, ErrNotStarted
	}
	writer, ok := n.control.(interface {
		MarkNodeRemoved(context.Context, control.MarkNodeRemovedRequest) (control.MarkNodeRemovedResult, error)
	})
	if !ok {
		return control.MarkNodeRemovedResult{}, ErrNotStarted
	}
	return writer.MarkNodeRemoved(ctx, req)
}

// PromoteControllerVoter promotes one active non-Controller node into Controller Raft voting membership.
func (n *Node) PromoteControllerVoter(ctx context.Context, req control.PromoteControllerVoterRequest) (control.PromoteControllerVoterResult, error) {
	if err := ctxErr(ctx); err != nil {
		return control.PromoteControllerVoterResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return control.PromoteControllerVoterResult{}, err
	}
	if n.control == nil {
		return control.PromoteControllerVoterResult{}, ErrNotStarted
	}
	writer, ok := n.control.(interface {
		PromoteControllerVoter(context.Context, control.PromoteControllerVoterRequest) (control.PromoteControllerVoterResult, error)
	})
	if !ok {
		return control.PromoteControllerVoterResult{}, ErrNotStarted
	}
	return writer.PromoteControllerVoter(ctx, req)
}

// PrepareControllerVoter prepares this node's local Controller runtime for voter promotion.
func (n *Node) PrepareControllerVoter(ctx context.Context, req cv2.PrepareControllerVoterRequest) (cv2.PrepareControllerVoterResult, error) {
	if err := ctxErr(ctx); err != nil {
		return cv2.PrepareControllerVoterResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return cv2.PrepareControllerVoterResult{}, err
	}
	if n.control == nil {
		return cv2.PrepareControllerVoterResult{}, ErrNotStarted
	}
	writer, ok := n.control.(interface {
		PrepareControllerVoter(context.Context, cv2.PrepareControllerVoterRequest) (cv2.PrepareControllerVoterResult, error)
	})
	if !ok {
		return cv2.PrepareControllerVoterResult{}, ErrNotStarted
	}
	result, err := writer.PrepareControllerVoter(ctx, req)
	if err != nil {
		return cv2.PrepareControllerVoterResult{}, err
	}
	if runtime, ok := n.control.(*control.Runtime); ok {
		n.registerControlRuntimeRPCHandlers(runtime)
	}
	return result, nil
}
