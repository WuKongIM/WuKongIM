package meta

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

// Channel stores durable channel flags and subscriber metadata version.
type Channel struct {
	// ChannelID identifies the logical channel.
	ChannelID string
	// ChannelType separates namespaces for the same channel id.
	ChannelType int64
	// Ban marks the channel as banned for sends.
	Ban int64
	// Disband marks the channel as disbanded.
	Disband int64
	// SendBan marks the channel as send-banned.
	SendBan int64
	// AllowStranger records whether non-subscribers may access the channel.
	AllowStranger int64
	// Large marks the channel as a large-group channel.
	Large int64
	// SubscriberMutationVersion tracks subscriber list mutation ordering.
	SubscriberMutationVersion uint64
	// SubscriberCount stores the durable number of ordinary subscriber rows.
	SubscriberCount uint64
}

var channelTable = registerMetaTable(TableSpec[Channel]{
	ID:   TableIDChannel,
	Name: "channel",
	Columns: []schema.Column{
		{ID: columnIDStringKey, Name: "channel_id", Type: schema.TypeString, Required: true},
		{ID: columnIDIntKey, Name: "channel_type", Type: schema.TypeInt64, Required: true},
		{ID: columnIDValue, Name: "value", Type: schema.TypeBytes},
		{ID: columnIDUpdatedAt, Name: "updated_at", Type: schema.TypeInt64},
	},
	Families: []schema.Family{{ID: channelPrimaryFamilyID, Name: "primary", Columns: []uint16{columnIDValue, columnIDUpdatedAt}}},
	Primary: PrimarySpec[Channel]{
		IndexID:  channelPrimaryIndexID,
		FamilyID: channelPrimaryFamilyID,
		Name:     "pk_channel",
		Columns:  []uint16{columnIDStringKey, columnIDIntKey},
		Layout:   KeyLayout{KeyString, KeyInt64Ordered},
		Key: func(channel Channel) KeyParts {
			return KeyParts{String(channel.ChannelID), Int64Ordered(channel.ChannelType)}
		},
	},
	Indexes: []IndexSpec[Channel]{
		{
			ID:      channelIDIndexID,
			Name:    "idx_channel_id",
			Columns: []uint16{columnIDStringKey, columnIDIntKey},
			Layout:  KeyLayout{KeyString, KeyInt64Ordered},
			// Keep the pre-runtime durable index key as (channel_id, channel_type).
			PrimaryFromIndex: true,
			// Keep channel bytes in index values for old channel-index readers.
			StorePrimaryValue: true,
			Key: func(channel Channel) (KeyParts, bool) {
				return KeyParts{String(channel.ChannelID), Int64Ordered(channel.ChannelType)}, true
			},
		},
		{
			ID:             channelActiveIndexID,
			Name:           "idx_channel_active",
			Columns:        []uint16{columnIDUpdatedAt, columnIDStringKey},
			Layout:         KeyLayout{KeyInt64Ordered, KeyString},
			DescriptorOnly: true,
		},
	},
	Validate: validateChannel,
	EncodeValue: func(channel Channel) ([]byte, error) {
		return encodeChannelValue(channel), nil
	},
	DecodeValue: func(primary KeyParts, value []byte) (Channel, error) {
		return decodeChannelValue(primary[0].S, primary[1].I64, value)
	},
})

// ChannelTable describes the channel table schema.
var ChannelTable = channelTable.Schema()

// CreateChannel inserts a channel and rejects duplicates.
func (s *Shard) CreateChannel(ctx context.Context, channel Channel) error {
	primaryKey, err := s.writeChannel(ctx, channel, tableWriteCreate)
	if err != nil {
		return err
	}
	s.db.forgetChannel(primaryKey)
	return nil
}

// UpsertChannel stores a channel regardless of prior existence.
func (s *Shard) UpsertChannel(ctx context.Context, channel Channel) error {
	primaryKey, err := s.writeChannel(ctx, channel, tableWriteUpsert)
	if err != nil {
		return err
	}
	s.db.forgetChannel(primaryKey)
	return nil
}

