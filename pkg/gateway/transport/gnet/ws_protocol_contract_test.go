package gnet

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
)

const testWebSocketKey = "dGhlIHNhbXBsZSBub25jZQ=="

func TestParseWSHandshakeAcceptsRFCUpgradeAndPreservesPipelinedBytes(t *testing.T) {
	req := websocketUpgradeRequest(http.MethodGet, "/ws", map[string]string{
		"Connection":            "keep-alive, UpGrAdE",
		"Upgrade":               "WebSocket",
		"Sec-WebSocket-Key":     testWebSocketKey,
		"Sec-WebSocket-Version": "13",
	})
	pipelined := []byte{0x81, 0x00}
	input := append(append([]byte(nil), req...), pipelined...)

	result, failure, complete := parseWSHandshake(input, "/ws")
	if !complete || failure != nil || result == nil {
		t.Fatalf("parseWSHandshake() = (%+v, %+v, %v), want successful completion", result, failure, complete)
	}
	if got, want := result.consumed, len(req); got != want {
		t.Fatalf("consumed = %d, want %d", got, want)
	}
	resp := readTestHTTPResponse(t, result.response)
	if got, want := resp.StatusCode, http.StatusSwitchingProtocols; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := resp.Header.Get("Sec-WebSocket-Accept"), "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="; got != want {
		t.Fatalf("Sec-WebSocket-Accept = %q, want %q", got, want)
	}
	if got := resp.Header.Get("Connection"); !strings.EqualFold(got, "Upgrade") {
		t.Fatalf("Connection = %q, want Upgrade", got)
	}

	state := &connState{
		runtime: &listenerRuntime{opts: transport.ListenerOptions{
			Network: "websocket",
			Path:    "/ws",
		}},
		mode:      connModeWSHandshake,
		wsInbound: input,
	}
	stateResult, stateFailure, stateComplete := state.consumeWSHandshake()
	if !stateComplete || stateFailure != nil || stateResult == nil {
		t.Fatalf("consumeWSHandshake() = (%+v, %+v, %v), want success", stateResult, stateFailure, stateComplete)
	}
	if got := state.currentMode(); got != connModeWSFrames {
		t.Fatalf("mode = %v, want websocket frames", got)
	}
	if !bytes.Equal(state.wsInbound, pipelined) {
		t.Fatalf("pipelined bytes = %v, want %v", state.wsInbound, pipelined)
	}
}

func TestParseWSHandshakeWaitsForCompleteHeaderAndDefaultsRootPath(t *testing.T) {
	if result, failure, complete := parseWSHandshake([]byte("GET / HTTP/1.1\r\n"), ""); complete || result != nil || failure != nil {
		t.Fatalf("incomplete handshake = (%+v, %+v, %v), want pending", result, failure, complete)
	}

	req := websocketUpgradeRequest(http.MethodGet, "/", map[string]string{
		"Connection":            "upgrade",
		"Upgrade":               "websocket",
		"Sec-WebSocket-Key":     testWebSocketKey,
		"Sec-WebSocket-Version": "13",
	})
	result, failure, complete := parseWSHandshake(req, "")
	if !complete || result == nil || failure != nil {
		t.Fatalf("root handshake = (%+v, %+v, %v), want success", result, failure, complete)
	}
}

