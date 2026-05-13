package meta

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"

	"github.com/cockroachdb/pebble/v2"
)

type Channel struct {
	ChannelID   string
	ChannelType int64
	Ban         int64
	// Disband marks a channel as dissolved and blocks sends.
	Disband int64
	// SendBan blocks sends while preserving receive semantics.
	SendBan int64
	// SubscriberMutationVersion is the durable version fence for subscriber mutations.
	SubscriberMutationVersion uint64
}

// ChannelCursor identifies the last emitted channel in a shard page scan.
type ChannelCursor struct {
	// ChannelID is the last emitted channel ID in primary-key order.
	ChannelID string
	// ChannelType is the last emitted channel type in primary-key order.
	ChannelType int64
}

func (s *ShardStore) CreateChannel(ctx context.Context, ch Channel) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if err := validateChannel(ch); err != nil {
		return err
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	primaryKey := encodeChannelPrimaryKey(s.slot, ch.ChannelID, ch.ChannelType, channelPrimaryFamilyID)
	exists, err := s.db.hasKey(primaryKey)
	if err != nil {
		return err
	}
	if exists {
		return ErrAlreadyExists
	}
	s.db.runAfterExistenceCheckHook()
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}

	value := encodeChannelFamilyValue(ch.Ban, ch.Disband, ch.SendBan, ch.SubscriberMutationVersion, primaryKey)
	indexKey := encodeChannelIDIndexKey(s.slot, ch.ChannelID, ch.ChannelType)
	indexValue := encodeChannelIndexValue(ch.Ban)

	batch := s.db.db.NewBatch()
	defer batch.Close()

	if err := batch.Set(primaryKey, value, nil); err != nil {
		return err
	}
	if err := batch.Set(indexKey, indexValue, nil); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (s *ShardStore) GetChannel(ctx context.Context, channelID string, channelType int64) (Channel, error) {
	if err := s.validate(); err != nil {
		return Channel{}, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return Channel{}, err
	}
	if channelID == "" {
		return Channel{}, ErrInvalidArgument
	}

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()

	return s.getChannelLocked(channelID, channelType)
}

func (s *ShardStore) getChannelLocked(channelID string, channelType int64) (Channel, error) {
	primaryKey := encodeChannelPrimaryKey(s.slot, channelID, channelType, channelPrimaryFamilyID)
	ch, exists, err := s.getChannelForPrimaryKeyLocked(primaryKey, channelID, channelType)
	if err != nil {
		return Channel{}, err
	}
	if !exists {
		return Channel{}, ErrNotFound
	}
	return ch, nil
}

func (s *ShardStore) getChannelForPrimaryKeyLocked(primaryKey []byte, channelID string, channelType int64) (Channel, bool, error) {
	value, err := s.db.getValue(primaryKey)
	if err != nil {
		if err == ErrNotFound {
			return Channel{}, false, nil
		}
		return Channel{}, false, err
	}

	ban, disband, sendBan, version, err := decodeChannelFamilyValue(primaryKey, value)
	if err != nil {
		return Channel{}, false, err
	}
	return Channel{
		ChannelID:                 channelID,
		ChannelType:               channelType,
		Ban:                       ban,
		Disband:                   disband,
		SendBan:                   sendBan,
		SubscriberMutationVersion: version,
	}, true, nil
}

