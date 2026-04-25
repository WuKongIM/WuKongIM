package app

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"
	"unsafe"

	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestChannelLogConversationFactsLoadLatestMessagesBatchesRemoteLoadsByOwner(t *testing.T) {
	remote := &recordingConversationFactsRemote{
		latestByNode: map[uint64]map[channel.ChannelID]channel.Message{
			2: {
				{ID: "g1", Type: 2}: {ChannelID: "g1", ChannelType: 2, MessageSeq: 10},
				{ID: "g2", Type: 2}: {ChannelID: "g2", ChannelType: 2, MessageSeq: 20},
			},
			3: {
				{ID: "g3", Type: 2}: {ChannelID: "g3", ChannelType: 2, MessageSeq: 30},
			},
		},
		singularLatestErr: errors.New("unexpected singular latest load"),
	}
	metas := &staticConversationFactsMetas{
		metas: map[channel.ChannelID]metadb.ChannelRuntimeMeta{
			{ID: "g1", Type: 2}: {ChannelID: "g1", ChannelType: 2, Leader: 2},
			{ID: "g2", Type: 2}: {ChannelID: "g2", ChannelType: 2, Leader: 2},
			{ID: "g3", Type: 2}: {ChannelID: "g3", ChannelType: 2, Leader: 3},
		},
		singularErr: errors.New("unexpected singular meta lookup"),
	}
	facts := channelLogConversationFacts{
		cluster: staleConversationFactsCluster{},
		metas:   metas,
		remote:  remote,
	}

	got, err := facts.LoadLatestMessages(context.Background(), []conversationusecase.ConversationKey{
		{ChannelID: "g1", ChannelType: 2},
		{ChannelID: "g2", ChannelType: 2},
		{ChannelID: "g3", ChannelType: 2},
	})
	require.NoError(t, err)
	require.Equal(t, map[conversationusecase.ConversationKey]channel.Message{
		{ChannelID: "g1", ChannelType: 2}: {ChannelID: "g1", ChannelType: 2, MessageSeq: 10},
		{ChannelID: "g2", ChannelType: 2}: {ChannelID: "g2", ChannelType: 2, MessageSeq: 20},
		{ChannelID: "g3", ChannelType: 2}: {ChannelID: "g3", ChannelType: 2, MessageSeq: 30},
	}, got)
	require.Empty(t, remote.singularLatestCalls)
	require.Equal(t, []conversationFactsBatchCall{
		{NodeID: 2, Keys: []channel.ChannelID{{ID: "g1", Type: 2}, {ID: "g2", Type: 2}}},
		{NodeID: 3, Keys: []channel.ChannelID{{ID: "g3", Type: 2}}},
	}, normalizeConversationFactsBatchCalls(remote.latestBatchCalls))
	require.Equal(t, 1, metas.batchCalls)
}

func TestChannelLogConversationFactsSupportsBatchRecentLoadsByOwner(t *testing.T) {
	remote := &recordingConversationFactsRemote{
		recentsByNode: map[uint64]map[channel.ChannelID][]channel.Message{
			2: {
				{ID: "g1", Type: 2}: {
					{ChannelID: "g1", ChannelType: 2, MessageSeq: 11},
					{ChannelID: "g1", ChannelType: 2, MessageSeq: 10},
				},
				{ID: "g2", Type: 2}: {
					{ChannelID: "g2", ChannelType: 2, MessageSeq: 21},
					{ChannelID: "g2", ChannelType: 2, MessageSeq: 20},
				},
			},
		},
		singularRecentErr: errors.New("unexpected singular recent load"),
	}
	metas := &staticConversationFactsMetas{
		metas: map[channel.ChannelID]metadb.ChannelRuntimeMeta{
			{ID: "g1", Type: 2}: {ChannelID: "g1", ChannelType: 2, Leader: 2},
			{ID: "g2", Type: 2}: {ChannelID: "g2", ChannelType: 2, Leader: 2},
		},
		singularErr: errors.New("unexpected singular meta lookup"),
	}
	facts := channelLogConversationFacts{
		cluster: staleConversationFactsCluster{},
		metas:   metas,
		remote:  remote,
	}

	loader, ok := any(facts).(interface {
		LoadRecentMessagesBatch(context.Context, []conversationusecase.ConversationKey, int) (map[conversationusecase.ConversationKey][]channel.Message, error)
	})
	require.True(t, ok, "channelLogConversationFacts should implement batch recent loading")

	got, err := loader.LoadRecentMessagesBatch(context.Background(), []conversationusecase.ConversationKey{
		{ChannelID: "g1", ChannelType: 2},
		{ChannelID: "g2", ChannelType: 2},
	}, 2)
	require.NoError(t, err)
	require.Equal(t, map[conversationusecase.ConversationKey][]channel.Message{
		{ChannelID: "g1", ChannelType: 2}: {
			{ChannelID: "g1", ChannelType: 2, MessageSeq: 11},
			{ChannelID: "g1", ChannelType: 2, MessageSeq: 10},
		},
		{ChannelID: "g2", ChannelType: 2}: {
			{ChannelID: "g2", ChannelType: 2, MessageSeq: 21},
			{ChannelID: "g2", ChannelType: 2, MessageSeq: 20},
		},
	}, got)
	require.Empty(t, remote.singularRecentCalls)
	require.Equal(t, []conversationFactsBatchCall{
		{NodeID: 2, Keys: []channel.ChannelID{{ID: "g1", Type: 2}, {ID: "g2", Type: 2}}},
	}, normalizeConversationFactsBatchCalls(remote.recentBatchCalls))
	require.Equal(t, 1, metas.batchCalls)
}

