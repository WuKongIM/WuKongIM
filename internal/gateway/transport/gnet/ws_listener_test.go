package gnet

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway/transport"
	"github.com/gorilla/websocket"
)

func TestWSListenerUpgradesDeliversMessagesAndWritesFrames(t *testing.T) {
	handler := newTCPRecordingHandler(func(conn transport.Conn, data []byte) error {
		return conn.Write([]byte("ack:" + string(data)))
	})

	listener := buildWSListener(t, "ws-one", "", handler, nil)
	defer func() { _ = listener.Stop() }()

	if err := listener.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	conn := mustDialWS(t, listener.Addr(), "")
	defer func() { _ = conn.Close() }()

	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatalf("WriteMessage(text): %v", err)
	}

	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage(text): %v", err)
	}
	if messageType != websocket.TextMessage {
		t.Fatalf("reply type = %d, want %d", messageType, websocket.TextMessage)
	}
	if got, want := string(payload), "ack:ping"; got != want {
		t.Fatalf("reply payload = %q, want %q", got, want)
	}

	if err := conn.WriteMessage(websocket.BinaryMessage, []byte{0x01, 0x02, 0x03}); err != nil {
		t.Fatalf("WriteMessage(binary): %v", err)
	}

	messageType, payload, err = conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage(binary): %v", err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("binary reply type = %d, want %d", messageType, websocket.BinaryMessage)
	}
	if got, want := payload, []byte("ack:\x01\x02\x03"); !bytes.Equal(got, want) {
		t.Fatalf("binary reply payload = %v, want %v", got, want)
	}

	waitUntil(t, time.Second, func() bool {
		return handler.OpenCount() == 1 && handler.DataCount() == 2
	})

	if got, want := handler.DataPayload(0), "ping"; got != want {
		t.Fatalf("first payload = %q, want %q", got, want)
	}
	if got, want := handler.DataPayload(1), "\x01\x02\x03"; got != want {
		t.Fatalf("second payload = %q, want %q", got, want)
	}
}

func TestWSHandshakeRejectsInvalidRequestsAndReportsErrors(t *testing.T) {
	errs := newErrorRecorder()
	listener := buildWSListener(t, "ws-one", "", newTCPRecordingHandler(nil), errs.Record)
	defer func() { _ = listener.Stop() }()

	if err := listener.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	tests := []struct {
		name    string
		request string
		status  int
	}{
		{
			name: "path mismatch",
			request: rawUpgradeRequest("/wrong", map[string]string{
				"Host":                  listener.Addr(),
				"Upgrade":               "websocket",
				"Connection":            "Upgrade",
				"Sec-WebSocket-Key":     validWSKey,
				"Sec-WebSocket-Version": "13",
			}),
			status: http.StatusNotFound,
		},
		{
			name: "invalid method",
			request: strings.Replace(rawUpgradeRequest("/", map[string]string{
				"Host":                  listener.Addr(),
				"Upgrade":               "websocket",
				"Connection":            "Upgrade",
				"Sec-WebSocket-Key":     validWSKey,
				"Sec-WebSocket-Version": "13",
			}), "GET / HTTP/1.1", "POST / HTTP/1.1", 1),
			status: http.StatusMethodNotAllowed,
		},
		{
			name: "missing upgrade header",
			request: rawUpgradeRequest("/", map[string]string{
				"Host":                  listener.Addr(),
				"Connection":            "Upgrade",
				"Sec-WebSocket-Key":     validWSKey,
				"Sec-WebSocket-Version": "13",
			}),
			status: http.StatusBadRequest,
		},
		{
			name: "missing connection header",
			request: rawUpgradeRequest("/", map[string]string{
				"Host":                  listener.Addr(),
				"Upgrade":               "websocket",
				"Sec-WebSocket-Key":     validWSKey,
				"Sec-WebSocket-Version": "13",
			}),
			status: http.StatusBadRequest,
		},
		{
			name: "invalid connection header",
			request: rawUpgradeRequest("/", map[string]string{
				"Host":                  listener.Addr(),
				"Upgrade":               "websocket",
				"Connection":            "close",
				"Sec-WebSocket-Key":     validWSKey,
				"Sec-WebSocket-Version": "13",
			}),
			status: http.StatusBadRequest,
		},
		{
			name: "missing key",
			request: rawUpgradeRequest("/", map[string]string{
				"Host":                  listener.Addr(),
				"Upgrade":               "websocket",
				"Connection":            "Upgrade",
				"Sec-WebSocket-Version": "13",
			}),
			status: http.StatusBadRequest,
		},
		{
			name: "invalid key",
			request: rawUpgradeRequest("/", map[string]string{
				"Host":                  listener.Addr(),
				"Upgrade":               "websocket",
				"Connection":            "Upgrade",
				"Sec-WebSocket-Key":     "bad-key",
				"Sec-WebSocket-Version": "13",
			}),
			status: http.StatusBadRequest,
		},
		{
			name: "unsupported version",
			request: rawUpgradeRequest("/", map[string]string{
				"Host":                  listener.Addr(),
				"Upgrade":               "websocket",
				"Connection":            "Upgrade",
				"Sec-WebSocket-Key":     validWSKey,
				"Sec-WebSocket-Version": "12",
			}),
			status: http.StatusUpgradeRequired,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, body, closed := doRawHandshake(t, listener.Addr(), tc.request)
			if status != tc.status {
				t.Fatalf("status = %d, want %d (body=%q)", status, tc.status, body)
			}
			if !closed {
				t.Fatal("connection remained open after handshake rejection")
			}
		})
	}

	waitUntil(t, time.Second, func() bool { return errs.Count() == len(tests) })
}

