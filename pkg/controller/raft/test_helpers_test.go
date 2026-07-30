package raft

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
	"github.com/WuKongIM/WuKongIM/pkg/controller/statefile"
)

func newTestStateMachine(t *testing.T, path string) *fsm.StateMachine {
	t.Helper()
	sm, err := fsm.New(statefile.New(path))
	require.NoError(t, err)
	return sm
}

func testInitCommand(clusterID string, peers []Peer) command.Command {
	controllers := make([]state.ControllerVoter, 0, len(peers))
	nodes := make([]state.Node, 0, len(peers))
	for _, peer := range peers {
		controllers = append(controllers, state.ControllerVoter{NodeID: peer.NodeID, Addr: peer.Addr, Role: state.ControllerRoleVoter})
		nodes = append(nodes, state.Node{NodeID: peer.NodeID, Name: fmt.Sprintf("n%d", peer.NodeID), Addr: peer.Addr, Roles: []state.NodeRole{state.NodeRoleControllerVoter, state.NodeRoleData}, JoinState: state.NodeJoinStateActive, Status: state.NodeStatusAlive, CapacityWeight: 10})
	}
	return command.Command{Kind: command.KindInitClusterState, Init: &command.InitClusterState{ClusterID: clusterID, Config: state.ClusterConfig{SlotCount: 4, HashSlotCount: 16, ReplicaCount: 3, DefaultCapacityWeight: 10}, Controllers: controllers, Nodes: nodes}}
}
