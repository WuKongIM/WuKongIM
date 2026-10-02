package store

import (
	"context"
	"strings"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

func TestWillReceiptAdapterPreservesCommittedProofAfterTrimAndReopen(t *testing.T) {
	for _, follower := range []bool{false, true} {
		t.Run(map[bool]string{false: "leader", true: "follower"}[follower], func(t *testing.T) {
			ctx := context.Background()
			path := t.TempDir()
			factory := NewMessageDBFactory(path)
			t.Cleanup(func() { require.NoError(t, factory.Close()) })
			id := ch.ChannelID{ID: "will-receipt", Type: 2}
			cs, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, cs.Close()) })
			reader, ok := cs.(WillReceiptLookup)
			require.True(t, ok, "persistent stores must expose the retained-proof capability")
			key := "mqtt-will-v1:" + strings.Repeat("a", 64)
			metadata, err := publication.Encode(publication.Metadata{Source: publication.SourceWill, QoS: 1,
				PublisherNamespace: "n", PublisherClientID: "c", OriginalTopic: "t", ServerWillKey: key})
			require.NoError(t, err)
			records := []ch.Record{
				{ID: 10, Index: 1, Epoch: 1, FromUID: "sender", ClientMsgNo: key, Payload: []byte("native"), SizeBytes: 6},
				{ID: 11, Index: 2, Epoch: 1, FromUID: "sender", ClientMsgNo: key, Payload: []byte("body"),
					SizeBytes: 4 + len(metadata), PublicationMetadata: metadata, ServerTimestampMS: 1000},
			}
			if follower {
				_, err = cs.ApplyFollower(ctx, ApplyFollowerRequest{Records: records})
			} else {
				_, err = cs.AppendLeader(ctx, AppendLeaderRequest{Records: records})
			}
			require.NoError(t, err)
			_, found, err := reader.LookupWillReceipt(ctx, "sender", key)
			require.NoError(t, err)
			require.False(t, found, "durable append alone does not prove committed visibility")
			require.NoError(t, cs.StoreCheckpoint(ctx, ch.Checkpoint{HW: 2}))
			before, found, err := reader.LookupWillReceipt(ctx, "sender", key)
			require.NoError(t, err)
			require.True(t, found)
			require.True(t, before.Valid())
			require.EqualValues(t, 11, before.MessageID, "native client key must not alias the Will identity")
			require.EqualValues(t, 2, before.MessageSeq)
			require.EqualValues(t, 1000, before.ServerTimestampMS)
			_, found, err = reader.LookupWillReceipt(ctx, "other", key)
			require.NoError(t, err)
			require.False(t, found)
			_, err = cs.AdoptRetentionBoundary(ctx, 2, "committed")
			require.NoError(t, err)
			trim, err := cs.TrimMessagesThrough(ctx, 2, RetentionTrimOptions{MaxMessages: 2})
			require.NoError(t, err)
			require.EqualValues(t, 2, trim.Deleted)
			_, found, err = cs.(WillIdempotencyLookup).LookupWillIdempotency(ctx, "sender", key)
			require.NoError(t, err)
			require.False(t, found, "ordinary body lookup keeps its existing visibility")
			after, found, err := reader.LookupWillReceipt(ctx, "sender", key)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, before, after)
			require.NoError(t, cs.Close())
			require.NoError(t, factory.Close())
			factory = NewMessageDBFactory(path)
			cs, err = factory.ChannelStore(ch.ChannelKeyForID(id), id)
			require.NoError(t, err)
			after, found, err = cs.(WillReceiptLookup).LookupWillReceipt(ctx, "sender", key)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, before, after)
		})
	}
}

func TestWillReceiptAdapterErrorsNeverReturnProof(t *testing.T) {
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { require.NoError(t, factory.Close()) })
	id := ch.ChannelID{ID: "will-receipt-errors", Type: 2}
	cs, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cs.Close()) })
	reader := cs.(WillReceiptLookup)
	ctx := context.Background()
	key := "mqtt-will-v1:" + strings.Repeat("b", 64)
	check := func(ctx context.Context, uid, serverKey string, want error) {
		t.Helper()
		r, found, err := reader.LookupWillReceipt(ctx, uid, serverKey)
		require.ErrorIs(t, err, want)
		require.False(t, found)
		require.Equal(t, ch.WillReceipt{}, r)
	}
	check(ctx, "", key, ch.ErrInvalidConfig)
	check(ctx, "sender", "bad-key", ch.ErrInvalidConfig)
	check(ctx, strings.Repeat("u", 65536), key, ch.ErrInvalidConfig)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	check(cancelled, "sender", key, context.Canceled)
	require.NoError(t, cs.Close())
	check(ctx, "sender", key, ch.ErrClosed)
	cs, err = factory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	reader = cs.(WillReceiptLookup)
	require.NoError(t, factory.Close())
	check(ctx, "sender", key, ch.ErrClosed)
}
