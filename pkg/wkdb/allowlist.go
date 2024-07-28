package wkdb

import (
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
	"go.uber.org/zap"
)

func (wk *wukongDB) AddAllowlist(channelId string, channelType uint8, uids []string) error {

	db := wk.channelDb(channelId, channelType)

	channelPrimaryId, err := wk.getChannelPrimaryKey(channelId, channelType)
	if err != nil {
		return err
	}
	if channelPrimaryId == 0 {
		channelPrimaryId, err = wk.AddOrUpdateChannel(ChannelInfo{
			ChannelId:   channelId,
			ChannelType: channelType,
		})
		if err != nil {
			return err
		}
	}

	w := db.NewBatch()
	defer w.Close()
	for _, uid := range uids {
		id := key.HashWithString(uid)
		if err := wk.writeAllowlist(channelId, channelType, id, uid, w); err != nil {
			return err
		}
	}

	err = wk.incChannelInfoAllowlistCount(channelPrimaryId, len(uids), db)
	if err != nil {
		wk.Error("incChannelInfoAllowlistCount failed", zap.Error(err))
		return err
	}

	return w.Commit(wk.sync)
}

func (wk *wukongDB) GetAllowlist(channelId string, channelType uint8) ([]string, error) {
	iter := wk.channelDb(channelId, channelType).NewIter(&pebble.IterOptions{
		LowerBound: key.NewAllowlistPrimaryKey(channelId, channelType, 0),
		UpperBound: key.NewAllowlistPrimaryKey(channelId, channelType, math.MaxUint64),
	})
	defer iter.Close()
	uids := make([]string, 0)
	err := wk.iterateAllowlist(iter, func(uid string) bool {
		uids = append(uids, uid)
		return true
	})
	return uids, err
}

func (wk *wukongDB) HasAllowlist(channelId string, channelType uint8) (bool, error) {
	iter := wk.channelDb(channelId, channelType).NewIter(&pebble.IterOptions{
		LowerBound: key.NewAllowlistPrimaryKey(channelId, channelType, 0),
		UpperBound: key.NewAllowlistPrimaryKey(channelId, channelType, math.MaxUint64),
	})
	defer iter.Close()
	var exist = false
	err := wk.iterateAllowlist(iter, func(uid string) bool {
		exist = true
		return false
	})
	return exist, err
}

func (wk *wukongDB) ExistAllowlist(channeId string, channelType uint8, uid string) (bool, error) {
	uidIndexKey := key.NewAllowlistIndexUidKey(channeId, channelType, uid)
	_, closer, err := wk.channelDb(channeId, channelType).Get(uidIndexKey)
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

func (wk *wukongDB) RemoveAllowlist(channelId string, channelType uint8, uids []string) error {

	db := wk.channelDb(channelId, channelType)

	channelPrimaryId, err := wk.getChannelPrimaryKey(channelId, channelType)
	if err != nil {
		return err
	}
	if channelPrimaryId == 0 {
		channelPrimaryId, err = wk.AddOrUpdateChannel(ChannelInfo{
			ChannelId:   channelId,
			ChannelType: channelType,
		})
		if err != nil {
			return err
		}
	}

	idMap, err := wk.getAllowlistIdsByUids(channelId, channelType, uids)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil
		}
		return err
	}
	w := db.NewBatch()
	defer w.Close()
	for uid, id := range idMap {
		if err := wk.removeAllowlist(channelId, channelType, id, uid, w); err != nil {
			return err
		}
	}

	err = wk.incChannelInfoAllowlistCount(channelPrimaryId, -len(idMap), db)
	if err != nil {
		wk.Error("RemoveAllowlist: incChannelInfoAllowlistCount failed", zap.Error(err))
		return err
	}

	return w.Commit(wk.sync)
}

