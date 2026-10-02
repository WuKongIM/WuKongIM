package message

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
	"github.com/stretchr/testify/require"
)

func replayFixture(t *testing.T, l *ChannelLog) {
	t.Helper()
	ctx := context.Background()
	metadata := publicationFixture(t)
	_, err := l.Append(ctx, []Record{
		{ID: 11, Payload: []byte("first"), PublicationMetadata: metadata, ServerTimestampMS: 1234},
		{ID: 12, Payload: []byte("second"), ServerTimestampMS: 1234},
		{ID: 13, Payload: []byte("third"), ServerTimestampMS: 1234},
	}, AppendOptions{})
	require.NoError(t, err)
	require.NoError(t, l.StoreCheckpoint(ctx, Checkpoint{Epoch: 1, HW: 3}))
	require.NoError(t, l.ApplyMQTTSourceState(ctx, 0, MQTTSourceState{Generation: "g", Revision: 1}))
}

func TestMQTTReplaySharedContentSurvivesSourceDeletionReopenAndBackup(t *testing.T) {
	ctx := context.Background()
	s := openTestMessageStore(t)
	defer func() { s.close(t) }()
	l := testChannelLog(s)
	id, key := l.id, l.key
	replayFixture(t, l)
	opts := ReadOptions{Limit: 256, MaxBytes: 16 << 20}
	page, err := l.CopyMQTTReplaySource(ctx, "g", 1, 3, opts)
	require.NoError(t, err)
	require.Len(t, page.Records, 3)
	require.Equal(t, uint64(3), page.Through)
	for _, r := range page.Records {
		original, ok, err := s.db.engine.Get(encodeMessageRowKey(key, r.Position, 0))
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, original, r.Content)
	}
	measure, err := l.MeasureMQTTReplayRange(ctx, "g", 1, 3)
	require.NoError(t, err)
	require.Equal(t, uint64(2), measure.Messages)
	require.Equal(t, uint64(11), measure.Bytes)
	all, err := l.MeasureMQTTReplayRange(ctx, "g", 0, 3)
	require.NoError(t, err)
	require.Equal(t, uint64(16+len(publicationFixture(t))), all.Bytes)
	source, _, err := l.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	require.Zero(t, source.CopiedThrough, "local copy cannot release the source")
	source.Revision, source.CopiedThrough, source.ReceiptDigest = 2, 3, page.Digest
	require.NoError(t, l.ApplyMQTTSourceState(ctx, 1, source))
	trim, err := l.TrimPrefixThrough(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, 3, trim.Deleted)
	retry, err := l.CopyMQTTReplaySource(ctx, "g", 1, 3, opts)
	require.NoError(t, err)
	require.Equal(t, page, retry)
	clear(retry.Records[0].Content)
	read, err := l.ReadMQTTReplay(ctx, "g", 1, 3, opts)
	require.NoError(t, err)
	require.Equal(t, page, read)
	require.NoError(t, l.Close())
	s.close(t)
	s = openTestMessageStoreAt(t, s.path)
	l = mustAcquireChannel(t, s.db, key, id)
	defer l.Close()
	read, err = l.ReadMQTTReplay(ctx, "g", 1, 3, opts)
	require.NoError(t, err)
	require.Equal(t, page, read)
	request := BackupSnapshotRequest{HashSlot: 1, Channels: []BackupChannelCut{{Key: key, ID: id, Checkpoint: Checkpoint{Epoch: 1, HW: 3}}}}
	stream, stats, err := s.db.OpenBackupSnapshotWithStats(ctx, request)
	require.NoError(t, err)
	body, err := io.ReadAll(stream)
	require.NoError(t, err)
	require.NoError(t, stream.Close())
	require.Equal(t, uint16(2), binary.BigEndian.Uint16(body[4:6]))
	require.Zero(t, stats.MessageCount)
	require.Equal(t, uint64(3), stats.ReplayMessageCount)
	require.Equal(t, uint64(13), stats.MaxMessageID)
	for _, streaming := range []bool{false, true} {
		target := openTestMessageStore(t)
		defer target.close(t)
		for range 2 {
			var imported BackupSnapshotStats
			if streaming {
				imported, err = target.db.ImportBackupSnapshotReader(ctx, bytes.NewReader(body), int64(len(body)))
			} else {
				imported, err = target.db.ImportBackupSnapshot(ctx, body)
			}
			require.NoError(t, err)
			require.Equal(t, stats, imported)
		}
		restored := mustAcquireChannel(t, target.db, key, id)
		defer restored.Close()
		read, err := restored.ReadMQTTReplay(ctx, "g", 1, 3, opts)
		require.NoError(t, err)
		require.Equal(t, page, read)
		_, ok, err := target.db.engine.Get(encodeGlobalMessageIDIndexKey(11))
		require.NoError(t, err)
		require.False(t, ok, "replay must not reinsert the ordinary global ID")
	}
	request.Channels[0].Checkpoint.HW = 2
	stream, err = s.db.OpenBackupSnapshot(ctx, request)
	if err == nil {
		_, err = io.ReadAll(stream)
		stream.Close()
	}
	require.Error(t, err, "backup cut below replay frontier")
}

