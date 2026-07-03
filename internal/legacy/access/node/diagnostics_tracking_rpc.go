package node

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/legacy/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func (a *Adapter) handleDiagnosticsTrackingRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeDiagnosticsTrackingRequest(body)
	if err != nil {
		return nil, err
	}
	if a == nil || a.diagnosticsTracking == nil {
		return encodeDiagnosticsTrackingResponse(diagnosticsTrackingResponse{
			Status: rpcStatusRejected,
			Error:  "diagnostics tracking is not configured on this node",
		})
	}
	switch req.Op {
	case diagnosticsTrackingOpAdd:
		rule, err := a.diagnosticsTracking.AddDiagnosticsTrackingRule(ctx, req.Rule)
		if err != nil {
			return encodeDiagnosticsTrackingResponse(diagnosticsTrackingResponse{Status: rpcStatusRejected, Error: err.Error()})
		}
		return encodeDiagnosticsTrackingResponse(diagnosticsTrackingResponse{Status: rpcStatusOK, Rule: rule})
	case diagnosticsTrackingOpList:
		rules, err := a.diagnosticsTracking.ListDiagnosticsTrackingRules(ctx)
		if err != nil {
			return encodeDiagnosticsTrackingResponse(diagnosticsTrackingResponse{Status: rpcStatusRejected, Error: err.Error()})
		}
		return encodeDiagnosticsTrackingResponse(diagnosticsTrackingResponse{Status: rpcStatusOK, Rules: rules})
	case diagnosticsTrackingOpDelete:
		if err := a.diagnosticsTracking.DeleteDiagnosticsTrackingRule(ctx, req.RuleID); err != nil {
			return encodeDiagnosticsTrackingResponse(diagnosticsTrackingResponse{Status: rpcStatusRejected, Error: err.Error()})
		}
		return encodeDiagnosticsTrackingResponse(diagnosticsTrackingResponse{Status: rpcStatusOK})
	default:
		return nil, fmt.Errorf("access/node: unknown diagnostics tracking op %q", req.Op)
	}
}

// AddDiagnosticsTrackingRule installs one diagnostics tracking rule on a remote node.
func (c *Client) AddDiagnosticsTrackingRule(ctx context.Context, nodeID uint64, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error) {
	resp, err := c.callDiagnosticsTracking(ctx, nodeID, diagnosticsTrackingRequest{Op: diagnosticsTrackingOpAdd, Rule: input})
	if err != nil {
		return diagnostics.TrackingRule{}, err
	}
	return resp.Rule, nil
}

// ListDiagnosticsTrackingRules returns active diagnostics tracking rules on a remote node.
func (c *Client) ListDiagnosticsTrackingRules(ctx context.Context, nodeID uint64) ([]diagnostics.TrackingRule, error) {
	resp, err := c.callDiagnosticsTracking(ctx, nodeID, diagnosticsTrackingRequest{Op: diagnosticsTrackingOpList})
	if err != nil {
		return nil, err
	}
	return resp.Rules, nil
}

// DeleteDiagnosticsTrackingRule removes one diagnostics tracking rule on a remote node.
func (c *Client) DeleteDiagnosticsTrackingRule(ctx context.Context, nodeID uint64, ruleID string) error {
	_, err := c.callDiagnosticsTracking(ctx, nodeID, diagnosticsTrackingRequest{Op: diagnosticsTrackingOpDelete, RuleID: ruleID})
	return err
}

func (c *Client) callDiagnosticsTracking(ctx context.Context, nodeID uint64, req diagnosticsTrackingRequest) (diagnosticsTrackingResponse, error) {
	if c == nil || c.cluster == nil {
		return diagnosticsTrackingResponse{}, fmt.Errorf("access/node: cluster not configured")
	}
	body, err := encodeDiagnosticsTrackingRequest(req)
	if err != nil {
		return diagnosticsTrackingResponse{}, err
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, diagnosticsTrackingRPCServiceID, body)
	if err != nil {
		return diagnosticsTrackingResponse{}, err
	}
	resp, err := decodeDiagnosticsTrackingResponse(respBody)
	if err != nil {
		return diagnosticsTrackingResponse{}, err
	}
	if resp.Status != rpcStatusOK {
		if resp.Error != "" {
			return diagnosticsTrackingResponse{}, fmt.Errorf("access/node: diagnostics tracking status %q: %s", resp.Status, resp.Error)
		}
		return diagnosticsTrackingResponse{}, fmt.Errorf("access/node: diagnostics tracking status %q", resp.Status)
	}
	return resp, nil
}
