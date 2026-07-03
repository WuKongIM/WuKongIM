package control

import controller "github.com/WuKongIM/WuKongIM/pkg/controller"

// JoinNodeRequest describes a data-node join intent.
type JoinNodeRequest struct {
	// NodeID is the non-zero stable identity of the joining node.
	NodeID uint64 `json:"node_id"`
	// Name is an optional human-readable label for the node.
	Name string `json:"name,omitempty"`
	// Addr is the stable control-plane address for the node.
	Addr string `json:"addr"`
	// Roles is the requested durable capability set.
	Roles []Role `json:"roles,omitempty"`
	// CapacityWeight is the planner placement weight; zero defaults to one.
	CapacityWeight uint32 `json:"capacity_weight,omitempty"`
}

// JoinNodeResult describes the durable node record observed or created by JoinNode.
type JoinNodeResult struct {
	// Created reports whether JoinNode actually advanced cluster state.
	Created bool `json:"created"`
	// Node is the durable node record that satisfies the request.
	Node Node `json:"node"`
	// Revision is the cluster-state revision observed by the method.
	Revision uint64 `json:"revision"`
}

// ActivateNodeRequest describes a request to make a joining node assignment-ready.
type ActivateNodeRequest struct {
	// NodeID is the non-zero stable identity of the joining node.
	NodeID uint64 `json:"node_id"`
}

// ActivateNodeResult describes the durable node record observed or activated by ActivateNode.
type ActivateNodeResult struct {
	// Changed reports whether ActivateNode actually advanced cluster state.
	Changed bool `json:"changed"`
	// Node is the durable node record that satisfies the request.
	Node Node `json:"node"`
	// Revision is the cluster-state revision observed by the method.
	Revision uint64 `json:"revision"`
}

// MarkNodeLeavingRequest identifies a data node that should stop receiving new assignments.
type MarkNodeLeavingRequest struct {
	// NodeID is the stable node identity to mark leaving.
	NodeID uint64 `json:"node_id"`
}

// MarkNodeLeavingResult describes the node record after the transition.
type MarkNodeLeavingResult struct {
	// Changed reports whether the request changed Controller state.
	Changed bool `json:"changed"`
	// Node is the durable node record after the request.
	Node Node `json:"node"`
	// Revision is the observed Controller state revision after the write.
	Revision uint64 `json:"revision"`
}

// MarkNodeRemovedRequest identifies a leaving node that should become a removed tombstone.
type MarkNodeRemovedRequest struct {
	// NodeID is the stable node identity to mark removed.
	NodeID uint64 `json:"node_id"`
	// StateRevision fences the remove write to the safety-check control revision.
	StateRevision uint64 `json:"state_revision,omitempty"`
}

// MarkNodeRemovedResult describes the removed node record.
type MarkNodeRemovedResult struct {
	// Changed reports whether control state changed.
	Changed bool `json:"changed"`
	// Node is the durable node record after the request.
	Node Node `json:"node"`
	// Revision is the observed control-state revision.
	Revision uint64 `json:"revision"`
}

func controllerJoinNodeRequest(req JoinNodeRequest) controller.JoinNodeRequest {
	return controller.JoinNodeRequest{
		NodeID:         req.NodeID,
		Name:           req.Name,
		Addr:           req.Addr,
		Roles:          controllerNodeRoles(req.Roles),
		CapacityWeight: req.CapacityWeight,
	}
}

func controllerActivateNodeRequest(req ActivateNodeRequest) controller.ActivateNodeRequest {
	return controller.ActivateNodeRequest{NodeID: req.NodeID}
}

func controllerMarkNodeLeavingRequest(req MarkNodeLeavingRequest) controller.MarkNodeLeavingRequest {
	return controller.MarkNodeLeavingRequest{NodeID: req.NodeID}
}

func controllerMarkNodeRemovedRequest(req MarkNodeRemovedRequest) controller.MarkNodeRemovedRequest {
	return controller.MarkNodeRemovedRequest{NodeID: req.NodeID, ExpectedRevision: req.StateRevision}
}

func joinNodeResultFromController(result controller.JoinNodeResult) JoinNodeResult {
	return JoinNodeResult{
		Created:  result.Created,
		Node:     controlNodeFromControllerNode(result.Node),
		Revision: result.Revision,
	}
}

func activateNodeResultFromController(result controller.ActivateNodeResult) ActivateNodeResult {
	return ActivateNodeResult{
		Changed:  result.Changed,
		Node:     controlNodeFromControllerNode(result.Node),
		Revision: result.Revision,
	}
}

func markNodeLeavingResultFromController(result controller.MarkNodeLeavingResult) MarkNodeLeavingResult {
	return MarkNodeLeavingResult{
		Changed:  result.Changed,
		Node:     controlNodeFromControllerNode(result.Node),
		Revision: result.Revision,
	}
}

func markNodeRemovedResultFromController(result controller.MarkNodeRemovedResult) MarkNodeRemovedResult {
	return MarkNodeRemovedResult{
		Changed:  result.Changed,
		Node:     controlNodeFromControllerNode(result.Node),
		Revision: result.Revision,
	}
}

func controllerNodeRoles(roles []Role) []controller.NodeRole {
	for _, role := range roles {
		if role == RoleData {
			return []controller.NodeRole{controller.NodeRoleData}
		}
	}
	return []controller.NodeRole{controller.NodeRoleData}
}

func controlNodeFromControllerNode(node controller.Node) Node {
	return Node{
		NodeID:         node.NodeID,
		Addr:           node.Addr,
		Roles:          mapControllerRoles(node.Roles),
		Status:         mapControllerStatus(node.Status),
		JoinState:      mapControllerJoinState(node.JoinState),
		CapacityWeight: node.CapacityWeight,
	}
}
