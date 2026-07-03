package app

import (
	"context"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
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

type debugDisabledCapturedLogger struct {
	*capturedLogger
}

func (l *debugDisabledCapturedLogger) DebugEnabled() bool { return false }

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

func TestAppChannelClusterLocalAppendRecordsTraceID(t *testing.T) {
	sink := &recordingAppSendTraceSink{}
	restore := sendtrace.SetSink(sink)
	t.Cleanup(restore)

	service := &stubChannelService{
		appendResult: channel.AppendResult{MessageID: 9, MessageSeq: 10},
	}
	cluster := &appChannelCluster{service: service, localNodeID: 1}

	_, err := cluster.Append(context.Background(), channel.AppendRequest{
		ChannelID: channel.ChannelID{ID: "room", Type: 2},
		Message:   channel.Message{FromUID: "u1", ClientMsgNo: "m1"},
		TraceID:   "trace-1",
		Attempt:   2,
	})

	require.NoError(t, err)
	events := sink.snapshot()
	require.Len(t, events, 1)
	require.Equal(t, sendtrace.StageChannelAppendLocal, events[0].Stage)
	require.Equal(t, "trace-1", events[0].TraceID)
	require.Equal(t, "u1", events[0].FromUID)
	require.Equal(t, 2, events[0].Attempt)
}

func TestAppChannelClusterForwardAppendRecordsTraceID(t *testing.T) {
	sink := &recordingAppSendTraceSink{}
	restore := sendtrace.SetSink(sink)
	t.Cleanup(restore)

	req := channel.AppendRequest{
		ChannelID: channel.ChannelID{ID: "room", Type: 2},
		Message:   channel.Message{FromUID: "u1", ClientMsgNo: "m1", Payload: []byte("hi")},
		TraceID:   "trace-1",
		Attempt:   3,
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

	_, err := cluster.Append(context.Background(), req)

	require.NoError(t, err)
	events := sink.snapshot()
	require.Len(t, events, 1)
	require.Equal(t, sendtrace.StageChannelAppendForward, events[0].Stage)
	require.Equal(t, "trace-1", events[0].TraceID)
	require.Equal(t, "u1", events[0].FromUID)
	require.Equal(t, 3, events[0].Attempt)
	require.Equal(t, "channel_append", events[0].Service)
	require.Equal(t, 1, events[0].RecordCount)
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
	require.Equal(t, channel.AppendResult{MessageID: 9, MessageSeq: 10, Message: req.Message}, result)
	require.Empty(t, remote.calls)
	require.Len(t, remote.batchCalls, 1)
	require.Equal(t, uint64(2), remote.batchCalls[0].nodeID)
	require.Equal(t, appendRequestAsBatchForTest(req), remote.batchCalls[0].req)
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
	require.Equal(t, channel.AppendResult{MessageID: 10, MessageSeq: 11, Message: req.Message}, result)
	require.Empty(t, remote.calls)
	require.Len(t, remote.batchCalls, 1)
	require.Equal(t, uint64(2), remote.batchCalls[0].nodeID)
	require.Equal(t, appendRequestAsBatchForTest(req), remote.batchCalls[0].req)
}

func TestAppChannelClusterAppendBatchForwardsSingleRemoteBatch(t *testing.T) {
	req := channel.AppendBatchRequest{
		ChannelID: channel.ChannelID{ID: "room", Type: 2},
		Messages: []channel.Message{
			{FromUID: "u1", ClientMsgNo: "m1", Payload: []byte("hi-1")},
			{FromUID: "u2", ClientMsgNo: "m2", Payload: []byte("hi-2")},
		},
	}
	want := channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{
		{MessageID: 9, MessageSeq: 10, Message: req.Messages[0]},
		{MessageID: 11, MessageSeq: 12, Message: req.Messages[1]},
	}}
	service := &stubChannelService{
		meta: channel.Meta{
			Key:    channel.ChannelKey("room"),
			ID:     req.ChannelID,
			Leader: 2,
		},
		appendBatchErr: channel.ErrNotLeader,
	}
	remote := &recordingRemoteChannelAppender{batchResult: want}
	cluster := &appChannelCluster{
		service:        service,
		localNodeID:    1,
		remoteAppender: remote,
	}

	result, err := cluster.AppendBatch(context.Background(), req)

	require.NoError(t, err)
	require.Equal(t, want, result)
	require.Empty(t, remote.calls)
	require.Len(t, remote.batchCalls, 1)
	require.Equal(t, uint64(2), remote.batchCalls[0].nodeID)
	require.Equal(t, req, remote.batchCalls[0].req)
}

func TestAppChannelClusterAppendLocalBatchDoesNotForward(t *testing.T) {
	req := channel.AppendBatchRequest{
		ChannelID: channel.ChannelID{ID: "room", Type: 2},
		Messages:  []channel.Message{{FromUID: "u1", ClientMsgNo: "m1", Payload: []byte("hi")}},
	}
	service := &stubChannelService{
		meta: channel.Meta{
			Key:    channel.ChannelKey("room"),
			ID:     req.ChannelID,
			Leader: 2,
		},
		appendBatchErr: channel.ErrNotLeader,
	}
	remote := &recordingRemoteChannelAppender{}
	cluster := &appChannelCluster{
		service:        service,
		localNodeID:    1,
		remoteAppender: remote,
	}

	_, err := cluster.AppendLocalBatch(context.Background(), req)

	require.ErrorIs(t, err, channel.ErrNotLeader)
	require.Empty(t, remote.calls)
	require.Empty(t, remote.batchCalls)
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
	entry := requireCapturedLogEntry(t, logger, "DEBUG", "app.channel.append", "app.channel.append_batch.forward.triggered")
	require.Equal(t, "forwarding channel append batch to leader", entry.msg)
	require.Equal(t, uint64(1), requireCapturedFieldValue[uint64](t, entry, "nodeID"))
	require.Equal(t, uint64(2), requireCapturedFieldValue[uint64](t, entry, "leaderNodeID"))
	require.Equal(t, "room", requireCapturedFieldValue[string](t, entry, "channelID"))
	require.Equal(t, 1, requireCapturedFieldValue[int](t, entry, "recordCount"))
}

func appendRequestAsBatchForTest(req channel.AppendRequest) channel.AppendBatchRequest {
	return channel.AppendBatchRequest{
		ChannelID:             req.ChannelID,
		Messages:              []channel.Message{req.Message},
		SupportsMessageSeqU64: req.SupportsMessageSeqU64,
		CommitMode:            req.CommitMode,
		ExpectedChannelEpoch:  req.ExpectedChannelEpoch,
		ExpectedLeaderEpoch:   req.ExpectedLeaderEpoch,
		TraceID:               req.TraceID,
		Attempt:               req.Attempt,
	}
}

type stubChannelService struct {
	meta              channel.Meta
	appendResult      channel.AppendResult
	appendErr         error
	appendBatchResult channel.AppendBatchResult
	appendBatchErr    error
	fetchResult       channel.FetchResult
	fetchErr          error
}

func (s *stubChannelService) ApplyMeta(meta channel.Meta) error {
	s.meta = meta
	return nil
}

func (s *stubChannelService) Append(context.Context, channel.AppendRequest) (channel.AppendResult, error) {
	return s.appendResult, s.appendErr
}

func (s *stubChannelService) AppendBatch(_ context.Context, req channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	if s.appendBatchErr != nil {
		return s.appendBatchResult, s.appendBatchErr
	}
	if s.appendErr != nil {
		return s.appendBatchResult, s.appendErr
	}
	if len(s.appendBatchResult.Items) > 0 {
		return s.appendBatchResult, nil
	}
	items := make([]channel.AppendBatchItemResult, len(req.Messages))
	for i, msg := range req.Messages {
		items[i] = channel.AppendBatchItemResult{MessageID: s.appendResult.MessageID, MessageSeq: s.appendResult.MessageSeq, Message: msg}
	}
	return channel.AppendBatchResult{Items: items}, nil
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

type remoteChannelAppendBatchCall struct {
	nodeID uint64
	req    channel.AppendBatchRequest
}

type recordingRemoteChannelAppender struct {
	calls       []remoteChannelAppendCall
	batchCalls  []remoteChannelAppendBatchCall
	result      channel.AppendResult
	batchResult channel.AppendBatchResult
	err         error
	batchErr    error
}

func (r *recordingRemoteChannelAppender) AppendBatchToLeader(ctx context.Context, nodeID uint64, req channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	r.batchCalls = append(r.batchCalls, remoteChannelAppendBatchCall{nodeID: nodeID, req: req})
	if len(r.batchResult.Items) == 0 && len(req.Messages) == 1 {
		msg := req.Messages[0]
		return channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{{
			MessageID:  r.result.MessageID,
			MessageSeq: r.result.MessageSeq,
			Message:    msg,
		}}}, r.err
	}
	return r.batchResult, r.batchErr
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

type recordingAppSendTraceSink struct {
	mu     sync.Mutex
	events []sendtrace.Event
}

func (s *recordingAppSendTraceSink) RecordSendTrace(event sendtrace.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *recordingAppSendTraceSink) snapshot() []sendtrace.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]sendtrace.Event, len(s.events))
	copy(out, s.events)
	return out
}
