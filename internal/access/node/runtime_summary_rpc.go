package node

import (
	"context"
	"encoding/json"
)

type runtimeSummaryRequest struct {
	NodeID uint64 `json:"node_id"`
}

type runtimeSummaryResponse struct {
	Status  string         `json:"status"`
	Summary RuntimeSummary `json:"summary"`
}

func (a *Adapter) handleRuntimeSummaryRPC(ctx context.Context, body []byte) ([]byte, error) {
	var req runtimeSummaryRequest
	if err := json.Unmarshal(body, &req); err != nil {
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
	resp, err := callDirectRPC(ctx, c, nodeID, runtimeSummaryRPCServiceID, runtimeSummaryRequest{NodeID: nodeID}, decodeRuntimeSummaryResponse)
	if err != nil {
		return RuntimeSummary{}, err
	}
	return resp.Summary, nil
}

func encodeRuntimeSummaryResponse(resp runtimeSummaryResponse) ([]byte, error) {
	return json.Marshal(resp)
}

func decodeRuntimeSummaryResponse(body []byte) (runtimeSummaryResponse, error) {
	var resp runtimeSummaryResponse
	err := json.Unmarshal(body, &resp)
	return resp, err
}
