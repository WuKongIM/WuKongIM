package message

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// MessageDB owns channel-scoped message log storage.
type MessageDB struct {
	engine *engine.DB
	mu     sync.Mutex
	logs   map[ChannelKey]*ChannelLog
}

// NewDB creates a MessageDB backed by engine.
func NewDB(engine *engine.DB) *MessageDB {
	return &MessageDB{
		engine: engine,
		logs:   make(map[ChannelKey]*ChannelLog),
	}
}

// Channel returns a typed handle for one channel log.
func (db *MessageDB) Channel(key ChannelKey, id ChannelID) *ChannelLog {
	if db == nil {
		return nil
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.logs == nil {
		db.logs = make(map[ChannelKey]*ChannelLog)
	}
	if log := db.logs[key]; log != nil {
		return log
	}
	log := &ChannelLog{db: db, key: key, id: id}
	db.logs[key] = log
	return log
}
