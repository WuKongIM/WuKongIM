//go:build integration

package message

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func retiredReplayFixture(t *testing.T) *replayTransferFixture {
	t.Helper()
	f := repairPlanFixture(t)
	first, found, err := f.source.LoadMQTTReplayAnchor(context.Background(), 5)
	require.NoError(t, err)
	require.True(t, found)
	last, found, err := f.source.LoadMQTTReplayAnchor(context.Background(), 7)
	require.NoError(t, err)
	require.True(t, found)
	previous := last.Manifest
	for i, p := range []MQTTReplayAnchorProof{first, last} {
		m, records := replayRetirementProposal(t, previous, quorumlog.MQTTReplayRetirement{Anchor: p.Anchor, AnchorPosition: p.Manifest.LastOffset, AnchorDigest: p.Manifest.Digest}, byte(6+i))
		for _, s := range []*ChannelStore{f.source, f.target} {
			appendMQTTActivation(t, s, m, records, m.LastOffset)
		}
		previous = m
	}
	return f
}

func TestMQTTRetiredReplayPreservesSuffixRepairMeteringAndRestart(t *testing.T) {
	f := retiredReplayFixture(t)
	ctx := context.Background()
	cp, err := f.source.LoadCheckpoint()
	require.NoError(t, err)
	source, _, err := f.source.log.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	page, err := f.source.ExportMQTTReplayAnchor(ctx, 7, 5, replayTransferBudget)
	require.NoError(t, err)
	first, err := f.source.RetireMQTTReplay(ctx, f.generation, 8, 2)
	require.NoError(t, err)
	require.Equal(t, uint64(4), first.Retired.Through)
	require.Equal(t, uint64(2), first.DeletedThrough)
	require.Equal(t, 2, first.Deleted)
	require.False(t, first.Done)
	for _, position := range []uint64{1, 2} {
		_, found, err := f.sourceEngine.engine.Get(mqttReplayRowKey(f.source.log.key, f.generation, position))
		require.NoError(t, err)
		require.False(t, found)
		_, found, err = f.sourceEngine.engine.Get(mqttReplayMeterKey(f.source.log.key, f.generation, position))
		require.NoError(t, err)
		require.False(t, found)
	}
	_, err = f.source.log.ReadMQTTReplay(ctx, f.generation, 3, 4, replayTransferBudget)
	require.Error(t, err, "logical retirement hides rows before physical cleanup finishes")
	_, err = f.source.log.MeasureMQTTReplayRange(ctx, f.generation, 0, 4)
	require.Error(t, err)
	measure, err := f.source.log.MeasureMQTTReplayRange(ctx, f.generation, 4, 6)
	require.NoError(t, err)
	require.Equal(t, uint64(2), measure.Messages)
	require.Equal(t, page.After.TotalBytes-page.Before.TotalBytes, measure.Bytes)
	retired, err := f.target.RetireMQTTReplay(ctx, f.generation, 8, 1)
	require.NoError(t, err)
	require.True(t, retired.Done, "a replica without old copies can retire without downloading them")
	require.Zero(t, retired.Deleted)
	plan, err := f.target.PlanMQTTReplayRepair(ctx, f.generation, 7, 0, 64)
	require.NoError(t, err)
	require.True(t, plan.HasNext)
	require.Equal(t, uint64(4), plan.Current.Through)
	require.Equal(t, uint64(7), plan.Next.Manifest.LastOffset)
	transferred, err := f.source.ExportMQTTReplayAnchor(ctx, 7, 5, replayTransferBudget)
	require.NoError(t, err)
	require.Equal(t, page, transferred)
	_, err = f.target.ImportMQTTReplayAnchor(ctx, 7, transferred)
	require.NoError(t, err)
	_, err = f.target.ImportMQTTReplayAnchor(ctx, 5, f.all)
	require.Error(t, err, "an old transfer cannot resurrect retired content")
	require.NoError(t, f.target.Close())
	require.NoError(t, f.targetEngine.Close())
	f.targetEngine, err = Open(f.targetPath)
	require.NoError(t, err)
	f.target = mustForChannel(t, f.targetEngine, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
	plan, err = f.target.PlanMQTTReplayRepair(ctx, f.generation, 5, 0, 64)
	require.NoError(t, err)
	require.True(t, plan.Complete, "older accepted responsibility is retired")
	second, err := f.target.RetireMQTTReplay(ctx, f.generation, 9, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(6), second.Retired.Through)
	require.Equal(t, uint64(5), second.DeletedThrough)
	require.False(t, second.Done)
	done, err := f.target.RetireMQTTReplay(ctx, f.generation, 8, 1)
	require.NoError(t, err)
	require.True(t, done.Done)
	require.Equal(t, uint64(9), done.RetirementPosition)
	require.Equal(t, uint64(6), done.DeletedThrough)
	ready, err := f.target.ReadMQTTReplayReadiness(ctx, 9)
	require.NoError(t, err)
	require.True(t, ready.Covered)
	_, err = f.target.ReadMQTTReplayReadiness(ctx, 7)
	require.Error(t, err, "historical HW cannot borrow a later retirement")
	_, err = f.target.ReleaseMQTTSourceAtAnchor(ctx, f.generation, 7)
	require.NoError(t, err)
	trimmed, err := f.target.log.TrimPrefixThrough(ctx, 6)
	require.NoError(t, err)
	require.Equal(t, 6, trimmed.Deleted)
	copied, err := f.target.log.CopyMQTTReplaySource(ctx, f.generation, 7, 9, replayTransferBudget)
	require.NoError(t, err)
	require.Equal(t, uint64(9), copied.Through)
	require.Len(t, copied.Records, 3)
	require.Greater(t, copied.Records[0].TotalStoredBytes, done.Retired.TotalStoredBytes)
	cpAfter, err := f.source.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, cp, cpAfter)
	sourceAfter, _, err := f.source.log.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	require.Equal(t, source, sourceAfter)
	t.Log("mqtt_retired_storage_evidence: bounded_cleanup=true absent_copy=true suffix_repair=true retired_import_rejected=true restart=true cumulative_metering=true native_hw_unchanged=true product_admission=controlled")
}

