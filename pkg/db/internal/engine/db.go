package engine

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/bloom"
)

const (
	defaultCacheSize    = 128 << 20
	defaultMemTableSize = 32 << 20
)

// Options controls Pebble engine tuning.
type Options struct {
	// CacheSize configures Pebble block cache bytes.
	CacheSize int64
	// MemTableSize configures Pebble memtable bytes.
	MemTableSize int64
	// ReadOnly opens the engine without allowing writes or background compactions.
	ReadOnly bool
}

// DB wraps a Pebble database without exposing Pebble types to domain packages.
type DB struct {
	pdb *pebble.DB
}

// Open opens a Pebble-backed engine at path.
func Open(path string, opts Options) (*DB, error) {
	if path == "" {
		return nil, dberrors.ErrInvalidArgument
	}
	pdb, err := pebble.Open(path, pebbleOptions(opts))
	if err != nil {
		return nil, err
	}
	return &DB{pdb: pdb}, nil
}

// Close closes the underlying engine.
func (e *DB) Close() error {
	if e == nil || e.pdb == nil {
		return nil
	}
	pdb := e.pdb
	e.pdb = nil
	return pdb.Close()
}

// Get returns a copied value for key.
func (e *DB) Get(key []byte) ([]byte, bool, error) {
	if e == nil || e.pdb == nil {
		return nil, false, dberrors.ErrClosed
	}
	value, closer, err := e.pdb.Get(key)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer closer.Close()
	return append([]byte(nil), value...), true, nil
}

// NewBatch creates a write batch. The caller must close it.
func (e *DB) NewBatch() *Batch {
	if e == nil || e.pdb == nil {
		return &Batch{}
	}
	return &Batch{batch: e.pdb.NewBatch()}
}

// NewIter creates an iterator over span.
func (e *DB) NewIter(span Span, opts IterOptions) (*Iter, error) {
	if e == nil || e.pdb == nil {
		return nil, dberrors.ErrClosed
	}
	iterOpts := &pebble.IterOptions{}
	if len(span.Start) > 0 {
		iterOpts.LowerBound = append([]byte(nil), span.Start...)
	}
	if len(span.End) > 0 {
		iterOpts.UpperBound = append([]byte(nil), span.End...)
	}
	iter, err := e.pdb.NewIter(iterOpts)
	if err != nil {
		return nil, err
	}
	return &Iter{iter: iter}, nil
}

func pebbleOptions(opts Options) *pebble.Options {
	if opts.CacheSize <= 0 {
		opts.CacheSize = defaultCacheSize
	}
	if opts.MemTableSize <= 0 {
		opts.MemTableSize = defaultMemTableSize
	}
	popts := &pebble.Options{
		CacheSize:                   opts.CacheSize,
		MemTableSize:                uint64(opts.MemTableSize),
		MemTableStopWritesThreshold: 4,
		L0CompactionThreshold:       8,
		L0StopWritesThreshold:       24,
		ReadOnly:                    opts.ReadOnly,
	}
	popts.Levels[0].FilterPolicy = bloom.FilterPolicy(10)
	return popts
}
