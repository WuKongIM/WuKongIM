package meta

import (
	"bytes"
	"context"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// AddSubscribers adds sorted unique subscribers and advances channel mutation version.
func (s *Shard) AddSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, mutationVersion uint64) error {
	return s.mutateSubscribers(ctx, channelID, channelType, uids, mutationVersion, true)
}

// RemoveSubscribers removes subscribers and advances channel mutation version.
func (s *Shard) RemoveSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, mutationVersion uint64) error {
	return s.mutateSubscribers(ctx, channelID, channelType, uids, mutationVersion, false)
}

// ContainsSubscriber reports whether uid belongs to a channel.
func (s *Shard) ContainsSubscriber(ctx context.Context, channelID string, channelType int64, uid string) (bool, error) {
	if err := s.check(ctx); err != nil {
		return false, err
	}
	if err := validateKeyString(channelID); err != nil {
		return false, err
	}
	if err := validateKeyString(uid); err != nil {
		return false, err
	}
	_, ok, err := s.db.get(encodeSubscriberRowKey(s.hashSlot, channelID, channelType, uid, subscriberPrimaryFamilyID))
	return ok, err
}

// HasSubscribers reports whether a channel has at least one subscriber.
func (s *Shard) HasSubscribers(ctx context.Context, channelID string, channelType int64) (bool, error) {
	list, _, _, err := s.ListSubscribersPage(ctx, channelID, channelType, "", 1)
	if err != nil {
		return false, err
	}
	return len(list) > 0, nil
}

// SnapshotSubscribers returns all subscribers in stable UID order.
func (s *Shard) SnapshotSubscribers(ctx context.Context, channelID string, channelType int64) ([]string, error) {
	var out []string
	cursor := ""
	for {
		page, next, done, err := s.ListSubscribersPage(ctx, channelID, channelType, cursor, 256)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if done {
			return out, nil
		}
		cursor = next
	}
}

// ListSubscribersPage returns subscribers after cursorUID in stable UID order.
func (s *Shard) ListSubscribersPage(ctx context.Context, channelID string, channelType int64, cursorUID string, limit int) ([]string, string, bool, error) {
	if err := s.check(ctx); err != nil {
		return nil, "", false, err
	}
	if err := validateKeyString(channelID); err != nil {
		return nil, "", false, err
	}
	if limit <= 0 {
		return nil, "", true, nil
	}
	prefix := encodeSubscriberRowPrefix(s.hashSlot, channelID, channelType)
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := s.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, "", false, err
	}
	defer iter.Close()
	uids := make([]string, 0, limit)
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return nil, "", false, err
		}
		uid, ok := decodeSubscriberUID(prefix, iter.Key())
		if !ok || uid <= cursorUID {
			continue
		}
		uids = append(uids, uid)
		if len(uids) == limit {
			return uids, uid, false, nil
		}
	}
	if err := iter.Error(); err != nil {
		return nil, "", false, err
	}
	return uids, "", true, nil
}

func (s *Shard) mutateSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, mutationVersion uint64, add bool) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateKeyString(channelID); err != nil {
		return err
	}
	normalized, err := normalizeSubscriberUIDs(uids)
	if err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()

	primaryKey := encodeChannelRowKey(s.hashSlot, channelID, channelType, channelPrimaryFamilyID)
	value, ok, err := s.db.get(primaryKey)
	if err != nil {
		return err
	}
	if !ok {
		return dberrors.ErrNotFound
	}
	channel, err := decodeChannelValue(channelID, channelType, value)
	if err != nil {
		return err
	}
	if mutationVersion > channel.SubscriberMutationVersion {
		channel.SubscriberMutationVersion = mutationVersion
	}

	batch := s.db.engine.NewBatch()
	defer batch.Close()
	for _, uid := range normalized {
		key := encodeSubscriberRowKey(s.hashSlot, channelID, channelType, uid, subscriberPrimaryFamilyID)
		if add {
			if err := batch.Set(key, nil); err != nil {
				return err
			}
		} else if err := batch.Delete(key); err != nil {
			return err
		}
	}
	if err := s.stageChannel(batch, primaryKey, channel); err != nil {
		return err
	}
	if err := batch.Commit(true); err != nil {
		return err
	}
	s.db.forgetChannel(primaryKey)
	return nil
}

func normalizeSubscriberUIDs(uids []string) ([]string, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(uids))
	out := make([]string, 0, len(uids))
	for _, uid := range uids {
		if err := validateKeyString(uid); err != nil {
			return nil, err
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		out = append(out, uid)
	}
	sort.Strings(out)
	return out, nil
}

func decodeSubscriberUID(prefix []byte, key []byte) (string, bool) {
	if !bytes.HasPrefix(key, prefix) {
		return "", false
	}
	uid, rest, err := keycodec.ReadString(key[len(prefix):])
	if err != nil || len(rest) != 2 {
		return "", false
	}
	return uid, true
}
