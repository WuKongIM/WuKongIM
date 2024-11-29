package wkdb

import (
	"math"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/cockroachdb/pebble"
	"go.uber.org/zap"
)

func (wk *wukongDB) AddChannel(channelInfo ChannelInfo) (uint64, error) {

	wk.metrics.AddChannelAdd(1)

	if wk.opts.EnableCost {
		start := time.Now()
		defer func() {
			cost := time.Since(start)
			if cost.Milliseconds() > 200 {
				wk.Info("AddChannel done", zap.Duration("cost", cost), zap.String("channelId", channelInfo.ChannelId), zap.Uint8("channelType", channelInfo.ChannelType))
			}
		}()
	}

	primaryKey, err := wk.getChannelPrimaryKey(channelInfo.ChannelId, channelInfo.ChannelType)
	if err != nil {
		return 0, err
	}

	w := wk.channelDb(channelInfo.ChannelId, channelInfo.ChannelType).NewBatch()
	defer w.Close()

	if err := wk.writeChannelInfo(primaryKey, channelInfo, w); err != nil {
		return 0, err
	}

	// if isCreate {
	// 	err = wk.IncChannelCount(1)
	// 	if err != nil {
	// 		wk.Error("IncChannelCount failed", zap.Error(err))
	// 		return 0, err
	// 	}
	// }

	return primaryKey, w.Commit(wk.sync)
}

func (wk *wukongDB) UpdateChannel(channelInfo ChannelInfo) error {

	wk.metrics.UpdateChannelAdd(1)

	if wk.opts.EnableCost {
		start := time.Now()
		defer func() {
			cost := time.Since(start)
			if cost.Milliseconds() > 200 {
				wk.Info("UpdateChannel done", zap.Duration("cost", cost), zap.String("channelId", channelInfo.ChannelId), zap.Uint8("channelType", channelInfo.ChannelType))
			}
		}()
	}

	primaryKey, err := wk.getChannelPrimaryKey(channelInfo.ChannelId, channelInfo.ChannelType)
	if err != nil {
		return err
	}

	w := wk.channelDb(channelInfo.ChannelId, channelInfo.ChannelType).NewBatch()
	defer w.Close()

	oldChannelInfo, err := wk.GetChannel(channelInfo.ChannelId, channelInfo.ChannelType)
	if err != nil && err != ErrNotFound {
		return err
	}

	if !IsEmptyChannelInfo(oldChannelInfo) {
		// 更新操作不需要更新创建时间
		if oldChannelInfo.CreatedAt != nil {
			oldChannelInfo.CreatedAt = nil
		}
		// 删除旧索引
		if err := wk.deleteChannelInfoBaseIndex(oldChannelInfo, w); err != nil {
			return err
		}
	}

	if channelInfo.CreatedAt != nil {
		channelInfo.CreatedAt = nil
	}
	if err := wk.writeChannelInfo(primaryKey, channelInfo, w); err != nil {
		return err
	}

	return w.Commit(wk.sync)
}

