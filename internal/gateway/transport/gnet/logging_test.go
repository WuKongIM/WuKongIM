package gnet

import (
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway/transport"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func TestTCPListenerLogsDebugOnConnectSuccess(t *testing.T) {
	logger := newTransportRecordingLogger()
	handler := newTCPRecordingHandler(nil)
	spec := namedTCPListenerSpecWithAddress("tcp-one", handler, freeTCPAddress(t))
	spec.Options.Logger = logger
	listener, err := NewFactory().Build([]transport.ListenerSpec{spec})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	handle := requireListenerHandle(t, listener[0])
	defer func() { _ = handle.Stop() }()

	if err := handle.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	conn, err := net.Dial("tcp", handle.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_ = conn.Close()

	waitUntil(t, time.Second, func() bool { return handler.OpenCount() == 1 })
	entry := waitForTransportLogEntry(t, logger, "gateway.transport.conn.connected")
	if got, want := entry.stringField("listener"), "tcp-one"; got != want {
		t.Fatalf("listener = %q, want %q", got, want)
	}
	if got, want := entry.stringField("network"), "tcp"; got != want {
		t.Fatalf("network = %q, want %q", got, want)
	}
	if entry.uint64Field("connID") == 0 {
		t.Fatal("connID = 0, want non-zero")
	}
}

func TestTCPListenerLogsDebugOnConnectFailure(t *testing.T) {
	logger := newTransportRecordingLogger()
	wantErr := errors.New("reject open")
	spec := namedTCPListenerSpecWithAddress("tcp-one", newTCPRecordingHandler(nil), freeTCPAddress(t))
	spec.Options.Logger = logger
	spec.Handler = &openFailConnHandler{err: wantErr}
	listener, err := NewFactory().Build([]transport.ListenerSpec{spec})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	handle := requireListenerHandle(t, listener[0])
	defer func() { _ = handle.Stop() }()

	if err := handle.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	conn, err := net.Dial("tcp", handle.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_ = conn.Close()

	entry := waitForTransportLogEntry(t, logger, "gateway.transport.conn.connect_failed")
	if got := entry.errorField("error"); !errors.Is(got, wantErr) {
		t.Fatalf("error = %v, want %v", got, wantErr)
	}
}

func TestWSListenerLogsDebugOnConnectSuccess(t *testing.T) {
	logger := newTransportRecordingLogger()
	handler := newTCPRecordingHandler(nil)
	spec := namedWSListenerSpecWithAddress("ws-one", handler, freeTCPAddress(t), "", nil)
	spec.Options.Logger = logger
	listener, err := NewFactory().Build([]transport.ListenerSpec{spec})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	handle := requireListenerHandle(t, listener[0])
	defer func() { _ = handle.Stop() }()

	if err := handle.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	conn := mustDialWS(t, handle.Addr(), "")
	_ = conn.Close()

	waitUntil(t, time.Second, func() bool { return handler.OpenCount() == 1 })
	entry := waitForTransportLogEntry(t, logger, "gateway.transport.conn.connected")
	if got, want := entry.stringField("listener"), "ws-one"; got != want {
		t.Fatalf("listener = %q, want %q", got, want)
	}
	if got, want := entry.stringField("network"), "websocket"; got != want {
		t.Fatalf("network = %q, want %q", got, want)
	}
}

func TestWSListenerLogsDebugOnHandshakeFailure(t *testing.T) {
	logger := newTransportRecordingLogger()
	spec := namedWSListenerSpecWithAddress("ws-one", newTCPRecordingHandler(nil), freeTCPAddress(t), "", nil)
	spec.Options.Logger = logger
	listener, err := NewFactory().Build([]transport.ListenerSpec{spec})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	handle := requireListenerHandle(t, listener[0])
	defer func() { _ = handle.Stop() }()

	if err := handle.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	conn, err := net.Dial("tcp", handle.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := io.WriteString(conn, rawUpgradeRequest("/", map[string]string{
		"Host":                  handle.Addr(),
		"Connection":            "Upgrade",
		"Sec-WebSocket-Key":     validWSKey,
		"Sec-WebSocket-Version": "13",
	})); err != nil {
		t.Fatalf("WriteString: %v", err)
	}

	entry := waitForTransportLogEntry(t, logger, "gateway.transport.conn.connect_failed")
	if got, want := entry.stringField("listener"), "ws-one"; got != want {
		t.Fatalf("listener = %q, want %q", got, want)
	}
	if got, want := entry.stringField("network"), "websocket"; got != want {
		t.Fatalf("network = %q, want %q", got, want)
	}
	if got := entry.errorField("error"); got == nil {
		t.Fatal("error = nil, want non-nil")
	}
}

type openFailConnHandler struct {
	err error
}

func (h *openFailConnHandler) OnOpen(transport.Conn) error         { return h.err }
func (h *openFailConnHandler) OnData(transport.Conn, []byte) error { return nil }
func (h *openFailConnHandler) OnClose(transport.Conn, error)       {}

type transportRecordedLogEntry struct {
	level  string
	msg    string
	fields []wklog.Field
}

func (e transportRecordedLogEntry) stringField(key string) string {
	for _, field := range e.fields {
		if field.Key == key {
			if value, ok := field.Value.(string); ok {
				return value
			}
		}
	}
	return ""
}

func (e transportRecordedLogEntry) uint64Field(key string) uint64 {
	for _, field := range e.fields {
		if field.Key == key {
			if value, ok := field.Value.(uint64); ok {
				return value
			}
		}
	}
	return 0
}

func (e transportRecordedLogEntry) errorField(key string) error {
	for _, field := range e.fields {
		if field.Key == key {
			if value, ok := field.Value.(error); ok {
				return value
			}
		}
	}
	return nil
}

type transportRecordingLogger struct {
	mu      sync.Mutex
	entries []transportRecordedLogEntry
}

func newTransportRecordingLogger() *transportRecordingLogger { return &transportRecordingLogger{} }

func (r *transportRecordingLogger) Debug(msg string, fields ...wklog.Field) {
	r.log("DEBUG", msg, fields...)
}
func (r *transportRecordingLogger) Info(msg string, fields ...wklog.Field) {
	r.log("INFO", msg, fields...)
}
func (r *transportRecordingLogger) Warn(msg string, fields ...wklog.Field) {
	r.log("WARN", msg, fields...)
}
func (r *transportRecordingLogger) Error(msg string, fields ...wklog.Field) {
	r.log("ERROR", msg, fields...)
}
func (r *transportRecordingLogger) Fatal(msg string, fields ...wklog.Field) {
	r.log("FATAL", msg, fields...)
}
func (r *transportRecordingLogger) Named(string) wklog.Logger        { return r }
func (r *transportRecordingLogger) With(...wklog.Field) wklog.Logger { return r }
func (r *transportRecordingLogger) Sync() error                      { return nil }

func (r *transportRecordingLogger) log(level, msg string, fields ...wklog.Field) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, transportRecordedLogEntry{
		level:  level,
		msg:    msg,
		fields: append([]wklog.Field(nil), fields...),
	})
}

func (r *transportRecordingLogger) findByEvent(event string) (transportRecordedLogEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entry := range r.entries {
		for _, field := range entry.fields {
			if field.Key == "event" && field.Value == event {
				return entry, true
			}
		}
	}
	return transportRecordedLogEntry{}, false
}

func waitForTransportLogEntry(t *testing.T, logger *transportRecordingLogger, event string) transportRecordedLogEntry {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if entry, ok := logger.findByEvent(event); ok {
			return entry
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("log entry not found for event %q", event)
	return transportRecordedLogEntry{}
}
