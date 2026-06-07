package meta

import (
	"context"
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

const (
	channelLatestColumnChannelID      uint16 = 1
	channelLatestColumnChannelType    uint16 = 2
	channelLatestColumnLastMessageID  uint16 = 3
	channelLatestColumnLastMessageSeq uint16 = 4
	channelLatestColumnLastAt         uint16 = 5
	channelLatestColumnFromUID        uint16 = 6
	channelLatestColumnClientMsgNo    uint16 = 7
	channelLatestColumnPayload        uint16 = 8
	channelLatestColumnUpdatedAt      uint16 = 9
)

// ChannelLatest stores the newest durable message projection for one channel.
type ChannelLatest struct {
	// ChannelID identifies the projected channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// LastMessageID is the newest durable message id observed for the channel.
	LastMessageID uint64
	// LastMessageSeq is the newest durable channel sequence observed.
	LastMessageSeq uint64
	// LastAt records the message timestamp used for conversation list sorting.
	LastAt int64
	// FromUID identifies the sender of the newest projected message.
	FromUID string
	// ClientMsgNo stores the sender client idempotency key for diagnostics.
	ClientMsgNo string
	// Payload stores the newest message payload or preview bytes.
	Payload []byte
	// UpdatedAt records when this projection row was last advanced.
	UpdatedAt int64
}

var channelLatestTable = registerMetaTable(TableSpec[ChannelLatest]{
	ID:   TableIDChannelLatest,
	Name: "channel_latest",
	Columns: []schema.Column{
		{ID: channelLatestColumnChannelID, Name: "channel_id", Type: schema.TypeString, Required: true},
		{ID: channelLatestColumnChannelType, Name: "channel_type", Type: schema.TypeInt64, Required: true},
		{ID: channelLatestColumnLastMessageID, Name: "last_message_id", Type: schema.TypeUint64},
		{ID: channelLatestColumnLastMessageSeq, Name: "last_message_seq", Type: schema.TypeUint64},
		{ID: channelLatestColumnLastAt, Name: "last_at", Type: schema.TypeInt64},
		{ID: channelLatestColumnFromUID, Name: "from_uid", Type: schema.TypeString},
		{ID: channelLatestColumnClientMsgNo, Name: "client_msg_no", Type: schema.TypeString},
		{ID: channelLatestColumnPayload, Name: "payload", Type: schema.TypeBytes},
		{ID: channelLatestColumnUpdatedAt, Name: "updated_at", Type: schema.TypeInt64},
	},
	Families: []schema.Family{{ID: channelLatestPrimaryFamilyID, Name: "primary", Columns: []uint16{
		channelLatestColumnLastMessageID,
		channelLatestColumnLastMessageSeq,
		channelLatestColumnLastAt,
		channelLatestColumnFromUID,
		channelLatestColumnClientMsgNo,
		channelLatestColumnPayload,
		channelLatestColumnUpdatedAt,
	}}},
	Primary: PrimarySpec[ChannelLatest]{
		IndexID:  channelLatestPrimaryIndexID,
		FamilyID: channelLatestPrimaryFamilyID,
		Name:     "pk_channel_latest",
		Columns:  []uint16{channelLatestColumnChannelID, channelLatestColumnChannelType},
		Layout:   KeyLayout{KeyString, KeyInt64Ordered},
		Key: func(latest ChannelLatest) KeyParts {
			return channelLatestPrimaryKey(latest.ChannelID, latest.ChannelType)
		},
	},
	Validate: validateChannelLatest,
	EncodeValue: func(latest ChannelLatest) ([]byte, error) {
		return encodeChannelLatestValue(latest), nil
	},
	DecodeValue: func(primary KeyParts, value []byte) (ChannelLatest, error) {
		return decodeChannelLatestValue(primary[0].S, primary[1].I64, value)
	},
})

// ChannelLatestTable describes the channel latest projection table schema.
var ChannelLatestTable = channelLatestTable.Schema()

// GetChannelLatest returns one channel latest projection row.
func (s *Shard) GetChannelLatest(ctx context.Context, channelID string, channelType int64) (ChannelLatest, bool, error) {
	if err := s.check(ctx); err != nil {
		return ChannelLatest{}, false, err
	}
	if err := validateConversationKey(ConversationKey{ChannelID: channelID, ChannelType: channelType}); err != nil {
		return ChannelLatest{}, false, err
	}
	return channelLatestTable.Get(ctx, s, channelLatestPrimaryKey(channelID, channelType))
}

// UpsertChannelLatest advances the channel latest projection monotonically by message sequence.
func (s *Shard) UpsertChannelLatest(ctx context.Context, latest ChannelLatest) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateChannelLatest(latest); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()

	primaryKey, err := channelLatestRowKey(s.hashSlot, latest.ChannelID, latest.ChannelType)
	if err != nil {
		return err
	}
	existing, exists, err := channelLatestTable.getByPrimaryKey(s.db, s.hashSlot, channelLatestPrimaryKey(latest.ChannelID, latest.ChannelType))
	if err != nil {
		return err
	}
	next := resolveChannelLatest(existing, exists, latest)
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := stageChannelLatest(batch, primaryKey, next); err != nil {
		return err
	}
	return batch.Commit(true)
}

