package management

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	// ErrNodeLifecycleUnavailable reports that node lifecycle writes are not configured.
	ErrNodeLifecycleUnavailable = errors.New("internalv2/usecase/management: node lifecycle unavailable")
	// ErrNodeLifecycleConflict reports that a node lifecycle write conflicts with cluster state.
	ErrNodeLifecycleConflict = errors.New("internalv2/usecase/management: node lifecycle conflict")
	// ErrNodeLifecycleNotFound reports that a node lifecycle write targets a missing node.
	ErrNodeLifecycleNotFound = errors.New("internalv2/usecase/management: node lifecycle node not found")
	// ErrNodeNotReadyForActivation reports that a joining node has not reached activation readiness.
	ErrNodeNotReadyForActivation = errors.New("internalv2/usecase/management: node not ready for activation")
)

// JoinNodeRequest is the manager-facing data-node join intent.
type JoinNodeRequest struct {
	// NodeID is the non-zero stable identity of the joining node.
	NodeID uint64
	// Name is an optional operator-facing node label.
	Name string
	// Addr is the stable cluster control-plane address for the node.
	Addr string
	// CapacityWeight is the optional planner placement weight passed to the control writer.
	CapacityWeight uint32
}

// JoinNodeResponse is returned after submitting or observing a node join.
type JoinNodeResponse struct {
	// Created reports whether the control writer advanced cluster state.
	Created bool
	// NodeID is the durable node identity returned by control state.
	NodeID uint64
	// Addr is the durable cluster control-plane address returned by control state.
	Addr string
	// JoinState is the durable membership lifecycle state.
	JoinState string
	// Revision is the control-state revision observed by the writer.
	Revision uint64
}

// ActivateNodeRequest is the manager-facing node activation intent.
type ActivateNodeRequest struct {
	// NodeID is the non-zero stable identity of the joining node.
	NodeID uint64
}

// ActivateNodeResponse is returned after submitting or observing node activation.
type ActivateNodeResponse struct {
	// Changed reports whether the control writer advanced cluster state.
	Changed bool
	// NodeID is the durable node identity returned by control state.
	NodeID uint64
	// Addr is the durable cluster control-plane address returned by control state.
	Addr string
	// JoinState is the durable membership lifecycle state.
	JoinState string
	// Revision is the control-state revision observed by the writer.
	Revision uint64
}

// NodeReadiness is the manager-facing activation readiness view for one node.
type NodeReadiness struct {
	// NodeID is the node identity that reported readiness.
	NodeID uint64
	// ExpectedClusterID is the cluster identity expected by the probed node.
	ExpectedClusterID string
	// MirrorClusterID is the cluster identity observed in the node's local control mirror.
	MirrorClusterID string
	// MirrorRevision is the latest control-state revision observed by the node.
	MirrorRevision uint64
	// Reachable reports whether the readiness RPC reached the target node.
	Reachable bool
	// TransportReady reports whether node transport is serving.
	TransportReady bool
	// ControlReady reports whether the target node has a usable control mirror.
	ControlReady bool
	// RuntimeReady reports whether local app runtimes required for activation are ready.
	RuntimeReady bool
	// Unknown reports that readiness could not be determined.
	Unknown bool
	// LastError carries a compact diagnostic for failed readiness checks.
	LastError string
}

// JoinNode validates and submits a data-node join intent.
func (a *App) JoinNode(ctx context.Context, req JoinNodeRequest) (JoinNodeResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return JoinNodeResponse{}, err
	}
	addr := strings.TrimSpace(req.Addr)
	if req.NodeID == 0 || addr == "" {
		return JoinNodeResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.nodeLifecycle == nil {
		return JoinNodeResponse{}, ErrNodeLifecycleUnavailable
	}
	result, err := a.nodeLifecycle.JoinNode(ctx, control.JoinNodeRequest{
		NodeID:         req.NodeID,
		Name:           strings.TrimSpace(req.Name),
		Addr:           addr,
		Roles:          []control.Role{control.RoleData},
		CapacityWeight: req.CapacityWeight,
	})
	if err != nil {
		return JoinNodeResponse{}, mapNodeLifecycleError(err)
	}
	return JoinNodeResponse{
		Created:   result.Created,
		NodeID:    result.Node.NodeID,
		Addr:      result.Node.Addr,
		JoinState: string(result.Node.JoinState),
		Revision:  result.Revision,
	}, nil
}