func TestMQTTRetiredReplayBackupRestoresPartialCleanupAndEmptySuffix(t *testing.T) {
	for _, emptySuffix := range []bool{false, true} {
		t.Run(map[bool]string{false: "suffix", true: "empty"}[emptySuffix], func(t *testing.T) {
			f := retiredReplayFixture(t)
			ctx := context.Background()
			position, expectedRows := uint64(8), uint64(2)
			if emptySuffix {
				position, expectedRows = 9, 0
			}
			cut := BackupChannelCut{Key: f.source.log.key, ID: f.source.log.id, Checkpoint: Checkpoint{HW: 9}}
			unpruned := readBackupSnapshot(t, f.source.log.db, BackupSnapshotRequest{HashSlot: 1, Channels: []BackupChannelCut{cut}})
			retired, err := f.source.RetireMQTTReplay(ctx, f.generation, position, 1)
			require.NoError(t, err)
			require.False(t, retired.Done)
			body := readBackupSnapshot(t, f.source.log.db, BackupSnapshotRequest{HashSlot: 1, Channels: []BackupChannelCut{cut}})
			require.Equal(t, uint16(3), binary.BigEndian.Uint16(body[4:6]))
			restored := openTestMessageStore(t)
			defer restored.close(t)
			for range 2 {
				stats, err := restored.db.ImportBackupSnapshot(ctx, body)
				require.NoError(t, err)
				require.Equal(t, expectedRows, stats.ReplayMessageCount)
				if emptySuffix {
					require.Zero(t, stats.ReplayStoredBytes)
				}
			}
			log := mustAcquireChannel(t, restored.db, f.source.log.key, f.source.log.id)
			defer log.Close()
			finished, err := log.RetireMQTTReplay(ctx, f.generation, position, 1)
			require.NoError(t, err)
			require.True(t, finished.Done)
			require.Zero(t, finished.Deleted, "archive omits all logically retired rows")
			require.Equal(t, retired.Retired, finished.Retired)
			ready, err := log.ReadMQTTReplayReadiness(ctx, 9)
			require.NoError(t, err)
			require.True(t, ready.Covered)
			_, err = log.ReadMQTTReplay(ctx, f.generation, 1, 4, replayTransferBudget)
			require.Error(t, err)
			_, err = restored.db.ImportBackupSnapshot(ctx, unpruned)
			require.Error(t, err, "an older unpruned archive cannot reset retirement")
			_, err = f.source.log.db.ImportBackupSnapshot(ctx, body)
			require.NoError(t, err, "restore may finish an identical partial cleanup")
			finished, err = f.source.RetireMQTTReplay(ctx, f.generation, position, 1)
			require.NoError(t, err)
			require.True(t, finished.Done)
			require.Zero(t, finished.Deleted)
			badCut := cut
			badCut.Checkpoint.HW = position - 1
			stream, _, err := f.source.log.db.OpenBackupSnapshotWithStats(ctx, BackupSnapshotRequest{HashSlot: 1, Channels: []BackupChannelCut{badCut}})
			if stream != nil {
				stream.Close()
			}
			require.Error(t, err, "a historical backup cannot borrow future retirement")
			validMarker := encodeMQTTReplayRetired(cut.Key, position, retired.Retired.Through)
			require.True(t, bytes.Contains(body, validMarker))
			badBody := bytes.Replace(body, validMarker, encodeMQTTReplayRetired(cut.Key, 7, retired.Retired.Through), 1)
			binary.BigEndian.PutUint32(badBody[len(badBody)-4:], crc32.ChecksumIEEE(badBody[:len(badBody)-4]))
			invalidTarget := openTestMessageStore(t)
			defer invalidTarget.close(t)
			_, err = invalidTarget.db.ImportBackupSnapshot(ctx, badBody)
			require.Error(t, err)
			_, exists, err := invalidTarget.db.engine.Get(encodeCheckpointKey(cut.Key))
			require.NoError(t, err)
			require.False(t, exists, "preflight failure must precede any target write")
		})
	}
}

