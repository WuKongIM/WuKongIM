package store

import (
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