// ActivateNode validates and submits a node activation intent.
func (a *App) ActivateNode(ctx context.Context, req ActivateNodeRequest) (ActivateNodeResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return ActivateNodeResponse{}, err
	}
	if req.NodeID == 0 {
		return ActivateNodeResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.nodeLifecycle == nil {
		return ActivateNodeResponse{}, ErrNodeLifecycleUnavailable
	}
	snapshot, err := a.activationControlSnapshot(ctx)
	if err != nil {
		return ActivateNodeResponse{}, err
	}
	node, ok := findControlNode(snapshot, req.NodeID)
	if !ok {
		return ActivateNodeResponse{}, ErrNodeLifecycleNotFound
	}
	if node.JoinState != control.NodeJoinStateJoining {
		return ActivateNodeResponse{}, ErrNodeLifecycleConflict
	}
	readiness, err := a.activationNodeReadiness(ctx, req.NodeID)
	if err != nil {
		return ActivateNodeResponse{}, err
	}
	if !nodeReadyForActivation(readiness, req.NodeID, snapshot.Revision) {
		return ActivateNodeResponse{}, fmt.Errorf("%w: %s", ErrNodeNotReadyForActivation, readiness.LastError)
	}
	result, err := a.nodeLifecycle.ActivateNode(ctx, control.ActivateNodeRequest{NodeID: req.NodeID})
	if err != nil {
		return ActivateNodeResponse{}, mapNodeLifecycleError(err)
	}
	return ActivateNodeResponse{
		Changed:   result.Changed,
		NodeID:    result.Node.NodeID,
		Addr:      result.Node.Addr,
		JoinState: string(result.Node.JoinState),
		Revision:  result.Revision,
	}, nil
}

func (a *App) activationControlSnapshot(ctx context.Context) (control.Snapshot, error) {
	if a == nil || a.cluster == nil {
		return control.Snapshot{}, ErrNodeLifecycleUnavailable
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return control.Snapshot{}, err
	}
	return snapshot, nil
}

func (a *App) activationNodeReadiness(ctx context.Context, nodeID uint64) (NodeReadiness, error) {
	if a == nil || a.nodeReadiness == nil {
		return NodeReadiness{}, ErrNodeLifecycleUnavailable
	}
	readiness, err := a.nodeReadiness.NodeReadiness(ctx, nodeID)
	if err != nil {
		return NodeReadiness{}, fmt.Errorf("%w: %v", ErrNodeNotReadyForActivation, err)
	}
	return readiness, nil
}

func findControlNode(snapshot control.Snapshot, nodeID uint64) (control.Node, bool) {
	for _, node := range snapshot.Nodes {
		if node.NodeID == nodeID {
			return node, true
		}
	}
	return control.Node{}, false
}

func nodeReadyForActivation(readiness NodeReadiness, expectedNodeID uint64, minRevision uint64) bool {
	expectedClusterID := strings.TrimSpace(readiness.ExpectedClusterID)
	mirrorClusterID := strings.TrimSpace(readiness.MirrorClusterID)
	switch {
	case readiness.Unknown:
		return false
	case readiness.NodeID != expectedNodeID:
		return false
	case !readiness.Reachable || !readiness.TransportReady || !readiness.ControlReady || !readiness.RuntimeReady:
		return false
	case expectedClusterID == "" || mirrorClusterID == "" || expectedClusterID != mirrorClusterID:
		return false
	case readiness.MirrorRevision < minRevision:
		return false
	default:
		return true
	}
}

func mapNodeLifecycleError(err error) error {
	switch {
	case errors.Is(err, cv2.ErrNotLeader),
		errors.Is(err, cv2.ErrNotStarted),
		errors.Is(err, cv2.ErrStopped):
		return fmt.Errorf("%w: %w", ErrNodeLifecycleUnavailable, err)
	case errors.Is(err, cv2.ErrNodeLifecycleConflict):
		return fmt.Errorf("%w: %v", ErrNodeLifecycleConflict, err)
	case errors.Is(err, cv2.ErrNodeLifecycleNotFound):
		return fmt.Errorf("%w: %v", ErrNodeLifecycleNotFound, err)
	default:
		return err
	}
}
