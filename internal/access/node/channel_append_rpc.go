package node

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type channelAppendRequest struct {
	AppendRequest channel.AppendRequest `json:"append_request"`
}

type channelAppendBatchRequest struct {
	AppendBatchRequest channel.AppendBatchRequest `json:"append_batch_request"`
}

type channelAppendResponse struct {
	Status   string               `json:"status"`
	LeaderID uint64               `json:"leader_id,omitempty"`
	Result   channel.AppendResult `json:"result,omitempty"`
}

type channelAppendBatchResponse struct {
	Status   string                    `json:"status"`
	LeaderID uint64                    `json:"leader_id,omitempty"`
	Result   channel.AppendBatchResult `json:"result,omitempty"`
}

func (r channelAppendResponse) rpcStatus() string {
	return r.Status
}

func (r channelAppendResponse) rpcLeaderID() uint64 {
	return r.LeaderID
}

func (a *Adapter) handleChannelAppendRPC(ctx context.Context, body []byte) ([]byte, error) {
	if isChannelAppendBatchRequestBinary(body) {
		return a.handleChannelAppendBatchRPC(ctx, body)
	}
	req, err := decodeChannelAppendRequest(body)
	if err != nil {
		return nil, err
	}
	if a.channelLog == nil {
		return nil, fmt.Errorf("access/node: channel log not configured")
	}

	if body, handled, err := a.handleChannelLeaderRedirect(req.AppendRequest.ChannelID); handled || err != nil {
		return body, err
	}

	batchReq := appendRequestAsBatch(req.AppendRequest)
	result, err := a.channelLog.AppendBatch(ctx, batchReq)
	if err == nil {
		itemResult, itemErr := appendResultFromBatch(result)
		if itemErr != nil {
			err = itemErr
		} else {
			return encodeChannelAppendResponse(channelAppendResponse{
				Status: rpcStatusOK,
				Result: itemResult,
			})
		}
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
			batchReq.ExpectedChannelEpoch = refreshed.Epoch
			batchReq.ExpectedLeaderEpoch = refreshed.LeaderEpoch
			if refreshed.Leader != 0 && uint64(refreshed.Leader) != a.localNodeID {
				return encodeChannelAppendResponse(channelAppendResponse{
					Status:   rpcStatusNotLeader,
					LeaderID: uint64(refreshed.Leader),
				})
			}
			result, err = a.channelLog.AppendBatch(ctx, batchReq)
			if err == nil {
				itemResult, itemErr := appendResultFromBatch(result)
				if itemErr != nil {
					err = itemErr
				} else {
					return encodeChannelAppendResponse(channelAppendResponse{
						Status: rpcStatusOK,
						Result: itemResult,
					})
				}
			}
		}
	}
	if errors.Is(err, channel.ErrNotLeader) {
		if body, handled, statusErr := a.handleChannelLeaderRedirect(req.AppendRequest.ChannelID); handled || statusErr != nil {
			return body, statusErr
		}
		return encodeChannelAppendResponse(channelAppendResponse{Status: rpcStatusNotLeader})
	}
	if errors.Is(err, channel.ErrWriteFenced) {
		return encodeChannelAppendResponse(channelAppendResponse{Status: rpcStatusRetryableWriteFenced})
	}
	return nil, err
}

func appendRequestAsBatch(req channel.AppendRequest) channel.AppendBatchRequest {
	return channel.AppendBatchRequest{
		ChannelID:             req.ChannelID,
		Messages:              []channel.Message{req.Message},
		SupportsMessageSeqU64: req.SupportsMessageSeqU64,
		CommitMode:            req.CommitMode,
		ExpectedChannelEpoch:  req.ExpectedChannelEpoch,
		ExpectedLeaderEpoch:   req.ExpectedLeaderEpoch,
		TraceID:               req.TraceID,
		Attempt:               req.Attempt,
	}
}

func appendResultFromBatch(result channel.AppendBatchResult) (channel.AppendResult, error) {
	if len(result.Items) == 0 {
		return channel.AppendResult{}, nil
	}
	item := result.Items[0]
	if item.Err != nil {
		return channel.AppendResult{}, item.Err
	}
	return channel.AppendResult{
		MessageID:  item.MessageID,
		MessageSeq: item.MessageSeq,
		Message:    item.Message,
	}, nil
}

func (a *Adapter) handleChannelAppendBatchRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeChannelAppendBatchRequest(body)
	if err != nil {
		return nil, err
	}
	if a.channelLog == nil {
		return nil, fmt.Errorf("access/node: channel log not configured")
	}

	if body, handled, err := a.handleChannelLeaderRedirectBatch(req.AppendBatchRequest.ChannelID); handled || err != nil {
		return body, err
	}

	result, err := a.channelLog.AppendBatch(ctx, req.AppendBatchRequest)
	if err == nil {
		return encodeChannelAppendBatchResponse(channelAppendBatchResponse{
			Status: rpcStatusOK,
			Result: result,
		})
	}
	if errors.Is(err, channel.ErrNotLeader) || errors.Is(err, channel.ErrStaleMeta) {
		if refreshed, refreshErr := a.refreshChannelAppendMeta(ctx, req.AppendBatchRequest.ChannelID); refreshErr == nil {
			a.channelAppendLogger().Debug("resolved refreshed channel append metadata",
				wklog.Event("access.node.channel_append.refresh.resolved"),
				wklog.NodeID(a.localNodeID),
				wklog.LeaderNodeID(uint64(refreshed.Leader)),
				wklog.ChannelID(req.AppendBatchRequest.ChannelID.ID),
				wklog.ChannelType(int64(req.AppendBatchRequest.ChannelID.Type)),
				wklog.Uint64("channelEpoch", refreshed.Epoch),
				wklog.Uint64("leaderEpoch", refreshed.LeaderEpoch),
				wklog.Int("replicaCount", len(refreshed.Replicas)),
				wklog.Int("isrCount", len(refreshed.ISR)),
				wklog.Int("minISR", refreshed.MinISR),
			)
			req.AppendBatchRequest.ExpectedChannelEpoch = refreshed.Epoch
			req.AppendBatchRequest.ExpectedLeaderEpoch = refreshed.LeaderEpoch
			if refreshed.Leader != 0 && uint64(refreshed.Leader) != a.localNodeID {
				return encodeChannelAppendBatchResponse(channelAppendBatchResponse{
					Status:   rpcStatusNotLeader,
					LeaderID: uint64(refreshed.Leader),
				})
			}
			result, err = a.channelLog.AppendBatch(ctx, req.AppendBatchRequest)
			if err == nil {
				return encodeChannelAppendBatchResponse(channelAppendBatchResponse{
					Status: rpcStatusOK,
					Result: result,
				})
			}
		}
	}
	if errors.Is(err, channel.ErrNotLeader) {
		if body, handled, statusErr := a.handleChannelLeaderRedirectBatch(req.AppendBatchRequest.ChannelID); handled || statusErr != nil {
			return body, statusErr
		}
		return encodeChannelAppendBatchResponse(channelAppendBatchResponse{Status: rpcStatusNotLeader})
	}
	if errors.Is(err, channel.ErrWriteFenced) {
		return encodeChannelAppendBatchResponse(channelAppendBatchResponse{Status: rpcStatusRetryableWriteFenced})
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

func (a *Adapter) handleChannelLeaderRedirectBatch(id channel.ChannelID) ([]byte, bool, error) {
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
	body, encodeErr := encodeChannelAppendBatchResponse(channelAppendBatchResponse{
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
	if c == nil || c.cluster == nil {
		return channel.AppendResult{}, fmt.Errorf("access/node: cluster not configured")
	}
	if nodeID == 0 {
		return channel.AppendResult{}, channel.ErrNotLeader
	}
	body, err := encodeChannelAppendRequestBinary(channelAppendRequest{
		AppendRequest: req,
	})
	if err != nil {
		return channel.AppendResult{}, err
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

		respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(target), 0, channelAppendRPCServiceID, body)
		if err == nil {
			var resp channelAppendResponse
			resp, err = decodeChannelAppendResponse(respBody)
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
				case rpcStatusRetryableWriteFenced:
					lastErr = channel.ErrWriteFenced
					continue
				default:
					lastErr = fmt.Errorf("access/node: unexpected channel append status %q", resp.Status)
					continue
				}
			}
		}
		if err != nil {
			lastErr = normalizeChannelAppendRPCError(err)
			continue
		}
	}

	if lastErr != nil {
		return channel.AppendResult{}, lastErr
	}
	return channel.AppendResult{}, channel.ErrNotLeader
}

func (c *Client) AppendBatchToLeader(ctx context.Context, nodeID uint64, req channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	if c == nil || c.cluster == nil {
		return channel.AppendBatchResult{}, fmt.Errorf("access/node: cluster not configured")
	}
	if nodeID == 0 {
		return channel.AppendBatchResult{}, channel.ErrNotLeader
	}
	body, err := encodeChannelAppendBatchRequestBinary(channelAppendBatchRequest{
		AppendBatchRequest: req,
	})
	if err != nil {
		return channel.AppendBatchResult{}, err
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

		respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(target), 0, channelAppendRPCServiceID, body)
		if err == nil {
			var resp channelAppendBatchResponse
			resp, err = decodeChannelAppendBatchResponse(respBody)
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
				case rpcStatusRetryableWriteFenced:
					lastErr = channel.ErrWriteFenced
					continue
				default:
					lastErr = fmt.Errorf("access/node: unexpected channel append batch status %q", resp.Status)
					continue
				}
			}
		}
		if err != nil {
			lastErr = normalizeChannelAppendRPCError(err)
			continue
		}
	}

	if lastErr != nil {
		return channel.AppendBatchResult{}, lastErr
	}
	return channel.AppendBatchResult{}, channel.ErrNotLeader
}

func encodeChannelAppendResponse(resp channelAppendResponse) ([]byte, error) {
	return encodeChannelAppendResponseBinary(resp)
}

func decodeChannelAppendResponse(body []byte) (channelAppendResponse, error) {
	return decodeChannelAppendResponseBinary(body)
}

func encodeChannelAppendBatchResponse(resp channelAppendBatchResponse) ([]byte, error) {
	return encodeChannelAppendBatchResponseBinary(resp)
}

func decodeChannelAppendBatchResponse(body []byte) (channelAppendBatchResponse, error) {
	return decodeChannelAppendBatchResponseBinary(body)
}

func normalizeChannelAppendRPCError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, channel.ErrNotLeader) || errors.Is(err, channel.ErrStaleMeta) {
		return err
	}
	if errors.Is(err, channel.ErrWriteFenced) {
		return err
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, channel.ErrNotLeader.Error()):
		return channel.ErrNotLeader
	case strings.Contains(msg, channel.ErrStaleMeta.Error()):
		return channel.ErrStaleMeta
	case strings.Contains(msg, channel.ErrWriteFenced.Error()):
		return channel.ErrWriteFenced
	default:
		return err
	}
}
