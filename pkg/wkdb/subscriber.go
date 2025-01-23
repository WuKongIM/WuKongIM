package wkdb

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/cockroachdb/pebble"
	"go.uber.org/zap"
)

func (wk *wukongDB) AddSubscribers(channelId string, channelType uint8, subscribers []Member) error {

	wk.metrics.AddSubscribersAdd(1)

	db := wk.channelDb(channelId, channelType)

	channelPrimaryId, err := wk.getChannelPrimaryKey(channelId, channelType)
	if err != nil {
		return err
	}
	if channelPrimaryId == 0 {
		return fmt.Errorf("AddSubscribers: channelId: %s channelType: %d not found", channelId, channelType)
	}

	w := db.NewBatch()
	defer w.Close()

	for _, subscriber := range subscribers {
		id := key.HashWithString(subscriber.Uid)
		subscriber.Id = id
		if err := wk.writeSubscriber(channelId, channelType, subscriber, w); err != nil {
			return err
		}
	}

	return w.Commit(wk.sync)
}

func (wk *wukongDB) GetSubscribers(channelId string, channelType uint8) ([]Member, error) {

	wk.metrics.GetSubscribersAdd(1)

	iter := wk.channelDb(channelId, channelType).NewIter(&pebble.IterOptions{
		LowerBound: key.NewSubscriberColumnKey(channelId, channelType, 0, key.MinColumnKey),
		UpperBound: key.NewSubscriberColumnKey(channelId, channelType, math.MaxUint64, key.MaxColumnKey),
	})
	defer iter.Close()

	members := make([]Member, 0)
	err := wk.iterateSubscriber(iter, func(member Member) bool {
		members = append(members, member)
		return true
	})
	if err != nil {
		return nil, err
	}
	return members, nil
}

