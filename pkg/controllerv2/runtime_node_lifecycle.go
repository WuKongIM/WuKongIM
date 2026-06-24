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
	baseline, err := r.latestAppliedState(ctx, st)
	if err != nil {
		return JoinNodeResult{}, err
	}
	if baseline.Revision != st.Revision {
		node, created, err = buildJoinNode(baseline, req)
		if err != nil {
			return JoinNodeResult{}, err
		}
		if !created {
			return JoinNodeResult{Created: false, Node: node, Revision: baseline.Revision}, nil
		}
	}
	expectedRevision := baseline.Revision
	if err := r.raft.Propose(ctx, command.Command{
		Kind:             command.KindUpsertNode,
		IssuedAt:         r.cfg.Now().UTC(),
		ExpectedRevision: &expectedRevision,
		Node:             &node,
	}); err != nil {
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
	created = updated.Revision > baseline.Revision && equivalentLifecycleNode(finalNode, node)
	return JoinNodeResult{Created: created, Node: finalNode, Revision: updated.Revision}, nil
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
	baseline, err := r.latestAppliedState(ctx, st)
	if err != nil {
		return ActivateNodeResult{}, err
	}
	if baseline.Revision != st.Revision {
		node, changed, err = buildActivateNode(baseline, req)
		if err != nil {
			return ActivateNodeResult{}, err
		}
		if !changed {
			return ActivateNodeResult{Changed: false, Node: node, Revision: baseline.Revision}, nil
		}
	}
	expectedRevision := baseline.Revision
	if err := r.raft.Propose(ctx, command.Command{
		Kind:             command.KindUpsertNode,
		IssuedAt:         r.cfg.Now().UTC(),
		ExpectedRevision: &expectedRevision,
		Node:             &node,
	}); err != nil {
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
	changed = updated.Revision > baseline.Revision && equivalentLifecycleNode(finalNode, node)
	return ActivateNodeResult{Changed: changed, Node: finalNode, Revision: updated.Revision}, nil
}

func (r *Runtime) latestAppliedState(ctx context.Context, fallback ClusterState) (ClusterState, error) {
	if r.sm == nil {
		return fallback, nil
	}
	latest := r.sm.Snapshot(ctx)
	if latest.Revision <= fallback.Revision {
		return fallback, nil
	}
	if err := r.publishState(latest); err != nil {
		return ClusterState{}, err
	}
	return latest, nil
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
				return Node{}, false, fmt.Errorf("controllerv2: node %d already uses addr %q", req.NodeID, existing.Addr)
			}
			if existing.JoinState == NodeJoinStateActive || existing.JoinState == NodeJoinStateJoining {
				return existing, false, nil
			}
			return Node{}, false, fmt.Errorf("controllerv2: node %d is %s", req.NodeID, existing.JoinState)
		}
		if existing.Addr == addr {
			return Node{}, false, fmt.Errorf("controllerv2: addr %q already belongs to node %d", addr, existing.NodeID)
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
			return Node{}, false, fmt.Errorf("controllerv2: node %d is %s", req.NodeID, existing.JoinState)
		}
		node := existing
		node.JoinState = NodeJoinStateActive
		node.Status = NodeStatusAlive
		return node, true, nil
	}
	return Node{}, false, fmt.Errorf("controllerv2: node %d not found", req.NodeID)
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

func equivalentLifecycleNode(left, right Node) bool {
	if left.NodeID != right.NodeID ||
		left.Name != right.Name ||
		left.Addr != right.Addr ||
		left.JoinState != right.JoinState ||
		left.Status != right.Status ||
		left.CapacityWeight != right.CapacityWeight ||
		len(left.Roles) != len(right.Roles) {
		return false
	}
	counts := make(map[NodeRole]int, len(left.Roles))
	for _, role := range left.Roles {
		counts[role]++
	}
	for _, role := range right.Roles {
		counts[role]--
		if counts[role] < 0 {
			return false
		}
	}
	return true
}
