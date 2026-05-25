package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"unsafe"

	"github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestCMDSyncMessageStoreLocalReadsCommandMessagesFromSeq(t *testing.T) {
	id := channel.ChannelID{ID: channelid.ToCommandChannel("g-local"), Type: frame.ChannelTypeGroup}
	engine := openManagerMessageTestEngine(t)
	appendManagerMessageTestRows(t, engine, id, 4)

	store := cmdsyncMessageStore{
		localNodeID: 1,
		channelLog:  engine,
		metas: cmdsyncMessageMetasFake{meta: metadb.ChannelRuntimeMeta{
			ChannelID:   id.ID,
			ChannelType: int64(id.Type),
			Leader:      1,
		}},
	}

	messages, err := store.LoadCommandMessages(context.Background(), cmdsync.CommandChannelKey{ChannelID: id.ID, ChannelType: id.Type}, 3, 10)
	require.NoError(t, err)
	require.Equal(t, []uint64{3, 4}, cmdsyncMessageSeqs(messages))
	require.Equal(t, id.ID, messages[0].ChannelID)
}

func TestCMDSyncMessageStoreRemoteReadsCommandOwnerWithSyncMode(t *testing.T) {
	id := channel.ChannelID{ID: channelid.ToCommandChannel("g-remote"), Type: frame.ChannelTypeGroup}
	remote := &cmdsyncMessageRemoteCapture{page: node.ChannelMessagesPage{Messages: []channel.Message{{MessageSeq: 7, ChannelID: id.ID, ChannelType: id.Type}}}}
	store := cmdsyncMessageStore{
		localNodeID: 1,
		metas: cmdsyncMessageMetasFake{meta: metadb.ChannelRuntimeMeta{
			ChannelID:           id.ID,
			ChannelType:         int64(id.Type),
			Leader:              9,
			RetentionThroughSeq: 4,
		}},
		remote: remote,
	}

	messages, err := store.LoadCommandMessages(context.Background(), cmdsync.CommandChannelKey{ChannelID: id.ID, ChannelType: id.Type}, 5, 20)
	require.NoError(t, err)
	require.Equal(t, []uint64{7}, cmdsyncMessageSeqs(messages))
	require.Equal(t, []uint64{9}, remote.nodeIDs)
	require.Len(t, remote.requests, 1)
	require.Equal(t, id, remote.requests[0].ChannelID)
	require.True(t, remote.requests[0].SyncMode)
	require.Equal(t, uint64(5), remote.requests[0].StartSeq)
	require.Equal(t, 20, remote.requests[0].Limit)
	require.Equal(t, uint8(channelhandler.SyncPullModeUp), remote.requests[0].PullMode)
	require.Equal(t, uint64(5), remote.requests[0].MinAvailableSeq)
}

func TestCMDSyncMessageStoreReturnsEmptyWhenCommandLogUnavailable(t *testing.T) {
	id := channel.ChannelID{ID: channelid.ToCommandChannel("g-missing"), Type: frame.ChannelTypeGroup}
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "not ready", err: channel.ErrNotReady},
		{name: "not found", err: channel.ErrChannelNotFound},
		{name: "remote not ready text", err: errors.New("rpc failed: channel: not ready")},
		{name: "remote not found text", err: errors.New("rpc failed: channel: channel not found")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := cmdsyncMessageStore{
				localNodeID: 1,
				metas: cmdsyncMessageMetasFake{meta: metadb.ChannelRuntimeMeta{
					ChannelID:   id.ID,
					ChannelType: int64(id.Type),
					Leader:      9,
				}, err: nil},
				remote: &cmdsyncMessageRemoteCapture{err: tc.err},
			}

			messages, err := store.LoadCommandMessages(context.Background(), cmdsync.CommandChannelKey{ChannelID: id.ID, ChannelType: id.Type}, 1, 10)
			require.NoError(t, err)
			require.Empty(t, messages)
		})
	}
}