func (wk *wukongDB) GetChannel(channelId string, channelType uint8) (ChannelInfo, error) {

	wk.metrics.GetChannelAdd(1)

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

	wk.metrics.SearchChannelsAdd(1)

	var channelInfos []ChannelInfo
	if req.ChannelId != "" && req.ChannelType != 0 {
		channelInfo, err := wk.GetChannel(req.ChannelId, req.ChannelType)
		if err != nil {
			return nil, err
		}
		channelInfos = append(channelInfos, channelInfo)
		return channelInfos, nil
	}

	iterFnc := func(channelInfos *[]ChannelInfo) func(channelInfo ChannelInfo) bool {
		currentSize := 0
		return func(channelInfo ChannelInfo) bool {
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
			if req.Pre {
				if req.OffsetCreatedAt > 0 && channelInfo.CreatedAt != nil && channelInfo.CreatedAt.UnixNano() <= req.OffsetCreatedAt {
					return false
				}
			} else {
				if req.OffsetCreatedAt > 0 && channelInfo.CreatedAt != nil && channelInfo.CreatedAt.UnixNano() >= req.OffsetCreatedAt {
					return false
				}
			}

			if currentSize > req.Limit {
				return false
			}
			currentSize++
			*channelInfos = append(*channelInfos, channelInfo)

			return true
		}
	}

	allChannelInfos := make([]ChannelInfo, 0, req.Limit*len(wk.dbs))
	for _, db := range wk.dbs {
		channelInfos := make([]ChannelInfo, 0, req.Limit)
		fnc := iterFnc(&channelInfos)
		// 通过索引查询
		has, err := wk.searchChannelsByIndex(req, db, fnc)
		if err != nil {
			return nil, err
		}
		if has { // 如果有触发索引，则无需全局查询
			allChannelInfos = append(allChannelInfos, channelInfos...)
			continue
		}

		start := uint64(req.OffsetCreatedAt)
		end := uint64(math.MaxUint64)
		if req.OffsetCreatedAt > 0 {
			if req.Pre {
				start = uint64(req.OffsetCreatedAt + 1)
				end = uint64(math.MaxUint64)
			} else {
				start = 0
				end = uint64(req.OffsetCreatedAt)
			}
		}

		iter := db.NewIter(&pebble.IterOptions{
			LowerBound: key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.CreatedAt, start, 0),
			UpperBound: key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.CreatedAt, end, 0),
		})
		defer iter.Close()

		var iterStepFnc func() bool
		if req.Pre {
			if !iter.First() {
				continue
			}
			iterStepFnc = iter.Next
		} else {
			if !iter.Last() {
				continue
			}
			iterStepFnc = iter.Prev
		}

		for ; iter.Valid(); iterStepFnc() {
			_, id, err := key.ParseChannelInfoSecondIndexKey(iter.Key())
			if err != nil {
				return nil, err
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
				return nil, err
			}
			if !fnc(ch) {
				break
			}
		}

		allChannelInfos = append(allChannelInfos, channelInfos...)
	}

	// 降序排序
	sort.Slice(allChannelInfos, func(i, j int) bool {
		return allChannelInfos[i].CreatedAt.UnixNano() > allChannelInfos[j].CreatedAt.UnixNano()
	})

	if req.Limit > 0 && len(allChannelInfos) > req.Limit {
		if req.Pre {
			allChannelInfos = allChannelInfos[len(allChannelInfos)-req.Limit:]
		} else {
			allChannelInfos = allChannelInfos[:req.Limit]
		}
	}

	var (
		count int
	)
	for i, result := range allChannelInfos {

		// 获取频道的最新消息序号
		lastMsgSeq, lastTime, err := wk.GetChannelLastMessageSeq(result.ChannelId, result.ChannelType)
		if err != nil {
			return nil, err
		}
		allChannelInfos[i].LastMsgSeq = lastMsgSeq
		allChannelInfos[i].LastMsgTime = lastTime

		// 订阅数量
		count, err = wk.GetSubscriberCount(result.ChannelId, result.ChannelType)
		if err != nil {
			return nil, err
		}
		allChannelInfos[i].SubscriberCount = count

	}

	return allChannelInfos, nil
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

	wk.metrics.ExistChannelAdd(1)

	channel, err := wk.GetChannel(channelId, channelType)
	if err != nil {
		if err == ErrNotFound {
			return false, nil
		}
		return false, err
	}
	return !IsEmptyChannelInfo(channel), nil
}

func (wk *wukongDB) DeleteChannel(channelId string, channelType uint8) error {

	wk.metrics.DeleteChannelAdd(1)

	id, err := wk.getChannelPrimaryKey(channelId, channelType)
	if err != nil {
		return err
	}
	if id == 0 {
		return nil
	}
	batch := wk.channelDb(channelId, channelType).NewBatch()
	defer batch.Close()

	// 删除数据
	err = batch.DeleteRange(key.NewChannelInfoColumnKey(id, key.MinColumnKey), key.NewChannelInfoColumnKey(id, key.MaxColumnKey), wk.noSync)
	if err != nil {
		return err
	}

	err = batch.DeleteRange(key.NewChannelInfoSecondIndexKey(key.MinColumnKey, 0, id), key.NewChannelInfoSecondIndexKey(key.MaxColumnKey, math.MaxUint64, id), wk.noSync)
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

	wk.metrics.UpdateChannelAppliedIndexAdd(1)

	indexBytes := make([]byte, 8)
	wk.endian.PutUint64(indexBytes, index)
	return wk.channelDb(channelId, channelType).Set(key.NewChannelCommonColumnKey(channelId, channelType, key.TableChannelCommon.Column.AppliedIndex), indexBytes, wk.sync)
}

