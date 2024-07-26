package wkdb

import (
	"fmt"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
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
		id := uint64(wk.prmaryKeyGen.Generate().Int64())
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
	err = wk.incChannelInfoSubscriberCount(channelPrimaryId, math.MinInt, db)
	if err != nil {
		wk.Error("RemoveAllSubscriber: incChannelInfoSubscriberCount failed", zap.Error(err))
		return err
	}

	return batch.Commit(wk.sync)
}

func (wk *wukongDB) AddOrUpdateChannel(channelInfo ChannelInfo) (uint64, error) {

	primaryKey, err := wk.getChannelPrimaryKey(channelInfo.ChannelId, channelInfo.ChannelType)
	if err != nil {
		return 0, err
	}

	var isCreate bool
	if primaryKey == 0 {
		isCreate = true
		primaryKey = uint64(wk.prmaryKeyGen.Generate().Int64())
	}

	w := wk.channelDb(channelInfo.ChannelId, channelInfo.ChannelType).NewBatch()
	defer w.Close()
	if err := wk.writeChannelInfo(primaryKey, channelInfo, w); err != nil {
		return 0, err
	}

	if isCreate {
		err = wk.IncChannelCount(1)
		if err != nil {
			wk.Error("IncChannelCount failed", zap.Error(err))
			return 0, err
		}
	}

	return primaryKey, w.Commit(wk.sync)
}

func (wk *wukongDB) GetChannel(channelId string, channelType uint8) (ChannelInfo, error) {

	id, err := wk.getChannelPrimaryKey(channelId, channelType)
	if err != nil {
		return EmptyChannelInfo, err
	}
	if id == 0 {
		return EmptyChannelInfo, nil
	}

	iter := wk.channelDb(channelId, channelType).NewIter(&pebble.IterOptions{
		LowerBound: key.NewChannelInfoColumnKey(id, key.MinColumnKey),
		UpperBound: key.NewChannelInfoColumnKey(id, key.MaxColumnKey),
	})
	defer iter.Close()

	var channelInfos []ChannelInfo
	err = wk.iterChannelInfo(iter, func(channelInfo ChannelInfo) bool {
		channelInfos = append(channelInfos, channelInfo)
		return true
	})
	if err != nil {
		return EmptyChannelInfo, err
	}
	if len(channelInfos) == 0 {
		return EmptyChannelInfo, nil

	}
	return channelInfos[0], nil
}

func (wk *wukongDB) SearchChannels(req ChannelSearchReq) ([]ChannelInfo, error) {

	var channelInfos []ChannelInfo
	if req.ChannelId != "" && req.ChannelType != 0 {
		channelInfo, err := wk.GetChannel(req.ChannelId, req.ChannelType)
		if err != nil {
			return nil, err
		}
		channelInfos = append(channelInfos, channelInfo)
		return channelInfos, nil
	}

	currentSize := 0

	iterFnc := func(channelInfo ChannelInfo) bool {
		if req.ChannelId != "" && req.ChannelId != channelInfo.ChannelId {
			return true
		}
		if req.ChannelType != 0 && req.ChannelType != channelInfo.ChannelType {
			return true
		}
		if req.Ban != nil && *req.Ban != channelInfo.Ban {
			return true
		}
		if req.Disband != nil && *req.Disband != channelInfo.Disband {
			return true
		}
		if currentSize > req.Limit*req.CurrentPage { // 大于当前页的消息终止遍历
			return false
		}

		currentSize++

		if currentSize > (req.CurrentPage-1)*req.Limit && currentSize <= req.CurrentPage*req.Limit {
			channelInfos = append(channelInfos, channelInfo)
			return true
		}
		return true
	}

	for _, db := range wk.dbs {

		// 通过索引查询
		has, err := wk.searchChannelsByIndex(req, db, iterFnc)
		if err != nil {
			return nil, err
		}
		if has { // 如果有触发索引，则无需全局查询
			continue
		}

		iter := db.NewIter(&pebble.IterOptions{
			LowerBound: key.NewChannelInfoColumnKey(0, key.MinColumnKey),
			UpperBound: key.NewChannelInfoColumnKey(math.MaxUint64, key.MaxColumnKey),
		})
		defer iter.Close()
		err = wk.iterChannelInfo(iter, iterFnc)
		if err != nil {
			return nil, err
		}
	}

	var results = channelInfos

	if req.Limit > 0 && len(channelInfos) > req.Limit {
		results = channelInfos[:req.Limit]
	}

	for i, result := range results {
		lastMsgSeq, lastTime, err := wk.GetChannelLastMessageSeq(result.ChannelId, result.ChannelType)
		if err != nil {
			return nil, err
		}
		results[i].LastMsgSeq = lastMsgSeq
		results[i].LastMsgTime = lastTime
	}

	return results, nil
}

