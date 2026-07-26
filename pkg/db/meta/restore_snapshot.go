package meta

import (
	"context"
	"fmt"
	"io"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// BackupSnapshotEntry is one authenticated key/value row from a portable
// semantic metadata snapshot. Key and Value are valid only during the visitor.
type BackupSnapshotEntry struct {
	Key   []byte
	Value []byte
}

// ReplayBackupHashSlotSnapshot validates and visits one complete seekable
// semantic metadata snapshot without retaining its rows.
func ReplayBackupHashSlotSnapshot(
	ctx context.Context,
	reader io.ReadSeeker,
	size int64,
	visit func(BackupSnapshotEntry) error,
) ([]uint16, BackupSnapshotStats, error) {
	if visit == nil {
		return nil, BackupSnapshotStats{}, dberrors.ErrInvalidArgument
	}
	if err := verifySeekableSnapshotChecksum(reader, size); err != nil {
		return nil, BackupSnapshotStats{}, err
	}
	slots, count, err := visitSlotSnapshotStream(
		ctx, reader, size,
		func(key, value []byte) error {
			return visit(BackupSnapshotEntry{Key: key, Value: value})
		},
	)
	if err != nil {
		return nil, BackupSnapshotStats{}, err
	}
	return slots, BackupSnapshotStats{EntryCount: count}, nil
}

// RestoreSnapshotWriter installs a fresh semantic metadata snapshot in bounded
// batches before incremental Slot commands are replayed.
type RestoreSnapshotWriter struct {
	db               *MetaDB
	hashSlots        []HashSlot
	invalidateTokens bool
	batch            *engine.Batch
	batchEntries     int
	batchBytes       int
	closed           bool
}

// NewRestoreSnapshotWriter opens a fresh-Slot restore writer.
func (db *MetaDB) NewRestoreSnapshotWriter(
	ctx context.Context,
	hashSlots []uint16,
	invalidateTokens bool,
) (*RestoreSnapshotWriter, error) {
	if err := checkSnapshotDB(ctx, db); err != nil {
		return nil, err
	}
	normalized, err := normalizeSnapshotHashSlots(hashSlots)
	if err != nil {
		return nil, err
	}
	return &RestoreSnapshotWriter{
		db: db, hashSlots: normalized, invalidateTokens: invalidateTokens,
		batch: db.engine.NewBatch(),
	}, nil
}

// Put validates and stages one semantic snapshot row.
func (w *RestoreSnapshotWriter) Put(
	ctx context.Context,
	key []byte,
	value []byte,
) error {
	if w == nil || w.closed || w.db == nil || w.batch == nil {
		return dberrors.ErrClosed
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	if !snapshotEntryInHashSlots(key, w.hashSlots) ||
		!snapshotEntryInBackupSpans(key, w.hashSlots) {
		return fmt.Errorf(
			"%w: restore snapshot key is outside semantic spans",
			dberrors.ErrInvalidArgument,
		)
	}
	var err error
	if w.invalidateTokens {
		value, err = invalidateSnapshotAuthenticationToken(
			key, value, w.hashSlots,
		)
		if err != nil {
			return err
		}
	}
	if err := w.batch.Set(key, value); err != nil {
		return err
	}
	w.batchEntries++
	w.batchBytes += len(key) + len(value)
	if w.batchEntries >= slotSnapshotImportBatchEntries ||
		w.batchBytes >= slotSnapshotImportBatchBytes {
		return w.flush()
	}
	return nil
}

// Close commits all staged rows and invalidates the writer.
func (w *RestoreSnapshotWriter) Close() error {
	if w == nil || w.closed {
		return nil
	}
	w.closed = true
	err := w.flush()
	closeErr := w.batch.Close()
	w.batch = nil
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	w.db.clearChannelCache()
	return nil
}

func (w *RestoreSnapshotWriter) flush() error {
	if w.batchEntries == 0 {
		return nil
	}
	if err := w.batch.Commit(true); err != nil {
		return err
	}
	if err := w.batch.Close(); err != nil {
		return err
	}
	w.batch = w.db.engine.NewBatch()
	w.batchEntries = 0
	w.batchBytes = 0
	return nil
}

func snapshotEntryInBackupSpans(
	key []byte,
	hashSlots []HashSlot,
) bool {
	for _, hashSlot := range hashSlots {
		for _, span := range hashSlotBackupDataSpans(hashSlot) {
			if bytesInSpan(key, span) {
				return true
			}
		}
	}
	return false
}