func (wk *wukongDB) GetChannelAppliedIndex(channelId string, channelType uint8) (uint64, error) {

	wk.metrics.GetChannelAppliedIndexAdd(1)

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

// 增加频道属性数量 id为频道信息的唯一主键 count为math.MinInt 表示重置为0
func (wk *wukongDB) incChannelInfoColumnCount(id uint64, columnName, indexName [2]byte, count int, batch *pebble.Batch) error {
	countKey := key.NewChannelInfoColumnKey(id, columnName)
	if count == 0 { //
		return batch.Set(countKey, []byte{0x00, 0x00, 0x00, 0x00}, wk.noSync)
	}

	countBytes, closer, err := batch.Get(countKey)
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
	err = batch.Set(countKey, countBytes, wk.noSync)
	if err != nil {
		return err
	}
	// 设置数量对应的索引
	secondIndexKey := key.NewChannelInfoSecondIndexKey(indexName, uint64(count), id)
	err = batch.Set(secondIndexKey, nil, wk.noSync)
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

	// createdAt
	if channelInfo.CreatedAt != nil {
		ct := uint64(channelInfo.CreatedAt.UnixNano())
		createdAt := make([]byte, 8)
		wk.endian.PutUint64(createdAt, ct)
		if err = w.Set(key.NewChannelInfoColumnKey(primaryKey, key.TableChannelInfo.Column.CreatedAt), createdAt, wk.noSync); err != nil {
			return err
		}
	}

	if channelInfo.UpdatedAt != nil {
		// updatedAt
		updatedAt := make([]byte, 8)
		wk.endian.PutUint64(updatedAt, uint64(channelInfo.UpdatedAt.UnixNano()))
		if err = w.Set(key.NewChannelInfoColumnKey(primaryKey, key.TableChannelInfo.Column.UpdatedAt), updatedAt, wk.noSync); err != nil {
			return err
		}

	}

	// write index
	if err = wk.writeChannelInfoBaseIndex(channelInfo, w); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) writeChannelInfoBaseIndex(channelInfo ChannelInfo, w pebble.Writer) error {
	primaryKey, err := wk.getChannelPrimaryKey(channelInfo.ChannelId, channelInfo.ChannelType)
	if err != nil {
		return err
	}

	if channelInfo.CreatedAt != nil {
		// createdAt second index
		if err := w.Set(key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.CreatedAt, uint64(channelInfo.CreatedAt.UnixNano()), primaryKey), nil, wk.noSync); err != nil {
			return err
		}
	}

	if channelInfo.UpdatedAt != nil {
		// updatedAt second index
		if err := w.Set(key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.UpdatedAt, uint64(channelInfo.UpdatedAt.UnixNano()), primaryKey), nil, wk.noSync); err != nil {
			return err
		}
	}

	// channel index
	primaryKeyBytes := make([]byte, 8)
	wk.endian.PutUint64(primaryKeyBytes, primaryKey)
	if err = w.Set(key.NewChannelInfoIndexKey(key.TableChannelInfo.Index.Channel, key.ChannelToNum(channelInfo.ChannelId, channelInfo.ChannelType)), primaryKeyBytes, wk.noSync); err != nil {
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

func (wk *wukongDB) deleteChannelInfoBaseIndex(channelInfo ChannelInfo, w pebble.Writer) error {
	if channelInfo.CreatedAt != nil {
		// createdAt second index
		ct := uint64(channelInfo.CreatedAt.UnixNano())
		if err := w.Delete(key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.CreatedAt, ct, channelInfo.Id), wk.noSync); err != nil {
			return err
		}
	}

	if channelInfo.UpdatedAt != nil {
		// updatedAt second index
		if err := w.Delete(key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.UpdatedAt, uint64(channelInfo.UpdatedAt.UnixNano()), channelInfo.Id), wk.noSync); err != nil {
			return err
		}
	}

	// channel index
	if err := w.Delete(key.NewChannelInfoIndexKey(key.TableChannelInfo.Index.Channel, key.ChannelToNum(channelInfo.ChannelId, channelInfo.ChannelType)), wk.noSync); err != nil {
		return err
	}

	// ban index
	if err := w.Delete(key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.Ban, uint64(wkutil.BoolToInt(channelInfo.Ban)), channelInfo.Id), wk.noSync); err != nil {
		return err
	}

	// disband index
	if err := w.Delete(key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.Disband, uint64(wkutil.BoolToInt(channelInfo.Disband)), channelInfo.Id), wk.noSync); err != nil {
		return err
	}

	// // subscriberCount index
	// if err := w.Delete(key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.SubscriberCount, uint64(channelInfo.SubscriberCount), channelInfo.Id), wk.noSync); err != nil {
	// 	return err
	// }

	// // allowlistCount index
	// if err := w.Delete(key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.AllowlistCount, uint64(channelInfo.AllowlistCount), channelInfo.Id), wk.noSync); err != nil {
	// 	return err
	// }

	// // denylistCount index
	// if err := w.Delete(key.NewChannelInfoSecondIndexKey(key.TableChannelInfo.SecondIndex.DenylistCount, uint64(channelInfo.DenylistCount), channelInfo.Id), wk.noSync); err != nil {
	// 	return err
	// }
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
		case key.TableChannelInfo.Column.CreatedAt:
			tm := int64(wk.endian.Uint64(iter.Value()))
			if tm > 0 {
				t := time.Unix(tm/1e9, tm%1e9)
				preChannelInfo.CreatedAt = &t
			}

		case key.TableChannelInfo.Column.UpdatedAt:
			tm := int64(wk.endian.Uint64(iter.Value()))
			if tm > 0 {
				t := time.Unix(tm/1e9, tm%1e9)
				preChannelInfo.UpdatedAt = &t
			}
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
	// primaryKey := key.NewChannelInfoIndexKey(key.TableChannelInfo.Index.Channel, key.ChannelToNum(channelId, channelType))
	// indexValue, closer, err := wk.channelDb(channelId, channelType).Get(primaryKey)
	// if err != nil {
	// 	if err == pebble.ErrNotFound {
	// 		return 0, nil
	// 	}
	// 	return 0, err
	// }
	// defer closer.Close()

	// if len(indexValue) == 0 {
	// 	return 0, nil
	// }
	// return wk.endian.Uint64(indexValue), nil

	return key.ChannelToNum(channelId, channelType), nil

}
