package store

import (
	"context"
	"encoding/hex"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestPublicationStorageRejectsInvalidContentBeforeMutation(t *testing.T) {
	for _, backend := range []string{"memory", "message_db"} {
		for _, operation := range []string{"append", "exact", "follower", "replace", "append_batch", "follower_batch"} {
			if backend == "memory" && (operation == "append_batch" || operation == "follower_batch") {
				continue
			}
			t.Run(backend+"/"+operation, func(t *testing.T) {
				var factory Factory = NewMemoryFactory()
				if backend == "message_db" {
					f := NewMessageDBFactory(t.TempDir())
					t.Cleanup(func() { require.NoError(t, f.Close()) })
					factory = f
				}
				id := ch.ChannelID{ID: "invalid-publication", Type: 2}
				key := ch.ChannelKeyForID(id)
				s, err := factory.ChannelStore(key, id)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, s.Close()) })
				ctx := context.Background()
				records := []ch.Record{{ID: 101, Index: 1, Epoch: 1, Payload: []byte("valid"), ServerTimestampMS: 1000}, {ID: 102, Index: 2, Epoch: 1, Payload: []byte("invalid"), ServerTimestampMS: 1000, PublicationMetadata: []byte{1}}}
				manifest, _, ok := ch.SealProposalManifest(ch.ProposalManifest{Version: 3, ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1, CommandID: ch.CommandID{1}, LastOffset: 2}, records)
				require.True(t, ok, "a valid content hash is not semantic validation")
				switch operation {
				case "append", "exact":
					req := AppendLeaderRequest{Records: records}
					if operation == "exact" {
						req.ExactBaseOffset = true
						req.Proposal = manifest
						req.Committed = 2
					}
					result, e := s.AppendLeader(ctx, req)
					err = e
					require.Equal(t, AppendOutcomeDefinitelyNotWritten, result.Outcome)
				case "follower":
					_, err = s.ApplyFollower(ctx, ApplyFollowerRequest{Records: records, LeaderHW: 2})
				case "replace":
					result, e := s.(RecoverySuffixReplacer).ReplaceRecoverySuffix(ctx, ReplaceRecoverySuffixRequest{Proposals: []RecoveryProposal{{Manifest: manifest, Records: records}}, Committed: 2})
					err = e
					require.Equal(t, AppendOutcomeDefinitelyNotWritten, result.Outcome)
				case "append_batch":
					results := factory.(*MessageDBFactory).AppendLeaderBatch(ctx, []AppendLeaderBatchItem{{ChannelKey: key, ChannelID: id, Request: AppendLeaderRequest{Records: records}}})
					require.Len(t, results, 1)
					err = results[0].Err
					require.Equal(t, AppendOutcomeDefinitelyNotWritten, results[0].Outcome)
				case "follower_batch":
					results := factory.(*MessageDBFactory).ApplyFollowerBatch(ctx, []ApplyFollowerBatchItem{{ChannelKey: key, ChannelID: id, Request: ApplyFollowerRequest{Records: records, LeaderHW: 2}}})
					require.Len(t, results, 1)
					err = results[0].Err
				}
				require.ErrorIs(t, err, ch.ErrInvalidConfig)
				state, err := s.Load(ctx)
				require.NoError(t, err)
				require.Zero(t, state.LEO)
				require.Zero(t, state.HW)
			})
		}
	}
}

func TestPublicationStorageSizeHintCannotHideContent(t *testing.T) {
	metadata, err := hex.DecodeString("01010100000000000003e800016e00016300017400030600017800036f6e65020000003c06000178000374776f")
	require.NoError(t, err)
	for _, backend := range []string{"memory", "message_db"} {
		for _, follower := range []bool{false, true} {
			var factory Factory = NewMemoryFactory()
			if backend == "message_db" {
				f := NewMessageDBFactory(t.TempDir())
				t.Cleanup(func() { require.NoError(t, f.Close()) })
				factory = f
			}
			id := ch.ChannelID{ID: "publication-size-hint", Type: 2}
			s, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, s.Close()) })
			records := []ch.Record{{ID: 1, Index: 1, Epoch: 1, Payload: []byte("body"), PublicationMetadata: metadata, SizeBytes: 4, ServerTimestampMS: 1000}, {ID: 2, Index: 2, Epoch: 1, Payload: []byte("body"), PublicationMetadata: metadata, SizeBytes: 4, ServerTimestampMS: 1000}}
			if follower {
				_, err = s.ApplyFollower(context.Background(), ApplyFollowerRequest{Records: records, LeaderHW: 2})
			} else {
				_, err = s.AppendLeader(context.Background(), AppendLeaderRequest{Records: records})
			}
			require.NoError(t, err)
			page, err := s.ReadCommitted(context.Background(), ReadCommittedRequest{FromSeq: 1, Limit: 2, MaxBytes: 4 + len(metadata)})
			require.NoError(t, err)
			require.Len(t, page.Messages, 1, "metadata bypassed %s read budget", backend)
			log, err := s.ReadLog(context.Background(), ReadLogRequest{FromOffset: 1, MaxBytes: 4 + len(metadata)})
			require.NoError(t, err)
			require.Len(t, log.Records, 1)
			require.Equal(t, 4+len(metadata), log.Records[0].SizeBytes)
		}
	}
}
