package store

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/cockroachdb/pebble/v2"
)

func (s *ChannelStore) LoadHistory() ([]channel.EpochPoint, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	prefix := encodeHistoryPrefix(s.key)
	iter, err := s.engine.db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: keyUpperBound(prefix)})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	points := make([]channel.EpochPoint, 0)
	for valid := iter.First(); valid; valid = iter.Next() {
		point, err := decodeEpochPoint(iter.Value())
		if err != nil {
			return nil, err
		}
		points = append(points, point)
	}
	if len(points) == 0 {
		return nil, channel.ErrEmptyState
	}
	return points, nil
}

func (s *ChannelStore) loadHistoryOrEmpty() ([]channel.EpochPoint, error) {
	points, err := s.LoadHistory()
	if errors.Is(err, channel.ErrEmptyState) {
		return nil, nil
	}
	return points, err
}

func (s *ChannelStore) AppendHistory(point channel.EpochPoint) error {
	if err := s.validate(); err != nil {
		return err
	}
	points, err := s.loadHistoryOrEmpty()
	if err != nil {
		return err
	}
	needsAppend, err := shouldAppendHistoryPoint(points, point)
	if err != nil {
		return err
	}
	if !needsAppend {
		return nil
	}

	return s.engine.db.Set(encodeHistoryPointKey(s.key, point), encodeEpochPoint(point), pebble.Sync)
}

func shouldAppendHistoryPoint(points []channel.EpochPoint, point channel.EpochPoint) (bool, error) {
	if point.Epoch == 0 {
		return false, channel.ErrCorruptState
	}
	if len(points) == 0 {
		return true, nil
	}
	last := points[len(points)-1]
	switch {
	case point.Epoch > last.Epoch:
		if point.StartOffset < last.StartOffset {
			return false, channel.ErrCorruptState
		}
		return true, nil
	case point.Epoch == last.Epoch && point.StartOffset == last.StartOffset:
		return false, nil
	default:
		return false, channel.ErrCorruptState
	}
}

func (s *ChannelStore) writeHistoryPoint(writeBatch *pebble.Batch, point channel.EpochPoint) error {
	if err := s.validate(); err != nil {
		return err
	}
	return writeBatch.Set(encodeHistoryPointKey(s.key, point), encodeEpochPoint(point), pebble.NoSync)
}

func (s *ChannelStore) TruncateHistoryTo(leo uint64) error {
	if leo == ^uint64(0) {
		return nil
	}
	return s.trimHistoryAfter(leo + 1)
}

func (s *ChannelStore) trimHistoryAfter(startOffset uint64) error {
	if err := s.validate(); err != nil {
		return err
	}
	prefix := encodeHistoryPrefix(s.key)
	batch := s.engine.db.NewBatch()
	defer batch.Close()
	if err := batch.DeleteRange(encodeHistoryOffsetKey(s.key, startOffset), keyUpperBound(prefix), pebble.Sync); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}