func TestWSHandshakeFailureReportsErrorBeforeConnectionCloses(t *testing.T) {
	errs := newErrorRecorder()
	listener := buildWSListener(t, "ws-one", "", newTCPRecordingHandler(nil), errs.Record)
	defer func() { _ = listener.Stop() }()

	if err := listener.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	conn, err := net.Dial("tcp", listener.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	closed := make(chan error, 1)
	go func() {
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		_, err := io.Copy(io.Discard, conn)
		closed <- err
	}()

	req := rawUpgradeRequest("/", map[string]string{
		"Host":                  listener.Addr(),
		"Connection":            "Upgrade",
		"Sec-WebSocket-Key":     validWSKey,
		"Sec-WebSocket-Version": "13",
	})
	if _, err := io.WriteString(conn, req); err != nil {
		t.Fatalf("WriteString: %v", err)
	}

	waitUntil(t, time.Second, func() bool { return errs.Count() == 1 })

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("connection did not close after handshake failure")
	}
}

func TestWSWriteProducesWireFrame(t *testing.T) {
	handler := newTCPRecordingHandler(func(conn transport.Conn, data []byte) error {
		return conn.Write([]byte("ack:" + string(data)))
	})

	listener := buildWSListener(t, "ws-one", "", handler, nil)
	defer func() { _ = listener.Stop() }()

	if err := listener.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	rawConn := mustRawWSHandshake(t, listener.Addr(), "")
	defer func() { _ = rawConn.Close() }()

	if _, err := rawConn.Write(mustEncodeWSFrame(testWSFrame{
		final:   true,
		opcode:  testWSOpcodeText,
		masked:  true,
		maskKey: [4]byte{1, 2, 3, 4},
		payload: []byte("ping"),
	})); err != nil {
		t.Fatalf("Write(frame): %v", err)
	}

	frame := mustReadWSFrame(t, rawConn)
	if got, want := frame.opcode, byte(testWSOpcodeText); got != want {
		t.Fatalf("opcode = %d, want %d", got, want)
	}
	if frame.masked {
		t.Fatal("server frame was masked")
	}
	if got, want := string(frame.payload), "ack:ping"; got != want {
		t.Fatalf("payload = %q, want %q", got, want)
	}
}