func TestMQTTReplayCopyAtomicBoundsAndGapFailures(t *testing.T) {
	ctx := context.Background()
	s := openTestMessageStore(t)
	defer s.close(t)
	l := testChannelLog(s)
	defer l.Close()
	replayFixture(t, l)
	opts := ReadOptions{Limit: 2, MaxBytes: 4096}
	for _, req := range []struct {
		g             string
		from, through uint64
		opts          ReadOptions
	}{
		{"g", 0, 1, opts}, {"g", 2, 3, opts}, {"wrong", 1, 3, opts}, {"g", 1, 4, opts},
		{"g", 1, 3, ReadOptions{}}, {"g", 1, 3, ReadOptions{Limit: 257, MaxBytes: 4096}},
		{"g", 1, 3, ReadOptions{Limit: 1, MaxBytes: 17 << 20}}, {"g", 1, 3, ReadOptions{Limit: 1, MaxBytes: 1}},
	} {
		_, err := l.CopyMQTTReplaySource(ctx, req.g, req.from, req.through, req.opts)
		require.Error(t, err, "%+v", req)
	}
	_, ok, err := l.LoadMQTTReplayState(ctx)
	require.NoError(t, err)
	require.False(t, ok)
	// A missing later source position must roll back the entire attempted page.
	key := encodeMessageRowKey(l.key, 2, 0)
	original, _, err := s.db.engine.Get(key)
	require.NoError(t, err)
	b := s.db.engine.NewBatch()
	defer b.Close()
	require.NoError(t, b.Delete(key))
	require.NoError(t, b.Commit(true))
	_, err = l.CopyMQTTReplaySource(ctx, "g", 1, 3, opts)
	require.ErrorIs(t, err, dberrors.ErrCorruptState)
	_, ok, err = l.LoadMQTTReplayState(ctx)
	require.NoError(t, err)
	require.False(t, ok)
	b = s.db.engine.NewBatch()
	defer b.Close()
	require.NoError(t, b.Set(key, original))
	require.NoError(t, b.Commit(true))
	first, err := l.CopyMQTTReplaySource(ctx, "g", 1, 3, opts)
	require.NoError(t, err)
	require.Equal(t, uint64(2), first.Through)
	_, err = l.CopyMQTTReplaySource(ctx, "g", 2, 3, opts)
	require.ErrorIs(t, err, dberrors.ErrConflict)
	last, err := l.CopyMQTTReplaySource(ctx, "g", 3, 3, opts)
	require.NoError(t, err)
	other := openTestMessageStore(t)
	defer other.close(t)
	otherLog := mustAcquireChannel(t, other.db, l.key, l.id)
	defer otherLog.Close()
	replayFixture(t, otherLog)
	whole, err := otherLog.CopyMQTTReplaySource(ctx, "g", 1, 3, ReadOptions{Limit: 3, MaxBytes: 4096})
	require.NoError(t, err)
	require.Equal(t, whole.Digest, last.Digest, "batch boundaries must not affect the proof")
	_, err = l.ReadMQTTReplay(ctx, "wrong", 1, 3, opts)
	require.Error(t, err)
	_, err = l.ReadMQTTReplay(ctx, "g", 1, 4, opts)
	require.Error(t, err)
	_, err = l.MeasureMQTTReplayRange(ctx, "g", 0, 4)
	require.Error(t, err)
}