func (b *Batch) UpsertChannelLatest(hashSlot HashSlot, latest ChannelLatest) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if err := validateChannelLatest(latest); err != nil {
		return err
	}
	pk := channelLatestPrimaryKey(latest.ChannelID, latest.ChannelType)
	primaryKey, err := channelLatestTable.primaryRowKey(hashSlot, pk)
	if err != nil {
		return err
	}
	b.addOp(hashSlot, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		existing, exists, err := channelLatestTable.loadBatchRow(state, hashSlot, pk, primaryKey)
		if err != nil {
			return err
		}
		next := resolveChannelLatest(existing, exists, latest)
		if err := stageChannelLatest(batch, primaryKey, next); err != nil {
			return err
		}
		value := encodeChannelLatestValue(next)
		state.tableRows[string(primaryKey)] = tableRowOverlay{value: append([]byte(nil), value...), exists: true}
		return nil
	})
	return nil
}

func stageChannelLatest(batch *engine.Batch, primaryKey []byte, latest ChannelLatest) error {
	return batch.Set(primaryKey, encodeChannelLatestValue(latest))
}

func resolveChannelLatest(existing ChannelLatest, exists bool, next ChannelLatest) ChannelLatest {
	if !exists {
		next.Payload = append([]byte(nil), next.Payload...)
		return next
	}
	if next.LastMessageSeq <= existing.LastMessageSeq {
		return existing
	}
	next.Payload = append([]byte(nil), next.Payload...)
	return next
}

func channelLatestPrimaryKey(channelID string, channelType int64) KeyParts {
	return KeyParts{String(channelID), Int64Ordered(channelType)}
}

func channelLatestRowKey(hashSlot HashSlot, channelID string, channelType int64) ([]byte, error) {
	return channelLatestTable.primaryRowKey(hashSlot, channelLatestPrimaryKey(channelID, channelType))
}

func validateChannelLatest(latest ChannelLatest) error {
	return validateConversationKey(ConversationKey{ChannelID: latest.ChannelID, ChannelType: latest.ChannelType})
}

func encodeChannelLatestValue(latest ChannelLatest) []byte {
	value := appendValueUint64(nil, latest.LastMessageID)
	value = appendValueUint64(value, latest.LastMessageSeq)
	value = appendValueInt64(value, latest.LastAt)
	value = appendValueString(value, latest.FromUID)
	value = appendValueString(value, latest.ClientMsgNo)
	value = appendValueBytes(value, latest.Payload)
	return appendValueInt64(value, latest.UpdatedAt)
}

func decodeChannelLatestValue(channelID string, channelType int64, value []byte) (ChannelLatest, error) {
	lastMessageID, rest, err := readValueUint64(value)
	if err != nil {
		return ChannelLatest{}, err
	}
	lastMessageSeq, rest, err := readValueUint64(rest)
	if err != nil {
		return ChannelLatest{}, err
	}
	lastAt, rest, err := readValueInt64(rest)
	if err != nil {
		return ChannelLatest{}, err
	}
	fromUID, rest, err := readValueString(rest)
	if err != nil {
		return ChannelLatest{}, err
	}
	clientMsgNo, rest, err := readValueString(rest)
	if err != nil {
		return ChannelLatest{}, err
	}
	payload, rest, err := readValueBytes(rest)
	if err != nil {
		return ChannelLatest{}, err
	}
	updatedAt, rest, err := readValueInt64(rest)
	if err != nil {
		return ChannelLatest{}, err
	}
	if len(rest) != 0 {
		return ChannelLatest{}, dberrors.ErrCorruptValue
	}
	return ChannelLatest{
		ChannelID:      channelID,
		ChannelType:    channelType,
		LastMessageID:  lastMessageID,
		LastMessageSeq: lastMessageSeq,
		LastAt:         lastAt,
		FromUID:        fromUID,
		ClientMsgNo:    clientMsgNo,
		Payload:        payload,
		UpdatedAt:      updatedAt,
	}, nil
}

func appendValueBytes(dst []byte, value []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func readValueBytes(src []byte) ([]byte, []byte, error) {
	if len(src) < 4 {
		return nil, nil, dberrors.ErrCorruptValue
	}
	n := int(binary.BigEndian.Uint32(src[:4]))
	src = src[4:]
	if n < 0 || len(src) < n {
		return nil, nil, dberrors.ErrCorruptValue
	}
	return append([]byte(nil), src[:n]...), src[n:], nil
}
