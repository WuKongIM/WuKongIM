package jsonrpc_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/protocol"
	adapterpkg "github.com/WuKongIM/WuKongIM/pkg/gateway/protocol/jsonrpc"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/testkit"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	pkgjsonrpc "github.com/WuKongIM/WuKongIM/pkg/protocol/jsonrpc"
)

func TestAdapterOwnsDecodedFrames(t *testing.T) {
	owner, ok := any(adapterpkg.New()).(protocol.DecodedFrameOwner)
	if !ok {
		t.Fatal("jsonrpc adapter does not implement DecodedFrameOwner")
	}
	if !owner.OwnsDecodedFrames() {
		t.Fatal("jsonrpc adapter should mark decoded frames as owned")
	}
}

func TestAdapterRequiresConnectAuthentication(t *testing.T) {
	policy, ok := any(adapterpkg.New()).(protocol.ConnectAuthenticationPolicy)
	if !ok {
		t.Fatal("jsonrpc adapter does not declare its CONNECT authentication policy")
	}
	required, resolved := policy.ConnectAuthenticationRequired(testkit.NewProtocolSession())
	if !resolved || !required {
		t.Fatalf("ConnectAuthenticationRequired() = (%v, %v), want (true, true)", required, resolved)
	}
}

func TestAdapterDecodeReturnsReplyTokenForRequest(t *testing.T) {
	adapter := adapterpkg.New()
	sess := testkit.NewProtocolSession()

	payload, err := pkgjsonrpc.Encode(pkgjsonrpc.PingRequest{
		BaseRequest: pkgjsonrpc.BaseRequest{
			Jsonrpc: "2.0",
			Method:  pkgjsonrpc.MethodPing,
			ID:      "req-1",
		},
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	frames, consumed, err := adapter.Decode(sess, payload)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	tracker, ok := any(adapter).(protocol.ReplyTokenTracker)
	if !ok {
		t.Fatalf("adapter does not implement ReplyTokenTracker")
	}
	tokens := tracker.TakeReplyTokens(sess, len(frames))
	if consumed != len(payload) {
		t.Fatalf("expected consumed=%d, got %d", len(payload), consumed)
	}
	if len(frames) != 1 {
		t.Fatalf("expected one frame, got %d", len(frames))
	}
	if len(tokens) != 1 || tokens[0] != "req-1" {
		t.Fatalf("expected reply token req-1, got %v", tokens)
	}
	if _, ok := frames[0].(*frame.PingPacket); !ok {
		t.Fatalf("expected ping packet, got %T", frames[0])
	}
}

func TestAdapterEncodeUsesReplyTokenAsResponseID(t *testing.T) {
	adapter := adapterpkg.New()
	sess := testkit.NewProtocolSession()

	body, err := adapter.Encode(sess, &frame.PongPacket{}, session.OutboundMeta{ReplyToken: "req-1"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !pkgjsonrpc.IsJSONObjectPrefix(body) {
		t.Fatalf("expected json object payload, got %q", body)
	}
	if !strings.Contains(string(body), `"id":"req-1"`) {
		t.Fatalf("expected response id in payload: %s", body)
	}
}

func TestAdapterBridgesSubscriptionRequestAndCorrelatedAcknowledgement(t *testing.T) {
	adapter := adapterpkg.New()
	sess := testkit.NewProtocolSession()

	request := []byte(`{"jsonrpc":"2.0","id":"sub-1","method":"subscribe","params":{"subNo":"presence","channelId":"room-1","channelType":2,"param":"online"}}`)
	frames, consumed, err := adapter.Decode(sess, request)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if consumed != len(request) || len(frames) != 1 {
		t.Fatalf("Decode() consumed %d and returned %d frames", consumed, len(frames))
	}
	subscription, ok := frames[0].(*frame.SubPacket)
	if !ok || subscription.Action != frame.Subscribe || subscription.SubNo != "presence" || subscription.ChannelID != "room-1" || subscription.ChannelType != 2 {
		t.Fatalf("subscription frame = %#v", frames[0])
	}

	tokens := adapter.TakeReplyTokens(sess, 1)
	if len(tokens) != 1 || tokens[0] != "sub-1" {
		t.Fatalf("reply tokens = %#v", tokens)
	}
	body, err := adapter.Encode(sess, &frame.SubackPacket{
		SubNo: "presence", ChannelID: "room-1", ChannelType: 2,
		Action: frame.Subscribe, ReasonCode: frame.ReasonSuccess,
	}, session.OutboundMeta{ReplyToken: tokens[0]})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	var response struct {
		ID     string `json:"id"`
		Result struct {
			SubNo       string `json:"subNo"`
			ChannelID   string `json:"channelId"`
			ChannelType int    `json:"channelType"`
			Action      int    `json:"action"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("subscription response JSON = %q: %v", body, err)
	}
	if response.ID != "sub-1" || response.Result.SubNo != "presence" || response.Result.ChannelID != "room-1" || response.Result.ChannelType != 2 || response.Result.Action != int(pkgjsonrpc.ActionSubscribe) {
		t.Fatalf("subscription response = %#v", response)
	}
}
