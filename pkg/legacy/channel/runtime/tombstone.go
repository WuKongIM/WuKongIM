package runtime

import (
	"sync"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

type tombstone struct {
	channelKey core.ChannelKey
	generation uint64
	expiresAt  time.Time
}

type tombstoneManager struct {
	mu         sync.RWMutex
	generation map[core.ChannelKey]map[uint64]tombstone
	beforeAdd  func()
	onDrop     func()
}

func newTombstoneManager() *tombstoneManager {
	return &tombstoneManager{
		generation: make(map[core.ChannelKey]map[uint64]tombstone),
	}
}

func (m *tombstoneManager) setHooks(beforeAdd, onDrop func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.beforeAdd = beforeAdd
	m.onDrop = onDrop
}

func (m *tombstoneManager) add(key core.ChannelKey, generation uint64, expiresAt time.Time) {
	m.mu.RLock()
	beforeAdd := m.beforeAdd
	m.mu.RUnlock()
	if beforeAdd != nil {
		beforeAdd()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	generations, ok := m.generation[key]
	if !ok {
		generations = make(map[uint64]tombstone)
		m.generation[key] = generations
	}
	generations[generation] = tombstone{channelKey: key, generation: generation, expiresAt: expiresAt}
}

func (m *tombstoneManager) contains(key core.ChannelKey, generation uint64) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	generations, ok := m.generation[key]
	if !ok {
		return false
	}
	_, ok = generations[generation]
	return ok
}

func (m *tombstoneManager) dropExpired(now time.Time) {
	m.mu.RLock()
	onDrop := m.onDrop
	m.mu.RUnlock()
	if onDrop != nil {
		onDrop()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for key, generations := range m.generation {
		for generation, stone := range generations {
			if now.Before(stone.expiresAt) {
				continue
			}
			delete(generations, generation)
		}
		if len(generations) == 0 {
			delete(m.generation, key)
		}
	}
}
