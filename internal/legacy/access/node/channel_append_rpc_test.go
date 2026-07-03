package node

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/legacy/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/legacy/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/legacy/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/require"
)

type nodeRecordedLogEntry struct {
	level  string
	module string
	msg    string
	fields []wklog.Field
}

func (e nodeRecordedLogEntry) field(key string) (wklog.Field, bool) {
	for _, field := range e.fields {
		if field.Key == key {
			return field, true
		}
	}
	return wklog.Field{}, false
}

type nodeRecordingLoggerSink struct {
	mu      sync.Mutex
	entries []nodeRecordedLogEntry
}

type nodeRecordingLogger struct {
	module string
	base   []wklog.Field
	sink   *nodeRecordingLoggerSink
}

func newNodeRecordingLogger(module string) *nodeRecordingLogger {
	return &nodeRecordingLogger{module: module, sink: &nodeRecordingLoggerSink{}}
}

func (l *nodeRecordingLogger) Debug(msg string, fields ...wklog.Field) {
	l.log("DEBUG", msg, fields...)
}
func (l *nodeRecordingLogger) Info(msg string, fields ...wklog.Field) { l.log("INFO", msg, fields...) }
func (l *nodeRecordingLogger) Warn(msg string, fields ...wklog.Field) { l.log("WARN", msg, fields...) }
func (l *nodeRecordingLogger) Error(msg string, fields ...wklog.Field) {
	l.log("ERROR", msg, fields...)
}
func (l *nodeRecordingLogger) Fatal(msg string, fields ...wklog.Field) {
	l.log("FATAL", msg, fields...)
}

func (l *nodeRecordingLogger) Named(name string) wklog.Logger {
	if name == "" {
		return l
	}
	module := name
	if l.module != "" {
		module = l.module + "." + name
	}
	return &nodeRecordingLogger{module: module, base: append([]wklog.Field(nil), l.base...), sink: l.sink}
}

func (l *nodeRecordingLogger) With(fields ...wklog.Field) wklog.Logger {
	merged := append(append([]wklog.Field(nil), l.base...), fields...)
	return &nodeRecordingLogger{module: l.module, base: merged, sink: l.sink}
}

func (l *nodeRecordingLogger) Sync() error { return nil }

func (l *nodeRecordingLogger) log(level, msg string, fields ...wklog.Field) {
	if l == nil || l.sink == nil {
		return
	}
	entry := nodeRecordedLogEntry{
		level:  level,
		module: l.module,
		msg:    msg,
		fields: append(append([]wklog.Field(nil), l.base...), fields...),
	}
	l.sink.mu.Lock()
	defer l.sink.mu.Unlock()
	l.sink.entries = append(l.sink.entries, entry)
}

func (l *nodeRecordingLogger) entries() []nodeRecordedLogEntry {
	if l == nil || l.sink == nil {
		return nil
	}
	l.sink.mu.Lock()
	defer l.sink.mu.Unlock()
	out := make([]nodeRecordedLogEntry, len(l.sink.entries))
	copy(out, l.sink.entries)
	return out
}

func requireNodeLogEntry(t *testing.T, logger *nodeRecordingLogger, level, module, event string) nodeRecordedLogEntry {
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
	return nodeRecordedLogEntry{}
}

func requireNodeFieldValue[T any](t *testing.T, entry nodeRecordedLogEntry, key string) T {
	t.Helper()
	field, ok := entry.field(key)
	require.True(t, ok, "field %q not found in entry %#v", key, entry)
	value, ok := field.Value.(T)
	require.True(t, ok, "field %q has type %T, want %T", key, field.Value, *new(T))
	return value
}

