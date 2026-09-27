package meta

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

const (
	maxSlotSnapshotStreamEntryBytes = 256 << 20
	slotSnapshotImportBatchEntries  = 1024
	slotSnapshotImportBatchBytes    = 16 << 20
)

type portableSnapshotEntryScope uint8

const (
	portableSnapshotAllEntries portableSnapshotEntryScope = iota
	portableSnapshotBackupEntries
)

// portableSnapshotEntryOrder validates the registry-defined traversal emitted
// by snapshot export while materializing spans for only one Hash Slot at a time.
type portableSnapshotEntryOrder struct {
	hashSlots   []HashSlot
	scope       portableSnapshotEntryScope
	hashSlotIdx int
	spans       []Span
	spanIndex   int
	previousKey []byte
}

// ImportHashSlotSnapshotReader validates then installs a seekable portable
// snapshot without retaining the complete hash-slot payload in memory.
func (db *MetaDB) ImportHashSlotSnapshotReader(ctx context.Context, hashSlots []uint16, reader io.ReadSeeker, size int64) error {
	return db.importHashSlotSnapshotReader(ctx, hashSlots, reader, size, portableSnapshotAllEntries, false, false, nil, nil)
}

// ImportHashSlotSnapshotReaderPreservingMigrationMeta installs semantic data
// while retaining target-local migration workflow rows.
func (db *MetaDB) ImportHashSlotSnapshotReaderPreservingMigrationMeta(ctx context.Context, hashSlots []uint16, reader io.ReadSeeker, size int64) error {
	return db.importHashSlotSnapshotReader(ctx, hashSlots, reader, size, portableSnapshotBackupEntries, true, false, nil, nil)
}

// ImportHashSlotSnapshotReaderForRestore installs semantic data while
// retaining target-local migration rows and optionally clearing every restored
// user and device token as the rows enter the target database.
func (db *MetaDB) ImportHashSlotSnapshotReaderForRestore(ctx context.Context, hashSlots []uint16, reader io.ReadSeeker, size int64, invalidateTokens bool) error {
	return db.importHashSlotSnapshotReader(ctx, hashSlots, reader, size, portableSnapshotBackupEntries, true, invalidateTokens, nil, nil)
}

// ImportHashSlotSnapshotReaderForRestoreWithStats installs semantic data and
// returns the exact record count authenticated by the portable stream.
func (db *MetaDB) ImportHashSlotSnapshotReaderForRestoreWithStats(ctx context.Context, hashSlots []uint16, reader io.ReadSeeker, size int64, invalidateTokens bool) (BackupSnapshotStats, error) {
	var stats BackupSnapshotStats
	err := db.importHashSlotSnapshotReader(ctx, hashSlots, reader, size, portableSnapshotBackupEntries, true, invalidateTokens, &stats, nil)
	return stats, err
}

// VerifyBackupHashSlotSnapshotReader validates a complete portable metadata
// stream and its exact Hash Slot ownership without mutating the database.
func VerifyBackupHashSlotSnapshotReader(
	ctx context.Context,
	hashSlots []uint16,
	reader io.ReadSeeker,
	size int64,
) (BackupSnapshotStats, error) {
	normalized, err := normalizeSnapshotHashSlots(hashSlots)
	if err != nil {
		return BackupSnapshotStats{}, err
	}
	if err := verifySeekableSnapshotChecksum(reader, size); err != nil {
		return BackupSnapshotStats{}, err
	}
	streamSlots, entryCount, err := visitSlotSnapshotStream(
		ctx, reader, size, portableSnapshotBackupEntries,
		func(_, _ []byte) error { return nil },
	)
	if err != nil {
		return BackupSnapshotStats{}, err
	}
	if !equalUint16HashSlots(streamSlots, uint16HashSlots(normalized)) {
		return BackupSnapshotStats{},
			fmt.Errorf("%w: snapshot hash slots do not match request", dberrors.ErrInvalidArgument)
	}
	return BackupSnapshotStats{EntryCount: entryCount}, nil
}

