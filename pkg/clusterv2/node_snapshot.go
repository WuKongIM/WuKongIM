package clusterv2

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
)

func (n *Node) initialControlSnapshot(ctx context.Context) (control.Snapshot, error) {
	snapshot, err := n.control.LocalSnapshot(ctx)
	if err != nil {
		return control.Snapshot{}, err
	}
	if err := snapshot.Validate(); err == nil {
		return snapshot, nil
	} else if !emptyControlSnapshot(snapshot) {
		return control.Snapshot{}, err
	}

	watch := n.control.Watch()
	for {
		select {
		case <-ctx.Done():
			return control.Snapshot{}, ctx.Err()
		case event, ok := <-watch:
			if !ok {
				return control.Snapshot{}, ErrNotStarted
			}
			if err := event.Snapshot.Validate(); err == nil {
				return event.Snapshot.Clone(), nil
			} else if !emptyControlSnapshot(event.Snapshot) {
				return control.Snapshot{}, err
			}
		}
	}
}

func emptyControlSnapshot(snapshot control.Snapshot) bool {
	return snapshot.Revision == 0 &&
		snapshot.ControllerID == 0 &&
		len(snapshot.Nodes) == 0 &&
		len(snapshot.Slots) == 0 &&
		snapshot.HashSlots.Count == 0 &&
		len(snapshot.HashSlots.Ranges) == 0 &&
		len(snapshot.Tasks) == 0
}

func (n *Node) applySnapshot(ctx context.Context, snapshot control.Snapshot) error {
	n.mu.RLock()
	previous := n.controlSnapshot.Clone()
	firstSnapshot := emptyControlSnapshot(previous)
	n.mu.RUnlock()
	changes := snapshotChanges(previous, snapshot)
	if n.router != nil && (firstSnapshot || changes.slots || changes.hashSlots) {
		if err := n.router.UpdateControlSnapshot(snapshot); err != nil {
			return err
		}
	}
	if n.discovery != nil && (firstSnapshot || changes.nodes) {
		n.discovery.Update(discoveryNodes(snapshot.Nodes))
	}
	if n.slots != nil && (firstSnapshot || changes.slots) {
		if err := n.slots.Reconcile(ctx, snapshot); err != nil {
			return err
		}
	}
	n.mu.Lock()
	n.controlSnapshot = snapshot.Clone()
	n.snapshot = Snapshot{NodeID: n.cfg.NodeID, ControllerLead: snapshot.ControllerID, StateRevision: snapshot.Revision, RoutesReady: n.router != nil && n.router.Table() != nil, SlotsReady: true, ChannelsReady: n.channels != nil, SlotCount: uint32(len(snapshot.Slots)), HashSlotCount: snapshot.HashSlots.Count}
	n.mu.Unlock()
	return nil
}

func discoveryNodes(nodes []control.Node) []clusternet.NodeAddress {
	out := make([]clusternet.NodeAddress, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, clusternet.NodeAddress{NodeID: node.NodeID, Addr: node.Addr})
	}
	return out
}

func controlVoterNodes(voters []ControlVoter) []clusternet.NodeAddress {
	out := make([]clusternet.NodeAddress, 0, len(voters))
	for _, voter := range voters {
		out = append(out, clusternet.NodeAddress{NodeID: voter.NodeID, Addr: voter.Addr})
	}
	return out
}

func runtimeVoters(voters []ControlVoter) []control.RuntimeVoter {
	out := make([]control.RuntimeVoter, 0, len(voters))
	for _, voter := range voters {
		out = append(out, control.RuntimeVoter{NodeID: voter.NodeID, Addr: voter.Addr})
	}
	return out
}

func (n *Node) markChannelsReady(ready bool) {
	if n == nil {
		return
	}
	n.mu.Lock()
	n.snapshot.NodeID = n.cfg.NodeID
	n.snapshot.ChannelsReady = ready
	n.mu.Unlock()
}

func convertRoute(route routing.Route, err error) (Route, error) {
	if err != nil {
		return Route{}, mapRouteError(err)
	}
	return Route{HashSlot: route.HashSlot, SlotID: route.SlotID, Leader: route.Leader, Peers: append([]uint64(nil), route.Peers...), Revision: route.Revision}, nil
}

func mapRouteError(err error) error {
	switch {
	case errors.Is(err, routing.ErrRouteNotReady):
		return ErrRouteNotReady
	case errors.Is(err, routing.ErrNoSlotLeader):
		return ErrNoSlotLeader
	case errors.Is(err, routing.ErrRouteMismatch):
		return ErrRouteNotReady
	default:
		return err
	}
}

func (n *Node) ensureForeground() error {
	if n == nil {
		return ErrNotStarted
	}
	if n.stopping.Load() {
		return ErrStopping
	}
	if !n.started.Load() {
		return ErrNotStarted
	}
	return nil
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
