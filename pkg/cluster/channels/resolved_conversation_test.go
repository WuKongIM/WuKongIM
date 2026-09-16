package channels

import (
	"context"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestResolvedConversationHeadsPreserveRemoteAuthorityAndRetention(t *testing.T) {
	ctx := context.Background()
	id := ch.ChannelID{ID: "resolved-head", Type: 2}
	factory := channelstore.NewMemoryFactory()
	store, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	_, err = store.AppendLeader(ctx, channelstore.AppendLeaderRequest{Records: []ch.Record{{ID: 1, FromUID: "sender", Payload: []byte("retained")}, {ID: 2, FromUID: "sender", Payload: []byte("tail")}}})
	require.NoError(t, err)
	require.NoError(t, store.Close())
	meta := ch.Meta{ID: id, Epoch: 3, LeaderEpoch: 5, Leader: 2, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive, RetentionThroughSeq: 1}
	runtime := &runtimeHWProbeRuntime{fakeRuntime: &fakeRuntime{}}
	remoteSource := NewStaticMetaSource([]ch.Meta{meta})
	remote, err := NewService(Config{Runtime: runtime, LocalNode: 2, Store: factory, MetaSource: remoteSource})
	require.NoError(t, err)
	// An empty source proves the origin does not issue a second metadata lookup.
	origin, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, Store: factory, MetaSource: NewStaticMetaSource(nil), Forward: &persistedCodecForward{target: remote}})
	require.NoError(t, err)
	read := func(m ch.Meta) ([]ConversationHeadResult, error) {
		return origin.ReadPersistedConversationHeadsResolved(ctx, []ch.ChannelID{id}, "reader", []ch.Meta{m})
	}
	heads, err := read(meta)
	require.NoError(t, err)
	require.NoError(t, heads[0].Err)
	require.Equal(t, uint64(2), heads[0].Head.Message.MessageSeq)
	require.Equal(t, uint64(1), heads[0].Head.RetentionThroughSeq)
	bad := meta
	bad.ID.ID = "different"
	heads, err = read(bad)
	require.NoError(t, err)
	require.ErrorIs(t, heads[0].Err, ch.ErrStaleMeta)
	_, err = origin.ReadPersistedConversationHeadsResolved(ctx, []ch.ChannelID{id}, "reader", nil)
	require.ErrorIs(t, err, ch.ErrInvalidConfig)
	// A changed remote leader must still reject the origin's old routing facts.
	remote.metaSource = NewStaticMetaSource([]ch.Meta{{ID: id, Epoch: 4, LeaderEpoch: 6, Leader: 1, Status: ch.StatusActive}})
	heads, err = read(meta)
	require.NoError(t, err)
	require.ErrorIs(t, heads[0].Err, ch.ErrNotLeader)
	require.Zero(t, runtime.probeCalls)
	require.Zero(t, runtime.applyCalls)
}