func TestChannelAppendBinaryCodecRoundTrip(t *testing.T) {
	req := channelAppendRequest{AppendRequest: channel.AppendRequest{
		ChannelID: channel.ChannelID{ID: "append-binary", Type: frame.ChannelTypeGroup},
		Message: channel.Message{
			MessageID:  7,
			MessageSeq: 8,
			Framer: frame.Framer{
				FrameType:        frame.SEND,
				RemainingLength:  12,
				NoPersist:        true,
				RedDot:           true,
				SyncOnce:         true,
				DUP:              true,
				HasServerVersion: true,
				End:              true,
				FrameSize:        34,
			},
			Setting:     frame.SettingReceiptEnabled | frame.SettingTopic,
			MsgKey:      "key",
			Expire:      60,
			ClientSeq:   9,
			ClientMsgNo: "client-1",
			StreamNo:    "stream-1",
			StreamID:    10,
			StreamFlag:  frame.StreamFlagIng,
			Timestamp:   1777777777,
			ChannelID:   "append-binary",
			ChannelType: frame.ChannelTypeGroup,
			Topic:       "topic-1",
			FromUID:     "u1",
			Payload:     []byte("hello"),
		},
		SupportsMessageSeqU64: true,
		CommitMode:            channel.CommitModeLocal,
		ExpectedChannelEpoch:  11,
		ExpectedLeaderEpoch:   12,
	}}

	reqBody, err := encodeChannelAppendRequestBinary(req)
	require.NoError(t, err)
	require.True(t, isChannelAppendRequestBinary(reqBody))

	gotReq, err := decodeChannelAppendRequest(reqBody)
	require.NoError(t, err)
	require.Equal(t, req, gotReq)

	resp := channelAppendResponse{
		Status:   rpcStatusOK,
		LeaderID: 2,
		Result: channel.AppendResult{
			MessageID:  13,
			MessageSeq: 14,
			Message:    req.AppendRequest.Message,
		},
	}
	respBody, err := encodeChannelAppendResponse(resp)
	require.NoError(t, err)
	require.True(t, isChannelAppendResponseBinary(respBody))

	gotResp, err := decodeChannelAppendResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}

func TestChannelAppendBatchBinaryCodecRoundTrip(t *testing.T) {
	req := channelAppendBatchRequest{AppendBatchRequest: channel.AppendBatchRequest{
		ChannelID: channel.ChannelID{ID: "append-batch-binary", Type: frame.ChannelTypeGroup},
		Messages: []channel.Message{
			{
				MessageID:   7,
				MessageSeq:  8,
				Framer:      frame.Framer{FrameType: frame.SEND, RedDot: true, SyncOnce: true},
				Setting:     frame.SettingReceiptEnabled | frame.SettingTopic,
				MsgKey:      "key-1",
				Expire:      60,
				ClientSeq:   9,
				ClientMsgNo: "client-1",
				StreamNo:    "stream-1",
				StreamID:    10,
				StreamFlag:  frame.StreamFlagIng,
				Timestamp:   1777777777,
				ChannelID:   "append-batch-binary",
				Topic:       "topic-1",
				FromUID:     "u1",
				Payload:     []byte("hello-1"),
			},
			{
				MessageID:   17,
				MessageSeq:  18,
				Framer:      frame.Framer{FrameType: frame.SEND, RedDot: true},
				ClientSeq:   19,
				ClientMsgNo: "client-2",
				Timestamp:   1777777778,
				ChannelID:   "append-batch-binary",
				FromUID:     "u2",
				Payload:     []byte("hello-2"),
			},
		},
		SupportsMessageSeqU64: true,
		CommitMode:            channel.CommitModeLocal,
		ExpectedChannelEpoch:  11,
		ExpectedLeaderEpoch:   12,
	}}

	reqBody, err := encodeChannelAppendBatchRequestBinary(req)
	require.NoError(t, err)
	require.True(t, isChannelAppendBatchRequestBinary(reqBody))

	gotReq, err := decodeChannelAppendBatchRequest(reqBody)
	require.NoError(t, err)
	require.Equal(t, req, gotReq)

	resp := channelAppendBatchResponse{
		Status:   rpcStatusOK,
		LeaderID: 2,
		Result: channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{
			{MessageID: 13, MessageSeq: 14, Message: req.AppendBatchRequest.Messages[0]},
			{MessageID: 15, MessageSeq: 16, Message: req.AppendBatchRequest.Messages[1], Err: channel.ErrIdempotencyConflict},
		}},
	}
	respBody, err := encodeChannelAppendBatchResponse(resp)
	require.NoError(t, err)
	require.True(t, isChannelAppendBatchResponseBinary(respBody))

	gotResp, err := decodeChannelAppendBatchResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}

func TestChannelAppendRPCRejectsJSONPayload(t *testing.T) {
	adapter := New(Options{ChannelLog: &stubNodeChannelLog{}})

	_, err := adapter.handleChannelAppendRPC(context.Background(), []byte(`{"append_request":{"channel_id":{"id":"append-json","type":2}}}`))
	require.Error(t, err)
}

