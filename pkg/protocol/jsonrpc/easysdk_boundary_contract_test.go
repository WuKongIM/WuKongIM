package jsonrpc

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestEasySDKUnmarshalRejectsInvalidShapes(t *testing.T) {
	tests := []struct {
		name   string
		target interface{}
		wire   string
	}{
		{name: "header", target: &Header{}, wire: `[]`},
		{name: "connect", target: &ConnectParams{}, wire: `[]`},
		{name: "send", target: &SendParams{}, wire: `[]`},
		{name: "recvack", target: &RecvAckParams{}, wire: `[]`},
		{name: "recv notification", target: &RecvNotificationParams{}, wire: `[]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(tt.wire), tt.target); err == nil {
				t.Fatal("json.Unmarshal() error = nil")
			}
		})
	}

	for _, wire := range []string{
		`{"payload":"%%%"}`,
		`{"payload":tru}`,
	} {
		var params RecvNotificationParams
		if err := json.Unmarshal([]byte(wire), &params); err == nil {
			t.Fatalf("json.Unmarshal(%s) error = nil", wire)
		}
	}
}

func TestEasySDKCamelCaseTakesPrecedenceOverAliases(t *testing.T) {
	var header Header
	if err := json.Unmarshal([]byte(`{"noPersist":false,"no_persist":true,"redDot":false,"red_dot":true,"syncOnce":false,"sync_once":true,"dup":false,"DUP":true,"end":true}`), &header); err != nil {
		t.Fatalf("Unmarshal(header): %v", err)
	}
	if header.NoPersist || header.RedDot || header.SyncOnce || header.Dup || !header.End {
		t.Fatalf("camel-case header precedence = %#v", header)
	}

	var connect ConnectParams
	if err := json.Unmarshal([]byte(`{"clientKey":"camel-key","client_key":"snake-key","deviceId":"camel-device","device_id":"snake-device","deviceFlag":1,"device_flag":2,"clientTimestamp":11,"client_timestamp":22,"uid":"alice","token":"token"}`), &connect); err != nil {
		t.Fatalf("Unmarshal(connect): %v", err)
	}
	if connect.ClientKey != "camel-key" || connect.DeviceID != "camel-device" || connect.DeviceFlag != DeviceWeb || connect.ClientTimestamp != 11 {
		t.Fatalf("camel-case connect precedence = %#v", connect)
	}

	var send SendParams
	if err := json.Unmarshal([]byte(`{"msgKey":"camel-key","msg_key":"snake-key","clientMsgNo":"camel-message","client_msg_no":"snake-message","streamNo":"camel-stream","stream_no":"snake-stream","channelId":"camel-channel","channel_id":"snake-channel","channelType":1,"channel_type":2,"payload":null}`), &send); err != nil {
		t.Fatalf("Unmarshal(send): %v", err)
	}
	if send.MsgKey != "camel-key" || send.ClientMsgNo != "camel-message" || send.StreamNo != "camel-stream" || send.ChannelID != "camel-channel" || send.ChannelType != 1 || send.Payload != nil {
		t.Fatalf("camel-case send precedence = %#v", send)
	}
}

func TestEasySDKLegacyOnlyAliasesRemainSupported(t *testing.T) {
	var header Header
	if err := json.Unmarshal([]byte(`{"DUP":true}`), &header); err != nil {
		t.Fatalf("Unmarshal(header): %v", err)
	}
	if !header.Dup {
		t.Fatalf("legacy DUP header = %#v", header)
	}

	var connect ConnectParams
	if err := json.Unmarshal([]byte(`{"client_key":"legacy-key","uid":"alice","token":"token"}`), &connect); err != nil {
		t.Fatalf("Unmarshal(connect): %v", err)
	}
	if connect.ClientKey != "legacy-key" {
		t.Fatalf("legacy connect = %#v", connect)
	}

	var send SendParams
	if err := json.Unmarshal([]byte(`{"msg_key":"legacy-key","stream_no":"legacy-stream","payload":null}`), &send); err != nil {
		t.Fatalf("Unmarshal(send): %v", err)
	}
	if send.MsgKey != "legacy-key" || send.StreamNo != "legacy-stream" {
		t.Fatalf("legacy send = %#v", send)
	}

	if err := json.Unmarshal([]byte(`{"payload":"%%%"}`), &send); err == nil {
		t.Fatal("Unmarshal(send invalid payload) error = nil")
	}
}

func TestEasySDKPayloadVariantsRemainRoundTrippable(t *testing.T) {
	binaryPayload := []byte{0x00, 0x7f, 0xff}
	tests := []struct {
		name    string
		raw     json.RawMessage
		want    []byte
		wantErr bool
	}{
		{name: "missing", raw: nil, want: nil},
		{name: "null", raw: json.RawMessage(`null`), want: nil},
		{name: "android JSON text", raw: json.RawMessage(`"{\"type\":1}"`), want: []byte(`{"type":1}`)},
		{name: "binary base64", raw: json.RawMessage(`"` + base64.StdEncoding.EncodeToString(binaryPayload) + `"`), want: binaryPayload},
		{name: "object", raw: json.RawMessage(` {"type":1} `), want: []byte(`{"type":1}`)},
		{name: "invalid JSON string", raw: json.RawMessage(`"unterminated`), wantErr: true},
		{name: "invalid base64", raw: json.RawMessage(`"%%%"`), wantErr: true},
		{name: "invalid raw JSON", raw: json.RawMessage(`tru`), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeEasySDKPayload(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatal("decodeEasySDKPayload() error = nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeEasySDKPayload() error = %v", err)
			}
			if string(got) != string(tt.want) {
				t.Fatalf("decodeEasySDKPayload() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRecvNotificationPayloadWireShapeMatchesPayloadKind(t *testing.T) {
	tests := []struct {
		name        string
		params      RecvNotificationParams
		wantPayload interface{}
		wantHeader  map[string]interface{}
	}{
		{
			name:        "JSON object stays an object",
			params:      RecvNotificationParams{Header: &Header{End: true}, MessageID: "1", Payload: []byte(`{"type":1}`)},
			wantPayload: map[string]interface{}{"type": float64(1)},
			wantHeader:  map[string]interface{}{"end": true},
		},
		{
			name:        "binary payload becomes base64",
			params:      RecvNotificationParams{MessageID: "2", Payload: []byte{0x00, 0xff}},
			wantPayload: "AP8=",
			wantHeader:  map[string]interface{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wire, err := json.Marshal(tt.params)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			var decoded struct {
				Header  map[string]interface{} `json:"header"`
				Payload interface{}            `json:"payload"`
			}
			if err := json.Unmarshal(wire, &decoded); err != nil {
				t.Fatalf("Unmarshal(wire) error = %v", err)
			}
			if !jsonValuesEqual(decoded.Header, tt.wantHeader) || !jsonValuesEqual(decoded.Payload, tt.wantPayload) {
				t.Fatalf("wire = %s, decoded = %#v", wire, decoded)
			}
		})
	}
}

func jsonValuesEqual(left, right interface{}) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}