func (s *ShardStore) ListChannelsByChannelID(ctx context.Context, channelID string) ([]Channel, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return nil, err
	}
	if channelID == "" {
		return nil, ErrInvalidArgument
	}

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()

	prefix := encodeChannelIDIndexPrefix(s.slot, channelID)
	iter, err := s.db.db.NewIter(nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var channels []Channel
	for iter.SeekGE(prefix); iter.Valid(); iter.Next() {
		if err := s.db.checkContext(ctx); err != nil {
			return nil, err
		}
		indexKey := iter.Key()
		if !bytes.HasPrefix(indexKey, prefix) {
			break
		}

		channelType, rest, err := decodeOrderedInt64(indexKey[len(prefix):])
		if err != nil {
			return nil, err
		}
		if len(rest) != 0 {
			return nil, fmt.Errorf("%w: malformed channel index key", ErrCorruptValue)
		}

		indexValue, err := iter.ValueAndErr()
		if err != nil {
			return nil, err
		}
		if _, err := decodeChannelIndexValue(indexKey, indexValue); err != nil {
			return nil, err
		}
		channel, err := s.getChannelLocked(channelID, channelType)
		if err != nil {
			return nil, err
		}
		channels = append(channels, channel)
	}

	if err := iter.Error(); err != nil {
		return nil, err
	}
	return channels, nil
}

// ListChannelsPage scans one hash slot page in primary-key order.
func (s *ShardStore) ListChannelsPage(ctx context.Context, after ChannelCursor, limit int) ([]Channel, ChannelCursor, bool, error) {
	if err := s.validate(); err != nil {
		return nil, ChannelCursor{}, false, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return nil, ChannelCursor{}, false, err
	}
	if err := validateChannelCursor(after); err != nil {
		return nil, ChannelCursor{}, false, err
	}
	if limit <= 0 {
		return nil, ChannelCursor{}, false, ErrInvalidArgument
	}

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()

	prefix := encodeChannelPrimaryPrefix(s.slot)
	lowerBound := prefix
	if after != (ChannelCursor{}) {
		lowerBound = nextPrefix(encodeChannelPrimaryKey(s.slot, after.ChannelID, after.ChannelType, channelPrimaryFamilyID))
	}

	iter, err := s.db.db.NewIter(&pebble.IterOptions{
		LowerBound: lowerBound,
		UpperBound: nextPrefix(prefix),
	})
	if err != nil {
		return nil, ChannelCursor{}, false, err
	}
	defer iter.Close()

	channels := make([]Channel, 0, limit+1)
	for ok := iter.SeekGE(lowerBound); ok; ok = iter.Next() {
		if err := s.db.checkContext(ctx); err != nil {
			return nil, ChannelCursor{}, false, err
		}

		key := iter.Key()
		if !bytes.HasPrefix(key, prefix) {
			break
		}
		value, err := iter.ValueAndErr()
		if err != nil {
			return nil, ChannelCursor{}, false, err
		}

		channel, familyID, err := decodeChannelRecord(key, value, prefix)
		if err != nil {
			return nil, ChannelCursor{}, false, err
		}
		if familyID != channelPrimaryFamilyID {
			continue
		}

		channels = append(channels, channel)
		if len(channels) > limit {
			return channels[:limit], channelToCursor(channels[limit-1]), false, nil
		}
	}
	if err := iter.Error(); err != nil {
		return nil, ChannelCursor{}, false, err
	}

	cursor := after
	if len(channels) > 0 {
		cursor = channelToCursor(channels[len(channels)-1])
	}
	return channels, cursor, true, nil
}

