package store

import (
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestOpenCreatesEngineAndForChannelBindsKeyAndID(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer engine.Close()

	key := channel.ChannelKey("channel/1/c1")
	id := channel.ChannelID{ID: "c1", Type: 1}
	store := engine.ForChannel(key, id)
	if store == nil {
		t.Fatal("expected ChannelStore")
	}
	if store.key != key {
		t.Fatalf("ChannelStore.key = %q, want %q", store.key, key)
	}
	if store.id != id {
		t.Fatalf("ChannelStore.id = %+v, want %+v", store.id, id)
	}
}

func TestEngineForChannelReturnsStableStorePerKey(t *testing.T) {
	engine := openTestEngine(t)

	key := channel.ChannelKey("channel/1/c1")
	id := channel.ChannelID{ID: "c1", Type: 1}
	first := engine.ForChannel(key, id)
	second := engine.ForChannel(key, id)
	other := engine.ForChannel(channel.ChannelKey("channel/1/c2"), channel.ChannelID{ID: "c2", Type: 1})

	if first != second {
		t.Fatal("expected ForChannel() to reuse the same store for the same key")
	}
	if first == other {
		t.Fatal("expected different keys to have different ChannelStore instances")
	}
}

func TestEngineForChannelPanicsOnMismatchedChannelID(t *testing.T) {
	engine := openTestEngine(t)

	key := channel.ChannelKey("channel/1/c1")
	engine.ForChannel(key, channel.ChannelID{ID: "c1", Type: 1})

	require.Panics(t, func() {
		engine.ForChannel(key, channel.ChannelID{ID: "c2", Type: 1})
	})
}

func TestDefaultPebbleOptionsUseWALMinSyncInterval(t *testing.T) {
	opts := defaultPebbleOptions()
	require.NotNil(t, opts)
	require.NotNil(t, opts.WALMinSyncInterval)
	require.Equal(t, defaultChannelWALMinSyncInterval, opts.WALMinSyncInterval())
}

func TestListChannelKeysAllocatesPerChannelNotPerMessageRow(t *testing.T) {
	engine := openTestEngine(t)
	st := engine.ForChannel(channel.ChannelKey("channel/1/hot"), channel.ChannelID{ID: "hot", Type: 1})
	payloads := make([]string, 512)
	for i := range payloads {
		payloads[i] = "msg"
	}
	mustAppendRecords(t, st, payloads)

	keys, err := engine.ListChannelKeys()
	require.NoError(t, err)
	require.Equal(t, []channel.ChannelKey{st.key}, keys)

	allocs := testing.AllocsPerRun(5, func() {
		_, err := engine.ListChannelKeys()
		require.NoError(t, err)
	})
	require.Less(t, allocs, float64(320), "ListChannelKeys should allocate with unique channels, not message rows")
}

func TestListChannelKeysIncludesSystemOnlyChannelsWhenSkippingRows(t *testing.T) {
	engine := openTestEngine(t)
	first := engine.ForChannel(channel.ChannelKey("channel/1/cursor-a"), channel.ChannelID{ID: "cursor-a", Type: 1})
	second := engine.ForChannel(channel.ChannelKey("channel/1/cursor-b"), channel.ChannelID{ID: "cursor-b", Type: 1})
	require.NoError(t, first.StoreCommittedDispatchCursor("committed", 1))
	require.NoError(t, second.StoreCommittedDispatchCursor("committed", 1))

	keys, err := engine.ListChannelKeys()
	require.NoError(t, err)
	require.ElementsMatch(t, []channel.ChannelKey{first.key, second.key}, keys)
}

func TestNextKeyAfterPrefix(t *testing.T) {
	require.Equal(t, []byte{0x15, 0x02, 'a', 'd'}, nextKeyAfterPrefix([]byte{0x15, 0x02, 'a', 'c'}))
	require.Equal(t, []byte{0x15, 0x03}, nextKeyAfterPrefix([]byte{0x15, 0x02, 0xff}))
	require.Equal(t, []byte{0xff, 0xff, 0xff}, nextKeyAfterPrefix([]byte{0xff, 0xff}))
}
