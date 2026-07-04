package node

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// ChannelAppendRPCServiceID is the cluster RPC service for SEND forwarding to the channel append authority.
const ChannelAppendRPCServiceID uint8 = clusternet.RPCChannelAuthoritySend

// ChannelAppend accepts send batches that are authoritative on this node.
type ChannelAppend interface {
	// SubmitForAuthority submits item-aligned sends to the local channel authority.
	SubmitForAuthority(context.Context, channelappend.AuthorityTarget, []channelappend.SendBatchItem) []channelappend.SendBatchItemResult
}

// ChannelAppendOptions configures the channel append RPC adapter.
type ChannelAppendOptions struct {
	// ChannelAppend handles channel authority write batches after payload decoding.
	ChannelAppend ChannelAppend
	// Logger records channel append RPC adapter failures.
	Logger wklog.Logger
}

// ChannelAppendAdapter decodes channel append RPC payloads for a local authority port.
type ChannelAppendAdapter struct {
	// channelAppend owns channel authority write admission decisions.
	channelAppend ChannelAppend
	// logger records adapter decode errors and rejected local operations.
	logger wklog.Logger
}

// NewChannelAppendAdapter creates a channel append RPC adapter.
func NewChannelAppendAdapter(opts ChannelAppendOptions) *ChannelAppendAdapter {
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}
	return &ChannelAppendAdapter{channelAppend: opts.ChannelAppend, logger: opts.Logger}
}

// HandleChannelAppendRPC handles one encoded channel append RPC payload.
func (a *ChannelAppendAdapter) HandleChannelAppendRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeChannelAppendRequest(payload)
	if err != nil {
		a.channelAppendRPCLogger().Warn("channel append rpc decode failed",
			wklog.Event("internal.access.node.channel_append_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.channelAppend == nil {
		return encodeChannelAppendResponse(channelAppendResponse{Status: rpcStatusRejected})
	}
	now := time.Now()
	items := make([]channelappend.SendBatchItem, len(req.Items))
	for i, item := range req.Items {
		items[i] = channelappend.SendBatchItem{
			Context: ctx,
			Command: item.Command,
		}
		if item.Timeout > 0 {
			items[i].Deadline = now.Add(item.Timeout)
		}
	}
	results := a.channelAppend.SubmitForAuthority(ctx, req.Target, items)
	return encodeChannelAppendResponse(channelAppendResponse{Status: rpcStatusOK, Results: results})
}

func (a *ChannelAppendAdapter) channelAppendRPCLogger() wklog.Logger {
	if a == nil || a.logger == nil {
		return wklog.NewNop()
	}
	return a.logger
}

// ForwardSendBatch forwards SEND items to the target channel authority node.
func (c *Client) ForwardSendBatch(ctx context.Context, target channelappend.AuthorityTarget, items []channelappend.SendBatchItem) []channelappend.SendBatchItemResult {
	now := time.Now()
	results := make([]channelappend.SendBatchItemResult, len(items))
	reqItems := make([]channelAppendItem, 0, len(items))
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
		reqItems = append(reqItems, channelAppendItem{
			Command: item.Command.Clone(),
			Timeout: channelAppendRelativeTimeout(item, now),
		})
	}
	if len(activeIndexes) == 0 {
		return results
	}
	if c == nil || c.node == nil {
		return channelAppendFillActiveErrors(results, activeIndexes, fmt.Errorf("internal/access/node: channel append rpc client not configured"))
	}
	body, err := encodeChannelAppendRequest(channelAppendRequest{Target: target, Items: reqItems})
	if err != nil {
		return channelAppendFillActiveErrors(results, activeIndexes, err)
	}
	respBody, err := c.node.CallRPC(ctx, target.LeaderNodeID, ChannelAppendRPCServiceID, body)
	if err != nil {
		return channelAppendFillActiveErrors(results, activeIndexes, channelAppendRPCError(err))
	}
	resp, err := decodeChannelAppendResponse(respBody)
	if err != nil {
		return channelAppendFillActiveErrors(results, activeIndexes, err)
	}
	if err := channelAppendErrorForStatus(resp.Status); err != nil {
		return channelAppendFillActiveErrors(results, activeIndexes, err)
	}
	if len(resp.Results) != len(activeIndexes) {
		return channelAppendFillActiveErrors(results, activeIndexes, channelappend.ErrAppendResultMissing)
	}
	for i, result := range resp.Results {
		results[activeIndexes[i]] = result
	}
	return results
}

func channelAppendRPCError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, transport.ErrCanceled):
		return context.Canceled
	case errors.Is(err, transport.ErrTimeout):
		return context.DeadlineExceeded
	case channelAppendTransportUnavailable(err):
		return fmt.Errorf("%w: %w", channelappend.ErrRouteNotReady, err)
	default:
		return err
	}
}

func channelAppendTransportUnavailable(err error) bool {
	switch {
	case errors.Is(err, transport.ErrDialFailed),
		errors.Is(err, transport.ErrNodeNotFound),
		errors.Is(err, transport.ErrStopped),
		errors.Is(err, net.ErrClosed),
		errors.Is(err, io.EOF),
		errors.Is(err, io.ErrUnexpectedEOF),
		errors.Is(err, syscall.ECONNRESET),
		errors.Is(err, syscall.ECONNREFUSED),
		errors.Is(err, syscall.EPIPE):
		return true
	default:
		return false
	}
}

func channelAppendRelativeTimeout(item channelappend.SendBatchItem, now time.Time) time.Duration {
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

func channelAppendFillActiveErrors(results []channelappend.SendBatchItemResult, indexes []int, err error) []channelappend.SendBatchItemResult {
	for _, index := range indexes {
		results[index].Err = err
	}
	return results
}

func channelAppendErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusNotLeader:
		return channelappend.ErrNotLeader
	case channelAppendErrCodeNotChannelAuthority:
		return channelappend.ErrNotChannelAuthority
	case rpcStatusStaleRoute:
		return channelappend.ErrStaleRoute
	case rpcStatusRouteNotReady:
		return channelappend.ErrRouteNotReady
	case channelAppendErrCodeBackpressured:
		return channelappend.ErrBackpressured
	case channelAppendErrCodeAppendResultMissing:
		return channelappend.ErrAppendResultMissing
	case channelAppendErrCodeChannelBusy:
		return channelappend.ErrChannelBusy
	case rpcStatusContextCanceled:
		return context.Canceled
	case rpcStatusContextDeadlineExceeded:
		return context.DeadlineExceeded
	case rpcStatusRejected:
		return fmt.Errorf("internal/access/node: channel append rpc rejected")
	default:
		return fmt.Errorf("internal/access/node: unknown channel append rpc status %q", status)
	}
}
