package message

import (
	"context"
	"math"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func TestMQTTSourceAnchorReleasePreservesSuffixAndSurvivesRestore(t *testing.T) {
	f := repairPlanFixture(t)
	ctx := context.Background()
	before, _, err := f.source.log.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	checkpoint, err := f.source.LoadCheckpoint()
	require.NoError(t, err)
	proof, found, err := f.source.LoadMQTTReplayAnchor(ctx, 5)
	require.NoError(t, err)
	require.True(t, found)
	// The local prefix extends beyond this anchor; only its authenticated
	// historical endpoint may release originals.
	released, err := f.source.ReleaseMQTTSourceAtAnchor(ctx, f.generation, 5)
	require.NoError(t, err)
	want := before
	want.Revision, want.CopiedThrough, want.ReceiptDigest = 2, 4, proof.Manifest.Digest
	require.Equal(t, want, released)
	trim, err := f.source.log.TrimPrefixThrough(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, 4, trim.Deleted)
	require.Equal(t, uint64(4), trim.DeletedThroughSeq)
	rows, err := f.source.log.ReadMQTTProtectedSource(ctx, f.generation, 5, 7, replayTransferBudget)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	page, err := f.source.ExportMQTTReplayAnchor(ctx, 5, 1, replayTransferBudget)
	require.NoError(t, err)
	// A native replica with the same committed anchor cannot release until it
	// has independently imported the anchored shared content.
	_, err = f.target.ReleaseMQTTSourceAtAnchor(ctx, f.generation, 5)
	require.Error(t, err)
	_, err = f.target.ImportMQTTReplayAnchor(ctx, 5, page)
	require.NoError(t, err)
	got, err := f.target.ReleaseMQTTSourceAtAnchor(ctx, f.generation, 5)
	require.NoError(t, err)
	require.Equal(t, released, got)
	page, err = f.source.ExportMQTTReplayAnchor(ctx, 7, 5, replayTransferBudget)
	require.NoError(t, err)
	_, err = f.target.ImportMQTTReplayAnchor(ctx, 7, page)
	require.NoError(t, err)
	for _, s := range []*ChannelStore{f.source, f.target} {
		got, err = s.ReleaseMQTTSourceAtAnchor(ctx, f.generation, 7)
		require.NoError(t, err)
		require.Equal(t, uint64(3), got.Revision)
		require.Equal(t, uint64(6), got.CopiedThrough)
		for _, position := range []uint64{7, 5, 7} {
			retry, err := s.ReleaseMQTTSourceAtAnchor(ctx, f.generation, position)
			require.NoError(t, err)
			require.Equal(t, got, retry)
		}
		_, err = s.log.TrimPrefixThrough(ctx, 7)
		require.NoError(t, err)
		cp, err := s.LoadCheckpoint()
		require.NoError(t, err)
		require.Equal(t, checkpoint, cp)
		retention, _, err := s.log.LoadRetentionState(ctx)
		require.NoError(t, err)
		require.Equal(t, uint64(7), retention.LocalRetentionThroughSeq)
		require.Equal(t, uint64(6), retention.PhysicalRetentionThroughSeq)
	}
	require.NoError(t, f.target.Close())
	require.NoError(t, f.targetEngine.Close())
	f.targetEngine, err = Open(f.targetPath)
	require.NoError(t, err)
	f.target = mustForChannel(t, f.targetEngine, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
	retry, err := f.target.ReleaseMQTTSourceAtAnchor(ctx, f.generation, 5)
	require.NoError(t, err)
	require.Equal(t, got, retry)
	key, id := f.source.log.key, f.source.log.id
	body := readBackupSnapshot(t, f.source.log.db, BackupSnapshotRequest{HashSlot: 1,
		Channels: []BackupChannelCut{{Key: key, ID: id, Checkpoint: Checkpoint{HW: 7}}}})
	restored := openTestMessageStore(t)
	defer restored.close(t)
	_, err = restored.db.ImportBackupSnapshot(ctx, body)
	require.NoError(t, err)
	l := mustAcquireChannel(t, restored.db, key, id)
	defer l.Close()
	retry, err = l.ReleaseMQTTSourceAtAnchor(ctx, f.generation, 7)
	require.NoError(t, err)
	require.Equal(t, got, retry)
	actual, err := l.ExportMQTTReplayAnchor(ctx, 7, 5, replayTransferBudget)
	require.NoError(t, err)
	require.Equal(t, page, actual)
}

func TestMQTTSourceAnchorReleaseRejectsIncompleteEvidenceWithoutMutation(t *testing.T) {
	for _, fault := range []string{"absent_anchor", "pending", "journal", "entry", "command", "activation", "source", "checkpoint", "generation", "no_content", "partial", "tail", "meter", "digest", "overflow", "physical_boundary", "cancel"} {
		t.Run(fault, func(t *testing.T) {
			f := repairPlanFixture(t)
			ctx := context.Background()
			l, eng := f.source.log, f.sourceEngine
			generation, position := f.generation, uint64(7)
			switch fault {
			case "absent_anchor":
				position = 6
			case "pending":
				setPhysicalTestValue(t, eng, encodeCheckpointKey(l.key), encodeCheckpoint(Checkpoint{HW: 6}))
			case "journal":
				deletePhysicalTestKey(t, eng, mqttReplayAnchorKey(l.key, 7))
			case "entry":
				deletePhysicalTestKey(t, eng, encodeEntryIdentityKey(l.key, 7))
			case "command":
				deletePhysicalTestKey(t, eng, encodeProposalByCommandKey(l.key, quorumlog.CommandID{5}))
			case "activation":
				deletePhysicalTestKey(t, eng, mqttActivationKey(l.key))
			case "source":
				deletePhysicalTestKey(t, eng, mqttSourceKey(l.key))
			case "checkpoint":
				deletePhysicalTestKey(t, eng, encodeCheckpointKey(l.key))
			case "generation":
				generation = "wrong-generation"
			case "no_content", "partial":
				l, eng = f.target.log, f.targetEngine
				if fault == "partial" {
					_, err := l.CopyMQTTReplaySource(ctx, generation, 1, 4, replayTransferBudget)
					require.NoError(t, err)
				}
			case "tail":
				deletePhysicalTestKey(t, eng, mqttReplayRowKey(l.key, generation, 6))
			case "meter":
				position = 5
				deletePhysicalTestKey(t, eng, mqttReplayMeterKey(l.key, generation, 4))
			case "digest":
				s, _, err := l.LoadMQTTReplayState(ctx)
				require.NoError(t, err)
				s.Digest[0]++
				setPhysicalTestValue(t, eng, mqttReplayStateKey(l.key), encodeMQTTReplayState(l.key, s))
			case "overflow":
				s, err := l.ReleaseMQTTSourceAtAnchor(ctx, generation, 5)
				require.NoError(t, err)
				s.Revision = math.MaxUint64
				setPhysicalTestValue(t, eng, mqttSourceKey(l.key), encodeMQTTSourceState(mqttSourceKey(l.key), s))
			case "physical_boundary":
				// Use the retention writer so the fixture also invalidates its
				// warm read cache; raw engine writes intentionally bypass it.
				require.NoError(t, l.StoreRetentionState(ctx, RetentionState{LocalRetentionThroughSeq: 2, PhysicalRetentionThroughSeq: 2, RetainedMaxSeq: 7}))
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			before, present, err := eng.engine.Get(mqttSourceKey(l.key))
			require.NoError(t, err)
			got, err := l.ReleaseMQTTSourceAtAnchor(ctx, generation, position)
			require.Error(t, err)
			require.Zero(t, got)
			after, found, readErr := eng.engine.Get(mqttSourceKey(l.key))
			require.NoError(t, readErr)
			require.Equal(t, present, found)
			require.Equal(t, before, after)
		})
	}
}

func TestMQTTSourceAnchorReleaseRevalidatesRetryAndLease(t *testing.T) {
	f := repairPlanFixture(t)
	ctx := context.Background()
	_, err := f.source.ReleaseMQTTSourceAtAnchor(ctx, f.generation, 5)
	require.NoError(t, err)
	deletePhysicalTestKey(t, f.sourceEngine, encodeEntryIdentityKey(f.source.log.key, 5))
	_, err = f.source.ReleaseMQTTSourceAtAnchor(ctx, f.generation, 5)
	require.Error(t, err)
	for _, args := range []struct {
		generation string
		position   uint64
	}{{"", 7}, {f.generation, 0}} {
		_, err = f.source.log.ReleaseMQTTSourceAtAnchor(ctx, args.generation, args.position)
		require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
	}
	require.NoError(t, f.source.Close())
	_, err = f.source.ReleaseMQTTSourceAtAnchor(ctx, f.generation, 5)
	require.Error(t, err)
}

func TestMQTTSourceAnchorReleaseSerializesWithRetention(t *testing.T) {
	f := repairPlanFixture(t)
	var wg sync.WaitGroup
	errors := make(chan error, 12)
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			if i%3 == 0 {
				_, err = f.source.log.TrimPrefixThrough(context.Background(), 7)
			} else {
				_, err = f.source.ReleaseMQTTSourceAtAnchor(context.Background(), f.generation, uint64(5+2*(i%2)))
			}
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	s, _, err := f.source.log.LoadMQTTSourceState(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(6), s.CopiedThrough)
	require.Contains(t, []uint64{2, 3}, s.Revision)
	rows, err := f.source.log.ReadMQTTProtectedSource(context.Background(), f.generation, 7, 7, replayTransferBudget)
	require.NoError(t, err)
	require.Len(t, rows, 1)
}
