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
