package node

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const rpcStatusUnavailable = "unavailable"

type monitorMetricsRequest struct {
	WindowSeconds int
	StepSeconds   int
}

type monitorMetricsResponse struct {
	Status string
	Result metrics.QueryResult
}

func (a *Adapter) handleMonitorMetricsRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeMonitorMetricsRequest(body)
	if err != nil {
		return nil, err
	}
	if a == nil || a.monitorMetrics == nil {
		return encodeMonitorMetricsResponse(monitorMetricsResponse{Status: rpcStatusUnavailable})
	}
	result, err := a.monitorMetrics.LocalMonitorMetrics(ctx, time.Duration(req.WindowSeconds)*time.Second, time.Duration(req.StepSeconds)*time.Second)
	if err != nil {
		return encodeMonitorMetricsResponse(monitorMetricsResponse{Status: rpcStatusUnavailable})
	}
	return encodeMonitorMetricsResponse(monitorMetricsResponse{Status: rpcStatusOK, Result: result})
}

// MonitorMetrics queries one remote node's local dashboard collector.
func (c *Client) MonitorMetrics(ctx context.Context, nodeID uint64, window, step time.Duration) (metrics.QueryResult, error) {
	if c == nil || c.cluster == nil {
		return metrics.QueryResult{}, fmt.Errorf("access/node: cluster not configured")
	}
	body, err := encodeMonitorMetricsRequestBinary(monitorMetricsRequest{
		WindowSeconds: int(window.Seconds()),
		StepSeconds:   int(step.Seconds()),
	})
	if err != nil {
		return metrics.QueryResult{}, err
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, monitorMetricsRPCServiceID, body)
	if err != nil {
		return metrics.QueryResult{}, err
	}
	resp, err := decodeMonitorMetricsResponse(respBody)
	if err != nil {
		return metrics.QueryResult{}, err
	}
	if resp.Status != rpcStatusOK {
		return metrics.QueryResult{}, metrics.ErrInsufficientData
	}
	return resp.Result, nil
}

func encodeMonitorMetricsResponse(resp monitorMetricsResponse) ([]byte, error) {
	return encodeMonitorMetricsResponseBinary(resp)
}

func decodeMonitorMetricsResponse(body []byte) (monitorMetricsResponse, error) {
	return decodeMonitorMetricsResponseBinary(body)
}
