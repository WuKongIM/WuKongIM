package conversation

import (
	"context"
	"errors"
	"testing"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestSyncPositiveVersionDoesNotScanFullUserDirectory(t *testing.T) {
	repo := newConversationSyncRepoStub()
	repo.active = []metadb.UserConversationState{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ReadSeq: 1, ActiveAt: 100},
	}
	repo.states[metadbKey("g1", 2)] = repo.active[0]
	repo.latest[key("g1", 2)] = testMessage("g1", 2, 2, "u2", 100, "c1")
	repo.latest[key("historical", 2)] = testMessage("historical", 2, 9, "u2", 200, "hist")

	app := New(Options{States: repo, Facts: repo})
	got, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Version: 123, Limit: 10})

	require.NoError(t, err)
	require.Equal(t, []SyncConversation{{
		ChannelID: "g1", ChannelType: 2, Unread: 1, Timestamp: 100, LastMsgSeq: 2, LastClientMsgNo: "c1", ReadToMsgSeq: 1, Version: time.Unix(100, 0).UnixNano(),
	}}, got.Conversations)
	require.Zero(t, repo.scanCalls)
}

func TestSyncUsesActiveSetAndClientKnownConversations(t *testing.T) {
	repo := newConversationSyncRepoStub()
	repo.active = []metadb.UserConversationState{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ReadSeq: 5, ActiveAt: 100},
	}
	repo.states[metadbKey("g1", 2)] = repo.active[0]
	repo.latest[key("g1", 2)] = testMessage("g1", 2, 12, "u2", 150, "c1")
	repo.latest[key("g2", 2)] = testMessage("g2", 2, 8, "u3", 200, "c2")

	app := New(Options{States: repo, Facts: repo})
	got, err := app.Sync(context.Background(), SyncQuery{
		UID:         "u1",
		LastMsgSeqs: map[ConversationKey]uint64{key("g2", 2): 3},
		Limit:       10,
	})

	require.NoError(t, err)
	require.Equal(t, []SyncConversation{
		{ChannelID: "g2", ChannelType: 2, Unread: 8, Timestamp: 200, LastMsgSeq: 8, LastClientMsgNo: "c2", ReadToMsgSeq: 0, Version: time.Unix(200, 0).UnixNano()},
		{ChannelID: "g1", ChannelType: 2, Unread: 7, Timestamp: 150, LastMsgSeq: 12, LastClientMsgNo: "c1", ReadToMsgSeq: 5, Version: time.Unix(150, 0).UnixNano()},
	}, got.Conversations)
	require.ElementsMatch(t, []ConversationKey{key("g1", 2), key("g2", 2)}, repo.latestLoads)
	require.Zero(t, repo.scanCalls)
}

func TestSyncDeletedConversationReappearsForNewerLatestMessage(t *testing.T) {
	repo := newConversationSyncRepoStub()
	repo.active = []metadb.UserConversationState{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, DeletedToSeq: 10, ActiveAt: 100},
	}
	repo.states[metadbKey("g1", 2)] = repo.active[0]
	repo.latest[key("g1", 2)] = testMessage("g1", 2, 11, "u2", 101, "new")

	app := New(Options{States: repo, Facts: repo})
	got, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 10})

	require.NoError(t, err)
	require.Equal(t, []SyncConversation{{
		ChannelID: "g1", ChannelType: 2, Unread: 1, Timestamp: 101, LastMsgSeq: 11, LastClientMsgNo: "new", ReadToMsgSeq: 10, Version: time.Unix(101, 0).UnixNano(),
	}}, got.Conversations)
}

func TestSyncDeletedConversationHiddenWhenLatestAtDeleteBarrier(t *testing.T) {
	repo := newConversationSyncRepoStub()
	repo.active = []metadb.UserConversationState{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, DeletedToSeq: 10, ActiveAt: 100},
	}
	repo.states[metadbKey("g1", 2)] = repo.active[0]
	repo.latest[key("g1", 2)] = testMessage("g1", 2, 10, "u2", 100, "deleted")

	app := New(Options{States: repo, Facts: repo})
	got, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 10})

	require.NoError(t, err)
	require.Empty(t, got.Conversations)
}

