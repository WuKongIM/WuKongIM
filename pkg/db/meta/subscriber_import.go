package meta

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// ImportSubscriberSequence installs an offline allocation floor before exact
// member imports. It rejects regression, including on an otherwise empty Slot.
func (s *Shard) ImportSubscriberSequence(ctx context.Context, n uint64) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if n == 0 {
		return dberrors.ErrInvalidArgument
	}
	unlock := s.lock()
	defer unlock()
	current, err := s.SubscriberSequence(ctx)
	if err != nil {
		return err
	}
	if n < current {
		return dberrors.ErrConflict
	}
	if n == current {
		return nil
	}
	b := s.db.engine.NewBatch()
	defer b.Close()
	if _, err = stageSubscriberSequence(b, subscriberSequenceKey(s.hashSlot), n); err != nil {
		return err
	}
	return b.Commit(true)
}

// ImportSubscribers installs at most 4096 exact offline rows without assigning
// new authority. The caller must import their sequence witness first. Conflicting
// live members reject the entire batch; zero incarnation denotes legacy 1.
func (s *Shard) ImportSubscribers(ctx context.Context, channelID string, channelType int64, rows []Subscriber) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if validateKeyString(channelID) != nil || len(rows) > 4096 {
		return dberrors.ErrInvalidArgument
	}
	unique := make(map[string]Subscriber, len(rows))
	uids := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.ChannelID != channelID || r.ChannelType != channelType || validateSubscriber(r) != nil {
			return dberrors.ErrInvalidArgument
		}
		if r.Incarnation == 0 {
			r.Incarnation = 1
		}
		if old, ok := unique[r.UID]; ok && old != r {
			return dberrors.ErrInvalidArgument
		}
		unique[r.UID] = r
		uids = append(uids, r.UID)
	}
	uids, _ = normalizeSubscriberUIDs(uids)
	unlock := s.lock()
	defer unlock()
	sequence, err := s.SubscriberSequence(ctx)
	if err != nil {
		return err
	}
	chKey := encodeChannelRowKey(s.hashSlot, channelID, channelType, channelPrimaryFamilyID)
	value, exists, err := s.db.get(chKey)
	if err != nil {
		return err
	}
	if !exists {
		return dberrors.ErrNotFound
	}
	ch, err := decodeChannelValue(channelID, channelType, value)
	if err != nil {
		return err
	}
	b := s.db.engine.NewBatch()
	defer b.Close()
	for _, uid := range uids {
		r := unique[uid]
		if r.Incarnation > sequence {
			return dberrors.ErrConflict
		}
		old, exists, err := s.GetSubscriber(ctx, channelID, channelType, uid)
		if err != nil {
			return err
		}
		if exists {
			if old != r {
				return dberrors.ErrConflict
			}
			continue
		}
		key, err := subscriberRowKey(s.hashSlot, channelID, channelType, uid)
		if err != nil {
			return err
		}
		value, err := encodeSubscriberValue(key, r)
		if err != nil {
			return err
		}
		if err = b.Set(key, value); err != nil {
			return err
		}
		ch.SubscriberCount++
	}
	if err = s.stageChannel(b, chKey, ch); err != nil {
		return err
	}
	if err = b.Commit(true); err != nil {
		return err
	}
	s.db.forgetChannel(chKey)
	return nil
}
