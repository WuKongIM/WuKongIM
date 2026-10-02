package message

import (
	"context"
	"testing"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func TestMQTTConsumerMessagesPreserveFieldsAndNativeClassificationAfterTrim(t *testing.T) {
	f := repairPlanFixture(t)
	ctx := context.Background()
	raw := f.page(t, 1, 6)
	expected, err := f.source.ReadMQTTReplayMessages(ctx, f.generation, 7, 1, 6, replayTransferBudget)
	require.NoError(t, err)
	require.Len(t, expected.Records, 6)
	for i, r := range expected.Records {
		original, e := mqttReplayOriginalRow(f.source.log.key, uint64(i+1), raw.Records[i].Content)
		require.NoError(t, e)
		require.Equal(t, channelMessageFromRow(original), r.Message)
		require.Equal(t, i == 0 || i == 4, r.Internal)
		require.Equal(t, raw.Records[i].ContentHash, r.ContentHash)
		require.Equal(t, raw.Records[i].TotalBytes, r.TotalBytes)
	}
	_, err = f.source.ReleaseMQTTSourceAtAnchor(ctx, f.generation, 7)
	require.NoError(t, err)
	trimmed, err := f.source.log.TrimPrefixThrough(ctx, 6)
	require.NoError(t, err)
	require.Equal(t, 6, trimmed.Deleted)
	got, err := f.source.ReadMQTTReplayMessages(ctx, f.generation, 7, 1, 6, replayTransferBudget)
	require.NoError(t, err)
	require.Equal(t, expected, got)
	// Install two independently verified intervals, then reopen the receiver.
	for _, interval := range [][2]uint64{{5, 1}, {7, 5}} {
		page, e := f.source.ExportMQTTReplayAnchor(ctx, interval[0], interval[1], replayTransferBudget)
		require.NoError(t, e)
		_, e = f.target.ImportMQTTReplayAnchor(ctx, interval[0], page)
		require.NoError(t, e)
	}
	require.NoError(t, f.target.Close())
	require.NoError(t, f.targetEngine.Close())
	f.targetEngine, err = Open(f.targetPath)
	require.NoError(t, err)
	f.target = mustForChannel(t, f.targetEngine, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
	got, err = f.target.ReadMQTTReplayMessages(ctx, f.generation, 7, 1, 6, replayTransferBudget)
	require.NoError(t, err)
	require.Equal(t, expected, got)
	clear(got.Records[1].Message.Payload)
	clear(got.Records[1].Message.PublicationMetadata)
	again, err := f.target.ReadMQTTReplayMessages(ctx, f.generation, 7, 1, 6, replayTransferBudget)
	require.NoError(t, err)
	require.Equal(t, expected, again)
}

func TestMQTTConsumerMessagesRejectMissingNativeProofWithoutPartialResult(t *testing.T) {
	for _, fault := range []string{"entry", "command", "paired", "foreign_entry"} {
		t.Run(fault, func(t *testing.T) {
			f := repairPlanFixture(t)
			key := f.source.log.key
			switch fault {
			case "entry":
				deletePhysicalTestKey(t, f.sourceEngine, encodeEntryIdentityKey(key, 3))
			case "command":
				deletePhysicalTestKey(t, f.sourceEngine, encodeProposalByCommandKey(key, f.business.CommandID))
			case "paired":
				deletePhysicalTestKey(t, f.sourceEngine, encodeProposalByLastKey(key, f.business.LastOffset))
			case "foreign_entry":
				entry, ok, e := loadDurableEntryIdentityFrom(f.sourceEngine.engine, key, 3)
				require.NoError(t, e)
				require.True(t, ok)
				entry.Digest[0] ^= 1
				setPhysicalTestValue(t, f.sourceEngine, encodeEntryIdentityKey(key, 3), encodeDurableEntryIdentity(entry))
			}
			got, e := f.source.ReadMQTTReplayMessages(context.Background(), f.generation, 7, 2, 4, replayTransferBudget)
			require.Error(t, e)
			require.Zero(t, got)
		})
	}
}

func TestMQTTConsumerMessagesDoNotClassifyPayloadOrSyncOnceAsControl(t *testing.T) {
	f := repairPlanFixture(t)
	ctx := context.Background()
	previous, found, err := f.source.LoadMQTTReplayAnchor(ctx, 7)
	require.NoError(t, err)
	require.True(t, found)
	var records []channel.Record
	for i, flags := range []uint8{0, 4} {
		r, e := compatibilityRecordFromRow(messageRow{MessageID: uint64(810 + i), ChannelID: "activation", ChannelType: 1, FromUID: "alice", FramerFlags: flags, Payload: []byte(quorumlog.MQTTSourceActivationPayload), ServerTimestampMS: 9000})
		require.NoError(t, e)
		r.Epoch = 1
		records = append(records, r)
	}
	m := previous.Manifest
	next := sealCompatProposalManifest(t, DurableProposalManifest{Version: 1, ChannelEpoch: 1, LeaderTerm: 2, FenceVersion: 2, CommandID: quorumlog.CommandID{8}, BaseOffset: 7, LastOffset: 9, PreviousIndex: 7, PreviousTerm: m.LeaderTerm, PreviousDigest: m.Digest}, records)
	appendMQTTActivation(t, f.source, next, records, 9)
	_, err = f.source.log.CopyMQTTReplaySource(ctx, f.generation, 7, 9, replayTransferBudget)
	require.NoError(t, err)
	p, _, err := f.source.log.LoadMQTTReplayState(ctx)
	require.NoError(t, err)
	a := quorumlog.MQTTReplayAnchor{SourceCommand: f.activation.CommandID, StartAfter: p.StartAfter, Through: p.Through, TotalBytes: p.TotalBytes, TotalStoredBytes: p.TotalStoredBytes, Digest: p.Digest}
	body, err := a.MarshalBinary()
	require.NoError(t, err)
	r, err := compatibilityRecordFromRow(messageRow{MessageID: 910, ChannelID: "activation", ChannelType: 1, FramerFlags: 4, Payload: body, ServerTimestampMS: 9100})
	require.NoError(t, err)
	r.Epoch = 1
	anchor := sealCompatProposalManifest(t, DurableProposalManifest{Version: 5, ChannelEpoch: 1, LeaderTerm: 2, FenceVersion: 2, CommandID: quorumlog.CommandID{9}, BaseOffset: 9, LastOffset: 10, PreviousIndex: 9, PreviousTerm: 2, PreviousDigest: next.Digest}, []channel.Record{r})
	appendMQTTActivation(t, f.source, anchor, []channel.Record{r}, 10)
	got, err := f.source.ReadMQTTReplayMessages(ctx, f.generation, 10, 7, 9, replayTransferBudget)
	require.NoError(t, err)
	require.Len(t, got.Records, 3)
	require.True(t, got.Records[0].Internal)
	require.False(t, got.Records[1].Internal)
	require.False(t, got.Records[2].Internal)
	require.True(t, got.Records[2].Message.Framer.SyncOnce)
}
