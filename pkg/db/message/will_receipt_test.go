package message

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/stretchr/testify/require"
)

func TestWillReceiptSurvivesTrimReopenAndCommittedBackup(t *testing.T) {
	ctx := context.Background()
	source := openTestMessageStore(t)
	defer func() { source.close(t) }()
	key, id := ChannelKey("will-receipt:2"), ChannelID{ID: "will-receipt", Type: 2}
	l := mustAcquireChannel(t, source.db, key, id)
	record := willRecord(t, 900, "a", "client")
	_, err := l.Append(ctx, []Record{{ID: 1, Payload: []byte("native")}, record, willRecord(t, 999, "b", "pending")}, AppendOptions{})
	require.NoError(t, err)
	identity := IdempotencyKey{FromUID: record.FromUID, ServerWillKey: willServerKey("a")}
	_, found, err := l.LookupWillReceipt(ctx, identity)
	require.NoError(t, err)
	require.False(t, found, "no checkpoint is not commit proof")
	require.NoError(t, l.StoreCheckpoint(ctx, Checkpoint{Epoch: 1, HW: 2}))
	receipt, found, err := l.LookupWillReceipt(ctx, identity)
	require.NoError(t, err)
	require.True(t, found)
	require.EqualValues(t, 900, receipt.MessageID)
	require.EqualValues(t, 2, receipt.MessageSeq)
	require.EqualValues(t, 1000, receipt.ServerTimestampMS)
	fingerprint, err := WillPublicationHash(record.FromUID, record.ClientMsgNo, record.Payload, record.PublicationMetadata)
	require.NoError(t, err)
	require.Equal(t, fingerprint, receipt.ContentHash)
	_, err = l.TrimPrefixThrough(ctx, 2)
	require.NoError(t, err)
	_, found, err = l.LookupIdempotency(ctx, identity)
	require.NoError(t, err)
	require.False(t, found, "ordinary index retains existing visibility semantics")
	for _, mode := range []AppendMode{AppendStrict, AppendServerAllocatedMessageID, AppendTrustedContiguous} {
		duplicate := willRecord(t, 901, "a", "changed")
		_, err = l.Append(ctx, []Record{duplicate}, AppendOptions{Mode: mode})
		require.ErrorIs(t, err, dberrors.ErrConflict)
	}
	require.NoError(t, l.Close())
	source.close(t)
	source = openTestMessageStoreAt(t, source.path)
	l = mustAcquireChannel(t, source.db, key, id)
	defer l.Close()
	got, found, err := l.LookupWillReceipt(ctx, identity)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, receipt, got)
	req := BackupSnapshotRequest{HashSlot: 7, Channels: []BackupChannelCut{{Key: key, ID: id, Checkpoint: Checkpoint{Epoch: 1, HW: 2}}}}
	stream, stats, err := source.db.OpenBackupSnapshotWithStats(ctx, req)
	require.NoError(t, err)
	body := readAllWillBackup(t, stream)
	require.EqualValues(t, 4, binary.BigEndian.Uint16(body[4:6]))
	require.EqualValues(t, 1, stats.WillReceiptCount)
	require.EqualValues(t, 900, stats.MaxMessageID)
	for _, streaming := range []bool{false, true} {
		target := openTestMessageStore(t)
		var restoredStats BackupSnapshotStats
		if streaming {
			restoredStats, err = target.db.ImportBackupSnapshotReader(ctx, bytes.NewReader(body), int64(len(body)))
		} else {
			restoredStats, err = target.db.ImportBackupSnapshot(ctx, body)
		}
		require.NoError(t, err)
		require.Equal(t, stats, restoredStats)
		restored := mustAcquireChannel(t, target.db, key, id)
		got, found, err = restored.LookupWillReceipt(ctx, identity)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, receipt, got)
		_, found, err = restored.LookupWillReceipt(ctx, IdempotencyKey{FromUID: "sender", ServerWillKey: willServerKey("b")})
		require.NoError(t, err)
		require.False(t, found)
		_, err = restored.Append(ctx, []Record{willRecord(t, 902, "a", "client")}, AppendOptions{})
		require.ErrorIs(t, err, dberrors.ErrConflict)
		require.NoError(t, restored.Close())
		target.close(t)
	}
	downgraded := bytes.Clone(body)
	binary.BigEndian.PutUint16(downgraded[4:6], 3)
	binary.BigEndian.PutUint32(downgraded[len(downgraded)-4:], crc32.ChecksumIEEE(downgraded[:len(downgraded)-4]))
	target := openTestMessageStore(t)
	defer target.close(t)
	_, err = target.db.ImportBackupSnapshotReader(ctx, bytes.NewReader(downgraded), int64(len(downgraded)))
	require.Error(t, err)
}