func TestAppendToLeaderRPCAppendsOnTargetNode(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {1, 2}},
		map[uint64]uint64{1: 1},
	)
	node1 := network.cluster(1)
	node2 := network.cluster(2)

	req := channel.AppendRequest{
		ChannelID: channel.ChannelID{ID: "u2@u1", Type: frame.ChannelTypePerson},
		Message: channel.Message{
			FromUID:     "u1",
			ClientMsgNo: "m1",
			Payload:     []byte("hi"),
		},
	}
	channelLog := &stubNodeChannelLog{
		status: channel.ChannelRuntimeStatus{
			ID:     req.ChannelID,
			Leader: 2,
		},
		appendBatchResult: channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{{
			MessageID:  7,
			MessageSeq: 8,
			Message:    req.Message,
		}}},
	}
	New(Options{
		Cluster:     node2,
		Presence:    presence.New(presence.Options{}),
		Online:      online.NewRegistry(),
		LocalNodeID: 2,
		ChannelLog:  channelLog,
	})

	client := NewClient(node1)
	result, err := client.AppendToLeader(context.Background(), 2, req)

	require.NoError(t, err)
	require.Equal(t, channel.AppendResult{MessageID: 7, MessageSeq: 8, Message: req.Message}, result)
	require.Empty(t, channelLog.appendCalls)
	require.Equal(t, []channel.AppendBatchRequest{{
		ChannelID:             req.ChannelID,
		Messages:              []channel.Message{req.Message},
		SupportsMessageSeqU64: req.SupportsMessageSeqU64,
		CommitMode:            req.CommitMode,
		ExpectedChannelEpoch:  req.ExpectedChannelEpoch,
		ExpectedLeaderEpoch:   req.ExpectedLeaderEpoch,
		TraceID:               req.TraceID,
		Attempt:               req.Attempt,
	}}, channelLog.appendBatchCalls)
}

func TestAppendBatchToLeaderRPCAppendsBatchOnTargetNode(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {1, 2}},
		map[uint64]uint64{1: 1},
	)
	node1 := network.cluster(1)
	node2 := network.cluster(2)

	req := channel.AppendBatchRequest{
		ChannelID: channel.ChannelID{ID: "group-batch", Type: frame.ChannelTypeGroup},
		Messages: []channel.Message{
			{FromUID: "u1", ClientMsgNo: "m1", Payload: []byte("hi-1")},
			{FromUID: "u2", ClientMsgNo: "m2", Payload: []byte("hi-2")},
		},
	}
	want := channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{
		{MessageID: 7, MessageSeq: 8, Message: req.Messages[0]},
		{MessageID: 9, MessageSeq: 10, Message: req.Messages[1]},
	}}
	channelLog := &stubNodeChannelLog{
		status: channel.ChannelRuntimeStatus{
			ID:     req.ChannelID,
			Leader: 2,
		},
		appendBatchResult: want,
	}
	New(Options{
		Cluster:     node2,
		Presence:    presence.New(presence.Options{}),
		Online:      online.NewRegistry(),
		LocalNodeID: 2,
		ChannelLog:  channelLog,
	})

	client := NewClient(node1)
	result, err := client.AppendBatchToLeader(context.Background(), 2, req)

	require.NoError(t, err)
	require.Equal(t, want, result)
	require.Equal(t, []channel.AppendBatchRequest{req}, channelLog.appendBatchCalls)
	require.Empty(t, channelLog.appendCalls)
}

