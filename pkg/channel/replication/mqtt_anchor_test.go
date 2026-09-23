package replication

import (
	"context"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMQTTAnchorRejectsStoreWithoutJournalCapability(t *testing.T) {
	st, err := NewStoreAdapter(StoreAdapterConfig{Factory: channelstore.NewMemoryFactory(), MaxBatchItems: 4, MaxBatchBytes: 4096})
	require.NoError(t, err)
	body, err := (quorumlog.MQTTReplayAnchor{SourceCommand: ch.CommandID{1}, Through: 1, TotalStoredBytes: 100, Digest: ch.EntryDigest{1}}).MarshalBinary()
	require.NoError(t, err)
	record := ch.Record{ID: 2, Epoch: 1, ServerTimestampMS: 1000, SyncOnce: true, Payload: body, SizeBytes: len(body)}
	manifest, _, ok := ch.SealProposalManifest(ch.ProposalManifest{Version: quorumlog.MQTTReplayAnchorProposalManifestVersion, ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1, CommandID: ch.CommandID{2}, BaseOffset: 1, LastOffset: 2, PreviousIndex: 1, PreviousTerm: 1, PreviousDigest: ch.EntryDigest{3}}, []ch.Record{record})
	require.True(t, ok)
	result := st.Sync(context.Background(), []Mutation{{ChannelKey: "1:source", ChannelID: ch.ChannelID{ID: "source", Type: 1}, Manifest: manifest, Records: []ch.Record{record}, Committed: 1}})
	require.Len(t, result, 1)
	require.ErrorIs(t, result[0].Err, ch.ErrInvalidConfig)
	require.False(t, result[0].Outcome.Durable())
}