func TestWillReceiptSuffixRollbackAndRetainedPrefixRecovery(t *testing.T) {
	ctx := context.Background()
	e := openCompatEngine(t)
	s := mustForChannel(t, e, "will-suffix:2", channel.ChannelID{ID: "will-suffix", Type: 2})
	defer s.Close()
	a, b := willRecord(t, 100, "a", "a"), willRecord(t, 200, "b", "b")
	_, err := s.log.Append(ctx, []Record{a, b}, AppendOptions{})
	require.NoError(t, err)
	require.NoError(t, s.log.StoreCheckpoint(ctx, Checkpoint{Epoch: 1, HW: 1}))
	_, err = s.log.TrimPrefixThrough(ctx, 1)
	require.NoError(t, err)
	row := normalizeMessageRow(s.log.recordToRow(3, willRecord(t, 300, "a", "changed"), 1000))
	seen := newAppendValidationSeen(1)
	require.ErrorIs(t, s.validateRecoveryRows(ctx, []messageRow{row}, 1, &seen), dberrors.ErrConflict)
	row = normalizeMessageRow(s.log.recordToRow(3, willRecord(t, 300, "b", "changed"), 1000))
	seen = newAppendValidationSeen(1)
	require.NoError(t, s.validateRecoveryRows(ctx, []messageRow{row}, 1, &seen), "discarded suffix may reuse its identity")
	require.NoError(t, s.log.TruncateFrom(ctx, 2))
	_, present, err := e.engine.Get(willReceiptKey(s.log.key, IdempotencyKey{FromUID: "sender", ServerWillKey: willServerKey("b")}))
	require.NoError(t, err)
	require.False(t, present)
	_, err = s.log.Append(ctx, []Record{willRecord(t, 300, "b", "changed")}, AppendOptions{})
	require.NoError(t, err)
	require.NoError(t, s.log.StoreCheckpoint(ctx, Checkpoint{Epoch: 1, HW: 2}))
	got, found, err := s.log.LookupWillReceipt(ctx, IdempotencyKey{FromUID: "sender", ServerWillKey: willServerKey("a")})
	require.NoError(t, err)
	require.True(t, found)
	require.EqualValues(t, 100, got.MessageID)
}

