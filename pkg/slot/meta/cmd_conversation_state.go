package meta

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"

	"github.com/cockroachdb/pebble/v2"
)

// CMDConversationState stores one user's durable command-channel sync cursor.
type CMDConversationState struct {
	// UID identifies the user that owns the CMD conversation state.
	UID string
	// ChannelID identifies the durable command channel, e.g. source____cmd.
	ChannelID string
	// ChannelType identifies the command channel kind.
	ChannelType int64
	// ReadSeq is the highest command-channel message sequence acknowledged by the user.
	ReadSeq uint64
	// DeletedToSeq is the highest command-channel sequence hidden from future sync.
	DeletedToSeq uint64
	// ActiveAt is the latest command activity timestamp used by active scans.
	ActiveAt int64
	// UpdatedAt records the latest cursor/state mutation timestamp.
	UpdatedAt int64
}

// CMDConversationReadPatch advances one CMD conversation read cursor.
type CMDConversationReadPatch struct {
	// UID identifies the user that owns the CMD conversation state.
	UID string
	// ChannelID identifies the durable command channel, e.g. source____cmd.
	ChannelID string
	// ChannelType identifies the command channel kind.
	ChannelType int64
	// ReadSeq is the candidate acknowledged command-channel sequence.
	ReadSeq uint64
	// UpdatedAt records when the read cursor advance was requested.
	UpdatedAt int64
}

func (s *ShardStore) GetCMDConversationState(ctx context.Context, uid, channelID string, channelType int64) (CMDConversationState, error) {
	if err := s.validate(); err != nil {
		return CMDConversationState{}, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return CMDConversationState{}, err
	}
	if err := validateConversationUID(uid); err != nil {
		return CMDConversationState{}, err
	}
	if err := validateConversationKey(ConversationKey{ChannelID: channelID, ChannelType: channelType}); err != nil {
		return CMDConversationState{}, err
	}

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()

	return s.getCMDConversationStateLocked(uid, channelID, channelType)
}

func (s *ShardStore) getCMDConversationStateLocked(uid, channelID string, channelType int64) (CMDConversationState, error) {
	key := encodeCMDConversationStatePrimaryKey(s.slot, uid, channelType, channelID, cmdConversationStatePrimaryFamilyID)
	value, err := s.db.getValue(key)
	if err != nil {
		return CMDConversationState{}, err
	}

	state, err := decodeCMDConversationStateFamilyValue(key, value)
	if err != nil {
		return CMDConversationState{}, err
	}
	state.UID = uid
	state.ChannelID = channelID
	state.ChannelType = channelType
	return state, nil
}

func (s *ShardStore) UpsertCMDConversationState(ctx context.Context, state CMDConversationState) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if err := validateCMDConversationState(state); err != nil {
		return err
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	existing, err := s.getCMDConversationStateLocked(state.UID, state.ChannelID, state.ChannelType)
	switch {
	case err == nil:
		state = mergeCMDConversationState(existing, state)
	case err == ErrNotFound:
		s.db.runAfterExistenceCheckHook()
		if err := s.db.checkContext(ctx); err != nil {
			return err
		}
	default:
		return err
	}

	primaryKey := encodeCMDConversationStatePrimaryKey(s.slot, state.UID, state.ChannelType, state.ChannelID, cmdConversationStatePrimaryFamilyID)
	value := encodeCMDConversationStateFamilyValue(state, primaryKey)

	batch := s.db.db.NewBatch()
	defer batch.Close()

	if err == nil && existing.ActiveAt > 0 && existing.ActiveAt != state.ActiveAt {
		oldIndexKey := encodeCMDConversationActiveIndexKey(s.slot, state.UID, existing.ActiveAt, state.ChannelType, state.ChannelID)
		if err := batch.Delete(oldIndexKey, nil); err != nil {
			return err
		}
	}
	if err := batch.Set(primaryKey, value, nil); err != nil {
		return err
	}
	if state.ActiveAt > 0 {
		indexKey := encodeCMDConversationActiveIndexKey(s.slot, state.UID, state.ActiveAt, state.ChannelType, state.ChannelID)
		if err := batch.Set(indexKey, nil, nil); err != nil {
			return err
		}
	}
	return batch.Commit(pebble.Sync)
}

