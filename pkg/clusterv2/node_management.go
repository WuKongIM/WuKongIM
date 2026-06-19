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
