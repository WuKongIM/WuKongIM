package jsonrpc_test

import (
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
