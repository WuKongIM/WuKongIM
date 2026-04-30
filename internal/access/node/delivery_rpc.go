package node

import (
	"encoding/json"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
)

type deliveryResponse struct {
	Status string `json:"status"`
}

// DeliveryPushResponse reports per-route results from a realtime delivery push RPC.
type DeliveryPushResponse struct {
	Status    string                     `json:"status"`
	Accepted  []deliveryruntime.RouteKey `json:"accepted,omitempty"`
	Retryable []deliveryruntime.RouteKey `json:"retryable,omitempty"`
	Dropped   []deliveryruntime.RouteKey `json:"dropped,omitempty"`
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
	return json.Marshal(resp)
}

func encodeDeliveryPushResponse(resp DeliveryPushResponse) ([]byte, error) {
	return json.Marshal(resp)
}

func decodeDeliveryResponse(body []byte) (deliveryResponse, error) {
	var resp deliveryResponse
	err := json.Unmarshal(body, &resp)
	return resp, err
}

func decodeDeliveryPushResponse(body []byte) (DeliveryPushResponse, error) {
	var resp DeliveryPushResponse
	err := json.Unmarshal(body, &resp)
	return resp, err
}