func (wk *wukongDB) searchChannelsByIndex(req ChannelSearchReq, db *pebble.DB, iterFnc func(ch ChannelInfo) bool) (bool, error) {
	var lowKey []byte
	var highKey []byte

	var existKey = false

	if req.Ban != nil {
		lowKey = key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.Ban, uint64(wkutil.BoolToInt(*req.Ban)), 0)
		highKey = key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.Ban, uint64(wkutil.BoolToInt(*req.Ban)), math.MaxUint64)
		existKey = true
	}

	if !existKey && req.Disband != nil {
		lowKey = key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.Disband, uint64(wkutil.BoolToInt(*req.Disband)), 0)
		highKey = key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.Disband, uint64(wkutil.BoolToInt(*req.Disband)), math.MaxUint64)
		existKey = true
	}

	if !existKey && req.SubscriberCountGte != nil && req.SubscriberCountLte != nil {
		lowKey = key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.SubscriberCount, uint64(*req.SubscriberCountGte), 0)
		highKey = key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.SubscriberCount, uint64(*req.SubscriberCountLte), math.MaxUint64)
		existKey = true
	}

	if !existKey && req.SubscriberCountLte != nil {
		lowKey = key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.SubscriberCount, 0, 0)
		highKey = key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.SubscriberCount, uint64(*req.SubscriberCountLte), math.MaxUint64)
		existKey = true
	}

	if !existKey && req.SubscriberCountGte != nil {
		lowKey = key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.SubscriberCount, uint64(*req.SubscriberCountGte), 0)
		highKey = key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.SubscriberCount, math.MaxUint64, math.MaxUint64)
		existKey = true
	}

	if !existKey && req.AllowlistCountGte != nil && req.AllowlistCountLte != nil {
		lowKey = key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.AllowlistCount, uint64(*req.AllowlistCountGte), 0)
		highKey = key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.AllowlistCount, uint64(*req.AllowlistCountLte), math.MaxUint64)
		existKey = true
	}

	if !existKey && req.AllowlistCountLte != nil {
		lowKey = key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.AllowlistCount, 0, 0)
		highKey = key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.AllowlistCount, uint64(*req.AllowlistCountLte), math.MaxUint64)
		existKey = true
	}

	if !existKey && req.AllowlistCountGte != nil {
		lowKey = key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.AllowlistCount, uint64(*req.AllowlistCountGte), 0)
		highKey = key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.AllowlistCount, math.MaxUint64, math.MaxUint64)
		existKey = true
	}

	if !existKey && req.DenylistCountGte != nil && req.DenylistCountLte != nil {
		lowKey = key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.DenylistCount, uint64(*req.DenylistCountGte), 0)
		highKey = key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.DenylistCount, uint64(*req.DenylistCountLte), math.MaxUint64)
		existKey = true
	}

	if !existKey && req.DenylistCountLte != nil {
		lowKey = key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.DenylistCount, 0, 0)
		highKey = key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.DenylistCount, uint64(*req.DenylistCountLte), math.MaxUint64)
		existKey = true
	}
	if !existKey && req.DenylistCountGte != nil {
		lowKey = key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.DenylistCount, uint64(*req.DenylistCountGte), 0)
		highKey = key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.DenylistCount, math.MaxUint64, math.MaxUint64)
		existKey = true
	}

	if !existKey {
		return false, nil
	}

	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: lowKey,
		UpperBound: highKey,
	})

	for iter.Last(); iter.Valid(); iter.Prev() {
		_, id, err := key.ParseChannelInfoSecondIndexKey(iter.Key())
		if err != nil {
			return false, err
		}

		dataIter := db.NewIter(&pebble.IterOptions{
			LowerBound: key.NewChannelInfoColumnKey(id, key.MinColumnKey),
			UpperBound: key.NewChannelInfoColumnKey(id, key.MaxColumnKey),
		})
		defer dataIter.Close()

		var ch ChannelInfo
		err = wk.iterChannelInfo(dataIter, func(channelInfo ChannelInfo) bool {
			ch = channelInfo
			return false
		})
		if err != nil {
			return false, err
		}
		if iterFnc != nil {
			if !iterFnc(ch) {
				break
			}
		}

	}
	return true, nil
}