func TestWillReceiptStrictEnvelopeAndPinnedEvidence(t *testing.T) {
	for _, mode := range []string{"bad-key", "bad-value", "missing-original", "changed-original", "missing-receipt", "uncommitted"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s := openTestMessageStore(t)
			defer s.close(t)
			l := testChannelLog(s)
			record := willRecord(t, 10, "a", "client")
			_, err := l.Append(ctx, []Record{record}, AppendOptions{})
			require.NoError(t, err)
			require.NoError(t, l.StoreCheckpoint(ctx, Checkpoint{Epoch: 1, HW: 1}))
			identity := IdempotencyKey{FromUID: "sender", ServerWillKey: willServerKey("a")}
			key := willReceiptKey(l.key, identity)
			value, found, err := s.db.engine.Get(key)
			require.NoError(t, err)
			require.True(t, found)
			batch := s.db.engine.NewBatch()
			defer batch.Close()
			switch mode {
			case "bad-key":
				identity.ServerWillKey = willServerKey("b")
				require.NoError(t, batch.Set(willReceiptKey(l.key, identity), value))
			case "bad-value":
				value[len(value)-1] ^= 1
				require.NoError(t, batch.Set(key, value))
			case "missing-original":
				require.NoError(t, batch.Delete(encodeMessageRowKey(l.key, 1, messageHeaderFamilyID)))
			case "changed-original":
				row := normalizeMessageRow(l.recordToRow(1, willRecord(t, 11, "a", "changed"), 1000))
				require.NoError(t, l.stageMessageHeaderRow(batch, row, l.appendKeyCache))
			case "missing-receipt":
				require.NoError(t, batch.Delete(key))
			case "uncommitted":
				require.NoError(t, batch.Set(encodeCheckpointKey(l.key), encodeCheckpoint(Checkpoint{Epoch: 1})))
			}
			require.NoError(t, batch.Commit(true))
			_, found, err = l.LookupWillReceipt(ctx, identity)
			if mode == "uncommitted" {
				require.NoError(t, err)
				require.False(t, found)
			} else {
				require.Error(t, err)
			}
		})
	}
	identity := IdempotencyKey{FromUID: "sender", ServerWillKey: willServerKey("a")}
	key := willReceiptKey("k", identity)
	receipt := WillReceipt{MessageSeq: 1, MessageID: 2, ServerTimestampMS: 1000, ContentHash: [32]byte{1}}
	encoded, err := encodeWillReceipt(key, receipt)
	require.NoError(t, err)
	decoded, err := decodeWillReceipt("k", key, encoded)
	require.NoError(t, err)
	require.Equal(t, receipt, decoded)
	env, err := rowcodec.Unwrap(key, encoded)
	require.NoError(t, err)
	for _, bad := range [][]byte{encoded[:len(encoded)-1], append(bytes.Clone(encoded), 0), rowcodec.Wrap(key, 2, env.Codec, env.Flags, env.Payload), rowcodec.Wrap(key, 1, env.Codec, 0, env.Payload)} {
		_, err = decodeWillReceipt("k", key, bad)
		require.Error(t, err)
	}
	for _, change := range []func(*WillReceipt){func(r *WillReceipt) { r.MessageID = 0 }, func(r *WillReceipt) { r.MessageSeq = 0 }, func(r *WillReceipt) { r.ServerTimestampMS = 0 }, func(r *WillReceipt) { r.ContentHash = [32]byte{} }} {
		bad := receipt
		change(&bad)
		_, err = encodeWillReceipt(key, bad)
		require.Error(t, err)
	}
	for _, k := range []IdempotencyKey{{FromUID: "sender", ClientMsgNo: "x"}, {FromUID: "sender", ServerWillKey: "bad"}, {ServerWillKey: willServerKey("a")}} {
		s := openTestMessageStore(t)
		_, _, err := testChannelLog(s).LookupWillReceipt(context.Background(), k)
		require.Error(t, err)
		s.close(t)
	}
}

func TestWillReceiptContentHashBindsFullPublication(t *testing.T) {
	r := willRecord(t, 1, "a", "client")
	hash, err := WillPublicationHash(r.FromUID, r.ClientMsgNo, r.Payload, r.PublicationMetadata)
	require.NoError(t, err)
	for _, change := range []func(*Record){func(r *Record) { r.FromUID = "other" }, func(r *Record) { r.ClientMsgNo = "other" }, func(r *Record) { r.Payload = []byte("different") }, func(r *Record) { r.PublicationMetadata = willRecord(t, 1, "b", "client").PublicationMetadata }} {
		other := r
		change(&other)
		got, err := WillPublicationHash(other.FromUID, other.ClientMsgNo, other.Payload, other.PublicationMetadata)
		require.NoError(t, err)
		require.NotEqual(t, hash, got)
	}
	_, err = WillPublicationHash("sender", "client", nil, nil)
	require.Error(t, err)
	_, err = WillPublicationHash("", "client", r.Payload, r.PublicationMetadata)
	require.Error(t, err)
}

func readAllWillBackup(t *testing.T, stream io.ReadCloser) []byte {
	t.Helper()
	body, err := io.ReadAll(stream)
	require.NoError(t, err)
	require.NoError(t, stream.Close())
	return body
}

