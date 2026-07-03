package cmdsync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestPendingUpdaterCoalescesByCommandChannelAndUID(t *testing.T) {
	store := &fakePendingStateStore{}
	updater := NewConversationUpdater(ConversationUpdaterOptions{Store: store, Now: fixedNano(1000)})

	require.NoError(t, updater.PushIntent(context.Background(), ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 5, ActiveAt: 50, UserReadSeqs: map[string]uint64{"u1": 0, "u2": 5}}))
	require.NoError(t, updater.PushIntent(context.Background(), ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 7, ActiveAt: 70, UserReadSeqs: map[string]uint64{"u1": 0, "u2": 0, "u3": 0}}))

	got := updater.ListPending(context.Background(), "u2", 10)
	require.Equal(t, []PendingConversationView{{CommandChannelID: "g1____cmd", ChannelType: 2, LastMsgSeq: 7, ActiveAt: 70, ReadSeq: 5}}, got)
}

func TestPendingUpdaterKeepsPerUIDLastSeqForDisjointRecipients(t *testing.T) {
	store := &fakePendingStateStore{}
	updater := NewConversationUpdater(ConversationUpdaterOptions{Store: store, Now: fixedNano(1000)})

	require.NoError(t, updater.PushIntent(context.Background(), ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 5, ActiveAt: 50, UserReadSeqs: map[string]uint64{"u1": 0}}))
	require.NoError(t, updater.PushIntent(context.Background(), ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 7, ActiveAt: 70, UserReadSeqs: map[string]uint64{"u2": 0}}))

	require.Equal(t, []PendingConversationView{{CommandChannelID: "g1____cmd", ChannelType: 2, LastMsgSeq: 5, ActiveAt: 50, ReadSeq: 0}}, updater.ListPending(context.Background(), "u1", 10))
	require.Equal(t, []PendingConversationView{{CommandChannelID: "g1____cmd", ChannelType: 2, LastMsgSeq: 7, ActiveAt: 70, ReadSeq: 0}}, updater.ListPending(context.Background(), "u2", 10))

	require.NoError(t, updater.Flush(context.Background()))
	require.Equal(t, []metadb.CMDConversationState{
		{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 0, ActiveAt: 50, UpdatedAt: 1000},
		{UID: "u2", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 0, ActiveAt: 70, UpdatedAt: 1000},
	}, store.states)
}

func TestPendingUpdaterKeepsWholeFailedFlushBatch(t *testing.T) {
	store := &fakePendingStateStore{err: errors.New("store down")}
	updater := NewConversationUpdater(ConversationUpdaterOptions{Store: store, Now: fixedNano(1000), FlushBatchSize: 10})
	require.NoError(t, updater.PushIntent(context.Background(), ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 9, ActiveAt: 90, UserReadSeqs: map[string]uint64{"u1": 0, "u2": 0}}))

	require.Error(t, updater.Flush(context.Background()))
	require.Equal(t, []PendingConversationView{{CommandChannelID: "g1____cmd", ChannelType: 2, LastMsgSeq: 9, ActiveAt: 90, ReadSeq: 0}}, updater.ListPending(context.Background(), "u1", 10))
	require.Equal(t, []PendingConversationView{{CommandChannelID: "g1____cmd", ChannelType: 2, LastMsgSeq: 9, ActiveAt: 90, ReadSeq: 0}}, updater.ListPending(context.Background(), "u2", 10))
}

func TestPendingUpdaterRemovesSuccessfulFlushBatch(t *testing.T) {
	store := &fakePendingStateStore{}
	updater := NewConversationUpdater(ConversationUpdaterOptions{Store: store, Now: fixedNano(1000), FlushBatchSize: 10})
	require.NoError(t, updater.PushIntent(context.Background(), ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 9, ActiveAt: 90, UserReadSeqs: map[string]uint64{"u1": 0, "u2": 0}}))

	require.NoError(t, updater.Flush(context.Background()))
	require.Empty(t, updater.ListPending(context.Background(), "u1", 10))
	require.Empty(t, updater.ListPending(context.Background(), "u2", 10))
	require.Equal(t, []metadb.CMDConversationState{
		{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 0, ActiveAt: 90, UpdatedAt: 1000},
		{UID: "u2", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 0, ActiveAt: 90, UpdatedAt: 1000},
	}, store.states)
}

func TestPendingUpdaterSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	updater := NewConversationUpdater(ConversationUpdaterOptions{Store: &fakePendingStateStore{}, DataDir: dir, Now: fixedNano(1000)})
	require.NoError(t, updater.PushIntent(context.Background(), ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 3, ActiveAt: 30, UserReadSeqs: map[string]uint64{"u1": 0}}))
	require.NoError(t, updater.Stop())

	loaded := NewConversationUpdater(ConversationUpdaterOptions{Store: &fakePendingStateStore{}, DataDir: dir, Now: fixedNano(1000)})
	require.NoError(t, loaded.Start())
	t.Cleanup(func() { require.NoError(t, loaded.Stop()) })
	require.Equal(t, []PendingConversationView{{CommandChannelID: "g1____cmd", ChannelType: 2, LastMsgSeq: 3, ActiveAt: 30, ReadSeq: 0}}, loaded.ListPending(context.Background(), "u1", 10))
}

func TestPendingUpdaterKeepsLoadedFileUntilSuccessfulFlush(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conversationv2", "cmd_conversation_updates.json")
	saved := NewConversationUpdater(ConversationUpdaterOptions{Store: &fakePendingStateStore{}, DataDir: dir, Now: fixedNano(1000)})
	require.NoError(t, saved.PushIntent(context.Background(), ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 3, ActiveAt: 30, UserReadSeqs: map[string]uint64{"u1": 0}}))
	require.NoError(t, saved.Stop())

	loadedStore := &fakePendingStateStore{}
	loaded := NewConversationUpdater(ConversationUpdaterOptions{Store: loadedStore, DataDir: dir, FlushInterval: time.Hour, Now: fixedNano(2000)})
	require.NoError(t, loaded.Start())
	t.Cleanup(func() { require.NoError(t, loaded.Stop()) })
	require.FileExists(t, path)
	require.Equal(t, []PendingConversationView{{CommandChannelID: "g1____cmd", ChannelType: 2, LastMsgSeq: 3, ActiveAt: 30, ReadSeq: 0}}, loaded.ListPending(context.Background(), "u1", 10))

	require.NoError(t, loaded.Flush(context.Background()))
	require.NoFileExists(t, path)
	require.Equal(t, []metadb.CMDConversationState{{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 0, ActiveAt: 30, UpdatedAt: 2000}}, loadedStore.states)
}

func TestPendingUpdaterKeepsLoadedFileWhenFlushFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conversationv2", "cmd_conversation_updates.json")
	saved := NewConversationUpdater(ConversationUpdaterOptions{Store: &fakePendingStateStore{}, DataDir: dir, Now: fixedNano(1000)})
	require.NoError(t, saved.PushIntent(context.Background(), ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 3, ActiveAt: 30, UserReadSeqs: map[string]uint64{"u1": 0}}))
	require.NoError(t, saved.Stop())

	loaded := NewConversationUpdater(ConversationUpdaterOptions{Store: &fakePendingStateStore{err: errors.New("store down")}, DataDir: dir, FlushInterval: time.Hour, Now: fixedNano(2000)})
	require.NoError(t, loaded.Start())
	t.Cleanup(func() { _ = loaded.Stop() })
	require.Error(t, loaded.Flush(context.Background()))
	require.FileExists(t, path)
}