func (wk *wukongDB) ExistChannel(channelId string, channelType uint8) (bool, error) {
	id, err := wk.getChannelPrimaryKey(channelId, channelType)
	if err != nil {
		return false, err
	}
	return id > 0, nil
}

func (wk *wukongDB) DeleteChannel(channelId string, channelType uint8) error {
	id, err := wk.getChannelPrimaryKey(channelId, channelType)
	if err != nil {
		return err
	}
	if id == 0 {
		return nil
	}
	batch := wk.channelDb(channelId, channelType).NewBatch()
	defer batch.Close()
	// 删除索引
	err = batch.Delete(key.NewChannelInfoIndexKey(channelId, channelType), wk.noSync)
	if err != nil {
		return err
	}

	// 删除数据
	err = batch.DeleteRange(key.NewChannelInfoColumnKey(id, key.MinColumnKey), key.NewChannelInfoColumnKey(id, key.MaxColumnKey), wk.sync)
	if err != nil {
		return err
	}

	err = batch.DeleteRange(key.NewChannelInfoSecondIndexKey(key.MinColumnKey, 0, id), key.NewChannelInfoSecondIndexKey(key.MaxColumnKey, math.MaxUint64, id), wk.sync)
	if err != nil {
		return err
	}

	err = wk.IncChannelCount(-1)
	if err != nil {
		return err
	}

	return batch.Commit(wk.sync)
}

func (wk *wukongDB) UpdateChannelAppliedIndex(channelId string, channelType uint8, index uint64) error {

	indexBytes := make([]byte, 8)
	wk.endian.PutUint64(indexBytes, index)
	return wk.channelDb(channelId, channelType).Set(key.NewChannelCommonColumnKey(channelId, channelType, key.TableChannelCommon.Column.AppliedIndex), indexBytes, wk.sync)
}

func (wk *wukongDB) GetChannelAppliedIndex(channelId string, channelType uint8) (uint64, error) {

	data, closer, err := wk.channelDb(channelId, channelType).Get(key.NewChannelCommonColumnKey(channelId, channelType, key.TableChannelCommon.Column.AppliedIndex))
	if closer != nil {
		defer closer.Close()
	}
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}

	return wk.endian.Uint64(data), nil
}