func TestParseWSHandshakeRejectsInvalidUpgradeBoundaries(t *testing.T) {
	valid := map[string]string{
		"Connection":            "upgrade",
		"Upgrade":               "websocket",
		"Sec-WebSocket-Key":     testWebSocketKey,
		"Sec-WebSocket-Version": "13",
	}
	tests := []struct {
		name       string
		request    []byte
		path       string
		wantStatus int
		wantHeader string
	}{
		{name: "malformed request", request: []byte("not-http\r\n\r\n"), path: "/ws", wantStatus: http.StatusBadRequest},
		{name: "method", request: websocketUpgradeRequest(http.MethodPost, "/ws", cloneHeaders(valid)), path: "/ws", wantStatus: http.StatusMethodNotAllowed, wantHeader: "Allow"},
		{name: "path", request: websocketUpgradeRequest(http.MethodGet, "/other", cloneHeaders(valid)), path: "/ws", wantStatus: http.StatusNotFound},
		{name: "connection token", request: websocketUpgradeRequest(http.MethodGet, "/ws", withoutHeader(valid, "Connection")), path: "/ws", wantStatus: http.StatusBadRequest},
		{name: "upgrade", request: websocketUpgradeRequest(http.MethodGet, "/ws", withHeader(valid, "Upgrade", "h2c")), path: "/ws", wantStatus: http.StatusBadRequest},
		{name: "empty key", request: websocketUpgradeRequest(http.MethodGet, "/ws", withHeader(valid, "Sec-WebSocket-Key", "")), path: "/ws", wantStatus: http.StatusBadRequest},
		{name: "malformed key", request: websocketUpgradeRequest(http.MethodGet, "/ws", withHeader(valid, "Sec-WebSocket-Key", "%%%")), path: "/ws", wantStatus: http.StatusBadRequest},
		{name: "wrong key length", request: websocketUpgradeRequest(http.MethodGet, "/ws", withHeader(valid, "Sec-WebSocket-Key", "YQ==")), path: "/ws", wantStatus: http.StatusBadRequest},
		{name: "version", request: websocketUpgradeRequest(http.MethodGet, "/ws", withHeader(valid, "Sec-WebSocket-Version", "12")), path: "/ws", wantStatus: http.StatusUpgradeRequired, wantHeader: "Sec-WebSocket-Version"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, failure, complete := parseWSHandshake(tt.request, tt.path)
			if !complete || result != nil || failure == nil || failure.err == nil {
				t.Fatalf("parseWSHandshake() = (%+v, %+v, %v), want failure", result, failure, complete)
			}
			resp := readTestHTTPResponse(t, failure.response)
			if got := resp.StatusCode; got != tt.wantStatus {
				t.Fatalf("status = %d, want %d", got, tt.wantStatus)
			}
			if tt.wantHeader != "" && resp.Header.Get(tt.wantHeader) == "" {
				t.Fatalf("response missing %s header", tt.wantHeader)
			}
			if !resp.Close {
				t.Fatal("failure response did not require connection close")
			}
		})
	}

	tooLarge := bytes.Repeat([]byte{'x'}, wsMaxHeaderSize+1)
	result, failure, complete := parseWSHandshake(tooLarge, "/ws")
	if !complete || result != nil || failure == nil {
		t.Fatalf("oversized handshake = (%+v, %+v, %v), want failure", result, failure, complete)
	}
	if got := readTestHTTPResponse(t, failure.response).StatusCode; got != http.StatusRequestHeaderFieldsTooLarge {
		t.Fatalf("oversized status = %d, want %d", got, http.StatusRequestHeaderFieldsTooLarge)
	}

	state := &connState{
		runtime: &listenerRuntime{opts: transport.ListenerOptions{
			Network: "websocket",
			Path:    "/ws",
		}},
		mode:      connModeWSHandshake,
		wsInbound: append([]byte(nil), tests[0].request...),
	}
	if result, failure, complete := state.consumeWSHandshake(); !complete || result != nil || failure == nil {
		t.Fatalf("state failure = (%+v, %+v, %v), want completed failure", result, failure, complete)
	}
	if len(state.wsInbound) != 0 {
		t.Fatalf("failed handshake retained %d inbound bytes", len(state.wsInbound))
	}
}

