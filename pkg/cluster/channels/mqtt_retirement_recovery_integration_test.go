//go:build integration

package channels

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func TestMQTTRetirementRecoverySkipsPrunedBodiesAndResumesCleanup(t *testing.T) {
	for _, hasOldBodies := range []bool{false, true} {
		name := "missing_bodies"
		if hasOldBodies {
			name = "partial_cleanup"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s, _, _, _, forward, q := mqttRecoveryFixture(t)
			paths := []string{t.TempDir(), t.TempDir()}
			factories := make([]*channelstore.MessageDBFactory, 2)
			stores := make([]channelstore.ChannelStore, 2)
			open := func(i int) {
				factories[i] = channelstore.NewMessageDBFactory(paths[i])
				var err error
				stores[i], err = factories[i].ChannelStore(ch.ChannelKeyForID(q.Source.ChannelID), q.Source.ChannelID)
				require.NoError(t, err)
			}
			open(0)
			open(1)
			t.Cleanup(func() {
				for i := range stores {
					require.NoError(t, stores[i].Close())
					require.NoError(t, factories[i].Close())
				}
			})
			previous := ch.ProposalManifest{}
			appendControl := func(version uint16, command byte, records []ch.Record) ch.ProposalManifest {
				t.Helper()
				m, _, valid := ch.SealProposalManifest(ch.ProposalManifest{Version: version, ChannelEpoch: 2, LeaderTerm: 3, FenceVersion: 4, CommandID: ch.CommandID{command}, BaseOffset: previous.LastOffset, LastOffset: previous.LastOffset + uint64(len(records)), PreviousIndex: previous.LastOffset, PreviousTerm: previous.LeaderTerm, PreviousDigest: previous.Digest}, records)
				require.True(t, valid)
				for _, st := range stores {
					_, err := st.AppendLeader(ctx, channelstore.AppendLeaderRequest{Records: records, Proposal: m, ExpectedBaseOffset: previous.LastOffset, ExactBaseOffset: true, Committed: m.LastOffset})
					require.NoError(t, err)
				}
				previous = m
				return m
			}
			control := func(id uint64, payload []byte) []ch.Record {
				return []ch.Record{{ID: id, Epoch: 2, SyncOnce: true, Payload: payload, ServerTimestampMS: int64(id)}}
			}
			activation := appendControl(4, 1, control(1, []byte(quorumlog.MQTTSourceActivationPayload)))
			q.Source.Generation = quorumlog.MQTTSourceGeneration(activation.CommandID)
			rows := make([]ch.Record, 64)
			for i := range rows {
				rows[i] = ch.Record{ID: uint64(i + 2), Epoch: 2, Payload: []byte("retired business"), ServerTimestampMS: int64(i + 2)}
			}
			appendControl(1, 2, rows)
			rangeQ := ch.MQTTReplayRange{Generation: q.Source.Generation, From: 1, Through: 65, Limit: 256, MaxBytes: 1 << 20}
			page, err := stores[0].(channelstore.MQTTReplayPreparer).PrepareMQTTReplay(ctx, rangeQ)
			require.NoError(t, err)
			if hasOldBodies {
				_, err = stores[1].(channelstore.MQTTReplayPreparer).PrepareMQTTReplay(ctx, rangeQ)
				require.NoError(t, err)
			}
			anchor := func(command byte, page ch.MQTTReplayPage) ch.MQTTReplayAnchorProof {
				a := quorumlog.MQTTReplayAnchor{SourceCommand: activation.CommandID, StartAfter: page.After.StartAfter, Through: page.After.Through, TotalBytes: page.After.TotalBytes, TotalStoredBytes: page.After.TotalStoredBytes, Digest: page.After.Digest}
				payload, err := a.MarshalBinary()
				require.NoError(t, err)
				m := appendControl(5, command, control(previous.LastOffset+1, payload))
				return ch.MQTTReplayAnchorProof{Anchor: a, Manifest: m}
			}
			first := anchor(3, page)
			appendControl(1, 4, []ch.Record{{ID: 67, Epoch: 2, Payload: []byte("retained business"), ServerTimestampMS: 67}})
			rangeQ.From, rangeQ.Through = 66, 67
			page, err = stores[0].(channelstore.MQTTReplayPreparer).PrepareMQTTReplay(ctx, rangeQ)
			require.NoError(t, err)
			last := anchor(5, page)
			retirement := quorumlog.MQTTReplayRetirement{Anchor: first.Anchor, AnchorPosition: first.Manifest.LastOffset, AnchorDigest: first.Manifest.Digest}
			payload, err := retirement.MarshalBinary()
			require.NoError(t, err)
			decision := appendControl(6, 6, control(69, payload))
			anchorState, err := stores[0].(channelstore.MQTTReplayAnchorStateReader).ReadMQTTReplayAnchors(ctx, decision.LastOffset, ch.CommandID{})
			require.NoError(t, err)
			require.True(t, anchorState.MaintenanceOnly, "adapter preserves pinned native maintenance proof")
			plan := ch.MQTTReplayPlan{Source: anchorState.Source, Anchor: anchorState.Latest, HasAnchor: anchorState.HasLatest, MaintenanceOnly: anchorState.MaintenanceOnly}
			_, more, err := plan.NextRange(256, 1<<20)
			require.NoError(t, err)
			require.False(t, more)
			_, err = stores[0].(channelstore.MQTTReplayRetirer).RetireMQTTReplay(ctx, q.Source.Generation, decision.LastOffset, 256)
			require.NoError(t, err)
			oldRange := rangeQ
			oldRange.From, oldRange.Through = 1, 65
			_, err = stores[0].(channelstore.MQTTReplayAnchorTransfer).ExportMQTTReplayAnchor(ctx, first.Manifest.LastOffset, oldRange)
			require.Error(t, err, "donor has already removed all old bodies")
			s.store = factories[1]
			q.TargetAnchor, q.ScanLimit, q.ApplyRetirement, q.ReleaseSource = last.Manifest.LastOffset, 64, true, true
			forward.fetch = func(_ context.Context, r ch.MQTTReplayRepairRequest) (ch.MQTTReplayPage, error) {
				require.Equal(t, uint64(66), r.Request.Range.From)
				require.Equal(t, uint64(67), r.Request.Range.Through)
				return stores[0].(channelstore.MQTTReplayAnchorTransfer).ExportMQTTReplayAnchor(ctx, r.AnchorPosition, r.Request.Range)
			}
			out, err := s.StepMQTTReplayRecovery(ctx, q)
			require.NoError(t, err)
			require.True(t, out.Repaired)
			require.Equal(t, hasOldBodies, out.RetirementPending)
			require.Equal(t, uint64(65), out.Plan.Current.Through)
			require.Equal(t, 1, forward.calls)
			require.NoError(t, stores[1].Close())
			require.NoError(t, factories[1].Close())
			open(1)
			s.store = factories[1]
			done, err := s.StepMQTTReplayRecovery(ctx, q)
			require.NoError(t, err)
			require.True(t, done.ValidFor(q))
			require.True(t, done.Plan.Complete)
			require.True(t, done.SourceReleased)
			require.False(t, done.RetirementPending)
			require.Equal(t, 1, forward.calls)
			got, err := stores[1].(channelstore.MQTTReplayAnchorTransfer).ExportMQTTReplayAnchor(ctx, last.Manifest.LastOffset, rangeQ)
			require.NoError(t, err)
			require.Equal(t, page, got)
			t.Logf("controlled_retirement=%d recovered_suffix=66..67 target_reopened=true cleanup_limit=64 product_acceptance=false", decision.LastOffset)
		})
	}
}
