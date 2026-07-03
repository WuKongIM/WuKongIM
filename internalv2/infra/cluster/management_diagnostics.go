package cluster

import (
	"context"
	"fmt"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	"github.com/WuKongIM/WuKongIM/internalv2/observability/diagnostics"
)

// ManagementDiagnosticsRPCNode exposes cluster node RPC for manager diagnostics reads and tracking rules.
type ManagementDiagnosticsRPCNode interface {
	// NodeID returns the local cluster node ID.
	NodeID() uint64
	// CallRPC invokes one typed node RPC service on a peer node.
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
}

// LocalDiagnostics reads and mutates diagnostics state on this node.
type LocalDiagnostics interface {
	// QueryDiagnostics returns retained node-local diagnostics events.
	QueryDiagnostics(context.Context, diagnostics.Query) diagnostics.QueryResult
	// AddDiagnosticsTrackingRule installs one local-node diagnostics tracking rule.
	AddDiagnosticsTrackingRule(context.Context, diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error)
	// ListDiagnosticsTrackingRules returns active local-node diagnostics tracking rules.
	ListDiagnosticsTrackingRules(context.Context) ([]diagnostics.TrackingRule, error)
	// DeleteDiagnosticsTrackingRule removes one local-node diagnostics tracking rule.
	DeleteDiagnosticsTrackingRule(context.Context, string) error
}

// ManagementDiagnosticsReader routes manager diagnostics operations to local state or selected peer nodes.
type ManagementDiagnosticsReader struct {
	node   ManagementDiagnosticsRPCNode
	local  LocalDiagnostics
	remote *accessnode.Client
}

// NewManagementDiagnosticsReader creates a cluster-routed manager diagnostics reader/operator.
func NewManagementDiagnosticsReader(node ManagementDiagnosticsRPCNode, local LocalDiagnostics) *ManagementDiagnosticsReader {
	return &ManagementDiagnosticsReader{
		node:   node,
		local:  local,
		remote: accessnode.NewClient(node),
	}
}

// QueryNodeDiagnostics returns diagnostics events from exactly one selected node.
func (r *ManagementDiagnosticsReader) QueryNodeDiagnostics(ctx context.Context, nodeID uint64, query diagnostics.Query) (diagnostics.QueryResult, error) {
	if r == nil {
		return diagnostics.QueryResult{}, errManagementDiagnosticsUnavailable()
	}
	if r.isLocal(nodeID) {
		if r.local == nil {
			return diagnostics.QueryResult{}, errManagementDiagnosticsUnavailable()
		}
		return r.local.QueryDiagnostics(ctx, query), nil
	}
	if r.remote == nil {
		return diagnostics.QueryResult{}, errManagementDiagnosticsUnavailable()
	}
	return r.remote.QueryManagerDiagnostics(ctx, nodeID, query)
}

// AddNodeDiagnosticsTrackingRule installs one diagnostics tracking rule on exactly one selected node.
func (r *ManagementDiagnosticsReader) AddNodeDiagnosticsTrackingRule(ctx context.Context, nodeID uint64, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error) {
	if r == nil {
		return diagnostics.TrackingRule{}, errManagementDiagnosticsUnavailable()
	}
	if r.isLocal(nodeID) {
		if r.local == nil {
			return diagnostics.TrackingRule{}, errManagementDiagnosticsUnavailable()
		}
		return r.local.AddDiagnosticsTrackingRule(ctx, input)
	}
	if r.remote == nil {
		return diagnostics.TrackingRule{}, errManagementDiagnosticsUnavailable()
	}
	return r.remote.AddManagerDiagnosticsTrackingRule(ctx, nodeID, input)
}

// ListNodeDiagnosticsTrackingRules returns active diagnostics tracking rules from exactly one selected node.
func (r *ManagementDiagnosticsReader) ListNodeDiagnosticsTrackingRules(ctx context.Context, nodeID uint64) ([]diagnostics.TrackingRule, error) {
	if r == nil {
		return nil, errManagementDiagnosticsUnavailable()
	}
	if r.isLocal(nodeID) {
		if r.local == nil {
			return nil, errManagementDiagnosticsUnavailable()
		}
		return r.local.ListDiagnosticsTrackingRules(ctx)
	}
	if r.remote == nil {
		return nil, errManagementDiagnosticsUnavailable()
	}
	return r.remote.ListManagerDiagnosticsTrackingRules(ctx, nodeID)
}

// DeleteNodeDiagnosticsTrackingRule removes one diagnostics tracking rule from exactly one selected node.
func (r *ManagementDiagnosticsReader) DeleteNodeDiagnosticsTrackingRule(ctx context.Context, nodeID uint64, ruleID string) error {
	if r == nil {
		return errManagementDiagnosticsUnavailable()
	}
	if r.isLocal(nodeID) {
		if r.local == nil {
			return errManagementDiagnosticsUnavailable()
		}
		return r.local.DeleteDiagnosticsTrackingRule(ctx, ruleID)
	}
	if r.remote == nil {
		return errManagementDiagnosticsUnavailable()
	}
	return r.remote.DeleteManagerDiagnosticsTrackingRule(ctx, nodeID, ruleID)
}

func (r *ManagementDiagnosticsReader) isLocal(nodeID uint64) bool {
	return nodeID == 0 || (r.node != nil && nodeID == r.node.NodeID())
}

func errManagementDiagnosticsUnavailable() error {
	return fmt.Errorf("internalv2/infra/cluster: management diagnostics unavailable")
}