func TestMQTTReplaySchemaAndCorruption(t *testing.T) {
	require.NoError(t, schema.ValidateTable(MQTTReplayTable))
	require.Equal(t, uint32(2), MQTTReplayTable.ID)
	ctx := context.Background()
	s := openTestMessageStore(t)
	defer s.close(t)
	l := testChannelLog(s)
	defer l.Close()
	replayFixture(t, l)
	opts := ReadOptions{Limit: 3, MaxBytes: 4096}
	_, err := l.CopyMQTTReplaySource(ctx, "g", 1, 3, opts)
	require.NoError(t, err)
	key := mqttReplayRowKey(l.key, "g", 2)
	value, ok, err := s.db.engine.Get(key)
	require.NoError(t, err)
	require.True(t, ok)
	for i := 0; i < len(value); i++ {
		_, err := decodeMQTTReplayRecord(l.key, "g", 2, value[:i])
		require.Error(t, err, "truncated at %d", i)
	}
	_, err = decodeMQTTReplayRecord(l.key, "other", 2, value)
	require.Error(t, err)
	b := s.db.engine.NewBatch()
	defer b.Close()
	require.NoError(t, b.Delete(key))
	require.NoError(t, b.Commit(true))
	_, err = l.ReadMQTTReplay(ctx, "g", 1, 3, opts)
	require.True(t, errors.Is(err, dberrors.ErrCorruptState), "%v", err)
}

func TestMQTTReplayBackupPinnedViewAndPreflight(t *testing.T) {
	ctx := context.Background()
	s := openTestMessageStore(t)
	defer s.close(t)
	l := testChannelLog(s)
	defer l.Close()
	replayFixture(t, l)
	opts := ReadOptions{Limit: 3, MaxBytes: 4096}
	request := BackupSnapshotRequest{Channels: []BackupChannelCut{{Key: l.key, ID: l.id, Checkpoint: Checkpoint{Epoch: 1, HW: 3}}}}
	native := readBackupSnapshot(t, s.db, request)
	require.Equal(t, uint16(1), binary.BigEndian.Uint16(native[4:6]))
	first, err := l.CopyMQTTReplaySource(ctx, "g", 1, 2, opts)
	require.NoError(t, err)
	stream, stats, err := s.db.OpenBackupSnapshotWithStats(ctx, request)
	require.NoError(t, err)
	_, err = l.CopyMQTTReplaySource(ctx, "g", 3, 3, opts)
	require.NoError(t, err)
	body, err := io.ReadAll(stream)
	require.NoError(t, err)
	require.NoError(t, stream.Close())
	require.Equal(t, uint64(2), stats.ReplayMessageCount)
	target := openTestMessageStore(t)
	defer target.close(t)
	imported, err := target.db.ImportBackupSnapshotReader(ctx, bytes.NewReader(body), int64(len(body)))
	require.NoError(t, err)
	require.Equal(t, stats, imported)
	restored := mustAcquireChannel(t, target.db, l.key, l.id)
	defer restored.Close()
	page, err := restored.ReadMQTTReplay(ctx, "g", 1, 2, opts)
	require.NoError(t, err)
	require.Equal(t, first, page)
	_, err = restored.ReadMQTTReplay(ctx, "g", 1, 3, opts)
	require.Error(t, err)
	// Tamper the last copy of this envelope (the replay, not the ordinary row)
	// and fix the outer stream CRC so semantic preflight must detect it.
	bad := bytes.Clone(body)
	offset := bytes.LastIndex(bad, first.Records[0].Content)
	require.GreaterOrEqual(t, offset, 0)
	bad[offset+len(first.Records[0].Content)-1] ^= 1
	binary.BigEndian.PutUint32(bad[len(bad)-4:], crc32.ChecksumIEEE(bad[:len(bad)-4]))
	for _, streaming := range []bool{false, true} {
		empty := openTestMessageStore(t)
		defer empty.close(t)
		if streaming {
			_, err = empty.db.ImportBackupSnapshotReader(ctx, bytes.NewReader(bad), int64(len(bad)))
		} else {
			_, err = empty.db.ImportBackupSnapshot(ctx, bad)
		}
		require.Error(t, err)
		_, present, err := empty.db.engine.Get(encodeCatalogKey(l.key))
		require.NoError(t, err)
		require.False(t, present, "invalid replay must fail before ordinary rows are imported")
	}
}

func TestMQTTReplayCannotExtendAcrossMissingDurableTail(t *testing.T) {
	ctx := context.Background()
	s := openTestMessageStore(t)
	defer s.close(t)
	l := testChannelLog(s)
	defer l.Close()
	replayFixture(t, l)
	opts := ReadOptions{Limit: 3, MaxBytes: 4096}
	_, err := l.CopyMQTTReplaySource(ctx, "g", 1, 2, opts)
	require.NoError(t, err)
	b := s.db.engine.NewBatch()
	defer b.Close()
	require.NoError(t, b.Delete(mqttReplayRowKey(l.key, "g", 2)))
	require.NoError(t, b.Commit(true))
	_, err = l.CopyMQTTReplaySource(ctx, "g", 3, 3, opts)
	require.ErrorIs(t, err, dberrors.ErrCorruptState)
	state, _, err := l.LoadMQTTReplayState(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), state.Through)
}