func TestWillReceiptBackupRejectsInconsistentLiveContentBeforeMutation(t *testing.T) {
	ctx := context.Background()
	source := openTestMessageStore(t)
	defer source.close(t)
	l := testChannelLog(source)
	_, err := l.Append(ctx, []Record{willRecord(t, 5, "a", "client")}, AppendOptions{})
	require.NoError(t, err)
	require.NoError(t, l.StoreCheckpoint(ctx, Checkpoint{Epoch: 1, HW: 1}))
	identity := IdempotencyKey{FromUID: "sender", ServerWillKey: willServerKey("a")}
	key := willReceiptKey(l.key, identity)
	value, found, err := source.db.engine.Get(key)
	require.NoError(t, err)
	require.True(t, found)
	receipt, err := decodeWillReceipt(l.key, key, value)
	require.NoError(t, err)
	raw := backupRawEntry{Key: key, Value: value}
	require.NoError(t, validateBackupProposalSystemEntries(l.key, 1, []backupRawEntry{raw}))
	require.Error(t, validateBackupProposalSystemEntries(l.key, 1, []backupRawEntry{raw, raw}))
	require.Error(t, validateBackupProposalSystemEntries(l.key, 0, []backupRawEntry{raw}))
	body := readBackupSnapshot(t, source.db, BackupSnapshotRequest{HashSlot: 7, Channels: []BackupChannelCut{{Key: l.key, ID: l.id, Checkpoint: Checkpoint{Epoch: 1, HW: 1}}}})
	at := bytes.Index(body, value)
	require.GreaterOrEqual(t, at, 0)
	receipt.ContentHash[0] ^= 1
	bad, err := encodeWillReceipt(key, receipt)
	require.NoError(t, err)
	require.Len(t, bad, len(value))
	copy(body[at:], bad)
	binary.BigEndian.PutUint32(body[len(body)-4:], crc32.ChecksumIEEE(body[:len(body)-4]))
	target := openTestMessageStore(t)
	defer target.close(t)
	_, err = target.db.ImportBackupSnapshotReader(ctx, bytes.NewReader(body), int64(len(body)))
	require.Error(t, err)
	_, found, err = target.db.engine.Get(encodeCatalogKey(l.key))
	require.NoError(t, err)
	require.False(t, found, "preflight must precede writes")
}

