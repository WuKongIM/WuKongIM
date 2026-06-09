package node

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/channelwrite"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// ChannelWriteRPCServiceID is the clusterv2 RPC service for channel authority write forwarding.
const ChannelWriteRPCServiceID uint8 = clusternet.RPCChannelWrite

// ChannelWrite accepts send batches that are authoritative on this node.
type ChannelWrite interface {
	// SubmitForAuthority submits item-aligned sends to the local channel authority.
	SubmitForAuthority(context.Context, channelwrite.AuthorityTarget, []channelwrite.SendBatchItem) []channelwrite.SendBatchItemResult
}

// ChannelWriteOptions configures the channel write RPC adapter.
type ChannelWriteOptions struct {
	// ChannelWrite handles channel authority write batches after payload decoding.
	ChannelWrite ChannelWrite
	// Logger records channel write RPC adapter failures.
	Logger wklog.Logger
}

// ChannelWriteAdapter decodes channel write RPC payloads for a local authority port.
type ChannelWriteAdapter struct {
	// channelWrite owns channel authority write admission decisions.
	channelWrite ChannelWrite
	// logger records adapter decode errors and rejected local operations.
	logger wklog.Logger
}

// NewChannelWriteAdapter creates a channel write RPC adapter.
func NewChannelWriteAdapter(opts ChannelWriteOptions) *ChannelWriteAdapter {
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}
	return &ChannelWriteAdapter{channelWrite: opts.ChannelWrite, logger: opts.Logger}
}

// HandleChannelWriteRPC handles one encoded channel write RPC payload.
func (a *ChannelWriteAdapter) HandleChannelWriteRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeChannelWriteRequest(payload)
	if err != nil {
		a.channelWriteRPCLogger().Warn("channel write rpc decode failed",
			wklog.Event("internalv2.access.node.channel_write_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.channelWrite == nil {
		return encodeChannelWriteResponse(channelWriteResponse{Status: rpcStatusRejected})
	}
	now := time.Now()
	items := make([]channelwrite.SendBatchItem, len(req.Items))
	for i, item := range req.Items {
		items[i] = channelwrite.SendBatchItem{
			Context: ctx,
			Command: item.Command,
		}
		if item.Timeout > 0 {
			items[i].Deadline = now.Add(item.Timeout)
		}
	}
	results := a.channelWrite.SubmitForAuthority(ctx, req.Target, items)
	return encodeChannelWriteResponse(channelWriteResponse{Status: rpcStatusOK, Results: results})
}

func (a *ChannelWriteAdapter) channelWriteRPCLogger() wklog.Logger {
	if a == nil || a.logger == nil {
		return wklog.NewNop()
	}
	return a.logger
}

// ForwardSendBatch forwards SEND items to the target channel authority node.
func (c *Client) ForwardSendBatch(ctx context.Context, target channelwrite.AuthorityTarget, items []channelwrite.SendBatchItem) []channelwrite.SendBatchItemResult {
	now := time.Now()
	results := make([]channelwrite.SendBatchItemResult, len(items))
	reqItems := make([]channelWriteItem, 0, len(items))
	activeIndexes := make([]int, 0, len(items))
	for i, item := range items {
		if item.Context != nil {
			if err := item.Context.Err(); err != nil {
				results[i].Err = err
				continue
			}
		}
		if !item.Deadline.IsZero() && !item.Deadline.After(now) {
			results[i].Err = context.DeadlineExceeded
			continue
		}
		activeIndexes = append(activeIndexes, i)
		reqItems = append(reqItems, channelWriteItem{
			Command: item.Command.Clone(),
			Timeout: channelWriteRelativeTimeout(item, now),
		})
	}
	if len(activeIndexes) == 0 {
		return results
	}
	if c == nil || c.node == nil {
		return channelWriteFillActiveErrors(results, activeIndexes, fmt.Errorf("internalv2/access/node: channel write rpc client not configured"))
	}
	body, err := encodeChannelWriteRequest(channelWriteRequest{Target: target, Items: reqItems})
	if err != nil {
		return channelWriteFillActiveErrors(results, activeIndexes, err)
	}
	respBody, err := c.node.CallRPC(ctx, target.LeaderNodeID, ChannelWriteRPCServiceID, body)
	if err != nil {
		return channelWriteFillActiveErrors(results, activeIndexes, err)
	}
	resp, err := decodeChannelWriteResponse(respBody)
	if err != nil {
		return channelWriteFillActiveErrors(results, activeIndexes, err)
	}
	if err := channelWriteErrorForStatus(resp.Status); err != nil {
		return channelWriteFillActiveErrors(results, activeIndexes, err)
	}
	if len(resp.Results) != len(activeIndexes) {
		return channelWriteFillActiveErrors(results, activeIndexes, channelwrite.ErrAppendResultMissing)
	}
	for i, result := range resp.Results {
		results[activeIndexes[i]] = result
	}
	return results
}

func channelWriteRelativeTimeout(item channelwrite.SendBatchItem, now time.Time) time.Duration {
	var deadline time.Time
	if !item.Deadline.IsZero() && item.Deadline.After(now) {
		deadline = item.Deadline
	}
	if item.Context != nil {
		if ctxDeadline, ok := item.Context.Deadline(); ok && ctxDeadline.After(now) && (deadline.IsZero() || ctxDeadline.Before(deadline)) {
			deadline = ctxDeadline
		}
	}
	if deadline.IsZero() {
		return 0
	}
	return deadline.Sub(now)
}

func channelWriteFillActiveErrors(results []channelwrite.SendBatchItemResult, indexes []int, err error) []channelwrite.SendBatchItemResult {
	for _, index := range indexes {
		results[index].Err = err
	}
	return results
}

func channelWriteErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusNotLeader:
		return channelwrite.ErrNotLeader
	case channelWriteErrCodeNotChannelAuthority:
		return channelwrite.ErrNotChannelAuthority
	case rpcStatusStaleRoute:
		return channelwrite.ErrStaleRoute
	case rpcStatusRouteNotReady:
		return channelwrite.ErrRouteNotReady
	case channelWriteErrCodeBackpressured:
		return channelwrite.ErrBackpressured
	case channelWriteErrCodeAppendResultMissing:
		return channelwrite.ErrAppendResultMissing
	case channelWriteErrCodeChannelBusy:
		return channelwrite.ErrChannelBusy
	case rpcStatusContextCanceled:
		return context.Canceled
	case rpcStatusContextDeadlineExceeded:
		return context.DeadlineExceeded
	case rpcStatusRejected:
		return fmt.Errorf("internalv2/access/node: channel write rpc rejected")
	default:
		return fmt.Errorf("internalv2/access/node: unknown channel write rpc status %q", status)
	}
}
