package store

import (
	"encoding/binary"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/cockroachdb/pebble/v2"
)

// LoadCommittedDispatchCursor loads the last message sequence dispatched by a
// replay lane. Missing cursors are normal and mean replay should start at seq 1.
func (s *ChannelStore) LoadCommittedDispatchCursor(name string) (uint64, bool, error) {
	if err := s.validateCursorName(name); err != nil {
		return 0, false, err
	}
	value, closer, err := s.engine.db.Get(encodeCommittedDispatchCursorKey(s.key, name))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return 0, false, nil
		}
		return 0, false, err
	}
	defer closer.Close()
	if len(value) != 8 {
		return 0, false, channel.ErrCorruptValue
	}
	return binary.BigEndian.Uint64(value), true, nil
}

// StoreCommittedDispatchCursor persists replay progress for a lane. The cursor
// is a duplicate-suppression hint, so NoSync keeps replay off the send hot path;
// losing the latest cursor only causes safe duplicate replay from channel log.
func (s *ChannelStore) StoreCommittedDispatchCursor(name string, seq uint64) error {
	if err := s.validateCursorName(name); err != nil {
		return err
	}
	value := binary.BigEndian.AppendUint64(nil, seq)
	return s.engine.db.Set(encodeCommittedDispatchCursorKey(s.key, name), value, pebble.NoSync)
}

func (s *ChannelStore) validateCursorName(name string) error {
	if err := s.validate(); err != nil {
		return err
	}
	if name == "" {
		return channel.ErrInvalidArgument
	}
	return nil
}
