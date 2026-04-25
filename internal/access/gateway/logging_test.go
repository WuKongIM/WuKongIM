package gateway

import (
	"context"
	"errors"
	"sync"
	"testing"

	coregateway "github.com/WuKongIM/WuKongIM/internal/gateway"
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

func TestHandlerOnSessionActivateLogsAuthFailure(t *testing.T) {
	logger := newRecordingLogger("access.gateway")
	handler := newHandlerWithPresence(t, &fakePresenceUsecase{}, Options{Logger: logger})

	_, err := handler.OnSessionActivate(newOptionRecordingContext(1, "tcp"))
	require.ErrorIs(t, err, ErrUnauthenticatedSession)

	entry := requireLogEntry(t, logger, "WARN", "access.gateway.conn", "access.gateway.conn.auth_failed")
	require.Equal(t, "reject unauthenticated session", entry.msg)
	require.Equal(t, uint64(1), requireFieldValue[uint64](t, entry, "sessionID"))
	require.EqualError(t, requireFieldValue[error](t, entry, "error"), ErrUnauthenticatedSession.Error())
}

func TestHandleSendLogsRejectedRequest(t *testing.T) {
	logger := newRecordingLogger("access.gateway")
	sender := newOptionRecordingSession(1, "tcp")
	sender.SetValue(coregateway.SessionValueUID, "u1")
	handler := New(Options{Logger: logger, Messages: &fakeMessageUsecase{}})

	err := handler.OnFrame(newSendContext(sender), &frame.SendPacket{
		ChannelID:   "u3@u4",
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   2,
		ClientMsgNo: "m-invalid",
	})
	require.NoError(t, err)

	entry := requireLogEntry(t, logger, "WARN", "access.gateway.frame", "access.gateway.frame.send_rejected")
	require.Equal(t, "reject send request", entry.msg)
	require.Equal(t, uint64(1), requireFieldValue[uint64](t, entry, "sessionID"))
	require.Equal(t, "u1", requireFieldValue[string](t, entry, "uid"))
	require.EqualError(t, requireFieldValue[error](t, entry, "error"), "runtime/channelid: invalid person channel")
}

func TestHandleSendLogsContextualWarnWithSourceModule(t *testing.T) {
	logger := newRecordingLogger("access.gateway")
	sender := newOptionRecordingSession(1, "tcp")
	sender.SetValue(coregateway.SessionValueUID, "u1")
	handler := New(Options{Logger: logger, Messages: &fakeMessageUsecase{sendErr: errors.New("raft quorum unavailable")}})

	err := handler.OnFrame(newSendContext(sender), &frame.SendPacket{
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   13,
		ClientMsgNo: "m4",
		Payload:     []byte("hi"),
	})
	require.Error(t, err)

	entry := requireLogEntry(t, logger, "WARN", "access.gateway.frame", "access.gateway.frame.send_failed")
	require.Equal(t, "send request failed", entry.msg)
	require.Equal(t, "message.send", requireFieldValue[string](t, entry, "sourceModule"))
	require.Equal(t, uint64(1), requireFieldValue[uint64](t, entry, "sessionID"))
	require.Equal(t, "u1", requireFieldValue[string](t, entry, "uid"))
	require.Equal(t, "u2@u1", requireFieldValue[string](t, entry, "channelID"))
	require.EqualError(t, requireFieldValue[error](t, entry, "error"), "raft quorum unavailable")
}

func newOptionRecordingContext(sessionID uint64, listener string) *coregateway.Context {
	sess := newOptionRecordingSession(sessionID, listener)
	return &coregateway.Context{Session: sess, Listener: listener}
}

func newSendContext(sender *optionRecordingSession) *coregateway.Context {
	return &coregateway.Context{
		Session:        sender,
		Listener:       sender.Listener(),
		ReplyToken:     "reply-1",
		RequestContext: context.Background(),
	}
}