func TestMQTTRetiredReplayRejectsUncommittedOrBrokenAuthority(t *testing.T) {
	for _, fault := range []string{"pending", "retirement", "anchor", "activation", "source", "entry", "pair", "foreign", "budget", "future_frontier"} {
		t.Run(fault, func(t *testing.T) {
			f := retiredReplayFixture(t)
			key, gen, limit := f.source.log.key, f.generation, 1
			switch fault {
			case "pending":
				setPhysicalTestValue(t, f.sourceEngine, encodeCheckpointKey(key), encodeCheckpoint(Checkpoint{HW: 7}))
			case "retirement":
				deletePhysicalTestKey(t, f.sourceEngine, mqttReplayRetirementKey(key, 8))
			case "anchor":
				deletePhysicalTestKey(t, f.sourceEngine, mqttReplayAnchorKey(key, 5))
			case "activation":
				deletePhysicalTestKey(t, f.sourceEngine, mqttActivationKey(key))
			case "source":
				deletePhysicalTestKey(t, f.sourceEngine, mqttSourceKey(key))
			case "entry":
				deletePhysicalTestKey(t, f.sourceEngine, encodeEntryIdentityKey(key, 8))
			case "pair":
				deletePhysicalTestKey(t, f.sourceEngine, encodeProposalByCommandKey(key, quorumlog.CommandID{6}))
			case "foreign":
				gen = quorumlog.MQTTSourceGeneration(quorumlog.CommandID{99})
			case "budget":
				limit = 257
			case "future_frontier":
				_, err := f.source.log.CopyMQTTReplaySource(context.Background(), gen, 7, 9, replayTransferBudget)
				require.NoError(t, err)
				setPhysicalTestValue(t, f.sourceEngine, encodeCheckpointKey(key), encodeCheckpoint(Checkpoint{HW: 8}))
			}
			out, err := f.source.RetireMQTTReplay(context.Background(), gen, 8, limit)
			require.Error(t, err)
			require.Zero(t, out)
			_, exists, err := f.sourceEngine.engine.Get(mqttReplayRowKey(key, f.generation, 1))
			require.NoError(t, err)
			require.True(t, exists, "failure must not delete content")
		})
	}
}

