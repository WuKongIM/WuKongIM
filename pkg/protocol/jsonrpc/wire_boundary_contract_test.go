package jsonrpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"testing"
)

type jsonRPCFailingReader struct {
	err error
}

func (r jsonRPCFailingReader) Read([]byte) (int, error) {
	return 0, r.err
}

func TestDetermineMessageTypePreservesJSONRPCClassification(t *testing.T) {
	tests := []struct {
		name        string
		probe       Probe
		wantType    int
		wantVersion string
		wantErr     error
	}{
		{
			name:        "omitted version defaults to 2.0 request",
			probe:       Probe{ID: json.RawMessage(`"request-1"`), Method: MethodPing},
			wantType:    msgTypeRequest,
			wantVersion: jsonRPCVersion,
		},
		{
			name:        "result is a response",
			probe:       Probe{Jsonrpc: json.RawMessage(`"2.0"`), ID: json.RawMessage(`"request-2"`), Result: json.RawMessage(`null`)},
			wantType:    msgTypeResponse,
			wantVersion: jsonRPCVersion,
		},
		{
			name:        "known method without id is a notification",
			probe:       Probe{Method: MethodRecv, Params: json.RawMessage(`{}`)},
			wantType:    msgTypeNotification,
			wantVersion: jsonRPCVersion,
		},
		{
			name:        "response cannot contain result and error",
			probe:       Probe{ID: json.RawMessage(`"request-3"`), Result: json.RawMessage(`null`), Error: json.RawMessage(`{"code":1,"message":"failed"}`)},
			wantType:    msgTypeUnknown,
			wantVersion: jsonRPCVersion,
			wantErr:     ErrResponseFormat,
		},
		{
			name:        "unknown notification method is classified but rejected",
			probe:       Probe{Method: "future-method", Params: json.RawMessage(`{}`)},
			wantType:    msgTypeNotification,
			wantVersion: jsonRPCVersion,
		},
		{
			name:        "orphan id is structurally invalid",
			probe:       Probe{ID: json.RawMessage(`"request-4"`)},
			wantType:    msgTypeUnknown,
			wantVersion: jsonRPCVersion,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotVersion, err := determineMessageType(&tt.probe)
			if gotType != tt.wantType || gotVersion != tt.wantVersion {
				t.Fatalf("determineMessageType() = (%d, %q), want (%d, %q)", gotType, gotVersion, tt.wantType, tt.wantVersion)
			}
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("determineMessageType() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if tt.name == "orphan id is structurally invalid" {
				if err == nil {
					t.Fatal("determineMessageType() error = nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("determineMessageType() error = %v", err)
			}
		})
	}
}

func TestDecodePreservesReaderErrorsAndRequestBoundaries(t *testing.T) {
	readErr := errors.New("upstream frame read failed")
	message, probe, err := Decode(json.NewDecoder(jsonRPCFailingReader{err: readErr}))
	if !errors.Is(err, readErr) || message != nil || probe.Method != "" {
		t.Fatalf("Decode(reader error) = (%#v, %#v, %v)", message, probe, err)
	}

	tests := []struct {
		name    string
		wire    string
		wantErr error
		check   func(*testing.T, interface{})
	}{
		{name: "connect malformed params", wire: `{"jsonrpc":"2.0","id":"1","method":"connect","params":[]}`, wantErr: ErrUnmarshalFieldFailed},
		{name: "send missing params", wire: `{"jsonrpc":"2.0","id":"1","method":"send"}`, wantErr: ErrMissingParams},
		{name: "subscribe malformed params", wire: `{"jsonrpc":"2.0","id":"1","method":"subscribe","params":[]}`, wantErr: ErrUnmarshalFieldFailed},
		{name: "unsubscribe missing params", wire: `{"jsonrpc":"2.0","id":"1","method":"unsubscribe"}`, wantErr: ErrMissingParams},
		{
			name: "ping empty params are retained", wire: `{"jsonrpc":"2.0","id":"1","method":"ping","params":{}}`,
			check: func(t *testing.T, value interface{}) {
				request, ok := value.(PingRequest)
				if !ok || request.Params == nil {
					t.Fatalf("decoded ping = %#v", value)
				}
			},
		},
		{
			name: "disconnect request", wire: `{"jsonrpc":"2.0","id":"disconnect-1","method":"disconnect","params":{"reasonCode":2,"reason":"replaced"}}`,
			check: func(t *testing.T, value interface{}) {
				request, ok := value.(DisconnectRequest)
				if !ok || request.ID != "disconnect-1" || request.Params.ReasonCode != 2 || request.Params.Reason != "replaced" {
					t.Fatalf("decoded disconnect request = %#v", value)
				}
			},
		},
		{name: "disconnect malformed params", wire: `{"jsonrpc":"2.0","id":"1","method":"disconnect","params":[]}`, wantErr: ErrUnmarshalFieldFailed},
		{name: "recv malformed params", wire: `{"jsonrpc":"2.0","method":"recv","params":[]}`, wantErr: ErrUnmarshalFieldFailed},
		{name: "recvack missing params", wire: `{"jsonrpc":"2.0","method":"recvack"}`, wantErr: ErrMissingParams},
		{name: "disconnect notification malformed params", wire: `{"jsonrpc":"2.0","method":"disconnect","params":[]}`, wantErr: ErrUnmarshalFieldFailed},
		{
			name: "disconnect notification", wire: `{"jsonrpc":"2.0","method":"disconnect","params":{"reasonCode":2,"reason":"replaced"}}`,
			check: func(t *testing.T, value interface{}) {
				notification, ok := value.(DisconnectNotification)
				if !ok || notification.Params.ReasonCode != 2 || notification.Params.Reason != "replaced" {
					t.Fatalf("decoded disconnect notification = %#v", value)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, _, err := Decode(json.NewDecoder(bytes.NewBufferString(tt.wire)))
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Decode() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			tt.check(t, value)
		})
	}

	if _, _, err := Decode(json.NewDecoder(bytes.NewReader(nil))); !errors.Is(err, io.EOF) {
		t.Fatalf("Decode(empty) error = %v, want EOF", err)
	}
	if _, _, err := Decode(json.NewDecoder(bytes.NewBufferString(`{"jsonrpc":?}`))); !errors.Is(err, ErrInvalidStructure) {
		t.Fatalf("Decode(syntax error) = %v, want ErrInvalidStructure", err)
	}
	if _, _, err := Decode(json.NewDecoder(bytes.NewBufferString(`{"jsonrpc":"2.0","id":null,"result":null}`))); !errors.Is(err, ErrResponseFormat) {
		t.Fatalf("Decode(null response id) = %v, want ErrResponseFormat", err)
	}
}

func TestDecodeUnknownNotificationUsesStableErrorClassification(t *testing.T) {
	_, _, err := Decode(json.NewDecoder(bytes.NewBufferString(`{"jsonrpc":"2.0","method":"future-method","params":{}}`)))
	if !errors.Is(err, ErrUnknownMethod) {
		t.Fatalf("Decode(unknown notification) error = %v, want ErrUnknownMethod", err)
	}
}
