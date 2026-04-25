package store

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/cockroachdb/pebble/v2"
)

type appliedMessage struct {
	key   channel.IdempotencyKey
	entry channel.IdempotencyEntry
}

func (s *ChannelStore) PutIdempotency(key channel.IdempotencyKey, entry channel.IdempotencyEntry) error {
	if err := s.validateIdempotencyKey(key); err != nil {
		return err
	}
	if err := s.engine.db.Set(encodeIdempotencyIndexKey(s.key, key), encodeIndexedIdempotencyEntryValue(entry, 0), pebble.Sync); err != nil {
		return err
	}
	s.recordDurableCommit()
	return nil
}

func (s *ChannelStore) GetIdempotency(key channel.IdempotencyKey) (channel.IdempotencyEntry, bool, error) {
	if err := s.validateIdempotencyKey(key); err != nil {
		return channel.IdempotencyEntry{}, false, err
	}
	value, closer, err := s.engine.db.Get(encodeIdempotencyIndexKey(s.key, key))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return channel.IdempotencyEntry{}, false, nil
		}
		return channel.IdempotencyEntry{}, false, err
	}
	defer closer.Close()

	entry, _, err := decodeIndexedIdempotencyEntryValue(value)
	if err != nil {
		return channel.IdempotencyEntry{}, false, err
	}
	return entry, true, nil
}

func (s *ChannelStore) SnapshotIdempotency(offset uint64) ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}

	prefix := encodeIdempotencyIndexPrefix(s.key)
	iter, err := s.engine.db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: keyUpperBound(prefix)})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	entries := make([]stateSnapshotEntry, 0, 16)
	for valid := iter.First(); valid; valid = iter.Next() {
		key, err := decodeIdempotencyIndexKey(iter.Key(), prefix)
		if err != nil {
			return nil, err
		}
		entry, payloadHash, err := decodeIndexedIdempotencyEntryValue(iter.Value())
		if err != nil {
			return nil, err
		}
		if entry.Offset >= offset {
			continue
		}
		entries = append(entries, stateSnapshotEntry{
			FromUID:     key.FromUID,
			ClientMsgNo: key.ClientMsgNo,
			Entry:       entry,
			PayloadHash: payloadHash,
		})
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return encodeStateSnapshot(entries), nil
}

func (s *ChannelStore) RestoreIdempotency(snapshot []byte) error {
	if err := s.validate(); err != nil {
		return err
	}

	entries, err := decodeStateSnapshot(snapshot)
	if err != nil {
		return err
	}

	prefix := encodeIdempotencyIndexPrefix(s.key)
	batch := s.engine.db.NewBatch()
	defer batch.Close()

	if err := batch.DeleteRange(prefix, keyUpperBound(prefix), pebble.NoSync); err != nil {
		return err
	}
	for _, entry := range entries {
		key := channel.IdempotencyKey{
			ChannelID:   s.id,
			FromUID:     entry.FromUID,
			ClientMsgNo: entry.ClientMsgNo,
		}
		if err := batch.Set(encodeIdempotencyIndexKey(s.key, key), encodeIndexedIdempotencyEntryValue(entry.Entry, entry.PayloadHash), pebble.NoSync); err != nil {
			return err
		}
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return err
	}
	s.recordDurableCommit()
	return nil
}

func (s *ChannelStore) commitCommitted(checkpoint channel.Checkpoint, batch []appliedMessage) error {
	if err := s.validate(); err != nil {
		return err
	}

	writeBatch := s.engine.db.NewBatch()
	defer writeBatch.Close()

	if err := s.writeCommitted(writeBatch, checkpoint, batch); err != nil {
		return err
	}
	if err := writeBatch.Commit(pebble.Sync); err != nil {
		return err
	}
	s.recordDurableCommit()
	return nil
}

func (s *ChannelStore) commitCommittedWithCheckpoint(checkpoint channel.Checkpoint, batch []appliedMessage) error {
	if err := s.validate(); err != nil {
		return err
	}

	writeBatch := s.engine.db.NewBatch()
	defer writeBatch.Close()

	if err := s.writeCommitted(writeBatch, checkpoint, batch); err != nil {
		return err
	}
	if err := writeBatch.Set(encodeCheckpointKey(s.key), encodeCheckpoint(checkpoint), pebble.NoSync); err != nil {
		return err
	}
	if err := writeBatch.Commit(pebble.Sync); err != nil {
		return err
	}
	s.recordDurableCommit()
	return nil
}

func (s *ChannelStore) buildCommitCommittedWithCheckpoint(writeBatch *pebble.Batch, checkpoint channel.Checkpoint, batch []appliedMessage) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.writeCommitted(writeBatch, checkpoint, batch); err != nil {
		return err
	}
	return s.writeCheckpoint(writeBatch, checkpoint)
}

func (s *ChannelStore) validateIdempotencyKey(key channel.IdempotencyKey) error {
	if err := s.validate(); err != nil {
		return err
	}
	if key.ChannelID != s.id {
		return channel.ErrInvalidArgument
	}
	return nil
}

func (s *ChannelStore) writeCommitted(writeBatch *pebble.Batch, checkpoint channel.Checkpoint, batch []appliedMessage) error {
	for _, message := range batch {
		if err := s.validateIdempotencyKey(message.key); err != nil {
			return err
		}
		if message.entry.Offset >= checkpoint.HW {
			return channel.ErrInvalidArgument
		}
		if err := writeBatch.Set(encodeIdempotencyIndexKey(s.key, message.key), encodeIndexedIdempotencyEntryValue(message.entry, 0), pebble.NoSync); err != nil {
			return err
		}
	}
	return nil
}