func TestWSRejectsUnmaskedClientFrames(t *testing.T) {
	errs := newErrorRecorder()
	handler := newTCPRecordingHandler(nil)
	listener := buildWSListener(t, "ws-one", "", handler, errs.Record)
	defer func() { _ = listener.Stop() }()

	if err := listener.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	rawConn := mustRawWSHandshake(t, listener.Addr(), "")
	defer func() { _ = rawConn.Close() }()

	if _, err := rawConn.Write(mustEncodeWSFrame(testWSFrame{
		final:   true,
		opcode:  testWSOpcodeText,
		payload: []byte("oops"),
	})); err != nil {
		t.Fatalf("Write(unmasked): %v", err)
	}

	closeFrame := mustReadWSFrame(t, rawConn)
	if closeFrame.opcode != testWSOpcodeClose {
		t.Fatalf("opcode = %d, want close", closeFrame.opcode)
	}
	if got, want := decodeWSCloseCode(closeFrame.payload), uint16(testWSCloseProtocolError); got != want {
		t.Fatalf("close code = %d, want %d", got, want)
	}

	waitUntil(t, time.Second, func() bool { return handler.CloseCount() == 1 })
	if got := handler.DataCount(); got != 0 {
		t.Fatalf("data count = %d, want 0", got)
	}
	if got := errs.Count(); got != 0 {
		t.Fatalf("listener error count = %d, want 0", got)
	}
}

