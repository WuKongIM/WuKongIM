package jsonrpc

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestHeaderAndSettingConversionsPreserveEveryWireFlag(t *testing.T) {
	header := Header{NoPersist: true, RedDot: true, SyncOnce: true, Dup: true, End: true}
	wantHeader := &frame.Framer{NoPersist: true, RedDot: true, SyncOnce: true, DUP: true, End: true}
	if got := header.ToProto(); !reflect.DeepEqual(got, wantHeader) {
		t.Fatalf("Header.ToProto() = %#v, want %#v", got, wantHeader)
	}

	flags := SettingFlags{Receipt: true, Signal: true, Stream: true, Topic: true}
	wantSetting := frame.SettingReceiptEnabled | frame.SettingSignal | frame.SettingStream | frame.SettingTopic
	if got := flags.ToProto(); got != wantSetting {
		t.Fatalf("SettingFlags.ToProto() = %d, want %d", got, wantSetting)
	}
}

func TestNilAndControlFrameConversionsAreStable(t *testing.T) {
	if got := FromProtoConnectAck(nil); got != nil {
		t.Fatalf("FromProtoConnectAck(nil) = %#v", got)
	}
	if got := FromProtoSendAck(nil); got != nil {
		t.Fatalf("FromProtoSendAck(nil) = %#v", got)
	}
	if got := FromProtoDisconnectPacket(nil); got != (DisconnectNotificationParams{}) {
		t.Fatalf("FromProtoDisconnectPacket(nil) = %#v", got)
	}
	if got := (PingParams{}).ToProto(); got == nil || got.GetFrameType() != frame.PING {
		t.Fatalf("PingParams.ToProto() = %#v", got)
	}
	FromProtoPongPacket(nil)
	FromProtoPongPacket(&frame.PongPacket{})

	request := ConnectRequest{Params: ConnectParams{Version: 3, UID: "alice", Token: "token"}}
	packet := request.ToProto()
	if packet.Version != 3 || packet.UID != "alice" || packet.Token != "token" {
		t.Fatalf("ConnectRequest.ToProto() = %#v", packet)
	}
}

func TestNewRequestBuildsTheTypedProtocolRequest(t *testing.T) {
	ping := &PingParams{}
	tests := []struct {
		name   string
		method string
		params interface{}
		check  func(*testing.T, interface{})
	}{
		{name: "connect", method: MethodConnect, params: ConnectParams{UID: "alice"}, check: func(t *testing.T, got interface{}) {
			if _, ok := got.(ConnectRequest); !ok {
				t.Fatalf("got %T", got)
			}
		}},
		{name: "send", method: MethodSend, params: SendParams{ChannelID: "room"}, check: func(t *testing.T, got interface{}) {
			if _, ok := got.(SendRequest); !ok {
				t.Fatalf("got %T", got)
			}
		}},
		{name: "subscribe", method: MethodSubscribe, params: SubscribeParams{SubNo: "presence"}, check: func(t *testing.T, got interface{}) {
			if _, ok := got.(SubscribeRequest); !ok {
				t.Fatalf("got %T", got)
			}
		}},
		{name: "unsubscribe", method: MethodUnsubscribe, params: UnsubscribeParams{SubNo: "presence"}, check: func(t *testing.T, got interface{}) {
			if _, ok := got.(UnsubscribeRequest); !ok {
				t.Fatalf("got %T", got)
			}
		}},
		{name: "disconnect", method: MethodDisconnect, params: DisconnectParams{Reason: "bye"}, check: func(t *testing.T, got interface{}) {
			if _, ok := got.(DisconnectRequest); !ok {
				t.Fatalf("got %T", got)
			}
		}},
		{name: "ping value", method: MethodPing, params: PingParams{}, check: func(t *testing.T, got interface{}) {
			request, ok := got.(PingRequest)
			if !ok || request.Params == nil {
				t.Fatalf("got %#v", got)
			}
		}},
		{name: "ping pointer", method: MethodPing, params: ping, check: func(t *testing.T, got interface{}) {
			request, ok := got.(PingRequest)
			if !ok || request.Params != ping {
				t.Fatalf("got %#v", got)
			}
		}},
		{name: "ping nil", method: MethodPing, params: nil, check: func(t *testing.T, got interface{}) {
			request, ok := got.(PingRequest)
			if !ok || request.Params != nil {
				t.Fatalf("got %#v", got)
			}
		}},
		{name: "unsupported params retain base request", method: "future", params: struct{}{}, check: assertBaseRequest},
		{name: "nil non-ping retains base request", method: MethodSend, params: nil, check: assertBaseRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewRequest(tt.method, "request-9", tt.params)
			tt.check(t, got)
			base := baseRequestOf(got)
			if base.Jsonrpc != jsonRPCVersion || base.Method != tt.method || base.ID != "request-9" {
				t.Fatalf("base request = %#v", base)
			}
		})
	}
}

func assertBaseRequest(t *testing.T, got interface{}) {
	t.Helper()
	if _, ok := got.(BaseRequest); !ok {
		t.Fatalf("got %T, want BaseRequest", got)
	}
}

func baseRequestOf(value interface{}) BaseRequest {
	switch request := value.(type) {
	case ConnectRequest:
		return request.BaseRequest
	case SendRequest:
		return request.BaseRequest
	case SubscribeRequest:
		return request.BaseRequest
	case UnsubscribeRequest:
		return request.BaseRequest
	case DisconnectRequest:
		return request.BaseRequest
	case PingRequest:
		return request.BaseRequest
	case BaseRequest:
		return request
	default:
		return BaseRequest{}
	}
}

func TestResponseConstructorsPreserveCorrelationAndExclusivity(t *testing.T) {
	success := NewGenericResponse("request-10", json.RawMessage(`{"ok":true}`))
	if success.Jsonrpc != jsonRPCVersion || success.ID != "request-10" || success.Error != nil || string(success.Result) != `{"ok":true}` {
		t.Fatalf("NewGenericResponse() = %#v", success)
	}

	wantErr := &ErrorObject{Code: 17, Message: "rejected"}
	failure := NewGenericResponseWithErr("request-11", wantErr)
	if failure.Jsonrpc != jsonRPCVersion || failure.ID != "request-11" || failure.Error != wantErr || failure.Result != nil {
		t.Fatalf("NewGenericResponseWithErr() = %#v", failure)
	}

	for _, tt := range []struct {
		name    string
		packet  *frame.ConnackPacket
		failure bool
	}{
		{name: "success", packet: &frame.ConnackPacket{ServerVersion: 4, NodeId: 7, ReasonCode: frame.ReasonSuccess}},
		{name: "failure", packet: &frame.ConnackPacket{ReasonCode: frame.ReasonAuthFail}, failure: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response := FromProtoConnackNotification("request-12", tt.packet)
			if response.Jsonrpc != jsonRPCVersion || response.ID != "request-12" {
				t.Fatalf("response base = %#v", response.BaseResponse)
			}
			if tt.failure {
				if response.Result != nil || response.Error == nil || response.Error.Code != int(frame.ReasonAuthFail) {
					t.Fatalf("failure response = %#v", response)
				}
				return
			}
			if response.Error != nil || response.Result == nil || response.Result.NodeID != 7 {
				t.Fatalf("success response = %#v", response)
			}
		})
	}
}
