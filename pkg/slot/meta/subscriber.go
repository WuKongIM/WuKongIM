package meta

import (
	"context"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/cockroachdb/pebble/v2"
)

type Subscriber struct {
	ChannelID   string
	ChannelType int64
	UID         string
}

func (s *ShardStore) AddSubscribers(ctx context.Context, channelID string, channelType int64, uids []string) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if err := validateSubscriberChannel(channelID); err != nil {
		return err
	}

	normalized, err := normalizeSubscriberUIDs(uids)
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

	for _, uid := range normalized {
		key := encodeSubscriberPrimaryKey(s.slot, channelID, channelType, uid, subscriberPrimaryFamilyID)
		if err := batch.Set(key, wrapFamilyValue(key, nil), nil); err != nil {
			return err
		}
	}
	return batch.Commit(pebble.Sync)
}

func (s *ShardStore) RemoveSubscribers(ctx context.Context, channelID string, channelType int64, uids []string) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if err := validateSubscriberChannel(channelID); err != nil {
		return err
	}

	normalized, err := normalizeSubscriberUIDs(uids)
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

	for _, uid := range normalized {
		key := encodeSubscriberPrimaryKey(s.slot, channelID, channelType, uid, subscriberPrimaryFamilyID)
		if err := batch.Delete(key, nil); err != nil {
			return err
		}
	}
	return batch.Commit(pebble.Sync)
}

func (s *ShardStore) ListSubscribersPage(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error) {
	if err := s.validate(); err != nil {
		return nil, "", false, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return nil, "", false, err
	}
	if err := validateSubscriberPageArgs(channelID, afterUID, limit); err != nil {
		return nil, "", false, err
	}

	channelPrefix := encodeSubscriberChannelPrefix(s.slot, channelID, channelType)
	lowerBound := channelPrefix
	if afterUID != "" {
		lowerBound = nextPrefix(encodeSubscriberPrimaryKey(s.slot, channelID, channelType, afterUID, subscriberPrimaryFamilyID))
	}

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()

	iter, err := s.db.db.NewIter(&pebble.IterOptions{
		LowerBound: lowerBound,
		UpperBound: nextPrefix(channelPrefix),
	})
	if err != nil {
		return nil, "", false, err
	}
	defer iter.Close()

	uids := make([]string, 0, limit+1)
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := s.db.checkContext(ctx); err != nil {
			return nil, "", false, err
		}

		uid, err := decodeSubscriberUIDFromKey(iter.Key(), channelPrefix)
		if err != nil {
			return nil, "", false, err
		}
		uids = append(uids, uid)
		if len(uids) > limit {
			cursor := uids[limit-1]
			return uids[:limit], cursor, false, nil
		}
	}
	if err := iter.Error(); err != nil {
		return nil, "", false, err
	}

	cursor := afterUID
	if len(uids) > 0 {
		cursor = uids[len(uids)-1]
	}
	return uids, cursor, true, nil
}

func (s *ShardStore) ListSubscribersSnapshot(ctx context.Context, channelID string, channelType int64) ([]string, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return nil, err
	}
	if err := validateSubscriberChannel(channelID); err != nil {
		return nil, err
	}

	channelPrefix := encodeSubscriberChannelPrefix(s.slot, channelID, channelType)

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()

	iter, err := s.db.db.NewIter(&pebble.IterOptions{
		LowerBound: channelPrefix,
		UpperBound: nextPrefix(channelPrefix),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	uids := make([]string, 0, 16)
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := s.db.checkContext(ctx); err != nil {
			return nil, err
		}
		uid, err := decodeSubscriberUIDFromKey(iter.Key(), channelPrefix)
		if err != nil {
			return nil, err
		}
		uids = append(uids, uid)
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return uids, nil
}

func encodeSubscriberChannelPrefix(hashSlot uint16, channelID string, channelType int64) []byte {
	key := make([]byte, 0, 56)
	key = encodeStatePrefix(hashSlot, SubscriberTable.ID)
	key = appendKeyString(key, channelID)
	key = appendKeyInt64Ordered(key, channelType)
	return key
}

func encodeSubscriberPrimaryKey(hashSlot uint16, channelID string, channelType int64, uid string, familyID uint16) []byte {
	key := encodeSubscriberChannelPrefix(hashSlot, channelID, channelType)
	key = appendKeyString(key, uid)
	key = binary.AppendUvarint(key, uint64(familyID))
	return key
}

func decodeSubscriberUIDFromKey(key, channelPrefix []byte) (string, error) {
	uid, rest, err := decodeKeyString(key[len(channelPrefix):])
	if err != nil {
		return "", err
	}
	familyID, n := binary.Uvarint(rest)
	if n <= 0 {
		return "", ErrCorruptValue
	}
	if familyID != uint64(subscriberPrimaryFamilyID) {
		return "", fmt.Errorf("%w: invalid subscriber family %d", ErrCorruptValue, familyID)
	}
	if len(rest[n:]) != 0 {
		return "", ErrCorruptValue
	}
	return uid, nil
}

func normalizeSubscriberUIDs(uids []string) ([]string, error) {
	if len(uids) == 0 {
		return nil, nil
	}

	seen := make(map[string]struct{}, len(uids))
	normalized := make([]string, 0, len(uids))
	for _, uid := range uids {
		if err := validateSubscriberUID(uid); err != nil {
			return nil, err
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		normalized = append(normalized, uid)
	}
	sort.Slice(normalized, func(i, j int) bool {
		return encodedSubscriberUIDLess(normalized[i], normalized[j])
	})
	return normalized, nil
}

func encodedSubscriberUIDLess(left, right string) bool {
	if len(left) != len(right) {
		return len(left) < len(right)
	}
	return left < right
}

func validateSubscriberChannel(channelID string) error {
	if channelID == "" || len(channelID) > maxKeyStringLen {
		return ErrInvalidArgument
	}
	return nil
}

func validateSubscriberUID(uid string) error {
	if uid == "" || len(uid) > maxKeyStringLen {
		return ErrInvalidArgument
	}
	return nil
}

func validateSubscriberPageArgs(channelID, afterUID string, limit int) error {
	if err := validateSubscriberChannel(channelID); err != nil {
		return err
	}
	if afterUID != "" {
		if err := validateSubscriberUID(afterUID); err != nil {
			return err
		}
	}
	if limit <= 0 {
		return ErrInvalidArgument
	}
	return nil
}
