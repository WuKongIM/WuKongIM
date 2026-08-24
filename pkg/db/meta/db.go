package meta

import (
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/commit"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

const channelCacheCapacity = 8192

// MetaDB owns hash-slot-scoped metadata storage.
type MetaDB struct {
	engine    *engine.DB
	committer *commit.Coordinator

	mu         sync.Mutex
	shards     map[HashSlot]*Shard
	shardLocks map[HashSlot]*sync.Mutex

	channelCache *channelReadCache
	testLocked   []HashSlot
}

// NewDB creates a MetaDB backed by engine.
func NewDB(engine *engine.DB) *MetaDB {
	db := &MetaDB{
		engine:       engine,
		shards:       make(map[HashSlot]*Shard),
		shardLocks:   make(map[HashSlot]*sync.Mutex),
		channelCache: newChannelReadCache(channelCacheCapacity),
	}
	if engine != nil {
		db.committer = commit.NewCoordinator(engine, commit.Config{
			FlushWindow: 500 * time.Microsecond,
			QueueSize:   1024,
			MaxRequests: 128,
		})
	}
	return db
}

func (db *MetaDB) close() {
	if db == nil || db.committer == nil {
		return
	}
	db.committer.Close()
	db.committer = nil
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
	if db == nil || db.channelCache == nil {
		return
	}
	db.channelCache.put(string(cacheKey), channel)
}

func (db *MetaDB) cachedChannel(cacheKey []byte) (Channel, bool) {
	if db == nil || db.channelCache == nil {
		return Channel{}, false
	}
	return db.channelCache.get(string(cacheKey))
}

func (db *MetaDB) forgetChannel(cacheKey []byte) {
	if db == nil || db.channelCache == nil {
		return
	}
	db.channelCache.remove(string(cacheKey))
}

func (db *MetaDB) clearChannelCache() {
	if db == nil || db.channelCache == nil {
		return
	}
	db.channelCache.clear()
}

func (db *MetaDB) channelCacheSize() int {
	if db == nil || db.channelCache == nil {
		return 0
	}
	return db.channelCache.size()
}
