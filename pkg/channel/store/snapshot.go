package store

import (
	"errors"

	"github.com/cockroachdb/pebble/v2"
)

func (s *ChannelStore) LoadSnapshotPayload() ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	value, closer, err := s.engine.db.Get(encodeSnapshotKey(s.key))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	defer closer.Close()
	return append([]byte(nil), value...), nil
}

func (s *ChannelStore) StoreSnapshotPayload(payload []byte) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.engine.db.Set(encodeSnapshotKey(s.key), append([]byte(nil), payload...), pebble.Sync)
}
