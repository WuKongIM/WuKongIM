package node

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// SenderAuthorityRPCServiceID is the clusterv2 RPC service for sender UID authority SEND calls.
const SenderAuthorityRPCServiceID uint8 = clusternet.RPCSenderAuthority

// SenderAuthority accepts SEND batches that are authoritative on this node.
type SenderAuthority interface {
	SendBatchForAuthority(context.Context, authority.Target, []message.SendBatchItem) []message.SendBatchItemResult
}

// HandleSenderAuthorityRPC handles one encoded sender authority RPC payload.
func (a *Adapter) HandleSenderAuthorityRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeSenderAuthorityRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("sender authority rpc decode failed",
			wklog.Event("internalv2.access.node.sender_authority_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.senderAuthority == nil {
		return encodeSenderAuthorityResponse(senderAuthorityResponse{Status: rpcStatusRejected})
	}
	now := time.Now()
	items := make([]message.SendBatchItem, len(req.Items))
	for i, item := range req.Items {
		items[i] = message.SendBatchItem{
			Context: ctx,
			Command: item.Command,
		}
		if item.Timeout > 0 {
			items[i].Deadline = now.Add(item.Timeout)
		}
	}
	results := a.senderAuthority.SendBatchForAuthority(ctx, req.Target, items)
	return encodeSenderAuthorityResponse(senderAuthorityResponse{Status: rpcStatusOK, Results: results})
}

// SendBatchToAuthority forwards SEND items to the target sender authority node.
func (c *Client) SendBatchToAuthority(ctx context.Context, target authority.Target, items []message.SendBatchItem) []message.SendBatchItemResult {
	if c == nil || c.node == nil {
		return senderAuthorityErrorResults(len(items), fmt.Errorf("internalv2/access/node: sender authority rpc client not configured"))
	}
	now := time.Now()
	reqItems := make([]senderAuthorityItem, len(items))
	for i, item := range items {
		reqItems[i] = senderAuthorityItem{
			Command: item.Command,
			Timeout: senderAuthorityRelativeTimeout(item, now),
		}
	}
	body, err := encodeSenderAuthorityRequest(senderAuthorityRequest{Target: target, Items: reqItems})
	if err != nil {
		return senderAuthorityErrorResults(len(items), err)
	}
	respBody, err := c.node.CallRPC(ctx, target.LeaderNodeID, SenderAuthorityRPCServiceID, body)
	if err != nil {
		return senderAuthorityErrorResults(len(items), err)
	}
	resp, err := decodeSenderAuthorityResponse(respBody)
	if err != nil {
		return senderAuthorityErrorResults(len(items), err)
	}
	if err := senderAuthorityErrorForStatus(resp.Status); err != nil {
		return senderAuthorityErrorResults(len(items), err)
	}
	if len(resp.Results) != len(items) {
		return senderAuthorityErrorResults(len(items), message.ErrAppendResultMissing)
	}
	return resp.Results
}

func senderAuthorityRelativeTimeout(item message.SendBatchItem, now time.Time) time.Duration {
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

func senderAuthorityErrorResults(n int, err error) []message.SendBatchItemResult {
	results := make([]message.SendBatchItemResult, n)
	for i := range results {
		results[i].Err = err
	}
	return results
}

func senderAuthorityErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusRejected:
		return fmt.Errorf("internalv2/access/node: sender authority rpc rejected")
	default:
		return fmt.Errorf("internalv2/access/node: unknown sender authority rpc status %q", status)
	}
}