func (wk *wukongDB) RemoveAllAllowlist(channelId string, channelType uint8) error {

	db := wk.channelDb(channelId, channelType)

	channelPrimaryId, err := wk.getChannelPrimaryKey(channelId, channelType)
	if err != nil {
		return err
	}
	if channelPrimaryId == 0 {
		channelPrimaryId, err = wk.AddOrUpdateChannel(ChannelInfo{
			ChannelId:   channelId,
			ChannelType: channelType,
		})
		if err != nil {
			return err
		}
	}

	batch := db.NewBatch()
	defer batch.Close()

	// 删除数据
	err = batch.DeleteRange(key.NewAllowlistPrimaryKey(channelId, channelType, 0), key.NewAllowlistPrimaryKey(channelId, channelType, math.MaxUint64), wk.sync)
	if err != nil {
		return err
	}

	// 删除索引
	err = batch.DeleteRange(key.NewAllowlistIndexUidLowKey(channelId, channelType), key.NewAllowlistIndexUidHighKey(channelId, channelType), wk.sync)
	if err != nil {
		return err
	}

	// 白名单数量设置为0
	err = wk.incChannelInfoAllowlistCount(channelPrimaryId, math.MinInt, db)
	if err != nil {
		wk.Error("RemoveAllAllowlist: incChannelInfoAllowlistCount failed", zap.Error(err))
		return err
	}

	return batch.Commit(wk.sync)
}

func (wk *wukongDB) removeAllowlist(channelId string, channelType uint8, id uint64, uid string, w pebble.Writer) error {
	var (
		err error
	)
	// uid
	if err = w.Delete(key.NewAllowlistColumnKey(channelId, channelType, id, key.TableAllowlist.Column.Uid), wk.sync); err != nil {
		return err
	}

	// uid index
	uidIndexKey := key.NewAllowlistIndexUidKey(channelId, channelType, uid)
	if err = w.Delete(uidIndexKey, wk.sync); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) getAllowlistIdsByUids(channelId string, channelType uint8, uids []string) (map[string]uint64, error) {
	resultMap := make(map[string]uint64)
	for _, uid := range uids {
		uidIndexKey := key.NewAllowlistIndexUidKey(channelId, channelType, uid)
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

func (wk *wukongDB) writeAllowlist(channelId string, channelType uint8, id uint64, uid string, w pebble.Writer) error {
	var (
		err error
	)
	// uid
	if err = w.Set(key.NewAllowlistColumnKey(channelId, channelType, id, key.TableAllowlist.Column.Uid), []byte(uid), wk.sync); err != nil {
		return err
	}

	// uid index
	idBytes := make([]byte, 8)
	wk.endian.PutUint64(idBytes, id)
	if err = w.Set(key.NewAllowlistIndexUidKey(channelId, channelType, uid), idBytes, wk.sync); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) iterateAllowlist(iter *pebble.Iterator, iterFnc func(uid string) bool) error {
	var (
		preId          uint64
		preUid         string
		lastNeedAppend bool = true
		hasData        bool = false
	)
	for iter.First(); iter.Valid(); iter.Next() {
		id, columnName, err := key.ParseAllowlistColumnKey(iter.Key())
		if err != nil {
			return err
		}
		if id != preId {
			if preId != 0 {
				if !iterFnc(preUid) {
					lastNeedAppend = false
					break
				}
			}
			preId = id
			preUid = ""
		}
		if columnName == key.TableAllowlist.Column.Uid {
			preUid = string(iter.Value())

		}
		hasData = true
	}
	if lastNeedAppend && hasData {
		_ = iterFnc(preUid)
	}
	return nil

}

// 增加频道白名单数量
func (wk *wukongDB) incChannelInfoAllowlistCount(id uint64, count int, db *pebble.DB) error {
	wk.dblock.allowlistCountLock.lock(id)
	defer wk.dblock.allowlistCountLock.unlock(id)
	return wk.incChannelInfoColumnCount(id, key.TableChannelInfo.Column.AllowlistCount, key.TableChannelInfo.SecondIndex.AllowlistCount, count, db)
}
