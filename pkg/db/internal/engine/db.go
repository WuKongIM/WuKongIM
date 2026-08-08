package engine

import (
	"errors"
	"runtime"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/bloom"
)

const (
	defaultCacheSize                = 128 << 20
	defaultMemTableSize             = 32 << 20
	defaultL0CompactionConcurrency  = 6
	defaultCompactionConcurrencyMax = 4
	compactionDebtMemTableCount     = 4
	// Darwin implements Pebble's range SyncTo as a full-file fsync. A wider
	// interval avoids turning large SSTable compactions into hundreds of full
	// syncs while retaining bounded dirty-data smoothing.
	darwinBytesPerSync = 16 << 20
)

// Options controls Pebble engine tuning.
type Options struct {
	// CacheSize configures Pebble block cache bytes.
	CacheSize int64
	// MemTableSize configures Pebble memtable bytes.
	MemTableSize int64
	// CompactionDebtConcurrencyBytes configures the compaction-debt bytes that
	// permit each additional compaction. Values <= 0 derive four memtables.
	CompactionDebtConcurrencyBytes int64
	// ReadOnly opens the engine without allowing writes or background compactions.
	ReadOnly bool
	// Logger receives structured Pebble diagnostics; routine recovery details are debug-only.
	Logger wklog.Logger
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
	iter, err := e.pdb.NewIter(pebbleIterOptions(span, opts))
	if err != nil {
		return nil, err
	}
	return &Iter{iter: iter}, nil
}

func pebbleIterOptions(span Span, _ IterOptions) *pebble.IterOptions {
	options := &pebble.IterOptions{}
	if len(span.Start) > 0 {
		options.LowerBound = append([]byte(nil), span.Start...)
	}
	if len(span.End) > 0 {
		options.UpperBound = append([]byte(nil), span.End...)
	}
	return options
}

func pebbleOptions(opts Options) *pebble.Options {
	if opts.CacheSize <= 0 {
		opts.CacheSize = defaultCacheSize
	}
	if opts.MemTableSize <= 0 {
		opts.MemTableSize = defaultMemTableSize
	}
	if opts.CompactionDebtConcurrencyBytes <= 0 {
		opts.CompactionDebtConcurrencyBytes = opts.MemTableSize * compactionDebtMemTableCount
	}
	popts := &pebble.Options{
		CacheSize:                   opts.CacheSize,
		MemTableSize:                uint64(opts.MemTableSize),
		MemTableStopWritesThreshold: 4,
		L0CompactionThreshold:       8,
		L0StopWritesThreshold:       24,
		// Keep one baseline compaction and let Pebble add up to three more only as
		// L0 read amplification or compaction debt crosses successive pressure
		// thresholds. This preserves idle efficiency while preventing sustained
		// message writes from reaching the L0 write-stop boundary.
		CompactionConcurrencyRange: func() (int, int) {
			return 1, defaultCompactionConcurrencyMax
		},
		ReadOnly:     opts.ReadOnly,
		BytesPerSync: platformBytesPerSync(),
	}
	// Permit extra compactions at L0 depths 6, 12, and 18 so the fourth slot has
	// six sublevels of recovery headroom before the write-stop depth of 24. Also
	// open one slot per configured compaction-debt step (four memtables by
	// default). The upper bound keeps this recovery capacity finite.
	popts.Experimental.L0CompactionConcurrency = defaultL0CompactionConcurrency
	popts.Experimental.CompactionDebtConcurrency =
		uint64(opts.CompactionDebtConcurrencyBytes)
	if opts.Logger != nil {
		popts.Logger = wklog.NewDependencyLogger(opts.Logger, "pebble")
	}
	for i := range popts.Levels {
		popts.Levels[i].FilterPolicy = bloom.FilterPolicy(10)
	}
	return popts
}

func platformBytesPerSync() int {
	if runtime.GOOS == "darwin" {
		return darwinBytesPerSync
	}
	// Zero lets Pebble retain its platform-appropriate upstream default.
	return 0
}
