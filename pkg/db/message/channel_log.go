package message

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// channelEntry owns the canonical mutable state for one active channel.
type channelEntry struct {
	// db owns the physical engine and lifecycle guard used by this entry.
	db *MessageDB
	// key is the durable channel partition key.
	key ChannelKey
	// id is the logical identity required for same-key acquisitions.
	id ChannelID

	// appendKeyCache contains immutable encoded key prefixes for this channel.
	appendKeyCache appendKeyCache
	// appendMu serializes append frontier mutations for the canonical channel.
	appendMu sync.Mutex
	// checkpointMu serializes checkpoint reads followed by writes.
	checkpointMu sync.Mutex
	// leo caches the last durable message sequence after it is loaded.
	leo atomic.Uint64
	// loaded reports whether leo has been recovered from durable state.
	loaded atomic.Bool
	// detached rejects use after registry or database reclamation.
	detached atomic.Bool
	// refs counts caller leases and background commit pins under registry.mu.
	refs uint64
}

// ChannelLog is one idempotently closable lease over a canonical channel entry.
type ChannelLog struct {
	// channelEntry is the shared canonical mutable state for this lease.
	*channelEntry
	// registry owns this lease's terminal reference release.
	registry *channelRegistry

	// closeOnce makes lease release idempotent.
	closeOnce sync.Once
	// closed rejects new operations before waiting for admitted operations.
	closed atomic.Bool
	// inflight counts operations admitted on this lease.
	inflight atomic.Int64
	// useMu and useCond coordinate Close with admitted operations.
	useMu   sync.Mutex
	useCond sync.Cond
}

// Close releases this lease after its already-started operations finish.
func (l *ChannelLog) Close() error {
	if l == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		l.closed.Store(true)
		l.useMu.Lock()
		for l.inflight.Load() != 0 {
			l.useCond.Wait()
		}
		l.useMu.Unlock()
		if l.registry != nil {
			l.registry.releaseLease(l.channelEntry)
		}
	})
	return nil
}

func (l *ChannelLog) validateLease() error {
	if l == nil || l.channelEntry == nil || l.registry == nil || l.closed.Load() || l.detached.Load() || l.db == nil {
		return dberrors.ErrClosed
	}
	return nil
}

func (l *ChannelLog) beginUse() error {
	if err := l.validateLease(); err != nil {
		return err
	}
	if err := l.db.beginUse(); err != nil {
		return err
	}
	l.inflight.Add(1)
	if l.closed.Load() || l.detached.Load() {
		l.endLocalUse()
		l.db.endUse()
		return dberrors.ErrClosed
	}
	return nil
}

func (l *ChannelLog) endUse() {
	if l == nil || l.channelEntry == nil {
		return
	}
	l.endLocalUse()
	l.db.endUse()
}

func (l *ChannelLog) endLocalUse() {
	if l.inflight.Add(-1) == 0 && l.closed.Load() {
		l.useMu.Lock()
		l.useCond.Broadcast()
		l.useMu.Unlock()
	}
}

// LEO returns the next append base minus one for this channel log.
func (l *ChannelLog) LEO(ctx context.Context) (uint64, error) {
	if err := l.beginUse(); err != nil {
		return 0, err
	}
	defer l.endUse()
	return l.loadLEO(ctx)
}

func (l *ChannelLog) loadLEO(ctx context.Context) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if l.loaded.Load() {
		return l.leo.Load(), nil
	}
	l.appendMu.Lock()
	defer l.appendMu.Unlock()
	return l.loadLEOLocked(ctx)
}

func (l *ChannelLog) loadLEOLocked(ctx context.Context) (uint64, error) {
	if l.loaded.Load() {
		return l.leo.Load(), nil
	}
	leo, err := l.recoverLEO(ctx)
	if err != nil {
		return 0, err
	}
	l.leo.Store(leo)
	l.loaded.Store(true)
	return leo, nil
}

func (l *ChannelLog) recoverLEO(ctx context.Context) (uint64, error) {
	prefix := encodeMessageRowPrefix(l.key)
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := l.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return 0, err
	}
	defer iter.Close()

	var leo uint64
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		seq, familyID, ok := decodeMessageRowKey(l.key, iter.Key())
		if !ok || familyID != messageHeaderFamilyID {
			continue
		}
		if seq > leo {
			leo = seq
		}
	}
	if err := iter.Error(); err != nil {
		return 0, err
	}
	if state, ok, err := l.loadRetentionState(ctx); err != nil {
		return 0, err
	} else if ok && state.RetainedMaxSeq > leo {
		leo = state.RetainedMaxSeq
	}
	return leo, nil
}
