package raftlog

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/goroutine"
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
	// Goroutines is the optional goroutine registry for lifecycle tracking.
	Goroutines *goroutine.Registry
}

type DB struct {
	db *pebble.DB

	options Options
	// snapshotStore manages external snapshot chunk directories for this DB.
	snapshotStore *snapshotStore

	mu      sync.Mutex
	closing bool

	writeCh  chan *writeRequest
	workerWG sync.WaitGroup

	stateCache map[Scope]scopeWriteState

	// scopeMutationLocks serialize future per-scope snapshot/metadata mutations.
	scopeMutationLocksMu sync.Mutex
	scopeMutationLocks   map[Scope]*sync.Mutex
	// snapshotLifecycleMu coordinates snapshot staging/finalization with GC scans.
	snapshotLifecycleMu sync.Mutex
	// activeSnapshotPaths counts temporary or final snapshot paths in use.
	activeSnapshotPaths map[string]int
	// gcCtx is canceled during Close to stop background snapshot cleanup work.
	gcCtx context.Context
	// gcCancel cancels gcCtx.
	gcCancel context.CancelFunc
	// gcWG tracks running snapshot GC passes so Close can wait for them.
	gcWG sync.WaitGroup

	// snapshotGCTestHook blocks a GC pass at a deterministic point in tests.
	snapshotGCTestHook func()
	// currentMetaAfterMetaLoadHook blocks currentMeta after log metadata is read in tests.
	currentMetaAfterMetaLoadHook func(scope Scope)
	// snapshotReadAfterManifestHook blocks a snapshot read after manifest load and before active registration in tests.
	snapshotReadAfterManifestHook func(scope Scope, manifest SnapshotManifest)
	// snapshotReadBeforeChunksHook blocks a snapshot read after active path registration in tests.
	snapshotReadBeforeChunksHook func(scope Scope, manifest SnapshotManifest)
	// snapshotAfterPublishTestHook injects failures after final directory publication in tests.
	snapshotAfterPublishTestHook func(staged *stagedSnapshot) error
	// writeCommitTestHook injects write batch commit failures in tests.
	writeCommitTestHook func() error
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
	gcCtx, gcCancel := context.WithCancel(context.Background())

	db := &DB{
		db:                  pdb,
		options:             normalized,
		snapshotStore:       newSnapshotStore(normalized.SnapshotPath, normalized.SnapshotChunkSize),
		writeCh:             make(chan *writeRequest, defaultWriteChSize),
		stateCache:          make(map[Scope]scopeWriteState),
		scopeMutationLocks:  make(map[Scope]*sync.Mutex),
		activeSnapshotPaths: make(map[string]int),
		gcCtx:               gcCtx,
		gcCancel:            gcCancel,
	}
	if err := db.ensureManifest(); err != nil {
		gcCancel()
		_ = pdb.Close()
		return nil, err
	}

	db.workerWG.Add(1)
	goroutine.SafeGo(db.options.Goroutines, "raftlog", "write_worker", db.runWriteWorker)
	return db, nil
}

func normalizeOptions(path string, opts Options) (Options, error) {
	if opts.SnapshotPath == "" {
		opts.SnapshotPath = path + "-snapshots"
	}
	snapshotPath, err := filepath.Abs(opts.SnapshotPath)
	if err != nil {
		return Options{}, err
	}
	opts.SnapshotPath = filepath.Clean(snapshotPath)
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
	if db.gcCancel != nil {
		db.gcCancel()
	}
	db.mu.Unlock()

	db.workerWG.Wait()
	db.gcWG.Wait()

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

// startSnapshotGC starts a tracked best-effort GC pass unless the DB is closing.
func (db *DB) startSnapshotGC() bool {
	if db == nil {
		return false
	}

	db.mu.Lock()
	if db.closing || db.gcCtx == nil {
		db.mu.Unlock()
		return false
	}
	ctx := db.gcCtx
	db.gcWG.Add(1)
	db.mu.Unlock()

	goroutine.SafeGo(db.options.Goroutines, "raftlog", "snapshot_gc", func() {
		defer db.gcWG.Done()
		_ = db.runSnapshotGC(ctx)
	})
	return true
}

func (db *DB) lockScopeMutation(scope Scope) func() {
	db.scopeMutationLocksMu.Lock()
	if db.scopeMutationLocks == nil {
		db.scopeMutationLocks = make(map[Scope]*sync.Mutex)
	}
	lock := db.scopeMutationLocks[scope]
	if lock == nil {
		lock = &sync.Mutex{}
		db.scopeMutationLocks[scope] = lock
	}
	db.scopeMutationLocksMu.Unlock()

	lock.Lock()
	return lock.Unlock
}

func (db *DB) registerActiveSnapshotPath(path string) func() {
	path = normalizeSnapshotLifecyclePath(path)
	if path == "" {
		return func() {}
	}

	db.snapshotLifecycleMu.Lock()
	if db.activeSnapshotPaths == nil {
		db.activeSnapshotPaths = make(map[string]int)
	}
	db.activeSnapshotPaths[path]++
	db.snapshotLifecycleMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			db.snapshotLifecycleMu.Lock()
			if count := db.activeSnapshotPaths[path]; count <= 1 {
				delete(db.activeSnapshotPaths, path)
			} else {
				db.activeSnapshotPaths[path] = count - 1
			}
			db.snapshotLifecycleMu.Unlock()
		})
	}
}

func (db *DB) isActiveSnapshotPath(path string) bool {
	path = normalizeSnapshotLifecyclePath(path)
	db.snapshotLifecycleMu.Lock()
	count := db.activeSnapshotPaths[path]
	db.snapshotLifecycleMu.Unlock()
	return count > 0
}