func TestMQTTReplayImportCannotRegressExistingCoverage(t *testing.T) {
	ctx := context.Background()
	s := openTestMessageStore(t)
	defer s.close(t)
	l := testChannelLog(s)
	defer l.Close()
	replayFixture(t, l)
	request := BackupSnapshotRequest{Channels: []BackupChannelCut{{Key: l.key, ID: l.id, Checkpoint: Checkpoint{Epoch: 1, HW: 3}}}}
	native := readBackupSnapshot(t, s.db, request)
	opts := ReadOptions{Limit: 3, MaxBytes: 4096}
	_, err := l.CopyMQTTReplaySource(ctx, "g", 1, 2, opts)
	require.NoError(t, err)
	older := readBackupSnapshot(t, s.db, request)
	page, err := l.CopyMQTTReplaySource(ctx, "g", 3, 3, opts)
	require.NoError(t, err)
	source := MQTTSourceState{Generation: "g", Revision: 2, CopiedThrough: 3, ReceiptDigest: page.Digest}
	require.NoError(t, l.ApplyMQTTSourceState(ctx, 1, source))
	for _, body := range [][]byte{native, older} {
		for _, streaming := range []bool{false, true} {
			if streaming {
				_, err = s.db.ImportBackupSnapshotReader(ctx, bytes.NewReader(body), int64(len(body)))
			} else {
				_, err = s.db.ImportBackupSnapshot(ctx, body)
			}
			require.ErrorIs(t, err, dberrors.ErrConflict)
			got, _, err := l.LoadMQTTSourceState(ctx)
			require.NoError(t, err)
			require.Equal(t, source, got, "conflicting replay must not first overwrite source protection")
		}
	}
}

func TestMQTTReplayMeterIndexRestoredAndMissingEndpointFails(t *testing.T) {
	ctx := context.Background()
	s := openTestMessageStore(t)
	defer s.close(t)
	l := testChannelLog(s)
	defer l.Close()
	replayFixture(t, l)
	_, err := l.CopyMQTTReplaySource(ctx, "g", 1, 3, ReadOptions{Limit: 3, MaxBytes: 4096})
	require.NoError(t, err)
	request := BackupSnapshotRequest{Channels: []BackupChannelCut{{Key: l.key, ID: l.id, Checkpoint: Checkpoint{Epoch: 1, HW: 3}}}}
	body := readBackupSnapshot(t, s.db, request)
	target := openTestMessageStore(t)
	defer target.close(t)
	_, err = target.db.ImportBackupSnapshot(ctx, body)
	require.NoError(t, err)
	restored := mustAcquireChannel(t, target.db, l.key, l.id)
	defer restored.Close()
	measure, err := restored.MeasureMQTTReplayRange(ctx, "g", 1, 3)
	require.NoError(t, err)
	require.Equal(t, MQTTReplayMeasure{Messages: 2, Bytes: 11}, measure)
	key := mqttReplayMeterKey(l.key, "g", 2)
	value, present, err := target.db.engine.Get(key)
	require.NoError(t, err)
	require.True(t, present)
	require.Less(t, len(value), 64, "range counting must use bounded metadata")
	b := target.db.engine.NewBatch()
	defer b.Close()
	require.NoError(t, b.Delete(key))
	require.NoError(t, b.Commit(true))
	_, err = restored.MeasureMQTTReplayRange(ctx, "g", 1, 2)
	require.ErrorIs(t, err, dberrors.ErrCorruptState)
}

func TestMQTTReplayProofIgnoresReplicaLocalSizeHints(t *testing.T) {
	ctx := context.Background()
	var pages []MQTTReplayPage
	for _, sizeHint := range []int{0, 4096} {
		s := openTestMessageStore(t)
		defer s.close(t)
		l := testChannelLog(s)
		defer l.Close()
		_, err := l.Append(ctx, []Record{{ID: 31, Payload: []byte("same original content"), ServerTimestampMS: 1234, SizeBytes: sizeHint}}, AppendOptions{})
		require.NoError(t, err)
		require.NoError(t, l.StoreCheckpoint(ctx, Checkpoint{HW: 1}))
		require.NoError(t, l.ApplyMQTTSourceState(ctx, 0, MQTTSourceState{Generation: "g", Revision: 1}))
		page, err := l.CopyMQTTReplaySource(ctx, "g", 1, 1, ReadOptions{Limit: 1, MaxBytes: 4096})
		require.NoError(t, err)
		pages = append(pages, page)
	}
	require.Equal(t, pages[0], pages[1], "replicas with the same committed publication must agree on content and proof")
}
