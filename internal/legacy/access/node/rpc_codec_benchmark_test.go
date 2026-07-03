package node

import (
	"encoding/json"
	"fmt"
	"testing"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/legacy/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/legacy/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

var (
	benchmarkRPCBytesSink         []byte
	benchmarkDeliverySink         deliveryPushRequest
	benchmarkPresenceSink         presenceRPCRequest
	benchmarkDeliverySubmitSink   deliverySubmitRequest
	benchmarkChannelAppendSink    channelAppendRequest
	benchmarkChannelMessagesSink  channelMessagesResponse
	benchmarkConversationFactSink conversationFactsResponse
)

func TestRPCBinaryBenchmarkCodecsRoundTrip(t *testing.T) {
	delivery := benchmarkDeliveryPushBatchCommand()
	deliveryBody, err := encodeDeliveryPushBatchCommandBinary(delivery)
	if err != nil {
		t.Fatalf("encode delivery push binary failed: %v", err)
	}
	deliveryDecoded, _, err := decodeDeliveryPushRequest(deliveryBody)
	if err != nil {
		t.Fatalf("decode delivery push binary failed: %v", err)
	}
	if deliveryDecoded.OwnerNodeID != delivery.OwnerNodeID || len(deliveryDecoded.Items) != len(delivery.Items) {
		t.Fatalf("unexpected delivery round trip: owner=%d items=%d", deliveryDecoded.OwnerNodeID, len(deliveryDecoded.Items))
	}

	presenceReq := benchmarkPresenceHeartbeatRequest()
	presenceBody, err := encodePresenceRPCRequestBinary(presenceReq)
	if err != nil {
		t.Fatalf("encode presence heartbeat binary failed: %v", err)
	}
	presenceDecoded, err := decodePresenceRPCRequest(presenceBody)
	if err != nil {
		t.Fatalf("decode presence heartbeat binary failed: %v", err)
	}
	if presenceDecoded.Lease == nil || presenceDecoded.Lease.RouteDigest != presenceReq.Lease.RouteDigest {
		t.Fatalf("unexpected presence round trip: lease=%#v", presenceDecoded.Lease)
	}

	appendReq := benchmarkChannelAppendRequest()
	appendBody, err := encodeChannelAppendRequestBinary(appendReq)
	if err != nil {
		t.Fatalf("encode channel append binary failed: %v", err)
	}
	appendDecoded, err := decodeChannelAppendRequest(appendBody)
	if err != nil {
		t.Fatalf("decode channel append binary failed: %v", err)
	}
	if appendDecoded.AppendRequest.Message.ClientMsgNo != appendReq.AppendRequest.Message.ClientMsgNo {
		t.Fatalf("unexpected channel append round trip: %#v", appendDecoded.AppendRequest.Message)
	}

	messagesResp := benchmarkChannelMessagesResponse()
	messagesBody, err := encodeChannelMessagesResponse(messagesResp)
	if err != nil {
		t.Fatalf("encode channel messages binary failed: %v", err)
	}
	messagesDecoded, err := decodeChannelMessagesResponse(messagesBody)
	if err != nil {
		t.Fatalf("decode channel messages binary failed: %v", err)
	}
	if len(messagesDecoded.Page.Messages) != len(messagesResp.Page.Messages) {
		t.Fatalf("unexpected channel messages round trip: messages=%d", len(messagesDecoded.Page.Messages))
	}

	factsResp := benchmarkConversationFactsResponse()
	factsBody, err := encodeConversationFactsResponse(factsResp)
	if err != nil {
		t.Fatalf("encode conversation facts binary failed: %v", err)
	}
	factsDecoded, err := decodeConversationFactsResponse(factsBody)
	if err != nil {
		t.Fatalf("decode conversation facts binary failed: %v", err)
	}
	if len(factsDecoded.Entries) != len(factsResp.Entries) {
		t.Fatalf("unexpected conversation facts round trip: entries=%d", len(factsDecoded.Entries))
	}
}

func BenchmarkDeliveryPushRPCCodec(b *testing.B) {
	cmd := benchmarkDeliveryPushBatchCommand()
	jsonBody, err := json.Marshal(cmd)
	if err != nil {
		b.Fatalf("marshal delivery json failed: %v", err)
	}
	binaryBody, err := encodeDeliveryPushBatchCommandBinary(cmd)
	if err != nil {
		b.Fatalf("marshal delivery binary failed: %v", err)
	}

	b.Run("json_marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(jsonBody)), "payload_bytes/op")
		for i := 0; i < b.N; i++ {
			body, err := json.Marshal(cmd)
			if err != nil {
				b.Fatalf("marshal delivery json failed: %v", err)
			}
			benchmarkRPCBytesSink = body
		}
	})
	b.Run("binary_marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(binaryBody)), "payload_bytes/op")
		for i := 0; i < b.N; i++ {
			body, err := encodeDeliveryPushBatchCommandBinary(cmd)
			if err != nil {
				b.Fatalf("marshal delivery binary failed: %v", err)
			}
			benchmarkRPCBytesSink = body
		}
	})
	b.Run("json_unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(jsonBody)))
		for i := 0; i < b.N; i++ {
			var out deliveryPushRequest
			if err := json.Unmarshal(jsonBody, &out); err != nil {
				b.Fatalf("unmarshal delivery json failed: %v", err)
			}
			benchmarkDeliverySink = out
		}
	})
	b.Run("binary_unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(binaryBody)))
		for i := 0; i < b.N; i++ {
			out, _, err := decodeDeliveryPushRequest(binaryBody)
			if err != nil {
				b.Fatalf("unmarshal delivery binary failed: %v", err)
			}
			benchmarkDeliverySink = out
		}
	})
}

