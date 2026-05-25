package meta

import (
	"bytes"
	"context"
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// Channel stores durable channel flags and subscriber metadata version.
type Channel struct {
	ChannelID                 string
	ChannelType               uint8
	Ban                       int64
	Disband                   int64
	SendBan                   int64
	AllowStranger             int64
	SubscriberMutationVersion uint64
}

// CreateChannel inserts a channel and rejects duplicates.
func (s *Shard) CreateChannel(ctx context.Context, channel Channel) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateKeyString(channel.ChannelID); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()
	primaryKey := encodeChannelRowKey(s.hashSlot, channel.ChannelID, channel.ChannelType, channelPrimaryFamilyID)
	if _, ok, err := s.db.get(primaryKey); err != nil || ok {
		if err != nil {
			return err
		}
		return dberrors.ErrAlreadyExists
	}
	return s.writeChannelLocked(primaryKey, channel)
}

// UpsertChannel stores a channel regardless of prior existence.
func (s *Shard) UpsertChannel(ctx context.Context, channel Channel) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateKeyString(channel.ChannelID); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()
	return s.writeChannelLocked(encodeChannelRowKey(s.hashSlot, channel.ChannelID, channel.ChannelType, channelPrimaryFamilyID), channel)
}

// UpdateChannel updates an existing channel.
func (s *Shard) UpdateChannel(ctx context.Context, channel Channel) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateKeyString(channel.ChannelID); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()
	primaryKey := encodeChannelRowKey(s.hashSlot, channel.ChannelID, channel.ChannelType, channelPrimaryFamilyID)
	if _, ok, err := s.db.get(primaryKey); err != nil || !ok {
		if err != nil {
			return err
		}
		return dberrors.ErrNotFound
	}
	return s.writeChannelLocked(primaryKey, channel)
}

// GetChannel returns one channel by ID and type.
func (s *Shard) GetChannel(ctx context.Context, channelID string, channelType uint8) (Channel, bool, error) {
	if err := s.check(ctx); err != nil {
		return Channel{}, false, err
	}
	if err := validateKeyString(channelID); err != nil {
		return Channel{}, false, err
	}
	primaryKey := encodeChannelRowKey(s.hashSlot, channelID, channelType, channelPrimaryFamilyID)
	if channel, ok := s.db.cachedChannel(primaryKey); ok {
		return channel, true, nil
	}
	value, ok, err := s.db.get(primaryKey)
	if err != nil || !ok {
		return Channel{}, ok, err
	}
	channel, err := decodeChannelValue(channelID, channelType, value)
	if err != nil {
		return Channel{}, false, err
	}
	s.db.rememberChannel(primaryKey, channel)
	return channel, true, nil
}

// DeleteChannel removes one channel and its channel-id index entry.
func (s *Shard) DeleteChannel(ctx context.Context, channelID string, channelType uint8) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateKeyString(channelID); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()
	primaryKey := encodeChannelRowKey(s.hashSlot, channelID, channelType, channelPrimaryFamilyID)
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Delete(primaryKey); err != nil {
		return err
	}
	if err := batch.Delete(encodeChannelIDIndexKey(s.hashSlot, channelID, int64(channelType))); err != nil {
		return err
	}
	if err := batch.Commit(true); err != nil {
		return err
	}
	s.db.forgetChannel(primaryKey)
	return nil
}

// ListChannelsByChannelID returns channels with channelID ordered by type.
func (s *Shard) ListChannelsByChannelID(ctx context.Context, channelID string) ([]Channel, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	if err := validateKeyString(channelID); err != nil {
		return nil, err
	}
	prefix := encodeChannelIDIndexPrefix(s.hashSlot, channelID)
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := s.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	channels := make([]Channel, 0)
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		channelType, ok := decodeChannelIDIndexType(prefix, iter.Key())
		if !ok {
			return nil, dberrors.ErrCorruptValue
		}
		value, err := iter.Value()
		if err != nil {
			return nil, err
		}
		channel, err := decodeChannelValue(channelID, channelType, value)
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

func (s *Shard) writeChannelLocked(primaryKey []byte, channel Channel) error {
	value := encodeChannelValue(channel)
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(primaryKey, value); err != nil {
		return err
	}
	if err := batch.Set(encodeChannelIDIndexKey(s.hashSlot, channel.ChannelID, int64(channel.ChannelType)), value); err != nil {
		return err
	}
	if err := batch.Commit(true); err != nil {
		return err
	}
	s.db.forgetChannel(primaryKey)
	return nil
}

func encodeChannelValue(channel Channel) []byte {
	value := appendValueInt64(nil, channel.Ban)
	value = appendValueInt64(value, channel.Disband)
	value = appendValueInt64(value, channel.SendBan)
	value = appendValueInt64(value, channel.AllowStranger)
	value = appendValueUint64(value, channel.SubscriberMutationVersion)
	return value
}

func decodeChannelValue(channelID string, channelType uint8, value []byte) (Channel, error) {
	ban, rest, err := readValueInt64(value)
	if err != nil {
		return Channel{}, err
	}
	disband, rest, err := readValueInt64(rest)
	if err != nil {
		return Channel{}, err
	}
	sendBan, rest, err := readValueInt64(rest)
	if err != nil {
		return Channel{}, err
	}
	allowStranger, rest, err := readValueInt64(rest)
	if err != nil {
		return Channel{}, err
	}
	version, rest, err := readValueUint64(rest)
	if err != nil {
		return Channel{}, err
	}
	if len(rest) != 0 {
		return Channel{}, dberrors.ErrCorruptValue
	}
	return Channel{
		ChannelID:                 channelID,
		ChannelType:               channelType,
		Ban:                       ban,
		Disband:                   disband,
		SendBan:                   sendBan,
		AllowStranger:             allowStranger,
		SubscriberMutationVersion: version,
	}, nil
}

func decodeChannelIDIndexType(prefix []byte, key []byte) (uint8, bool) {
	if !bytes.HasPrefix(key, prefix) {
		return 0, false
	}
	rest := key[len(prefix):]
	if len(rest) != 8 {
		return 0, false
	}
	ordered := binary.BigEndian.Uint64(rest)
	value := int64(ordered ^ (uint64(1) << 63))
	return uint8(value), true
}