func TestMQTTRetiredReplayResumesPartialCleanupAfterReopen(t *testing.T) {
	f := retiredReplayFixture(t)
	ctx := context.Background()
	_, err := f.target.log.CopyMQTTReplaySource(ctx, f.generation, 1, 6, replayTransferBudget)
	require.NoError(t, err)
	first, err := f.target.RetireMQTTReplay(ctx, f.generation, 8, 2)
	require.NoError(t, err)
	require.Equal(t, uint64(2), first.DeletedThrough)
	require.NoError(t, f.target.Close())
	require.NoError(t, f.targetEngine.Close())
	f.targetEngine, err = Open(f.targetPath)
	require.NoError(t, err)
	f.target = mustForChannel(t, f.targetEngine, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
	for _, through := range []uint64{3, 4} {
		out, err := f.target.RetireMQTTReplay(ctx, f.generation, 8, 1)
		require.NoError(t, err)
		require.Equal(t, through, out.DeletedThrough)
		require.Equal(t, 1, out.Deleted)
		require.Equal(t, through == 4, out.Done)
	}
	again, err := f.target.RetireMQTTReplay(ctx, f.generation, 8, 1)
	require.NoError(t, err)
	require.Zero(t, again.Deleted)
	require.True(t, again.Done)
	page, err := f.target.ExportMQTTReplayAnchor(ctx, 7, 5, replayTransferBudget)
	require.NoError(t, err)
	require.Len(t, page.Records, 2)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	out, err := f.target.RetireMQTTReplay(canceled, f.generation, 9, 1)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, out)
}

func TestMQTTRetiredReplayBrokenBaselineCannotRecoverOrRestoreBodies(t *testing.T) {
	for _, fault := range []string{"missing_state", "missing_marker", "changed_marker", "cursor_ahead", "missing_decision"} {
		t.Run(fault, func(t *testing.T) {
			f := retiredReplayFixture(t)
			ctx := context.Background()
			_, err := f.source.RetireMQTTReplay(ctx, f.generation, 9, 256)
			require.NoError(t, err)
			key := f.source.log.key
			switch fault {
			case "missing_state":
				deletePhysicalTestKey(t, f.sourceEngine, mqttReplayStateKey(key))
			case "missing_marker":
				deletePhysicalTestKey(t, f.sourceEngine, mqttReplayRetiredKey(key))
			case "changed_marker":
				value, _, err := f.sourceEngine.engine.Get(mqttReplayRetiredKey(key))
				require.NoError(t, err)
				value[len(value)-1] ^= 1
				setPhysicalTestValue(t, f.sourceEngine, mqttReplayRetiredKey(key), value)
			case "cursor_ahead":
				setPhysicalTestValue(t, f.sourceEngine, mqttReplayRetiredKey(key), encodeMQTTReplayRetired(key, 9, 7))
			case "missing_decision":
				deletePhysicalTestKey(t, f.sourceEngine, mqttReplayRetirementKey(key, 9))
			}
			_, err = f.source.PlanMQTTReplayRepair(ctx, f.generation, 7, 0, 64)
			require.Error(t, err)
			_, err = f.source.ReadMQTTReplayReadiness(ctx, 9)
			require.Error(t, err)
			_, err = f.source.ImportMQTTReplayAnchor(ctx, 5, f.all)
			require.Error(t, err)
			// Even a correctly checksummed marker must resolve its own decision.
			if fault == "changed_marker" {
				k := mqttReplayRetiredKey(key)
				setPhysicalTestValue(t, f.sourceEngine, k, rowcodec.Wrap(k, 2, rowcodec.CodecFixed, rowcodec.FlagChecksum, make([]byte, 16)))
				_, err = f.source.ReadMQTTReplayReadiness(ctx, 9)
				require.Error(t, err)
			}
		})
	}
}

