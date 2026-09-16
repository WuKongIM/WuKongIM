package store

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestPersistedFrontierTracksDiskTailAcrossLeaseReopen(t *testing.T) {
	ctx := context.Background()
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { require.NoError(t, factory.Close()) })
	id := ch.ChannelID{ID: "persisted-frontier", Type: 2}
	store, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	frontier := store.(PersistedFrontierLoader)
	leo, err := frontier.LoadPersistedFrontier(ctx)
	require.NoError(t, err)
	require.Zero(t, leo)
	_, err = store.AppendLeader(ctx, AppendLeaderRequest{Records: []ch.Record{{ID: 11, Payload: []byte("first")}, {ID: 12, Payload: []byte("tail")}}})
	require.NoError(t, err)
	require.NoError(t, store.StoreCheckpoint(ctx, ch.Checkpoint{HW: 1}))
	leo, err = frontier.LoadPersistedFrontier(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), leo)
	state, err := store.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), state.HW)
	require.NoError(t, store.Close())
	_, err = frontier.LoadPersistedFrontier(ctx)
	require.Error(t, err)
	store, err = factory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	frontier = store.(PersistedFrontierLoader)
	leo, err = frontier.LoadPersistedFrontier(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), leo)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = frontier.LoadPersistedFrontier(canceled)
	require.ErrorIs(t, err, context.Canceled)
}