func TestDeliveryShardCountForParallelismUsesBoundedFanout(t *testing.T) {
	require.Equal(t, 4, deliveryShardCountForParallelism(1))
	require.Equal(t, 4, deliveryShardCountForParallelism(4))
	require.Equal(t, 8, deliveryShardCountForParallelism(8))
	require.Equal(t, 16, deliveryShardCountForParallelism(64))
}

func TestLoadLatestConversationMessageTreatsNotReadyAsEmpty(t *testing.T) {
	msg, ok, err := loadLatestConversationMessage(context.Background(), notReadyConversationFactsCluster{}, channel.ChannelID{ID: "g1", Type: 2}, 1024)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, channel.Message{}, msg)
}

func TestLoadRecentConversationMessagesTreatsNotReadyAsEmpty(t *testing.T) {
	msgs, err := loadRecentConversationMessages(context.Background(), notReadyConversationFactsCluster{}, channel.ChannelID{ID: "g1", Type: 2}, 10, 1024)
	require.NoError(t, err)
	require.Nil(t, msgs)
}

func TestBuildLongPollForwardsReplicationSettingsIntoChannelConfigs(t *testing.T) {
	cfg := testConfig(t)
	cfg.Cluster.LongPollLaneCount = 16
	cfg.Cluster.LongPollMaxWait = 2 * time.Millisecond
	cfg.Cluster.LongPollMaxBytes = 128 * 1024
	cfg.Cluster.LongPollMaxChannels = 32

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	require.Equal(t, channel.Config{
		LongPollLaneCount:   16,
		LongPollMaxWait:     2 * time.Millisecond,
		LongPollMaxBytes:    128 * 1024,
		LongPollMaxChannels: 32,
	}, cfg.Cluster.replicationConfig())
	require.Equal(t, 16, appRuntimeIntField(t, app.isrRuntime, "cfg", "LongPollLaneCount"))
	require.Equal(t, 2*time.Millisecond, appRuntimeDurationField(t, app.isrRuntime, "cfg", "LongPollMaxWait"))
	require.Equal(t, 128*1024, appRuntimeIntField(t, app.isrRuntime, "cfg", "LongPollMaxBytes"))
	require.Equal(t, 32, appRuntimeIntField(t, app.isrRuntime, "cfg", "LongPollMaxChannels"))
	require.Equal(t, 16, appTransportIntField(t, app.isrTransport, "longPollLaneCount"))
	require.Equal(t, 2*time.Millisecond, appTransportDurationField(t, app.isrTransport, "longPollMaxWait"))
	require.Equal(t, 128*1024, appTransportIntField(t, app.isrTransport, "longPollMaxBytes"))
	require.Equal(t, 32, appTransportIntField(t, app.isrTransport, "longPollMaxChannels"))
}

func TestBuildForwardsGatewaySendTimeoutIntoHandler(t *testing.T) {
	cfg := testConfig(t)
	cfg.Gateway.SendTimeout = 27 * time.Second

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	require.Equal(t, 27*time.Second, appGatewayHandlerDurationField(t, app.GatewayHandler(), "sendTimeout"))
}