func (s *ShardStore) AdvanceCMDConversationReadSeq(ctx context.Context, patch CMDConversationReadPatch) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if err := validateCMDConversationReadPatch(patch); err != nil {
		return err
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	current, err := s.getCMDConversationStateLocked(patch.UID, patch.ChannelID, patch.ChannelType)
	switch {
	case err == nil:
	case err == ErrNotFound:
		return nil
	default:
		return err
	}
	if patch.ReadSeq <= current.ReadSeq {
		return nil
	}

	current.ReadSeq = patch.ReadSeq
	if patch.UpdatedAt > current.UpdatedAt {
		current.UpdatedAt = patch.UpdatedAt
	}
	primaryKey := encodeCMDConversationStatePrimaryKey(s.slot, patch.UID, patch.ChannelType, patch.ChannelID, cmdConversationStatePrimaryFamilyID)
	value := encodeCMDConversationStateFamilyValue(current, primaryKey)

	batch := s.db.db.NewBatch()
	defer batch.Close()
	if err := batch.Set(primaryKey, value, nil); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (s *ShardStore) ListCMDConversationActive(ctx context.Context, uid string, limit int) ([]CMDConversationState, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return nil, err
	}
	if err := validateConversationUID(uid); err != nil {
		return nil, err
	}
	if err := validateConversationLimit(limit); err != nil {
		return nil, err
	}

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()

	prefix := encodeCMDConversationActiveIndexPrefix(s.slot, uid)
	iter, err := s.db.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: nextPrefix(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	states := make([]CMDConversationState, 0, limit)
	for ok := iter.First(); ok && len(states) < limit; ok = iter.Next() {
		if err := s.db.checkContext(ctx); err != nil {
			return nil, err
		}

		key := iter.Key()
		if !bytes.HasPrefix(key, prefix) {
			break
		}
		activeAt, conversationKey, err := decodeCMDConversationActiveIndexKey(key, prefix)
		if err != nil {
			return nil, err
		}
		state, err := s.getCMDConversationStateLocked(uid, conversationKey.ChannelID, conversationKey.ChannelType)
		if err != nil {
			if err == ErrNotFound {
				continue
			}
			return nil, err
		}
		if state.ActiveAt <= 0 || state.ActiveAt != activeAt {
			continue
		}
		states = append(states, state)
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return states, nil
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

func encodeCMDConversationStatePrimaryPrefix(hashSlot uint16, uid string) []byte {
	key := encodeStatePrefix(hashSlot, CMDConversationStateTable.ID)
	key = appendKeyString(key, uid)
	return key
}

func encodeCMDConversationStatePrimaryKey(hashSlot uint16, uid string, channelType int64, channelID string, familyID uint16) []byte {
	key := encodeCMDConversationStatePrimaryPrefix(hashSlot, uid)
	key = appendKeyInt64Ordered(key, channelType)
	key = appendKeyString(key, channelID)
	key = binary.AppendUvarint(key, uint64(familyID))
	return key
}

func encodeCMDConversationActiveIndexPrefix(hashSlot uint16, uid string) []byte {
	key := encodeIndexPrefix(hashSlot, CMDConversationStateTable.ID, cmdConversationStateActiveIndexID)
	key = appendKeyString(key, uid)
	return key
}

func encodeCMDConversationActiveIndexKey(hashSlot uint16, uid string, activeAt int64, channelType int64, channelID string) []byte {
	key := encodeCMDConversationActiveIndexPrefix(hashSlot, uid)
	key = appendKeyInt64OrderedDesc(key, activeAt)
	key = appendKeyInt64Ordered(key, channelType)
	key = appendKeyString(key, channelID)
	return key
}

func decodeCMDConversationActiveIndexKey(key, prefix []byte) (int64, ConversationKey, error) {
	rest := key[len(prefix):]
	activeAt, rest, err := decodeOrderedInt64Desc(rest)
	if err != nil {
		return 0, ConversationKey{}, err
	}
	channelType, rest, err := decodeOrderedInt64(rest)
	if err != nil {
		return 0, ConversationKey{}, err
	}
	channelID, rest, err := decodeKeyString(rest)
	if err != nil {
		return 0, ConversationKey{}, err
	}
	if len(rest) != 0 {
		return 0, ConversationKey{}, fmt.Errorf("%w: malformed cmd conversation active index key", ErrCorruptValue)
	}
	return activeAt, ConversationKey{ChannelID: channelID, ChannelType: channelType}, nil
}

func encodeCMDConversationStateFamilyValue(state CMDConversationState, key []byte) []byte {
	payload := make([]byte, 0, 64)
	payload = appendUint64Value(payload, cmdConversationStateColumnIDReadSeq, 0, state.ReadSeq)
	payload = appendUint64Value(payload, cmdConversationStateColumnIDDeletedToSeq, cmdConversationStateColumnIDReadSeq, state.DeletedToSeq)
	payload = appendIntValue(payload, cmdConversationStateColumnIDActiveAt, cmdConversationStateColumnIDDeletedToSeq, state.ActiveAt)
	payload = appendIntValue(payload, cmdConversationStateColumnIDUpdatedAt, cmdConversationStateColumnIDActiveAt, state.UpdatedAt)
	return wrapFamilyValue(key, payload)
}

func decodeCMDConversationStateFamilyValue(key, value []byte) (CMDConversationState, error) {
	_, payload, err := decodeWrappedValue(key, value)
	if err != nil {
		return CMDConversationState{}, err
	}

	var (
		state            CMDConversationState
		colID            uint16
		haveReadSeq      bool
		haveDeletedToSeq bool
		haveActiveAt     bool
		haveUpdatedAt    bool
	)

	for len(payload) > 0 {
		tag := payload[0]
		payload = payload[1:]

		delta := uint16(tag >> 4)
		valueType := tag & 0x0f
		if delta == 0 {
			return CMDConversationState{}, fmt.Errorf("%w: zero column delta", ErrCorruptValue)
		}
		colID += delta

		switch valueType {
		case valueTypeUint:
			raw, n := binary.Uvarint(payload)
			if n <= 0 {
				return CMDConversationState{}, fmt.Errorf("metadb: invalid uint payload")
			}
			payload = payload[n:]

			switch colID {
			case cmdConversationStateColumnIDReadSeq:
				state.ReadSeq = raw
				haveReadSeq = true
			case cmdConversationStateColumnIDDeletedToSeq:
				state.DeletedToSeq = raw
				haveDeletedToSeq = true
			default:
				return CMDConversationState{}, fmt.Errorf("%w: invalid uint column %d", ErrCorruptValue, colID)
			}
		case valueTypeInt:
			raw, n := binary.Uvarint(payload)
			if n <= 0 {
				return CMDConversationState{}, fmt.Errorf("metadb: invalid int payload")
			}
			payload = payload[n:]

			switch colID {
			case cmdConversationStateColumnIDActiveAt:
				state.ActiveAt = decodeZigZagInt64(raw)
				haveActiveAt = true
			case cmdConversationStateColumnIDUpdatedAt:
				state.UpdatedAt = decodeZigZagInt64(raw)
				haveUpdatedAt = true
			default:
				return CMDConversationState{}, fmt.Errorf("%w: invalid int column %d", ErrCorruptValue, colID)
			}
		default:
			return CMDConversationState{}, fmt.Errorf("metadb: unsupported value type %d", valueType)
		}
	}

	if !haveReadSeq {
		return CMDConversationState{}, fmt.Errorf("%w: missing uint column %d", ErrCorruptValue, cmdConversationStateColumnIDReadSeq)
	}
	if !haveDeletedToSeq {
		return CMDConversationState{}, fmt.Errorf("%w: missing uint column %d", ErrCorruptValue, cmdConversationStateColumnIDDeletedToSeq)
	}
	if !haveActiveAt {
		return CMDConversationState{}, fmt.Errorf("%w: missing int column %d", ErrCorruptValue, cmdConversationStateColumnIDActiveAt)
	}
	if !haveUpdatedAt {
		return CMDConversationState{}, fmt.Errorf("%w: missing int column %d", ErrCorruptValue, cmdConversationStateColumnIDUpdatedAt)
	}
	return state, nil
}
