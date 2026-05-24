package store

import (
	"context"
	"testing"

	oldchannel "github.com/WuKongIM/WuKongIM/pkg/channel"
	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/stretchr/testify/require"
)

func TestOldStoreAdapterContract(t *testing.T) {
	factory := NewOldStoreFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	testStoreContract(t, factory)
	testStoreCheckpointHWMonotonic(t, factory)
}

func TestOldStoreAdapterCheckpointPreservesExistingFields(t *testing.T) {
	factory := NewOldStoreFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	cs, err := factory.ChannelStore(ch.ChannelKey("1:preserve"), ch.ChannelID{ID: "preserve", Type: 1})
	require.NoError(t, err)
	adapter := cs.(*oldChannelStoreAdapter)
	require.NoError(t, adapter.store.StoreCheckpoint(oldchannel.Checkpoint{Epoch: 7, LogStartOffset: 2, HW: 5}))

	require.NoError(t, adapter.StoreCheckpoint(context.Background(), ch.Checkpoint{HW: 3}))
	current, err := adapter.store.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, oldchannel.Checkpoint{Epoch: 7, LogStartOffset: 2, HW: 5}, current)

	require.NoError(t, adapter.StoreCheckpoint(context.Background(), ch.Checkpoint{HW: 8}))
	current, err = adapter.store.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, oldchannel.Checkpoint{Epoch: 7, LogStartOffset: 2, HW: 8}, current)
}
