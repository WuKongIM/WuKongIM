package node

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
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
		appendResult: channel.AppendResult{MessageID: 7, MessageSeq: 8},
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
	require.Equal(t, channel.AppendResult{MessageID: 7, MessageSeq: 8}, result)
	require.Equal(t, []channel.AppendRequest{req}, channelLog.appendCalls)
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
		appendResult: channel.AppendResult{MessageID: 9, MessageSeq: 10},
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
	require.Equal(t, channel.AppendResult{MessageID: 9, MessageSeq: 10}, result)
	require.Empty(t, redirectLog.appendCalls)
	require.Equal(t, []channel.AppendRequest{req}, leaderLog.appendCalls)
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
	reqBody, err := json.Marshal(channelAppendRequest{AppendRequest: req})
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
	status        channel.ChannelRuntimeStatus
	statusErr     error
	appendResult  channel.AppendResult
	appendErr     error
	appendCalls   []channel.AppendRequest
	appendReplies []stubNodeAppendReply
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