func TestNewBuildsCMDSyncRuntimeAndCommittedFanout(t *testing.T) {
	cfg := testConfig(t)
	cfg.API.ListenAddr = "127.0.0.1:0"

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, app.Stop()) })

	require.NotNil(t, app.cmdSyncApp)
	require.NotNil(t, app.cmdConversationUpdater)

	dispatcher := messageAppDispatcherForTest(t, app)
	fanout, ok := dispatcher.(committedFanout)
	require.Truef(t, ok, "message dispatcher should be committedFanout, got %T", dispatcher)
	require.Len(t, fanout.subscribers, 2)
	require.Same(t, app.committedDispatcher, fanout.subscribers[0])
	require.Same(t, app.committedReplayer, fanout.subscribers[1])

	cmdPending, ok := unexportedFieldForTest(t, app.cmdSyncApp, "pending").(*cmdsync.ConversationUpdater)
	require.True(t, ok)
	require.Same(t, app.cmdConversationUpdater, cmdPending)

	messageSink, ok := unexportedFieldForTest(t, app.messageApp, "cmdConversationIntents").(cmdConversationIntentRouter)
	require.True(t, ok)
	require.Same(t, app.cmdConversationUpdater, messageSink.local)
	require.Same(t, app.nodeClient, messageSink.remote)
	require.Same(t, app.cluster, messageSink.cluster)
	require.Equal(t, cfg.Node.ID, messageSink.localNodeID)

	nodeCMDSync, ok := unexportedFieldForTest(t, app.nodeAccess, "cmdSync").(*cmdsync.App)
	require.True(t, ok)
	require.Same(t, app.cmdSyncApp, nodeCMDSync)
	nodeIntentSink, ok := unexportedFieldForTest(t, app.nodeAccess, "cmdConversationIntents").(ownerValidatingCMDIntentSink)
	require.True(t, ok)
	require.Same(t, app.cmdConversationUpdater, nodeIntentSink.local)
	require.Same(t, app.cluster, nodeIntentSink.cluster)
	require.Equal(t, cfg.Node.ID, nodeIntentSink.localNodeID)

	require.NotNil(t, app.api)
	apiCMDSync, ok := unexportedFieldForTest(t, app.api, "cmdSync").(clusterCMDSyncUsecase)
	require.True(t, ok)
	require.Same(t, app.cmdSyncApp, apiCMDSync.local)
	require.Same(t, app.nodeClient, apiCMDSync.remote)
	require.Same(t, app.cluster, apiCMDSync.cluster)
	require.Equal(t, cfg.Node.ID, apiCMDSync.localNodeID)
}

