package presence

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway/session"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/require"
)

type recordedLogEntry struct {
	level  string
	module string
	msg    string
	fields []wklog.Field
}

func (e recordedLogEntry) field(key string) (wklog.Field, bool) {
	for _, field := range e.fields {
		if field.Key == key {
			return field, true
		}
	}
	return wklog.Field{}, false
}

type recordingLoggerSink struct {
	mu      sync.Mutex
	entries []recordedLogEntry
}

type recordingLogger struct {
	module string
	base   []wklog.Field
	sink   *recordingLoggerSink
}

func newRecordingLogger(module string) *recordingLogger {
	return &recordingLogger{module: module, sink: &recordingLoggerSink{}}
}

func (r *recordingLogger) Debug(msg string, fields ...wklog.Field) { r.log("DEBUG", msg, fields...) }
func (r *recordingLogger) Info(msg string, fields ...wklog.Field)  { r.log("INFO", msg, fields...) }
func (r *recordingLogger) Warn(msg string, fields ...wklog.Field)  { r.log("WARN", msg, fields...) }
func (r *recordingLogger) Error(msg string, fields ...wklog.Field) { r.log("ERROR", msg, fields...) }
func (r *recordingLogger) Fatal(msg string, fields ...wklog.Field) { r.log("FATAL", msg, fields...) }

func (r *recordingLogger) Named(name string) wklog.Logger {
	if name == "" {
		return r
	}
	module := name
	if r.module != "" {
		module = r.module + "." + name
	}
	return &recordingLogger{module: module, base: append([]wklog.Field(nil), r.base...), sink: r.sink}
}

func (r *recordingLogger) With(fields ...wklog.Field) wklog.Logger {
	merged := append(append([]wklog.Field(nil), r.base...), fields...)
	return &recordingLogger{module: r.module, base: merged, sink: r.sink}
}

func (r *recordingLogger) Sync() error { return nil }

func (r *recordingLogger) log(level, msg string, fields ...wklog.Field) {
	if r == nil || r.sink == nil {
		return
	}
	entry := recordedLogEntry{
		level:  level,
		module: r.module,
		msg:    msg,
		fields: append(append([]wklog.Field(nil), r.base...), fields...),
	}
	r.sink.mu.Lock()
	defer r.sink.mu.Unlock()
	r.sink.entries = append(r.sink.entries, entry)
}

func (r *recordingLogger) entries() []recordedLogEntry {
	if r == nil || r.sink == nil {
		return nil
	}
	r.sink.mu.Lock()
	defer r.sink.mu.Unlock()
	out := make([]recordedLogEntry, len(r.sink.entries))
	copy(out, r.sink.entries)
	return out
}

func requireLogEntry(t *testing.T, logger *recordingLogger, level, module, event string) recordedLogEntry {
	t.Helper()
	for _, entry := range logger.entries() {
		if entry.level != level || entry.module != module {
			continue
		}
		field, ok := entry.field("event")
		if ok && field.Value == event {
			return entry
		}
	}
	t.Fatalf("log entry not found: level=%s module=%s event=%s entries=%#v", level, module, event, logger.entries())
	return recordedLogEntry{}
}

func requireFieldValue[T any](t *testing.T, entry recordedLogEntry, key string) T {
	t.Helper()
	field, ok := entry.field(key)
	require.True(t, ok, "field %q not found in entry %#v", key, entry)
	value, ok := field.Value.(T)
	require.True(t, ok, "field %q has type %T, want %T", key, field.Value, *new(T))
	return value
}