func TestBuildHTTPResponseHonorsExplicitConnectionHeader(t *testing.T) {
	response := buildHTTPResponse(599, map[string]string{"cOnNeCtIoN": "keep-alive"}, []byte("diagnostic"))
	resp := readTestHTTPResponse(t, response)
	defer resp.Body.Close()
	if got, want := resp.Status, "599 Unknown Status"; got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
	if got, want := resp.Header.Get("Connection"), "keep-alive"; got != want {
		t.Fatalf("Connection = %q, want %q", got, want)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(body): %v", err)
	}
	if got, want := string(body), "diagnostic"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestWSFrameDecoderRejectsMalformedAndOversizedFrames(t *testing.T) {
	tests := []struct {
		name      string
		frame     []byte
		limit     int
		wantError error
		wantCode  uint16
	}{
		{name: "short", frame: []byte{0x81}, wantError: errWSNeedMoreData},
		{name: "reserved bit", frame: []byte{0xc1, 0x00}, wantCode: wsCloseProtocolError},
		{name: "short sixteen bit length", frame: []byte{0x82, 126, 0}, wantError: errWSNeedMoreData},
		{name: "short sixty four bit length", frame: []byte{0x82, 127, 0, 0, 0}, wantError: errWSNeedMoreData},
		{name: "integer overflow", frame: []byte{0x82, 127, 0x80, 0, 0, 0, 0, 0, 0, 0}, wantCode: wsCloseProtocolError},
		{name: "configured limit", frame: []byte{0x82, 5, '1', '2', '3', '4', '5'}, limit: 4, wantError: ErrPendingBytesExceeded},
		{name: "short mask", frame: []byte{0x82, 0x80 | 1, 1, 2}, wantError: errWSNeedMoreData},
		{name: "short payload", frame: []byte{0x82, 3, '1'}, wantError: errWSNeedMoreData},
		{name: "fragmented control", frame: []byte{wsOpcodePing, 0}, wantCode: wsCloseProtocolError},
		{name: "large control", frame: append([]byte{0x80 | wsOpcodePing, 126, 0, 126}, make([]byte, 126)...), wantCode: wsCloseProtocolError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, consumed, err := decodeWSFrameWithLimit(append([]byte(nil), tt.frame...), tt.limit)
			if err == nil {
				t.Fatal("decodeWSFrameWithLimit() error = nil")
			}
			if consumed != 0 {
				t.Fatalf("consumed = %d, want 0", consumed)
			}
			if tt.wantError != nil && !errors.Is(err, tt.wantError) {
				t.Fatalf("error = %v, want %v", err, tt.wantError)
			}
			if tt.wantCode != 0 && wsCloseCodeForErr(err) != tt.wantCode {
				t.Fatalf("close code = %d, want %d", wsCloseCodeForErr(err), tt.wantCode)
			}
		})
	}
}

func TestWSFrameCodecPreservesPayloadBoundariesAndMasking(t *testing.T) {
	payloads := [][]byte{
		bytes.Repeat([]byte{'a'}, 125),
		bytes.Repeat([]byte{'b'}, 126),
		bytes.Repeat([]byte{'c'}, 1<<16),
	}
	for _, payload := range payloads {
		encoded, err := encodeWSFrame(wsFrame{final: true, opcode: wsOpcodeBinary, payload: payload})
		if err != nil {
			t.Fatalf("encodeWSFrame(%d): %v", len(payload), err)
		}
		decoded, consumed, err := decodeWSFrame(encoded)
		if err != nil {
			t.Fatalf("decodeWSFrame(%d): %v", len(payload), err)
		}
		if consumed != len(encoded) || !decoded.final || decoded.opcode != wsOpcodeBinary || !bytes.Equal(decoded.payload, payload) {
			t.Fatalf("round trip length %d = %+v consumed=%d", len(payload), decoded, consumed)
		}
	}

	masked := encodeMaskedTestWSFrame(t, true, wsOpcodeText, [4]byte{1, 2, 3, 4}, []byte("masked"))
	decoded, _, err := decodeWSFrame(masked)
	if err != nil {
		t.Fatalf("decode masked frame: %v", err)
	}
	if !decoded.masked || !bytes.Equal(decoded.payload, []byte("masked")) {
		t.Fatalf("masked frame = %+v, want decoded payload", decoded)
	}
}

