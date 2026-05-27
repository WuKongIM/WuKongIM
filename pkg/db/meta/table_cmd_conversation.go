package meta

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

const cmdConversationPrimaryFamilyID uint16 = 0

// CMDConversationState stores one user's durable command-channel sync cursor.
type CMDConversationState struct {
	// UID identifies the user that owns the command conversation state.
	UID string
	// ChannelID identifies the durable command channel.
	ChannelID string
	// ChannelType identifies the command channel namespace.
	ChannelType int64
	// ReadSeq is the highest command message sequence acknowledged by the user.
	ReadSeq uint64
	// DeletedToSeq is the highest command sequence hidden from future sync.
	DeletedToSeq uint64
	// ActiveAt is the latest command activity timestamp used by active scans.
	ActiveAt int64
	// UpdatedAt records the latest cursor/state mutation timestamp.
	UpdatedAt int64
}

var cmdConversationTable = registerMetaTable(TableSpec[CMDConversationState]{
	ID:   TableIDCMDConversation,
	Name: "cmd_conversation",
	Columns: []schema.Column{
		{ID: conversationColumnUID, Name: "uid", Type: schema.TypeString, Required: true},
		{ID: conversationColumnChannelID, Name: "channel_id", Type: schema.TypeString, Required: true},
		{ID: conversationColumnChannelType, Name: "channel_type", Type: schema.TypeInt64, Required: true},
		{ID: conversationColumnValue, Name: "value", Type: schema.TypeBytes},
		{ID: conversationColumnActiveAt, Name: "active_at", Type: schema.TypeInt64},
	},
	Families: []schema.Family{{ID: cmdConversationPrimaryFamilyID, Name: "primary", Columns: []uint16{conversationColumnValue, conversationColumnActiveAt}}},
	Primary: PrimarySpec[CMDConversationState]{
		IndexID:  conversationPrimaryIndexID,
		FamilyID: cmdConversationPrimaryFamilyID,
		Name:     "pk_cmd_conversation",
		Columns:  []uint16{conversationColumnUID, conversationColumnChannelID, conversationColumnChannelType},
		Layout:   KeyLayout{KeyString, KeyString, KeyInt64Ordered},
		Key: func(state CMDConversationState) KeyParts {
			return KeyParts{String(state.UID), String(state.ChannelID), Int64Ordered(state.ChannelType)}
		},
	},
	Indexes: []IndexSpec[CMDConversationState]{
		{
			ID:      conversationActiveIndexID,
			Name:    "idx_cmd_conversation_active",
			Columns: []uint16{conversationColumnUID, conversationColumnActiveAt, conversationColumnChannelID, conversationColumnChannelType},
			Layout:  KeyLayout{KeyString, KeyInt64Desc, KeyString, KeyInt64Ordered},
			Key: func(state CMDConversationState) (KeyParts, bool) {
				if state.ActiveAt <= 0 {
					return nil, false
				}
				return KeyParts{String(state.UID), Int64Desc(state.ActiveAt), String(state.ChannelID), Int64Ordered(state.ChannelType)}, true
			},
			PrimaryKeyFromIndexParts: conversationPrimaryFromActiveIndexParts,
			CorruptIndexKeyIsError:   true,
		},
	},
	Validate: validateCMDConversationState,
	EncodeValue: func(state CMDConversationState) ([]byte, error) {
		return encodeConversationValue(state.ReadSeq, state.DeletedToSeq, state.ActiveAt, state.UpdatedAt), nil
	},
	DecodeValue: func(primary KeyParts, value []byte) (CMDConversationState, error) {
		return decodeCMDConversationValue(primary[0].S, primary[1].S, primary[2].I64, value)
	},
})

// CMDConversationTable describes the command conversation table schema.
var CMDConversationTable = cmdConversationTable.Schema()

// CMDConversationReadPatch advances one command-channel read cursor.
type CMDConversationReadPatch struct {
	// UID identifies the user that owns the command conversation state.
	UID string
	// ChannelID identifies the durable command channel.
	ChannelID string
	// ChannelType identifies the command channel namespace.
	ChannelType int64
	// ReadSeq is the candidate acknowledged command sequence.
	ReadSeq uint64
	// UpdatedAt records when the read cursor advance was requested.
	UpdatedAt int64
}

// GetCMDConversationState returns one command conversation state row.
func (s *Shard) GetCMDConversationState(ctx context.Context, uid, channelID string, channelType int64) (CMDConversationState, bool, error) {
	if err := s.check(ctx); err != nil {
		return CMDConversationState{}, false, err
	}
	if err := validateConversationUID(uid); err != nil {
		return CMDConversationState{}, false, err
	}
	if err := validateConversationKey(ConversationKey{ChannelID: channelID, ChannelType: channelType}); err != nil {
		return CMDConversationState{}, false, err
	}
	state, ok, err := cmdConversationTable.Get(ctx, s, KeyParts{String(uid), String(channelID), Int64Ordered(channelType)})
	return state, ok, err
}