func TestClusterCMDSyncUsecaseRoutesLocalOwner(t *testing.T) {
	commandChannelID := channelid.ToCommandChannel("g-local")
	states := &cmdsyncStateStoreFake{
		active: []metadb.CMDConversationState{{
			UID: "u1", ChannelID: commandChannelID, ChannelType: int64(frame.ChannelTypeGroup), ActiveAt: 100,
		}},
	}
	messages := &cmdsyncMessageStoreFake{
		byKey: map[cmdsync.CommandChannelKey][]channel.Message{
			{ChannelID: commandChannelID, ChannelType: frame.ChannelTypeGroup}: {{
				MessageSeq: 7, ChannelID: commandChannelID, ChannelType: frame.ChannelTypeGroup,
			}},
		},
	}
	remote := &cmdsyncRemoteCapture{}
	uc := clusterCMDSyncUsecase{
		local:       cmdsync.New(cmdsync.Options{States: states, Messages: messages}),
		remote:      remote,
		cluster:     &cmdsyncRoutingClusterFake{slot: 3, leader: 1},
		localNodeID: 1,
	}

	result, err := uc.Sync(context.Background(), cmdsync.SyncQuery{UID: " u1 ", Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []channel.Message{{MessageSeq: 7, ChannelID: "g-local", ChannelType: frame.ChannelTypeGroup}}, result.Messages)
	require.Equal(t, []string{"u1"}, states.listUIDs)
	require.Empty(t, remote.syncQueries)

	require.NoError(t, uc.SyncAck(context.Background(), cmdsync.SyncAckCommand{UID: " u1 ", LastMessageSeq: 7}))
	require.Equal(t, []metadb.CMDConversationReadPatch{{
		UID: "u1", ChannelID: commandChannelID, ChannelType: int64(frame.ChannelTypeGroup), ReadSeq: 7,
	}}, stripCMDPatchTimes(states.patches))
	require.Empty(t, remote.acks)
}

func TestClusterCMDSyncUsecaseRoutesRemoteOwner(t *testing.T) {
	remote := &cmdsyncRemoteCapture{
		syncResult: cmdsync.SyncResult{Messages: []channel.Message{{MessageSeq: 9, ChannelID: "g-remote", ChannelType: frame.ChannelTypeGroup}}},
	}
	uc := clusterCMDSyncUsecase{
		local:       cmdsync.New(cmdsync.Options{States: &cmdsyncStateStoreFake{}, Messages: &cmdsyncMessageStoreFake{}}),
		remote:      remote,
		cluster:     &cmdsyncRoutingClusterFake{slot: 5, leader: 9},
		localNodeID: 1,
	}

	result, err := uc.Sync(context.Background(), cmdsync.SyncQuery{UID: " u2 ", MessageSeq: 3, Limit: 20})
	require.NoError(t, err)
	require.Equal(t, []channel.Message{{MessageSeq: 9, ChannelID: "g-remote", ChannelType: frame.ChannelTypeGroup}}, result.Messages)
	require.Equal(t, []uint64{9}, remote.syncNodeIDs)
	require.Equal(t, []cmdsync.SyncQuery{{UID: "u2", MessageSeq: 3, Limit: 20}}, remote.syncQueries)

	require.NoError(t, uc.SyncAck(context.Background(), cmdsync.SyncAckCommand{UID: " u2 ", LastMessageSeq: 9}))
	require.Equal(t, []uint64{9}, remote.ackNodeIDs)
	require.Equal(t, []cmdsync.SyncAckCommand{{UID: "u2", LastMessageSeq: 9}}, remote.acks)
}

func TestClusterCMDSyncUsecaseRejectsBlankUIDBeforeRouting(t *testing.T) {
	cluster := &cmdsyncRoutingClusterFake{slot: 5, leader: 9}
	remote := &cmdsyncRemoteCapture{}
	uc := clusterCMDSyncUsecase{
		local:       cmdsync.New(cmdsync.Options{States: &cmdsyncStateStoreFake{}, Messages: &cmdsyncMessageStoreFake{}}),
		remote:      remote,
		cluster:     cluster,
		localNodeID: 1,
	}

	_, err := uc.Sync(context.Background(), cmdsync.SyncQuery{UID: "   ", Limit: 1})
	require.ErrorIs(t, err, cmdsync.ErrUIDRequired)
	require.ErrorIs(t, uc.SyncAck(context.Background(), cmdsync.SyncAckCommand{UID: "   ", LastMessageSeq: 1}), cmdsync.ErrUIDRequired)
	require.Empty(t, cluster.slotKeys)
	require.Empty(t, remote.syncQueries)
	require.Empty(t, remote.acks)
}

func TestCMDSyncPendingIntentSyncAckDurableRoundTrip(t *testing.T) {
	ctx := context.Background()
	commandChannelID := channelid.ToCommandChannel("g1")
	store := newDurableCMDStateStore()
	updater := cmdsync.NewConversationUpdater(cmdsync.ConversationUpdaterOptions{Store: store})
	msg := channel.Message{
		ChannelID: commandChannelID, ChannelType: frame.ChannelTypeGroup, FromUID: "u1",
		MessageSeq: 7, Timestamp: 100, Framer: frame.Framer{SyncOnce: true},
	}

	intent, ok := cmdsync.BuildConversationIntent(msg, []string{"u1", "u2"}, nil)
	require.True(t, ok)
	require.NoError(t, updater.PushIntent(ctx, intent))
	require.Equal(t, uint64(7), updater.ListPending(ctx, "u1", 10)[0].ReadSeq)
	require.Zero(t, updater.ListPending(ctx, "u2", 10)[0].ReadSeq)

	app := cmdsync.New(cmdsync.Options{
		States:  store,
		Pending: updater,
		Messages: &cmdsyncMessageStoreFake{byKey: map[cmdsync.CommandChannelKey][]channel.Message{
			{ChannelID: commandChannelID, ChannelType: frame.ChannelTypeGroup}: {msg},
		}},
	})
	result, err := app.Sync(ctx, cmdsync.SyncQuery{UID: "u2", Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []channel.Message{{
		ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, FromUID: "u1", MessageSeq: 7, Timestamp: 100, Framer: frame.Framer{SyncOnce: true},
	}}, result.Messages)

	require.NoError(t, app.SyncAck(ctx, cmdsync.SyncAckCommand{UID: "u2", LastMessageSeq: 7}))
	require.Equal(t, uint64(7), store.state("u2", commandChannelID, frame.ChannelTypeGroup).ReadSeq)
	require.Empty(t, updater.ListPending(ctx, "u2", 10))

	result, err = app.Sync(ctx, cmdsync.SyncQuery{UID: "u2", Limit: 10})
	require.NoError(t, err)
	require.Empty(t, result.Messages)
}

func TestClusterCMDSyncUsecaseKeepsUIDOwnerSeparateFromCommandOwner(t *testing.T) {
	commandChannelID := channelid.ToCommandChannel("g-split")
	states := &cmdsyncStateStoreFake{
		active: []metadb.CMDConversationState{{
			UID: "u1", ChannelID: commandChannelID, ChannelType: int64(frame.ChannelTypeGroup), ActiveAt: 100,
		}},
	}
	commandOwner := &cmdsyncMessageRemoteCapture{
		page: node.ChannelMessagesPage{Messages: []channel.Message{{
			MessageSeq: 11, ChannelID: commandChannelID, ChannelType: frame.ChannelTypeGroup,
		}}},
	}
	uidOwnerRemote := &cmdsyncRemoteCapture{}
	uc := clusterCMDSyncUsecase{
		local: cmdsync.New(cmdsync.Options{
			States: states,
			Messages: cmdsyncMessageStore{
				localNodeID: 1,
				metas: cmdsyncMessageMetasFake{meta: metadb.ChannelRuntimeMeta{
					ChannelID: commandChannelID, ChannelType: int64(frame.ChannelTypeGroup), Leader: 7,
				}},
				remote: commandOwner,
			},
		}),
		remote:      uidOwnerRemote,
		cluster:     &cmdsyncRoutingClusterFake{slot: 4, leader: 1},
		localNodeID: 1,
	}

	result, err := uc.Sync(context.Background(), cmdsync.SyncQuery{UID: "u1", Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []channel.Message{{MessageSeq: 11, ChannelID: "g-split", ChannelType: frame.ChannelTypeGroup}}, result.Messages)
	require.Empty(t, uidOwnerRemote.syncQueries)
	require.Equal(t, []uint64{7}, commandOwner.nodeIDs)
	require.Equal(t, commandChannelID, commandOwner.requests[0].ChannelID.ID)
}

type cmdsyncMessageMetasFake struct {
	meta metadb.ChannelRuntimeMeta
	err  error
}

func (f cmdsyncMessageMetasFake) GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error) {
	return f.meta, f.err
}

type cmdsyncMessageRemoteCapture struct {
	nodeIDs  []uint64
	requests []node.ChannelMessagesQuery
	page     node.ChannelMessagesPage
	err      error
}

func (f *cmdsyncMessageRemoteCapture) QueryChannelMessages(_ context.Context, nodeID uint64, req node.ChannelMessagesQuery) (node.ChannelMessagesPage, error) {
	f.nodeIDs = append(f.nodeIDs, nodeID)
	f.requests = append(f.requests, req)
	if f.err != nil {
		return node.ChannelMessagesPage{}, f.err
	}
	return f.page, nil
}

func cmdsyncMessageSeqs(messages []channel.Message) []uint64 {
	seqs := make([]uint64, 0, len(messages))
	for _, msg := range messages {
		seqs = append(seqs, msg.MessageSeq)
	}
	return seqs
}

func messageAppDispatcherForTest(t *testing.T, app *App) any {
	t.Helper()
	require.NotNil(t, app.messageApp)
	field := reflect.ValueOf(app.messageApp).Elem().FieldByName("dispatcher")
	if !field.IsValid() {
		t.Fatal("message.App is missing dispatcher field")
	}
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Interface()
}

func unexportedFieldForTest(t *testing.T, target any, name string) any {
	t.Helper()
	value := reflect.ValueOf(target)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	field := value.FieldByName(name)
	require.Truef(t, field.IsValid(), "%T is missing field %s", target, name)
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Interface()
}

type cmdsyncRoutingClusterFake struct {
	slot        multiraft.SlotID
	leader      multiraft.NodeID
	err         error
	slotKeys    []string
	leaderSlots []multiraft.SlotID
}

func (f *cmdsyncRoutingClusterFake) SlotForKey(key string) multiraft.SlotID {
	f.slotKeys = append(f.slotKeys, key)
	return f.slot
}

func (f *cmdsyncRoutingClusterFake) LeaderOf(slotID multiraft.SlotID) (multiraft.NodeID, error) {
	f.leaderSlots = append(f.leaderSlots, slotID)
	return f.leader, f.err
}

type cmdsyncRemoteCapture struct {
	syncNodeIDs []uint64
	syncQueries []cmdsync.SyncQuery
	syncResult  cmdsync.SyncResult
	syncErr     error
	ackNodeIDs  []uint64
	acks        []cmdsync.SyncAckCommand
	ackErr      error
}

func (r *cmdsyncRemoteCapture) SyncCMD(_ context.Context, nodeID uint64, query cmdsync.SyncQuery) (cmdsync.SyncResult, error) {
	r.syncNodeIDs = append(r.syncNodeIDs, nodeID)
	r.syncQueries = append(r.syncQueries, query)
	return r.syncResult, r.syncErr
}

func (r *cmdsyncRemoteCapture) SyncAckCMD(_ context.Context, nodeID uint64, cmd cmdsync.SyncAckCommand) error {
	r.ackNodeIDs = append(r.ackNodeIDs, nodeID)
	r.acks = append(r.acks, cmd)
	return r.ackErr
}

type cmdsyncStateStoreFake struct {
	active   []metadb.CMDConversationState
	listUIDs []string
	patches  []metadb.CMDConversationReadPatch
	upserts  []metadb.CMDConversationState
}

func (s *cmdsyncStateStoreFake) ListCMDConversationActive(_ context.Context, uid string, limit int) ([]metadb.CMDConversationState, error) {
	s.listUIDs = append(s.listUIDs, uid)
	out := make([]metadb.CMDConversationState, 0, len(s.active))
	for _, state := range s.active {
		if state.UID != uid {
			continue
		}
		out = append(out, state)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *cmdsyncStateStoreFake) AdvanceCMDConversationReadSeq(_ context.Context, patches []metadb.CMDConversationReadPatch) error {
	s.patches = append(s.patches, patches...)
	return nil
}

func (s *cmdsyncStateStoreFake) UpsertCMDConversationStates(_ context.Context, states []metadb.CMDConversationState) error {
	s.upserts = append(s.upserts, states...)
	return nil
}

type cmdsyncMessageStoreFake struct {
	byKey map[cmdsync.CommandChannelKey][]channel.Message
}

func (s *cmdsyncMessageStoreFake) LoadCommandMessages(_ context.Context, key cmdsync.CommandChannelKey, fromSeq uint64, limit int) ([]channel.Message, error) {
	out := make([]channel.Message, 0, len(s.byKey[key]))
	for _, msg := range s.byKey[key] {
		if msg.MessageSeq < fromSeq {
			continue
		}
		out = append(out, msg)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

type durableCMDStateStore struct {
	rows map[cmdsyncDurableStateKey]metadb.CMDConversationState
}

func newDurableCMDStateStore() *durableCMDStateStore {
	return &durableCMDStateStore{rows: make(map[cmdsyncDurableStateKey]metadb.CMDConversationState)}
}

func (s *durableCMDStateStore) UpsertCMDConversationStates(_ context.Context, states []metadb.CMDConversationState) error {
	for _, state := range states {
		key := cmdsyncDurableStateKey{uid: state.UID, channelID: state.ChannelID, channelType: state.ChannelType}
		existing := s.rows[key]
		if state.ReadSeq < existing.ReadSeq {
			state.ReadSeq = existing.ReadSeq
		}
		s.rows[key] = state
	}
	return nil
}

func (s *durableCMDStateStore) ListCMDConversationActive(_ context.Context, uid string, limit int) ([]metadb.CMDConversationState, error) {
	out := make([]metadb.CMDConversationState, 0, len(s.rows))
	for _, state := range s.rows {
		if state.UID != uid {
			continue
		}
		out = append(out, state)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *durableCMDStateStore) AdvanceCMDConversationReadSeq(_ context.Context, patches []metadb.CMDConversationReadPatch) error {
	for _, patch := range patches {
		key := cmdsyncDurableStateKey{uid: patch.UID, channelID: patch.ChannelID, channelType: patch.ChannelType}
		state := s.rows[key]
		if patch.ReadSeq > state.ReadSeq {
			state.ReadSeq = patch.ReadSeq
		}
		state.UpdatedAt = patch.UpdatedAt
		s.rows[key] = state
	}
	return nil
}

func (s *durableCMDStateStore) state(uid, channelID string, channelType uint8) metadb.CMDConversationState {
	return s.rows[cmdsyncDurableStateKey{uid: uid, channelID: channelID, channelType: int64(channelType)}]
}

type cmdsyncDurableStateKey struct {
	uid         string
	channelID   string
	channelType int64
}

func stripCMDPatchTimes(patches []metadb.CMDConversationReadPatch) []metadb.CMDConversationReadPatch {
	out := append([]metadb.CMDConversationReadPatch(nil), patches...)
	for i := range out {
		out[i].UpdatedAt = 0
	}
	return out
}