func TestSyncAppliesOnlyUnreadDeleteLineAndStableLimitOrdering(t *testing.T) {
	repo := newConversationSyncRepoStub()
	repo.active = []metadb.UserConversationState{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ReadSeq: 5, ActiveAt: 400},
		{UID: "u1", ChannelID: "g2", ChannelType: 2, ReadSeq: 4, DeletedToSeq: 6, ActiveAt: 390},
		{UID: "u1", ChannelID: "g3", ChannelType: 2, ReadSeq: 3, ActiveAt: 380},
		{UID: "u1", ChannelID: "g4", ChannelType: 2, ReadSeq: 1, ActiveAt: 370},
	}
	for _, state := range repo.active {
		repo.states[metadbKey(state.ChannelID, uint8(state.ChannelType))] = state
	}
	repo.latest[key("g1", 2)] = testMessage("g1", 2, 7, "u2", 300, "c1")
	repo.latest[key("g2", 2)] = testMessage("g2", 2, 6, "u2", 350, "c2")
	repo.latest[key("g3", 2)] = testMessage("g3", 2, 4, "u1", 300, "c3")
	repo.latest[key("g4", 2)] = testMessage("g4", 2, 5, "u4", 250, "c4")

	app := New(Options{States: repo, Facts: repo})
	got, err := app.Sync(context.Background(), SyncQuery{UID: "u1", OnlyUnread: true, Limit: 1})

	require.NoError(t, err)
	require.Equal(t, []SyncConversation{{
		ChannelID: "g1", ChannelType: 2, Unread: 2, Timestamp: 300, LastMsgSeq: 7, LastClientMsgNo: "c1", ReadToMsgSeq: 5, Version: time.Unix(300, 0).UnixNano(),
	}}, got.Conversations)
}

func TestSyncLoadsRecentsOnlyForFinalLimitedWindow(t *testing.T) {
	repo := newConversationSyncRepoStub()
	repo.active = []metadb.UserConversationState{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 300},
		{UID: "u1", ChannelID: "g2", ChannelType: 2, ActiveAt: 200},
		{UID: "u1", ChannelID: "g3", ChannelType: 2, ActiveAt: 100},
	}
	for _, state := range repo.active {
		repo.states[metadbKey(state.ChannelID, uint8(state.ChannelType))] = state
	}
	repo.latest[key("g1", 2)] = testMessage("g1", 2, 30, "u2", 300, "c1")
	repo.latest[key("g2", 2)] = testMessage("g2", 2, 20, "u2", 200, "c2")
	repo.latest[key("g3", 2)] = testMessage("g3", 2, 10, "u2", 100, "c3")
	repo.recents[key("g1", 2)] = []channel.Message{testMessage("g1", 2, 30, "u2", 300, "c1"), testMessage("g1", 2, 29, "u2", 299, "c1-1")}
	repo.recents[key("g2", 2)] = []channel.Message{testMessage("g2", 2, 20, "u2", 200, "c2"), testMessage("g2", 2, 19, "u2", 199, "c2-1")}
	repo.recents[key("g3", 2)] = []channel.Message{testMessage("g3", 2, 10, "u2", 100, "c3"), testMessage("g3", 2, 9, "u2", 99, "c3-1")}
	repo.recentLoadErr = errors.New("unexpected single recent load")

	app := New(Options{States: repo, Facts: repo})
	got, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 2, MsgCount: 2})

	require.NoError(t, err)
	require.Len(t, got.Conversations, 2)
	require.Equal(t, [][]ConversationKey{{key("g1", 2), key("g2", 2)}}, repo.recentBatchLoads)
	require.Empty(t, repo.recentLoads)
	require.Equal(t, []channel.Message{testMessage("g1", 2, 30, "u2", 300, "c1"), testMessage("g1", 2, 29, "u2", 299, "c1-1")}, got.Conversations[0].Recents)
	require.Equal(t, []channel.Message{testMessage("g2", 2, 20, "u2", 200, "c2"), testMessage("g2", 2, 19, "u2", 199, "c2-1")}, got.Conversations[1].Recents)
}

func TestSyncDoesNotReadDedicatedCMDConversationState(t *testing.T) {
	repo := newConversationSyncRepoStub()
	repo.active = []metadb.UserConversationState{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ReadSeq: 1, ActiveAt: 100},
		{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 0, ActiveAt: 200},
	}
	cmdRows := []metadb.CMDConversationState{
		{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 200},
	}
	repo.latest[key("g1", 2)] = testMessage("g1", 2, 3, "u2", 100, "chat")
	repo.latest[key("g1____cmd", 2)] = testMessage("g1____cmd", 2, 9, "system", 200, "cmd")

	app := New(Options{States: repo, Facts: repo})
	got, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 10})

	require.NoError(t, err)
	require.NotEmpty(t, cmdRows, "CMD rows exist in a separate store that conversation sync cannot access")
	require.Equal(t, []SyncConversation{{
		ChannelID: "g1", ChannelType: 2, Unread: 2, Timestamp: 100, LastMsgSeq: 3, LastClientMsgNo: "chat", ReadToMsgSeq: 1, Version: time.Unix(100, 0).UnixNano(),
	}}, got.Conversations)
	require.NotContains(t, repo.latestLoads, key("g1____cmd", 2))
}

