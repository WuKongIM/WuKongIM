package slots

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

// Reconciler converges local Slot assignments from control snapshots.
type Reconciler struct {
	localNode uint64
	manager   *Manager
}

// NewReconciler creates a Reconciler.
func NewReconciler(localNode uint64, manager *Manager) *Reconciler {
	return &Reconciler{localNode: localNode, manager: manager}
}

// Reconcile ensures Slots assigned to the local node and skips destructive cleanup in v1.
func (r *Reconciler) Reconcile(ctx context.Context, snapshot control.Snapshot) error {
	if r == nil || r.manager == nil {
		return nil
	}
	for _, slot := range snapshot.Slots {
		if !containsNode(slot.DesiredPeers, r.localNode) {
			continue
		}
		if err := r.manager.Ensure(ctx, Assignment{SlotID: slot.SlotID, DesiredPeers: slot.DesiredPeers, PreferredLeader: slot.PreferredLeader}); err != nil {
			return err
		}
	}
	return nil
}