func BenchmarkPresenceHeartbeatRPCCodec(b *testing.B) {
	req := benchmarkPresenceHeartbeatRequest()
	jsonBody, err := json.Marshal(req)
	if err != nil {
		b.Fatalf("marshal presence json failed: %v", err)
	}
	binaryBody, err := encodePresenceRPCRequestBinary(req)
	if err != nil {
		b.Fatalf("marshal presence binary failed: %v", err)
	}

	b.Run("json_marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(jsonBody)), "payload_bytes/op")
		for i := 0; i < b.N; i++ {
			body, err := json.Marshal(req)
			if err != nil {
				b.Fatalf("marshal presence json failed: %v", err)
			}
			benchmarkRPCBytesSink = body
		}
	})
	b.Run("binary_marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(binaryBody)), "payload_bytes/op")
		for i := 0; i < b.N; i++ {
			body, err := encodePresenceRPCRequestBinary(req)
			if err != nil {
				b.Fatalf("marshal presence binary failed: %v", err)
			}
			benchmarkRPCBytesSink = body
		}
	})
	b.Run("json_unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(jsonBody)))
		for i := 0; i < b.N; i++ {
			var out presenceRPCRequest
			if err := json.Unmarshal(jsonBody, &out); err != nil {
				b.Fatalf("unmarshal presence json failed: %v", err)
			}
			benchmarkPresenceSink = out
		}
	})
	b.Run("binary_unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(binaryBody)))
		for i := 0; i < b.N; i++ {
			out, err := decodePresenceRPCRequest(binaryBody)
			if err != nil {
				b.Fatalf("unmarshal presence binary failed: %v", err)
			}
			benchmarkPresenceSink = out
		}
	})
}

