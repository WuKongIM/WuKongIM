package meta

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// MetaDB owns hash-slot-scoped metadata storage.
type MetaDB struct {
	engine *engine.DB

	mu         sync.Mutex
	shards     map[HashSlot]*Shard
	shardLocks map[HashSlot]*sync.Mutex

	channelCache map[string]Channel
	testLocked   []HashSlot
}

// NewDB creates a MetaDB backed by engine.
func NewDB(engine *engine.DB) *MetaDB {
	return &MetaDB{
		engine:       engine,
		shards:       make(map[HashSlot]*Shard),
		shardLocks:   make(map[HashSlot]*sync.Mutex),
		channelCache: make(map[string]Channel),
	}
}

// HashSlot returns a stable shard handle for hashSlot.
func (db *MetaDB) HashSlot(hashSlot HashSlot) *Shard {
	if db == nil {
		return nil
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.shards == nil {
		db.shards = make(map[HashSlot]*Shard)
	}
	if shard := db.shards[hashSlot]; shard != nil {
		return shard
	}
	shard := &Shard{db: db, hashSlot: hashSlot}
	db.shards[hashSlot] = shard
	return shard
}

func (db *MetaDB) lockHashSlots(hashSlots []HashSlot) func() {
	ordered := orderedHashSlots(hashSlots)
	locks := make([]*sync.Mutex, 0, len(ordered))
	for _, hashSlot := range ordered {
		lock := db.lockForHashSlot(hashSlot)
		lock.Lock()
		locks = append(locks, lock)
		db.mu.Lock()
		db.testLocked = append(db.testLocked, hashSlot)
		db.mu.Unlock()
	}
	return func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].Unlock()
		}
		db.mu.Lock()
		db.testLocked = nil
		db.mu.Unlock()
	}
}

func (db *MetaDB) lockForHashSlot(hashSlot HashSlot) *sync.Mutex {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.shardLocks == nil {
		db.shardLocks = make(map[HashSlot]*sync.Mutex)
	}
	if lock := db.shardLocks[hashSlot]; lock != nil {
		return lock
	}
	lock := &sync.Mutex{}
	db.shardLocks[hashSlot] = lock
	return lock
}

func (db *MetaDB) testLockedOrder() []HashSlot {
	db.mu.Lock()
	defer db.mu.Unlock()
	return append([]HashSlot(nil), db.testLocked...)
}

func (db *MetaDB) rememberChannel(cacheKey []byte, channel Channel) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.channelCache == nil {
		db.channelCache = make(map[string]Channel)
	}
	db.channelCache[string(cacheKey)] = channel
}

func (db *MetaDB) cachedChannel(cacheKey []byte) (Channel, bool) {
	db.mu.Lock()
	defer db.mu.Unlock()
	channel, ok := db.channelCache[string(cacheKey)]
	return channel, ok
}

func (db *MetaDB) forgetChannel(cacheKey []byte) {
	db.mu.Lock()
	defer db.mu.Unlock()
	delete(db.channelCache, string(cacheKey))
}

func (db *MetaDB) clearChannelCache() {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.channelCache = make(map[string]Channel)
}

func (db *MetaDB) channelCacheSize() int {
	db.mu.Lock()
	defer db.mu.Unlock()
	return len(db.channelCache)
}
