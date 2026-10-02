package channels

import (
	"bytes"
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

func TestPublicationPersistedBatchCountsMetadata(t *testing.T) {
	metadata, err := publication.Encode(publication.Metadata{Source: publication.SourceMQTT, QoS: 1, AcceptedAtMS: 1000, PublisherNamespace: "n", PublisherClientID: "c", OriginalTopic: "t", Properties: []publication.Property{{Kind: publication.CorrelationData, Binary: make([]byte, 16<<10)}}})
	require.NoError(t, err)
	id := ch.ChannelID{ID: "publication-read-budget", Type: 2}
	factory := channelstore.NewMemoryFactory()
	store, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	_, err = store.AppendLeader(context.Background(), channelstore.AppendLeaderRequest{Records: []ch.Record{{ID: 1, Payload: make([]byte, 900<<10), PublicationMetadata: metadata, ServerTimestampMS: 1000}}})
	require.NoError(t, err)
	require.NoError(t, store.Close())
	meta := ch.Meta{ID: id, Leader: 1, Epoch: 1, LeaderEpoch: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	svc, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: NewStaticMetaSource([]ch.Meta{meta}), Store: factory})
	require.NoError(t, err)
	requests := make([]CommittedRead, 9)
	for i := range requests {
		requests[i] = CommittedRead{ChannelID: id, Request: channelstore.ReadCommittedRequest{Limit: 1, MaxBytes: 1 << 20}}
	}
	rows, err := svc.ReadPersistedBatch(context.Background(), requests)
	require.NoError(t, err)
	require.Len(t, rows, len(requests))
	for _, row := range rows {
		require.ErrorIs(t, row.Err, ch.ErrBackpressured, "nine bodies fit; their metadata exceeds the combined budget")
	}
}

func TestPublicationForwardedSingleReadsOwnMetadata(t *testing.T) {
	for _, head := range []bool{false, true} {
		msg := publicationCodecMessage(t)
		want := bytes.Clone(msg.PublicationMetadata)
		forward := &recordingLastVisibleForward{message: msg, ok: true}
		id := ch.ChannelID{ID: "publication-forwarded-read", Type: 2}
		meta := ch.Meta{ID: id, Leader: 2, Epoch: 1, LeaderEpoch: 1, Replicas: []ch.NodeID{2}, ISR: []ch.NodeID{2}, MinISR: 1, Status: ch.StatusActive}
		svc, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: NewStaticMetaSource([]ch.Meta{meta}), Forward: forward})
		require.NoError(t, err)
		var got ch.Message
		if head {
			result, err := svc.ReadConversationHead(context.Background(), id, "reader")
			require.NoError(t, err)
			require.True(t, result.Found)
			got = result.Message
		} else {
			var found bool
			got, found, err = svc.ReadChannelLastVisible(context.Background(), id, 0)
			require.NoError(t, err)
			require.True(t, found)
		}
		require.Equal(t, want, got.PublicationMetadata)
		clear(got.PublicationMetadata)
		require.Equal(t, want, forward.message.PublicationMetadata, "service exposed borrowed metadata")
	}
}
