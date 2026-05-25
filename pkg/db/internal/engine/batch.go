package engine

import (
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/cockroachdb/pebble/v2"
)

// Batch stages multiple writes for atomic commit.
type Batch struct {
	batch *pebble.Batch
}

// Set stages a key/value write.
func (b *Batch) Set(key []byte, value []byte) error {
	if b == nil || b.batch == nil {
		return dberrors.ErrClosed
	}
	return b.batch.Set(key, value, nil)
}

// SetDeferred stages a key/value write while allowing the caller to fill Pebble-owned buffers.
func (b *Batch) SetDeferred(keyLen int, valueLen int, fill func(key, value []byte) error) error {
	if b == nil || b.batch == nil {
		return dberrors.ErrClosed
	}
	if keyLen < 0 || valueLen < 0 || fill == nil {
		return dberrors.ErrInvalidArgument
	}
	op := b.batch.SetDeferred(keyLen, valueLen)
	if err := fill(op.Key, op.Value); err != nil {
		return err
	}
	return op.Finish()
}

// Delete stages a point delete.
func (b *Batch) Delete(key []byte) error {
	if b == nil || b.batch == nil {
		return dberrors.ErrClosed
	}
	return b.batch.Delete(key, nil)
}

// DeleteRange stages a range delete over span.
func (b *Batch) DeleteRange(span Span) error {
	if b == nil || b.batch == nil {
		return dberrors.ErrClosed
	}
	if len(span.Start) == 0 || len(span.End) == 0 {
		return dberrors.ErrInvalidArgument
	}
	return b.batch.DeleteRange(span.Start, span.End, nil)
}

// Commit commits the batch, syncing when sync is true.
func (b *Batch) Commit(sync bool) error {
	if b == nil || b.batch == nil {
		return dberrors.ErrClosed
	}
	opts := pebble.NoSync
	if sync {
		opts = pebble.Sync
	}
	return b.batch.Commit(opts)
}

// Close releases batch resources.
func (b *Batch) Close() error {
	if b == nil || b.batch == nil {
		return nil
	}
	batch := b.batch
	b.batch = nil
	return batch.Close()
}
