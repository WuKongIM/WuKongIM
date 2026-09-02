package wsmux_test

import (
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/protocol"
	adapterpkg "github.com/WuKongIM/WuKongIM/pkg/gateway/protocol/wsmux"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/testkit"
	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestAdapterSelectsAndPinsJSONRPCForConnection(t *testing.T) {
	t.Parallel()

	adapter := adapterpkg.New()
	sess := testkit.NewProtocolSession()
	payload := []byte(`  {"jsonrpc":"2.0","id":"req-1","method":"ping"}`)
	frames, consumed, err := adapter.Decode(sess, payload)
	if err != nil {
		t.Fatalf("Decode(JSON-RPC): %v", err)
	}
	if consumed != len(payload) || len(frames) != 1 {
		t.Fatalf("Decode(JSON-RPC) = %d bytes, %d frames", consumed, len(frames))
	}
	if selected, _ := sess.Value(gatewaytypes.SessionValueProtocolName).(string); selected != "jsonrpc" {
		t.Fatalf("selected protocol = %q, want jsonrpc", selected)
	}
	tracker, ok := any(adapter).(protocol.ReplyTokenTracker)
	if !ok {
		t.Fatal("adapter does not implement ReplyTokenTracker")
	}
	if got := tracker.TakeReplyTokens(sess, 1); len(got) != 1 || got[0] != "req-1" {
		t.Fatalf("reply tokens = %#v, want req-1", got)
	}
	encoded, err := adapter.Encode(sess, &frame.PongPacket{}, session.OutboundMeta{ReplyToken: "req-1"})
	if err != nil {
		t.Fatalf("Encode(JSON-RPC): %v", err)
	}
	if !strings.Contains(string(encoded), `"id":"req-1"`) {
		t.Fatalf("encoded response = %q, want correlated request id", encoded)
	}
}

func TestAdapterSelectsWKProtoAndKeepsSelectionSticky(t *testing.T) {
	t.Parallel()

	adapter := adapterpkg.New()
	sess := testkit.NewProtocolSession()
	wire, err := codec.New().EncodeFrame(&frame.PingPacket{}, frame.LatestVersion)
	if err != nil {
		t.Fatalf("EncodeFrame(): %v", err)
	}
	frames, consumed, err := adapter.Decode(sess, wire)
	if err != nil {
		t.Fatalf("Decode(WKProto): %v", err)
	}
	if consumed != len(wire) || len(frames) != 1 {
		t.Fatalf("Decode(WKProto) = %d bytes, %d frames", consumed, len(frames))
	}
	if selected, _ := sess.Value(gatewaytypes.SessionValueProtocolName).(string); selected != "wkproto" {
		t.Fatalf("selected protocol = %q, want wkproto", selected)
	}
	if tokens := adapter.TakeReplyTokens(sess, 1); tokens != nil {
		t.Fatalf("WKProto reply tokens = %#v, want nil", tokens)
	}
	_, _, _ = adapter.Decode(sess, []byte(`{"jsonrpc":"2.0","method":"ping"}`))
	if selected, _ := sess.Value(gatewaytypes.SessionValueProtocolName).(string); selected != "wkproto" {
		t.Fatalf("protocol switch changed pinned selection to %q", selected)
	}
}

func TestAdapterRejectsMissingUnknownAndNilProtocolSelection(t *testing.T) {
	t.Parallel()

	adapter := adapterpkg.New()
	sess := testkit.NewProtocolSession()
	if frames, consumed, err := adapter.Decode(sess, []byte(" \r\n\t")); err != nil || frames != nil || consumed != 0 {
		t.Fatalf("Decode(whitespace) = (%#v, %d, %v)", frames, consumed, err)
	}
	if _, err := adapter.Encode(sess, &frame.PongPacket{}, session.OutboundMeta{}); err == nil {
		t.Fatal("Encode(unselected) error = nil")
	}
	sess.SetValue(gatewaytypes.SessionValueProtocolName, "unknown")
	if _, _, err := adapter.Decode(sess, []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "unsupported protocol") {
		t.Fatalf("Decode(unknown) error = %v", err)
	}
	if err := adapter.OnClose(sess); err != nil {
		t.Fatalf("OnClose(unknown): %v", err)
	}

	var nilAdapter *adapterpkg.Adapter
	if nilAdapter.Name() != "" || nilAdapter.OwnsDecodedFrames() {
		t.Fatal("nil adapter reported protocol capabilities")
	}
	if _, _, err := nilAdapter.Decode(testkit.NewProtocolSession(), []byte(`{}`)); err == nil {
		t.Fatal("nil adapter Decode() error = nil")
	}
	if tokens := adapter.TakeReplyTokens(sess, 0); tokens != nil {
		t.Fatalf("TakeReplyTokens(count=0) = %#v, want nil", tokens)
	}
}