func (db *MetaDB) importHashSlotSnapshotReader(ctx context.Context, hashSlots []uint16, reader io.ReadSeeker, size int64, scope portableSnapshotEntryScope, preserveMigrationMeta, invalidateTokens bool, stats *BackupSnapshotStats, startup *startupRestore) error {
	if err := checkSnapshotDB(ctx, db); err != nil {
		return err
	}
	normalized, err := normalizeSnapshotHashSlots(hashSlots)
	if err != nil {
		return err
	}
	report := func(p SnapshotRestoreProgress) {
		if startup != nil && startup.report != nil {
			startup.report(p)
		}
	}
	tracked := &snapshotValidationReader{ReadSeeker: reader, ctx: ctx, size: size, stage: "snapshot_checksum", report: report}
	if startup != nil && reader != nil {
		reader = tracked
	}
	report(SnapshotRestoreProgress{Stage: "snapshot_checksum", TotalBytes: size})
	if err := verifySeekableSnapshotChecksum(reader, size); err != nil {
		if canceled := contextErr(ctx); canceled != nil {
			return canceled
		}
		return err
	}
	tracked.stage = "snapshot_validate"
	report(SnapshotRestoreProgress{Stage: "snapshot_validate", TotalBytes: size})
	streamSlots, entryCount, err := visitSlotSnapshotStream(
		ctx, reader, size, scope,
		func(_, _ []byte) error { return nil },
	)
	if err != nil {
		return err
	}
	if !equalUint16HashSlots(streamSlots, uint16HashSlots(normalized)) {
		return fmt.Errorf("%w: snapshot hash slots do not match request", dberrors.ErrInvalidArgument)
	}
	if stats != nil {
		stats.EntryCount = entryCount
	}

	if err := contextErr(ctx); err != nil {
		return err
	}
	tracked.stage = ""
	report(SnapshotRestoreProgress{Stage: "snapshot_prepare", TotalBytes: size, TotalEntries: int64(entryCount)})
	if err := contextErr(ctx); err != nil {
		return err
	}
	unlock := db.lockHashSlots(normalized)
	defer unlock()
	deleteBatch := db.engine.NewBatch()
	// Startup admission and the validated ownership set make these mutations
	// Slot-scoped. Keep unrelated proofs; this Slot remains uncertified until
	// the caller establishes its new snapshot anchor after complete installation.
	classifyStartupBatch := func(batch *engine.Batch) {
		if startup != nil && db.recoveryEnabled {
			batch.PreserveRecoveryCertificateAt(recoveryCheckpointKey(startup.slotID), 0)
		}
	}
	classifyStartupBatch(deleteBatch)
	for _, hashSlot := range normalized {
		for _, span := range hashSlotSnapshotReplaceSpans(hashSlot, preserveMigrationMeta) {
			if err := deleteBatch.DeleteRange(engine.Span{Start: span.Start, End: span.End}); err != nil {
				_ = deleteBatch.Close()
				return err
			}
		}
	}
	if startup != nil {
		if err := deleteBatch.Delete(recoveryCheckpointKey(startup.slotID)); err != nil {
			_ = deleteBatch.Close()
			return err
		}
		if err := deleteBatch.Set(encodeSlotRestorePendingKey(startup.slotID), slotIndexValue(startup.index)); err != nil {
			_ = deleteBatch.Close()
			return err
		}
		if err := deleteBatch.Set(encodeSlotAppliedIndexKey(startup.slotID), slotIndexValue(0)); err != nil {
			_ = deleteBatch.Close()
			return err
		}
	}
	if err := deleteBatch.Commit(true); err != nil {
		_ = deleteBatch.Close()
		return err
	}
	if err := deleteBatch.Close(); err != nil {
		return err
	}

	report(SnapshotRestoreProgress{Stage: "snapshot_install", TotalBytes: size, TotalEntries: int64(entryCount)})
	// Invalidate even when a later chunk fails after the old ranges were deleted.
	defer db.clearChannelCache()
	maxEntries, maxBytes := slotSnapshotImportBatchEntries, slotSnapshotImportBatchBytes
	if startup != nil {
		maxEntries, maxBytes = 64<<10, 8<<20
	}
	var installedEntries, installedBytes int64

	newBatch := db.engine.NewBatch
	if startup != nil {
		capacity := maxBytes + maxEntries*11 + 16
		if size < int64(capacity) {
			capacity = int(size) + 16
		}
		newBatch = func() *engine.Batch {
			batch := db.engine.NewBatchWithSize(capacity)
			classifyStartupBatch(batch)
			return batch
		}
	}
	batch := newBatch()
	batchEntries := 0
	batchBytes := 0
	flush := func() error {
		if batchEntries == 0 {
			return nil
		}
		if err := batch.Commit(true); err != nil {
			return err
		}
		if err := batch.Close(); err != nil {
			return err
		}
		installedEntries += int64(batchEntries)
		installedBytes += int64(batchBytes)
		report(SnapshotRestoreProgress{Stage: "snapshot_install", Bytes: installedBytes, TotalBytes: size, Entries: installedEntries, TotalEntries: int64(entryCount)})
		batch = newBatch()
		batchEntries = 0
		batchBytes = 0
		return nil
	}
	_, _, err = visitSlotSnapshotStream(ctx, reader, size, scope, func(key, value []byte) error {
		entry := snapshotEntry{Key: key, Value: value}
		if invalidateTokens {
			entry.Value, err = invalidateSnapshotAuthenticationToken(entry.Key, entry.Value, normalized)
			if err != nil {
				return err
			}
		}
		// Flush before the next record would exceed the byte bound. One
		// format-allowed large record may occupy its own batch.
		if batchEntries > 0 && batchBytes+len(key)+len(entry.Value) > maxBytes {
			if err := flush(); err != nil {
				return err
			}
		}
		if preserveMigrationMeta {
			if err := db.stageSlotSnapshotEntry(batch, entry, normalized, true); err != nil {
				return err
			}
		} else if err := batch.Set(entry.Key, entry.Value); err != nil {
			return err
		}
		batchEntries++
		batchBytes += len(key) + len(entry.Value)
		if batchEntries >= maxEntries || batchBytes >= maxBytes {
			return flush()
		}
		return nil
	})
	if canceled := contextErr(ctx); canceled != nil {
		err = canceled
	}
	if err == nil {
		err = flush()
	}
	closeErr := batch.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	if startup != nil {
		final := db.engine.NewBatch()
		defer final.Close()
		classifyStartupBatch(final)
		if err := final.Set(encodeSlotAppliedIndexKey(startup.slotID), slotIndexValue(startup.index)); err != nil {
			return err
		}
		if err := final.Delete(encodeSlotRestorePendingKey(startup.slotID)); err != nil {
			return err
		}
		if err := final.Commit(true); err != nil {
			return err
		}
	}
	report(SnapshotRestoreProgress{Stage: "snapshot_installed", Bytes: size, TotalBytes: size, Entries: int64(entryCount), TotalEntries: int64(entryCount)})

	db.clearChannelCache()
	return nil
}

