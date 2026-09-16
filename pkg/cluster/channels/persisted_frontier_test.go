package channels

import (
	"context"
	"errors"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

type persistedFrontierStore struct {
	*channelstore.MemoryChannelStore
	frontierErr error
	loads       int
}

var errUnusedCheckpoint = errors.New("checkpoint must not be read for a persisted preview")

func (s *persistedFrontierStore) Load(context.Context) (channelstore.InitialState, error) {
	s.loads++
	return channelstore.InitialState{}, errUnusedCheckpoint
}

func (s *persistedFrontierStore) LoadPersistedFrontier(ctx context.Context) (uint64, error) {
	if s.frontierErr != nil {
		return 0, s.frontierErr
	}
	state, err := s.MemoryChannelStore.Load(ctx)
	return state.LEO, err
}

type persistedFrontierFactory struct{ store *persistedFrontierStore }

func (f persistedFrontierFactory) ChannelStore(ch.ChannelKey, ch.ChannelID) (channelstore.ChannelStore, error) {
	return f.store, nil
}

func TestPersistedReadsUseDiskFrontierWithoutCheckpoint(t *testing.T) {
	ctx := context.Background()
	id := ch.ChannelID{ID: "persisted-frontier", Type: 2}
	factory := channelstore.NewMemoryFactory()
	base, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	store := &persistedFrontierStore{MemoryChannelStore: base.(*channelstore.MemoryChannelStore)}
	_, err = store.AppendLeader(ctx, channelstore.AppendLeaderRequest{Records: []ch.Record{
		{ID: 1, FromUID: "sender", Payload: []byte("retained")},
		{ID: 2, FromUID: "sender", Payload: []byte("disk-tail")},
	}})
	require.NoError(t, err)
	svc := &Service{store: persistedFrontierFactory{store: store}}
	head, activate, err := svc.readStoredConversationHead(ctx, id, "reader", 1, 2, 0, false, true)
	require.NoError(t, err)
	require.False(t, activate)
	require.True(t, head.Found)
	require.Equal(t, uint64(2), head.ReadThroughSeq)
	require.Equal(t, "disk-tail", string(head.Message.Payload))
	read := CommittedRead{ChannelID: id, Request: channelstore.ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 1024}}
	page, err := svc.readStoredMessages(ctx, read, 1, 2, 0, false, true)
	require.NoError(t, err)
	require.Len(t, page.Messages, 1)
	require.Equal(t, uint64(2), page.Messages[0].MessageSeq)
	require.Zero(t, store.loads)
	_, err = svc.readStoredMessages(ctx, read, 1, 2, 2, true, false)
	require.ErrorIs(t, err, errUnusedCheckpoint, "committed history still loads its checkpoint")
	store.frontierErr = errors.New("disk frontier failure")
	_, _, err = svc.readStoredConversationHead(ctx, id, "reader", 1, 2, 0, false, true)
	require.ErrorIs(t, err, store.frontierErr)
}