func (wk *wukongDB) AddDenylist(channelId string, channelType uint8, uids []string) error {

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
		id := uint64(wk.prmaryKeyGen.Generate().Int64())
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
	if channelPrimaryId == 0 {
		channelPrimaryId, err = wk.AddOrUpdateChannel(ChannelInfo{
			ChannelId:   channelId,
			ChannelType: channelType,
		})
		if err != nil {
			return err
		}
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
	if channelPrimaryId == 0 {
		channelPrimaryId, err = wk.AddOrUpdateChannel(ChannelInfo{
			ChannelId:   channelId,
			ChannelType: channelType,
		})
		if err != nil {
			return err
		}
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
	err = wk.incChannelInfoDenylistCount(channelPrimaryId, math.MinInt, db)
	if err != nil {
		wk.Error("RemoveAllDenylist: incChannelInfoDenylistCount failed", zap.Error(err))
		return err

	}

	return batch.Commit(wk.sync)
}

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
		id := uint64(wk.prmaryKeyGen.Generate().Int64())
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

// 增加频道白名单数量
func (wk *wukongDB) incChannelInfoSubscriberCount(id uint64, count int, db *pebble.DB) error {
	wk.dblock.subscriberCountLock.lock(id)
	defer wk.dblock.subscriberCountLock.unlock(id)

	return wk.incChannelInfoColumnCount(id, key.TableChannelInfo.Column.SubscriberCount, key.TableChannelInfo.SecondIndex.SubscriberCount, count, db)
}

// 增加频道白名单数量
func (wk *wukongDB) incChannelInfoAllowlistCount(id uint64, count int, db *pebble.DB) error {
	wk.dblock.allowlistCountLock.lock(id)
	defer wk.dblock.allowlistCountLock.unlock(id)
	return wk.incChannelInfoColumnCount(id, key.TableChannelInfo.Column.AllowlistCount, key.TableChannelInfo.SecondIndex.AllowlistCount, count, db)
}

// 增加频道黑名单数量
func (wk *wukongDB) incChannelInfoDenylistCount(id uint64, count int, db *pebble.DB) error {
	wk.dblock.denylistCountLock.lock(id)
	defer wk.dblock.denylistCountLock.unlock(id)
	return wk.incChannelInfoColumnCount(id, key.TableChannelInfo.Column.DenylistCount, key.TableChannelInfo.SecondIndex.DenylistCount, count, db)
}

// 增加频道属性数量 id为频道信息的唯一主键 count为math.MinInt 表示重置为0
func (wk *wukongDB) incChannelInfoColumnCount(id uint64, columnName, indexName [2]byte, count int, db *pebble.DB) error {
	countKey := key.NewChannelInfoColumnKey(id, columnName)
	if count == math.MinInt { //
		return db.Set(countKey, []byte{0x00, 0x00, 0x00, 0x00}, wk.sync)
	}

	countBytes, closer, err := db.Get(countKey)
	if err != nil && err != pebble.ErrNotFound {
		return err
	}
	if closer != nil {
		defer closer.Close()
	}

	var oldCount uint32
	if len(countBytes) > 0 {
		oldCount = wk.endian.Uint32(countBytes)
	} else {
		countBytes = make([]byte, 4)
	}
	count += int(oldCount)
	wk.endian.PutUint32(countBytes, uint32(count))

	// 设置数量
	err = db.Set(countKey, countBytes, wk.sync)
	if err != nil {
		return err
	}
	// 设置数量对应的索引
	secondIndexKey := key.NewChannelInfoSecondIndexKey(indexName, uint64(count), id)
	err = db.Set(secondIndexKey, nil, wk.sync)
	if err != nil {
		return err
	}
	return nil
}

func (wk *wukongDB) writeChannelInfo(primaryKey uint64, channelInfo ChannelInfo, w pebble.Writer) error {

	var (
		err error
	)
	// channelId
	if err = w.Set(key.NewChannelInfoColumnKey(primaryKey, key.TableChannelInfo.Column.ChannelId), []byte(channelInfo.ChannelId), wk.noSync); err != nil {
		return err
	}

	// channelType
	channelTypeBytes := make([]byte, 1)
	channelTypeBytes[0] = channelInfo.ChannelType
	if err = w.Set(key.NewChannelInfoColumnKey(primaryKey, key.TableChannelInfo.Column.ChannelType), channelTypeBytes, wk.noSync); err != nil {
		return err
	}

	// ban
	banBytes := make([]byte, 1)
	banBytes[0] = wkutil.BoolToUint8(channelInfo.Ban)
	if err = w.Set(key.NewChannelInfoColumnKey(primaryKey, key.TableChannelInfo.Column.Ban), banBytes, wk.noSync); err != nil {
		return err
	}

	// large
	largeBytes := make([]byte, 1)
	largeBytes[0] = wkutil.BoolToUint8(channelInfo.Large)
	if err = w.Set(key.NewChannelInfoColumnKey(primaryKey, key.TableChannelInfo.Column.Large), largeBytes, wk.noSync); err != nil {
		return err
	}

	// disband
	disbandBytes := make([]byte, 1)
	disbandBytes[0] = wkutil.BoolToUint8(channelInfo.Disband)
	if err = w.Set(key.NewChannelInfoColumnKey(primaryKey, key.TableChannelInfo.Column.Disband), disbandBytes, wk.noSync); err != nil {
		return err
	}

	// channel index
	idBytes := make([]byte, 8)
	wk.endian.PutUint64(idBytes, primaryKey)
	if err = w.Set(key.NewChannelInfoIndexKey(channelInfo.ChannelId, channelInfo.ChannelType), idBytes, wk.noSync); err != nil {
		return err
	}

	// ban index
	if err = w.Set(key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.Ban, uint64(wkutil.BoolToInt(channelInfo.Ban)), primaryKey), nil, wk.noSync); err != nil {
		return err
	}

	// disband index
	if err = w.Set(key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.Disband, uint64(wkutil.BoolToInt(channelInfo.Disband)), primaryKey), nil, wk.noSync); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) iterChannelInfo(iter *pebble.Iterator, iterFnc func(channelInfo ChannelInfo) bool) error {
	var (
		preId          uint64
		preChannelInfo ChannelInfo
		lastNeedAppend bool = true
		hasData        bool = false
	)
	for iter.First(); iter.Valid(); iter.Next() {
		id, columnName, err := key.ParseChannelInfoColumnKey(iter.Key())
		if err != nil {
			return err
		}
		if id != preId {
			if preId != 0 {
				if !iterFnc(preChannelInfo) {
					lastNeedAppend = false
					break
				}
			}
			preId = id
			preChannelInfo = ChannelInfo{
				Id: id,
			}
		}

		switch columnName {
		case key.TableChannelInfo.Column.ChannelId:
			preChannelInfo.ChannelId = string(iter.Value())
		case key.TableChannelInfo.Column.ChannelType:
			preChannelInfo.ChannelType = iter.Value()[0]
		case key.TableChannelInfo.Column.Ban:
			preChannelInfo.Ban = wkutil.Uint8ToBool(iter.Value()[0])
		case key.TableChannelInfo.Column.Large:
			preChannelInfo.Large = wkutil.Uint8ToBool(iter.Value()[0])
		case key.TableChannelInfo.Column.Disband:
			preChannelInfo.Disband = wkutil.Uint8ToBool(iter.Value()[0])
		case key.TableChannelInfo.Column.SubscriberCount:
			preChannelInfo.SubscriberCount = int(wk.endian.Uint32(iter.Value()))
		case key.TableChannelInfo.Column.AllowlistCount:
			preChannelInfo.AllowlistCount = int(wk.endian.Uint32(iter.Value()))
		case key.TableChannelInfo.Column.DenylistCount:
			preChannelInfo.DenylistCount = int(wk.endian.Uint32(iter.Value()))

		}
		hasData = true
	}
	if lastNeedAppend && hasData {
		_ = iterFnc(preChannelInfo)
	}
	return nil
}

