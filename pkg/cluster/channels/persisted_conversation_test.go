package channels

import (
	"context"
	"errors"
	"fmt"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

func TestPersistedConversationReadsColdUncommittedTailWithoutRuntime(t *testing.T) {
	id := ch.ChannelID{ID: "cold-persisted", Type: 2}
	factory := channelstore.NewMemoryFactory()
	store, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	_, err = store.AppendLeader(context.Background(), channelstore.AppendLeaderRequest{Records: []ch.Record{{ID: 1, FromUID: "sender", Payload: []byte("persisted")}}})
	require.NoError(t, err)
	require.NoError(t, store.Close())
	runtime := &runtimeHWProbeRuntime{fakeRuntime: &fakeRuntime{}}
	meta := ch.Meta{ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
	svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: NewStaticMetaSource([]ch.Meta{meta}), Store: factory})
	require.NoError(t, err)
	heads, err := svc.ReadPersistedConversationHeads(context.Background(), []ch.ChannelID{id}, "reader")
	require.NoError(t, err)
	require.Len(t, heads, 1)
	require.NoError(t, heads[0].Err)
	require.True(t, heads[0].Head.Found)
	require.Equal(t, []byte("persisted"), heads[0].Head.Message.Payload)
	require.Zero(t, runtime.applyCalls)
	require.Zero(t, runtime.probeCalls)
}

func TestPersistedConversationRPCUsesDistinctKindAndNeverActivatesRemoteLeader(t *testing.T) {
	id := ch.ChannelID{ID: "remote-persisted", Type: 2}
	factory := channelstore.NewMemoryFactory()
	store, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	_, err = store.AppendLeader(context.Background(), channelstore.AppendLeaderRequest{Records: []ch.Record{{ID: 1, FromUID: "sender", Payload: []byte("disk-only")}}})
	require.NoError(t, err)
	require.NoError(t, store.Close())
	meta := ch.Meta{ID: id, Epoch: 3, LeaderEpoch: 5, Leader: 2, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
	runtime := &runtimeHWProbeRuntime{fakeRuntime: &fakeRuntime{}}
	remote, err := NewService(Config{Runtime: runtime, LocalNode: 2, Store: factory, MetaSource: NewStaticMetaSource([]ch.Meta{meta})})
	require.NoError(t, err)
	forward := &persistedCodecForward{target: remote}
	origin, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, Store: factory, MetaSource: NewStaticMetaSource([]ch.Meta{meta}), Forward: forward})
	require.NoError(t, err)
	results, err := origin.ReadPersistedConversationHeads(context.Background(), []ch.ChannelID{id}, "reader")
	require.NoError(t, err)
	require.NoError(t, results[0].Err)
	require.Equal(t, uint64(1), results[0].Head.ReadThroughSeq)
	require.Equal(t, uint8(kindPersistedConversationHeads), forward.kind)
	require.Zero(t, runtime.probeCalls)
	require.Zero(t, runtime.applyCalls)
}

type persistedCodecForward struct {
	ForwardClient
	target *Service
	kind   uint8
}

func (f *persistedCodecForward) ForwardConversationHeads(ctx context.Context, _ ch.NodeID, req ConversationHeadsRequest) (ConversationHeadsResponse, error) {
	encoded, err := encodeConversationHeadsRequest(req)
	if err != nil {
		return ConversationHeadsResponse{}, err
	}
	f.kind = encoded[1]
	decoded, err := decodeConversationHeadsRequest(encoded)
	if err != nil {
		return ConversationHeadsResponse{}, err
	}
	return f.target.handleForwardConversationHeads(ctx, decoded)
}

func TestPersistedConversationAdmissionBoundsAllCallersAndReleasesOnError(t *testing.T) {
	id := ch.ChannelID{ID: "failure", Type: 2}
	factory := &persistedFailingFactory{}
	meta := ch.Meta{ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, MinISR: 2, Status: ch.StatusActive}
	svc, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, Store: factory, MetaSource: NewStaticMetaSource([]ch.Meta{meta})})
	require.NoError(t, err)
	for i := 0; i < cap(svc.persistedReads); i++ {
		svc.persistedReads <- struct{}{}
	}
	results, err := svc.ReadPersistedConversationHeads(context.Background(), []ch.ChannelID{id}, "reader")
	require.NoError(t, err)
	require.ErrorIs(t, results[0].Err, ch.ErrBackpressured)
	require.Zero(t, factory.calls)
	<-svc.persistedReads
	results, err = svc.ReadPersistedConversationHeads(context.Background(), []ch.ChannelID{id}, "reader")
	require.NoError(t, err)
	require.ErrorIs(t, results[0].Err, errPersistedDiskFailure)
	require.Equal(t, 1, factory.calls)
	require.Equal(t, cap(svc.persistedReads)-1, len(svc.persistedReads))
}

var errPersistedDiskFailure = errors.New("disk read failed")

type persistedFailingFactory struct {
	channelstore.Factory
	calls int
}

func (f *persistedFailingFactory) ChannelStore(ch.ChannelKey, ch.ChannelID) (channelstore.ChannelStore, error) {
	f.calls++
	return nil, errPersistedDiskFailure
}

func TestPersistedConversationReadsManyColdChannelsWithoutLoading(t *testing.T) {
	factory := channelstore.NewMemoryFactory()
	ids := make([]ch.ChannelID, 200)
	metas := make([]ch.Meta, 200)
	for i := range ids {
		ids[i] = ch.ChannelID{ID: fmt.Sprintf("cold-%03d", i), Type: 2}
		metas[i] = ch.Meta{ID: ids[i], Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
		store, err := factory.ChannelStore(ch.ChannelKeyForID(ids[i]), ids[i])
		require.NoError(t, err)
		_, err = store.AppendLeader(context.Background(), channelstore.AppendLeaderRequest{Records: []ch.Record{{ID: uint64(i + 1), FromUID: "sender", Payload: []byte("disk")}}})
		require.NoError(t, err)
		require.NoError(t, store.Close())
	}
	svc, err := NewService(Config{LocalNode: 1, ReactorCount: 1, MaxChannels: 1, Store: factory, Transport: channeltransport.NewLocalNetwork(), MetaSource: NewStaticMetaSource(metas)})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })
	for attempt := 0; attempt < 3; attempt++ {
		heads, err := svc.ReadPersistedConversationHeads(context.Background(), ids, "reader")
		require.NoError(t, err)
		require.Len(t, heads, len(ids))
		for _, head := range heads {
			require.NoError(t, head.Err)
			require.True(t, head.Head.Found)
		}
		probe, err := svc.RuntimeProbe(context.Background(), ch.RuntimeSelector{ChannelIDs: ids})
		require.NoError(t, err)
		require.Empty(t, probe.Channels)
		require.Len(t, probe.Missing, len(ids))
	}
}
