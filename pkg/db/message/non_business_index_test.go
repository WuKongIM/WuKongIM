package message

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/stretchr/testify/require"
)

// appendBadgeRows exercises the shared atomic primary/index writer with explicit
// SyncOnce flags. Legacy mode reproduces rows written before the derived index.
func appendBadgeRows(t *testing.T, log *ChannelLog, base uint64, legacy bool, flags ...bool) {
	t.Helper()
	log.appendMu.Lock()
	defer log.appendMu.Unlock()
	batch := log.db.engine.NewBatch()
	defer batch.Close()
	rows := make([]messageRow, len(flags))
	for i, syncOnce := range flags {
		seq := base + uint64(i)
		rows[i] = normalizeMessageRow(messageRow{MessageSeq: seq, MessageID: seq + 100, ChannelID: log.id.ID, ChannelType: log.id.Type, Payload: []byte("record")})
		if syncOnce {
			rows[i].FramerFlags = 4
		}
		if legacy {
			require.NoError(t, log.stageMessageRow(batch, rows[i], log.appendKeyCache))
		}
	}
	if !legacy {
		require.NoError(t, log.stageMessageRows(context.Background(), batch, rows))
	}
	require.NoError(t, log.stageCatalog(batch))
	require.NoError(t, batch.Commit(true))
	log.leo.Store(base + uint64(len(flags)) - 1)
	log.loaded.Store(true)
}

func assertBadgeCount(t *testing.T, log *ChannelLog, after, through, want uint64) {
	t.Helper()
	got, err := log.CountOrdinaryMessages(context.Background(), after, through)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestOrdinaryCountExcludesInteriorAndTrailingSyncOnce(t *testing.T) {
	s := openTestMessageStore(t)
	defer s.close(t)
	log := testChannelLog(s)
	appendBadgeRows(t, log, 1, false, false, true, true, false, true, true, false, true, true, false, true)
	for _, c := range [][3]uint64{{0, 11, 4}, {1, 11, 3}, {2, 10, 3}, {4, 10, 2}, {7, 11, 1}, {10, 11, 0}, {11, 11, 0}} {
		assertBadgeCount(t, log, c[0], c[1], c[2])
	}
	assertMessageIndexEntryCount(t, log, messageIndexIDNonBusinessSeq, 7)
}

func TestOrdinaryCountBackfillReopenTruncateAndRetention(t *testing.T) {
	s := openTestMessageStore(t)
	log := testChannelLog(s)
	appendBadgeRows(t, log, 1, true, false, true, true, false, true, true, false, true, true, false)
	assertBadgeCount(t, log, 0, 10, 4)
	path := s.path
	s.close(t)
	s = openTestMessageStoreAt(t, path)
	defer s.close(t)
	log = testChannelLog(s)
	assertBadgeCount(t, log, 0, 10, 4)
	require.NoError(t, log.TruncateFrom(context.Background(), 8))
	appendBadgeRows(t, log, 8, false, true, false, true, false)
	assertBadgeCount(t, log, 0, 11, 5)
	_, err := log.TrimPrefixThrough(context.Background(), 5)
	require.NoError(t, err)
	assertBadgeCount(t, log, 5, 11, 3)
	// Remove every indexed barrier: later appends must start a new valid baseline.
	_, err = log.TrimPrefixThrough(context.Background(), 11)
	require.NoError(t, err)
	appendBadgeRows(t, log, 12, false, true, false, true, false)
	assertBadgeCount(t, log, 11, 15, 2)
	assertBadgeCount(t, log, 12, 15, 2)
}

func TestOrdinaryCountCanceledBackfillAndUncommittedStage(t *testing.T) {
	s := openTestMessageStore(t)
	defer s.close(t)
	log := testChannelLog(s)
	appendBadgeRows(t, log, 1, true, false, true, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := log.CountOrdinaryMessages(ctx, 0, 3)
	require.ErrorIs(t, err, context.Canceled)
	_, ok, err := s.engine.Get(nonBusinessVersionKey(log.key))
	require.NoError(t, err)
	require.False(t, ok)
	// A crash during a previous rebuild leaves no marker and arbitrary partial
	// index entries; the next admitted read rebuilds only from primary rows.
	batch := s.engine.NewBatch()
	require.NoError(t, batch.Set(nonBusinessIndexKey(log.key, 2), encodeUint64(99)))
	require.NoError(t, batch.Commit(true))
	require.NoError(t, batch.Close())
	assertBadgeCount(t, log, 0, 3, 2)
	batch = s.engine.NewBatch()
	log.appendMu.Lock()
	require.NoError(t, log.stageMessageRows(context.Background(), batch, []messageRow{normalizeMessageRow(messageRow{MessageSeq: 4, MessageID: 104, ChannelID: log.id.ID, ChannelType: log.id.Type, FramerFlags: 4, Payload: []byte("aborted")})}))
	require.NoError(t, batch.Close())
	log.appendMu.Unlock()
	assertMessageIndexEntryCount(t, log, messageIndexIDNonBusinessSeq, 1)
	appendBadgeRows(t, log, 4, false, true, false)
	assertBadgeCount(t, log, 0, 5, 3)
}

func TestOrdinaryCountPortableBackupRebuildsIndex(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "bytes", true: "stream"}[streaming], func(t *testing.T) {
			source := openTestMessageStore(t)
			defer source.close(t)
			log := testChannelLog(source)
			appendBadgeRows(t, log, 1, true, false, true, true, false, true, false)
			assertBadgeCount(t, log, 0, 6, 3)
			// Retained ordinal values deliberately begin above one.
			_, err := log.TrimPrefixThrough(context.Background(), 2)
			require.NoError(t, err)
			require.NoError(t, log.StoreCheckpoint(context.Background(), Checkpoint{Epoch: 1, HW: 6}))
			reader, err := source.db.OpenBackupSnapshot(context.Background(), BackupSnapshotRequest{HashSlot: 1, Channels: []BackupChannelCut{{Key: log.key, ID: log.id, Checkpoint: Checkpoint{Epoch: 1, HW: 6}}}})
			require.NoError(t, err)
			body, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.NoError(t, reader.Close())
			target := openTestMessageStore(t)
			defer target.close(t)
			if streaming {
				_, err = target.db.ImportBackupSnapshotReader(context.Background(), bytes.NewReader(body), int64(len(body)))
			} else {
				_, err = target.db.ImportBackupSnapshot(context.Background(), body)
			}
			require.NoError(t, err)
			restored := testChannelLog(target)
			assertBadgeCount(t, restored, 2, 6, 2)
			appendBadgeRows(t, restored, 7, false, true, false)
			assertBadgeCount(t, restored, 2, 8, 3)
		})
	}
}

func TestOrdinaryCountBackfillBatchesAndWarmReadAvoidsPrimaryRows(t *testing.T) {
	s := openTestMessageStore(t)
	defer s.close(t)
	log := testChannelLog(s)
	flags := make([]bool, 8200)
	for i := range flags {
		flags[i] = i%2 == 0
	}
	appendBadgeRows(t, log, 1, true, flags...)
	assertBadgeCount(t, log, 0, 8200, 4100)
	// Hide the primary span from this fixture after publication: a warm rank
	// read must depend only on the sparse index, not rescan message payloads.
	span := keycodec.NewPrefixSpan(encodeMessageRowPrefix(log.key))
	batch := s.engine.NewBatch()
	defer batch.Close()
	require.NoError(t, batch.DeleteRange(engine.Span{Start: span.Start, End: span.End}))
	require.NoError(t, batch.Commit(true))
	assertBadgeCount(t, log, 4000, 8200, 2100)
}
