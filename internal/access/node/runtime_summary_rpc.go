package node

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type runtimeSummaryRequest struct {
	NodeID uint64 `json:"node_id"`
}

type runtimeSummaryResponse struct {
	Status  string         `json:"status"`
	Summary RuntimeSummary `json:"summary"`
}

func (a *Adapter) handleRuntimeSummaryRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeRuntimeSummaryRequest(body)
	if err != nil {
		return nil, err
	}
	if a == nil || a.runtimeSummary == nil {
		return encodeRuntimeSummaryResponse(runtimeSummaryResponse{
			Status:  rpcStatusOK,
			Summary: RuntimeSummary{NodeID: req.NodeID, Unknown: true},
		})
	}
	summary, err := a.runtimeSummary.LocalRuntimeSummary(ctx)
	if err != nil {
		return nil, err
	}
	if summary.NodeID == 0 {
		summary.NodeID = req.NodeID
	}
	return encodeRuntimeSummaryResponse(runtimeSummaryResponse{Status: rpcStatusOK, Summary: summary})
}

func (c *Client) RuntimeSummary(ctx context.Context, nodeID uint64) (RuntimeSummary, error) {
	if c == nil || c.cluster == nil {
		return RuntimeSummary{}, fmt.Errorf("access/node: cluster not configured")
	}
	body, err := encodeRuntimeSummaryRequestBinary(runtimeSummaryRequest{NodeID: nodeID})
	if err != nil {
		return RuntimeSummary{}, err
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, runtimeSummaryRPCServiceID, body)
	if err != nil {
		return RuntimeSummary{}, err
	}
	resp, err := decodeRuntimeSummaryResponse(respBody)
	if err != nil {
		return RuntimeSummary{}, err
	}
	return resp.Summary, nil
}

func encodeRuntimeSummaryResponse(resp runtimeSummaryResponse) ([]byte, error) {
	return encodeRuntimeSummaryResponseBinary(resp)
}

func decodeRuntimeSummaryResponse(body []byte) (runtimeSummaryResponse, error) {
	return decodeRuntimeSummaryResponseBinary(body)
}
