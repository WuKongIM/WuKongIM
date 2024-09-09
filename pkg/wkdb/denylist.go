package wkdb

import (
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
	"go.uber.org/zap"
)

func (wk *wukongDB) AddDenylist(channelId string, channelType uint8, uids []string) error {

	db := wk.channelDb(channelId, channelType)

	channelPrimaryId, err := wk.getChannelPrimaryKey(channelId, channelType)
	if err != nil {
		return err
	}

	w := db.NewBatch()
	defer w.Close()
	for _, uid := range uids {
		id := key.HashWithString(uid)
		if err := wk.writeDenylist(channelId, channelType, id, uid, w); err != nil {
			return err
		}
	}
	err = wk.incChannelInfoDenylistCount(channelPrimaryId, len(uids), db)
	if err != nil {
		wk.Error("incChannelInfoDenylistCount failed", zap.Error(err))
		return err

	}
	return w.Commit(wk.sync)
}

func (wk *wukongDB) GetDenylist(channelId string, channelType uint8) ([]string, error) {
	iter := wk.channelDb(channelId, channelType).NewIter(&pebble.IterOptions{
		LowerBound: key.NewDenylistPrimaryKey(channelId, channelType, 0),
		UpperBound: key.NewDenylistPrimaryKey(channelId, channelType, math.MaxUint64),
	})
	defer iter.Close()
	return wk.parseDenylist(iter, 0)
}

func (wk *wukongDB) ExistDenylist(channelId string, channelType uint8, uid string) (bool, error) {
	uidIndexKey := key.NewDenylistIndexUidKey(channelId, channelType, uid)
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

func (wk *wukongDB) RemoveDenylist(channelId string, channelType uint8, uids []string) error {
	db := wk.channelDb(channelId, channelType)

	channelPrimaryId, err := wk.getChannelPrimaryKey(channelId, channelType)
	if err != nil {
		return err
	}

	idMap, err := wk.getDenylistIdsByUids(channelId, channelType, uids)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil
		}
		return err
	}
	w := db.NewBatch()
	defer w.Close()
	for uid, id := range idMap {
		if err := wk.removeDenylist(channelId, channelType, id, uid, w); err != nil {
			return err
		}
	}

	err = wk.incChannelInfoDenylistCount(channelPrimaryId, -len(idMap), db)
	if err != nil {
		wk.Error("RemoveDenylist: incChannelInfoDenylistCount failed", zap.Error(err))
		return err
	}

	return w.Commit(wk.sync)
}

func (wk *wukongDB) RemoveAllDenylist(channelId string, channelType uint8) error {

	channelPrimaryId, err := wk.getChannelPrimaryKey(channelId, channelType)
	if err != nil {
		return err
	}

	db := wk.channelDb(channelId, channelType)

	batch := db.NewBatch()
	defer batch.Close()

	// 删除数据
	err = batch.DeleteRange(key.NewDenylistPrimaryKey(channelId, channelType, 0), key.NewDenylistPrimaryKey(channelId, channelType, math.MaxUint64), wk.noSync)
	if err != nil {
		return err
	}

	// 删除索引
	err = batch.DeleteRange(key.NewDenylistIndexUidLowKey(channelId, channelType), key.NewDenylistIndexUidHighKey(channelId, channelType), wk.noSync)
	if err != nil {
		return err
	}

	// 黑名单数量设置为0
	err = wk.incChannelInfoDenylistCount(channelPrimaryId, 0, db)
	if err != nil {
		wk.Error("RemoveAllDenylist: incChannelInfoDenylistCount failed", zap.Error(err))
		return err

	}

	return batch.Commit(wk.sync)
}

func (wk *wukongDB) removeDenylist(channelId string, channelType uint8, id uint64, uid string, w pebble.Writer) error {
	var (
		err error
	)
	// uid
	if err = w.Delete(key.NewDenylistColumnKey(channelId, channelType, id, key.TableDenylist.Column.Uid), wk.sync); err != nil {
		return err
	}

	// uid index
	uidIndexKey := key.NewDenylistIndexUidKey(channelId, channelType, uid)
	if err = w.Delete(uidIndexKey, wk.sync); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) getDenylistIdsByUids(channelId string, channelType uint8, uids []string) (map[string]uint64, error) {
	resultMap := make(map[string]uint64)
	for _, uid := range uids {
		uidIndexKey := key.NewDenylistIndexUidKey(channelId, channelType, uid)
		uidIndexValue, closer, err := wk.channelDb(channelId, channelType).Get(uidIndexKey)
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

func (wk *wukongDB) writeDenylist(channelId string, channelType uint8, id uint64, uid string, w pebble.Writer) error {
	var (
		err error
	)
	// uid
	if err = w.Set(key.NewDenylistColumnKey(channelId, channelType, id, key.TableDenylist.Column.Uid), []byte(uid), wk.sync); err != nil {
		return err
	}

	// uid index
	idBytes := make([]byte, 8)
	wk.endian.PutUint64(idBytes, id)
	if err = w.Set(key.NewDenylistIndexUidKey(channelId, channelType, uid), idBytes, wk.sync); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) parseDenylist(iter *pebble.Iterator, limit int) ([]string, error) {

	var (
		uids           = make([]string, 0, limit)
		preId          uint64
		preUid         string
		lastNeedAppend bool = true
		hasData        bool = false
	)
	for iter.First(); iter.Valid(); iter.Next() {
		id, columnName, err := key.ParseDenylistColumnKey(iter.Key())
		if err != nil {
			return nil, err
		}
		if id != preId {
			if preId != 0 {
				uids = append(uids, preUid)
				if limit != 0 && len(uids) >= limit {
					lastNeedAppend = false
					break
				}
			}
			preId = id
			preUid = ""
		}
		if columnName == key.TableDenylist.Column.Uid {
			preUid = string(iter.Value())

		}
		hasData = true
	}
	if lastNeedAppend && hasData {
		uids = append(uids, preUid)
	}
	return uids, nil
}

// 增加频道黑名单数量
func (wk *wukongDB) incChannelInfoDenylistCount(id uint64, count int, db *pebble.DB) error {
	wk.dblock.denylistCountLock.lock(id)
	defer wk.dblock.denylistCountLock.unlock(id)
	return wk.incChannelInfoColumnCount(id, key.TableChannelInfo.Column.DenylistCount, key.TableChannelInfo.SecondIndex.DenylistCount, count, db)
}