func invalidateSnapshotAuthenticationToken(key, value []byte, hashSlots []HashSlot) ([]byte, error) {
	for _, hashSlot := range hashSlots {
		if !bytesHasPrefix(key, encodeRowPrefix(hashSlot, TableIDUser)) && !bytesHasPrefix(key, encodeRowPrefix(hashSlot, TableIDDevice)) {
			continue
		}
		_, rest, err := readValueString(value)
		if err != nil {
			return nil, err
		}
		result := appendValueString(nil, "")
		return append(result, rest...), nil
	}
	return value, nil
}

func verifySeekableSnapshotChecksum(reader io.ReadSeeker, size int64) error {
	const minSnapshotBytes = 4 + 2 + 2 + 8 + 4
	if reader == nil || size < minSnapshotBytes {
		return dberrors.ErrCorruptValue
	}
	end, err := reader.Seek(0, io.SeekEnd)
	if err != nil || end != size {
		return dberrors.ErrCorruptValue
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return err
	}
	checksum := crc32.NewIEEE()
	if _, err := io.CopyN(checksum, reader, size-4); err != nil {
		return dberrors.ErrCorruptValue
	}
	var trailer [4]byte
	if _, err := io.ReadFull(reader, trailer[:]); err != nil {
		return dberrors.ErrCorruptValue
	}
	if checksum.Sum32() != binary.BigEndian.Uint32(trailer[:]) {
		return dberrors.ErrChecksumMismatch
	}
	return nil
}

