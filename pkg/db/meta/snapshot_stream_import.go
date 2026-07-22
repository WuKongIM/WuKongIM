package meta

import (
	"bufio"
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

// ImportHashSlotSnapshotReader validates then installs a seekable portable
// snapshot without retaining the complete hash-slot payload in memory.
func (db *MetaDB) ImportHashSlotSnapshotReader(ctx context.Context, hashSlots []uint16, reader io.ReadSeeker, size int64) error {
	return db.importHashSlotSnapshotReader(ctx, hashSlots, reader, size, false, false, nil)
}

// ImportHashSlotSnapshotReaderPreservingMigrationMeta installs semantic data
// while retaining target-local migration workflow rows.
func (db *MetaDB) ImportHashSlotSnapshotReaderPreservingMigrationMeta(ctx context.Context, hashSlots []uint16, reader io.ReadSeeker, size int64) error {
	return db.importHashSlotSnapshotReader(ctx, hashSlots, reader, size, true, false, nil)
}

// ImportHashSlotSnapshotReaderForRestore installs semantic data while
// retaining target-local migration rows and optionally clearing every restored
// user and device token as the rows enter the target database.
func (db *MetaDB) ImportHashSlotSnapshotReaderForRestore(ctx context.Context, hashSlots []uint16, reader io.ReadSeeker, size int64, invalidateTokens bool) error {
	return db.importHashSlotSnapshotReader(ctx, hashSlots, reader, size, true, invalidateTokens, nil)
}

// ImportHashSlotSnapshotReaderForRestoreWithStats installs semantic data and
// returns the exact record count authenticated by the portable stream.
func (db *MetaDB) ImportHashSlotSnapshotReaderForRestoreWithStats(ctx context.Context, hashSlots []uint16, reader io.ReadSeeker, size int64, invalidateTokens bool) (BackupSnapshotStats, error) {
	var stats BackupSnapshotStats
	err := db.importHashSlotSnapshotReader(ctx, hashSlots, reader, size, true, invalidateTokens, &stats)
	return stats, err
}

func (db *MetaDB) importHashSlotSnapshotReader(ctx context.Context, hashSlots []uint16, reader io.ReadSeeker, size int64, preserveMigrationMeta, invalidateTokens bool, stats *BackupSnapshotStats) error {
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
	validate := func(key, value []byte) error {
		if !snapshotEntryInHashSlots(key, normalized) {
			return fmt.Errorf("%w: snapshot key %x is outside selected hash slots %v", dberrors.ErrInvalidArgument, key, normalized)
		}
		return nil
	}
	streamSlots, entryCount, err := visitSlotSnapshotStream(ctx, reader, size, validate)
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
	_, _, err = visitSlotSnapshotStream(ctx, reader, size, func(key, value []byte) error {
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

func visitSlotSnapshotStream(ctx context.Context, source io.ReadSeeker, size int64, visit func(key, value []byte) error) ([]uint16, uint64, error) {
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
		if err := visit(key, value); err != nil {
			return nil, 0, err
		}
	}
	if _, err := reader.ReadByte(); err != io.EOF {
		return nil, 0, dberrors.ErrCorruptValue
	}
	return hashSlots, entryCount, nil
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
