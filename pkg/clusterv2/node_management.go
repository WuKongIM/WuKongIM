package clusterv2

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
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
	return n.controlSnapshot.Clone(), nil
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
