package controllerv2

import (
	"context"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
)

// JoinNodeRequest describes a data-node join intent.
type JoinNodeRequest struct {
	// NodeID is the non-zero stable identity of the joining node.
	NodeID uint64
	// Name is an optional human-readable label for the node.
	Name string
	// Addr is the stable control-plane address for the node.
	Addr string
	// Roles is the requested durable capability set; controller voter is ignored.
	Roles []NodeRole
	// CapacityWeight is the planner placement weight; zero defaults to one.
	CapacityWeight uint32
}

// JoinNodeResult describes the durable node record observed or created by JoinNode.
type JoinNodeResult struct {
	// Created reports whether JoinNode actually advanced cluster state.
	Created bool
	// Node is the durable node record that satisfies the request.
	Node Node
	// Revision is the cluster-state revision observed by the method.
	Revision uint64
}

// ActivateNodeRequest describes a request to make a joining node assignment-ready.
type ActivateNodeRequest struct {
	// NodeID is the non-zero stable identity of the joining node.
	NodeID uint64
}

// ActivateNodeResult describes the durable node record observed or activated by ActivateNode.
type ActivateNodeResult struct {
	// Changed reports whether ActivateNode actually advanced cluster state.
	Changed bool
	// Node is the durable node record that satisfies the request.
	Node Node
	// Revision is the cluster-state revision observed by the method.
	Revision uint64
}

// MarkNodeLeavingRequest identifies a data node that should stop receiving new assignments.
type MarkNodeLeavingRequest struct {
	// NodeID is the stable node identity to mark leaving.
	NodeID uint64
}

// MarkNodeLeavingResult describes the node record after the transition.
type MarkNodeLeavingResult struct {
	// Changed reports whether the request changed ControllerV2 state.
	Changed bool
	// Node is the durable node record after the request.
	Node Node
	// Revision is the observed ControllerV2 state revision after the write.
	Revision uint64
}

// MarkNodeRemovedRequest identifies a leaving node that passed external drain safety.
type MarkNodeRemovedRequest struct {
	// NodeID is the stable node identity to tombstone.
	NodeID uint64
	// ExpectedRevision fences a remove write to the control revision used by the safety check.
	ExpectedRevision uint64
}

// MarkNodeRemovedResult describes the node record after the transition.
type MarkNodeRemovedResult struct {
	// Changed reports whether the request changed ControllerV2 state.
	Changed bool
	// Node is the durable node record after the request.
	Node Node
	// Revision is the observed ControllerV2 state revision after the write.
	Revision uint64
}

func gofailMarkNodeRemovedPostCommitFault(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	return fmt.Errorf("controllerv2: %s", raw)
}

// JoinNode adds a data-capable node in joining state without changing Slot assignments.
func (r *Runtime) JoinNode(ctx context.Context, req JoinNodeRequest) (JoinNodeResult, error) {
	if err := ctxErr(ctx); err != nil {
		return JoinNodeResult{}, err
	}
	if r == nil || r.raft == nil {
		return JoinNodeResult{}, ErrNotStarted
	}
	st, err := r.LocalState(ctx)
	if err != nil {
		return JoinNodeResult{}, err
	}
	node, created, err := buildJoinNode(st, req)
	if err != nil {
		return JoinNodeResult{}, err
	}
	if !created {
		return JoinNodeResult{Created: false, Node: node, Revision: st.Revision}, nil
	}
	expectedRevision := st.Revision
	proposal, err := r.raft.ProposeResult(ctx, command.Command{
		Kind:             command.KindUpsertNode,
		IssuedAt:         r.cfg.Now().UTC(),
		ExpectedRevision: &expectedRevision,
		Node:             &node,
	})
	if err != nil {
		return JoinNodeResult{}, err
	}
	if err := r.publishFromState(ctx); err != nil {
		return JoinNodeResult{}, err
	}
	updated, err := r.LocalState(ctx)
	if err != nil {
		return JoinNodeResult{}, err
	}
	finalNode, ok := findLifecycleNode(updated, req.NodeID)
	if !ok {
		return JoinNodeResult{}, fmt.Errorf("controllerv2: node %d not found after join proposal", req.NodeID)
	}
	return JoinNodeResult{Created: proposal.Changed, Node: finalNode, Revision: updated.Revision}, nil
}