// func (wk *wukongDB) parseChannelInfo(iter *pebble.Iterator, limit int) ([]ChannelInfo, error) {

// 	var (
// 		channelInfos   = make([]ChannelInfo, 0, limit)
// 		preId          uint64
// 		preChannelInfo ChannelInfo
// 		lastNeedAppend bool = true
// 		hasData        bool = false
// 	)
// 	for iter.First(); iter.Valid(); iter.Next() {
// 		id, columnName, err := key.ParseChannelInfoColumnKey(iter.Key())
// 		if err != nil {
// 			return nil, err
// 		}
// 		if id != preId {
// 			if preId != 0 {

// 				channelInfos = append(channelInfos, preChannelInfo)
// 				if limit != 0 && len(channelInfos) >= limit {
// 					lastNeedAppend = false
// 					break
// 				}
// 			}
// 			preId = id
// 			preChannelInfo = ChannelInfo{}
// 		}

// 		switch columnName {
// 		case key.TableChannelInfo.Column.ChannelId:
// 			preChannelInfo.ChannelId = string(iter.Value())
// 		case key.TableChannelInfo.Column.ChannelType:
// 			preChannelInfo.ChannelType = iter.Value()[0]
// 		case key.TableChannelInfo.Column.Ban:
// 			preChannelInfo.Ban = wkutil.Uint8ToBool(iter.Value()[0])
// 		case key.TableChannelInfo.Column.Large:
// 			preChannelInfo.Large = wkutil.Uint8ToBool(iter.Value()[0])
// 		case key.TableChannelInfo.Column.Disband:
// 			preChannelInfo.Disband = wkutil.Uint8ToBool(iter.Value()[0])

