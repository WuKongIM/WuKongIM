package jsonrpc

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// These fixtures preserve the wire shapes from exact released revisions:
// iOS v1.0.3 643848f85be70e3e3f2be22fceb86ae428b6cc38,
// Android v1.0.3 62084632cd8d1f26c751b053b0fb82d6aaa63892,
// Flutter v1.0.4 5b199f92972065549ed3bb0a3296e89b79246061, and
// JavaScript v2.0.2 55da36d4992d8272cfc56486a98b33895df98be6.
// IDs and message content are synthetic; field and payload shapes are source-aligned.

func TestDecodeEasySDKConnectRequests(t *testing.T) {
	tests := []struct {
		name      string
		wire      string
		id        string
		deviceID  string
		device    DeviceFlagEnum
		timestamp int64
	}{
		{
			name:      "iOS v1.0.3",
			wire:      `{"jsonrpc":"2.0","method":"connect","params":{"uid":"alice","token":"token","deviceId":"ios-device","deviceFlag":0,"clientTimestamp":1725000000001},"id":"ios-connect"}`,
			id:        "ios-connect",
			deviceID:  "ios-device",
			device:    DeviceApp,
			timestamp: 1725000000001,
		},
		{
			name:      "Android v1.0.3",
			wire:      `{"jsonrpc":"2.0","method":"connect","params":{"uid":"alice","token":"token","device_id":"android-device","device_flag":1,"client_timestamp":1725000000002},"id":"android-connect"}`,
			id:        "android-connect",
			deviceID:  "android-device",
			device:    DeviceWeb,
			timestamp: 1725000000002,
		},
		{
			name:      "Flutter v1.0.4",
			wire:      `{"jsonrpc":"2.0","method":"connect","params":{"uid":"alice","token":"token","deviceId":"flutter-device","deviceFlag":0,"clientTimestamp":1725000000003},"id":"flutter-connect"}`,
			id:        "flutter-connect",
			deviceID:  "flutter-device",
			device:    DeviceApp,
			timestamp: 1725000000003,
		},
		{
			name:      "JavaScript v2.0.2 omits jsonrpc",
			wire:      `{"method":"connect","params":{"uid":"alice","token":"token","deviceId":"js-device","deviceFlag":1,"clientTimestamp":1725000000004},"id":"js-connect"}`,
			id:        "js-connect",
			deviceID:  "js-device",
			device:    DeviceWeb,
			timestamp: 1725000000004,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decoded, _, err := Decode(json.NewDecoder(bytes.NewBufferString(tt.wire)))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}

			request, ok := decoded.(ConnectRequest)
			if !ok {
				t.Fatalf("expected ConnectRequest, got %T", decoded)
			}
			if request.Jsonrpc != jsonRPCVersion {
				t.Fatalf("expected normalized jsonrpc %q, got %q", jsonRPCVersion, request.Jsonrpc)
			}
			if request.ID != tt.id || request.Params.DeviceID != tt.deviceID || request.Params.DeviceFlag != tt.device || request.Params.ClientTimestamp != tt.timestamp {
				t.Fatalf("decoded request mismatch: %+v", request)
			}
		})
	}
}