// ActivateNode marks an existing joining node active and assignment-ready.
func (r *Runtime) ActivateNode(ctx context.Context, req ActivateNodeRequest) (ActivateNodeResult, error) {
	if err := ctxErr(ctx); err != nil {
		return ActivateNodeResult{}, err
	}
	if r == nil || r.raft == nil {
		return ActivateNodeResult{}, ErrNotStarted
	}
	st, err := r.LocalState(ctx)
	if err != nil {
		return ActivateNodeResult{}, err
	}
	node, changed, err := buildActivateNode(st, req)
	if err != nil {
		return ActivateNodeResult{}, err
	}
	if !changed {
		return ActivateNodeResult{Changed: false, Node: node, Revision: st.Revision}, nil
	}
	expectedRevision := st.Revision
	proposal, err := r.raft.ProposeResult(ctx, command.Command{
		Kind:             command.KindUpsertNode,
		IssuedAt:         r.cfg.Now().UTC(),
		ExpectedRevision: &expectedRevision,
		Node:             &node,
	})
	if err != nil {
		return ActivateNodeResult{}, err
	}
	if err := r.publishFromState(ctx); err != nil {
		return ActivateNodeResult{}, err
	}
	updated, err := r.LocalState(ctx)
	if err != nil {
		return ActivateNodeResult{}, err
	}
	finalNode, ok := findLifecycleNode(updated, req.NodeID)
	if !ok {
		return ActivateNodeResult{}, fmt.Errorf("controllerv2: node %d not found after activate proposal", req.NodeID)
	}
	return ActivateNodeResult{Changed: proposal.Changed, Node: finalNode, Revision: updated.Revision}, nil
}

// MarkNodeLeaving marks an active data node as leaving so planners stop new placement.
func (r *Runtime) MarkNodeLeaving(ctx context.Context, req MarkNodeLeavingRequest) (MarkNodeLeavingResult, error) {
	if err := ctxErr(ctx); err != nil {
		return MarkNodeLeavingResult{}, err
	}
	if r == nil || r.raft == nil {
		return MarkNodeLeavingResult{}, ErrNotStarted
	}
	st, err := r.LocalState(ctx)
	if err != nil {
		return MarkNodeLeavingResult{}, err
	}
	node, changed, err := buildMarkNodeLeaving(st, req)
	if err != nil {
		return MarkNodeLeavingResult{}, err
	}
	if !changed {
		return MarkNodeLeavingResult{Changed: false, Node: node, Revision: st.Revision}, nil
	}
	expectedRevision := st.Revision
	proposal, err := r.raft.ProposeResult(ctx, command.Command{
		Kind:             command.KindUpsertNode,
		IssuedAt:         r.cfg.Now().UTC(),
		ExpectedRevision: &expectedRevision,
		Node:             &node,
	})
	if err != nil {
		return MarkNodeLeavingResult{}, err
	}
	if err := r.publishFromState(ctx); err != nil {
		return MarkNodeLeavingResult{}, err
	}
	updated, err := r.LocalState(ctx)
	if err != nil {
		return MarkNodeLeavingResult{}, err
	}
	finalNode, ok := findLifecycleNode(updated, req.NodeID)
	if !ok {
		return MarkNodeLeavingResult{}, fmt.Errorf("controllerv2: node %d not found after leaving proposal", req.NodeID)
	}
	return MarkNodeLeavingResult{Changed: proposal.Changed, Node: finalNode, Revision: updated.Revision}, nil
}

// MarkNodeRemoved marks an already-drained leaving data node as a removed tombstone.
func (r *Runtime) MarkNodeRemoved(ctx context.Context, req MarkNodeRemovedRequest) (MarkNodeRemovedResult, error) {
	if err := ctxErr(ctx); err != nil {
		return MarkNodeRemovedResult{}, err
	}
	if r == nil || r.raft == nil {
		return MarkNodeRemovedResult{}, ErrNotStarted
	}
	st, err := r.LocalState(ctx)
	if err != nil {
		return MarkNodeRemovedResult{}, err
	}
	node, changed, err := buildMarkNodeRemoved(st, req)
	if err != nil {
		return MarkNodeRemovedResult{}, err
	}
	if !changed {
		return MarkNodeRemovedResult{Changed: false, Node: node, Revision: st.Revision}, nil
	}
	if req.ExpectedRevision != 0 && st.Revision != req.ExpectedRevision {
		return MarkNodeRemovedResult{}, ErrExpectedRevisionMismatch
	}
	expectedRevision := st.Revision
	proposal, err := r.raft.ProposeResult(ctx, command.Command{
		Kind:             command.KindUpsertNode,
		IssuedAt:         r.cfg.Now().UTC(),
		ExpectedRevision: &expectedRevision,
		Node:             &node,
	})
	if err != nil {
		return MarkNodeRemovedResult{}, err
	}
	if err := r.publishFromState(ctx); err != nil {
		return MarkNodeRemovedResult{}, err
	}
	// gofail: var wkMarkNodeRemovedPostCommitFault string
	// if err := gofailMarkNodeRemovedPostCommitFault(wkMarkNodeRemovedPostCommitFault); err != nil { return MarkNodeRemovedResult{}, err }
	updated, err := r.LocalState(ctx)
	if err != nil {
		return MarkNodeRemovedResult{}, err
	}
	finalNode, ok := findLifecycleNode(updated, req.NodeID)
	if !ok {
		return MarkNodeRemovedResult{}, fmt.Errorf("controllerv2: node %d not found after removed proposal", req.NodeID)
	}
	return MarkNodeRemovedResult{Changed: proposal.Changed, Node: finalNode, Revision: updated.Revision}, nil
}

