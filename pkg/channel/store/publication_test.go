package store

import (
	"bytes"
	"context"
	"encoding/hex"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestPublicationContentSurvivesChannelStoreAndExactRecovery(t *testing.T) {
	for _, backend := range []string{"memory", "message_db"} {
		t.Run(backend, func(t *testing.T) {
			var factory Factory = NewMemoryFactory()
			if backend == "message_db" {
				f := NewMessageDBFactory(t.TempDir())
				t.Cleanup(func() { require.NoError(t, f.Close()) })
				factory = f
			}
			ctx := context.Background()
			id := ch.ChannelID{ID: "publication", Type: 2}
			s, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, s.Close()) })
			metadata, err := hex.DecodeString("01010100000000000003e800016e00016300017400030600017800036f6e65020000003c06000178000374776f")
			require.NoError(t, err)
			r := ch.Record{ID: 101, Epoch: 3, FromUID: "sender", ClientMsgNo: "client", ServerTimestampMS: 2000, Payload: []byte("body"), PublicationMetadata: bytes.Clone(metadata), SizeBytes: 4 + len(metadata)}
			version := ch.ProposalVersionForRecords([]ch.Record{{Expire: 10}, r})
			require.Equal(t, uint16(3), version)
			mf, _, ok := ch.SealProposalManifest(ch.ProposalManifest{Version: version, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7, CommandID: ch.CommandID{1}, LastOffset: 1}, []ch.Record{r})
			require.True(t, ok)
			req := AppendLeaderRequest{Records: []ch.Record{r}, ExactBaseOffset: true, Proposal: mf, Committed: 1}
			res, err := s.AppendLeader(ctx, req)
			require.NoError(t, err)
			require.Equal(t, AppendOutcomeDurable, res.Outcome)
			clear(r.PublicationMetadata)
			lookup := s.(ExactProposalLookup)
			query := ExactProposalRequest{CommandID: mf.CommandID, MaxRecords: 1, MaxBytes: 1 << 20}
			loaded, found, err := lookup.LoadExactProposal(ctx, query)
			require.NoError(t, err)
			require.True(t, found)
			require.Len(t, loaded.Records, 1)
			require.Equal(t, metadata, loaded.Records[0].PublicationMetadata)
			require.Equal(t, 4+len(metadata), loaded.Records[0].SizeBytes)
			clear(loaded.Records[0].PublicationMetadata)
			loaded, found, err = lookup.LoadExactProposal(ctx, query)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, metadata, loaded.Records[0].PublicationMetadata)
			proof, _, ok := ch.SealProposalManifest(loaded.Manifest, loaded.Records)
			require.True(t, ok)
			require.Equal(t, mf, proof)
			committed, err := s.ReadCommitted(ctx, ReadCommittedRequest{FromSeq: 1, Limit: 1, MaxBytes: 1 << 20})
			require.NoError(t, err)
			require.Len(t, committed.Messages, 1)
			require.Equal(t, metadata, committed.Messages[0].PublicationMetadata)
			res, err = s.AppendLeader(ctx, AppendLeaderRequest{Records: loaded.Records, ExactBaseOffset: true, Proposal: mf, Committed: 1})
			require.NoError(t, err)
			require.Equal(t, AppendOutcomeAlreadyDurable, res.Outcome)
			loaded.Records[0].PublicationMetadata[2] = 0
			_, err = s.AppendLeader(ctx, AppendLeaderRequest{Records: loaded.Records, ExactBaseOffset: true, Proposal: mf, Committed: 1})
			require.Error(t, err, "changed metadata cannot reuse the original exact proof")
			query.MaxBytes = 96 + len("sender") + len("client") + len("body")
			_, _, err = lookup.LoadExactProposal(ctx, query)
			require.Error(t, err, "metadata must count against recovery page budgets")
		})
	}
}