func TestActivateLogsAuthorityRegisterFailure(t *testing.T) {
	logger := newRecordingLogger("presence")
	onlineReg := online.NewRegistry()
	authority := &fakeAuthorityClient{registerErr: errors.New("register failed")}
	app := New(Options{
		Logger:          logger,
		LocalNodeID:     1,
		GatewayBootID:   101,
		Online:          onlineReg,
		Router:          fakeRouter{slotID: 1},
		AuthorityClient: authority,
	})

	err := app.Activate(context.Background(), validActivateCommand())
	require.Error(t, err)

	entry := requireLogEntry(t, logger, "ERROR", "presence.activate", "presence.activate.authority_register.failed")
	require.Equal(t, "register authoritative route failed", entry.msg)
	require.Equal(t, "u1", requireFieldValue[string](t, entry, "uid"))
	require.Equal(t, uint64(11), requireFieldValue[uint64](t, entry, "sessionID"))
	require.Equal(t, uint64(1), requireFieldValue[uint64](t, entry, "slotID"))
	require.EqualError(t, requireFieldValue[error](t, entry, "error"), "register failed")
}

func TestDeactivateLogsBestEffortUnregister(t *testing.T) {
	logger := newRecordingLogger("presence")
	onlineReg := online.NewRegistry()
	sess := session.New(session.Config{ID: 11, Listener: "tcp"})
	require.NoError(t, onlineReg.Register(online.OnlineConn{
		SessionID:   11,
		UID:         "u1",
		DeviceID:    "d1",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
		SlotID:      1,
		State:       online.LocalRouteStateActive,
		Listener:    "tcp",
		ConnectedAt: time.Unix(200, 0),
		Session:     sess,
	}))
	authority := &fakeAuthorityClient{unregisterErr: errors.New("best effort")}
	app := New(Options{
		Logger:          logger,
		LocalNodeID:     1,
		GatewayBootID:   101,
		Online:          onlineReg,
		Router:          fakeRouter{slotID: 1},
		AuthorityClient: authority,
	})

	require.NoError(t, app.Deactivate(context.Background(), DeactivateCommand{UID: "u1", SessionID: 11}))

	entry := requireLogEntry(t, logger, "WARN", "presence.activate", "presence.activate.authority_unregister.failed")
	require.Equal(t, "unregister authoritative route failed", entry.msg)
	require.Equal(t, "u1", requireFieldValue[string](t, entry, "uid"))
	require.Equal(t, uint64(11), requireFieldValue[uint64](t, entry, "sessionID"))
	require.Equal(t, uint64(1), requireFieldValue[uint64](t, entry, "slotID"))
	require.EqualError(t, requireFieldValue[error](t, entry, "error"), "best effort")
}

func TestApplyRouteActionLogsFencedMismatch(t *testing.T) {
	logger := newRecordingLogger("presence")
	onlineReg := online.NewRegistry()
	sess := session.New(session.Config{ID: 11, Listener: "tcp"})
	require.NoError(t, onlineReg.Register(online.OnlineConn{
		SessionID:   11,
		UID:         "u1",
		DeviceID:    "d1",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
		SlotID:      1,
		State:       online.LocalRouteStateActive,
		Listener:    "tcp",
		ConnectedAt: time.Unix(200, 0),
		Session:     sess,
	}))
	app := New(Options{
		Logger:        logger,
		LocalNodeID:   1,
		GatewayBootID: 101,
		Online:        onlineReg,
	})

	err := app.ApplyRouteAction(context.Background(), RouteAction{
		UID:       "u2",
		NodeID:    1,
		BootID:    101,
		SessionID: 11,
		Kind:      "close",
	})
	require.Error(t, err)

	entry := requireLogEntry(t, logger, "ERROR", "presence.activate", "presence.activate.route_action.rejected")
	require.Equal(t, "reject fenced route action", entry.msg)
	require.Equal(t, "u2", requireFieldValue[string](t, entry, "uid"))
	require.Equal(t, uint64(11), requireFieldValue[uint64](t, entry, "sessionID"))
	require.Equal(t, uint64(1), requireFieldValue[uint64](t, entry, "nodeID"))
	require.EqualError(t, requireFieldValue[error](t, entry, "error"), "presence: fenced route mismatch for session 11")
}

func validActivateCommand() ActivateCommand {
	return ActivateCommand{
		UID:         "u1",
		DeviceID:    "d1",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
		Listener:    "tcp",
		ConnectedAt: time.Unix(200, 0),
		Session:     session.New(session.Config{ID: 11, Listener: "tcp"}),
	}
}