// UpdateChannel updates an existing channel.
func (s *Shard) UpdateChannel(ctx context.Context, channel Channel) error {
	primaryKey, err := s.writeChannel(ctx, channel, tableWriteUpdate)
	if err != nil {
		return err
	}
	s.db.forgetChannel(primaryKey)
	return nil
}

// GetChannel returns one channel by ID and type.
func (s *Shard) GetChannel(ctx context.Context, channelID string, channelType int64) (Channel, bool, error) {
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
	channel, ok, err := channelTable.Get(ctx, s, KeyParts{String(channelID), Int64Ordered(channelType)})
	if err != nil || !ok {
		return Channel{}, ok, err
	}
	s.db.rememberChannel(primaryKey, channel)
	return channel, true, nil
}

// DeleteChannel removes one channel and its channel-id index entry.
func (s *Shard) DeleteChannel(ctx context.Context, channelID string, channelType int64) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateKeyString(channelID); err != nil {
		return err
	}
	primaryKey := encodeChannelRowKey(s.hashSlot, channelID, channelType, channelPrimaryFamilyID)
	if err := channelTable.Delete(ctx, s, KeyParts{String(channelID), Int64Ordered(channelType)}); err != nil {
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
	return channelTable.ScanIndexAll(ctx, s, channelIDIndexID, KeyParts{String(channelID)})
}

func (s *Shard) stageChannel(batch *engine.Batch, primaryKey []byte, channel Channel) error {
	pk, err := channelTable.primaryKey(channel)
	if err != nil {
		return err
	}
	value, err := channelTable.spec.EncodeValue(channel)
	if err != nil {
		return err
	}
	if err := batch.Set(primaryKey, value); err != nil {
		return err
	}
	if err := channelTable.stagePutIndexEntries(batch, s.hashSlot, channel, pk, value); err != nil {
		return err
	}
	return nil
}

func (s *Shard) writeChannel(ctx context.Context, channel Channel, mode tableWriteMode) ([]byte, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	if err := validateChannel(channel); err != nil {
		return nil, err
	}
	pk, err := channelTable.primaryKey(channel)
	if err != nil {
		return nil, err
	}
	unlock := s.lock()
	defer unlock()

	primaryKey, err := channelTable.primaryRowKey(s.hashSlot, pk)
	if err != nil {
		return nil, err
	}
	existingValue, exists, err := s.db.get(primaryKey)
	if err != nil {
		return nil, err
	}
	switch mode {
	case tableWriteCreate:
		if exists {
			return nil, dberrors.ErrAlreadyExists
		}
	case tableWriteUpdate:
		if !exists {
			return nil, dberrors.ErrNotFound
		}
	}
	if exists && mode != tableWriteCreate {
		existing, err := decodeChannelValue(channel.ChannelID, channel.ChannelType, existingValue)
		if err != nil && !errors.Is(err, dberrors.ErrCorruptValue) {
			return nil, err
		}
		if err == nil {
			channel.SubscriberCount = existing.SubscriberCount
		}
	}

	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := s.stageChannel(batch, primaryKey, channel); err != nil {
		return nil, err
	}
	if err := batch.Commit(true); err != nil {
		return nil, err
	}
	return primaryKey, nil
}

func validateChannel(channel Channel) error {
	return validateKeyString(channel.ChannelID)
}

func encodeChannelValue(channel Channel) []byte {
	value := appendValueInt64(nil, channel.Ban)
	value = appendValueInt64(value, channel.Disband)
	value = appendValueInt64(value, channel.SendBan)
	value = appendValueInt64(value, channel.AllowStranger)
	value = appendValueUint64(value, channel.SubscriberMutationVersion)
	value = appendValueUint64(value, channel.SubscriberCount)
	value = appendValueInt64(value, channel.Large)
	return value
}

func decodeChannelValue(channelID string, channelType int64, value []byte) (Channel, error) {
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
	subscriberCount, rest, err := readValueUint64(rest)
	if err != nil {
		return Channel{}, err
	}
	large, rest, err := readValueInt64(rest)
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
		Large:                     large,
		SubscriberMutationVersion: version,
		SubscriberCount:           subscriberCount,
	}, nil
}