type interruptReplayRestore struct {
	reader   *bytes.Reader
	boundary int64
	passes   int
	cancel   context.CancelFunc
}

func (r *interruptReplayRestore) Seek(offset int64, whence int) (int64, error) {
	if offset == 0 && whence == io.SeekStart {
		r.passes++
	}
	return r.reader.Seek(offset, whence)
}

func (r *interruptReplayRestore) Read(p []byte) (int, error) {
	position := r.reader.Size() - int64(r.reader.Len())
	if r.passes == 3 {
		if position >= r.boundary {
			r.cancel()
			return 0, context.Canceled
		}
		if int64(len(p)) > r.boundary-position {
			p = p[:r.boundary-position]
		}
	}
	return r.reader.Read(p)
}

func TestMQTTRetiredReplayInterruptedRestoreDoesNotPublishCoverage(t *testing.T) {
	for _, pruned := range []bool{false, true} {
		t.Run(map[bool]string{false: "version_2", true: "version_3"}[pruned], func(t *testing.T) {
			verifyInterruptedReplayRestore(t, pruned)
		})
	}
}

func verifyInterruptedReplayRestore(t *testing.T, pruned bool) {
	t.Helper()
	f := retiredReplayFixture(t)
	ctx := context.Background()
	state, _, err := f.source.log.LoadMQTTReplayState(ctx)
	require.NoError(t, err)
	cut := BackupChannelCut{Key: f.source.log.key, ID: f.source.log.id, Checkpoint: Checkpoint{HW: 9}}
	marker := encodeMQTTReplayState(cut.Key, state)
	if pruned {
		out, err := f.source.RetireMQTTReplay(ctx, f.generation, 8, 1)
		require.NoError(t, err)
		marker = encodeMQTTReplayRetired(cut.Key, 8, out.Retired.Through)
	}
	body := readBackupSnapshot(t, f.source.log.db, BackupSnapshotRequest{HashSlot: 1, Channels: []BackupChannelCut{cut}})
	boundary := bytes.LastIndex(body, marker)
	require.Positive(t, boundary)
	target := openTestMessageStore(t)
	defer target.close(t)
	canceled, cancel := context.WithCancel(ctx)
	defer cancel()
	reader := &interruptReplayRestore{reader: bytes.NewReader(body), boundary: int64(boundary), cancel: cancel}
	_, err = target.db.ImportBackupSnapshotReader(canceled, reader, int64(len(body)))
	require.Error(t, err)
	_, found, err := target.db.engine.Get(encodeCheckpointKey(cut.Key))
	require.NoError(t, err)
	require.True(t, found, "interruption must occur after native metadata installation")
	for _, key := range [][]byte{mqttReplayStateKey(cut.Key), mqttReplayRetiredKey(cut.Key)} {
		_, found, err = target.db.engine.Get(key)
		require.NoError(t, err)
		require.False(t, found, "incomplete suffix cannot publish coverage or a baseline")
	}
	_, err = target.db.ImportBackupSnapshot(ctx, body)
	require.NoError(t, err)
	log := mustAcquireChannel(t, target.db, cut.Key, cut.ID)
	defer log.Close()
	ready, err := log.ReadMQTTReplayReadiness(ctx, 9)
	require.NoError(t, err)
	require.True(t, ready.Covered)
}
