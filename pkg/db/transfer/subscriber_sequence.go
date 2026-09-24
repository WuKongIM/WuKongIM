package transfer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// SubscriberSequenceRecord preserves a hash Slot's deleted as well as live join
// identities. Sequence uses decimal JSON strings without float rounding.
type SubscriberSequenceRecord struct {
	HashSlot uint16 `json:"hash_slot"`
	Sequence Uint64 `json:"sequence"`
}

func exportSubscriberSequences(ctx context.Context, root string, db *metadb.MetaDB, opts ExportOptions, stats *ExportStats) (*FileEntry, error) {
	const path = "meta/subscriber_sequences.jsonl"
	w, err := newExportFileWriter(root, path, FileKindMetaSubscriberSequences)
	if err != nil {
		return nil, err
	}
	defer w.Close()
	for slot := uint16(0); slot < opts.HashSlotCount; slot++ {
		n, err := db.HashSlot(slot).SubscriberSequence(ctx)
		if err != nil {
			return nil, err
		}
		if n <= 1 {
			continue
		}
		if err = w.Write(SubscriberSequenceRecord{HashSlot: slot, Sequence: Uint64(n)}); err != nil {
			return nil, err
		}
		stats.RowsExported++
	}
	entry, err := w.Close()
	if err != nil {
		return nil, err
	}
	if entry.Rows == 0 {
		return nil, os.Remove(filepath.Join(root, path))
	}
	stats.FilesWritten++
	stats.BytesWritten += entryFileSize(root, path)
	return &entry, nil
}

func (v *bundleValidator) finishSubscriberSequences() error {
	for slot, n := range v.subscriberMax {
		if n > 1 && v.subscriberSequences[slot] < n {
			return fmt.Errorf("%w: subscriber incarnation exceeds sequence witness in hash Slot %d", ErrValidation, slot)
		}
	}
	return nil
}

func scanSubscriberSequenceDigest(ctx context.Context, db *metadb.MetaDB, opts VerifyOptions) (int64, string, error) {
	digest := newVerifyDigest()
	var rows int64
	for slot := uint16(0); slot < opts.HashSlotCount; slot++ {
		n, err := db.HashSlot(slot).SubscriberSequence(ctx)
		if err != nil {
			return 0, "", err
		}
		if n <= 1 {
			continue
		}
		if err = digest.write(SubscriberSequenceRecord{HashSlot: slot, Sequence: Uint64(n)}); err != nil {
			return 0, "", err
		}
		rows++
	}
	return rows, digest.sum(), nil
}