// 		}
// 		hasData = true
// 	}
// 	if lastNeedAppend && hasData {
// 		channelInfos = append(channelInfos, preChannelInfo)
// 	}
// 	return channelInfos, nil
// }

func (wk *wukongDB) getChannelPrimaryKey(channelId string, channelType uint8) (uint64, error) {
	primaryKey := key.NewChannelInfoIndexKey(channelId, channelType)
	indexValue, closer, err := wk.channelDb(channelId, channelType).Get(primaryKey)
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}
	defer closer.Close()

	if len(indexValue) == 0 {
		return 0, nil
	}
	return wk.endian.Uint64(indexValue), nil
}

func (wk *wukongDB) writeSubscriber(channelId string, channelType uint8, id uint64, uid string, w pebble.Writer) error {
	var (
		err error
	)
	// uid
	if err = w.Set(key.NewSubscriberColumnKey(channelId, channelType, id, key.TableUser.Column.Uid), []byte(uid), wk.sync); err != nil {
		return err
	}

	// uid index
	idBytes := make([]byte, 8)
	wk.endian.PutUint64(idBytes, id)
	if err = w.Set(key.NewSubscriberIndexUidKey(channelId, channelType, uid), idBytes, wk.sync); err != nil {
		return err
	}

	return nil
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

// func (wk *wukongDB) parseAllowlist(iter *pebble.Iterator, limit int) ([]string, error) {

// 	var (
// 		uids           = make([]string, 0, limit)
// 		preId          uint64
// 		preUid         string
// 		lastNeedAppend bool = true
// 		hasData        bool = false
// 	)
// 	for iter.First(); iter.Valid(); iter.Next() {
// 		id, columnName, err := key.ParseAllowlistColumnKey(iter.Key())
// 		if err != nil {
// 			return nil, err
// 		}
// 		if id != preId {
// 			if preId != 0 {
// 				uids = append(uids, preUid)
// 				if limit != 0 && len(uids) >= limit {
// 					lastNeedAppend = false
// 					break
// 				}
// 			}
// 			preId = id
// 			preUid = ""
// 		}
// 		if columnName == key.TableAllowlist.Column.Uid {
// 			preUid = string(iter.Value())

// 		}
// 		hasData = true
// 	}
// 	if lastNeedAppend && hasData {
// 		uids = append(uids, preUid)
// 	}
// 	return uids, nil
// }