func TestWSHandlesPingPongAndCloseFrames(t *testing.T) {
	handler := newTCPRecordingHandler(nil)
	listener := buildWSListener(t, "ws-one", "", handler, nil)
	defer func() { _ = listener.Stop() }()

	if err := listener.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	conn := mustDialWS(t, listener.Addr(), "")
	defer func() { _ = conn.Close() }()

	pongCh := make(chan string, 1)
	conn.SetPongHandler(func(appData string) error {
		pongCh <- appData
		return nil
	})

	if err := conn.WriteControl(websocket.PingMessage, []byte("keepalive"), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("WriteControl(ping): %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Fatal("ReadMessage unexpectedly returned application data")
	}
	if websocket.IsUnexpectedCloseError(err) {
		t.Fatalf("ReadMessage returned unexpected close: %v", err)
	}
	select {
	case payload := <-pongCh:
		if payload != "keepalive" {
			t.Fatalf("pong payload = %q, want %q", payload, "keepalive")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pong")
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatalf("clear read deadline: %v", err)
	}

	if err := conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye"), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("WriteControl(close): %v", err)
	}

	waitUntil(t, time.Second, func() bool { return handler.CloseCount() == 1 })
}

func TestWSReassemblesFragmentedMessagesBeforeDelivery(t *testing.T) {
	handler := newTCPRecordingHandler(nil)
	listener := buildWSListener(t, "ws-one", "", handler, nil)
	defer func() { _ = listener.Stop() }()

	if err := listener.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	rawConn := mustRawWSHandshake(t, listener.Addr(), "")
	defer func() { _ = rawConn.Close() }()

	frames := [][]byte{
		mustEncodeWSFrame(testWSFrame{
			final:   false,
			opcode:  testWSOpcodeText,
			masked:  true,
			maskKey: [4]byte{1, 2, 3, 4},
			payload: []byte("hel"),
		}),
		mustEncodeWSFrame(testWSFrame{
			final:   true,
			opcode:  testWSOpcodeContinuation,
			masked:  true,
			maskKey: [4]byte{5, 6, 7, 8},
			payload: []byte("lo"),
		}),
	}
	for _, frame := range frames {
		if _, err := rawConn.Write(frame); err != nil {
			t.Fatalf("Write(fragment): %v", err)
		}
	}

	waitUntil(t, time.Second, func() bool { return handler.DataCount() == 1 })
	if got, want := handler.DataPayload(0), "hello"; got != want {
		t.Fatalf("payload = %q, want %q", got, want)
	}
}

func TestWSListenersShareOneGroupAndRouteByAddress(t *testing.T) {
	firstHandler := newTCPRecordingHandler(nil)
	secondHandler := newTCPRecordingHandler(nil)

	first, second := buildWSListeners(t,
		namedWSListenerSpecWithAddress("ws-a", firstHandler, freeTCPAddress(t), "/a", nil),
		namedWSListenerSpecWithAddress("ws-b", secondHandler, freeTCPAddress(t), "/b", nil),
	)
	defer func() { _ = second.Stop() }()
	defer func() { _ = first.Stop() }()

	if first.group != second.group {
		t.Fatal("expected websocket listeners to share one engine group")
	}

	if err := first.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := second.Start(); err != nil {
		t.Fatalf("second Start: %v", err)
	}

	firstConn := mustDialWS(t, first.Addr(), "/a")
	defer func() { _ = firstConn.Close() }()
	secondConn := mustDialWS(t, second.Addr(), "/b")
	defer func() { _ = secondConn.Close() }()

	if err := firstConn.WriteMessage(websocket.TextMessage, []byte("alpha")); err != nil {
		t.Fatalf("first WriteMessage: %v", err)
	}
	if err := secondConn.WriteMessage(websocket.TextMessage, []byte("beta")); err != nil {
		t.Fatalf("second WriteMessage: %v", err)
	}

	waitUntil(t, time.Second, func() bool {
		return firstHandler.DataCount() == 1 && secondHandler.DataCount() == 1
	})

	if got, want := firstHandler.DataPayload(0), "alpha"; got != want {
		t.Fatalf("first payload = %q, want %q", got, want)
	}
	if got, want := secondHandler.DataPayload(0), "beta"; got != want {
		t.Fatalf("second payload = %q, want %q", got, want)
	}
}

func buildWSListener(t *testing.T, name, path string, handler transport.ConnHandler, onError func(error)) *listenerHandle {
	t.Helper()

	listeners, err := NewFactory().Build([]transport.ListenerSpec{
		namedWSListenerSpecWithAddress(name, handler, freeTCPAddress(t), path, onError),
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return requireListenerHandle(t, listeners[0])
}

func buildWSListeners(t *testing.T, specs ...transport.ListenerSpec) (*listenerHandle, *listenerHandle) {
	t.Helper()

	listeners, err := NewFactory().Build(specs)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return requireListenerHandle(t, listeners[0]), requireListenerHandle(t, listeners[1])
}

func namedWSListenerSpecWithAddress(name string, handler transport.ConnHandler, address, path string, onError func(error)) transport.ListenerSpec {
	return transport.ListenerSpec{
		Options: transport.ListenerOptions{
			Name:    name,
			Network: "websocket",
			Address: address,
			Path:    path,
			OnError: onError,
		},
		Handler: handler,
	}
}

func mustDialWS(t *testing.T, addr, path string) *websocket.Conn {
	t.Helper()

	url := fmt.Sprintf("ws://%s%s", addr, path)
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial(%q): %v", url, err)
	}
	return conn
}

func mustRawWSHandshake(t *testing.T, addr, path string) net.Conn {
	t.Helper()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial(%q): %v", addr, err)
	}

	req := rawUpgradeRequest(path, map[string]string{
		"Host":                  addr,
		"Upgrade":               "websocket",
		"Connection":            "Upgrade",
		"Sec-WebSocket-Key":     validWSKey,
		"Sec-WebSocket-Version": "13",
	})
	if _, err := io.WriteString(conn, req); err != nil {
		_ = conn.Close()
		t.Fatalf("WriteString(handshake): %v", err)
	}

	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		_ = conn.Close()
		t.Fatalf("ReadResponse: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		_ = conn.Close()
		t.Fatalf("status = %d, want 101 (body=%q)", resp.StatusCode, body)
	}
	_ = resp.Body.Close()
	return &prefixedConn{Conn: conn, reader: reader}
}

func doRawHandshake(t *testing.T, addr, request string) (int, string, bool) {
	t.Helper()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial(%q): %v", addr, err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatalf("WriteString: %v", err)
	}

	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if err := conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 1)
	_, err = reader.Read(buf)
	closed := err != nil
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		closed = false
	}
	_ = conn.SetReadDeadline(time.Time{})

	return resp.StatusCode, string(body), closed
}