func TestDecodeEasySDKSendRequests(t *testing.T) {
	tests := []struct {
		name        string
		wire        string
		clientMsgNo string
		channelID   string
		payload     string
		redDot      bool
	}{
		{
			name:        "iOS v1.0.3 object payload",
			wire:        `{"jsonrpc":"2.0","method":"send","params":{"clientMsgNo":"ios-1","channelId":"bob","channelType":1,"payload":{"content":"hello","type":1},"header":{"redDot":true}},"id":"ios-send"}`,
			clientMsgNo: "ios-1",
			channelID:   "bob",
			payload:     `{"content":"hello","type":1}`,
			redDot:      true,
		},
		{
			name:        "Android v1.0.3 snake case JSON text payload",
			wire:        `{"jsonrpc":"2.0","method":"send","params":{"client_msg_no":"android-1","channel_id":"bob","channel_type":1,"payload":"{\"content\":\"hello\",\"type\":1}","header":{"no_persist":false,"red_dot":true,"sync_once":false,"dup":false}},"id":"android-send"}`,
			clientMsgNo: "android-1",
			channelID:   "bob",
			payload:     `{"content":"hello","type":1}`,
			redDot:      true,
		},
		{
			name:        "Flutter v1.0.4 base64 payload",
			wire:        `{"jsonrpc":"2.0","method":"send","params":{"clientMsgNo":"flutter-1","channelId":"bob","channelType":1,"payload":"eyJjb250ZW50IjoiaGVsbG8iLCJ0eXBlIjoxfQ==","header":{"redDot":true}},"id":"flutter-send"}`,
			clientMsgNo: "flutter-1",
			channelID:   "bob",
			payload:     `{"content":"hello","type":1}`,
			redDot:      true,
		},
		{
			name:        "JavaScript v2.0.2 omits jsonrpc and uses base64",
			wire:        `{"method":"send","params":{"clientMsgNo":"js-1","channelId":"bob","channelType":1,"payload":"eyJjb250ZW50IjoiaGVsbG8iLCJ0eXBlIjoxfQ==","header":{"redDot":true}},"id":"js-send"}`,
			clientMsgNo: "js-1",
			channelID:   "bob",
			payload:     `{"content":"hello","type":1}`,
			redDot:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decoded, _, err := Decode(json.NewDecoder(bytes.NewBufferString(tt.wire)))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}

			request, ok := decoded.(SendRequest)
			if !ok {
				t.Fatalf("expected SendRequest, got %T", decoded)
			}
			if request.Params.ClientMsgNo != tt.clientMsgNo || request.Params.ChannelID != tt.channelID || request.Params.ChannelType != 1 {
				t.Fatalf("decoded routing mismatch: %+v", request.Params)
			}
			if string(request.Params.Payload) != tt.payload {
				t.Fatalf("expected payload %s, got %s", tt.payload, request.Params.Payload)
			}
			if request.Params.Header.RedDot != tt.redDot {
				t.Fatalf("expected redDot=%v, got %+v", tt.redDot, request.Params.Header)
			}
		})
	}
}

func TestDecodeEasySDKRecvAckNotifications(t *testing.T) {
	tests := []struct {
		name       string
		wire       string
		messageID  string
		messageSeq uint64
	}{
		{
			name:       "Android v1.0.3 snake case",
			wire:       `{"jsonrpc":"2.0","method":"recvack","params":{"message_id":"101","message_seq":22}}`,
			messageID:  "101",
			messageSeq: 22,
		},
		{
			name:       "iOS v1.0.3 camel case",
			wire:       `{"jsonrpc":"2.0","method":"recvack","params":{"messageId":"102","messageSeq":23}}`,
			messageID:  "102",
			messageSeq: 23,
		},
		{
			name:       "Flutter v1.0.4 camel case",
			wire:       `{"jsonrpc":"2.0","method":"recvack","params":{"messageId":"103","messageSeq":24}}`,
			messageID:  "103",
			messageSeq: 24,
		},
		{
			name:       "JavaScript v2.0.2 omits jsonrpc",
			wire:       `{"method":"recvack","params":{"messageId":"104","messageSeq":25}}`,
			messageID:  "104",
			messageSeq: 25,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decoded, _, err := Decode(json.NewDecoder(bytes.NewBufferString(tt.wire)))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}

			notification, ok := decoded.(RecvAckNotification)
			if !ok {
				t.Fatalf("expected RecvAckNotification, got %T", decoded)
			}
			if notification.Params.MessageID != tt.messageID || notification.Params.MessageSeq != tt.messageSeq {
				t.Fatalf("decoded recvack mismatch: %+v", notification.Params)
			}
		})
	}
}

func TestEncodeEasySDKSuccessResponsesExposeCamelAndSnakeCase(t *testing.T) {
	tests := []struct {
		name     string
		request  string
		packet   frame.Frame
		expected map[string]any
	}{
		{
			name:    "connect",
			request: "connect-1",
			packet: &frame.ConnackPacket{
				ServerVersion: 4,
				ServerKey:     "",
				Salt:          "",
				TimeDiff:      0,
				ReasonCode:    frame.ReasonSuccess,
				NodeId:        7,
			},
			expected: map[string]any{
				"serverVersion":  float64(4),
				"server_version": float64(4),
				"serverKey":      "",
				"server_key":     "",
				"salt":           "",
				"timeDiff":       float64(0),
				"time_diff":      float64(0),
				"reasonCode":     float64(frame.ReasonSuccess),
				"reason_code":    float64(frame.ReasonSuccess),
				"nodeId":         float64(7),
				"node_id":        float64(7),
			},
		},
		{
			name:    "sendack",
			request: "send-1",
			packet: &frame.SendackPacket{
				MessageID:  99,
				MessageSeq: 42,
				ReasonCode: frame.ReasonSuccess,
			},
			expected: map[string]any{
				"messageId":   "99",
				"message_id":  "99",
				"messageSeq":  float64(42),
				"message_seq": float64(42),
				"reasonCode":  float64(frame.ReasonSuccess),
				"reason_code": float64(frame.ReasonSuccess),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message, err := FromFrame(tt.request, tt.packet)
			if err != nil {
				t.Fatalf("FromFrame: %v", err)
			}
			encoded, err := Encode(message)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}

			var response struct {
				ID     string         `json:"id"`
				Result map[string]any `json:"result"`
			}
			if err := json.Unmarshal(encoded, &response); err != nil {
				t.Fatalf("Unmarshal response: %v", err)
			}
			if response.ID != tt.request {
				t.Fatalf("expected correlated id %q, got %q", tt.request, response.ID)
			}
			for key, expected := range tt.expected {
				if actual := response.Result[key]; actual != expected {
					t.Fatalf("expected result[%q]=%v, got %v in %s", key, expected, actual, encoded)
				}
			}
		})
	}
}