func TestAppendToLeaderRPCFollowsLeaderRedirect(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {1, 2, 3}},
		map[uint64]uint64{1: 1},
	)
	node1 := network.cluster(1)
	node2 := network.cluster(2)
	node3 := network.cluster(3)

	req := channel.AppendRequest{
		ChannelID: channel.ChannelID{ID: "group-1", Type: frame.ChannelTypeGroup},
		Message: channel.Message{
			FromUID:     "u1",
			ClientMsgNo: "m2",
			Payload:     []byte("hello"),
		},
	}
	redirectLog := &stubNodeChannelLog{
		status: channel.ChannelRuntimeStatus{
			ID:     req.ChannelID,
			Leader: 3,
		},
	}
	leaderLog := &stubNodeChannelLog{
		status: channel.ChannelRuntimeStatus{
			ID:     req.ChannelID,
			Leader: 3,
		},
		appendBatchResult: channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{{
			MessageID:  9,
			MessageSeq: 10,
			Message:    req.Message,
		}}},
	}
	New(Options{
		Cluster:     node2,
		Presence:    presence.New(presence.Options{}),
		Online:      online.NewRegistry(),
		LocalNodeID: 2,
		ChannelLog:  redirectLog,
	})
	New(Options{
		Cluster:     node3,
		Presence:    presence.New(presence.Options{}),
		Online:      online.NewRegistry(),
		LocalNodeID: 3,
		ChannelLog:  leaderLog,
	})

	client := NewClient(node1)
	result, err := client.AppendToLeader(context.Background(), 2, req)

	require.NoError(t, err)
	require.Equal(t, channel.AppendResult{MessageID: 9, MessageSeq: 10, Message: req.Message}, result)
	require.Empty(t, redirectLog.appendCalls)
	require.Empty(t, leaderLog.appendCalls)
	require.Equal(t, []channel.AppendBatchRequest{{
		ChannelID:             req.ChannelID,
		Messages:              []channel.Message{req.Message},
		SupportsMessageSeqU64: req.SupportsMessageSeqU64,
		CommitMode:            req.CommitMode,
		ExpectedChannelEpoch:  req.ExpectedChannelEpoch,
		ExpectedLeaderEpoch:   req.ExpectedLeaderEpoch,
		TraceID:               req.TraceID,
		Attempt:               req.Attempt,
	}}, leaderLog.appendBatchCalls)
}

func TestAppendToLeaderRPCReturnsTypedNotLeaderWhenRemoteLeaderChangesBeforeAppend(t *testing.T) {
	req := channel.AppendRequest{
		ChannelID: channel.ChannelID{ID: "u2@u1", Type: frame.ChannelTypePerson},
		Message: channel.Message{
			FromUID:     "u1",
			ClientMsgNo: "m3",
			Payload:     []byte("hi"),
		},
	}
	client := NewClient(remoteErrorCluster{
		err: fmt.Errorf("nodetransport: remote error: %s", channel.ErrNotLeader),
	})
	_, err := client.AppendToLeader(context.Background(), 2, req)

	require.ErrorIs(t, err, channel.ErrNotLeader)
}

func TestAppendToLeaderRPCReturnsTypedWriteFenced(t *testing.T) {
	req := channel.AppendRequest{
		ChannelID: channel.ChannelID{ID: "u2@u1", Type: frame.ChannelTypePerson},
		Message: channel.Message{
			FromUID:     "u1",
			ClientMsgNo: "m-fenced",
			Payload:     []byte("hi"),
		},
	}
	client := NewClient(remoteErrorCluster{
		err: fmt.Errorf("nodetransport: remote error: %s", channel.ErrWriteFenced),
	})
	_, err := client.AppendToLeader(context.Background(), 2, req)

	require.ErrorIs(t, err, channel.ErrWriteFenced)
}

func TestAppendToLeaderRPCLogsRefreshDiagnostics(t *testing.T) {
	req := channel.AppendRequest{
		ChannelID: channel.ChannelID{ID: "u2@u1", Type: frame.ChannelTypePerson},
		Message: channel.Message{
			FromUID:     "u1",
			ClientMsgNo: "m4",
			Payload:     []byte("hi"),
		},
	}
	logger := newNodeRecordingLogger("access.node")
	channelLog := &stubNodeChannelLog{
		status: channel.ChannelRuntimeStatus{
			ID:     req.ChannelID,
			Leader: 2,
		},
		appendReplies: []stubNodeAppendReply{
			{err: channel.ErrNotLeader},
			{result: channel.AppendResult{MessageID: 7, MessageSeq: 8}},
		},
	}
	refresher := &stubNodeMetaRefresher{
		meta: channel.Meta{
			ID:          req.ChannelID,
			Epoch:       4,
			LeaderEpoch: 5,
			Leader:      2,
			Replicas:    []channel.NodeID{1, 2, 3},
			ISR:         []channel.NodeID{1, 2, 3},
			MinISR:      3,
		},
	}
	adapter := New(Options{
		Presence:    presence.New(presence.Options{}),
		Online:      online.NewRegistry(),
		LocalNodeID: 2,
		ChannelLog:  channelLog,
		ChannelMeta: refresher,
		Logger:      logger,
	})
	reqBody, err := encodeChannelAppendRequestBinary(channelAppendRequest{AppendRequest: req})
	require.NoError(t, err)

	_, err = adapter.handleChannelAppendRPC(context.Background(), reqBody)

	require.NoError(t, err)
	entry := requireNodeLogEntry(t, logger, "DEBUG", "access.node.channel_append", "access.node.channel_append.refresh.resolved")
	require.Equal(t, "resolved refreshed channel append metadata", entry.msg)
	require.Equal(t, uint64(2), requireNodeFieldValue[uint64](t, entry, "leaderNodeID"))
	require.Equal(t, uint64(4), requireNodeFieldValue[uint64](t, entry, "channelEpoch"))
	require.Equal(t, uint64(5), requireNodeFieldValue[uint64](t, entry, "leaderEpoch"))
	require.Equal(t, 3, requireNodeFieldValue[int](t, entry, "replicaCount"))
	require.Equal(t, 3, requireNodeFieldValue[int](t, entry, "isrCount"))
	require.Equal(t, 3, requireNodeFieldValue[int](t, entry, "minISR"))
}

