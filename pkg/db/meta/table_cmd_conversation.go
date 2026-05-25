package meta

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

const cmdConversationPrimaryFamilyID uint16 = 0

// CMDConversationState stores one user's durable command-channel sync cursor.
type CMDConversationState struct {
	// UID identifies the user that owns the command conversation state.
	UID string
	// ChannelID identifies the durable command channel.
	ChannelID string
	// ChannelType identifies the command channel namespace.
	ChannelType uint8
	// ReadSeq is the highest command message sequence acknowledged by the user.
	ReadSeq uint64
	// DeletedToSeq is the highest command sequence hidden from future sync.
	DeletedToSeq uint64
	// ActiveAt is the latest command activity timestamp used by active scans.
	ActiveAt int64
	// UpdatedAt records the latest cursor/state mutation timestamp.
	UpdatedAt int64
}

// CMDConversationReadPatch advances one command-channel read cursor.
type CMDConversationReadPatch struct {
	// UID identifies the user that owns the command conversation state.
	UID string
	// ChannelID identifies the durable command channel.
	ChannelID string
	// ChannelType identifies the command channel namespace.
	ChannelType uint8
	// ReadSeq is the candidate acknowledged command sequence.
	ReadSeq uint64
	// UpdatedAt records when the read cursor advance was requested.
	UpdatedAt int64
}

// GetCMDConversationState returns one command conversation state row.
func (s *Shard) GetCMDConversationState(ctx context.Context, uid, channelID string, channelType uint8) (CMDConversationState, bool, error) {
	if err := s.check(ctx); err != nil {
		return CMDConversationState{}, false, err
	}
	if err := validateConversationUID(uid); err != nil {
		return CMDConversationState{}, false, err
	}
	if err := validateConversationKey(ConversationKey{ChannelID: channelID, ChannelType: channelType}); err != nil {
		return CMDConversationState{}, false, err
	}
	key := encodeCMDConversationRowKey(s.hashSlot, uid, channelID, channelType, cmdConversationPrimaryFamilyID)
	return s.getCMDConversationStateByKey(ctx, key, uid, channelID, channelType)
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
	prefix := encodeConversationActiveIndexPrefix(s.hashSlot, TableIDCMDConversation, uid)
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := s.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	states := make([]CMDConversationState, 0, limit)
	for ok := iter.First(); ok && len(states) < limit; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		activeAt, key, err := decodeConversationActiveIndexKey(prefix, iter.Key())
		if err != nil {
			return nil, err
		}
		primaryKey := encodeCMDConversationRowKey(s.hashSlot, uid, key.ChannelID, key.ChannelType, cmdConversationPrimaryFamilyID)
		state, exists, err := s.getCMDConversationStateByKey(ctx, primaryKey, uid, key.ChannelID, key.ChannelType)
		if err != nil {
			return nil, err
		}
		if !exists || state.ActiveAt <= 0 || state.ActiveAt != activeAt {
			continue
		}
		states = append(states, state)
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return states, nil
}

func (s *Shard) getCMDConversationStateByKey(ctx context.Context, key []byte, uid, channelID string, channelType uint8) (CMDConversationState, bool, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return CMDConversationState{}, false, err
		}
	}
	value, ok, err := s.db.get(key)
	if err != nil || !ok {
		return CMDConversationState{}, ok, err
	}
	state, err := decodeCMDConversationValue(uid, channelID, channelType, value)
	if err != nil {
		return CMDConversationState{}, false, err
	}
	return state, true, nil
}

func (s *Shard) stageCMDConversationState(batch *engine.Batch, primaryKey []byte, existing CMDConversationState, exists bool, next CMDConversationState) error {
	if exists && existing.ActiveAt > 0 && existing.ActiveAt != next.ActiveAt {
		if err := batch.Delete(encodeConversationActiveIndexKey(s.hashSlot, TableIDCMDConversation, existing.UID, existing.ActiveAt, existing.ChannelID, existing.ChannelType)); err != nil {
			return err
		}
	}
	if err := batch.Set(primaryKey, encodeConversationValue(next.ReadSeq, next.DeletedToSeq, next.ActiveAt, next.UpdatedAt)); err != nil {
		return err
	}
	if next.ActiveAt > 0 {
		if err := batch.Set(encodeConversationActiveIndexKey(s.hashSlot, TableIDCMDConversation, next.UID, next.ActiveAt, next.ChannelID, next.ChannelType), nil); err != nil {
			return err
		}
	}
	return nil
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

func decodeCMDConversationValue(uid, channelID string, channelType uint8, value []byte) (CMDConversationState, error) {
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
