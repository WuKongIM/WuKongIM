package message

import (
	"bytes"
	"context"
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// CountOrdinaryMessages counts committed log positions excluding SyncOnce entries.
// The caller supplies its effective visibility/read floor and committed frontier.
// A sparse cumulative index keeps steady-state reads independent of history size.
func (l *ChannelLog) CountOrdinaryMessages(ctx context.Context, after, through uint64) (uint64, error) {
	if err := l.beginUse(); err != nil {
		return 0, err
	}
	defer l.endUse()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if through <= after {
		return 0, nil
	}
	l.appendMu.Lock()
	defer l.appendMu.Unlock()
	if err := l.ensureNonBusinessIndexLocked(ctx); err != nil {
		return 0, err
	}
	before, err := l.nonBusinessRank(ctx, after)
	if err != nil {
		return 0, err
	}
	last, err := l.nonBusinessRank(ctx, through)
	if err != nil {
		return 0, err
	}
	if last < before || last-before > through-after {
		return 0, dberrors.ErrCorruptState
	}
	return through - after - (last - before), nil
}

func nonBusinessIndexKey(key ChannelKey, seq uint64) []byte {
	return keycodec.AppendUint64(encodeMessageIndexPrefix(key, messageIndexIDNonBusinessSeq), seq)
}
func nonBusinessVersionKey(key ChannelKey) []byte {
	return encodeMessageSystemPrefix(key, messageSystemIDNonBusinessIndex)
}

// ensureNonBusinessIndexLocked rebuilds older unindexed rows in bounded batches.
// Only a complete rebuild publishes the marker. All primary writers must maintain
// this index after publication; mixed-version writers are not supported.
func (l *channelEntry) ensureNonBusinessIndexLocked(ctx context.Context) error {
	value, ok, err := l.db.engine.Get(nonBusinessVersionKey(l.key))
	if err != nil {
		return err
	}
	if ok {
		if !bytes.Equal(value, []byte{1}) {
			return dberrors.ErrCorruptValue
		}
		return nil
	}
	span := keycodec.NewPrefixSpan(encodeMessageIndexPrefix(l.key, messageIndexIDNonBusinessSeq))
	batch := l.db.engine.NewBatch()
	defer func() { _ = batch.Close() }()
	if err := batch.DeleteRange(engine.Span{Start: span.Start, End: span.End}); err != nil {
		return err
	}
	primary := keycodec.NewPrefixSpan(encodeMessageRowPrefix(l.key))
	it, err := l.db.engine.NewIter(engine.Span{Start: primary.Start, End: primary.End}, engine.IterOptions{})
	if err != nil {
		return err
	}
	defer it.Close()
	var ordinal uint64
	var staged int
	for valid := it.First(); valid; valid = it.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		seq, family, valid := decodeMessageRowKey(l.key, it.Key())
		if !valid {
			return dberrors.ErrCorruptValue
		}
		if family != messageHeaderFamilyID {
			continue
		}
		raw, err := it.Value()
		if err != nil {
			return err
		}
		row := messageRow{MessageSeq: seq}
		if err := decodeMessageHeader(it.Key(), raw, &row); err != nil {
			return err
		}
		if row.FramerFlags&4 == 0 {
			continue
		}
		ordinal++
		if err := batch.Set(nonBusinessIndexKey(l.key, seq), encodeUint64(ordinal)); err != nil {
			return err
		}
		staged++
		if staged == 4096 {
			if err := batch.Commit(true); err != nil {
				return err
			}
			if err := batch.Close(); err != nil {
				return err
			}
			batch = l.db.engine.NewBatch()
			staged = 0
		}
	}
	if err := it.Error(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := batch.Set(nonBusinessVersionKey(l.key), []byte{1}); err != nil {
		return err
	}
	return batch.Commit(true)
}

// nonBusinessRank uses the first surviving ordinal as the baseline after prefix
// retention. Removing an entire prefix therefore never resets unread history.
func (l *channelEntry) nonBusinessRank(ctx context.Context, through uint64) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	prefix := encodeMessageIndexPrefix(l.key, messageIndexIDNonBusinessSeq)
	span := keycodec.NewPrefixSpan(prefix)
	it, err := l.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return 0, err
	}
	defer it.Close()
	found := false
	if through == ^uint64(0) {
		found = it.Last()
	} else {
		found = it.SeekLT(nonBusinessIndexKey(l.key, through+1))
	}
	baseline := false
	if !found {
		if err := it.Error(); err != nil {
			return 0, err
		}
		found = it.First()
		baseline = true
	}
	if !found {
		return 0, it.Error()
	}
	if len(it.Key()) != len(prefix)+8 {
		return 0, dberrors.ErrCorruptValue
	}
	value, err := it.Value()
	if err != nil {
		return 0, err
	}
	if len(value) != 8 {
		return 0, dberrors.ErrCorruptValue
	}
	rank := binary.BigEndian.Uint64(value)
	if rank == 0 {
		return 0, dberrors.ErrCorruptValue
	}
	if baseline {
		rank--
	}
	return rank, nil
}

// nonBusinessStager belongs to one channel's atomic write batch. Its ordinal
// includes earlier uncommitted rows in that batch and is discarded on failure.
type nonBusinessStager struct {
	entry   *channelEntry
	batch   *engine.Batch
	ctx     context.Context
	loaded  bool
	ordinal uint64
	lastSeq uint64
}

func (s *nonBusinessStager) stage(row messageRow, cache appendKeyCache) error {
	if row.FramerFlags&4 != 0 {
		if !s.loaded {
			if err := s.entry.ensureNonBusinessIndexLocked(s.ctx); err != nil {
				return err
			}
			count, err := s.entry.nonBusinessRank(s.ctx, row.MessageSeq-1)
			if err != nil {
				return err
			}
			s.ordinal = count
			s.loaded = true
		}
		if row.MessageSeq <= s.lastSeq || s.ordinal == ^uint64(0) {
			return dberrors.ErrCorruptState
		}
		s.ordinal++
		s.lastSeq = row.MessageSeq
		if err := s.batch.Set(nonBusinessIndexKey(s.entry.key, row.MessageSeq), encodeUint64(s.ordinal)); err != nil {
			return err
		}
	}
	return s.entry.stageMessageRow(s.batch, row, cache)
}
