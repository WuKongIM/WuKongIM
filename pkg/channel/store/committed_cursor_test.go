package store

import (
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestChannelStoreCommittedDispatchCursorRoundTrips(t *testing.T) {
	st := newTestChannelStore(t)

	seq, ok, err := st.LoadCommittedDispatchCursor("conversation")
	require.NoError(t, err)
	require.False(t, ok)
	require.Zero(t, seq)

	require.NoError(t, st.StoreCommittedDispatchCursor("conversation", 12))

	seq, ok, err = st.LoadCommittedDispatchCursor("conversation")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(12), seq)
}

func TestChannelStoreCommittedDispatchCursorHotPathDoesNotMoveBackward(t *testing.T) {
	st := newTestChannelStore(t)
	require.NoError(t, st.StoreCommittedDispatchCursor("conversation", 12))

	require.NoError(t, st.StoreCommittedDispatchCursor("conversation", 4))

	seq, ok, err := st.LoadCommittedDispatchCursor("conversation")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(12), seq)
}

func TestChannelStoreConfirmCommittedDispatchCursorDurableRejectsMissingCursor(t *testing.T) {
	st := newTestChannelStore(t)

	_, err := st.ConfirmCommittedDispatchCursorDurable("committed", 1)
	require.ErrorIs(t, err, channel.ErrEmptyState)
}

func TestChannelStoreConfirmCommittedDispatchCursorDurableRejectsBelowMinSeq(t *testing.T) {
	st := newTestChannelStore(t)
	require.NoError(t, st.StoreCommittedDispatchCursor("committed", 6))

	_, err := st.ConfirmCommittedDispatchCursorDurable("committed", 7)
	require.ErrorIs(t, err, channel.ErrCorruptState)
}

func TestChannelStoreConfirmCommittedDispatchCursorDurableSyncsVisibleCursor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store")
	key, id := testChannelStoreIdentity("confirm-cursor")

	engine, err := Open(path)
	require.NoError(t, err)
	st := engine.ForChannel(key, id)
	require.NoError(t, st.StoreCommittedDispatchCursor("committed", 10))

	seq, err := st.ConfirmCommittedDispatchCursorDurable("committed", 7)
	require.NoError(t, err)
	require.Equal(t, uint64(10), seq)
	seq, ok, err := st.LoadCommittedDispatchCursor("committed")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(10), seq)
	require.NoError(t, engine.Close())

	reopened, err := Open(path)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, reopened.Close())
	}()
	seq, ok, err = reopened.ForChannel(key, id).LoadCommittedDispatchCursor("committed")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(10), seq)
}

func TestChannelStoreAdvanceCommittedDispatchCursorDurableWritesMissingCursor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store")
	key, id := testChannelStoreIdentity("advance-cursor")

	engine, err := Open(path)
	require.NoError(t, err)
	st := engine.ForChannel(key, id)
	require.NoError(t, st.AdvanceCommittedDispatchCursorDurable("committed", 7))
	require.NoError(t, engine.Close())

	reopened, err := Open(path)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, reopened.Close())
	}()
	seq, ok, err := reopened.ForChannel(key, id).LoadCommittedDispatchCursor("committed")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(7), seq)
}

func TestChannelStoreAdvanceCommittedDispatchCursorDurableRejectsBackwardMove(t *testing.T) {
	st := newTestChannelStore(t)
	require.NoError(t, st.StoreCommittedDispatchCursor("committed", 9))

	err := st.AdvanceCommittedDispatchCursorDurable("committed", 8)
	require.ErrorIs(t, err, channel.ErrCorruptState)

	seq, ok, err := st.LoadCommittedDispatchCursor("committed")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(9), seq)
}

func TestEngineListChannelKeysReturnsPersistedChannels(t *testing.T) {
	engine := openTestEngine(t)
	keyA := channel.ChannelKey("channel/1/a")
	keyB := channel.ChannelKey("channel/1/b")
	stA := engine.ForChannel(keyA, channel.ChannelID{ID: "a", Type: 1})
	stB := engine.ForChannel(keyB, channel.ChannelID{ID: "b", Type: 1})
	mustAppendRecords(t, stA, []string{"a1"})
	mustAppendRecords(t, stB, []string{"b1"})

	keys, err := engine.ListChannelKeys()

	require.NoError(t, err)
	require.ElementsMatch(t, []channel.ChannelKey{keyA, keyB}, keys)
}

func TestEngineListChannelKeysIncludesCheckpointOnlyChannel(t *testing.T) {
	engine := openTestEngine(t)
	key, id := testChannelStoreIdentity("checkpoint-only")
	st := engine.ForChannel(key, id)
	require.NoError(t, st.StoreCheckpoint(channel.Checkpoint{Epoch: 1, HW: 3}))

	keys, err := engine.ListChannelKeys()

	require.NoError(t, err)
	require.ElementsMatch(t, []channel.ChannelKey{key}, keys)
}

func TestEngineListChannelKeysIncludesCommittedCursorOnlyChannel(t *testing.T) {
	engine := openTestEngine(t)
	key, id := testChannelStoreIdentity("cursor-only")
	st := engine.ForChannel(key, id)
	require.NoError(t, st.StoreCommittedDispatchCursor("committed", 3))

	keys, err := engine.ListChannelKeys()

	require.NoError(t, err)
	require.ElementsMatch(t, []channel.ChannelKey{key}, keys)
}
