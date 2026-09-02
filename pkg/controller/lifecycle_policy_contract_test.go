package controller

import (
	"errors"
	"testing"

	controllerfsm "github.com/WuKongIM/WuKongIM/pkg/controller/fsm"
	controllerraft "github.com/WuKongIM/WuKongIM/pkg/controller/raft"
)

func TestNodeLifecyclePolicyEnforcesSafeJoinAndDrainTransitions(t *testing.T) {
	joined, changed, err := buildJoinNode(ClusterState{}, JoinNodeRequest{
		NodeID: 4, Name: "node-4", Addr: "  n4  ",
		Roles: []NodeRole{NodeRoleControllerVoter},
	})
	if err != nil || !changed {
		t.Fatalf("buildJoinNode() = %#v, %v, %v", joined, changed, err)
	}
	if joined.Addr != "n4" || joined.CapacityWeight != 1 || joined.JoinState != NodeJoinStateJoining || joined.Status != NodeStatusAlive || len(joined.Roles) != 1 || joined.Roles[0] != NodeRoleData {
		t.Fatalf("joined node = %#v", joined)
	}

	state := ClusterState{Nodes: []Node{joined}}
	active, changed, err := buildActivateNode(state, ActivateNodeRequest{NodeID: 4})
	if err != nil || !changed || active.JoinState != NodeJoinStateActive || active.Status != NodeStatusAlive {
		t.Fatalf("buildActivateNode() = %#v, %v, %v", active, changed, err)
	}

	state.Nodes[0] = active
	leaving, changed, err := buildMarkNodeLeaving(state, MarkNodeLeavingRequest{NodeID: 4})
	if err != nil || !changed || leaving.JoinState != NodeJoinStateLeaving {
		t.Fatalf("buildMarkNodeLeaving() = %#v, %v, %v", leaving, changed, err)
	}

	state.Nodes[0] = leaving
	removed, changed, err := buildMarkNodeRemoved(state, MarkNodeRemovedRequest{NodeID: 4})
	if err != nil || !changed || removed.JoinState != NodeJoinStateRemoved || removed.Status != NodeStatusDown {
		t.Fatalf("buildMarkNodeRemoved() = %#v, %v, %v", removed, changed, err)
	}

	state.Nodes[0] = removed
	if got, changed, err := buildMarkNodeRemoved(state, MarkNodeRemovedRequest{NodeID: 4}); err != nil || changed || got.JoinState != NodeJoinStateRemoved {
		t.Fatalf("idempotent remove = %#v, %v, %v", got, changed, err)
	}
}

func TestNodeLifecyclePolicyRejectsIdentityConflictsAndUnsafeTransitions(t *testing.T) {
	activeData := Node{NodeID: 4, Addr: "n4", Roles: []NodeRole{NodeRoleData}, JoinState: NodeJoinStateActive, Status: NodeStatusAlive}
	controller := Node{NodeID: 1, Addr: "n1", Roles: []NodeRole{NodeRoleControllerVoter, NodeRoleData}, JoinState: NodeJoinStateActive, Status: NodeStatusAlive}
	state := ClusterState{Nodes: []Node{controller, activeData}}

	joinTests := []struct {
		name string
		req  JoinNodeRequest
	}{
		{name: "zero id", req: JoinNodeRequest{Addr: "n5"}},
		{name: "empty addr", req: JoinNodeRequest{NodeID: 5}},
		{name: "same id different addr", req: JoinNodeRequest{NodeID: 4, Addr: "n5"}},
		{name: "address owned by another node", req: JoinNodeRequest{NodeID: 5, Addr: "n4"}},
	}
	for _, tt := range joinTests {
		t.Run("join "+tt.name, func(t *testing.T) {
			if _, _, err := buildJoinNode(state, tt.req); err == nil {
				t.Fatal("buildJoinNode() error = nil")
			}
		})
	}

	if got, changed, err := buildJoinNode(state, JoinNodeRequest{NodeID: 4, Addr: "n4"}); err != nil || changed || got.NodeID != 4 {
		t.Fatalf("idempotent join = %#v, %v, %v", got, changed, err)
	}
	if _, _, err := buildActivateNode(state, ActivateNodeRequest{NodeID: 99}); !errors.Is(err, ErrNodeLifecycleNotFound) {
		t.Fatalf("activate missing error = %v", err)
	}
	if _, _, err := buildActivateNode(state, ActivateNodeRequest{}); err == nil {
		t.Fatal("activate zero ID error = nil")
	}
	if got, changed, err := buildActivateNode(state, ActivateNodeRequest{NodeID: 4}); err != nil || changed || got.NodeID != 4 {
		t.Fatalf("idempotent activate = %#v, %v, %v", got, changed, err)
	}
	if _, _, err := buildMarkNodeLeaving(state, MarkNodeLeavingRequest{NodeID: 1}); !errors.Is(err, ErrNodeLifecycleConflict) {
		t.Fatalf("leave controller error = %v", err)
	}
	if _, _, err := buildMarkNodeLeaving(state, MarkNodeLeavingRequest{NodeID: 99}); !errors.Is(err, ErrNodeLifecycleNotFound) {
		t.Fatalf("leave missing error = %v", err)
	}
	if _, _, err := buildMarkNodeLeaving(state, MarkNodeLeavingRequest{}); err == nil {
		t.Fatal("leave zero ID error = nil")
	}
	if _, _, err := buildMarkNodeRemoved(state, MarkNodeRemovedRequest{NodeID: 1}); !errors.Is(err, ErrNodeLifecycleConflict) {
		t.Fatalf("remove controller error = %v", err)
	}
	if _, _, err := buildMarkNodeRemoved(state, MarkNodeRemovedRequest{NodeID: 4}); !errors.Is(err, ErrNodeLifecycleConflict) {
		t.Fatalf("remove active error = %v", err)
	}
	if _, _, err := buildMarkNodeRemoved(state, MarkNodeRemovedRequest{NodeID: 99}); !errors.Is(err, ErrNodeLifecycleNotFound) {
		t.Fatalf("remove missing error = %v", err)
	}
	if _, _, err := buildMarkNodeRemoved(state, MarkNodeRemovedRequest{}); err == nil {
		t.Fatal("remove zero ID error = nil")
	}
}

func TestControllerErrorClassifiersPreserveCommittedRejectionReasons(t *testing.T) {
	if !IsExpectedRevisionMismatch(ErrExpectedRevisionMismatch) {
		t.Fatal("direct expected revision mismatch was not classified")
	}
	if !IsExpectedRevisionMismatch(controllerraft.ProposalRejectedError{Index: 7, Reason: controllerfsm.ReasonExpectedRevisionMismatch}) {
		t.Fatal("committed expected revision rejection was not classified")
	}
	if IsExpectedRevisionMismatch(controllerraft.ProposalRejectedError{Index: 7, Reason: controllerfsm.ReasonTaskPhaseMismatch}) {
		t.Fatal("task phase rejection was classified as revision mismatch")
	}
	if !IsTaskPhaseMismatch(controllerraft.ProposalRejectedError{Index: 8, Reason: controllerfsm.ReasonTaskPhaseMismatch}) {
		t.Fatal("committed task phase rejection was not classified")
	}
	if IsTaskPhaseMismatch(errors.New("task_phase_mismatch")) {
		t.Fatal("untyped text was classified as a committed task phase rejection")
	}
}
