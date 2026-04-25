package message

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
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

func TestSendLogsPrimaryFailureWithMessageModule(t *testing.T) {
	logger := newRecordingLogger("message")
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{{err: errors.New("raft quorum unavailable")}},
	}
	app := New(Options{
		Now:           fixedNowFn,
		Logger:        logger,
		Cluster:       cluster,
		MetaRefresher: &fakeMetaRefresher{},
	})

	_, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})
	require.Error(t, err)

	entry := requireLogEntry(t, logger, "ERROR", "message.send", "message.send.append.failed")
	require.Equal(t, "local append failed", entry.msg)
	require.Equal(t, "u2@u1", requireFieldValue[string](t, entry, "channelID"))
	require.Equal(t, int64(frame.ChannelTypePerson), requireFieldValue[int64](t, entry, "channelType"))
	require.Equal(t, "u1", requireFieldValue[string](t, entry, "uid"))
	require.EqualError(t, requireFieldValue[error](t, entry, "error"), "raft quorum unavailable")
}

func TestSendLogsResolvedMetaAfterRefresh(t *testing.T) {
	logger := newRecordingLogger("message")
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{result: channel.AppendResult{MessageID: 88, MessageSeq: 9}},
		},
	}
	refresher := &fakeMetaRefresher{metas: []channel.Meta{{
		ID:          channel.ChannelID{ID: "u2@u1", Type: frame.ChannelTypePerson},
		Epoch:       4,
		LeaderEpoch: 5,
	}}}
	app := New(Options{
		Now:           fixedNowFn,
		Logger:        logger,
		Cluster:       cluster,
		MetaRefresher: refresher,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})
	require.NoError(t, err)
	require.Equal(t, int64(88), result.MessageID)
	require.Equal(t, uint64(9), result.MessageSeq)

	entry := requireLogEntry(t, logger, "DEBUG", "message.send", "message.send.meta.resolved")
	require.Equal(t, "resolved channel metadata", entry.msg)
	require.Equal(t, "u2@u1", requireFieldValue[string](t, entry, "channelID"))
	require.Equal(t, "u1", requireFieldValue[string](t, entry, "uid"))
}

func TestSendLogsResolvedMetaWithLeaderDetails(t *testing.T) {
	logger := newRecordingLogger("message")
	remote := &fakeRemoteAppender{
		replies: []fakeRemoteAppenderReply{
			{result: channel.AppendResult{MessageID: 88, MessageSeq: 9}},
		},
	}
	refresher := &fakeMetaRefresher{metas: []channel.Meta{{
		ID:          channel.ChannelID{ID: "u2@u1", Type: frame.ChannelTypePerson},
		Epoch:       4,
		LeaderEpoch: 5,
		Leader:      2,
		Replicas:    []channel.NodeID{1, 2, 3},
		ISR:         []channel.NodeID{1, 2, 3},
		MinISR:      3,
		LeaseUntil:  fixedSendNow.Add(3 * time.Second),
	}}}
	app := New(Options{
		Now:            fixedNowFn,
		Logger:         logger,
		Cluster:        &fakeChannelCluster{},
		MetaRefresher:  refresher,
		RemoteAppender: remote,
	})

	_, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})
	require.NoError(t, err)

	entry := requireLogEntry(t, logger, "DEBUG", "message.send", "message.send.meta.resolved")
	require.Equal(t, "resolved channel metadata", entry.msg)
	require.Equal(t, uint64(2), requireFieldValue[uint64](t, entry, "leaderNodeID"))
	require.Equal(t, uint64(4), requireFieldValue[uint64](t, entry, "channelEpoch"))
	require.Equal(t, uint64(5), requireFieldValue[uint64](t, entry, "leaderEpoch"))
	require.Equal(t, 3, requireFieldValue[int](t, entry, "replicaCount"))
	require.Equal(t, 3*time.Second, requireFieldValue[time.Duration](t, entry, "leaseRemaining"))
}

func TestSendLogsDispatchSubmitFailureAsWarn(t *testing.T) {
	logger := newRecordingLogger("message")
	dispatcher := &recordingCommittedDispatcher{err: errors.New("queue full")}
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{{result: channel.AppendResult{MessageID: 101, MessageSeq: 5}}},
	}
	app := New(Options{
		Now:                 fixedNowFn,
		Logger:              logger,
		Cluster:             cluster,
		MetaRefresher:       &fakeMetaRefresher{},
		CommittedDispatcher: dispatcher,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})
	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)

	entry := requireLogEntry(t, logger, "WARN", "message.send", "message.send.dispatch_submit.failed")
	require.Equal(t, "submit committed message failed", entry.msg)
	require.Equal(t, "u2@u1", requireFieldValue[string](t, entry, "channelID"))
	require.Equal(t, "u1", requireFieldValue[string](t, entry, "uid"))
	require.EqualError(t, requireFieldValue[error](t, entry, "error"), "queue full")
}