func TestPendingUpdaterRemovesLoadedFileWhenPendingAlreadySynced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conversationv2", "cmd_conversation_updates.json")
	saved := NewConversationUpdater(ConversationUpdaterOptions{Store: &fakePendingStateStore{}, DataDir: dir, Now: fixedNano(1000)})
	require.NoError(t, saved.PushIntent(context.Background(), ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 3, ActiveAt: 30, UserReadSeqs: map[string]uint64{"u1": 0}}))
	require.NoError(t, saved.Stop())

	loaded := NewConversationUpdater(ConversationUpdaterOptions{Store: &fakePendingStateStore{}, DataDir: dir, FlushInterval: time.Hour, Now: fixedNano(2000)})
	require.NoError(t, loaded.Start())
	t.Cleanup(func() { require.NoError(t, loaded.Stop()) })
	require.FileExists(t, path)
	require.NoError(t, loaded.MarkSynced(context.Background(), "u1", CommandChannelKey{ChannelID: "g1____cmd", ChannelType: 2}, 3))

	require.NoError(t, loaded.Flush(context.Background()))
	require.NoFileExists(t, path)
}

func TestPendingUpdaterStopContextBoundsBlockedFlush(t *testing.T) {
	store := newContextBlockingPendingStateStore()
	updater := NewConversationUpdater(ConversationUpdaterOptions{
		Store:         store,
		FlushInterval: time.Millisecond,
		Now:           fixedNano(1000),
	})
	require.NoError(t, updater.PushIntent(context.Background(), ConversationIntent{
		CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 1, ActiveAt: 10, UserReadSeqs: map[string]uint64{"u1": 0},
	}))
	require.NoError(t, updater.Start())
	store.waitEntered(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, updater.StopContext(ctx), context.DeadlineExceeded)
}

func TestPendingUpdaterMarkSyncedRemovesOnlyUIDWhenThroughSeqCoversPending(t *testing.T) {
	updater := NewConversationUpdater(ConversationUpdaterOptions{Store: &fakePendingStateStore{}, Now: fixedNano(1000)})
	require.NoError(t, updater.PushIntent(context.Background(), ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 9, ActiveAt: 90, UserReadSeqs: map[string]uint64{"u1": 0, "u2": 0}}))

	require.NoError(t, updater.MarkSynced(context.Background(), "u1", CommandChannelKey{ChannelID: "g1____cmd", ChannelType: 2}, 9))

	require.Empty(t, updater.ListPending(context.Background(), "u1", 10))
	require.Equal(t, []PendingConversationView{{CommandChannelID: "g1____cmd", ChannelType: 2, LastMsgSeq: 9, ActiveAt: 90, ReadSeq: 0}}, updater.ListPending(context.Background(), "u2", 10))
}

func TestPendingUpdaterMarkSyncedKeepsPendingWhenThroughSeqIsLower(t *testing.T) {
	updater := NewConversationUpdater(ConversationUpdaterOptions{Store: &fakePendingStateStore{}, Now: fixedNano(1000)})
	require.NoError(t, updater.PushIntent(context.Background(), ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 9, ActiveAt: 90, UserReadSeqs: map[string]uint64{"u1": 0}}))

	require.NoError(t, updater.MarkSynced(context.Background(), "u1", CommandChannelKey{ChannelID: "g1____cmd", ChannelType: 2}, 8))

	require.Equal(t, []PendingConversationView{{CommandChannelID: "g1____cmd", ChannelType: 2, LastMsgSeq: 9, ActiveAt: 90, ReadSeq: 0}}, updater.ListPending(context.Background(), "u1", 10))
}

