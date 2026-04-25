package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

import "github.com/WuKongIM/WuKongIM/pkg/channel"
import "github.com/WuKongIM/WuKongIM/pkg/wklog"

type channelAppendRequest struct {
	AppendRequest channel.AppendRequest `json:"append_request"`
}

type channelAppendResponse struct {
	Status   string               `json:"status"`
	LeaderID uint64               `json:"leader_id,omitempty"`
	Result   channel.AppendResult `json:"result,omitempty"`
}

func (r channelAppendResponse) rpcStatus() string {
	return r.Status
}

func (r channelAppendResponse) rpcLeaderID() uint64 {
	return r.LeaderID
}

func (a *Adapter) handleChannelAppendRPC(ctx context.Context, body []byte) ([]byte, error) {
	var req channelAppendRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	if a.channelLog == nil {
		return nil, fmt.Errorf("access/node: channel log not configured")
	}

	if body, handled, err := a.handleChannelLeaderRedirect(req.AppendRequest.ChannelID); handled || err != nil {
		return body, err
	}

	result, err := a.channelLog.Append(ctx, req.AppendRequest)
	if err == nil {
		return encodeChannelAppendResponse(channelAppendResponse{
			Status: rpcStatusOK,
			Result: result,
		})
	}
	if errors.Is(err, channel.ErrNotLeader) || errors.Is(err, channel.ErrStaleMeta) {
		if refreshed, refreshErr := a.refreshChannelAppendMeta(ctx, req.AppendRequest.ChannelID); refreshErr == nil {
			a.channelAppendLogger().Debug("resolved refreshed channel append metadata",
				wklog.Event("access.node.channel_append.refresh.resolved"),
				wklog.NodeID(a.localNodeID),
				wklog.LeaderNodeID(uint64(refreshed.Leader)),
				wklog.ChannelID(req.AppendRequest.ChannelID.ID),
				wklog.ChannelType(int64(req.AppendRequest.ChannelID.Type)),
				wklog.Uint64("channelEpoch", refreshed.Epoch),
				wklog.Uint64("leaderEpoch", refreshed.LeaderEpoch),
				wklog.Int("replicaCount", len(refreshed.Replicas)),
				wklog.Int("isrCount", len(refreshed.ISR)),
				wklog.Int("minISR", refreshed.MinISR),
			)
			req.AppendRequest.ExpectedChannelEpoch = refreshed.Epoch
			req.AppendRequest.ExpectedLeaderEpoch = refreshed.LeaderEpoch
			if refreshed.Leader != 0 && uint64(refreshed.Leader) != a.localNodeID {
				return encodeChannelAppendResponse(channelAppendResponse{
					Status:   rpcStatusNotLeader,
					LeaderID: uint64(refreshed.Leader),
				})
			}
			result, err = a.channelLog.Append(ctx, req.AppendRequest)
			if err == nil {
				return encodeChannelAppendResponse(channelAppendResponse{
					Status: rpcStatusOK,
					Result: result,
				})
			}
		}
	}
	if errors.Is(err, channel.ErrNotLeader) {
		if body, handled, statusErr := a.handleChannelLeaderRedirect(req.AppendRequest.ChannelID); handled || statusErr != nil {
			return body, statusErr
		}
		return encodeChannelAppendResponse(channelAppendResponse{Status: rpcStatusNotLeader})
	}
	return nil, err
}

func (a *Adapter) channelAppendLogger() wklog.Logger {
	if a == nil || a.logger == nil {
		return wklog.NewNop()
	}
	return a.logger.Named("channel_append")
}

func (a *Adapter) handleChannelLeaderRedirect(id channel.ChannelID) ([]byte, bool, error) {
	if a == nil || a.channelLog == nil || a.localNodeID == 0 {
		return nil, false, nil
	}
	status, err := a.channelLog.Status(id)
	if err != nil || status.Leader == 0 {
		return nil, false, nil
	}
	if uint64(status.Leader) == a.localNodeID {
		return nil, false, nil
	}
	body, encodeErr := encodeChannelAppendResponse(channelAppendResponse{
		Status:   rpcStatusNotLeader,
		LeaderID: uint64(status.Leader),
	})
	return body, true, encodeErr
}

func (a *Adapter) refreshChannelAppendMeta(ctx context.Context, id channel.ChannelID) (channel.Meta, error) {
	if a == nil || a.channelMeta == nil {
		return channel.Meta{}, fmt.Errorf("access/node: channel meta refresher not configured")
	}
	return a.channelMeta.RefreshChannelMeta(ctx, id)
}

func (c *Client) AppendToLeader(ctx context.Context, nodeID uint64, req channel.AppendRequest) (channel.AppendResult, error) {
	if c.cluster == nil {
		return channel.AppendResult{}, fmt.Errorf("access/node: cluster not configured")
	}
	if nodeID == 0 {
		return channel.AppendResult{}, channel.ErrNotLeader
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

		resp, err := callDirectRPC(ctx, c, target, channelAppendRPCServiceID, channelAppendRequest{
			AppendRequest: req,
		}, decodeChannelAppendResponse)
		if err != nil {
			lastErr = normalizeChannelAppendRPCError(err)
			continue
		}

		switch resp.Status {
		case rpcStatusOK:
			return resp.Result, nil
		case rpcStatusNotLeader:
			lastErr = channel.ErrNotLeader
			if resp.LeaderID != 0 {
				candidates = append([]uint64{resp.LeaderID}, candidates...)
			}
		default:
			lastErr = fmt.Errorf("access/node: unexpected channel append status %q", resp.Status)
		}
	}

	if lastErr != nil {
		return channel.AppendResult{}, lastErr
	}
	return channel.AppendResult{}, channel.ErrNotLeader
}

func encodeChannelAppendResponse(resp channelAppendResponse) ([]byte, error) {
	return json.Marshal(resp)
}

func decodeChannelAppendResponse(body []byte) (channelAppendResponse, error) {
	var resp channelAppendResponse
	err := json.Unmarshal(body, &resp)
	return resp, err
}

func normalizeChannelAppendRPCError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, channel.ErrNotLeader) || errors.Is(err, channel.ErrStaleMeta) {
		return err
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, channel.ErrNotLeader.Error()):
		return channel.ErrNotLeader
	case strings.Contains(msg, channel.ErrStaleMeta.Error()):
		return channel.ErrStaleMeta
	default:
		return err
	}
}