// UpsertCMDConversationState stores a command conversation state with max-field merging.
func (s *Shard) UpsertCMDConversationState(ctx context.Context, state CMDConversationState) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateCMDConversationState(state); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()

	primaryKey := encodeCMDConversationRowKey(s.hashSlot, state.UID, state.ChannelID, state.ChannelType, cmdConversationPrimaryFamilyID)
	existing, exists, err := s.getCMDConversationStateByKey(ctx, primaryKey, state.UID, state.ChannelID, state.ChannelType)
	if err != nil {
		return err
	}
	if exists {
		state = mergeCMDConversationState(existing, state)
	}
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := s.stageCMDConversationState(batch, primaryKey, existing, exists, state); err != nil {
		return err
	}
	return batch.Commit(true)
}

// AdvanceCMDConversationReadSeq advances read_seq without creating missing rows.
func (s *Shard) AdvanceCMDConversationReadSeq(ctx context.Context, patch CMDConversationReadPatch) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateCMDConversationReadPatch(patch); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()

	primaryKey := encodeCMDConversationRowKey(s.hashSlot, patch.UID, patch.ChannelID, patch.ChannelType, cmdConversationPrimaryFamilyID)
	current, exists, err := s.getCMDConversationStateByKey(ctx, primaryKey, patch.UID, patch.ChannelID, patch.ChannelType)
	if err != nil || !exists {
		return err
	}
	if patch.ReadSeq <= current.ReadSeq {
		return nil
	}
	current.ReadSeq = patch.ReadSeq
	if patch.UpdatedAt > current.UpdatedAt {
		current.UpdatedAt = patch.UpdatedAt
	}

	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := s.stageCMDConversationState(batch, primaryKey, current, true, current); err != nil {
		return err
	}
	return batch.Commit(true)
}

// ListCMDConversationActive returns active command conversations in newest-first order.
func (s *Shard) ListCMDConversationActive(ctx context.Context, uid string, limit int) ([]CMDConversationState, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	if err := validateConversationUID(uid); err != nil {
		return nil, err
	}
	if err := validateConversationLimit(limit); err != nil {
		return nil, err
	}
	rows, err := cmdConversationTable.scanIndexRows(ctx, s, conversationActiveIndexID, KeyParts{String(uid)}, limit)
	return rows, err
}

func (s *Shard) getCMDConversationStateByKey(ctx context.Context, key []byte, uid, channelID string, channelType int64) (CMDConversationState, bool, error) {
	return cmdConversationTable.Get(ctx, s, KeyParts{String(uid), String(channelID), Int64Ordered(channelType)})
}

func (s *Shard) stageCMDConversationState(batch *engine.Batch, primaryKey []byte, existing CMDConversationState, exists bool, next CMDConversationState) error {
	pk := KeyParts{String(next.UID), String(next.ChannelID), Int64Ordered(next.ChannelType)}
	if exists {
		if err := cmdConversationTable.stageDeleteIndexEntries(batch, s.hashSlot, existing, pk); err != nil {
			return err
		}
	}
	value := encodeConversationValue(next.ReadSeq, next.DeletedToSeq, next.ActiveAt, next.UpdatedAt)
	if err := batch.Set(primaryKey, value); err != nil {
		return err
	}
	return cmdConversationTable.stagePutIndexEntries(batch, s.hashSlot, next, pk, value)
}

func validateCMDConversationState(state CMDConversationState) error {
	if err := validateConversationUID(state.UID); err != nil {
		return err
	}
	return validateConversationKey(ConversationKey{ChannelID: state.ChannelID, ChannelType: state.ChannelType})
}

func validateCMDConversationReadPatch(patch CMDConversationReadPatch) error {
	if err := validateConversationUID(patch.UID); err != nil {
		return err
	}
	return validateConversationKey(ConversationKey{ChannelID: patch.ChannelID, ChannelType: patch.ChannelType})
}

func mergeCMDConversationState(existing, next CMDConversationState) CMDConversationState {
	next.UID = existing.UID
	next.ChannelID = existing.ChannelID
	next.ChannelType = existing.ChannelType
	if next.ReadSeq < existing.ReadSeq {
		next.ReadSeq = existing.ReadSeq
	}
	if next.DeletedToSeq < existing.DeletedToSeq {
		next.DeletedToSeq = existing.DeletedToSeq
	}
	if next.ActiveAt < existing.ActiveAt {
		next.ActiveAt = existing.ActiveAt
	}
	if next.UpdatedAt < existing.UpdatedAt {
		next.UpdatedAt = existing.UpdatedAt
	}
	return next
}

func decodeCMDConversationValue(uid, channelID string, channelType int64, value []byte) (CMDConversationState, error) {
	readSeq, deletedToSeq, activeAt, updatedAt, err := decodeConversationValue(value)
	if err != nil {
		return CMDConversationState{}, err
	}
	return CMDConversationState{
		UID:          uid,
		ChannelID:    channelID,
		ChannelType:  channelType,
		ReadSeq:      readSeq,
		DeletedToSeq: deletedToSeq,
		ActiveAt:     activeAt,
		UpdatedAt:    updatedAt,
	}, nil
}
