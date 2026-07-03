package node

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/legacy/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// ChannelRetentionAdvanceRequest describes one leader-authoritative retention advance request.
type ChannelRetentionAdvanceRequest struct {
	// ChannelID identifies the channel whose retention boundary is advanced.
	ChannelID channel.ChannelID `json:"channel_id"`
	// ThroughSeq is the requested highest unavailable message sequence.
	ThroughSeq uint64 `json:"through_seq"`
	// DryRun reports the calculated outcome without mutating metadata or runtime state.
	DryRun bool `json:"dry_run,omitempty"`
}

// ChannelRetentionAdvanceResult reports one channel retention advance outcome.
type ChannelRetentionAdvanceResult struct {
	// ChannelID identifies the channel whose retention boundary was evaluated.
	ChannelID channel.ChannelID `json:"channel_id"`
	// RequestedThroughSeq is the operator-requested highest unavailable sequence.
	RequestedThroughSeq uint64 `json:"requested_through_seq"`
	// AdvancedThroughSeq is the safe boundary that was or would be advanced.
	AdvancedThroughSeq uint64 `json:"advanced_through_seq"`
	// MinAvailableSeq is the first sequence visible after the resulting boundary.
	MinAvailableSeq uint64 `json:"min_available_seq"`
	// Status is the manager-visible retention request outcome.
	Status string `json:"status"`
	// BlockedReason explains why status is blocked.
	BlockedReason string `json:"blocked_reason,omitempty"`
}

type channelRetentionRequest struct {
	Request ChannelRetentionAdvanceRequest `json:"request"`
}

type channelRetentionResponse struct {
	Status   string                        `json:"status"`
	LeaderID uint64                        `json:"leader_id,omitempty"`
	Result   ChannelRetentionAdvanceResult `json:"result,omitempty"`
}

func (r channelRetentionResponse) rpcStatus() string {
	return r.Status
}

func (r channelRetentionResponse) rpcLeaderID() uint64 {
	return r.LeaderID
}

func (a *Adapter) handleChannelRetentionRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeChannelRetentionRequest(body)
	if err != nil {
		return nil, err
	}
	if a.channelRetention == nil {
		return nil, fmt.Errorf("access/node: channel retention not configured")
	}

	meta, err := a.refreshMessageQueryMeta(ctx, req.Request.ChannelID)
	switch {
	case err != nil:
		if err == raftcluster.ErrNoLeader {
			return encodeChannelRetentionResponse(channelRetentionResponse{Status: rpcStatusNoLeader})
		}
		return nil, err
	case meta.Leader == 0:
		return encodeChannelRetentionResponse(channelRetentionResponse{Status: rpcStatusNoLeader})
	case uint64(meta.Leader) != a.localNodeID:
		return encodeChannelRetentionResponse(channelRetentionResponse{Status: rpcStatusNotLeader, LeaderID: uint64(meta.Leader)})
	}

	result, err := a.channelRetention.AdvanceChannelRetention(ctx, req.Request)
	if err != nil {
		return nil, err
	}
	return encodeChannelRetentionResponse(channelRetentionResponse{Status: rpcStatusOK, Result: result})
}

func (c *Client) AdvanceChannelRetention(ctx context.Context, nodeID uint64, req ChannelRetentionAdvanceRequest) (ChannelRetentionAdvanceResult, error) {
	if c == nil || c.cluster == nil {
		return ChannelRetentionAdvanceResult{}, fmt.Errorf("access/node: cluster not configured")
	}
	if nodeID == 0 {
		return ChannelRetentionAdvanceResult{}, channel.ErrNotLeader
	}
	body, err := encodeChannelRetentionRequestBinary(channelRetentionRequest{Request: req})
	if err != nil {
		return ChannelRetentionAdvanceResult{}, err
	}

	tried := make(map[uint64]struct{}, 2)
	candidates := []uint64{nodeID}
	var lastErr error
	for len(candidates) > 0 {
		target := candidates[0]
		candidates = candidates[1:]
		if target == 0 {
			continue
		}
		if _, ok := tried[target]; ok {
			continue
		}
		tried[target] = struct{}{}

		respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(target), 0, channelRetentionRPCServiceID, body)
		if err == nil {
			var resp channelRetentionResponse
			resp, err = decodeChannelRetentionResponse(respBody)
			if err == nil {
				switch resp.Status {
				case rpcStatusOK:
					return resp.Result, nil
				case rpcStatusNotLeader:
					lastErr = channel.ErrNotLeader
					if resp.LeaderID != 0 {
						candidates = append([]uint64{resp.LeaderID}, candidates...)
					}
					continue
				case rpcStatusNoLeader:
					lastErr = raftcluster.ErrNoLeader
					continue
				default:
					lastErr = fmt.Errorf("access/node: unexpected channel retention status %q", resp.Status)
					continue
				}
			}
		}
		if err != nil {
			lastErr = normalizeChannelMessagesRPCError(err)
			continue
		}
	}
	if lastErr != nil {
		return ChannelRetentionAdvanceResult{}, lastErr
	}
	return ChannelRetentionAdvanceResult{}, channel.ErrNotLeader
}

func encodeChannelRetentionResponse(resp channelRetentionResponse) ([]byte, error) {
	return encodeChannelRetentionResponseBinary(resp)
}

func decodeChannelRetentionResponse(body []byte) (channelRetentionResponse, error) {
	return decodeChannelRetentionResponseBinary(body)
}
