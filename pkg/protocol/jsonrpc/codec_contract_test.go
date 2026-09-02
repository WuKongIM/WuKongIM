package jsonrpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestCodecPublicHelpersPreserveCorrelationAndFraming(t *testing.T) {
	encoded := EncodeErrorResponse("request-7", errors.New("permission denied"))
	decoded, _, err := Decode(json.NewDecoder(bytes.NewReader(encoded)))
	if err != nil {
		t.Fatalf("Decode(error response) error = %v", err)
	}
	response, ok := decoded.(GenericResponse)
	if !ok || response.ID != "request-7" || response.Error == nil || response.Error.Message != "permission denied" {
		t.Fatalf("error response = %#v", decoded)
	}

	if got := DecodeID(json.RawMessage(`"request-8"`)); got != "request-8" {
		t.Fatalf("DecodeID(string) = %q", got)
	}
	if got := DecodeID(json.RawMessage(`8`)); got != "" {
		t.Fatalf("DecodeID(number) = %q, want empty", got)
	}

	for _, data := range [][]byte{[]byte(`{}`), []byte(" \t\r\n{")} {
		if !IsJSONObjectPrefix(data) {
			t.Fatalf("IsJSONObjectPrefix(%q) = false", data)
		}
	}
	for _, data := range [][]byte{nil, []byte(" \n\t"), []byte("[]"), []byte("null")} {
		if IsJSONObjectPrefix(data) {
			t.Fatalf("IsJSONObjectPrefix(%q) = true", data)
		}
	}

	if _, err := Encode(make(chan int)); err == nil || !strings.Contains(err.Error(), "jsonrpc encode") {
		t.Fatalf("Encode(unsupported) error = %v", err)
	}
}