// 获取订阅者数量
func (wk *wukongDB) GetSubscriberCount(channelId string, channelType uint8) (int, error) {
	iter := wk.channelDb(channelId, channelType).NewIter(&pebble.IterOptions{
		LowerBound: key.NewSubscriberColumnKey(channelId, channelType, 0, key.TableSubscriber.Column.Uid),
		UpperBound: key.NewSubscriberColumnKey(channelId, channelType, math.MaxUint64, key.TableSubscriber.Column.Uid),
	})
	defer iter.Close()

	var count int
	err := wk.iterateSubscriber(iter, func(member Member) bool {
		count++
		return true
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (wk *wukongDB) RemoveSubscribers(channelId string, channelType uint8, subscribers []string) error {

	wk.metrics.RemoveSubscribersAdd(1)

	subscribers = wkutil.RemoveRepeatedElement(subscribers) // 去重复

	// channelPrimaryId, err := wk.getChannelPrimaryKey(channelId, channelType)
	// if err != nil {
	// 	return err
	// }

	// 通过uids获取订阅者对象
	members, err := wk.getSubscribersByUids(channelId, channelType, subscribers)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil
		}
		return err
	}
	db := wk.channelDb(channelId, channelType)
	w := db.NewIndexedBatch()
	defer w.Close()
	for _, member := range members {
		if err := wk.removeSubscriber(channelId, channelType, member, w); err != nil {
			return err
		}
	}
	// err = wk.incChannelInfoSubscriberCount(channelPrimaryId, -len(members), w)
	// if err != nil {
	// 	wk.Error("RemoveSubscribers: incChannelInfoSubscriberCount failed", zap.Error(err))
	// 	return err
	// }
	return w.Commit(wk.sync)
}

func (wk *wukongDB) ExistSubscriber(channelId string, channelType uint8, uid string) (bool, error) {

	wk.metrics.ExistSubscriberAdd(1)

	uidIndexKey := key.NewSubscriberIndexKey(channelId, channelType, key.TableSubscriber.Index.Uid, key.HashWithString(uid))
	_, closer, err := wk.channelDb(channelId, channelType).Get(uidIndexKey)
	if closer != nil {
		defer closer.Close()
	}
	if err != nil {
		if err == pebble.ErrNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (wk *wukongDB) RemoveAllSubscriber(channelId string, channelType uint8) error {

	wk.metrics.RemoveAllSubscriberAdd(1)

	if wk.opts.EnableCost {
		start := time.Now()
		defer func() {
			cost := time.Since(start)
			if cost.Milliseconds() > 200 {
				wk.Info("RemoveAllSubscriber done", zap.Duration("cost", cost), zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
			}
		}()
	}

	channelPrimaryId, err := wk.getChannelPrimaryKey(channelId, channelType)
	if err != nil {
		return err
	}
	if channelPrimaryId == 0 {
		return fmt.Errorf("RemoveAllSubscriber: channelId: %s channelType: %d not found", channelId, channelType)
	}

	db := wk.channelDb(channelId, channelType)
	batch := db.NewIndexedBatch()
	defer batch.Close()

	// 删除数据
	err = batch.DeleteRange(key.NewSubscriberColumnKey(channelId, channelType, 0, key.MinColumnKey), key.NewSubscriberColumnKey(channelId, channelType, math.MaxUint64, key.MaxColumnKey), wk.noSync)
	if err != nil {
		return err
	}

	// 删除索引
	err = wk.deleteAllSubscriberIndex(channelId, channelType, batch)
	if err != nil {
		return err
	}

	// // 订阅者数量设置为0
	// err = wk.incChannelInfoSubscriberCount(channelPrimaryId, 0, batch)
	// if err != nil {
	// 	wk.Error("RemoveAllSubscriber: incChannelInfoSubscriberCount failed", zap.Error(err))
	// 	return err
	// }

	return batch.Commit(wk.sync)
}

func (wk *wukongDB) removeSubscriber(channelId string, channelType uint8, member Member, w pebble.Writer) error {
	var (
		err error
	)
	// remove all column
	if err = w.DeleteRange(key.NewSubscriberColumnKey(channelId, channelType, member.Id, key.MinColumnKey), key.NewSubscriberColumnKey(channelId, channelType, member.Id, key.MaxColumnKey), wk.noSync); err != nil {
		return err
	}

	// delete index
	if err = wk.deleteSubscriberIndex(channelId, channelType, member, w); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) getSubscribersByUids(channelId string, channelType uint8, uids []string) ([]Member, error) {
	members := make([]Member, 0, len(uids))
	db := wk.channelDb(channelId, channelType)
	for _, uid := range uids {
		id := key.HashWithString(uid)

		iter := db.NewIter(&pebble.IterOptions{
			LowerBound: key.NewSubscriberColumnKey(channelId, channelType, id, key.MinColumnKey),
			UpperBound: key.NewSubscriberColumnKey(channelId, channelType, id, key.MaxColumnKey),
		})
		defer iter.Close()

		err := wk.iterateSubscriber(iter, func(member Member) bool {
			members = append(members, member)
			return true
		})
		if err != nil {
			return nil, err
		}

	}
	return members, nil
}

// 增加频道白名单数量
// func (wk *wukongDB) incChannelInfoSubscriberCount(id uint64, count int, batch *Batch) error {
// 	wk.dblock.subscriberCountLock.lock(id)
// 	defer wk.dblock.subscriberCountLock.unlock(id)

// 	return wk.incChannelInfoColumnCount(id, key.TableChannelInfo.Column.SubscriberCount, key.TableChannelInfo.SecondIndex.SubscriberCount, count, batch)
// }

func (wk *wukongDB) iterateSubscriber(iter *pebble.Iterator, iterFnc func(member Member) bool) error {

	var (
		preId          uint64
		preMember      Member
		lastNeedAppend bool = true
		hasData        bool = false
	)

	for iter.First(); iter.Valid(); iter.Next() {
		id, columnName, err := key.ParseSubscriberColumnKey(iter.Key())
		if err != nil {
			return err
		}

		if id != preId {
			if preId != 0 {
				if !iterFnc(preMember) {
					lastNeedAppend = false
					break
				}
			}
			preId = id
			preMember = Member{
				Id: id,
			}
		}

		switch columnName {
		case key.TableSubscriber.Column.Uid:
			preMember.Uid = string(iter.Value())
		case key.TableSubscriber.Column.CreatedAt:
			tm := int64(wk.endian.Uint64(iter.Value()))
			if tm > 0 {
				t := time.Unix(tm/1e9, tm%1e9)
				preMember.CreatedAt = &t
			}

		case key.TableSubscriber.Column.UpdatedAt:
			tm := int64(wk.endian.Uint64(iter.Value()))
			if tm > 0 {
				t := time.Unix(tm/1e9, tm%1e9)
				preMember.UpdatedAt = &t
			}
		}
		hasData = true
	}
	if lastNeedAppend && hasData {
		_ = iterFnc(preMember)
	}
	return nil
}

func (wk *wukongDB) writeSubscriber(channelId string, channelType uint8, member Member, w pebble.Writer) error {

	if member.Id == 0 {
		return errors.New("writeSubscriber: member.Id is 0")
	}
	var err error
	// uid
	if err = w.Set(key.NewSubscriberColumnKey(channelId, channelType, member.Id, key.TableSubscriber.Column.Uid), []byte(member.Uid), wk.noSync); err != nil {
		return err
	}

	// uid index
	idBytes := make([]byte, 8)
	wk.endian.PutUint64(idBytes, member.Id)
	if err = w.Set(key.NewSubscriberIndexKey(channelId, channelType, key.TableSubscriber.Index.Uid, member.Id), idBytes, wk.noSync); err != nil {
		return err
	}

	// createdAt
	if member.CreatedAt != nil {
		ct := uint64(member.CreatedAt.UnixNano())
		createdAt := make([]byte, 8)
		wk.endian.PutUint64(createdAt, ct)
		if err := w.Set(key.NewSubscriberColumnKey(channelId, channelType, member.Id, key.TableSubscriber.Column.CreatedAt), createdAt, wk.noSync); err != nil {
			return err
		}

		// createdAt second index
		if err := w.Set(key.NewSubscriberSecondIndexKey(channelId, channelType, key.TableSubscriber.SecondIndex.CreatedAt, ct, member.Id), nil, wk.noSync); err != nil {
			return err
		}

	}

	if member.UpdatedAt != nil {
		// updatedAt
		updatedAt := make([]byte, 8)
		wk.endian.PutUint64(updatedAt, uint64(member.UpdatedAt.UnixNano()))
		if err = w.Set(key.NewSubscriberColumnKey(channelId, channelType, member.Id, key.TableSubscriber.Column.UpdatedAt), updatedAt, wk.noSync); err != nil {
			return err
		}
		// updatedAt second index
		if err = w.Set(key.NewSubscriberSecondIndexKey(channelId, channelType, key.TableSubscriber.SecondIndex.UpdatedAt, uint64(member.UpdatedAt.UnixNano()), member.Id), nil, wk.noSync); err != nil {
			return err
		}
	}
	return nil
}

func (wk *wukongDB) deleteAllSubscriberIndex(channelId string, channelType uint8, w pebble.Writer) error {

	var err error
	// uid index
	if err = w.DeleteRange(key.NewSubscriberIndexKey(channelId, channelType, key.TableSubscriber.Index.Uid, 0), key.NewSubscriberIndexKey(channelId, channelType, key.TableSubscriber.Index.Uid, math.MaxUint64), wk.noSync); err != nil {
		return err
	}

	// createdAt second index
	if err = w.DeleteRange(key.NewSubscriberSecondIndexKey(channelId, channelType, key.TableSubscriber.SecondIndex.CreatedAt, 0, 0), key.NewSubscriberSecondIndexKey(channelId, channelType, key.TableSubscriber.SecondIndex.CreatedAt, math.MaxUint64, 0), wk.noSync); err != nil {
		return err
	}

	// updatedAt second index
	if err = w.DeleteRange(key.NewSubscriberSecondIndexKey(channelId, channelType, key.TableSubscriber.SecondIndex.UpdatedAt, 0, 0), key.NewSubscriberSecondIndexKey(channelId, channelType, key.TableSubscriber.SecondIndex.UpdatedAt, math.MaxUint64, 0), wk.noSync); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) deleteSubscriberIndex(channelId string, channelType uint8, oldMember Member, w pebble.Writer) error {
	var err error
	// uid index
	if err = w.Delete(key.NewSubscriberIndexKey(channelId, channelType, key.TableSubscriber.Index.Uid, key.HashWithString(oldMember.Uid)), wk.noSync); err != nil {
		return err
	}

	// createdAt second index
	if oldMember.CreatedAt != nil {
		if err = w.Delete(key.NewSubscriberSecondIndexKey(channelId, channelType, key.TableSubscriber.SecondIndex.CreatedAt, uint64(oldMember.CreatedAt.UnixNano()), oldMember.Id), wk.noSync); err != nil {
			return err
		}
	}

	// updatedAt second index
	if oldMember.UpdatedAt != nil {
		if err = w.Delete(key.NewSubscriberSecondIndexKey(channelId, channelType, key.TableSubscriber.SecondIndex.UpdatedAt, uint64(oldMember.UpdatedAt.UnixNano()), oldMember.Id), wk.noSync); err != nil {
			return err
		}
	}

	return nil
}