const validWSKey = "dGhlIHNhbXBsZSBub25jZQ=="

func rawUpgradeRequest(path string, headers map[string]string) string {
	if path == "" {
		path = "/"
	}
	var b strings.Builder
	b.WriteString("GET ")
	b.WriteString(path)
	b.WriteString(" HTTP/1.1\r\n")
	for _, key := range []string{
		"Host",
		"Upgrade",
		"Connection",
		"Sec-WebSocket-Key",
		"Sec-WebSocket-Version",
	} {
		if value, ok := headers[key]; ok {
			b.WriteString(key)
			b.WriteString(": ")
			b.WriteString(value)
			b.WriteString("\r\n")
		}
	}
	b.WriteString("\r\n")
	return b.String()
}

type errorRecorder struct {
	mu   sync.Mutex
	errs []error
}

func newErrorRecorder() *errorRecorder {
	return &errorRecorder{}
}

func (r *errorRecorder) Record(err error) {
	r.mu.Lock()
	r.errs = append(r.errs, err)
	r.mu.Unlock()
}

func (r *errorRecorder) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.errs)
}

type prefixedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *prefixedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func decodeWSCloseCode(payload []byte) uint16 {
	if len(payload) < 2 {
		return 0
	}
	return uint16(payload[0])<<8 | uint16(payload[1])
}

const (
	testWSOpcodeContinuation = 0x0
	testWSOpcodeText         = 0x1
	testWSOpcodeClose        = 0x8
	testWSCloseProtocolError = 1002
)

type testWSFrame struct {
	final   bool
	opcode  byte
	masked  bool
	maskKey [4]byte
	payload []byte
}

func mustEncodeWSFrame(frame testWSFrame) []byte {
	var header bytes.Buffer
	first := frame.opcode
	if frame.final {
		first |= 0x80
	}
	header.WriteByte(first)

	payloadLen := len(frame.payload)
	second := byte(0)
	if frame.masked {
		second |= 0x80
	}
	switch {
	case payloadLen < 126:
		header.WriteByte(second | byte(payloadLen))
	case payloadLen <= 0xffff:
		header.WriteByte(second | 126)
		_ = binary.Write(&header, binary.BigEndian, uint16(payloadLen))
	default:
		header.WriteByte(second | 127)
		_ = binary.Write(&header, binary.BigEndian, uint64(payloadLen))
	}

	payload := append([]byte(nil), frame.payload...)
	if frame.masked {
		header.Write(frame.maskKey[:])
		for i := range payload {
			payload[i] ^= frame.maskKey[i%4]
		}
	}
	header.Write(payload)
	return header.Bytes()
}

func mustReadWSFrame(t *testing.T, conn net.Conn) testWSFrame {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()

	var head [2]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		t.Fatalf("ReadFull(header): %v", err)
	}

	frame := testWSFrame{
		final:  head[0]&0x80 != 0,
		opcode: head[0] & 0x0f,
		masked: head[1]&0x80 != 0,
	}

	payloadLen := int(head[1] & 0x7f)
	switch payloadLen {
	case 126:
		var ext uint16
		if err := binary.Read(conn, binary.BigEndian, &ext); err != nil {
			t.Fatalf("Read(binary16): %v", err)
		}
		payloadLen = int(ext)
	case 127:
		var ext uint64
		if err := binary.Read(conn, binary.BigEndian, &ext); err != nil {
			t.Fatalf("Read(binary64): %v", err)
		}
		payloadLen = int(ext)
	}

	if frame.masked {
		if _, err := io.ReadFull(conn, frame.maskKey[:]); err != nil {
			t.Fatalf("ReadFull(mask): %v", err)
		}
	}
	frame.payload = make([]byte, payloadLen)
	if _, err := io.ReadFull(conn, frame.payload); err != nil {
		t.Fatalf("ReadFull(payload): %v", err)
	}
	if frame.masked {
		for i := range frame.payload {
			frame.payload[i] ^= frame.maskKey[i%4]
		}
	}

	return frame
}
