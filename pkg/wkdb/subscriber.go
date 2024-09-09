package wkdb

import (
	"fmt"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
	"go.uber.org/zap"
)

func (wk *wukongDB) AddSubscribers(channelId string, channelType uint8, subscribers []string) error {

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
	for _, uid := range subscribers {
		id := key.HashWithString(uid)
		if err := wk.writeSubscriber(channelId, channelType, id, uid, w); err != nil {
			return err
		}
	}
	err = wk.incChannelInfoSubscriberCount(channelPrimaryId, len(subscribers), db)
	if err != nil {
		wk.Error("incChannelInfoSubscriberCount failed", zap.Error(err))
		return err
	}

	return w.Commit(wk.sync)
}

func (wk *wukongDB) GetSubscribers(channelId string, channelType uint8) ([]string, error) {
	iter := wk.channelDb(channelId, channelType).NewIter(&pebble.IterOptions{
		LowerBound: key.NewSubscriberPrimaryKey(channelId, channelType, 0),
		UpperBound: key.NewSubscriberPrimaryKey(channelId, channelType, math.MaxUint64),
	})
	defer iter.Close()
	return wk.parseSubscriber(iter, 0)
}

func (wk *wukongDB) RemoveSubscribers(channelId string, channelType uint8, subscribers []string) error {

	channelPrimaryId, err := wk.getChannelPrimaryKey(channelId, channelType)
	if err != nil {
		return err
	}
	if channelPrimaryId == 0 {
		return fmt.Errorf("RemoveSubscribers: channelId: %s channelType: %d not found", channelId, channelType)
	}

	idMap, err := wk.getSubscriberIdsByUids(channelId, channelType, subscribers)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil
		}
		return err
	}
	db := wk.channelDb(channelId, channelType)
	w := db.NewBatch()
	defer w.Close()
	for uid, id := range idMap {
		if err := wk.removeSubscriber(channelId, channelType, id, uid, w); err != nil {
			return err
		}
	}
	err = wk.incChannelInfoSubscriberCount(channelPrimaryId, -len(idMap), db)
	if err != nil {
		wk.Error("RemoveSubscribers: incChannelInfoSubscriberCount failed", zap.Error(err))
		return err
	}
	return w.Commit(wk.sync)
}

func (wk *wukongDB) ExistSubscriber(channelId string, channelType uint8, uid string) (bool, error) {
	uidIndexKey := key.NewSubscriberIndexUidKey(channelId, channelType, uid)
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

	channelPrimaryId, err := wk.getChannelPrimaryKey(channelId, channelType)
	if err != nil {
		return err
	}
	if channelPrimaryId == 0 {
		return fmt.Errorf("RemoveAllSubscriber: channelId: %s channelType: %d not found", channelId, channelType)
	}

	db := wk.channelDb(channelId, channelType)
	batch := db.NewBatch()
	defer batch.Close()

	// 删除数据
	err = batch.DeleteRange(key.NewSubscriberPrimaryKey(channelId, channelType, 0), key.NewSubscriberPrimaryKey(channelId, channelType, math.MaxUint64), wk.noSync)
	if err != nil {
		return err
	}

	// 删除索引
	err = batch.DeleteRange(key.NewSubscriberIndexUidLowKey(channelId, channelType), key.NewSubscriberIndexUidHighKey(channelId, channelType), wk.noSync)
	if err != nil {
		return err
	}

	// 订阅者数量设置为0
	err = wk.incChannelInfoSubscriberCount(channelPrimaryId, 0, db)
	if err != nil {
		wk.Error("RemoveAllSubscriber: incChannelInfoSubscriberCount failed", zap.Error(err))
		return err
	}

	return batch.Commit(wk.sync)
}

func (wk *wukongDB) removeSubscriber(channelId string, channelType uint8, id uint64, uid string, w pebble.Writer) error {
	var (
		err error
	)
	// uid
	if err = w.Delete(key.NewSubscriberColumnKey(channelId, channelType, id, key.TableUser.Column.Uid), wk.sync); err != nil {
		return err
	}

	// uid index
	uidIndexKey := key.NewSubscriberIndexUidKey(channelId, channelType, uid)
	if err = w.Delete(uidIndexKey, wk.sync); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) getSubscriberIdsByUids(channelId string, channelType uint8, uids []string) (map[string]uint64, error) {
	resultMap := make(map[string]uint64)
	for _, uid := range uids {
		uidIndexKey := key.NewSubscriberIndexUidKey(channelId, channelType, uid)
		uidIndexValue, closer, err := wk.shardDB(channelId).Get(uidIndexKey)
		if err != nil {
			if err == pebble.ErrNotFound {
				continue
			}
			return nil, err
		}
		defer closer.Close()

		if len(uidIndexValue) == 0 {
			continue
		}
		resultMap[uid] = wk.endian.Uint64(uidIndexValue)
	}
	return resultMap, nil
}

// 增加频道白名单数量
func (wk *wukongDB) incChannelInfoSubscriberCount(id uint64, count int, db *pebble.DB) error {
	wk.dblock.subscriberCountLock.lock(id)
	defer wk.dblock.subscriberCountLock.unlock(id)

	return wk.incChannelInfoColumnCount(id, key.TableChannelInfo.Column.SubscriberCount, key.TableChannelInfo.SecondIndex.SubscriberCount, count, db)
}

func (wk *wukongDB) parseSubscriber(iter *pebble.Iterator, limit int) ([]string, error) {

	var (
		subscribers    = make([]string, 0, limit)
		preId          uint64
		preSubscriber  string
		lastNeedAppend bool = true
		hasData        bool = false
	)
	for iter.First(); iter.Valid(); iter.Next() {
		id, columnName, err := key.ParseSubscriberColumnKey(iter.Key())
		if err != nil {
			return nil, err
		}
		if id != preId {
			if preId != 0 {
				subscribers = append(subscribers, preSubscriber)
				if limit != 0 && len(subscribers) >= limit {
					lastNeedAppend = false
					break
				}
			}
			preId = id
			preSubscriber = ""
		}
		if columnName == key.TableUser.Column.Uid {
			preSubscriber = string(iter.Value())

		}
		hasData = true
	}
	if lastNeedAppend && hasData {
		subscribers = append(subscribers, preSubscriber)
	}
	return subscribers, nil
}

func (wk *wukongDB) writeSubscriber(channelId string, channelType uint8, id uint64, uid string, w pebble.Writer) error {
	var (
		err error
	)
	// uid
	if err = w.Set(key.NewSubscriberColumnKey(channelId, channelType, id, key.TableUser.Column.Uid), []byte(uid), wk.noSync); err != nil {
		return err
	}

	// uid index
	idBytes := make([]byte, 8)
	wk.endian.PutUint64(idBytes, id)
	if err = w.Set(key.NewSubscriberIndexUidKey(channelId, channelType, uid), idBytes, wk.noSync); err != nil {
		return err
	}

	return nil
}
