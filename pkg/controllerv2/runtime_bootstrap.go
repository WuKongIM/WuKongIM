package controllerv2

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	cv2raft "github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
)

// bootstrapIfNeeded creates the first state through Controller Raft, even for a single-node cluster.
func (r *Runtime) bootstrapIfNeeded(ctx context.Context) error {
	st := r.sm.Snapshot(ctx)
	if st.Revision != 0 {
		return r.runBootstrapPlanner(ctx)
	}
	if !r.cfg.AllowBootstrap {
		return errors.New("controllerv2: empty state and bootstrap disabled")
	}
	if err := r.waitLocalLeader(ctx); err != nil {
		return err
	}
	if err := r.raft.Propose(ctx, r.initCommand()); err != nil {
		return err
	}
	return r.runBootstrapPlanner(ctx)
}

func (r *Runtime) runBootstrapPlanner(ctx context.Context) error {
	for range r.cfg.InitialSlotCount {
		before := r.sm.Snapshot(ctx)
		if len(before.Slots) >= int(r.cfg.InitialSlotCount) {
			return nil
		}
		if err := r.server.TickPlanner(ctx); err != nil {
			return err
		}
		after := r.sm.Snapshot(ctx)
		if after.Revision == before.Revision {
			return nil
		}
	}
	return nil
}

func (r *Runtime) isLocalLeader() bool {
	return r.raft != nil && (r.raft.LeaderID() == r.cfg.NodeID || r.raft.Status().Role == cv2raft.RoleLeader)
}

func (r *Runtime) waitLocalLeader(ctx context.Context) error {
	ticker := time.NewTicker(r.cfg.TickInterval)
	defer ticker.Stop()
	for {
		if r.isLocalLeader() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (r *Runtime) initCommand() command.Command {
	controllers := make([]state.ControllerVoter, 0, len(r.cfg.Voters))
	nodes := make([]state.Node, 0, len(r.cfg.Voters))
	for _, voter := range r.cfg.Voters {
		controllers = append(controllers, state.ControllerVoter{NodeID: voter.NodeID, Addr: voter.Addr, Role: state.ControllerRoleVoter})
		nodes = append(nodes, state.Node{
			NodeID:         voter.NodeID,
			Addr:           voter.Addr,
			Roles:          []state.NodeRole{state.NodeRoleControllerVoter, state.NodeRoleData},
			JoinState:      state.NodeJoinStateActive,
			Status:         state.NodeStatusAlive,
			CapacityWeight: 1,
		})
	}
	return command.Command{
		Kind:     command.KindInitClusterState,
		IssuedAt: r.cfg.Now().UTC(),
		Init: &command.InitClusterState{
			ClusterID: r.cfg.ClusterID,
			Config: state.ClusterConfig{
				SlotCount:             r.cfg.InitialSlotCount,
				HashSlotCount:         r.cfg.HashSlotCount,
				ReplicaCount:          r.cfg.ReplicaCount,
				DefaultCapacityWeight: 1,
			},
			Controllers: controllers,
			Nodes:       nodes,
		},
	}
}

func (r *Runtime) raftPeers() []cv2raft.Peer {
	peers := make([]cv2raft.Peer, 0, len(r.cfg.Voters))
	for _, voter := range r.cfg.Voters {
		peers = append(peers, cv2raft.Peer{NodeID: voter.NodeID, Addr: voter.Addr})
	}
	return peers
}
