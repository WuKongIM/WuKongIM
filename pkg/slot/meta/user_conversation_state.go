package meta

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/cockroachdb/pebble/v2"
)

type UserConversationState struct {
	UID          string
	ChannelID    string
	ChannelType  int64
	ReadSeq      uint64
	DeletedToSeq uint64
	ActiveAt     int64
	UpdatedAt    int64
}

type ConversationKey struct {
	ChannelID   string
	ChannelType int64
}

type ConversationCursor struct {
	ChannelID   string
	ChannelType int64
}

type UserConversationActivePatch struct {
	UID         string
	ChannelID   string
	ChannelType int64
	ActiveAt    int64
}

func (s *ShardStore) GetUserConversationState(ctx context.Context, uid, channelID string, channelType int64) (UserConversationState, error) {
	if err := s.validate(); err != nil {
		return UserConversationState{}, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return UserConversationState{}, err
	}
	if err := validateConversationUID(uid); err != nil {
		return UserConversationState{}, err
	}
	if err := validateConversationKey(ConversationKey{ChannelID: channelID, ChannelType: channelType}); err != nil {
		return UserConversationState{}, err
	}

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()

	return s.getUserConversationStateLocked(uid, channelID, channelType)
}

func (s *ShardStore) getUserConversationStateLocked(uid, channelID string, channelType int64) (UserConversationState, error) {
	key := encodeUserConversationStatePrimaryKey(s.slot, uid, channelType, channelID, userConversationStatePrimaryFamilyID)
	value, err := s.db.getValue(key)
	if err != nil {
		return UserConversationState{}, err
	}

	state, err := decodeUserConversationStateFamilyValue(key, value)
	if err != nil {
		return UserConversationState{}, err
	}
	state.UID = uid
	state.ChannelID = channelID
	state.ChannelType = channelType
	return state, nil
}

func (s *ShardStore) UpsertUserConversationState(ctx context.Context, state UserConversationState) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if err := validateUserConversationState(state); err != nil {
		return err
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	existing, err := s.getUserConversationStateLocked(state.UID, state.ChannelID, state.ChannelType)
	switch {
	case err == nil:
		if state.ActiveAt < existing.ActiveAt {
			state.ActiveAt = existing.ActiveAt
		}
	case err == ErrNotFound:
		s.db.runAfterExistenceCheckHook()
		if err := s.db.checkContext(ctx); err != nil {
			return err
		}
	default:
		return err
	}

	primaryKey := encodeUserConversationStatePrimaryKey(s.slot, state.UID, state.ChannelType, state.ChannelID, userConversationStatePrimaryFamilyID)
	value := encodeUserConversationStateFamilyValue(state, primaryKey)

	batch := s.db.db.NewBatch()
	defer batch.Close()

	if err == nil && existing.ActiveAt > 0 && existing.ActiveAt != state.ActiveAt {
		oldIndexKey := encodeUserConversationActiveIndexKey(s.slot, state.UID, existing.ActiveAt, state.ChannelType, state.ChannelID)
		if err := batch.Delete(oldIndexKey, nil); err != nil {
			return err
		}
	}
	if err := batch.Set(primaryKey, value, nil); err != nil {
		return err
	}
	if state.ActiveAt > 0 {
		indexKey := encodeUserConversationActiveIndexKey(s.slot, state.UID, state.ActiveAt, state.ChannelType, state.ChannelID)
		if err := batch.Set(indexKey, nil, nil); err != nil {
			return err
		}
	}
	return batch.Commit(pebble.Sync)
}