func TestSyncIgnoresCommandChannelOverlayCandidates(t *testing.T) {
	repo := newConversationSyncRepoStub()
	commandKey := key(runtimechannelid.ToCommandChannel("g1"), 2)
	repo.latest[commandKey] = testMessage(commandKey.ChannelID, commandKey.ChannelType, 9, "system", 200, "cmd")

	app := New(Options{States: repo, Facts: repo})
	got, err := app.Sync(context.Background(), SyncQuery{
		UID:         "u1",
		LastMsgSeqs: map[ConversationKey]uint64{commandKey: 0},
		Limit:       10,
	})

	require.NoError(t, err)
	require.Empty(t, got.Conversations)
	require.NotContains(t, repo.latestLoads, commandKey)
}

type conversationSyncRepoStub struct {
	active             []metadb.UserConversationState
	states             map[metadb.ConversationKey]metadb.UserConversationState
	latest             map[ConversationKey]channel.Message
	recents            map[ConversationKey][]channel.Message
	upsertedStates     []metadb.UserConversationState
	scanCalls          int
	upsertErr          error
	latestLoads        []ConversationKey
	recentLoads        []ConversationKey
	recentBatchLoads   [][]ConversationKey
	recentLoadErr      error
	recentBatchLoadErr error
}

func newConversationSyncRepoStub() *conversationSyncRepoStub {
	return &conversationSyncRepoStub{
		states:  make(map[metadb.ConversationKey]metadb.UserConversationState),
		latest:  make(map[ConversationKey]channel.Message),
		recents: make(map[ConversationKey][]channel.Message),
	}
}

func (r *conversationSyncRepoStub) GetUserConversationState(_ context.Context, uid, channelID string, channelType int64) (metadb.UserConversationState, error) {
	state, ok := r.states[metadb.ConversationKey{ChannelID: channelID, ChannelType: channelType}]
	if !ok {
		return metadb.UserConversationState{}, metadb.ErrNotFound
	}
	return state, nil
}

func (r *conversationSyncRepoStub) UpsertUserConversationStates(_ context.Context, states []metadb.UserConversationState) error {
	if r.upsertErr != nil {
		return r.upsertErr
	}
	for _, state := range states {
		r.states[metadb.ConversationKey{ChannelID: state.ChannelID, ChannelType: state.ChannelType}] = state
		r.upsertedStates = append(r.upsertedStates, state)
	}
	return nil
}

func (r *conversationSyncRepoStub) ListUserConversationActive(_ context.Context, _ string, limit int) ([]metadb.UserConversationState, error) {
	out := append([]metadb.UserConversationState(nil), r.active...)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *conversationSyncRepoStub) LoadLatestMessages(_ context.Context, keys []ConversationKey) (map[ConversationKey]channel.Message, error) {
	r.latestLoads = append(r.latestLoads, append([]ConversationKey(nil), keys...)...)
	out := make(map[ConversationKey]channel.Message, len(keys))
	for _, key := range keys {
		msg, ok := r.latest[key]
		if ok {
			out[key] = msg
		}
	}
	return out, nil
}

func (r *conversationSyncRepoStub) LoadRecentMessages(_ context.Context, key ConversationKey, limit int) ([]channel.Message, error) {
	if r.recentLoadErr != nil {
		return nil, r.recentLoadErr
	}
	r.recentLoads = append(r.recentLoads, key)
	msgs := append([]channel.Message(nil), r.recents[key]...)
	if limit > 0 && len(msgs) > limit {
		msgs = msgs[:limit]
	}
	return msgs, nil
}

func (r *conversationSyncRepoStub) LoadRecentMessagesBatch(_ context.Context, keys []ConversationKey, limit int) (map[ConversationKey][]channel.Message, error) {
	if r.recentBatchLoadErr != nil {
		return nil, r.recentBatchLoadErr
	}
	r.recentBatchLoads = append(r.recentBatchLoads, append([]ConversationKey(nil), keys...))
	out := make(map[ConversationKey][]channel.Message, len(keys))
	for _, key := range keys {
		msgs := append([]channel.Message(nil), r.recents[key]...)
		if limit > 0 && len(msgs) > limit {
			msgs = msgs[:limit]
		}
		out[key] = msgs
	}
	return out, nil
}

func key(channelID string, channelType uint8) ConversationKey {
	return ConversationKey{ChannelID: channelID, ChannelType: channelType}
}

func metadbKey(channelID string, channelType uint8) metadb.ConversationKey {
	return metadb.ConversationKey{ChannelID: channelID, ChannelType: int64(channelType)}
}

func testMessage(channelID string, channelType uint8, seq uint64, fromUID string, ts int32, clientMsgNo string) channel.Message {
	return channel.Message{ChannelID: channelID, ChannelType: channelType, MessageSeq: seq, FromUID: fromUID, Timestamp: ts, ClientMsgNo: clientMsgNo}
}
