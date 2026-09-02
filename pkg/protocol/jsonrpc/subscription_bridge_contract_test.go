package jsonrpc

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestSubscriptionRequestsBridgeToProtocolFrames(t *testing.T) {
	tests := []struct {
		name       string
		wire       string
		wantID     string
		wantAction frame.Action
	}{
		{
			name:       "subscribe",
			wire:       `{"jsonrpc":"2.0","id":"sub-1","method":"subscribe","params":{"subNo":"presence","channelId":"room-1","channelType":2,"param":"online"}}`,
			wantID:     "sub-1",
			wantAction: frame.Subscribe,
		},
		{
			name:       "unsubscribe",
			wire:       `{"jsonrpc":"2.0","id":"sub-2","method":"unsubscribe","params":{"subNo":"presence","channelId":"room-1","channelType":2}}`,
			wantID:     "sub-2",
			wantAction: frame.UnSubscribe,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decoded, _, err := Decode(json.NewDecoder(bytes.NewBufferString(tt.wire)))
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			protocolFrame, requestID, err := ToFrame(decoded)
			if err != nil {
				t.Fatalf("ToFrame() error = %v", err)
			}
			if requestID != tt.wantID {
				t.Fatalf("request ID = %q, want %q", requestID, tt.wantID)
			}
			sub, ok := protocolFrame.(*frame.SubPacket)
			if !ok {
				t.Fatalf("frame = %T, want *frame.SubPacket", protocolFrame)
			}
			if sub.Action != tt.wantAction || sub.SubNo != "presence" || sub.ChannelID != "room-1" || sub.ChannelType != 2 {
				t.Fatalf("subscription frame = %#v", sub)
			}
		})
	}
}

func TestSubscriptionAcknowledgementsPreserveCorrelationAndFailure(t *testing.T) {
	tests := []struct {
		name       string
		reason     frame.ReasonCode
		wantResult bool
	}{
		{name: "success", reason: frame.ReasonSuccess, wantResult: true},
		{name: "failure", reason: frame.ReasonNotAllowSend, wantResult: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message, err := FromFrame("sub-3", &frame.SubackPacket{
				SubNo: "presence", ChannelID: "room-1", ChannelType: 2,
				Action: frame.Subscribe, ReasonCode: tt.reason,
			})
			if err != nil {
				t.Fatalf("FromFrame() error = %v", err)
			}
			response, ok := message.(SubscriptionResponse)
			if !ok {
				t.Fatalf("response = %T, want SubscriptionResponse", message)
			}
			if response.ID != "sub-3" {
				t.Fatalf("response ID = %q, want sub-3", response.ID)
			}
			if tt.wantResult {
				if response.Error != nil || response.Result == nil {
					t.Fatalf("success response = %#v", response)
				}
				if response.Result.SubNo != "presence" || response.Result.ChannelID != "room-1" || response.Result.ChannelType != 2 || response.Result.Action != ActionSubscribe {
					t.Fatalf("subscription result = %#v", response.Result)
				}
				return
			}
			if response.Result != nil || response.Error == nil || response.Error.Code != int(tt.reason) {
				t.Fatalf("failure response = %#v", response)
			}
		})
	}
}
