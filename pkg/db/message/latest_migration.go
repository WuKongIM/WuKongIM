package message

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

const (
	latestMessageIndexVersion       byte = 1
	latestMessageIndexBatchWrites        = 4096
	latestMessageIndexBatchChannels      = 256
	latestMessageIndexBatchPause         = 10 * time.Millisecond
)

// ErrLatestMessageIndexBuilding reports that an upgrade backfill is still running.
var ErrLatestMessageIndexBuilding = errors.New("message: latest message index building")

// ErrLatestMessageIndexMaintenance reports bounded cleanup of stale projection entries.
var ErrLatestMessageIndexMaintenance = errors.New("message: latest message index maintenance")

type latestMessageIndexState struct {
	ready chan struct{}
	once  sync.Once
	mu    sync.Mutex
	err   error
}

type latestMessageIndexProgress struct {
	afterChannel   ChannelKey
	currentChannel ChannelKey
	lastMessageID  uint64
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
	empty, err := db.messageCatalogEmpty()
	if err != nil {
		db.latestIndex.finish(err)
		return
	}
	if empty {
		db.latestIndex.finish(db.storeLatestMessageIndexVersion())
		return
	}
	if db.registry == nil || !db.registry.beginOperation() {
		db.latestIndex.finish(dberrors.ErrClosed)
		return
	}
	go func() {
		defer db.registry.endOperation()
		db.latestIndex.finish(db.backfillLatestMessageIndex(db.latestIndexCtx))
	}()
}

func (db *MessageDB) messageCatalogEmpty() (bool, error) {
	prefix := encodeCatalogPrefix()
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return false, err
	}
	defer iter.Close()
	empty := !iter.First()
	if err := iter.Error(); err != nil {
		return false, err
	}
	return empty, nil
}

func (db *MessageDB) backfillLatestMessageIndex(ctx context.Context) error {
	progress, err := db.loadLatestMessageIndexProgress()
	if err != nil {
		return err
	}
	batch := db.engine.NewBatch()
	defer func() { _ = batch.Close() }()
	writes := 0
	channelsSinceCommit := 0
	progressDirty := false
	commitBatch := func(syncWrite bool, reopen bool) error {
		if writes == 0 && !progressDirty && !syncWrite {
			return nil
		}
		if progressDirty {
			if err := batch.Set(encodeGlobalLatestIndexProgressKey(), encodeLatestMessageIndexProgress(progress)); err != nil {
				return err
			}
		}
		if err := batch.Commit(syncWrite); err != nil {
			return err
		}
		if err := batch.Close(); err != nil {
			return err
		}
		if reopen {
			batch = db.engine.NewBatch()
		} else {
			batch = nil
		}
		writes = 0
		channelsSinceCommit = 0
		progressDirty = false
		return nil
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if progress.currentChannel == "" {
			entries, _, _, err := db.listChannelsPage(ctx, progress.afterChannel, 1)
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				break
			}
			progress.currentChannel = entries[0].Key
			progress.lastMessageID = 0
			progressDirty = true
		}

		prefix := encodeMessageIndexPrefix(progress.currentChannel, messageIndexIDMessageID)
		span := keycodec.NewPrefixSpan(prefix)
		iter, err := db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
		if err != nil {
			return err
		}
		positioned := iter.First()
		if progress.lastMessageID > 0 {
			positioned = iter.SeekGE(encodeMessageIDIndexKey(progress.currentChannel, progress.lastMessageID))
		}
		for ok := positioned; ok; ok = iter.Next() {
			if err := ctx.Err(); err != nil {
				_ = iter.Close()
				return err
			}
			messageID, ok := decodeMessageIDIndexKey(progress.currentChannel, iter.Key())
			if !ok || messageID == 0 {
				_ = iter.Close()
				return dberrors.ErrCorruptValue
			}
			if messageID <= progress.lastMessageID {
				continue
			}
			value, err := iter.Value()
			if err != nil {
				_ = iter.Close()
				return err
			}
			seq, err := decodeMessageIDIndexValue(value)
			if err != nil || seq == 0 {
				_ = iter.Close()
				if err != nil {
					return err
				}
				return dberrors.ErrCorruptValue
			}
			if err := batch.Set(encodeGlobalMessageIDIndexKey(messageID), encodeGlobalMessageIDIndexValue(progress.currentChannel, seq)); err != nil {
				_ = iter.Close()
				return err
			}
			progress.lastMessageID = messageID
			progressDirty = true
			writes++
			if writes >= latestMessageIndexBatchWrites {
				if err := commitBatch(false, true); err != nil {
					_ = iter.Close()
					return err
				}
				if err := pauseLatestMessageIndexBackfill(ctx); err != nil {
					_ = iter.Close()
					return err
				}
			}
		}
		if err := iter.Error(); err != nil {
			_ = iter.Close()
			return err
		}
		if err := iter.Close(); err != nil {
			return err
		}
		progress.afterChannel = progress.currentChannel
		progress.currentChannel = ""
		progress.lastMessageID = 0
		progressDirty = true
		channelsSinceCommit++
		if channelsSinceCommit >= latestMessageIndexBatchChannels {
			if err := commitBatch(false, true); err != nil {
				return err
			}
			channelsSinceCommit = 0
			if err := pauseLatestMessageIndexBackfill(ctx); err != nil {
				return err
			}
		}
	}
	if err := batch.Set(encodeGlobalLatestIndexStateKey(), []byte{latestMessageIndexVersion}); err != nil {
		return err
	}
	if err := batch.Delete(encodeGlobalLatestIndexProgressKey()); err != nil {
		return err
	}
	progressDirty = false
	writes++
	return commitBatch(true, false)
}

func (db *MessageDB) loadLatestMessageIndexProgress() (latestMessageIndexProgress, error) {
	value, ok, err := db.engine.Get(encodeGlobalLatestIndexProgressKey())
	if err != nil || !ok {
		return latestMessageIndexProgress{}, err
	}
	return decodeLatestMessageIndexProgress(value)
}

func encodeLatestMessageIndexProgress(progress latestMessageIndexProgress) []byte {
	value := []byte{latestMessageIndexVersion}
	value = keycodec.AppendString(value, string(progress.afterChannel))
	value = keycodec.AppendString(value, string(progress.currentChannel))
	return binary.BigEndian.AppendUint64(value, progress.lastMessageID)
}

func decodeLatestMessageIndexProgress(value []byte) (latestMessageIndexProgress, error) {
	if len(value) < 1 || value[0] != latestMessageIndexVersion {
		return latestMessageIndexProgress{}, dberrors.ErrCorruptValue
	}
	after, rest, err := keycodec.ReadString(value[1:])
	if err != nil {
		return latestMessageIndexProgress{}, dberrors.ErrCorruptValue
	}
	current, rest, err := keycodec.ReadString(rest)
	if err != nil || len(rest) != 8 {
		return latestMessageIndexProgress{}, dberrors.ErrCorruptValue
	}
	return latestMessageIndexProgress{
		afterChannel:   ChannelKey(after),
		currentChannel: ChannelKey(current),
		lastMessageID:  binary.BigEndian.Uint64(rest),
	}, nil
}

func pauseLatestMessageIndexBackfill(ctx context.Context) error {
	timer := time.NewTimer(latestMessageIndexBatchPause)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (db *MessageDB) storeLatestMessageIndexVersion() error {
	batch := db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(encodeGlobalLatestIndexStateKey(), []byte{latestMessageIndexVersion}); err != nil {
		return err
	}
	return batch.Commit(true)
}

// WaitLatestMessageIndex waits until an upgrade backfill completes.
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
