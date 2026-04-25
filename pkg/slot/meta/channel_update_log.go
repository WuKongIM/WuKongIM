package meta

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/cockroachdb/pebble/v2"
)

type ChannelUpdateLog struct {
	ChannelID       string
	ChannelType     int64
	UpdatedAt       int64
	LastMsgSeq      uint64
	LastClientMsgNo string
	LastMsgAt       int64
}

func (s *ShardStore) UpsertChannelUpdateLog(ctx context.Context, entry ChannelUpdateLog) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if err := validateChannelUpdateLog(entry); err != nil {
		return err
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	key := encodeChannelUpdateLogPrimaryKey(s.slot, entry.ChannelID, entry.ChannelType, channelUpdateLogPrimaryFamilyID)
	value := encodeChannelUpdateLogFamilyValue(entry, key)

	batch := s.db.db.NewBatch()
	defer batch.Close()

	if err := batch.Set(key, value, nil); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (s *ShardStore) BatchGetChannelUpdateLogs(ctx context.Context, keys []ConversationKey) (map[ConversationKey]ChannelUpdateLog, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return nil, err
	}

	normalized, err := normalizeConversationKeys(keys)
	if err != nil {
		return nil, err
	}

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()

	entries := make(map[ConversationKey]ChannelUpdateLog, len(normalized))
	for _, key := range normalized {
		entry, err := s.getChannelUpdateLogLocked(key.ChannelID, key.ChannelType)
		if err != nil {
			if err == ErrNotFound {
				continue
			}
			return nil, err
		}
		entries[key] = entry
	}
	return entries, nil
}

func (s *ShardStore) DeleteChannelUpdateLogs(ctx context.Context, keys []ConversationKey) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
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
		primaryKey := encodeChannelUpdateLogPrimaryKey(s.slot, key.ChannelID, key.ChannelType, channelUpdateLogPrimaryFamilyID)
		if err := batch.Delete(primaryKey, nil); err != nil {
			return err
		}
	}
	return batch.Commit(pebble.Sync)
}

func (s *ShardStore) getChannelUpdateLogLocked(channelID string, channelType int64) (ChannelUpdateLog, error) {
	key := encodeChannelUpdateLogPrimaryKey(s.slot, channelID, channelType, channelUpdateLogPrimaryFamilyID)
	value, err := s.db.getValue(key)
	if err != nil {
		return ChannelUpdateLog{}, err
	}

	entry, err := decodeChannelUpdateLogFamilyValue(key, value)
	if err != nil {
		return ChannelUpdateLog{}, err
	}
	entry.ChannelID = channelID
	entry.ChannelType = channelType
	return entry, nil
}

func validateChannelUpdateLog(entry ChannelUpdateLog) error {
	if err := validateConversationKey(ConversationKey{ChannelID: entry.ChannelID, ChannelType: entry.ChannelType}); err != nil {
		return err
	}
	return nil
}

func encodeChannelUpdateLogPrimaryKey(hashSlot uint16, channelID string, channelType int64, familyID uint16) []byte {
	key := make([]byte, 0, 48)
	key = encodeStatePrefix(hashSlot, ChannelUpdateLogTable.ID)
	key = appendKeyInt64Ordered(key, channelType)
	key = appendKeyString(key, channelID)
	key = binary.AppendUvarint(key, uint64(familyID))
	return key
}

func encodeChannelUpdateLogFamilyValue(entry ChannelUpdateLog, key []byte) []byte {
	payload := make([]byte, 0, 64)
	payload = appendIntValue(payload, channelUpdateLogColumnIDUpdatedAt, 0, entry.UpdatedAt)
	payload = appendUint64Value(payload, channelUpdateLogColumnIDLastMsgSeq, channelUpdateLogColumnIDUpdatedAt, entry.LastMsgSeq)
	payload = appendBytesValue(payload, channelUpdateLogColumnIDLastClientMsgNo, channelUpdateLogColumnIDLastMsgSeq, entry.LastClientMsgNo)
	payload = appendIntValue(payload, channelUpdateLogColumnIDLastMsgAt, channelUpdateLogColumnIDLastClientMsgNo, entry.LastMsgAt)
	return wrapFamilyValue(key, payload)
}

