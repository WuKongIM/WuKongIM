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
	return db.importHashSlotSnapshotReader(ctx, hashSlots, reader, size, portableSnapshotAllEntries, false, false, nil)
}

// ImportHashSlotSnapshotReaderPreservingMigrationMeta installs semantic data
// while retaining target-local migration workflow rows.
func (db *MetaDB) ImportHashSlotSnapshotReaderPreservingMigrationMeta(ctx context.Context, hashSlots []uint16, reader io.ReadSeeker, size int64) error {
	return db.importHashSlotSnapshotReader(ctx, hashSlots, reader, size, portableSnapshotBackupEntries, true, false, nil)
}

// ImportHashSlotSnapshotReaderForRestore installs semantic data while
// retaining target-local migration rows and optionally clearing every restored
// user and device token as the rows enter the target database.
func (db *MetaDB) ImportHashSlotSnapshotReaderForRestore(ctx context.Context, hashSlots []uint16, reader io.ReadSeeker, size int64, invalidateTokens bool) error {
	return db.importHashSlotSnapshotReader(ctx, hashSlots, reader, size, portableSnapshotBackupEntries, true, invalidateTokens, nil)
}

// ImportHashSlotSnapshotReaderForRestoreWithStats installs semantic data and
// returns the exact record count authenticated by the portable stream.
func (db *MetaDB) ImportHashSlotSnapshotReaderForRestoreWithStats(ctx context.Context, hashSlots []uint16, reader io.ReadSeeker, size int64, invalidateTokens bool) (BackupSnapshotStats, error) {
	var stats BackupSnapshotStats
	err := db.importHashSlotSnapshotReader(ctx, hashSlots, reader, size, portableSnapshotBackupEntries, true, invalidateTokens, &stats)
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

func (db *MetaDB) importHashSlotSnapshotReader(ctx context.Context, hashSlots []uint16, reader io.ReadSeeker, size int64, scope portableSnapshotEntryScope, preserveMigrationMeta, invalidateTokens bool, stats *BackupSnapshotStats) error {
	if err := checkSnapshotDB(ctx, db); err != nil {
		return err
	}
	normalized, err := normalizeSnapshotHashSlots(hashSlots)
	if err != nil {
		return err
	}
	if err := verifySeekableSnapshotChecksum(reader, size); err != nil {
		return err
	}
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

	unlock := db.lockHashSlots(normalized)
	defer unlock()
	deleteBatch := db.engine.NewBatch()
	for _, hashSlot := range normalized {
		for _, span := range hashSlotSnapshotReplaceSpans(hashSlot, preserveMigrationMeta) {
			if err := deleteBatch.DeleteRange(engine.Span{Start: span.Start, End: span.End}); err != nil {
				_ = deleteBatch.Close()
				return err
			}
		}
	}
	if err := deleteBatch.Commit(true); err != nil {
		_ = deleteBatch.Close()
		return err
	}
	if err := deleteBatch.Close(); err != nil {
		return err
	}

	batch := db.engine.NewBatch()
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
		batch = db.engine.NewBatch()
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
		if err := db.stageSlotSnapshotEntry(batch, entry, normalized, preserveMigrationMeta); err != nil {
			return err
		}
		batchEntries++
		batchBytes += len(key) + len(value)
		if batchEntries >= slotSnapshotImportBatchEntries || batchBytes >= slotSnapshotImportBatchBytes {
			return flush()
		}
		return nil
	})
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
		key, err := readSlotStreamBytes(reader, keySize)
		if err != nil {
			return nil, 0, err
		}
		value, err := readSlotStreamBytes(reader, valueSize)
		if err != nil {
			return nil, 0, err
		}
		if err := entryOrder.accept(key); err != nil {
			return nil, 0, err
		}
		if err := visit(key, value); err != nil {
			return nil, 0, err
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

func readSlotStreamBytes(reader *bufio.Reader, size uint64) ([]byte, error) {
	value := make([]byte, int(size))
	if _, err := io.ReadFull(reader, value); err != nil {
		return nil, dberrors.ErrCorruptValue
	}
	return value, nil
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