func TestEncodeEasySDKRecvNotificationIncludesHeaderAndObjectPayload(t *testing.T) {
	message, err := FromFrame("", &frame.RecvPacket{
		MessageID:   501,
		MessageSeq:  12,
		Timestamp:   1725000000,
		ChannelID:   "alice",
		ChannelType: 1,
		FromUID:     "bob",
		Payload:     []byte(`{"content":"hello","type":1}`),
	})
	if err != nil {
		t.Fatalf("FromFrame: %v", err)
	}
	encoded, err := Encode(message)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	var notification struct {
		Method string `json:"method"`
		Params struct {
			Header  map[string]any `json:"header"`
			Payload map[string]any `json:"payload"`
		} `json:"params"`
	}
	if err := json.Unmarshal(encoded, &notification); err != nil {
		t.Fatalf("Unmarshal notification: %v; wire=%s", err, encoded)
	}
	if notification.Method != MethodRecv {
		t.Fatalf("expected recv notification, got %q", notification.Method)
	}
	if notification.Params.Header == nil || len(notification.Params.Header) != 0 {
		t.Fatalf("expected required empty header object, got %#v in %s", notification.Params.Header, encoded)
	}
	if notification.Params.Payload["content"] != "hello" || notification.Params.Payload["type"] != float64(1) {
		t.Fatalf("expected object payload, got %#v in %s", notification.Params.Payload, encoded)
	}
}

func TestEncodeEasySDKFailureAcksAsCorrelatedErrors(t *testing.T) {
	tests := []struct {
		name       string
		request    string
		packet     frame.Frame
		reasonCode frame.ReasonCode
	}{
		{
			name:       "connect failure",
			request:    "connect-failed",
			packet:     &frame.ConnackPacket{ReasonCode: frame.ReasonAuthFail},
			reasonCode: frame.ReasonAuthFail,
		},
		{
			name:       "send failure",
			request:    "send-failed",
			packet:     &frame.SendackPacket{ReasonCode: frame.ReasonNotAllowSend},
			reasonCode: frame.ReasonNotAllowSend,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message, err := FromFrame(tt.request, tt.packet)
			if err != nil {
				t.Fatalf("FromFrame: %v", err)
			}
			encoded, err := Encode(message)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}

			var response struct {
				ID     string          `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *ErrorObject    `json:"error"`
			}
			if err := json.Unmarshal(encoded, &response); err != nil {
				t.Fatalf("Unmarshal response: %v", err)
			}
			if response.ID != tt.request {
				t.Fatalf("expected correlated id %q, got %q", tt.request, response.ID)
			}
			if response.Error == nil || response.Error.Code != int(tt.reasonCode) || response.Error.Message != tt.reasonCode.String() {
				t.Fatalf("expected reason error %d/%s, got %#v in %s", tt.reasonCode, tt.reasonCode.String(), response.Error, encoded)
			}
			if response.Result != nil {
				t.Fatalf("failure response must omit result, got %s", response.Result)
			}
		})
	}
}

func TestEncodeEasySDKPongAsCorrelatedNullResult(t *testing.T) {
	message, err := FromFrame("ping-1", &frame.PongPacket{})
	if err != nil {
		t.Fatalf("FromFrame: %v", err)
	}
	encoded, err := Encode(message)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	var response map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &response); err != nil {
		t.Fatalf("Unmarshal response: %v", err)
	}
	var id string
	if err := json.Unmarshal(response["id"], &id); err != nil {
		t.Fatalf("decode id: %v", err)
	}
	if id != "ping-1" {
		t.Fatalf("expected correlated id ping-1, got %q", id)
	}
	result, ok := response["result"]
	if !ok || string(result) != "null" {
		t.Fatalf("expected explicit result:null, got %s", encoded)
	}
	if _, ok := response["error"]; ok {
		t.Fatalf("successful pong must omit error: %s", encoded)
	}
}