func visitSlotSnapshotStream(ctx context.Context, source io.ReadSeeker, size int64, scope portableSnapshotEntryScope, visit func(key, value []byte) error) ([]uint16, uint64, error) {
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return nil, 0, err
	}
	reader := bufio.NewReaderSize(io.LimitReader(source, size-4), 64<<10)
	var magic [4]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil || magic != slotSnapshotMagic {
		return nil, 0, dberrors.ErrCorruptValue
	}
	version, err := readSlotStreamUint16(reader)
	if err != nil || version != slotSnapshotVersion {
		return nil, 0, dberrors.ErrCorruptValue
	}
	hashSlotCount, err := readSlotStreamUint16(reader)
	if err != nil || hashSlotCount == 0 {
		return nil, 0, dberrors.ErrCorruptValue
	}
	hashSlots := make([]uint16, hashSlotCount)
	for index := range hashSlots {
		hashSlots[index], err = readSlotStreamUint16(reader)
		if err != nil {
			return nil, 0, dberrors.ErrCorruptValue
		}
	}
	normalized, err := normalizeSnapshotHashSlots(hashSlots)
	if err != nil || !equalUint16HashSlots(hashSlots, uint16HashSlots(normalized)) {
		return nil, 0, dberrors.ErrCorruptValue
	}
	entryOrder := newPortableSnapshotEntryOrder(normalized, scope)
	entryCount, err := readSlotStreamUint64(reader)
	if err != nil || entryCount > math.MaxInt {
		return nil, 0, dberrors.ErrCorruptValue
	}
	var largeRecord []byte
	for index := uint64(0); index < entryCount; index++ {
		if err := contextErr(ctx); err != nil {
			return nil, 0, err
		}
		keySize, err := readSlotStreamSize(reader)
		if err != nil {
			return nil, 0, err
		}
		valueSize, err := readSlotStreamSize(reader)
		if err != nil {
			return nil, 0, err
		}
		// Small rows borrow the buffered reader until the visitor returns.
		// Large rows reuse one bounded record buffer across visits.
		recordSize := int(keySize + valueSize)
		borrowed := recordSize <= reader.Size()
		var record []byte
		if borrowed {
			record, err = reader.Peek(recordSize)
		} else {
			if cap(largeRecord) < recordSize {
				largeRecord = make([]byte, recordSize)
			}
			record = largeRecord[:recordSize]
			_, err = io.ReadFull(reader, record)
		}
		if err != nil {
			return nil, 0, dberrors.ErrCorruptValue
		}
		key, value := record[:int(keySize)], record[int(keySize):]
		if err := entryOrder.accept(key); err != nil {
			return nil, 0, err
		}
		if err := visit(key, value); err != nil {
			return nil, 0, err
		}
		if borrowed {
			if _, err := reader.Discard(recordSize); err != nil {
				return nil, 0, err
			}
		}
	}
	if _, err := reader.ReadByte(); err != io.EOF {
		return nil, 0, dberrors.ErrCorruptValue
	}
	return hashSlots, entryCount, nil
}

func newPortableSnapshotEntryOrder(hashSlots []HashSlot, scope portableSnapshotEntryScope) *portableSnapshotEntryOrder {
	order := &portableSnapshotEntryOrder{hashSlots: hashSlots, scope: scope}
	if len(hashSlots) > 0 {
		order.spans = portableSnapshotSpans(hashSlots[0], scope)
	}
	return order
}

func (o *portableSnapshotEntryOrder) accept(key []byte) error {
	for o.hashSlotIdx < len(o.hashSlots) {
		for o.spanIndex < len(o.spans) {
			if bytesInSpan(key, o.spans[o.spanIndex]) {
				if len(o.previousKey) > 0 && bytes.Compare(key, o.previousKey) <= 0 {
					return fmt.Errorf("%w: snapshot keys are not strictly ordered within a registered span", dberrors.ErrCorruptValue)
				}
				o.previousKey = append(o.previousKey[:0], key...)
				return nil
			}
			o.spanIndex++
			o.previousKey = o.previousKey[:0]
		}
		o.hashSlotIdx++
		o.spanIndex = 0
		if o.hashSlotIdx < len(o.hashSlots) {
			o.spans = portableSnapshotSpans(o.hashSlots[o.hashSlotIdx], o.scope)
		}
	}

	for _, hashSlot := range o.hashSlots {
		for _, span := range portableSnapshotSpans(hashSlot, o.scope) {
			if bytesInSpan(key, span) {
				return fmt.Errorf("%w: snapshot registered spans are not in canonical order", dberrors.ErrCorruptValue)
			}
		}
	}
	return fmt.Errorf("%w: snapshot key %x is outside registered snapshot spans", dberrors.ErrInvalidArgument, key)
}

func portableSnapshotSpans(hashSlot HashSlot, scope portableSnapshotEntryScope) []Span {
	if scope == portableSnapshotBackupEntries {
		return hashSlotBackupDataSpans(hashSlot)
	}
	return hashSlotAllDataSpans(hashSlot)
}

func readSlotStreamSize(reader *bufio.Reader) (uint64, error) {
	size, err := binary.ReadUvarint(reader)
	if err != nil || size > maxSlotSnapshotStreamEntryBytes {
		return 0, dberrors.ErrCorruptValue
	}
	return size, nil
}

func readSlotStreamUint16(reader io.Reader) (uint16, error) {
	var value uint16
	err := binary.Read(reader, binary.BigEndian, &value)
	return value, err
}

func readSlotStreamUint64(reader io.Reader) (uint64, error) {
	var value uint64
	err := binary.Read(reader, binary.BigEndian, &value)
	return value, err
}
