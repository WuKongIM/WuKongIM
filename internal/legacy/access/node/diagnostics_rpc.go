package node

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/legacy/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type diagnosticsRequest struct {
	Query diagnostics.Query
}

type diagnosticsResponse struct {
	Status string
	Result diagnostics.QueryResult
}

func (a *Adapter) handleDiagnosticsRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeDiagnosticsRequest(body)
	if err != nil {
		return nil, err
	}
	if a == nil || a.diagnostics == nil {
		return encodeDiagnosticsResponse(diagnosticsResponse{
			Status: rpcStatusOK,
			Result: diagnostics.QueryResult{
				Scope:  "local_node",
				Query:  req.Query,
				Status: diagnostics.StatusNotFound,
				Events: []diagnostics.Event{},
				Notes:  []string{"diagnostics store is disabled on this node"},
			},
		})
	}
	return encodeDiagnosticsResponse(diagnosticsResponse{
		Status: rpcStatusOK,
		Result: a.diagnostics.QueryDiagnostics(ctx, req.Query),
	})
}

// QueryDiagnostics queries retained diagnostics events from a remote cluster node.
func (c *Client) QueryDiagnostics(ctx context.Context, nodeID uint64, query diagnostics.Query) (diagnostics.QueryResult, error) {
	if c == nil || c.cluster == nil {
		return diagnostics.QueryResult{}, fmt.Errorf("access/node: cluster not configured")
	}
	body, err := encodeDiagnosticsRequestBinary(diagnosticsRequest{Query: query})
	if err != nil {
		return diagnostics.QueryResult{}, err
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, diagnosticsRPCServiceID, body)
	if err != nil {
		return diagnostics.QueryResult{}, err
	}
	resp, err := decodeDiagnosticsResponse(respBody)
	if err != nil {
		return diagnostics.QueryResult{}, err
	}
	if resp.Status != rpcStatusOK {
		return diagnostics.QueryResult{}, fmt.Errorf("access/node: unexpected diagnostics status %q", resp.Status)
	}
	return resp.Result, nil
}
