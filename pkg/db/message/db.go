package message

import (
	"context"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// MessageDB owns channel-scoped message log storage.
type MessageDB struct {
	// engine is the shared physical message store closed by this domain.
	engine *engine.DB
	// registry owns canonical entries and database operation admission.
	registry *channelRegistry
	// latestIndex tracks the versioned background backfill required by global latest-message reads.
	latestIndex       *latestMessageIndexState
	latestIndexCtx    context.Context
	latestIndexCancel context.CancelFunc

	// closeOnce ensures the physical engine closes exactly once.
	closeOnce sync.Once
	// closeErr preserves the first physical close result.
	closeErr error
}

// NewDB creates a MessageDB backed by engine.
func NewDB(engine *engine.DB) *MessageDB {
	latestIndexCtx, latestIndexCancel := context.WithCancel(context.Background())
	db := &MessageDB{
		engine:            engine,
		registry:          newChannelRegistry(),
		latestIndex:       newLatestMessageIndexState(),
		latestIndexCtx:    latestIndexCtx,
		latestIndexCancel: latestIndexCancel,
	}
	db.initializeLatestMessageIndex()
	return db
}

// Channel returns a typed handle for one channel log.
func (db *MessageDB) Channel(key ChannelKey, id ChannelID) (*ChannelLog, error) {
	if db == nil || db.registry == nil {
		return nil, dberrors.ErrClosed
	}
	return db.registry.acquire(db, key, id)
}

// Close rejects new work, drains active operations and pins, and closes the backing engine once.
func (db *MessageDB) Close() error {
	return db.closeWithBeforeEngineClose(nil)
}

func (db *MessageDB) closeWithBeforeEngineClose(before func()) error {
	if db == nil {
		return nil
	}
	db.closeOnce.Do(func() {
		if db.latestIndexCancel != nil {
			db.latestIndexCancel()
		}
		if db.registry != nil {
			db.registry.beginClose()
			db.registry.waitForDrain()
			db.registry.detachEntries()
		}
		eng := db.engine
		db.engine = nil
		if before != nil {
			before()
		}
		if eng != nil {
			db.closeErr = eng.Close()
		}
	})
	return db.closeErr
}

func (db *MessageDB) beginUse() error {
	if db == nil || db.registry == nil || !db.registry.beginOperation() {
		return dberrors.ErrClosed
	}
	if db.engine == nil {
		db.registry.endOperation()
		return dberrors.ErrClosed
	}
	return nil
}

func (db *MessageDB) endUse() {
	if db != nil && db.registry != nil {
		db.registry.endOperation()
	}
}
