package raftlog

import (
	"errors"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/cockroachdb/pebble/v2"
)

type DB struct {
	db *pebble.DB

	mu      sync.Mutex
	closing bool

	writeCh  chan *writeRequest
	workerWG sync.WaitGroup

	stateCache map[Scope]scopeWriteState
}

func Open(path string) (*DB, error) {
	pdb, err := pebble.Open(path, &pebble.Options{})
	if err != nil {
		return nil, err
	}

	db := &DB{
		db:         pdb,
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