type staleConversationFactsCluster struct{}

func (staleConversationFactsCluster) Status(channel.ChannelID) (channel.ChannelRuntimeStatus, error) {
	return channel.ChannelRuntimeStatus{}, channel.ErrStaleMeta
}

func (staleConversationFactsCluster) Fetch(context.Context, channel.FetchRequest) (channel.FetchResult, error) {
	return channel.FetchResult{}, channel.ErrStaleMeta
}

type notReadyConversationFactsCluster struct{}

func (notReadyConversationFactsCluster) Status(channel.ChannelID) (channel.ChannelRuntimeStatus, error) {
	return channel.ChannelRuntimeStatus{}, channel.ErrNotReady
}

func (notReadyConversationFactsCluster) Fetch(context.Context, channel.FetchRequest) (channel.FetchResult, error) {
	return channel.FetchResult{}, channel.ErrNotReady
}

type staticConversationFactsMetas struct {
	metas       map[channel.ChannelID]metadb.ChannelRuntimeMeta
	singularErr error
	batchCalls  int
}

func (s *staticConversationFactsMetas) GetChannelRuntimeMeta(_ context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	if s.singularErr != nil {
		return metadb.ChannelRuntimeMeta{}, s.singularErr
	}
	meta, ok := s.metas[channel.ChannelID{ID: channelID, Type: uint8(channelType)}]
	if !ok {
		return metadb.ChannelRuntimeMeta{}, metadb.ErrNotFound
	}
	return meta, nil
}

func (s *staticConversationFactsMetas) BatchGetChannelRuntimeMetas(_ context.Context, keys []metadb.ConversationKey) (map[metadb.ConversationKey]metadb.ChannelRuntimeMeta, error) {
	s.batchCalls++
	out := make(map[metadb.ConversationKey]metadb.ChannelRuntimeMeta, len(keys))
	for _, key := range keys {
		meta, ok := s.metas[channel.ChannelID{ID: key.ChannelID, Type: uint8(key.ChannelType)}]
		if ok {
			out[key] = meta
		}
	}
	return out, nil
}

type conversationFactsBatchCall struct {
	NodeID uint64
	Keys   []channel.ChannelID
}

type recordingConversationFactsRemote struct {
	latestByNode        map[uint64]map[channel.ChannelID]channel.Message
	recentsByNode       map[uint64]map[channel.ChannelID][]channel.Message
	latestBatchCalls    []conversationFactsBatchCall
	recentBatchCalls    []conversationFactsBatchCall
	singularLatestCalls []channel.ChannelID
	singularRecentCalls []channel.ChannelID
	singularLatestErr   error
	singularRecentErr   error
}

func (r *recordingConversationFactsRemote) LoadLatestConversationMessage(_ context.Context, _ uint64, key channel.ChannelID, _ int) (channel.Message, bool, error) {
	r.singularLatestCalls = append(r.singularLatestCalls, key)
	if r.singularLatestErr != nil {
		return channel.Message{}, false, r.singularLatestErr
	}
	return channel.Message{}, false, nil
}

func (r *recordingConversationFactsRemote) LoadRecentConversationMessages(_ context.Context, _ uint64, key channel.ChannelID, _ int, _ int) ([]channel.Message, error) {
	r.singularRecentCalls = append(r.singularRecentCalls, key)
	if r.singularRecentErr != nil {
		return nil, r.singularRecentErr
	}
	return nil, nil
}

func (r *recordingConversationFactsRemote) LoadLatestConversationMessages(_ context.Context, nodeID uint64, keys []channel.ChannelID, _ int) (map[channel.ChannelID]channel.Message, error) {
	r.latestBatchCalls = append(r.latestBatchCalls, conversationFactsBatchCall{
		NodeID: nodeID,
		Keys:   append([]channel.ChannelID(nil), keys...),
	})
	out := make(map[channel.ChannelID]channel.Message, len(keys))
	for _, key := range keys {
		if msg, ok := r.latestByNode[nodeID][key]; ok {
			out[key] = msg
		}
	}
	return out, nil
}