func (s *ShardStore) TouchUserConversationActiveAt(ctx context.Context, uid, channelID string, channelType int64, activeAt int64) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if err := validateConversationUID(uid); err != nil {
		return err
	}
	if err := validateConversationKey(ConversationKey{ChannelID: channelID, ChannelType: channelType}); err != nil {
		return err
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	current, err := s.getUserConversationStateLocked(uid, channelID, channelType)
	switch {
	case err == nil:
	case err == ErrNotFound:
		current = UserConversationState{
			UID:         uid,
			ChannelID:   channelID,
			ChannelType: channelType,
		}
		s.db.runAfterExistenceCheckHook()
		if err := s.db.checkContext(ctx); err != nil {
			return err
		}
	default:
		return err
	}

	if activeAt <= current.ActiveAt {
		return nil
	}

	updated := current
	updated.ActiveAt = activeAt

	primaryKey := encodeUserConversationStatePrimaryKey(s.slot, uid, channelType, channelID, userConversationStatePrimaryFamilyID)
	value := encodeUserConversationStateFamilyValue(updated, primaryKey)

	batch := s.db.db.NewBatch()
	defer batch.Close()

	if current.ActiveAt > 0 {
		oldIndexKey := encodeUserConversationActiveIndexKey(s.slot, uid, current.ActiveAt, channelType, channelID)
		if err := batch.Delete(oldIndexKey, nil); err != nil {
			return err
		}
	}
	if err := batch.Set(primaryKey, value, nil); err != nil {
		return err
	}
	if err := batch.Set(encodeUserConversationActiveIndexKey(s.slot, uid, activeAt, channelType, channelID), nil, nil); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (s *ShardStore) ClearUserConversationActiveAt(ctx context.Context, uid string, keys []ConversationKey) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if err := validateConversationUID(uid); err != nil {
		return err
	}

	normalized, err := normalizeConversationKeys(keys)
	if err != nil {
		return err
	}
	if len(normalized) == 0 {
		return nil
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	batch := s.db.db.NewBatch()
	defer batch.Close()

	for _, key := range normalized {
		state, err := s.getUserConversationStateLocked(uid, key.ChannelID, key.ChannelType)
		switch {
		case err == nil:
		case err == ErrNotFound:
			continue
		default:
			return err
		}
		if state.ActiveAt <= 0 {
			continue
		}

		oldIndexKey := encodeUserConversationActiveIndexKey(s.slot, uid, state.ActiveAt, key.ChannelType, key.ChannelID)
		if err := batch.Delete(oldIndexKey, nil); err != nil {
			return err
		}
		state.ActiveAt = 0
		primaryKey := encodeUserConversationStatePrimaryKey(s.slot, uid, key.ChannelType, key.ChannelID, userConversationStatePrimaryFamilyID)
		if err := batch.Set(primaryKey, encodeUserConversationStateFamilyValue(state, primaryKey), nil); err != nil {
			return err
		}
	}
	return batch.Commit(pebble.Sync)
}

func (s *ShardStore) ListUserConversationActive(ctx context.Context, uid string, limit int) ([]UserConversationState, error) {
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

	prefix := encodeUserConversationActiveIndexPrefix(s.slot, uid)
	iter, err := s.db.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: nextPrefix(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	states := make([]UserConversationState, 0, limit)
	for ok := iter.First(); ok && len(states) < limit; ok = iter.Next() {
		if err := s.db.checkContext(ctx); err != nil {
			return nil, err
		}

		key := iter.Key()
		if !bytes.HasPrefix(key, prefix) {
			break
		}
		activeAt, conversationKey, err := decodeUserConversationActiveIndexKey(key, prefix)
		if err != nil {
			return nil, err
		}
		state, err := s.getUserConversationStateLocked(uid, conversationKey.ChannelID, conversationKey.ChannelType)
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

func (s *ShardStore) ListUserConversationStatePage(ctx context.Context, uid string, after ConversationCursor, limit int) ([]UserConversationState, ConversationCursor, bool, error) {
	if err := s.validate(); err != nil {
		return nil, ConversationCursor{}, false, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return nil, ConversationCursor{}, false, err
	}
	if err := validateConversationUID(uid); err != nil {
		return nil, ConversationCursor{}, false, err
	}
	if err := validateConversationCursor(after); err != nil {
		return nil, ConversationCursor{}, false, err
	}
	if err := validateConversationLimit(limit); err != nil {
		return nil, ConversationCursor{}, false, err
	}

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()

	prefix := encodeUserConversationStatePrimaryPrefix(s.slot, uid)
	lowerBound := prefix
	if after != (ConversationCursor{}) {
		lowerBound = nextPrefix(encodeUserConversationStatePrimaryKey(s.slot, uid, after.ChannelType, after.ChannelID, userConversationStatePrimaryFamilyID))
	}

	iter, err := s.db.db.NewIter(&pebble.IterOptions{
		LowerBound: lowerBound,
		UpperBound: nextPrefix(prefix),
	})
	if err != nil {
		return nil, ConversationCursor{}, false, err
	}
	defer iter.Close()

	states := make([]UserConversationState, 0, limit+1)
	for ok := iter.SeekGE(lowerBound); ok; ok = iter.Next() {
		if err := s.db.checkContext(ctx); err != nil {
			return nil, ConversationCursor{}, false, err
		}

		key := iter.Key()
		if !bytes.HasPrefix(key, prefix) {
			break
		}
		value, err := iter.ValueAndErr()
		if err != nil {
			return nil, ConversationCursor{}, false, err
		}
		state, err := decodeUserConversationStateRecord(key, value, prefix)
		if err != nil {
			return nil, ConversationCursor{}, false, err
		}
		state.UID = uid
		states = append(states, state)
		if len(states) > limit {
			return states[:limit], stateToCursor(states[limit-1]), false, nil
		}
	}
	if err := iter.Error(); err != nil {
		return nil, ConversationCursor{}, false, err
	}

	cursor := after
	if len(states) > 0 {
		cursor = stateToCursor(states[len(states)-1])
	}
	return states, cursor, true, nil
}

func validateUserConversationState(state UserConversationState) error {
	if err := validateConversationUID(state.UID); err != nil {
		return err
	}
	if err := validateConversationKey(ConversationKey{ChannelID: state.ChannelID, ChannelType: state.ChannelType}); err != nil {
		return err
	}
	return nil
}

func validateConversationUID(uid string) error {
	if uid == "" || len(uid) > maxKeyStringLen {
		return ErrInvalidArgument
	}
	return nil
}

func validateConversationKey(key ConversationKey) error {
	if key.ChannelID == "" || len(key.ChannelID) > maxKeyStringLen {
		return ErrInvalidArgument
	}
	return nil
}

func validateConversationCursor(cursor ConversationCursor) error {
	if cursor == (ConversationCursor{}) {
		return nil
	}
	return validateConversationKey(ConversationKey{
		ChannelID:   cursor.ChannelID,
		ChannelType: cursor.ChannelType,
	})
}

func validateConversationLimit(limit int) error {
	if limit <= 0 {
		return ErrInvalidArgument
	}
	return nil
}

func normalizeConversationKeys(keys []ConversationKey) ([]ConversationKey, error) {
	if len(keys) == 0 {
		return nil, nil
	}

	seen := make(map[ConversationKey]struct{}, len(keys))
	normalized := make([]ConversationKey, 0, len(keys))
	for _, key := range keys {
		if err := validateConversationKey(key); err != nil {
			return nil, err
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, key)
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].ChannelType != normalized[j].ChannelType {
			return normalized[i].ChannelType < normalized[j].ChannelType
		}
		return encodedConversationStringLess(normalized[i].ChannelID, normalized[j].ChannelID)
	})
	return normalized, nil
}

func encodedConversationStringLess(left, right string) bool {
	if len(left) != len(right) {
		return len(left) < len(right)
	}
	return left < right
}

func stateToCursor(state UserConversationState) ConversationCursor {
	return ConversationCursor{
		ChannelID:   state.ChannelID,
		ChannelType: state.ChannelType,
	}
}

func encodeUserConversationStatePrimaryPrefix(hashSlot uint16, uid string) []byte {
	key := make([]byte, 0, 48)
	key = encodeStatePrefix(hashSlot, UserConversationStateTable.ID)
	key = appendKeyString(key, uid)
	return key
}

func encodeUserConversationStatePrimaryKey(hashSlot uint16, uid string, channelType int64, channelID string, familyID uint16) []byte {
	key := encodeUserConversationStatePrimaryPrefix(hashSlot, uid)
	key = appendKeyInt64Ordered(key, channelType)
	key = appendKeyString(key, channelID)
	key = binary.AppendUvarint(key, uint64(familyID))
	return key
}

func decodeUserConversationStateRecord(key, value, prefix []byte) (UserConversationState, error) {
	rest := key[len(prefix):]
	channelType, rest, err := decodeOrderedInt64(rest)
	if err != nil {
		return UserConversationState{}, err
	}
	channelID, rest, err := decodeKeyString(rest)
	if err != nil {
		return UserConversationState{}, err
	}
	familyID, n := binary.Uvarint(rest)
	if n <= 0 {
		return UserConversationState{}, ErrCorruptValue
	}
	if familyID != uint64(userConversationStatePrimaryFamilyID) {
		return UserConversationState{}, fmt.Errorf("%w: invalid conversation state family %d", ErrCorruptValue, familyID)
	}
	if len(rest[n:]) != 0 {
		return UserConversationState{}, ErrCorruptValue
	}

	state, err := decodeUserConversationStateFamilyValue(key, value)
	if err != nil {
		return UserConversationState{}, err
	}
	state.ChannelID = channelID
	state.ChannelType = channelType
	return state, nil
}

func encodeUserConversationActiveIndexPrefix(hashSlot uint16, uid string) []byte {
	key := make([]byte, 0, 48)
	key = encodeIndexPrefix(hashSlot, UserConversationStateTable.ID, userConversationStateActiveIndexID)
	key = appendKeyString(key, uid)
	return key
}

func encodeUserConversationActiveIndexKey(hashSlot uint16, uid string, activeAt int64, channelType int64, channelID string) []byte {
	key := encodeUserConversationActiveIndexPrefix(hashSlot, uid)
	key = appendKeyInt64OrderedDesc(key, activeAt)
	key = appendKeyInt64Ordered(key, channelType)
	key = appendKeyString(key, channelID)
	return key
}

func decodeUserConversationActiveIndexKey(key, prefix []byte) (int64, ConversationKey, error) {
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
		return 0, ConversationKey{}, fmt.Errorf("%w: malformed conversation active index key", ErrCorruptValue)
	}
	return activeAt, ConversationKey{ChannelID: channelID, ChannelType: channelType}, nil
}

func encodeUserConversationStateFamilyValue(state UserConversationState, key []byte) []byte {
	payload := make([]byte, 0, 64)
	payload = appendUint64Value(payload, userConversationStateColumnIDReadSeq, 0, state.ReadSeq)
	payload = appendUint64Value(payload, userConversationStateColumnIDDeletedToSeq, userConversationStateColumnIDReadSeq, state.DeletedToSeq)
	payload = appendIntValue(payload, userConversationStateColumnIDActiveAt, userConversationStateColumnIDDeletedToSeq, state.ActiveAt)
	payload = appendIntValue(payload, userConversationStateColumnIDUpdatedAt, userConversationStateColumnIDActiveAt, state.UpdatedAt)
	return wrapFamilyValue(key, payload)
}

func decodeUserConversationStateFamilyValue(key, value []byte) (UserConversationState, error) {
	_, payload, err := decodeWrappedValue(key, value)
	if err != nil {
		return UserConversationState{}, err
	}

	var (
		state            UserConversationState
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
			return UserConversationState{}, fmt.Errorf("%w: zero column delta", ErrCorruptValue)
		}
		colID += delta

		switch valueType {
		case valueTypeUint:
			raw, n := binary.Uvarint(payload)
			if n <= 0 {
				return UserConversationState{}, fmt.Errorf("metadb: invalid uint payload")
			}
			payload = payload[n:]

			switch colID {
			case userConversationStateColumnIDReadSeq:
				state.ReadSeq = raw
				haveReadSeq = true
			case userConversationStateColumnIDDeletedToSeq:
				state.DeletedToSeq = raw
				haveDeletedToSeq = true
			default:
				return UserConversationState{}, fmt.Errorf("%w: invalid uint column %d", ErrCorruptValue, colID)
			}
		case valueTypeInt:
			raw, n := binary.Uvarint(payload)
			if n <= 0 {
				return UserConversationState{}, fmt.Errorf("metadb: invalid int payload")
			}
			payload = payload[n:]

			switch colID {
			case userConversationStateColumnIDActiveAt:
				state.ActiveAt = decodeZigZagInt64(raw)
				haveActiveAt = true
			case userConversationStateColumnIDUpdatedAt:
				state.UpdatedAt = decodeZigZagInt64(raw)
				haveUpdatedAt = true
			default:
				return UserConversationState{}, fmt.Errorf("%w: invalid int column %d", ErrCorruptValue, colID)
			}
		default:
			return UserConversationState{}, fmt.Errorf("metadb: unsupported value type %d", valueType)
		}
	}

	if !haveReadSeq {
		return UserConversationState{}, fmt.Errorf("%w: missing uint column %d", ErrCorruptValue, userConversationStateColumnIDReadSeq)
	}
	if !haveDeletedToSeq {
		return UserConversationState{}, fmt.Errorf("%w: missing uint column %d", ErrCorruptValue, userConversationStateColumnIDDeletedToSeq)
	}
	if !haveActiveAt {
		return UserConversationState{}, fmt.Errorf("%w: missing int column %d", ErrCorruptValue, userConversationStateColumnIDActiveAt)
	}
	if !haveUpdatedAt {
		return UserConversationState{}, fmt.Errorf("%w: missing int column %d", ErrCorruptValue, userConversationStateColumnIDUpdatedAt)
	}
	return state, nil
}

func appendKeyInt64OrderedDesc(dst []byte, value int64) []byte {
	u := ^(uint64(value) ^ 0x8000000000000000)
	return binary.BigEndian.AppendUint64(dst, u)
}

func decodeOrderedInt64Desc(src []byte) (int64, []byte, error) {
	if len(src) < 8 {
		return 0, nil, fmt.Errorf("metadb: ordered int64 too short")
	}
	u := ^binary.BigEndian.Uint64(src[:8])
	u ^= 0x8000000000000000
	return int64(u), src[8:], nil
}