func BenchmarkDeliverySubmitRPCCodec(b *testing.B) {
	req := benchmarkDeliverySubmitRequest()
	jsonBody, err := json.Marshal(req)
	if err != nil {
		b.Fatalf("marshal delivery submit json failed: %v", err)
	}
	binaryBody, err := encodeDeliverySubmitRequestBinary(req)
	if err != nil {
		b.Fatalf("marshal delivery submit binary failed: %v", err)
	}

	b.Run("json_marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(jsonBody)), "payload_bytes/op")
		for i := 0; i < b.N; i++ {
			body, err := json.Marshal(req)
			if err != nil {
				b.Fatalf("marshal delivery submit json failed: %v", err)
			}
			benchmarkRPCBytesSink = body
		}
	})
	b.Run("binary_marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(binaryBody)), "payload_bytes/op")
		for i := 0; i < b.N; i++ {
			body, err := encodeDeliverySubmitRequestBinary(req)
			if err != nil {
				b.Fatalf("marshal delivery submit binary failed: %v", err)
			}
			benchmarkRPCBytesSink = body
		}
	})
	b.Run("json_unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(jsonBody)))
		for i := 0; i < b.N; i++ {
			var out deliverySubmitRequest
			if err := json.Unmarshal(jsonBody, &out); err != nil {
				b.Fatalf("unmarshal delivery submit json failed: %v", err)
			}
			benchmarkDeliverySubmitSink = out
		}
	})
	b.Run("binary_unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(binaryBody)))
		for i := 0; i < b.N; i++ {
			out, err := decodeDeliverySubmitRequest(binaryBody)
			if err != nil {
				b.Fatalf("unmarshal delivery submit binary failed: %v", err)
			}
			benchmarkDeliverySubmitSink = out
		}
	})
}

func BenchmarkChannelAppendRPCCodec(b *testing.B) {
	req := benchmarkChannelAppendRequest()
	jsonBody, err := json.Marshal(req)
	if err != nil {
		b.Fatalf("marshal channel append json failed: %v", err)
	}
	binaryBody, err := encodeChannelAppendRequestBinary(req)
	if err != nil {
		b.Fatalf("marshal channel append binary failed: %v", err)
	}

	b.Run("json_marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(jsonBody)), "payload_bytes/op")
		for i := 0; i < b.N; i++ {
			body, err := json.Marshal(req)
			if err != nil {
				b.Fatalf("marshal channel append json failed: %v", err)
			}
			benchmarkRPCBytesSink = body
		}
	})
	b.Run("binary_marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(binaryBody)), "payload_bytes/op")
		for i := 0; i < b.N; i++ {
			body, err := encodeChannelAppendRequestBinary(req)
			if err != nil {
				b.Fatalf("marshal channel append binary failed: %v", err)
			}
			benchmarkRPCBytesSink = body
		}
	})
	b.Run("json_unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(jsonBody)))
		for i := 0; i < b.N; i++ {
			var out channelAppendRequest
			if err := json.Unmarshal(jsonBody, &out); err != nil {
				b.Fatalf("unmarshal channel append json failed: %v", err)
			}
			benchmarkChannelAppendSink = out
		}
	})
	b.Run("binary_unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(binaryBody)))
		for i := 0; i < b.N; i++ {
			out, err := decodeChannelAppendRequest(binaryBody)
			if err != nil {
				b.Fatalf("unmarshal channel append binary failed: %v", err)
			}
			benchmarkChannelAppendSink = out
		}
	})
}

