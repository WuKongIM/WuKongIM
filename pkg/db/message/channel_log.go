package message

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// ChannelLog owns durable state for one channel.
type ChannelLog struct {
	db  *MessageDB
	key ChannelKey
	id  ChannelID

	appendMu sync.Mutex
	leo      atomic.Uint64
	loaded   atomic.Bool
}

// LEO returns the next append base minus one for this channel log.
func (l *ChannelLog) LEO(ctx context.Context) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if l == nil || l.db == nil || l.db.engine == nil {
		return 0, dberrors.ErrClosed
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
	if state, ok, err := l.LoadRetentionState(ctx); err != nil {
		return 0, err
	} else if ok && state.RetainedMaxSeq > leo {
		leo = state.RetainedMaxSeq
	}
	return leo, nil
}
