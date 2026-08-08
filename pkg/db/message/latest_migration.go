package message

import (
	"context"
	"errors"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

const latestMessageIndexVersion byte = 1

// ErrLatestMessageIndexBuilding reports that latest-message index startup has
// not reached a terminal state yet.
var ErrLatestMessageIndexBuilding = errors.New("message: latest message index building")

// ErrLatestMessageIndexMaintenance reports bounded cleanup of stale index entries.
var ErrLatestMessageIndexMaintenance = errors.New("message: latest message index maintenance")

type latestMessageIndexState struct {
	ready chan struct{}
	once  sync.Once
	mu    sync.Mutex
	err   error
}

func newLatestMessageIndexState() *latestMessageIndexState {
	return &latestMessageIndexState{ready: make(chan struct{})}
}

func (s *latestMessageIndexState) finish(err error) {
	if s == nil {
		return
	}
	s.once.Do(func() {
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		close(s.ready)
	})
}

func (s *latestMessageIndexState) result() (bool, error) {
	if s == nil {
		return false, dberrors.ErrClosed
	}
	select {
	case <-s.ready:
		s.mu.Lock()
		defer s.mu.Unlock()
		return true, s.err
	default:
		return false, nil
	}
}

func (db *MessageDB) initializeLatestMessageIndex() {
	if db == nil || db.engine == nil || db.latestIndex == nil {
		return
	}
	value, ok, err := db.engine.Get(encodeGlobalLatestIndexStateKey())
	if err != nil {
		db.latestIndex.finish(err)
		return
	}
	if ok {
		if len(value) != 1 || value[0] != latestMessageIndexVersion {
			db.latestIndex.finish(dberrors.ErrCorruptValue)
			return
		}
		db.latestIndex.finish(nil)
		return
	}
	db.latestIndex.finish(db.storeLatestMessageIndexVersion())
}

func (db *MessageDB) storeLatestMessageIndexVersion() error {
	batch := db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(encodeGlobalLatestIndexStateKey(), []byte{latestMessageIndexVersion}); err != nil {
		return err
	}
	return batch.Commit(true)
}

// WaitLatestMessageIndex waits until index startup completes.
func (db *MessageDB) WaitLatestMessageIndex(ctx context.Context) error {
	if db == nil || db.latestIndex == nil {
		return dberrors.ErrClosed
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-db.latestIndex.ready:
		_, err := db.latestIndex.result()
		return err
	}
}
