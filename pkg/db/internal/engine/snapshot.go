package engine

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/cockroachdb/pebble/v2"
)

// Snapshot is a stable read-only engine view that does not block later writes.
type Snapshot struct {
	snapshot *pebble.Snapshot
}

// NewSnapshot pins the current engine view until the returned snapshot is closed.
func (e *DB) NewSnapshot() (*Snapshot, error) {
	if e == nil || e.pdb == nil {
		return nil, dberrors.ErrClosed
	}
	return &Snapshot{snapshot: e.pdb.NewSnapshot()}, nil
}

// NewIter creates an iterator over span in the pinned view.
func (s *Snapshot) NewIter(span Span, opts IterOptions) (*Iter, error) {
	if s == nil || s.snapshot == nil {
		return nil, dberrors.ErrClosed
	}
	iter, err := s.snapshot.NewIter(pebbleIterOptions(span, opts))
	if err != nil {
		return nil, err
	}
	return &Iter{iter: iter}, nil
}

// Get returns a copied value from the pinned view.
func (s *Snapshot) Get(key []byte) ([]byte, bool, error) {
	if s == nil || s.snapshot == nil {
		return nil, false, dberrors.ErrClosed
	}
	value, closer, err := s.snapshot.Get(key)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer closer.Close()
	return append([]byte(nil), value...), true, nil
}

// Close releases the pinned engine view.
func (s *Snapshot) Close() error {
	if s == nil || s.snapshot == nil {
		return nil
	}
	snapshot := s.snapshot
	s.snapshot = nil
	return snapshot.Close()
}