func BenchmarkChannelMessagesRPCCodec(b *testing.B) {
	resp := benchmarkChannelMessagesResponse()
	jsonBody, err := json.Marshal(resp)
	if err != nil {
		b.Fatalf("marshal channel messages json failed: %v", err)
	}
	binaryBody, err := encodeChannelMessagesResponse(resp)
	if err != nil {
		b.Fatalf("marshal channel messages binary failed: %v", err)
	}

	b.Run("json_marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(jsonBody)), "payload_bytes/op")
		for i := 0; i < b.N; i++ {
			body, err := json.Marshal(resp)
			if err != nil {
				b.Fatalf("marshal channel messages json failed: %v", err)
			}
			benchmarkRPCBytesSink = body
		}
	})
	b.Run("binary_marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(binaryBody)), "payload_bytes/op")
		for i := 0; i < b.N; i++ {
			body, err := encodeChannelMessagesResponse(resp)
			if err != nil {
				b.Fatalf("marshal channel messages binary failed: %v", err)
			}
			benchmarkRPCBytesSink = body
		}
	})
	b.Run("json_unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(jsonBody)))
		for i := 0; i < b.N; i++ {
			var out channelMessagesResponse
			if err := json.Unmarshal(jsonBody, &out); err != nil {
				b.Fatalf("unmarshal channel messages json failed: %v", err)
			}
			benchmarkChannelMessagesSink = out
		}
	})
	b.Run("binary_unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(binaryBody)))
		for i := 0; i < b.N; i++ {
			out, err := decodeChannelMessagesResponse(binaryBody)
			if err != nil {
				b.Fatalf("unmarshal channel messages binary failed: %v", err)
			}
			benchmarkChannelMessagesSink = out
		}
	})
}

func BenchmarkConversationFactsRPCCodec(b *testing.B) {
	resp := benchmarkConversationFactsResponse()
	jsonBody, err := json.Marshal(resp)
	if err != nil {
		b.Fatalf("marshal conversation facts json failed: %v", err)
	}
	binaryBody, err := encodeConversationFactsResponse(resp)
	if err != nil {
		b.Fatalf("marshal conversation facts binary failed: %v", err)
	}

	b.Run("json_marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(jsonBody)), "payload_bytes/op")
		for i := 0; i < b.N; i++ {
			body, err := json.Marshal(resp)
			if err != nil {
				b.Fatalf("marshal conversation facts json failed: %v", err)
			}
			benchmarkRPCBytesSink = body
		}
	})
	b.Run("binary_marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(binaryBody)), "payload_bytes/op")
		for i := 0; i < b.N; i++ {
			body, err := encodeConversationFactsResponse(resp)
			if err != nil {
				b.Fatalf("marshal conversation facts binary failed: %v", err)
			}
			benchmarkRPCBytesSink = body
		}
	})
	b.Run("json_unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(jsonBody)))
		for i := 0; i < b.N; i++ {
			var out conversationFactsResponse
			if err := json.Unmarshal(jsonBody, &out); err != nil {
				b.Fatalf("unmarshal conversation facts json failed: %v", err)
			}
			benchmarkConversationFactSink = out
		}
	})
	b.Run("binary_unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(binaryBody)))
		for i := 0; i < b.N; i++ {
			out, err := decodeConversationFactsResponse(binaryBody)
			if err != nil {
				b.Fatalf("unmarshal conversation facts binary failed: %v", err)
			}
			benchmarkConversationFactSink = out
		}
	})
}

func benchmarkDeliveryPushBatchCommand() DeliveryPushBatchCommand {
	frameBytes := make([]byte, 256)
	for i := range frameBytes {
		frameBytes[i] = byte(i)
	}
	items := make([]DeliveryPushItem, 4)
	for itemIdx := range items {
		routes := make([]deliveryruntime.RouteKey, 8)
		for routeIdx := range routes {
			routes[routeIdx] = deliveryruntime.RouteKey{
				UID:       fmt.Sprintf("uid-%02d-%02d", itemIdx, routeIdx),
				NodeID:    2,
				BootID:    9000 + uint64(itemIdx),
				SessionID: uint64(itemIdx*100 + routeIdx + 1),
			}
		}
		items[itemIdx] = DeliveryPushItem{
			ChannelID:   fmt.Sprintf("channel-%02d", itemIdx),
			ChannelType: 2,
			MessageID:   100000 + uint64(itemIdx),
			MessageSeq:  200000 + uint64(itemIdx),
			Routes:      routes,
			Frame:       frameBytes,
		}
	}
	return DeliveryPushBatchCommand{OwnerNodeID: 1, Items: items}
}

