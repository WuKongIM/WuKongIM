package control

import cv2 "github.com/WuKongIM/WuKongIM/pkg/controller"

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
	// Changed reports whether the request changed ControllerV2 state.
	Changed bool `json:"changed"`
	// Node is the durable node record after the request.
	Node Node `json:"node"`
	// Revision is the observed ControllerV2 state revision after the write.
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

func cv2JoinNodeRequest(req JoinNodeRequest) cv2.JoinNodeRequest {
	return cv2.JoinNodeRequest{
		NodeID:         req.NodeID,
		Name:           req.Name,
		Addr:           req.Addr,
		Roles:          cv2NodeRoles(req.Roles),
		CapacityWeight: req.CapacityWeight,
	}
}

func cv2ActivateNodeRequest(req ActivateNodeRequest) cv2.ActivateNodeRequest {
	return cv2.ActivateNodeRequest{NodeID: req.NodeID}
}

func cv2MarkNodeLeavingRequest(req MarkNodeLeavingRequest) cv2.MarkNodeLeavingRequest {
	return cv2.MarkNodeLeavingRequest{NodeID: req.NodeID}
}

func cv2MarkNodeRemovedRequest(req MarkNodeRemovedRequest) cv2.MarkNodeRemovedRequest {
	return cv2.MarkNodeRemovedRequest{NodeID: req.NodeID, ExpectedRevision: req.StateRevision}
}

func joinNodeResultFromCV2(result cv2.JoinNodeResult) JoinNodeResult {
	return JoinNodeResult{
		Created:  result.Created,
		Node:     controlNodeFromControllerNode(result.Node),
		Revision: result.Revision,
	}
}

func activateNodeResultFromCV2(result cv2.ActivateNodeResult) ActivateNodeResult {
	return ActivateNodeResult{
		Changed:  result.Changed,
		Node:     controlNodeFromControllerNode(result.Node),
		Revision: result.Revision,
	}
}

func markNodeLeavingResultFromCV2(result cv2.MarkNodeLeavingResult) MarkNodeLeavingResult {
	return MarkNodeLeavingResult{
		Changed:  result.Changed,
		Node:     controlNodeFromControllerNode(result.Node),
		Revision: result.Revision,
	}
}

func markNodeRemovedResultFromCV2(result cv2.MarkNodeRemovedResult) MarkNodeRemovedResult {
	return MarkNodeRemovedResult{
		Changed:  result.Changed,
		Node:     controlNodeFromControllerNode(result.Node),
		Revision: result.Revision,
	}
}

func cv2NodeRoles(roles []Role) []cv2.NodeRole {
	for _, role := range roles {
		if role == RoleData {
			return []cv2.NodeRole{cv2.NodeRoleData}
		}
	}
	return []cv2.NodeRole{cv2.NodeRoleData}
}

func controlNodeFromControllerNode(node cv2.Node) Node {
	return Node{
		NodeID:         node.NodeID,
		Addr:           node.Addr,
		Roles:          mapControllerV2Roles(node.Roles),
		Status:         mapControllerV2Status(node.Status),
		JoinState:      mapControllerV2JoinState(node.JoinState),
		CapacityWeight: node.CapacityWeight,
	}
}