func decodeChannelUpdateLogFamilyValue(key, value []byte) (ChannelUpdateLog, error) {
	_, payload, err := decodeWrappedValue(key, value)
	if err != nil {
		return ChannelUpdateLog{}, err
	}

	var (
		entry           ChannelUpdateLog
		colID           uint16
		haveUpdatedAt   bool
		haveLastMsgSeq  bool
		haveClientMsgNo bool
		haveLastMsgAt   bool
	)

	for len(payload) > 0 {
		tag := payload[0]
		payload = payload[1:]

		delta := uint16(tag >> 4)
		valueType := tag & 0x0f
		if delta == 0 {
			return ChannelUpdateLog{}, fmt.Errorf("%w: zero column delta", ErrCorruptValue)
		}
		colID += delta

		switch valueType {
		case valueTypeBytes:
			length, n := binary.Uvarint(payload)
			if n <= 0 {
				return ChannelUpdateLog{}, fmt.Errorf("metadb: invalid bytes length")
			}
			payload = payload[n:]
			if uint64(len(payload)) < length {
				return ChannelUpdateLog{}, fmt.Errorf("metadb: bytes payload truncated")
			}
			raw := payload[:length]
			payload = payload[length:]

			switch colID {
			case channelUpdateLogColumnIDLastClientMsgNo:
				entry.LastClientMsgNo = string(raw)
				haveClientMsgNo = true
			default:
				return ChannelUpdateLog{}, fmt.Errorf("%w: invalid bytes column %d", ErrCorruptValue, colID)
			}
		case valueTypeInt:
			raw, n := binary.Uvarint(payload)
			if n <= 0 {
				return ChannelUpdateLog{}, fmt.Errorf("metadb: invalid int payload")
			}
			payload = payload[n:]

			switch colID {
			case channelUpdateLogColumnIDUpdatedAt:
				entry.UpdatedAt = decodeZigZagInt64(raw)
				haveUpdatedAt = true
			case channelUpdateLogColumnIDLastMsgAt:
				entry.LastMsgAt = decodeZigZagInt64(raw)
				haveLastMsgAt = true
			default:
				return ChannelUpdateLog{}, fmt.Errorf("%w: invalid int column %d", ErrCorruptValue, colID)
			}
		case valueTypeUint:
			raw, n := binary.Uvarint(payload)
			if n <= 0 {
				return ChannelUpdateLog{}, fmt.Errorf("metadb: invalid uint payload")
			}
			payload = payload[n:]

			switch colID {
			case channelUpdateLogColumnIDLastMsgSeq:
				entry.LastMsgSeq = raw
				haveLastMsgSeq = true
			default:
				return ChannelUpdateLog{}, fmt.Errorf("%w: invalid uint column %d", ErrCorruptValue, colID)
			}
		default:
			return ChannelUpdateLog{}, fmt.Errorf("metadb: unsupported value type %d", valueType)
		}
	}

	if !haveUpdatedAt {
		return ChannelUpdateLog{}, fmt.Errorf("%w: missing int column %d", ErrCorruptValue, channelUpdateLogColumnIDUpdatedAt)
	}
	if !haveLastMsgSeq {
		return ChannelUpdateLog{}, fmt.Errorf("%w: missing uint column %d", ErrCorruptValue, channelUpdateLogColumnIDLastMsgSeq)
	}
	if !haveClientMsgNo {
		return ChannelUpdateLog{}, fmt.Errorf("%w: missing string column %d", ErrCorruptValue, channelUpdateLogColumnIDLastClientMsgNo)
	}
	if !haveLastMsgAt {
		return ChannelUpdateLog{}, fmt.Errorf("%w: missing int column %d", ErrCorruptValue, channelUpdateLogColumnIDLastMsgAt)
	}
	return entry, nil
}
