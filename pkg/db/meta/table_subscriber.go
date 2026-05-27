package meta

import (
	"context"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

const (
	subscriberColumnChannelID   uint16 = 1
	subscriberColumnChannelType uint16 = 2
	subscriberColumnUID         uint16 = 3
)

var subscriberTable = registerMetaTable(TableSpec[Subscriber]{
	ID:   TableIDSubscriber,
	Name: "subscriber",
	Columns: []schema.Column{
		{ID: subscriberColumnChannelID, Name: "channel_id", Type: schema.TypeString, Required: true},
		{ID: subscriberColumnChannelType, Name: "channel_type", Type: schema.TypeInt64, Required: true},
		{ID: subscriberColumnUID, Name: "uid", Type: schema.TypeString, Required: true},
	},
	Families: []schema.Family{{ID: subscriberPrimaryFamilyID, Name: "primary"}},
	Primary: PrimarySpec[Subscriber]{
		IndexID:  subscriberPrimaryIndexID,
		FamilyID: subscriberPrimaryFamilyID,
		Name:     "pk_subscriber",
		Columns:  []uint16{subscriberColumnChannelID, subscriberColumnChannelType, subscriberColumnUID},
		Layout:   KeyLayout{KeyString, KeyInt64Ordered, KeyString},
		Key: func(subscriber Subscriber) KeyParts {
			return subscriberPrimaryKey(subscriber.ChannelID, subscriber.ChannelType, subscriber.UID)
		},
	},
	Validate: validateSubscriber,
	EncodeValue: func(Subscriber) ([]byte, error) {
		return nil, nil
	},
	DecodeValue: func(primary KeyParts, value []byte) (Subscriber, error) {
		return Subscriber{ChannelID: primary[0].S, ChannelType: primary[1].I64, UID: primary[2].S}, nil
	},
})

// SubscriberTable describes the subscriber table schema.
var SubscriberTable = subscriberTable.Schema()

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
	_, ok, err := subscriberTable.Get(ctx, s, subscriberPrimaryKey(channelID, channelType, uid))
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
	var after KeyParts
	if cursorUID != "" {
		after = subscriberPrimaryKey(channelID, channelType, cursorUID)
	}
	rows, next, done, err := subscriberTable.scanPrimaryPrefixRows(ctx, s, KeyParts{String(channelID), Int64Ordered(channelType)}, after, limit)
	if err != nil {
		return nil, "", false, err
	}
	uids := make([]string, 0, len(rows))
	for _, row := range rows {
		uids = append(uids, row.UID)
	}
	if done || len(next) < 3 {
		return uids, "", done, nil
	}
	return uids, next[2].S, done, nil
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
		key, err := subscriberRowKey(s.hashSlot, channelID, channelType, uid)
		if err != nil {
			return err
		}
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

func subscriberPrimaryKey(channelID string, channelType int64, uid string) KeyParts {
	return KeyParts{String(channelID), Int64Ordered(channelType), String(uid)}
}

func subscriberRowKey(hashSlot HashSlot, channelID string, channelType int64, uid string) ([]byte, error) {
	return subscriberTable.primaryRowKey(hashSlot, subscriberPrimaryKey(channelID, channelType, uid))
}

func validateSubscriber(subscriber Subscriber) error {
	if err := validateKeyString(subscriber.ChannelID); err != nil {
		return err
	}
	return validateKeyString(subscriber.UID)
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
