package store

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/cockroachdb/pebble/v2"
)

func (s *ChannelStore) LoadCheckpoint() (channel.Checkpoint, error) {
	if err := s.validate(); err != nil {
		return channel.Checkpoint{}, err
	}
	value, closer, err := s.engine.db.Get(encodeCheckpointKey(s.key))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return channel.Checkpoint{}, channel.ErrEmptyState
		}
		return channel.Checkpoint{}, err
	}
	defer closer.Close()
	return decodeCheckpoint(value)
}

func (s *ChannelStore) StoreCheckpoint(checkpoint channel.Checkpoint) error {
	if err := s.validate(); err != nil {
		return err
	}

	coordinator := s.checkpointCoordinator()
	if coordinator != nil {
		return coordinator.submit(commitRequest{
			channelKey: s.key,
			build: func(writeBatch *pebble.Batch) error {
				return s.writeCheckpoint(writeBatch, checkpoint)
			},
			publish: func() error {
				s.recordDurableCommit()
				return nil
			},
		})
	}

	writeBatch := s.engine.db.NewBatch()
	defer writeBatch.Close()

	if err := s.writeCheckpoint(writeBatch, checkpoint); err != nil {
		return err
	}
	if err := writeBatch.Commit(pebble.Sync); err != nil {
		return err
	}
	s.recordDurableCommit()
	return nil
}

func (s *ChannelStore) writeCheckpoint(writeBatch *pebble.Batch, checkpoint channel.Checkpoint) error {
	if err := s.validate(); err != nil {
		return err
	}
	return writeBatch.Set(encodeCheckpointKey(s.key), encodeCheckpoint(checkpoint), pebble.NoSync)
}

func (s *ChannelStore) storeCheckpointAndMaybeDeleteSnapshot(checkpoint channel.Checkpoint, deleteSnapshot bool) error {
	if err := s.validate(); err != nil {
		return err
	}

	batch := s.engine.db.NewBatch()
	defer batch.Close()

	if err := batch.Set(encodeCheckpointKey(s.key), encodeCheckpoint(checkpoint), pebble.NoSync); err != nil {
		return err
	}
	if deleteSnapshot {
		if err := batch.Delete(encodeSnapshotKey(s.key), pebble.NoSync); err != nil {
			return err
		}
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return err
	}
	s.recordDurableCommit()
	return nil
}
