package stdnet_test

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway/transport"
	"github.com/WuKongIM/WuKongIM/internal/gateway/transport/stdnet"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/gorilla/websocket"
)

func TestTCPListenerLogsDebugOnConnectSuccess(t *testing.T) {
	logger := newRecordingLogger()
	handler := newConnRecordingHandler()
	listener, err := stdnet.NewTCPListener(transport.ListenerOptions{
		Name:    "tcp-wkproto",
		Network: "tcp",
		Address: "127.0.0.1:0",
		Logger:  logger,
	}, handler)
	if err != nil {
		t.Fatalf("NewTCPListener: %v", err)
	}
	defer func() { _ = listener.Stop() }()

	if err := listener.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	conn, err := net.Dial("tcp", listener.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_ = conn.Close()

	waitUntil(t, time.Second, func() bool { return handler.OpenCount() == 1 })
	entry := waitForLogEntry(t, logger, "gateway.transport.conn.connected")
	if entry.level != "DEBUG" {
		t.Fatalf("level = %q, want DEBUG", entry.level)
	}
	if got, want := entry.stringField("listener"), "tcp-wkproto"; got != want {
		t.Fatalf("listener = %q, want %q", got, want)
	}
	if got, want := entry.stringField("network"), "tcp"; got != want {
		t.Fatalf("network = %q, want %q", got, want)
	}
	if entry.uint64Field("connID") == 0 {
		t.Fatal("connID = 0, want non-zero")
	}
	if got, want := entry.stringField("localAddr"), listener.Addr(); got != want {
		t.Fatalf("localAddr = %q, want %q", got, want)
	}
	if got := entry.stringField("remoteAddr"); got == "" {
		t.Fatal("remoteAddr is empty")
	}
}

func TestTCPListenerLogsDebugOnConnectFailure(t *testing.T) {
	logger := newRecordingLogger()
	wantErr := errors.New("reject open")
	handler := &openFailHandler{err: wantErr}
	listener, err := stdnet.NewTCPListener(transport.ListenerOptions{
		Name:    "tcp-wkproto",
		Network: "tcp",
		Address: "127.0.0.1:0",
		Logger:  logger,
	}, handler)
	if err != nil {
		t.Fatalf("NewTCPListener: %v", err)
	}
	defer func() { _ = listener.Stop() }()

	if err := listener.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	conn, err := net.Dial("tcp", listener.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_ = conn.Close()

	entry := waitForLogEntry(t, logger, "gateway.transport.conn.connect_failed")
	if entry.level != "DEBUG" {
		t.Fatalf("level = %q, want DEBUG", entry.level)
	}
	if got, want := entry.stringField("listener"), "tcp-wkproto"; got != want {
		t.Fatalf("listener = %q, want %q", got, want)
	}
	if got, want := entry.stringField("network"), "tcp"; got != want {
		t.Fatalf("network = %q, want %q", got, want)
	}
	if got := entry.errorField("error"); !errors.Is(got, wantErr) {
		t.Fatalf("error = %v, want %v", got, wantErr)
	}
}

func TestWSListenerLogsDebugOnConnectSuccess(t *testing.T) {
	logger := newRecordingLogger()
	handler := newConnRecordingHandler()
	listener, err := stdnet.NewWSListener(transport.ListenerOptions{
		Name:    "ws-jsonrpc",
		Network: "websocket",
		Address: "127.0.0.1:0",
		Logger:  logger,
	}, handler)
	if err != nil {
		t.Fatalf("NewWSListener: %v", err)
	}
	defer func() { _ = listener.Stop() }()

	if err := listener.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	url := "ws://" + strings.TrimPrefix(listener.Addr(), "http://")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_ = conn.Close()

	waitUntil(t, time.Second, func() bool { return handler.OpenCount() == 1 })
	entry := waitForLogEntry(t, logger, "gateway.transport.conn.connected")
	if got, want := entry.stringField("listener"), "ws-jsonrpc"; got != want {
		t.Fatalf("listener = %q, want %q", got, want)
	}
	if got, want := entry.stringField("network"), "websocket"; got != want {
		t.Fatalf("network = %q, want %q", got, want)
	}
}

func TestWSListenerLogsDebugOnHandshakeFailure(t *testing.T) {
	logger := newRecordingLogger()
	handler := newConnRecordingHandler()
	listener, err := stdnet.NewWSListener(transport.ListenerOptions{
		Name:    "ws-jsonrpc",
		Network: "websocket",
		Address: "127.0.0.1:0",
		Logger:  logger,
	}, handler)
	if err != nil {
		t.Fatalf("NewWSListener: %v", err)
	}
	defer func() { _ = listener.Stop() }()

	if err := listener.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	resp, err := http.Get("http://" + strings.TrimPrefix(listener.Addr(), "http://"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_ = resp.Body.Close()
	if got, want := resp.StatusCode, http.StatusBadRequest; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}

	entry := waitForLogEntry(t, logger, "gateway.transport.conn.connect_failed")
	if got, want := entry.stringField("listener"), "ws-jsonrpc"; got != want {
		t.Fatalf("listener = %q, want %q", got, want)
	}
	if got, want := entry.stringField("network"), "websocket"; got != want {
		t.Fatalf("network = %q, want %q", got, want)
	}
	if got := entry.errorField("error"); got == nil {
		t.Fatal("error = nil, want non-nil")
	}
}

type openFailHandler struct {
	err error
}

func (h *openFailHandler) OnOpen(transport.Conn) error         { return h.err }
func (h *openFailHandler) OnData(transport.Conn, []byte) error { return nil }
func (h *openFailHandler) OnClose(transport.Conn, error)       {}

type recordedLogEntry struct {
	level  string
	msg    string
	fields []wklog.Field
}

func (e recordedLogEntry) stringField(key string) string {
	for _, field := range e.fields {
		if field.Key == key {
			if value, ok := field.Value.(string); ok {
				return value
			}
		}
	}
	return ""
}

func (e recordedLogEntry) uint64Field(key string) uint64 {
	for _, field := range e.fields {
		if field.Key == key {
			if value, ok := field.Value.(uint64); ok {
				return value
			}
		}
	}
	return 0
}

func (e recordedLogEntry) errorField(key string) error {
	for _, field := range e.fields {
		if field.Key == key {
			if value, ok := field.Value.(error); ok {
				return value
			}
		}
	}
	return nil
}

type recordingLogger struct {
	mu      sync.Mutex
	entries []recordedLogEntry
}

func newRecordingLogger() *recordingLogger { return &recordingLogger{} }

func (r *recordingLogger) Debug(msg string, fields ...wklog.Field) { r.log("DEBUG", msg, fields...) }
func (r *recordingLogger) Info(msg string, fields ...wklog.Field)  { r.log("INFO", msg, fields...) }
func (r *recordingLogger) Warn(msg string, fields ...wklog.Field)  { r.log("WARN", msg, fields...) }
func (r *recordingLogger) Error(msg string, fields ...wklog.Field) { r.log("ERROR", msg, fields...) }
func (r *recordingLogger) Fatal(msg string, fields ...wklog.Field) { r.log("FATAL", msg, fields...) }
func (r *recordingLogger) Named(string) wklog.Logger               { return r }
func (r *recordingLogger) With(...wklog.Field) wklog.Logger        { return r }
func (r *recordingLogger) Sync() error                             { return nil }

func (r *recordingLogger) log(level, msg string, fields ...wklog.Field) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, recordedLogEntry{
		level:  level,
		msg:    msg,
		fields: append([]wklog.Field(nil), fields...),
	})
}

func (r *recordingLogger) findByEvent(event string) (recordedLogEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entry := range r.entries {
		for _, field := range entry.fields {
			if field.Key == "event" && field.Value == event {
				return entry, true
			}
		}
	}
	return recordedLogEntry{}, false
}

func waitForLogEntry(t *testing.T, logger *recordingLogger, event string) recordedLogEntry {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if entry, ok := logger.findByEvent(event); ok {
			return entry
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("log entry not found for event %q", event)
	return recordedLogEntry{}
}
