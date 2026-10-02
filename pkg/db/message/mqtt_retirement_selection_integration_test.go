//go:build integration

package message

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func TestMQTTRetirementSelectionSurvivesLaterAnchorsTrimRestartAndRestore(t *testing.T) {
	f := repairPlanFixture(t)
	ctx := context.Background()
	page, err := f.target.SelectMQTTReplayRetirementAnchor(ctx, f.generation, 7, 5, 0, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(7), page.BeforeAnchor)
	expected, found, err := f.target.LoadMQTTReplayAnchor(ctx, 5)
	require.NoError(t, err)
	require.True(t, found)
	// A newly committed journal cannot change a previously captured upper bound.
	body, err := page.Captured.Anchor.MarshalBinary()
	require.NoError(t, err)
	r, err := compatibilityRecordFromRow(messageRow{MessageID: 1200, ChannelID: "activation", ChannelType: 1, FramerFlags: 4, ServerTimestampMS: 12000, Payload: body})
	require.NoError(t, err)
	r.Epoch = 1
	m := page.Captured.Manifest
	m.CommandID = quorumlog.CommandID{6}
	m.BaseOffset, m.LastOffset, m.PreviousIndex, m.PreviousDigest = 7, 8, 7, m.Digest
	m = sealCompatProposalManifest(t, m, []channel.Record{r})
	appendMQTTActivation(t, f.target, m, []channel.Record{r}, 8)
	// Controlled source release isolates selection from ordinary-history cleanup.
	source, _, err := f.target.log.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	released := source
	released.Revision++
	released.CopiedThrough, released.ReceiptDigest = 6, [32]byte{9}
	require.NoError(t, f.target.log.ApplyMQTTSourceState(ctx, source.Revision, released))
	trimmed, err := f.target.log.TrimPrefixThrough(ctx, 6)
	require.NoError(t, err)
	require.Equal(t, 6, trimmed.Deleted)
	require.NoError(t, f.target.Close())
	require.NoError(t, f.targetEngine.Close())
	f.targetEngine, err = Open(f.targetPath)
	require.NoError(t, err)
	f.target = mustForChannel(t, f.targetEngine, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
	next, err := f.target.SelectMQTTReplayRetirementAnchor(ctx, f.generation, 7, 5, page.BeforeAnchor, 1)
	require.NoError(t, err)
	require.True(t, next.Done)
	require.True(t, next.HasCandidate)
	require.Equal(t, expected, next.Candidate)
	require.Equal(t, page.Captured, next.Captured)
	old, err := f.target.SelectMQTTReplayRetirementAnchor(ctx, f.generation, 5, 4, 0, 1)
	require.NoError(t, err)
	require.Equal(t, expected, old.Candidate)
	cut := BackupChannelCut{Key: f.target.log.key, ID: f.target.log.id, Checkpoint: Checkpoint{HW: 8}}
	backup := readBackupSnapshot(t, f.target.log.db, BackupSnapshotRequest{HashSlot: 1, Channels: []BackupChannelCut{cut}})
	restored := openTestMessageStore(t)
	defer restored.close(t)
	_, err = restored.db.ImportBackupSnapshot(ctx, backup)
	require.NoError(t, err)
	log := mustAcquireChannel(t, restored.db, f.target.log.key, f.target.log.id)
	defer log.Close()
	again, err := log.SelectMQTTReplayRetirementAnchor(ctx, f.generation, 7, 5, page.BeforeAnchor, 1)
	require.NoError(t, err)
	require.Equal(t, next, again)
	_, present, err := log.LoadMQTTReplayState(ctx)
	require.NoError(t, err)
	require.False(t, present)
	t.Log("mqtt_retirement_selection_evidence: real_disk=true bounded_reverse_page=true original_trim=true restart=true backup_restore=true capture_stable=true local_replay_absent=true consumer_admission=controlled physical_gc=false product_listener=false")
}

func TestMQTTRetirementSelectionRejectsBrokenProofAndContinuation(t *testing.T) {
	for _, fault := range []string{"capture_journal", "candidate_changed", "candidate_entry", "candidate_pair", "checkpoint", "activation", "source", "capture_pending", "cursor_missing", "cursor_business", "cursor_eligible", "generation", "changed_floor", "excess_floor"} {
		t.Run(fault, func(t *testing.T) {
			f := repairPlanFixture(t)
			key, generation, floor, before := f.target.log.key, f.generation, uint64(5), uint64(0)
			switch fault {
			case "capture_journal":
				deletePhysicalTestKey(t, f.targetEngine, mqttReplayAnchorKey(key, 7))
			case "candidate_changed":
				k := mqttReplayAnchorKey(key, 5)
				v, _, err := f.targetEngine.engine.Get(k)
				require.NoError(t, err)
				env, err := rowcodec.UnwrapBorrowed(k, v)
				require.NoError(t, err)
				changed := append([]byte(nil), env.Payload...)
				changed[len(changed)-1] ^= 1
				setPhysicalTestValue(t, f.targetEngine, k, rowcodec.Wrap(k, 1, rowcodec.CodecFixed, rowcodec.FlagChecksum, changed))
			case "candidate_entry":
				deletePhysicalTestKey(t, f.targetEngine, encodeEntryIdentityKey(key, 5))
			case "candidate_pair":
				deletePhysicalTestKey(t, f.targetEngine, encodeProposalByCommandKey(key, quorumlog.CommandID{3}))
			case "checkpoint":
				deletePhysicalTestKey(t, f.targetEngine, encodeCheckpointKey(key))
			case "activation":
				deletePhysicalTestKey(t, f.targetEngine, mqttActivationKey(key))
			case "source":
				deletePhysicalTestKey(t, f.targetEngine, mqttSourceKey(key))
			case "capture_pending":
				setPhysicalTestValue(t, f.targetEngine, encodeCheckpointKey(key), encodeCheckpoint(Checkpoint{HW: 6}))
			case "cursor_missing":
				before, floor = 5, 3
				deletePhysicalTestKey(t, f.targetEngine, mqttReplayAnchorKey(key, 5))
			case "cursor_business":
				before = 6
			case "cursor_eligible":
				before = 5
			case "generation":
				generation = quorumlog.MQTTSourceGeneration(quorumlog.CommandID{9})
			case "changed_floor":
				before, floor = 7, 6
			case "excess_floor":
				floor = 7
			}
			result, err := f.target.SelectMQTTReplayRetirementAnchor(context.Background(), generation, 7, floor, before, 64)
			require.Error(t, err)
			require.Zero(t, result)
		})
	}
	f := repairPlanFixture(t)
	for _, limit := range []int{-1, 0, 65} {
		result, err := f.target.SelectMQTTReplayRetirementAnchor(context.Background(), f.generation, 7, 5, 0, limit)
		require.Error(t, err)
		require.Zero(t, result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := f.target.SelectMQTTReplayRetirementAnchor(ctx, f.generation, 7, 5, 0, 64)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, result)
	require.NoError(t, f.target.Close())
	result, err = f.target.SelectMQTTReplayRetirementAnchor(context.Background(), f.generation, 7, 5, 0, 64)
	require.Error(t, err)
	require.Zero(t, result)
}
