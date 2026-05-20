package store

import (
	"encoding/binary"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/bloom"
)

const (
	defaultChannelWALMinSyncInterval = 2 * time.Millisecond
	defaultChannelPebbleCacheSize    = 128 << 20
	defaultChannelPebbleMemTableSize = 32 << 20
)

func defaultPebbleOptions() *pebble.Options {
	opts := &pebble.Options{
		CacheSize:                   defaultChannelPebbleCacheSize,
		MemTableSize:                defaultChannelPebbleMemTableSize,
		MemTableStopWritesThreshold: 4,
		L0CompactionThreshold:       8,
		L0StopWritesThreshold:       24,
		WALMinSyncInterval: func() time.Duration {
			return defaultChannelWALMinSyncInterval
		},
	}
	opts.Levels[0].FilterPolicy = bloom.FilterPolicy(10)
	return opts
}

type Engine struct {
	mu          sync.Mutex
	db          *pebble.DB
	stores      map[channel.ChannelKey]*ChannelStore
	coordinator *commitCoordinator
	checkpoint  *commitCoordinator
	commitCfg   CommitCoordinatorConfig
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

// ConfigureCommitCoordinator sets cross-channel append/apply commit batching before the coordinator starts.
func (e *Engine) ConfigureCommitCoordinator(cfg CommitCoordinatorConfig) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.commitCfg = cfg
	if e.coordinator == nil {
		return
	}
	e.coordinator.configure(cfg)
}

// CommitCoordinatorConfig returns the effective append/apply commit coordinator settings.
func (e *Engine) CommitCoordinatorConfig() CommitCoordinatorConfig {
	if e == nil {
		return effectiveCommitCoordinatorConfig(CommitCoordinatorConfig{})
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.coordinator != nil {
		e.coordinator.batchMu.Lock()
		defer e.coordinator.batchMu.Unlock()
		return e.coordinator.cfg
	}
	return effectiveCommitCoordinatorConfig(e.commitCfg)
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

// ListChannelKeys returns persisted channels that have structured message rows
// or channel-scoped system state needed for replay/recovery.
func (e *Engine) ListChannelKeys() ([]channel.ChannelKey, error) {
	if e == nil || e.db == nil {
		return nil, channel.ErrInvalidArgument
	}
	seen := make(map[channel.ChannelKey]struct{})
	keys := make([]channel.ChannelKey, 0, 16)
	if err := e.collectTableStateChannelKeys(seen, &keys); err != nil {
		return nil, err
	}
	if err := e.collectTableSystemChannelKeys(seen, &keys); err != nil {
		return nil, err
	}
	return keys, nil
}

func (e *Engine) collectTableStateChannelKeys(seen map[channel.ChannelKey]struct{}, keys *[]channel.ChannelKey) error {
	iter, err := e.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte{keyspaceTableState},
		UpperBound: []byte{keyspaceTableState + 1},
	})
	if err != nil {
		return err
	}
	defer iter.Close()

	for ok := iter.First(); ok; {
		rawKey := iter.Key()
		start, end, rest, err := decodeKeyspaceChannelKeyParts(rawKey, keyspaceTableState)
		if err != nil {
			return err
		}
		if len(rest) < 4 || binary.BigEndian.Uint32(rest[:4]) != TableIDMessage {
			ok = iter.Next()
			continue
		}
		key := channel.ChannelKey(string(rawKey[start:end]))
		appendChannelKeyIfNew(seen, keys, key)
		ok = iter.SeekGE(nextKeyAfterPrefix(encodeKeyspacePrefix(keyspaceTableState, key)))
	}
	return iter.Error()
}

func (e *Engine) collectTableSystemChannelKeys(seen map[channel.ChannelKey]struct{}, keys *[]channel.ChannelKey) error {
	iter, err := e.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte{keyspaceTableSystem},
		UpperBound: []byte{keyspaceTableSystem + 1},
	})
	if err != nil {
		return err
	}
	defer iter.Close()

	for ok := iter.First(); ok; {
		rawKey := iter.Key()
		start, end, rest, err := decodeKeyspaceChannelKeyParts(rawKey, keyspaceTableSystem)
		if err != nil {
			return err
		}
		if !isChannelCatalogSystemKey(rest) {
			ok = iter.Next()
			continue
		}
		key := channel.ChannelKey(string(rawKey[start:end]))
		appendChannelKeyIfNew(seen, keys, key)
		ok = iter.SeekGE(nextKeyAfterPrefix(encodeKeyspacePrefix(keyspaceTableSystem, key)))
	}
	return iter.Error()
}

func appendChannelKeyIfNew(seen map[channel.ChannelKey]struct{}, keys *[]channel.ChannelKey, key channel.ChannelKey) {
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	*keys = append(*keys, key)
}

func nextKeyAfterPrefix(prefix []byte) []byte {
	next := append([]byte(nil), prefix...)
	for i := len(next) - 1; i >= 0; i-- {
		if next[i] == 0xff {
			continue
		}
		next[i]++
		return next[:i+1]
	}
	return append(prefix, 0xff)
}

func isChannelCatalogSystemKey(rest []byte) bool {
	if len(rest) < 6 || binary.BigEndian.Uint32(rest[:4]) != TableIDMessage {
		return false
	}
	switch binary.BigEndian.Uint16(rest[4:6]) {
	case channelSystemIDCheckpoint, channelSystemIDCommittedCursor, channelSystemIDRetentionState:
		return true
	default:
		return false
	}
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
		e.coordinator = newCommitCoordinatorWithConfig(e.db, e.commitCfg)
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