func TestDecodeRejectsMalformedProtocolShapes(t *testing.T) {
	tests := []struct {
		name string
		wire string
		want error
	}{
		{name: "version type", wire: `{"jsonrpc":2,"id":"1","method":"ping"}`, want: ErrUnmarshalFieldFailed},
		{name: "wrong version", wire: `{"jsonrpc":"1.0","id":"1","method":"ping"}`, want: ErrInvalidVersion},
		{name: "numeric request id", wire: `{"jsonrpc":"2.0","id":1,"method":"ping"}`, want: ErrUnmarshalFieldFailed},
		{name: "unknown request", wire: `{"jsonrpc":"2.0","id":"1","method":"missing","params":{}}`, want: ErrUnknownMethod},
		{name: "connect missing params", wire: `{"jsonrpc":"2.0","id":"1","method":"connect"}`, want: ErrMissingParams},
		{name: "send malformed params", wire: `{"jsonrpc":"2.0","id":"1","method":"send","params":1}`, want: ErrUnmarshalFieldFailed},
		{name: "subscribe missing params", wire: `{"jsonrpc":"2.0","id":"1","method":"subscribe"}`, want: ErrMissingParams},
		{name: "unsubscribe malformed params", wire: `{"jsonrpc":"2.0","id":"1","method":"unsubscribe","params":1}`, want: ErrUnmarshalFieldFailed},
		{name: "disconnect missing params", wire: `{"jsonrpc":"2.0","id":"1","method":"disconnect"}`, want: ErrMissingParams},
		{name: "ping malformed params", wire: `{"jsonrpc":"2.0","id":"1","method":"ping","params":1}`, want: ErrUnmarshalFieldFailed},
		{name: "recv missing params", wire: `{"jsonrpc":"2.0","method":"recv"}`, want: ErrMissingParams},
		{name: "recvack malformed params", wire: `{"jsonrpc":"2.0","method":"recvack","params":1}`, want: ErrUnmarshalFieldFailed},
		{name: "disconnect notification missing params", wire: `{"jsonrpc":"2.0","method":"disconnect"}`, want: ErrMissingParams},
		{name: "event malformed params", wire: `{"jsonrpc":"2.0","method":"event","params":1}`, want: ErrUnmarshalFieldFailed},
		{name: "response result and error", wire: `{"jsonrpc":"2.0","id":"1","result":{},"error":{"code":1,"message":"bad"}}`, want: ErrResponseFormat},
		{name: "response numeric id", wire: `{"jsonrpc":"2.0","id":1,"result":{}}`, want: ErrUnmarshalFieldFailed},
		{name: "response malformed error", wire: `{"jsonrpc":"2.0","id":"1","error":1}`, want: ErrUnmarshalFieldFailed},
		{name: "orphan result", wire: `{"jsonrpc":"2.0","result":{}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := Decode(json.NewDecoder(strings.NewReader(tt.wire)))
			if tt.want == nil {
				if err == nil {
					t.Fatal("Decode() error = nil")
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("Decode() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestFrameBridgePreservesClientControlPackets(t *testing.T) {
	connect := ConnectRequest{
		BaseRequest: BaseRequest{ID: "connect-1"},
		Params: ConnectParams{
			Header:  Header{NoPersist: true, RedDot: true, SyncOnce: true, Dup: true, End: true},
			Version: 4, ClientKey: "key", DeviceID: "device", DeviceFlag: DeviceWeb,
			ClientTimestamp: 123, UID: "u1", Token: "token",
		},
	}
	got, id, err := ToFrame(connect)
	if err != nil || id != "connect-1" {
		t.Fatalf("ToFrame(connect) = %T, %q, %v", got, id, err)
	}
	connectPacket, ok := got.(*frame.ConnectPacket)
	if !ok || connectPacket.Version != 4 || connectPacket.UID != "u1" || !connectPacket.NoPersist || !connectPacket.End {
		t.Fatalf("connect packet = %#v", got)
	}

	got, id, err = ToFrame(PingRequest{BaseRequest: BaseRequest{ID: "ping-1"}})
	if err != nil || id != "ping-1" || got.GetFrameType() != frame.PING {
		t.Fatalf("ToFrame(ping) = %T, %q, %v", got, id, err)
	}

	got, id, err = ToFrame(DisconnectRequest{
		BaseRequest: BaseRequest{ID: "disconnect-1"},
		Params:      DisconnectParams{ReasonCode: ReasonCodeEnum(frame.ReasonConnectKick), Reason: "other device"},
	})
	disconnect, ok := got.(*frame.DisconnectPacket)
	if err != nil || !ok || id != "disconnect-1" || disconnect.ReasonCode != frame.ReasonConnectKick || disconnect.Reason != "other device" {
		t.Fatalf("ToFrame(disconnect) = %#v, %q, %v", got, id, err)
	}

	got, id, err = ToFrame(RecvAckNotification{Params: RecvAckParams{
		Header: Header{RedDot: true}, MessageID: "9223372036854775000", MessageSeq: 19,
	}})
	recvAck, ok := got.(*frame.RecvackPacket)
	if err != nil || !ok || id != "" || recvAck.MessageID != 9_223_372_036_854_775_000 || recvAck.MessageSeq != 19 || !recvAck.RedDot {
		t.Fatalf("ToFrame(recvack) = %#v, %q, %v", got, id, err)
	}

	if got, id, err := ToFrame(struct{}{}); err == nil || got != nil || id != "" {
		t.Fatalf("ToFrame(unknown) = %#v, %q, %v", got, id, err)
	}
}

func TestFrameBridgePreservesServerPacketsAndFailures(t *testing.T) {
	tests := []struct {
		name  string
		id    string
		input frame.Frame
		check func(*testing.T, interface{})
	}{
		{
			name: "connect failure", id: "connect-2",
			input: &frame.ConnackPacket{ReasonCode: frame.ReasonAuthFail},
			check: func(t *testing.T, message interface{}) {
				response, ok := message.(ConnectResponse)
				if !ok || response.ID != "connect-2" || response.Error == nil || response.Error.Code != int(frame.ReasonAuthFail) {
					t.Fatalf("connect response = %#v", message)
				}
			},
		},
		{
			name: "send success", id: "send-1",
			input: &frame.SendackPacket{Framer: frame.Framer{RedDot: true}, MessageID: -7, MessageSeq: 11, ReasonCode: frame.ReasonSuccess},
			check: func(t *testing.T, message interface{}) {
				response, ok := message.(SendResponse)
				if !ok || response.ID != "send-1" || response.Result == nil || response.Result.MessageID != "-7" || response.Result.MessageSeq != 11 || response.Result.Header == nil || !response.Result.Header.RedDot {
					t.Fatalf("send response = %#v", message)
				}
			},
		},
		{
			name: "send failure", id: "send-2",
			input: &frame.SendackPacket{ReasonCode: frame.ReasonRateLimit},
			check: func(t *testing.T, message interface{}) {
				response, ok := message.(SendResponse)
				if !ok || response.ID != "send-2" || response.Error == nil || response.Error.Code != int(frame.ReasonRateLimit) {
					t.Fatalf("send response = %#v", message)
				}
			},
		},
		{
			name: "receive notification",
			input: &frame.RecvPacket{
				Framer: frame.Framer{NoPersist: true}, Setting: frame.SettingReceiptEnabled | frame.SettingSignal | frame.SettingStream | frame.SettingTopic,
				MessageID: 8, MessageSeq: 9, StreamId: 10, ChannelID: "room", ChannelType: 2, Payload: []byte(`{"ok":true}`),
			},
			check: func(t *testing.T, message interface{}) {
				notification, ok := message.(RecvNotification)
				if !ok || notification.Params.Header == nil || !notification.Params.Header.NoPersist || notification.Params.Setting == nil || !notification.Params.Setting.Receipt || !notification.Params.Setting.Signal || !notification.Params.Setting.Stream || !notification.Params.Setting.Topic || notification.Params.StreamID != "10" {
					t.Fatalf("receive notification = %#v", message)
				}
			},
		},
		{
			name:  "event notification",
			input: &frame.EventPacket{Framer: frame.Framer{End: true}, Id: "evt-1", Type: "done", Timestamp: 12, Data: []byte("payload")},
			check: func(t *testing.T, message interface{}) {
				notification, ok := message.(EventNotification)
				if !ok || notification.Params.Header == nil || !notification.Params.Header.End || notification.Params.ID != "evt-1" || notification.Params.Data != "payload" {
					t.Fatalf("event notification = %#v", message)
				}
			},
		},
		{
			name:  "disconnect notification",
			input: &frame.DisconnectPacket{ReasonCode: frame.ReasonConnectKick, Reason: "other device"},
			check: func(t *testing.T, message interface{}) {
				notification, ok := message.(DisconnectNotification)
				if !ok || notification.Params.ReasonCode != ReasonCodeEnum(frame.ReasonConnectKick) || notification.Params.Reason != "other device" {
					t.Fatalf("disconnect notification = %#v", message)
				}
			},
		},
		{
			name: "pong", id: "ping-2", input: &frame.PongPacket{},
			check: func(t *testing.T, message interface{}) {
				response, ok := message.(PongResponse)
				if !ok || response.ID != "ping-2" || string(response.Result) != "null" {
					t.Fatalf("pong response = %#v", message)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message, err := FromFrame(tt.id, tt.input)
			if err != nil {
				t.Fatalf("FromFrame() error = %v", err)
			}
			tt.check(t, message)
		})
	}

	if message, err := FromFrame("request", &frame.PingPacket{}); err == nil || message != nil {
		t.Fatalf("FromFrame(unknown) = %#v, %v", message, err)
	}
}
