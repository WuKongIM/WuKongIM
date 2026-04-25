package store

import (
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/cockroachdb/pebble/v2"
)

const defaultChannelWALMinSyncInterval = 2 * time.Millisecond

func defaultPebbleOptions() *pebble.Options {
	return &pebble.Options{
		WALMinSyncInterval: func() time.Duration {
			return defaultChannelWALMinSyncInterval
		},
	}
}

type Engine struct {
	mu          sync.Mutex
	db          *pebble.DB
	stores      map[channel.ChannelKey]*ChannelStore
	coordinator *commitCoordinator
	checkpoint  *commitCoordinator
}

func Open(path string) (*Engine, error) {
	pdb, err := pebble.Open(path, defaultPebbleOptions())
	if err != nil {
		return nil, err
	}
	return &Engine{
		db:     pdb,
		stores: make(map[channel.ChannelKey]*ChannelStore),
	}, nil
}

func (e *Engine) Close() error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	if e.db == nil {
		e.mu.Unlock()
		return nil
	}
	pdb := e.db
	coordinator := e.coordinator
	checkpoint := e.checkpoint
	e.db = nil
	e.stores = nil
	e.coordinator = nil
	e.checkpoint = nil
	e.mu.Unlock()
	if coordinator != nil {
		coordinator.close()
	}
	if checkpoint != nil {
		checkpoint.close()
	}
	return pdb.Close()
}

func (e *Engine) ForChannel(key channel.ChannelKey, id channel.ChannelID) *ChannelStore {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.stores == nil {
		e.stores = make(map[channel.ChannelKey]*ChannelStore)
	}
	if st, ok := e.stores[key]; ok {
		if st.id != id {
			panic("store: inconsistent channel key and channel id")
		}
		return st
	}
	st := &ChannelStore{
		engine:   e,
		key:      key,
		id:       id,
		messages: messageTable{channelKey: key, db: e.db},
	}
	e.stores[key] = st
	return st
}

func (e *Engine) commitCoordinator() *commitCoordinator {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.db == nil {
		return nil
	}
	if e.coordinator == nil {
		e.coordinator = newCommitCoordinator(e.db)
	}
	return e.coordinator
}

func (e *Engine) checkpointCoordinator() *commitCoordinator {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.db == nil {
		return nil
	}
	if e.checkpoint == nil {
		e.checkpoint = newCommitCoordinator(e.db)
	}
	return e.checkpoint
}

func (e *Engine) checkpointCoordinatorForStore() *commitCoordinator {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.db == nil {
		return nil
	}
	if e.checkpoint != nil {
		return e.checkpoint
	}
	for _, st := range e.stores {
		if st != nil && st.writeInProgress.Load() {
			e.checkpoint = newCommitCoordinator(e.db)
			return e.checkpoint
		}
	}
	return nil
}