func TestWillReceiptRestoreDoesNotOverwriteDifferentTargetProof(t *testing.T) {
	ctx := context.Background()
	source := openTestMessageStore(t)
	defer source.close(t)
	src := testChannelLog(source)
	_, err := src.Append(ctx, []Record{willRecord(t, 7, "a", "source")}, AppendOptions{})
	require.NoError(t, err)
	require.NoError(t, src.StoreCheckpoint(ctx, Checkpoint{Epoch: 1, HW: 1}))
	body := readBackupSnapshot(t, source.db, BackupSnapshotRequest{HashSlot: 7, Channels: []BackupChannelCut{{Key: src.key, ID: src.id, Checkpoint: Checkpoint{Epoch: 1, HW: 1}}}})
	// A legacy byte archive contains live Will rows but no System-16 projection.
	deleteReceipt := source.db.engine.NewBatch()
	require.NoError(t, deleteReceipt.Delete(willReceiptKey(src.key, IdempotencyKey{FromUID: "sender", ServerWillKey: willServerKey("a")})))
	require.NoError(t, deleteReceipt.Commit(true))
	require.NoError(t, deleteReceipt.Close())
	legacy := readBackupSnapshot(t, source.db, BackupSnapshotRequest{HashSlot: 7, Channels: []BackupChannelCut{{Key: src.key, ID: src.id, Checkpoint: Checkpoint{Epoch: 1, HW: 1}}}})
	require.EqualValues(t, 1, binary.BigEndian.Uint16(legacy[4:6]))
	target := openTestMessageStore(t)
	defer target.close(t)
	dst := testChannelLog(target)
	_, err = dst.Append(ctx, []Record{willRecord(t, 9, "a", "target")}, AppendOptions{})
	require.NoError(t, err)
	require.NoError(t, dst.StoreCheckpoint(ctx, Checkpoint{Epoch: 1, HW: 1}))
	id := IdempotencyKey{FromUID: "sender", ServerWillKey: willServerKey("a")}
	before, found, err := dst.LookupWillReceipt(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	_, err = target.db.ImportBackupSnapshotReader(ctx, bytes.NewReader(body), int64(len(body)))
	require.ErrorIs(t, err, dberrors.ErrConflict)
	after, found, err := dst.LookupWillReceipt(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, before, after)
	_, err = target.db.ImportBackupSnapshot(ctx, legacy)
	require.ErrorIs(t, err, dberrors.ErrConflict)
	after, found, err = dst.LookupWillReceipt(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, before, after)
}

func TestWillReceiptBackupRequiresPhysicalTrimWitness(t *testing.T) {
	ctx := context.Background()
	source := openTestMessageStore(t)
	defer source.close(t)
	log := testChannelLog(source)
	_, err := log.Append(ctx, []Record{willRecord(t, 99, "a", "client")}, AppendOptions{})
	require.NoError(t, err)
	require.NoError(t, log.StoreCheckpoint(ctx, Checkpoint{Epoch: 1, HW: 1}))
	_, err = log.TrimPrefixThrough(ctx, 1)
	require.NoError(t, err)
	body := readBackupSnapshot(t, source.db, BackupSnapshotRequest{HashSlot: 7, Channels: []BackupChannelCut{{Key: log.key, ID: log.id, Checkpoint: Checkpoint{Epoch: 1, HW: 1}}}})
	old, found, err := source.db.engine.Get(encodeRetentionStateKey(log.key))
	require.NoError(t, err)
	require.True(t, found)
	retention, err := decodeRetentionState(old)
	require.NoError(t, err)
	retention.PhysicalRetentionThroughSeq = 0
	changed := encodeRetentionState(retention)
	require.Len(t, changed, len(old))
	at := bytes.Index(body, old)
	require.GreaterOrEqual(t, at, 0)
	copy(body[at:], changed)
	binary.BigEndian.PutUint32(body[len(body)-4:], crc32.ChecksumIEEE(body[:len(body)-4]))
	target := openTestMessageStore(t)
	defer target.close(t)
	_, err = target.db.ImportBackupSnapshotReader(ctx, bytes.NewReader(body), int64(len(body)))
	require.Error(t, err)
	_, found, err = target.db.engine.Get(encodeCatalogKey(log.key))
	require.NoError(t, err)
	require.False(t, found)
}

func TestWillReceiptLegacyTrimMaterializesExactProofAndBoundsLookup(t *testing.T) {
	ctx := context.Background()
	s := openTestMessageStore(t)
	defer s.close(t)
	log := testChannelLog(s)
	record := willRecord(t, 21, "a", "client")
	_, err := log.Append(ctx, []Record{record}, AppendOptions{})
	require.NoError(t, err)
	require.NoError(t, log.StoreCheckpoint(ctx, Checkpoint{Epoch: 1, HW: 1}))
	id := IdempotencyKey{FromUID: "sender", ServerWillKey: willServerKey("a")}
	b := s.db.engine.NewBatch()
	require.NoError(t, b.Delete(willReceiptKey(log.key, id)))
	require.NoError(t, b.Commit(true))
	require.NoError(t, b.Close())
	_, found, err := log.LookupIdempotency(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	_, err = log.TrimPrefixThrough(ctx, 1)
	require.NoError(t, err)
	receipt, found, err := log.LookupWillReceipt(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	require.EqualValues(t, 21, receipt.MessageID)
	require.NotPanics(t, func() {
		_, _, err = log.LookupWillReceipt(ctx, IdempotencyKey{FromUID: string(bytes.Repeat([]byte{'u'}, 65536)), ServerWillKey: willServerKey("a")})
		require.Error(t, err)
	})
}
