package node

import (
	"fmt"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/legacy/runtime/delivery"
)

type deliveryResponse struct {
	Status string `json:"status"`
}

// DeliveryPushResponse reports per-route results from a realtime delivery push RPC.
type DeliveryPushResponse struct {
	Status string `json:"status"`
	// Accepted carries accepted routes for legacy v1 delivery push responses.
	Accepted []deliveryruntime.RouteKey `json:"accepted,omitempty"`
	// AcceptedCount carries the accepted route count for v2 responses without echoing every route.
	AcceptedCount uint64 `json:"accepted_count,omitempty"`
	// AcceptedCountSet is true when AcceptedCount is authoritative for this response.
	AcceptedCountSet bool                       `json:"-"`
	Retryable        []deliveryruntime.RouteKey `json:"retryable,omitempty"`
	Dropped          []deliveryruntime.RouteKey `json:"dropped,omitempty"`
}

type deliveryPushResponse = DeliveryPushResponse

// DeliveryPushCommand is the legacy single-item realtime delivery push RPC shape.
type DeliveryPushCommand struct {
	OwnerNodeID uint64                     `json:"owner_node_id"`
	ChannelID   string                     `json:"channel_id"`
	ChannelType uint8                      `json:"channel_type"`
	MessageID   uint64                     `json:"message_id"`
	MessageSeq  uint64                     `json:"message_seq"`
	Routes      []deliveryruntime.RouteKey `json:"routes"`
	Frame       []byte                     `json:"frame"`
}

func encodeDeliveryResponse(resp deliveryResponse) ([]byte, error) {
	return encodeDeliveryResponseBinary(resp)
}

func decodeDeliveryResponse(body []byte) (deliveryResponse, error) {
	return decodeDeliveryResponseBinary(body)
}

func decodeDeliveryPushResponse(body []byte) (DeliveryPushResponse, error) {
	if !isDeliveryPushResponseBinary(body) {
		return DeliveryPushResponse{}, fmt.Errorf("access/node: invalid delivery push response codec")
	}
	return decodeDeliveryPushResponseBinary(body)
}