func buildJoinNode(st ClusterState, req JoinNodeRequest) (Node, bool, error) {
	if req.NodeID == 0 {
		return Node{}, false, fmt.Errorf("controllerv2: join node requires node id")
	}
	addr := strings.TrimSpace(req.Addr)
	if addr == "" {
		return Node{}, false, fmt.Errorf("controllerv2: join node requires addr")
	}
	for _, existing := range st.Nodes {
		if existing.NodeID == req.NodeID {
			if existing.Addr != addr {
				return Node{}, false, fmt.Errorf("%w: node %d already uses addr %q", ErrNodeLifecycleConflict, req.NodeID, existing.Addr)
			}
			if existing.JoinState == NodeJoinStateActive || existing.JoinState == NodeJoinStateJoining {
				return existing, false, nil
			}
			return Node{}, false, fmt.Errorf("%w: node %d is %s", ErrNodeLifecycleConflict, req.NodeID, existing.JoinState)
		}
		if existing.Addr == addr {
			return Node{}, false, fmt.Errorf("%w: addr %q already belongs to node %d", ErrNodeLifecycleConflict, addr, existing.NodeID)
		}
	}
	weight := req.CapacityWeight
	if weight == 0 {
		weight = 1
	}
	return Node{
		NodeID:         req.NodeID,
		Name:           req.Name,
		Addr:           addr,
		Roles:          normalizeJoinRoles(req.Roles),
		JoinState:      NodeJoinStateJoining,
		Status:         NodeStatusAlive,
		CapacityWeight: weight,
	}, true, nil
}

func buildActivateNode(st ClusterState, req ActivateNodeRequest) (Node, bool, error) {
	if req.NodeID == 0 {
		return Node{}, false, fmt.Errorf("controllerv2: activate node requires node id")
	}
	for _, existing := range st.Nodes {
		if existing.NodeID != req.NodeID {
			continue
		}
		if existing.JoinState == NodeJoinStateActive {
			return existing, false, nil
		}
		if existing.JoinState != NodeJoinStateJoining {
			return Node{}, false, fmt.Errorf("%w: node %d is %s", ErrNodeLifecycleConflict, req.NodeID, existing.JoinState)
		}
		node := existing
		node.JoinState = NodeJoinStateActive
		node.Status = NodeStatusAlive
		return node, true, nil
	}
	return Node{}, false, fmt.Errorf("%w: node %d", ErrNodeLifecycleNotFound, req.NodeID)
}

func buildMarkNodeLeaving(st ClusterState, req MarkNodeLeavingRequest) (Node, bool, error) {
	if req.NodeID == 0 {
		return Node{}, false, fmt.Errorf("controllerv2: mark node leaving requires node id")
	}
	for _, existing := range st.Nodes {
		if existing.NodeID != req.NodeID {
			continue
		}
		if existing.HasRole(NodeRoleControllerVoter) {
			return Node{}, false, fmt.Errorf("%w: controller voter %d cannot be marked leaving", ErrNodeLifecycleConflict, req.NodeID)
		}
		if existing.JoinState == NodeJoinStateLeaving {
			return existing, false, nil
		}
		if existing.JoinState != NodeJoinStateActive {
			return Node{}, false, fmt.Errorf("%w: node %d is %s", ErrNodeLifecycleConflict, req.NodeID, existing.JoinState)
		}
		next := existing
		next.JoinState = NodeJoinStateLeaving
		return next, true, nil
	}
	return Node{}, false, fmt.Errorf("%w: node %d", ErrNodeLifecycleNotFound, req.NodeID)
}

func buildMarkNodeRemoved(st ClusterState, req MarkNodeRemovedRequest) (Node, bool, error) {
	if req.NodeID == 0 {
		return Node{}, false, fmt.Errorf("controllerv2: mark node removed requires node id")
	}
	for _, existing := range st.Nodes {
		if existing.NodeID != req.NodeID {
			continue
		}
		if existing.HasRole(NodeRoleControllerVoter) {
			return Node{}, false, fmt.Errorf("%w: controller voter %d cannot be marked removed", ErrNodeLifecycleConflict, req.NodeID)
		}
		if existing.JoinState == NodeJoinStateRemoved {
			return existing, false, nil
		}
		if existing.JoinState != NodeJoinStateLeaving {
			return Node{}, false, fmt.Errorf("%w: node %d is %s", ErrNodeLifecycleConflict, req.NodeID, existing.JoinState)
		}
		next := existing
		next.JoinState = NodeJoinStateRemoved
		next.Status = NodeStatusDown
		return next, true, nil
	}
	return Node{}, false, fmt.Errorf("%w: node %d", ErrNodeLifecycleNotFound, req.NodeID)
}

func normalizeJoinRoles(roles []NodeRole) []NodeRole {
	for _, role := range roles {
		if role == NodeRoleData {
			return []NodeRole{NodeRoleData}
		}
	}
	return []NodeRole{NodeRoleData}
}

func findLifecycleNode(st ClusterState, nodeID uint64) (Node, bool) {
	for _, node := range st.Nodes {
		if node.NodeID == nodeID {
			return node, true
		}
	}
	return Node{}, false
}
