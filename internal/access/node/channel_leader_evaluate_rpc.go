package node

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

// ChannelLeaderEvaluateRequest asks a replica to evaluate local promotion safety.
type ChannelLeaderEvaluateRequest struct {
	// Meta is the authoritative runtime metadata used for the dry-run.
	Meta metadb.ChannelRuntimeMeta `json:"meta"`
}

// ChannelLeaderPromotionReport describes one replica's promotion evaluation.
type ChannelLeaderPromotionReport struct {
	// NodeID identifies the evaluated replica node.
	NodeID uint64 `json:"node_id"`
	// Exists reports whether local durable state exists on this node.
	Exists bool `json:"exists"`
	// ChannelEpoch echoes the evaluated channel epoch.
	ChannelEpoch uint64 `json:"channel_epoch"`
	// LocalLEO is the local durable log end offset.
	LocalLEO uint64 `json:"local_leo"`
	// LocalCheckpointHW is the local durable checkpoint high watermark.
	LocalCheckpointHW uint64 `json:"local_checkpoint_hw"`
	// LocalOffsetEpoch is the epoch that owns LocalLEO.
	LocalOffsetEpoch uint64 `json:"local_offset_epoch"`
	// CommitReadyNow reports whether the replica can accept appends immediately.
	CommitReadyNow bool `json:"commit_ready_now"`
	// ProjectedSafeHW is the largest quorum-safe prefix the replica can prove.
	ProjectedSafeHW uint64 `json:"projected_safe_hw"`
	// ProjectedTruncateTo is the offset the replica would keep after reconcile.
	ProjectedTruncateTo uint64 `json:"projected_truncate_to"`
	// CanLead reports whether the replica is safe to promote.
	CanLead bool `json:"can_lead"`
	// Reason explains why the replica cannot safely lead when CanLead is false.
	Reason string `json:"reason,omitempty"`
}

type channelLeaderEvaluateResponse struct {
	Status string                        `json:"status"`
	Report *ChannelLeaderPromotionReport `json:"report,omitempty"`
}

func (a *Adapter) handleChannelLeaderEvaluateRPC(ctx context.Context, body []byte) ([]byte, error) {
	var req ChannelLeaderEvaluateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	if a == nil || a.channelLeaderEvaluate == nil {
		return nil, fmt.Errorf("access/node: channel leader evaluator not configured")
	}
	if !containsUint64(req.Meta.ISR, a.localNodeID) {
		return encodeChannelLeaderEvaluateResponse(channelLeaderEvaluateResponse{Status: rpcStatusRejected})
	}
	report, err := a.channelLeaderEvaluate.EvaluateChannelLeaderCandidate(ctx, req)
	if err != nil {
		return nil, err
	}
	if report.NodeID == 0 {
		report.NodeID = a.localNodeID
	}
	return encodeChannelLeaderEvaluateResponse(channelLeaderEvaluateResponse{
		Status: rpcStatusOK,
		Report: &report,
	})
}

// EvaluateChannelLeaderCandidate asks one replica to dry-run a promotion.
func (c *Client) EvaluateChannelLeaderCandidate(ctx context.Context, nodeID uint64, req ChannelLeaderEvaluateRequest) (ChannelLeaderPromotionReport, error) {
	resp, err := callDirectRPC(ctx, c, nodeID, channelLeaderEvaluateRPCServiceID, req, decodeChannelLeaderEvaluateResponse)
	if err != nil {
		return ChannelLeaderPromotionReport{}, err
	}
	switch resp.Status {
	case rpcStatusOK:
		if resp.Report == nil {
			return ChannelLeaderPromotionReport{}, fmt.Errorf("access/node: missing channel leader promotion report")
		}
		return *resp.Report, nil
	case rpcStatusRejected:
		return ChannelLeaderPromotionReport{}, channel.ErrInvalidMeta
	default:
		return ChannelLeaderPromotionReport{}, fmt.Errorf("access/node: unexpected channel leader evaluate status %q", resp.Status)
	}
}

func encodeChannelLeaderEvaluateResponse(resp channelLeaderEvaluateResponse) ([]byte, error) {
	return json.Marshal(resp)
}

func decodeChannelLeaderEvaluateResponse(body []byte) (channelLeaderEvaluateResponse, error) {
	var resp channelLeaderEvaluateResponse
	err := json.Unmarshal(body, &resp)
	return resp, err
}

func containsUint64(values []uint64, target uint64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