func TestPendingUpdaterListPendingSortsDeterministically(t *testing.T) {
	updater := NewConversationUpdater(ConversationUpdaterOptions{Store: &fakePendingStateStore{}, Now: fixedNano(1000)})
	for _, intent := range []ConversationIntent{
		{CommandChannelID: "b____cmd", ChannelType: 2, MessageSeq: 1, ActiveAt: 100, UserReadSeqs: map[string]uint64{"u1": 0}},
		{CommandChannelID: "a____cmd", ChannelType: 2, MessageSeq: 1, ActiveAt: 100, UserReadSeqs: map[string]uint64{"u1": 0}},
		{CommandChannelID: "c____cmd", ChannelType: 1, MessageSeq: 1, ActiveAt: 100, UserReadSeqs: map[string]uint64{"u1": 0}},
		{CommandChannelID: "z____cmd", ChannelType: 2, MessageSeq: 1, ActiveAt: 200, UserReadSeqs: map[string]uint64{"u1": 0}},
	} {
		require.NoError(t, updater.PushIntent(context.Background(), intent))
	}

	require.Equal(t, []PendingConversationView{
		{CommandChannelID: "z____cmd", ChannelType: 2, LastMsgSeq: 1, ActiveAt: 200, ReadSeq: 0},
		{CommandChannelID: "c____cmd", ChannelType: 1, LastMsgSeq: 1, ActiveAt: 100, ReadSeq: 0},
		{CommandChannelID: "a____cmd", ChannelType: 2, LastMsgSeq: 1, ActiveAt: 100, ReadSeq: 0},
		{CommandChannelID: "b____cmd", ChannelType: 2, LastMsgSeq: 1, ActiveAt: 100, ReadSeq: 0},
	}, updater.ListPending(context.Background(), "u1", 10))
}

func TestPendingUpdaterLoadRenamesBadJSONAndDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conversationv2", "cmd_conversation_updates.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("{"), 0o644))

	updater := NewConversationUpdater(ConversationUpdaterOptions{Store: &fakePendingStateStore{}, DataDir: dir, Now: fixedNano(1000)})
	require.NoError(t, updater.Start())

	_, err := os.Stat(path)
	require.True(t, errors.Is(err, os.ErrNotExist))
	_, err = os.Stat(path + ".bad")
	require.NoError(t, err)
}

func TestPendingUpdaterFlushRemovesOnlySuccessfulBatches(t *testing.T) {
	store := &fakePendingStateStore{failOnCall: 2, err: errors.New("later batch failed")}
	updater := NewConversationUpdater(ConversationUpdaterOptions{Store: store, Now: fixedNano(1000), FlushBatchSize: 1})
	require.NoError(t, updater.PushIntent(context.Background(), ConversationIntent{CommandChannelID: "a____cmd", ChannelType: 2, MessageSeq: 1, ActiveAt: 10, UserReadSeqs: map[string]uint64{"u1": 0}}))
	require.NoError(t, updater.PushIntent(context.Background(), ConversationIntent{CommandChannelID: "b____cmd", ChannelType: 2, MessageSeq: 2, ActiveAt: 20, UserReadSeqs: map[string]uint64{"u1": 0}}))

	require.Error(t, updater.Flush(context.Background()))

	require.Equal(t, []PendingConversationView{{CommandChannelID: "b____cmd", ChannelType: 2, LastMsgSeq: 2, ActiveAt: 20, ReadSeq: 0}}, updater.ListPending(context.Background(), "u1", 10))
}

func fixedNano(n int64) func() time.Time {
	return func() time.Time { return time.Unix(0, n) }
}

type fakePendingStateStore struct {
	states     []metadb.CMDConversationState
	calls      int
	failOnCall int
	err        error
}

func (f *fakePendingStateStore) UpsertCMDConversationStates(_ context.Context, states []metadb.CMDConversationState) error {
	f.calls++
	if f.err != nil && (f.failOnCall == 0 || f.failOnCall == f.calls) {
		return f.err
	}
	f.states = append(f.states, states...)
	return nil
}

type contextBlockingPendingStateStore struct {
	entered chan struct{}
	once    sync.Once
}

func newContextBlockingPendingStateStore() *contextBlockingPendingStateStore {
	return &contextBlockingPendingStateStore{entered: make(chan struct{})}
}

func (s *contextBlockingPendingStateStore) UpsertCMDConversationStates(ctx context.Context, _ []metadb.CMDConversationState) error {
	s.once.Do(func() { close(s.entered) })
	<-ctx.Done()
	return ctx.Err()
}

func (s *contextBlockingPendingStateStore) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-s.entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocked pending flush")
	}
}