func (s *ShardStore) UpdateChannel(ctx context.Context, ch Channel) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if err := validateChannel(ch); err != nil {
		return err
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	primaryKey := encodeChannelPrimaryKey(s.slot, ch.ChannelID, ch.ChannelType, channelPrimaryFamilyID)
	exists, err := s.db.hasKey(primaryKey)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}

	existing, exists, err := s.getChannelForPrimaryKeyLocked(primaryKey, ch.ChannelID, ch.ChannelType)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if ch.SubscriberMutationVersion == 0 {
		ch.SubscriberMutationVersion = existing.SubscriberMutationVersion
	}
	value := encodeChannelFamilyValue(ch.Ban, ch.Disband, ch.SendBan, ch.SubscriberMutationVersion, primaryKey)
	indexKey := encodeChannelIDIndexKey(s.slot, ch.ChannelID, ch.ChannelType)
	indexValue := encodeChannelIndexValue(ch.Ban)

	batch := s.db.db.NewBatch()
	defer batch.Close()

	if err := batch.Set(primaryKey, value, nil); err != nil {
		return err
	}
	if err := batch.Set(indexKey, indexValue, nil); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (s *ShardStore) UpsertChannel(ctx context.Context, ch Channel) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if err := validateChannel(ch); err != nil {
		return err
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	primaryKey := encodeChannelPrimaryKey(s.slot, ch.ChannelID, ch.ChannelType, channelPrimaryFamilyID)
	existing, exists, err := s.getChannelForPrimaryKeyLocked(primaryKey, ch.ChannelID, ch.ChannelType)
	if err != nil {
		return err
	}
	if exists && ch.SubscriberMutationVersion == 0 {
		ch.SubscriberMutationVersion = existing.SubscriberMutationVersion
	}
	value := encodeChannelFamilyValue(ch.Ban, ch.Disband, ch.SendBan, ch.SubscriberMutationVersion, primaryKey)
	indexKey := encodeChannelIDIndexKey(s.slot, ch.ChannelID, ch.ChannelType)
	indexValue := encodeChannelIndexValue(ch.Ban)

	batch := s.db.db.NewBatch()
	defer batch.Close()

	if err := batch.Set(primaryKey, value, nil); err != nil {
		return err
	}
	if err := batch.Set(indexKey, indexValue, nil); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (s *ShardStore) DeleteChannel(ctx context.Context, channelID string, channelType int64) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if channelID == "" {
		return ErrInvalidArgument
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	primaryKey := encodeChannelPrimaryKey(s.slot, channelID, channelType, channelPrimaryFamilyID)
	exists, err := s.db.hasKey(primaryKey)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}

	indexKey := encodeChannelIDIndexKey(s.slot, channelID, channelType)
	subscriberPrefix := encodeSubscriberChannelPrefix(s.slot, channelID, channelType)

	batch := s.db.db.NewBatch()
	defer batch.Close()

	if err := batch.Delete(primaryKey, nil); err != nil {
		return err
	}
	if err := batch.Delete(indexKey, nil); err != nil {
		return err
	}
	if err := batch.DeleteRange(subscriberPrefix, nextPrefix(subscriberPrefix), nil); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func validateChannel(ch Channel) error {
	if ch.ChannelID == "" || len(ch.ChannelID) > maxKeyStringLen {
		return ErrInvalidArgument
	}
	return nil
}

func validateChannelCursor(cursor ChannelCursor) error {
	if cursor == (ChannelCursor{}) {
		return nil
	}
	if cursor.ChannelID == "" || len(cursor.ChannelID) > maxKeyStringLen {
		return ErrInvalidArgument
	}
	return nil
}

func channelToCursor(ch Channel) ChannelCursor {
	return ChannelCursor{ChannelID: ch.ChannelID, ChannelType: ch.ChannelType}
}

func encodeChannelPrimaryPrefix(hashSlot uint16) []byte {
	return encodeStatePrefix(hashSlot, ChannelTable.ID)
}

func decodeChannelRecord(key, value, prefix []byte) (Channel, uint16, error) {
	rest := key[len(prefix):]
	channelID, rest, err := decodeKeyString(rest)
	if err != nil {
		return Channel{}, 0, err
	}
	channelType, rest, err := decodeOrderedInt64(rest)
	if err != nil {
		return Channel{}, 0, err
	}
	familyID, n := binary.Uvarint(rest)
	if n <= 0 {
		return Channel{}, 0, ErrCorruptValue
	}
	if len(rest[n:]) != 0 {
		return Channel{}, 0, ErrCorruptValue
	}

	ban, disband, sendBan, subscriberMutationVersion, err := decodeChannelFamilyValue(key, value)
	if err != nil {
		return Channel{}, 0, err
	}
	return Channel{
		ChannelID:                 channelID,
		ChannelType:               channelType,
		Ban:                       ban,
		Disband:                   disband,
		SendBan:                   sendBan,
		SubscriberMutationVersion: subscriberMutationVersion,
	}, uint16(familyID), nil
}
