package message

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// LoadHistory loads epoch history points in offset order.
func (l *ChannelLog) LoadHistory(ctx context.Context) ([]EpochPoint, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if l == nil || l.db == nil || l.db.engine == nil {
		return nil, false, dberrors.ErrClosed
	}
	prefix := encodeHistoryPrefix(l.key)
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := l.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, false, err
	}
	defer iter.Close()

	points := make([]EpochPoint, 0)
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		point, err := decodeEpochPointFromKeyValue(l.key, iter.Key(), iter.Value)
		if err != nil {
			return nil, false, err
		}
		points = append(points, point)
	}
	if err := iter.Error(); err != nil {
		return nil, false, err
	}
	if len(points) == 0 {
		return nil, false, nil
	}
	return points, true, nil
}

// AppendHistory appends an epoch point when it advances the current history.
func (l *ChannelLog) AppendHistory(ctx context.Context, point EpochPoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l == nil || l.db == nil || l.db.engine == nil {
		return dberrors.ErrClosed
	}
	points, ok, err := l.LoadHistory(ctx)
	if err != nil {
		return err
	}
	if !ok {
		points = nil
	}
	needsAppend, err := shouldAppendHistoryPoint(points, point)
	if err != nil || !needsAppend {
		return err
	}
	batch := l.db.engine.NewBatch()
	defer batch.Close()
	if err := l.writeHistoryPoint(batch, point); err != nil {
		return err
	}
	if err := l.stageCatalog(batch); err != nil {
		return err
	}
	return batch.Commit(true)
}

// TruncateHistoryTo removes history points after leo.
func (l *ChannelLog) TruncateHistoryTo(ctx context.Context, leo uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l == nil || l.db == nil || l.db.engine == nil {
		return dberrors.ErrClosed
	}
	if leo == ^uint64(0) {
		return nil
	}
	prefix := encodeHistoryPrefix(l.key)
	span := keycodec.NewPrefixSpan(prefix)
	batch := l.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.DeleteRange(engine.Span{Start: encodeHistoryOffsetKey(l.key, leo+1), End: span.End}); err != nil {
		return err
	}
	if err := l.stageCatalog(batch); err != nil {
		return err
	}
	return batch.Commit(true)
}

func (l *ChannelLog) writeHistoryPoint(batch *engine.Batch, point EpochPoint) error {
	if point.Epoch == 0 {
		return dberrors.ErrCorruptState
	}
	return batch.Set(encodeHistoryPointKey(l.key, point), encodeEpochPoint(point))
}

func shouldAppendHistoryPoint(points []EpochPoint, point EpochPoint) (bool, error) {
	if point.Epoch == 0 {
		return false, dberrors.ErrCorruptState
	}
	if len(points) == 0 {
		return true, nil
	}
	last := points[len(points)-1]
	switch {
	case point.Epoch > last.Epoch:
		if point.StartOffset < last.StartOffset {
			return false, dberrors.ErrCorruptState
		}
		return true, nil
	case point.Epoch == last.Epoch && point.StartOffset == last.StartOffset:
		return false, nil
	default:
		return false, dberrors.ErrCorruptState
	}
}

func encodeEpochPoint(point EpochPoint) []byte {
	value := make([]byte, 0, 16)
	value = binary.BigEndian.AppendUint64(value, point.Epoch)
	value = binary.BigEndian.AppendUint64(value, point.StartOffset)
	return value
}

func decodeEpochPoint(value []byte) (EpochPoint, error) {
	if len(value) != 16 {
		return EpochPoint{}, dberrors.ErrCorruptValue
	}
	return EpochPoint{
		Epoch:       binary.BigEndian.Uint64(value[0:8]),
		StartOffset: binary.BigEndian.Uint64(value[8:16]),
	}, nil
}

func decodeEpochPointFromKeyValue(channelKey ChannelKey, key []byte, valueFn func() ([]byte, error)) (EpochPoint, error) {
	prefix := encodeHistoryPrefix(channelKey)
	if len(key) != len(prefix)+16 {
		return EpochPoint{}, fmt.Errorf("%w: corrupt history key", dberrors.ErrCorruptValue)
	}
	value, err := valueFn()
	if err != nil {
		return EpochPoint{}, err
	}
	point, err := decodeEpochPoint(value)
	if err != nil {
		return EpochPoint{}, err
	}
	startOffset := binary.BigEndian.Uint64(key[len(prefix) : len(prefix)+8])
	epoch := binary.BigEndian.Uint64(key[len(prefix)+8:])
	if point.StartOffset != startOffset || point.Epoch != epoch {
		return EpochPoint{}, fmt.Errorf("%w: history key/value mismatch", dberrors.ErrCorruptState)
	}
	return point, nil
}