type stubNodeChannelLog struct {
	meta              channel.Meta
	status            channel.ChannelRuntimeStatus
	statusErr         error
	appendResult      channel.AppendResult
	appendErr         error
	appendCalls       []channel.AppendRequest
	appendBatchResult channel.AppendBatchResult
	appendBatchErr    error
	appendBatchCalls  []channel.AppendBatchRequest
	appendReplies     []stubNodeAppendReply
}

func (s *stubNodeChannelLog) Status(channel.ChannelID) (channel.ChannelRuntimeStatus, error) {
	return s.status, s.statusErr
}

func (s *stubNodeChannelLog) Fetch(context.Context, channel.FetchRequest) (channel.FetchResult, error) {
	return channel.FetchResult{}, nil
}

func (s *stubNodeChannelLog) Append(_ context.Context, req channel.AppendRequest) (channel.AppendResult, error) {
	s.appendCalls = append(s.appendCalls, req)
	if len(s.appendReplies) > 0 {
		reply := s.appendReplies[0]
		s.appendReplies = s.appendReplies[1:]
		return reply.result, reply.err
	}
	return s.appendResult, s.appendErr
}

func (s *stubNodeChannelLog) AppendBatch(_ context.Context, req channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	s.appendBatchCalls = append(s.appendBatchCalls, req)
	if len(s.appendReplies) > 0 {
		reply := s.appendReplies[0]
		s.appendReplies = s.appendReplies[1:]
		if reply.err != nil {
			return channel.AppendBatchResult{}, reply.err
		}
		msg := channel.Message{}
		if len(req.Messages) > 0 {
			msg = req.Messages[0]
		}
		return channel.AppendBatchResult{Items: []channel.AppendBatchItemResult{{
			MessageID:  reply.result.MessageID,
			MessageSeq: reply.result.MessageSeq,
			Message:    msg,
		}}}, nil
	}
	return s.appendBatchResult, s.appendBatchErr
}

func (s *stubNodeChannelLog) AppendLocalBatch(ctx context.Context, req channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	return s.AppendBatch(ctx, req)
}

func (s *stubNodeChannelLog) MetaSnapshot(channel.ChannelKey) (channel.Meta, bool) {
	return s.meta, s.meta.Key != ""
}

type stubNodeAppendReply struct {
	result channel.AppendResult
	err    error
}

type stubNodeMetaRefresher struct {
	meta channel.Meta
	err  error
}

func (s *stubNodeMetaRefresher) RefreshChannelMeta(context.Context, channel.ChannelID) (channel.Meta, error) {
	return s.meta, s.err
}

type remoteErrorCluster struct {
	err error
}

func (c remoteErrorCluster) RPCMux() *transport.RPCMux { return nil }

func (c remoteErrorCluster) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) {
	return 0, raftcluster.ErrNoLeader
}

func (c remoteErrorCluster) IsLocal(multiraft.NodeID) bool { return false }

func (c remoteErrorCluster) SlotForKey(string) multiraft.SlotID { return 0 }

func (c remoteErrorCluster) RPCService(context.Context, multiraft.NodeID, multiraft.SlotID, uint8, []byte) ([]byte, error) {
	return nil, c.err
}

func (c remoteErrorCluster) PeersForSlot(multiraft.SlotID) []multiraft.NodeID { return nil }
