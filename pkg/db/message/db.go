package message

import "github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"

// MessageDB owns channel-scoped message log storage.
type MessageDB struct {
	engine *engine.DB
}

// NewDB creates a MessageDB backed by engine.
func NewDB(engine *engine.DB) *MessageDB {
	return &MessageDB{engine: engine}
}

// Channel returns a typed handle for one channel log.
func (db *MessageDB) Channel(key ChannelKey, id ChannelID) *ChannelLog {
	return &ChannelLog{db: db, key: key, id: id}
}

// ChannelLog owns durable state for one channel.
type ChannelLog struct {
	db  *MessageDB
	key ChannelKey
	id  ChannelID
}
