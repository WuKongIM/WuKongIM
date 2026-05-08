package raftlog

import (
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/cockroachdb/pebble/v2"
)

const defaultSnapshotChunkSize uint64 = 8 << 20

// Options configures durable raft log storage.
type Options struct {
	// SnapshotPath is the external root for snapshot chunk directories.
	SnapshotPath string
	// SnapshotChunkSize controls the maximum bytes written to each snapshot chunk file.
	SnapshotChunkSize uint64
	// SnapshotGCGrace is how long orphan snapshot directories remain before GC can remove them.
	SnapshotGCGrace time.Duration
}

type DB struct {
	db *pebble.DB

	options Options

	mu      sync.Mutex
	closing bool

	writeCh  chan *writeRequest
	workerWG sync.WaitGroup

	stateCache map[Scope]scopeWriteState
}

func Open(path string, opts Options) (*DB, error) {
	normalized, err := normalizeOptions(path, opts)
	if err != nil {
		return nil, err
	}

	pdb, err := pebble.Open(path, &pebble.Options{})
	if err != nil {
		return nil, err
	}

	db := &DB{
		db:         pdb,
		options:    normalized,
		writeCh:    make(chan *writeRequest, defaultWriteChSize),
		stateCache: make(map[Scope]scopeWriteState),
	}
	if err := db.ensureManifest(); err != nil {
		_ = pdb.Close()
		return nil, err
	}

	db.workerWG.Add(1)
	go db.runWriteWorker()
	return db, nil
}

func normalizeOptions(path string, opts Options) (Options, error) {
	if opts.SnapshotPath == "" {
		opts.SnapshotPath = path + "-snapshots"
	}
	if opts.SnapshotChunkSize == 0 {
		opts.SnapshotChunkSize = defaultSnapshotChunkSize
	}
	if opts.SnapshotGCGrace < 0 {
		return Options{}, errors.New("raftstorage: snapshot GC grace must be non-negative")
	}
	return opts, nil
}

func (db *DB) ensureManifest() error {
	value, closer, err := db.db.Get(encodeManifestKey())
	if err == nil {
		defer closer.Close()
		if len(value) != 1 || value[0] != currentFormatVersion {
			return errors.New("raftstorage: unsupported manifest version")
		}
		return nil
	}
	if !errors.Is(err, pebble.ErrNotFound) {
		return err
	}
	return db.db.Set(encodeManifestKey(), []byte{currentFormatVersion}, pebble.Sync)
}

func (db *DB) Close() error {
	if db == nil || db.db == nil {
		return nil
	}

	db.mu.Lock()
	if db.closing {
		db.mu.Unlock()
		return nil
	}
	db.closing = true
	close(db.writeCh)
	db.mu.Unlock()

	db.workerWG.Wait()

	err := db.db.Close()
	db.db = nil
	return err
}

func (db *DB) For(scope Scope) multiraft.Storage {
	return &pebbleStore{db: db, scope: scope}
}

func (db *DB) ForSlot(slot uint64) multiraft.Storage {
	return db.For(SlotScope(slot))
}

func (db *DB) ForController() multiraft.Storage {
	return db.For(ControllerScope())
}