func TestWSFrameBuilderAndCloseValidation(t *testing.T) {
	if err := buildWSWritevFrame(wsFrame{}, nil); err == nil {
		t.Fatal("buildWSWritevFrame(nil) error = nil")
	}
	if _, err := encodeWSFrame(wsFrame{final: true, opcode: wsOpcodeText, masked: true}); err == nil {
		t.Fatal("encodeWSFrame(masked server frame) error = nil")
	}
	if _, err := encodeWSFrame(wsFrame{final: true, opcode: wsOpcodePing, payload: make([]byte, 126)}); err == nil {
		t.Fatal("encodeWSFrame(oversized control frame) error = nil")
	}
	writev := &wsWritevFrame{}
	payload := []byte("payload")
	if err := buildWSWritevFrame(wsFrame{final: true, opcode: wsOpcodeBinary, payload: payload}, writev); err != nil {
		t.Fatalf("buildWSWritevFrame(): %v", err)
	}
	decoded, _, err := decodeWSFrame(bytes.Join(writev.bufs[:], nil))
	if err != nil || !bytes.Equal(decoded.payload, payload) {
		t.Fatalf("writev frame decode = %+v, %v", decoded, err)
	}

	closeFrame := buildWSCloseFrame(wsCloseNormalClosure, "done")
	decoded, _, err = decodeWSFrame(closeFrame)
	if err != nil {
		t.Fatalf("decode close frame: %v", err)
	}
	if got := binary.BigEndian.Uint16(decoded.payload[:2]); got != wsCloseNormalClosure {
		t.Fatalf("close code = %d, want %d", got, wsCloseNormalClosure)
	}
	if got, want := string(decoded.payload[2:]), "done"; got != want {
		t.Fatalf("close reason = %q, want %q", got, want)
	}

	if err := validWSClosePayload(nil); err != nil {
		t.Fatalf("validWSClosePayload(nil): %v", err)
	}
	if err := validWSClosePayload([]byte{1}); wsCloseCodeForErr(err) != wsCloseProtocolError {
		t.Fatalf("one-byte close error = %v, code=%d", err, wsCloseCodeForErr(err))
	}
	invalidUTF8 := []byte{byte(wsCloseNormalClosure >> 8), byte(wsCloseNormalClosure & 0xff), 0xff}
	if err := validWSClosePayload(invalidUTF8); wsCloseCodeForErr(err) != wsCloseInvalidData {
		t.Fatalf("invalid UTF-8 close error = %v, code=%d", err, wsCloseCodeForErr(err))
	}
	valid := []byte{byte(wsCloseNormalClosure >> 8), byte(wsCloseNormalClosure & 0xff), 'o', 'k'}
	if err := validWSClosePayload(valid); err != nil {
		t.Fatalf("validWSClosePayload(valid): %v", err)
	}
}

func TestWSProtocolErrorMappingIsNilSafe(t *testing.T) {
	var nilProtocolErr *wsProtocolError
	if got := nilProtocolErr.Error(); got != "" {
		t.Fatalf("nil Error() = %q, want empty", got)
	}
	empty := &wsProtocolError{}
	if got := empty.Error(); got != "" {
		t.Fatalf("empty Error() = %q, want empty", got)
	}
	err := newWSProtocolError(wsCloseInvalidData, "invalid text")
	if got := wsCloseCodeForErr(err); got != wsCloseInvalidData {
		t.Fatalf("mapped close code = %d, want %d", got, wsCloseInvalidData)
	}
	if got := wsCloseCodeForErr(errors.New("plain")); got != wsCloseProtocolError {
		t.Fatalf("default close code = %d, want %d", got, wsCloseProtocolError)
	}
}

func websocketUpgradeRequest(method, path string, headers map[string]string) []byte {
	var b strings.Builder
	b.WriteString(method)
	b.WriteByte(' ')
	b.WriteString(path)
	b.WriteString(" HTTP/1.1\r\nHost: example.test\r\n")
	for key, value := range headers {
		b.WriteString(key)
		b.WriteString(": ")
		b.WriteString(value)
		b.WriteString("\r\n")
	}
	b.WriteString("\r\n")
	return []byte(b.String())
}

func readTestHTTPResponse(t *testing.T, raw []byte) *http.Response {
	t.Helper()
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(raw)), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("ReadResponse(): %v\n%s", err, raw)
	}
	return resp
}

func cloneHeaders(headers map[string]string) map[string]string {
	cloned := make(map[string]string, len(headers))
	for key, value := range headers {
		cloned[key] = value
	}
	return cloned
}

func withHeader(headers map[string]string, key, value string) map[string]string {
	cloned := cloneHeaders(headers)
	cloned[key] = value
	return cloned
}

func withoutHeader(headers map[string]string, key string) map[string]string {
	cloned := cloneHeaders(headers)
	delete(cloned, key)
	return cloned
}
