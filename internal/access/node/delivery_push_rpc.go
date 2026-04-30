package node

import (
	"context"
	"encoding/json"
	"fmt"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type deliveryPushRequest struct {
	OwnerNodeID uint64                     `json:"owner_node_id"`
	Items       []DeliveryPushItem         `json:"items"`
	ChannelID   string                     `json:"channel_id"`
	ChannelType uint8                      `json:"channel_type"`
	MessageID   uint64                     `json:"message_id"`
	MessageSeq  uint64                     `json:"message_seq"`
	Routes      []deliveryruntime.RouteKey `json:"routes"`
	Frame       []byte                     `json:"frame"`
}

// DeliveryPushItem carries one encoded realtime frame and the routes that should receive it.
type DeliveryPushItem struct {
	// ChannelID is the durable channel identifier used for ack ownership binding.
	ChannelID string `json:"channel_id"`
	// ChannelType is the durable channel type used for ack ownership binding.
	ChannelType uint8 `json:"channel_type"`
	// MessageID identifies the committed message delivered by this item.
	MessageID uint64 `json:"message_id"`
	// MessageSeq is the committed channel sequence delivered by this item.
	MessageSeq uint64 `json:"message_seq"`
	// Routes lists the remote sessions that should receive Frame.
	Routes []deliveryruntime.RouteKey `json:"routes"`
	// Frame is the protocol-encoded realtime frame for this route group.
	Frame []byte `json:"frame"`
}

// DeliveryPushBatchCommand batches one or more realtime delivery frames for a remote node.
type DeliveryPushBatchCommand struct {
	// OwnerNodeID identifies the committed owner that should receive downstream acks.
	OwnerNodeID uint64 `json:"owner_node_id"`
	// Items groups routes by the frame bytes they should receive.
	Items []DeliveryPushItem `json:"items"`
}

func (a *Adapter) handleDeliveryPushRPC(ctx context.Context, body []byte) ([]byte, error) {
	_ = ctx
	var req deliveryPushRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	items := req.deliveryPushItems()
	frames := make([]frame.Frame, len(items))
	for i, item := range items {
		f, _, err := a.codec.DecodeFrame(item.Frame, frame.LatestVersion)
		if err != nil {
			return nil, err
		}
		if f == nil {
			return nil, fmt.Errorf("access/node: invalid delivery push frame")
		}
		frames[i] = f
	}

	resp := DeliveryPushResponse{Status: rpcStatusOK}
	for i, item := range items {
		a.handleDeliveryPushItem(item, req.OwnerNodeID, frames[i], &resp)
	}
	if a.logger != nil {
		a.logger.Info("delivery push rpc finished",
			wklog.Event("delivery.diag.push_rpc"),
			wklog.Uint64("ownerNodeID", req.OwnerNodeID),
			wklog.Int("items", len(items)),
			wklog.Int("routes", deliveryPushRouteCount(items)),
			wklog.Int("accepted", len(resp.Accepted)),
			wklog.Int("retryable", len(resp.Retryable)),
			wklog.Int("dropped", len(resp.Dropped)),
		)
	}
	return encodeDeliveryPushResponse(resp)
}

func (r deliveryPushRequest) deliveryPushItems() []DeliveryPushItem {
	if len(r.Items) > 0 {
		return r.Items
	}
	if len(r.Routes) == 0 && len(r.Frame) == 0 {
		return nil
	}
	return []DeliveryPushItem{{
		ChannelID:   r.ChannelID,
		ChannelType: r.ChannelType,
		MessageID:   r.MessageID,
		MessageSeq:  r.MessageSeq,
		Routes:      r.Routes,
		Frame:       r.Frame,
	}}
}

func (a *Adapter) handleDeliveryPushItem(item DeliveryPushItem, ownerNodeID uint64, f frame.Frame, resp *DeliveryPushResponse) {
	for _, route := range item.Routes {
		switch {
		case a.localNodeID != 0 && route.NodeID != a.localNodeID:
			resp.Dropped = append(resp.Dropped, route)
		case a.gatewayBootID != 0 && route.BootID != a.gatewayBootID:
			resp.Dropped = append(resp.Dropped, route)
		default:
			conn, ok := a.online.Connection(route.SessionID)
			if !ok || conn.UID != route.UID || conn.State != online.LocalRouteStateActive || conn.Session == nil {
				resp.Dropped = append(resp.Dropped, route)
				continue
			}
			if a.deliveryAckIndex != nil {
				a.deliveryAckIndex.Bind(deliveryruntime.AckBinding{
					SessionID:   route.SessionID,
					MessageID:   item.MessageID,
					ChannelID:   item.ChannelID,
					ChannelType: item.ChannelType,
					OwnerNodeID: ownerNodeID,
					Route:       route,
				})
			}
			if err := conn.Session.WriteFrame(f); err != nil {
				if a.deliveryAckIndex != nil {
					a.deliveryAckIndex.Remove(route.SessionID, item.MessageID)
				}
				resp.Retryable = append(resp.Retryable, route)
				continue
			}
			resp.Accepted = append(resp.Accepted, route)
		}
	}
}

func deliveryPushRouteCount(items []DeliveryPushItem) int {
	total := 0
	for _, item := range items {
		total += len(item.Routes)
	}
	return total
}
