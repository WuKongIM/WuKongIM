package app

import (
	"context"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

type capturedLogEntry struct {
	level  string
	module string
	msg    string
	fields []wklog.Field
}

func (e capturedLogEntry) field(key string) (wklog.Field, bool) {
	for _, field := range e.fields {
		if field.Key == key {
			return field, true
		}
	}
	return wklog.Field{}, false
}

type capturedLoggerSink struct {
	mu      sync.Mutex
	entries []capturedLogEntry
}

type capturedLogger struct {
	module string
	base   []wklog.Field
	sink   *capturedLoggerSink
}

func newCapturedLogger(module string) *capturedLogger {
	return &capturedLogger{module: module, sink: &capturedLoggerSink{}}
}

func (l *capturedLogger) Debug(msg string, fields ...wklog.Field) { l.log("DEBUG", msg, fields...) }
func (l *capturedLogger) Info(msg string, fields ...wklog.Field)  { l.log("INFO", msg, fields...) }
func (l *capturedLogger) Warn(msg string, fields ...wklog.Field)  { l.log("WARN", msg, fields...) }
func (l *capturedLogger) Error(msg string, fields ...wklog.Field) { l.log("ERROR", msg, fields...) }
func (l *capturedLogger) Fatal(msg string, fields ...wklog.Field) { l.log("FATAL", msg, fields...) }

func (l *capturedLogger) Named(name string) wklog.Logger {
	if name == "" {
		return l
	}
	module := name
	if l.module != "" {
		module = l.module + "." + name
	}
	return &capturedLogger{module: module, base: append([]wklog.Field(nil), l.base...), sink: l.sink}
}

func (l *capturedLogger) With(fields ...wklog.Field) wklog.Logger {
	merged := append(append([]wklog.Field(nil), l.base...), fields...)
	return &capturedLogger{module: l.module, base: merged, sink: l.sink}
}

func (l *capturedLogger) Sync() error { return nil }

func (l *capturedLogger) log(level, msg string, fields ...wklog.Field) {
	if l == nil || l.sink == nil {
		return
	}
	entry := capturedLogEntry{
		level:  level,
		module: l.module,
		msg:    msg,
		fields: append(append([]wklog.Field(nil), l.base...), fields...),
	}
	l.sink.mu.Lock()
	defer l.sink.mu.Unlock()
	l.sink.entries = append(l.sink.entries, entry)
}

func (l *capturedLogger) entries() []capturedLogEntry {
	if l == nil || l.sink == nil {
		return nil
	}
	l.sink.mu.Lock()
	defer l.sink.mu.Unlock()
	out := make([]capturedLogEntry, len(l.sink.entries))
	copy(out, l.sink.entries)
	return out
}

func requireCapturedLogEntry(t *testing.T, logger *capturedLogger, level, module, event string) capturedLogEntry {
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
	return capturedLogEntry{}
}

func requireCapturedFieldValue[T any](t *testing.T, entry capturedLogEntry, key string) T {
	t.Helper()
	field, ok := entry.field(key)
	require.True(t, ok, "field %q not found in entry %#v", key, entry)
	value, ok := field.Value.(T)
	require.True(t, ok, "field %q has type %T, want %T", key, field.Value, *new(T))
	return value
}

func TestAppChannelClusterUpdatesObservabilityMetrics(t *testing.T) {
	key := channel.ChannelKey("room")
	meta := channel.Meta{
		Key:    key,
		ID:     channel.ChannelID{ID: "room", Type: 2},
		Status: channel.StatusActive,
	}
	service := &stubChannelService{
		appendResult: channel.AppendResult{MessageID: 9, MessageSeq: 10},
		fetchResult:  channel.FetchResult{},
	}
	runtime := &stubChannelRuntime{}
	registry := obsmetrics.New(1, "node-1")
	cluster := &appChannelCluster{
		service: service,
		runtime: runtime,
		metrics: registry,
	}

	require.NoError(t, cluster.ApplyMeta(meta))

	_, err := cluster.Append(context.Background(), channel.AppendRequest{ChannelID: meta.ID})
	require.NoError(t, err)

	_, err = cluster.Fetch(context.Background(), channel.FetchRequest{ChannelID: meta.ID, Limit: 1, MaxBytes: 1})
	require.NoError(t, err)

	require.Equal(t, int64(1), registry.Channel.Snapshot().ActiveChannels)

	families, err := registry.Gather()
	require.NoError(t, err)
	appendTotal := requireMetricFamilyByName(t, families, "wukongim_channel_append_total")
	require.Len(t, appendTotal.Metric, 1)
	require.Equal(t, float64(1), appendTotal.Metric[0].GetCounter().GetValue())

	require.NoError(t, cluster.RemoveLocal(key))
	require.Equal(t, int64(0), registry.Channel.Snapshot().ActiveChannels)
}

func TestAppChannelClusterAppendForwardsToLeaderWhenLocalReplicaIsFollower(t *testing.T) {
	req := channel.AppendRequest{
		ChannelID: channel.ChannelID{ID: "room", Type: 2},
		Message:   channel.Message{FromUID: "u1", ClientMsgNo: "m1", Payload: []byte("hi")},
	}
	service := &stubChannelService{
		meta: channel.Meta{
			Key:    channel.ChannelKey("room"),
			ID:     req.ChannelID,
			Leader: 2,
		},
		appendErr: channel.ErrNotLeader,
	}
	remote := &recordingRemoteChannelAppender{
		result: channel.AppendResult{MessageID: 9, MessageSeq: 10},
	}
	cluster := &appChannelCluster{
		service:        service,
		localNodeID:    1,
		remoteAppender: remote,
	}

	result, err := cluster.Append(context.Background(), req)

	require.NoError(t, err)
	require.Equal(t, channel.AppendResult{MessageID: 9, MessageSeq: 10}, result)
	require.Len(t, remote.calls, 1)
	require.Equal(t, uint64(2), remote.calls[0].nodeID)
	require.Equal(t, req, remote.calls[0].req)
}

func TestAppChannelClusterAppendForwardsToLeaderWhenLocalRuntimeIsMissingAfterRefresh(t *testing.T) {
	req := channel.AppendRequest{
		ChannelID: channel.ChannelID{ID: "room", Type: 2},
		Message:   channel.Message{FromUID: "u1", ClientMsgNo: "m2", Payload: []byte("hi")},
	}
	service := &stubChannelService{
		meta: channel.Meta{
			Key:    channel.ChannelKey("room"),
			ID:     req.ChannelID,
			Leader: 2,
		},
		appendErr: channel.ErrStaleMeta,
	}
	remote := &recordingRemoteChannelAppender{
		result: channel.AppendResult{MessageID: 10, MessageSeq: 11},
	}
	cluster := &appChannelCluster{
		service:        service,
		localNodeID:    1,
		remoteAppender: remote,
	}

	result, err := cluster.Append(context.Background(), req)

	require.NoError(t, err)
	require.Equal(t, channel.AppendResult{MessageID: 10, MessageSeq: 11}, result)
	require.Len(t, remote.calls, 1)
	require.Equal(t, uint64(2), remote.calls[0].nodeID)
	require.Equal(t, req, remote.calls[0].req)
}

func TestAppChannelClusterAppendLogsForwardDiagnostics(t *testing.T) {
	req := channel.AppendRequest{
		ChannelID: channel.ChannelID{ID: "room", Type: 2},
		Message:   channel.Message{FromUID: "u1", ClientMsgNo: "m1", Payload: []byte("hi")},
	}
	service := &stubChannelService{
		meta: channel.Meta{
			Key:    channel.ChannelKey("room"),
			ID:     req.ChannelID,
			Leader: 2,
		},
		appendErr: channel.ErrNotLeader,
	}
	remote := &recordingRemoteChannelAppender{
		result: channel.AppendResult{MessageID: 9, MessageSeq: 10},
	}
	logger := newCapturedLogger("app")
	cluster := &appChannelCluster{
		service:        service,
		localNodeID:    1,
		remoteAppender: remote,
		logger:         logger,
	}

	_, err := cluster.Append(context.Background(), req)

	require.NoError(t, err)
	entry := requireCapturedLogEntry(t, logger, "DEBUG", "app.channel.append", "app.channel.append.forward.triggered")
	require.Equal(t, "forwarding channel append to leader", entry.msg)
	require.Equal(t, uint64(1), requireCapturedFieldValue[uint64](t, entry, "nodeID"))
	require.Equal(t, uint64(2), requireCapturedFieldValue[uint64](t, entry, "leaderNodeID"))
	require.Equal(t, "room", requireCapturedFieldValue[string](t, entry, "channelID"))
	require.Equal(t, "m1", requireCapturedFieldValue[string](t, entry, "clientMsgNo"))
}

type stubChannelService struct {
	meta         channel.Meta
	appendResult channel.AppendResult
	appendErr    error
	fetchResult  channel.FetchResult
	fetchErr     error
}

func (s *stubChannelService) ApplyMeta(meta channel.Meta) error {
	s.meta = meta
	return nil
}

func (s *stubChannelService) Append(context.Context, channel.AppendRequest) (channel.AppendResult, error) {
	return s.appendResult, s.appendErr
}

func (s *stubChannelService) Fetch(context.Context, channel.FetchRequest) (channel.FetchResult, error) {
	return s.fetchResult, s.fetchErr
}

func (s *stubChannelService) Status(channel.ChannelID) (channel.ChannelRuntimeStatus, error) {
	return channel.ChannelRuntimeStatus{}, nil
}

func (s *stubChannelService) MetaSnapshot(channel.ChannelKey) (channel.Meta, bool) {
	return s.meta, s.meta.Key != ""
}

func (s *stubChannelService) RestoreMeta(_ channel.ChannelKey, meta channel.Meta, _ bool) {
	s.meta = meta
}

type stubChannelRuntime struct {
	upserts []channel.Meta
	removes []channel.ChannelKey
}

func (s *stubChannelRuntime) UpsertMeta(meta channel.Meta) error {
	s.upserts = append(s.upserts, meta)
	return nil
}

func (s *stubChannelRuntime) RemoveChannel(key channel.ChannelKey) error {
	s.removes = append(s.removes, key)
	return nil
}

type remoteChannelAppendCall struct {
	nodeID uint64
	req    channel.AppendRequest
}

type recordingRemoteChannelAppender struct {
	calls  []remoteChannelAppendCall
	result channel.AppendResult
	err    error
}

func (r *recordingRemoteChannelAppender) AppendToLeader(ctx context.Context, nodeID uint64, req channel.AppendRequest) (channel.AppendResult, error) {
	r.calls = append(r.calls, remoteChannelAppendCall{nodeID: nodeID, req: req})
	return r.result, r.err
}

func requireMetricFamilyByName(t *testing.T, families []*dto.MetricFamily, name string) *dto.MetricFamily {
	t.Helper()
	for _, family := range families {
		if family.GetName() == name {
			return family
		}
	}
	t.Fatalf("metric family %q not found", name)
	return nil
}