func benchmarkPresenceHeartbeatRequest() presenceRPCRequest {
	return presenceRPCRequest{
		Op: presenceOpHeartbeat,
		Lease: &presence.GatewayLease{
			SlotID:         128,
			GatewayNodeID:  2,
			GatewayBootID:  99,
			RouteCount:     100000,
			RouteDigest:    0x1234567890abcdef,
			LeaseUntilUnix: 1777777777,
		},
	}
}

func benchmarkDeliverySubmitRequest() deliverySubmitRequest {
	return deliverySubmitRequest{
		Envelope: deliveryruntime.CommittedEnvelope{
			Message:           benchmarkChannelMessage(1),
			SenderSessionID:   42,
			MessageScopedUIDs: []string{"bench-u1", "bench-u2"},
		},
	}
}

func benchmarkChannelAppendRequest() channelAppendRequest {
	return channelAppendRequest{
		AppendRequest: channel.AppendRequest{
			ChannelID:             channel.ChannelID{ID: "bench-channel", Type: frame.ChannelTypeGroup},
			Message:               benchmarkChannelMessage(2),
			SupportsMessageSeqU64: true,
			CommitMode:            channel.CommitModeQuorum,
			ExpectedChannelEpoch:  11,
			ExpectedLeaderEpoch:   12,
		},
	}
}

func benchmarkChannelMessagesResponse() channelMessagesResponse {
	messages := make([]channel.Message, 16)
	for i := range messages {
		messages[i] = benchmarkChannelMessage(i)
		messages[i].MessageSeq = uint64(1000 + i)
	}
	return channelMessagesResponse{
		Status:   rpcStatusOK,
		LeaderID: 2,
		Page: ChannelMessagesPage{
			Messages:      messages,
			HasMore:       true,
			NextBeforeSeq: 999,
			MaxMessageSeq: 1024,
		},
	}
}

func benchmarkConversationFactsResponse() conversationFactsResponse {
	entries := make([]conversationFactsEntry, 8)
	for i := range entries {
		entries[i] = conversationFactsEntry{
			Key: newConversationFactsChannelKey(channel.ChannelID{
				ID:   fmt.Sprintf("conversation-%02d", i),
				Type: frame.ChannelTypeGroup,
			}),
			Messages: []channel.Message{benchmarkChannelMessage(i)},
		}
	}
	return conversationFactsResponse{
		Status:   rpcStatusOK,
		Messages: []channel.Message{benchmarkChannelMessage(100)},
		Entries:  entries,
	}
}

func benchmarkChannelMessage(index int) channel.Message {
	payload := make([]byte, 512)
	for i := range payload {
		payload[i] = byte(i + index)
	}
	return channel.Message{
		MessageID:   uint64(100000 + index),
		MessageSeq:  uint64(200000 + index),
		Framer:      frame.Framer{FrameType: frame.SEND, RemainingLength: uint32(len(payload)), RedDot: true, SyncOnce: index%2 == 0, FrameSize: int64(len(payload) + 64)},
		Setting:     frame.SettingReceiptEnabled | frame.SettingTopic,
		MsgKey:      fmt.Sprintf("msg-key-%02d", index),
		Expire:      3600,
		ClientSeq:   uint64(index + 1),
		ClientMsgNo: fmt.Sprintf("client-msg-%02d", index),
		StreamNo:    fmt.Sprintf("stream-%02d", index),
		StreamID:    uint64(index + 10),
		StreamFlag:  frame.StreamFlagIng,
		Timestamp:   1777777777 + int32(index),
		ChannelID:   fmt.Sprintf("bench-channel-%02d", index),
		ChannelType: frame.ChannelTypeGroup,
		Topic:       fmt.Sprintf("topic-%02d", index),
		FromUID:     fmt.Sprintf("uid-%02d", index),
		Payload:     payload,
	}
}