func (r *recordingConversationFactsRemote) LoadRecentConversationMessagesBatch(_ context.Context, nodeID uint64, keys []channel.ChannelID, limit, _ int) (map[channel.ChannelID][]channel.Message, error) {
	r.recentBatchCalls = append(r.recentBatchCalls, conversationFactsBatchCall{
		NodeID: nodeID,
		Keys:   append([]channel.ChannelID(nil), keys...),
	})
	out := make(map[channel.ChannelID][]channel.Message, len(keys))
	for _, key := range keys {
		msgs := append([]channel.Message(nil), r.recentsByNode[nodeID][key]...)
		if limit > 0 && len(msgs) > limit {
			msgs = msgs[:limit]
		}
		out[key] = msgs
	}
	return out, nil
}

func normalizeConversationFactsBatchCalls(calls []conversationFactsBatchCall) []conversationFactsBatchCall {
	out := make([]conversationFactsBatchCall, 0, len(calls))
	for _, call := range calls {
		keys := append([]channel.ChannelID(nil), call.Keys...)
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].Type != keys[j].Type {
				return keys[i].Type < keys[j].Type
			}
			return keys[i].ID < keys[j].ID
		})
		out = append(out, conversationFactsBatchCall{
			NodeID: call.NodeID,
			Keys:   keys,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].NodeID < out[j].NodeID
	})
	return out
}

func appRuntimeStringField(t *testing.T, appRuntime any, structField, name string) string {
	t.Helper()

	value := reflect.ValueOf(appRuntime)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		t.Fatalf("runtime is %s, want non-nil pointer", value.Kind())
	}
	cfgField := value.Elem().FieldByName(structField)
	if !cfgField.IsValid() {
		t.Fatalf("runtime missing %s field", structField)
	}
	cfg := reflect.NewAt(cfgField.Type(), unsafe.Pointer(cfgField.UnsafeAddr())).Elem()
	field := cfg.FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("runtime config missing %s field", name)
	}
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().String()
}

func appRuntimeIntField(t *testing.T, appRuntime any, structField, name string) int {
	t.Helper()

	value := reflect.ValueOf(appRuntime)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		t.Fatalf("runtime is %s, want non-nil pointer", value.Kind())
	}
	cfgField := value.Elem().FieldByName(structField)
	if !cfgField.IsValid() {
		t.Fatalf("runtime missing %s field", structField)
	}
	cfg := reflect.NewAt(cfgField.Type(), unsafe.Pointer(cfgField.UnsafeAddr())).Elem()
	field := cfg.FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("runtime config missing %s field", name)
	}
	return int(reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Int())
}

func appRuntimeDurationField(t *testing.T, appRuntime any, structField, name string) time.Duration {
	t.Helper()

	value := reflect.ValueOf(appRuntime)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		t.Fatalf("runtime is %s, want non-nil pointer", value.Kind())
	}
	cfgField := value.Elem().FieldByName(structField)
	if !cfgField.IsValid() {
		t.Fatalf("runtime missing %s field", structField)
	}
	cfg := reflect.NewAt(cfgField.Type(), unsafe.Pointer(cfgField.UnsafeAddr())).Elem()
	field := cfg.FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("runtime config missing %s field", name)
	}
	return time.Duration(reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Int())
}

func appTransportStringField(t *testing.T, transport any, name string) string {
	t.Helper()

	value := reflect.ValueOf(transport)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		t.Fatalf("transport is %s, want non-nil pointer", value.Kind())
	}
	field := value.Elem().FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("transport missing %s field", name)
	}
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().String()
}

func appTransportIntField(t *testing.T, transport any, name string) int {
	t.Helper()

	value := reflect.ValueOf(transport)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		t.Fatalf("transport is %s, want non-nil pointer", value.Kind())
	}
	field := value.Elem().FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("transport missing %s field", name)
	}
	return int(reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Int())
}

func appTransportDurationField(t *testing.T, transport any, name string) time.Duration {
	t.Helper()

	value := reflect.ValueOf(transport)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		t.Fatalf("transport is %s, want non-nil pointer", value.Kind())
	}
	field := value.Elem().FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("transport missing %s field", name)
	}
	return time.Duration(reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Int())
}

func appGatewayHandlerDurationField(t *testing.T, handler any, name string) time.Duration {
	t.Helper()

	value := reflect.ValueOf(handler)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		t.Fatalf("gateway handler is %s, want non-nil pointer", value.Kind())
	}
	field := value.Elem().FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("gateway handler missing %s field", name)
	}
	return time.Duration(reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Int())
}
