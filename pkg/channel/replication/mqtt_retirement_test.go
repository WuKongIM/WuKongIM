package replication

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func TestMQTTRetirementRejectsStoresWithoutJournalCapability(t *testing.T) {
	st, err := NewStoreAdapter(StoreAdapterConfig{Factory: channelstore.NewMemoryFactory(), MaxBatchItems: 4, MaxBatchBytes: 4096})
	require.NoError(t, err)
	r := quorumlog.MQTTReplayRetirement{Anchor: quorumlog.MQTTReplayAnchor{SourceCommand: ch.CommandID{1}, Through: 1, TotalStoredBytes: 100, Digest: ch.EntryDigest{1}}, AnchorPosition: 2, AnchorDigest: ch.EntryDigest{2}}
	body, err := r.MarshalBinary()
	require.NoError(t, err)
	record := ch.Record{ID: 3, Epoch: 1, ServerTimestampMS: 1000, SyncOnce: true, Payload: body, SizeBytes: len(body)}
	manifest, _, ok := ch.SealProposalManifest(ch.ProposalManifest{Version: quorumlog.MQTTReplayRetirementProposalManifestVersion, ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1, CommandID: ch.CommandID{3}, BaseOffset: 2, LastOffset: 3, PreviousIndex: 2, PreviousTerm: 1, PreviousDigest: ch.EntryDigest{2}}, []ch.Record{record})
	require.True(t, ok)
	result := st.Sync(context.Background(), []Mutation{{ChannelKey: "1:source", ChannelID: ch.ChannelID{ID: "source", Type: 1}, Manifest: manifest, Records: []ch.Record{record}, Committed: 2}})
	require.Len(t, result, 1)
	require.ErrorIs(t, result[0].Err, ch.ErrInvalidConfig)
	require.False(t, result[0].Outcome.Durable())
	replaced := st.Replace(context.Background(), []RecoveryReplacement{{ChannelKey: "1:source", ChannelID: ch.ChannelID{ID: "source", Type: 1}, Proposals: []RecoveryProposal{{Manifest: manifest, Records: []ch.Record{record}}}, Committed: 3}})
	require.Len(t, replaced, 1)
	require.ErrorIs(t, replaced[0].Err, ch.ErrInvalidConfig)
	require.False(t, replaced[0].Outcome.Durable())
}
