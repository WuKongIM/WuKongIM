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
