package node

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// ManagerDiagnosticsRPCServiceID is the cluster RPC service for node-local manager diagnostics.
const ManagerDiagnosticsRPCServiceID uint8 = clusternet.RPCManagerDiagnostics

// HandleManagerDiagnosticsRPC handles one encoded manager diagnostics RPC payload.
func (a *Adapter) HandleManagerDiagnosticsRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeManagerDiagnosticsRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("manager diagnostics rpc decode failed",
			wklog.Event("internalv2.access.node.manager_diagnostics_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.managerDiagnostics == nil {
		return encodeManagerDiagnosticsResponse(managerDiagnosticsRPCResponse{
			Status: rpcStatusRejected,
			Error:  "manager diagnostics is not configured on this node",
		})
	}
	switch req.Op {
	case managerDiagnosticsOpQuery:
		return encodeManagerDiagnosticsResponse(managerDiagnosticsRPCResponse{
			Status: rpcStatusOK,
			Result: a.managerDiagnostics.QueryDiagnostics(ctx, req.Query),
		})
	case managerDiagnosticsOpAdd:
		rule, err := a.managerDiagnostics.AddDiagnosticsTrackingRule(ctx, req.Rule)
		if err != nil {
			return encodeManagerDiagnosticsResponse(managerDiagnosticsRPCResponse{Status: rpcStatusRejected, Error: err.Error()})
		}
		return encodeManagerDiagnosticsResponse(managerDiagnosticsRPCResponse{Status: rpcStatusOK, Rule: rule})
	case managerDiagnosticsOpList:
		rules, err := a.managerDiagnostics.ListDiagnosticsTrackingRules(ctx)
		if err != nil {
			return encodeManagerDiagnosticsResponse(managerDiagnosticsRPCResponse{Status: rpcStatusRejected, Error: err.Error()})
		}
		return encodeManagerDiagnosticsResponse(managerDiagnosticsRPCResponse{Status: rpcStatusOK, Rules: rules})
	case managerDiagnosticsOpDelete:
		if err := a.managerDiagnostics.DeleteDiagnosticsTrackingRule(ctx, req.RuleID); err != nil {
			return encodeManagerDiagnosticsResponse(managerDiagnosticsRPCResponse{Status: rpcStatusRejected, Error: err.Error()})
		}
		return encodeManagerDiagnosticsResponse(managerDiagnosticsRPCResponse{Status: rpcStatusOK})
	default:
		err := fmt.Errorf("internalv2/access/node: unknown manager diagnostics op %q", req.Op)
		a.rpcLogger().Warn("manager diagnostics rpc unknown operation",
			wklog.Event("internalv2.access.node.manager_diagnostics_unknown_op"),
			wklog.String("op", req.Op),
			wklog.Error(err),
		)
		return nil, err
	}
}

// QueryManagerDiagnostics queries retained diagnostics events on a remote node.
func (c *Client) QueryManagerDiagnostics(ctx context.Context, nodeID uint64, query diagnostics.Query) (diagnostics.QueryResult, error) {
	resp, err := c.callManagerDiagnostics(ctx, nodeID, managerDiagnosticsRPCRequest{Op: managerDiagnosticsOpQuery, Query: query})
	if err != nil {
		return diagnostics.QueryResult{}, err
	}
	if err := managerDiagnosticsRPCErrorForStatus(resp); err != nil {
		return diagnostics.QueryResult{}, err
	}
	return resp.Result, nil
}

// AddManagerDiagnosticsTrackingRule installs one diagnostics tracking rule on a remote node.
func (c *Client) AddManagerDiagnosticsTrackingRule(ctx context.Context, nodeID uint64, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error) {
	resp, err := c.callManagerDiagnostics(ctx, nodeID, managerDiagnosticsRPCRequest{Op: managerDiagnosticsOpAdd, Rule: input})
	if err != nil {
		return diagnostics.TrackingRule{}, err
	}
	if err := managerDiagnosticsRPCErrorForStatus(resp); err != nil {
		return diagnostics.TrackingRule{}, err
	}
	return resp.Rule, nil
}

// ListManagerDiagnosticsTrackingRules returns active diagnostics tracking rules on a remote node.
func (c *Client) ListManagerDiagnosticsTrackingRules(ctx context.Context, nodeID uint64) ([]diagnostics.TrackingRule, error) {
	resp, err := c.callManagerDiagnostics(ctx, nodeID, managerDiagnosticsRPCRequest{Op: managerDiagnosticsOpList})
	if err != nil {
		return nil, err
	}
	if err := managerDiagnosticsRPCErrorForStatus(resp); err != nil {
		return nil, err
	}
	return resp.Rules, nil
}

// DeleteManagerDiagnosticsTrackingRule removes one diagnostics tracking rule on a remote node.
func (c *Client) DeleteManagerDiagnosticsTrackingRule(ctx context.Context, nodeID uint64, ruleID string) error {
	resp, err := c.callManagerDiagnostics(ctx, nodeID, managerDiagnosticsRPCRequest{Op: managerDiagnosticsOpDelete, RuleID: ruleID})
	if err != nil {
		return err
	}
	return managerDiagnosticsRPCErrorForStatus(resp)
}

func (c *Client) callManagerDiagnostics(ctx context.Context, nodeID uint64, req managerDiagnosticsRPCRequest) (managerDiagnosticsRPCResponse, error) {
	if c == nil || c.node == nil {
		return managerDiagnosticsRPCResponse{}, fmt.Errorf("internalv2/access/node: manager diagnostics rpc client not configured")
	}
	body, err := encodeManagerDiagnosticsRequest(req)
	if err != nil {
		return managerDiagnosticsRPCResponse{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, ManagerDiagnosticsRPCServiceID, body)
	if err != nil {
		return managerDiagnosticsRPCResponse{}, err
	}
	return decodeManagerDiagnosticsResponse(respBody)
}

func managerDiagnosticsRPCErrorForStatus(resp managerDiagnosticsRPCResponse) error {
	if resp.Status == rpcStatusOK {
		return nil
	}
	if resp.Error != "" {
		return fmt.Errorf("internalv2/access/node: manager diagnostics status %q: %s", resp.Status, resp.Error)
	}
	return fmt.Errorf("internalv2/access/node: manager diagnostics status %q", resp.Status)
}
