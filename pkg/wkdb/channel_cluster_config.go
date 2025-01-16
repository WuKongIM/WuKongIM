package wkdb

import (
	"encoding/binary"
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
	"go.uber.org/zap"
)

func (wk *wukongDB) SaveChannelClusterConfig(channelClusterConfig ChannelClusterConfig) error {

	wk.metrics.SaveChannelClusterConfigAdd(1)

	if wk.opts.EnableCost {
		start := time.Now()
		defer func() {
			end := time.Since(start)
			if end > time.Millisecond*500 {
				wk.Warn("save channel cluster config", zap.Duration("cost", time.Since(start)), zap.String("channelId", channelClusterConfig.ChannelId), zap.Uint8("channelType", channelClusterConfig.ChannelType))
			}
		}()
	}

	wk.dblock.channelClusterConfig.lockByChannel(channelClusterConfig.ChannelId, channelClusterConfig.ChannelType)
	defer wk.dblock.channelClusterConfig.unlockByChannel(channelClusterConfig.ChannelId, channelClusterConfig.ChannelType)

	primaryKey := key.ChannelToNum(channelClusterConfig.ChannelId, channelClusterConfig.ChannelType)

	// oldChannelClusterConfig, err := wk.getChannelClusterConfigById(primaryKey)
	// if err != nil && err != ErrNotFound {
	// 	return err
	// }

	db := wk.defaultShardBatchDB()

	batch := db.NewBatch()

	// existConfig := !IsEmptyChannelClusterConfig(oldChannelClusterConfig)

	// // 删除旧的索引
	// if existConfig {
	// 	oldChannelClusterConfig.CreatedAt = nil // 旧的创建时间不参与索引
	// 	err = wk.deleteChannelClusterConfigIndex(oldChannelClusterConfig.Id, oldChannelClusterConfig, batch)
	// 	if err != nil {
	// 		wk.Error("delete channel cluster config index error", zap.Error(err), zap.String("channelId", channelClusterConfig.ChannelId), zap.Uint8("channelType", channelClusterConfig.ChannelType))
	// 		return err
	// 	}
	// }

	channelClusterConfig.Id = primaryKey

	// if existConfig {
	// 	channelClusterConfig.CreatedAt = nil // 创建时间不参与更新
	// }

	if err := wk.writeChannelClusterConfig(primaryKey, channelClusterConfig, batch); err != nil {
		return err
	}

	return batch.CommitWait()
}

func (wk *wukongDB) SaveChannelClusterConfigs(channelClusterConfigs []ChannelClusterConfig) error {

	if wk.opts.EnableCost {
		start := time.Now()
		defer func() {
			end := time.Since(start)
			if end > time.Millisecond*500 {
				wk.Warn("SaveChannelClusterConfigs too cost", zap.Duration("cost", time.Since(start)), zap.Int("count", len(channelClusterConfigs)))
			}
		}()
	}

	wk.metrics.SaveChannelClusterConfigsAdd(1)

	db := wk.defaultShardBatchDB()
	// batch := db.NewBatch()
	// 先删除旧的

	// for i, cfg := range channelClusterConfigs {
	// 	primaryKey := key.ChannelToNum(cfg.ChannelId, cfg.ChannelType)
	// 	oldChannelClusterConfig, err := wk.getChannelClusterConfigById(primaryKey)
	// 	if err != nil && err != ErrNotFound {
	// 		return err
	// 	}
	// 	existConfig := !IsEmptyChannelClusterConfig(oldChannelClusterConfig)

	// 	// 删除旧的索引
	// 	if existConfig {
	// 		oldChannelClusterConfig.CreatedAt = nil // 旧的创建时间不参与索引
	// 		err = wk.deleteChannelClusterConfigIndex(oldChannelClusterConfig.Id, oldChannelClusterConfig, batch)
	// 		if err != nil {
	// 			wk.Error("delete channel cluster config index error", zap.Error(err), zap.String("channelId", cfg.ChannelId), zap.Uint8("channelType", cfg.ChannelType))
	// 			return err
	// 		}
	// 	}
	// 	channelClusterConfigs[i].Id = primaryKey
	// 	if existConfig {
	// 		channelClusterConfigs[i].CreatedAt = nil // 创建时间不参与更新
	// 	}
	// }

	// if err := batch.CommitWait(); err != nil {
	// 	return err
	// }
	// 再添加新的
	batch := db.NewBatch()
	for _, cfg := range channelClusterConfigs {
		primaryKey := key.ChannelToNum(cfg.ChannelId, cfg.ChannelType)
		if err := wk.writeChannelClusterConfig(primaryKey, cfg, batch); err != nil {
			return err
		}
	}
	return batch.CommitWait()
}

func (wk *wukongDB) GetChannelClusterConfig(channelId string, channelType uint8) (ChannelClusterConfig, error) {

	wk.metrics.GetChannelClusterConfigAdd(1)

	start := time.Now()
	defer func() {
		end := time.Since(start)
		if end > time.Millisecond*200 {
			wk.Warn("get channel cluster config cost to long", zap.Duration("cost", end), zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
		}
	}()

	primaryKey := key.ChannelToNum(channelId, channelType)

	return wk.getChannelClusterConfigById(primaryKey)
}

func (wk *wukongDB) getChannelClusterConfigById(id uint64) (ChannelClusterConfig, error) {
	iter := wk.defaultShardDB().NewIter(&pebble.IterOptions{
		LowerBound: key.NewChannelClusterConfigColumnKey(id, key.MinColumnKey),
		UpperBound: key.NewChannelClusterConfigColumnKey(id, key.MaxColumnKey),
	})
	defer iter.Close()

	var resultCfg ChannelClusterConfig = EmptyChannelClusterConfig
	err := wk.iteratorChannelClusterConfig(iter, func(cfg ChannelClusterConfig) bool {
		resultCfg = cfg
		return false
	})

	if err != nil {
		return EmptyChannelClusterConfig, err
	}

	if IsEmptyChannelClusterConfig(resultCfg) {
		return EmptyChannelClusterConfig, ErrNotFound
	}
	resultCfg.Id = id

	return resultCfg, nil
}

func (wk *wukongDB) GetChannelClusterConfigVersion(channelId string, channelType uint8) (uint64, error) {
	primaryKey := key.ChannelToNum(channelId, channelType)
	result, closer, err := wk.defaultShardDB().Get(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.ConfVersion))
	if err != nil {
		return 0, err
	}
	defer closer.Close()
	if len(result) == 0 {
		return 0, nil
	}
	return binary.BigEndian.Uint64(result), nil
}

// func (wk *wukongDB) DeleteChannelClusterConfig(channelId string, channelType uint8) error {

// 	primaryKey := key.channelToNum(channelId, channelType)

// 	cfg, err := wk.getChannelClusterConfigById(primaryKey)
// 	if err != nil {
// 		return err
// 	}
// 	batch := wk.defaultShardDB().NewIndexedBatch()
// 	defer batch.Close()
// 	err = wk.deleteChannelClusterConfig(cfg, batch)
// 	if err != nil {
// 		return err
// 	}
// 	return batch.Commit(wk.sync)
// }

func (wk *wukongDB) GetChannelClusterConfigs(offsetId uint64, limit int) ([]ChannelClusterConfig, error) {

	wk.metrics.GetChannelClusterConfigsAdd(1)

	iter := wk.defaultShardDB().NewIter(&pebble.IterOptions{
		LowerBound: key.NewChannelClusterConfigColumnKey(offsetId+1, key.MinColumnKey),
		UpperBound: key.NewChannelClusterConfigColumnKey(math.MaxUint64, key.MaxColumnKey),
	})
	defer iter.Close()

	results := make([]ChannelClusterConfig, 0)
	err := wk.iteratorChannelClusterConfig(iter, func(cfg ChannelClusterConfig) bool {
		if len(results) >= limit {
			return false
		}
		if cfg.Id > offsetId {
			results = append(results, cfg)
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	return results, nil

}

func (wk *wukongDB) SearchChannelClusterConfig(req ChannelClusterConfigSearchReq, filter ...func(cfg ChannelClusterConfig) bool) ([]ChannelClusterConfig, error) {

	wk.metrics.SearchChannelClusterConfigAdd(1)

	if req.ChannelId != "" {
		cfg, err := wk.GetChannelClusterConfig(req.ChannelId, req.ChannelType)
		if err != nil {
			if err == pebble.ErrNotFound {
				return nil, nil
			}
			return nil, err
		}
		if len(filter) > 0 {
			for _, fnc := range filter {
				v := fnc(cfg)
				if !v {
					return nil, nil
				}
			}
		}
		return []ChannelClusterConfig{cfg}, nil
	}

	iterFnc := func(cfgs *[]ChannelClusterConfig) func(cfg ChannelClusterConfig) bool {
		currentSize := 0
		return func(cfg ChannelClusterConfig) bool {
			if req.Pre {
				if req.OffsetCreatedAt > 0 && cfg.CreatedAt != nil && cfg.CreatedAt.UnixNano() <= req.OffsetCreatedAt {
					return false
				}
			} else {
				if req.OffsetCreatedAt > 0 && cfg.CreatedAt != nil && cfg.CreatedAt.UnixNano() >= req.OffsetCreatedAt {
					return false
				}
			}

			if currentSize > req.Limit {
				return false
			}

			if len(filter) > 0 {
				for _, fnc := range filter {
					v := fnc(cfg)
					if !v {
						return true
					}
				}
			}

			currentSize++
			*cfgs = append(*cfgs, cfg)

			return true
		}
	}

	cfgs := make([]ChannelClusterConfig, 0, req.Limit)
	fnc := iterFnc(&cfgs)

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

	db := wk.defaultShardDB()

	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewChannelClusterConfigSecondIndexKey(key.TableChannelClusterConfig.SecondIndex.CreatedAt, start, 0),
		UpperBound: key.NewChannelClusterConfigSecondIndexKey(key.TableChannelClusterConfig.SecondIndex.CreatedAt, end, 0),
	})
	defer iter.Close()

	var iterStepFnc func() bool
	if req.Pre {
		if !iter.First() {
			return nil, nil
		}
		iterStepFnc = iter.Next
	} else {
		if !iter.Last() {
			return nil, nil
		}
		iterStepFnc = iter.Prev
	}
	for ; iter.Valid(); iterStepFnc() {
		_, id, err := key.ParseChannelClusterConfigSecondIndexKey(iter.Key())
		if err != nil {
			return nil, err
		}

		dataIter := db.NewIter(&pebble.IterOptions{
			LowerBound: key.NewChannelClusterConfigColumnKey(id, key.MinColumnKey),
			UpperBound: key.NewChannelClusterConfigColumnKey(id, key.MaxColumnKey),
		})
		defer dataIter.Close()

		var c ChannelClusterConfig
		err = wk.iteratorChannelClusterConfig(dataIter, func(cfg ChannelClusterConfig) bool {
			c = cfg
			return false
		})
		if err != nil {
			return nil, err
		}
		if !fnc(c) {
			break
		}
	}

	return cfgs, nil
}

func (wk *wukongDB) GetChannelClusterConfigCountWithSlotId(slotId uint32) (int, error) {

	wk.metrics.GetChannelClusterConfigCountWithSlotIdAdd(1)

	cfgs, err := wk.GetChannelClusterConfigWithSlotId(slotId)
	if err != nil {
		return 0, err
	}
	return len(cfgs), nil
}

func (wk *wukongDB) GetChannelClusterConfigWithSlotId(slotId uint32) ([]ChannelClusterConfig, error) {

	wk.metrics.GetChannelClusterConfigWithSlotIdAdd(1)

	iter := wk.defaultShardDB().NewIter(&pebble.IterOptions{
		LowerBound: key.NewChannelClusterConfigColumnKey(0, key.MinColumnKey),
		UpperBound: key.NewChannelClusterConfigColumnKey(math.MaxUint64, key.MaxColumnKey),
	})
	defer iter.Close()

	results := make([]ChannelClusterConfig, 0)
	err := wk.iteratorChannelClusterConfig(iter, func(cfg ChannelClusterConfig) bool {
		resultSlotId := wk.channelSlotId(cfg.ChannelId)
		if slotId == resultSlotId {
			results = append(results, cfg)
		}
		return true
	})
	if err != nil {
		return nil, err
	}

	return results, nil
}

// func (wk *wukongDB) deleteChannelClusterConfig(channelClusterConfig ChannelClusterConfig, w *Batch) error {

// 	// delete channel cluster config
// 	w.DeleteRange(key.NewChannelClusterConfigColumnKey(channelClusterConfig.Id, key.MinColumnKey), key.NewChannelClusterConfigColumnKey(channelClusterConfig.Id, key.MaxColumnKey))

// 	// delete index
// 	err := wk.deleteChannelClusterConfigIndex(channelClusterConfig.Id, channelClusterConfig, w)
// 	if err != nil {
// 		return err
// 	}

// 	return nil
// }

// func (wk *wukongDB) deleteChannelClusterConfigLeaderIndex(id uint64, w *pebble.Batch) error {

// 	leaderValue, closer, err := w.Get(key.NewChannelClusterConfigColumnKey(id, key.TableChannelClusterConfig.Column.LeaderId))
// 	if err != nil && err != pebble.ErrNotFound {
// 		return err
// 	}
// 	if closer != nil {
// 		defer closer.Close()
// 	}

// 	if len(leaderValue) == 0 {
// 		return nil
// 	}

// 	leaderId := wk.endian.Uint64(leaderValue)

// 	// delete old leader second index
// 	err = w.Delete(key.NewChannelClusterConfigSecondIndexKey(key.TableChannelClusterConfig.OtherIndex.LeaderId, leaderId, id), wk.noSync)
// 	if err != nil {
// 		return err
// 	}
// 	return nil
// }

func (wk *wukongDB) writeChannelClusterConfig(primaryKey uint64, channelClusterConfig ChannelClusterConfig, w *Batch) error {

	// channelId
	w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.ChannelId), []byte(channelClusterConfig.ChannelId))

	// channelType
	w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.ChannelType), []byte{channelClusterConfig.ChannelType})

	// replicaMaxCount
	var replicaMaxCountBytes = make([]byte, 2)
	binary.BigEndian.PutUint16(replicaMaxCountBytes, channelClusterConfig.ReplicaMaxCount)
	w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.ReplicaMaxCount), replicaMaxCountBytes)

	// replicas
	var replicasBytes = make([]byte, 8*len(channelClusterConfig.Replicas))
	for i, replica := range channelClusterConfig.Replicas {
		binary.BigEndian.PutUint64(replicasBytes[i*8:], replica)
	}
	w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.Replicas), replicasBytes)

	// learners
	var learnersBytes = make([]byte, 8*len(channelClusterConfig.Learners))
	for i, learner := range channelClusterConfig.Learners {
		binary.BigEndian.PutUint64(learnersBytes[i*8:], learner)
	}
	w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.Learners), learnersBytes)

	// leaderId
	leaderIdBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(leaderIdBytes, channelClusterConfig.LeaderId)
	w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.LeaderId), leaderIdBytes)

	// term
	termBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(termBytes, channelClusterConfig.Term)
	w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.Term), termBytes)

	// migrateFrom
	migrateFromBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(migrateFromBytes, channelClusterConfig.MigrateFrom)
	w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.MigrateFrom), migrateFromBytes)

	// migrateTo
	migrateToBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(migrateToBytes, channelClusterConfig.MigrateTo)
	w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.MigrateTo), migrateToBytes)

	// status
	statusBytes := make([]byte, 1)
	statusBytes[0] = uint8(channelClusterConfig.Status)
	w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.Status), statusBytes)

	// config version
	configVersionBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(configVersionBytes, channelClusterConfig.ConfVersion)
	w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.ConfVersion), configVersionBytes)

	//version
	versionBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(versionBytes, channelClusterConfig.version)
	w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.Version), versionBytes)

	if channelClusterConfig.CreatedAt != nil {
		// createdAt
		ct := uint64(channelClusterConfig.CreatedAt.UnixNano())
		var createdAtBytes = make([]byte, 8)
		binary.BigEndian.PutUint64(createdAtBytes, ct)
		w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.CreatedAt), createdAtBytes)
	}

	if channelClusterConfig.UpdatedAt != nil {
		// updatedAt
		up := uint64(channelClusterConfig.UpdatedAt.UnixNano())
		var updatedAtBytes = make([]byte, 8)
		binary.BigEndian.PutUint64(updatedAtBytes, up)
		w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.UpdatedAt), updatedAtBytes)
	}

	// write index
	if err := wk.writeChannelClusterConfigIndex(primaryKey, channelClusterConfig, w); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) writeChannelClusterConfigIndex(primaryKey uint64, channelClusterConfig ChannelClusterConfig, w *Batch) error {

	// channel index
	primaryKeyBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(primaryKeyBytes, primaryKey)
	w.Set(key.NewChannelClusterConfigIndexKey(channelClusterConfig.ChannelId, channelClusterConfig.ChannelType), primaryKeyBytes)

	// leader second index
	w.Set(key.NewChannelClusterConfigSecondIndexKey(key.TableChannelClusterConfig.SecondIndex.LeaderId, channelClusterConfig.LeaderId, primaryKey), nil)
	if channelClusterConfig.CreatedAt != nil {
		ct := uint64(channelClusterConfig.CreatedAt.UnixNano())
		// createdAt second index
		w.Set(key.NewChannelClusterConfigSecondIndexKey(key.TableChannelClusterConfig.SecondIndex.CreatedAt, ct, primaryKey), nil)
	}

	if channelClusterConfig.UpdatedAt != nil {
		up := uint64(channelClusterConfig.UpdatedAt.UnixNano())
		// updatedAt second index
		w.Set(key.NewChannelClusterConfigSecondIndexKey(key.TableChannelClusterConfig.SecondIndex.UpdatedAt, up, primaryKey), nil)
	}
	return nil
}

func (wk *wukongDB) deleteChannelClusterConfigIndex(primaryKey uint64, channelClusterConfig ChannelClusterConfig, w *Batch) error {

	// channel index
	w.Delete(key.NewChannelClusterConfigIndexKey(channelClusterConfig.ChannelId, channelClusterConfig.ChannelType))

	// leader second index
	w.Delete(key.NewChannelClusterConfigSecondIndexKey(key.TableChannelClusterConfig.SecondIndex.LeaderId, channelClusterConfig.LeaderId, primaryKey))

	if channelClusterConfig.CreatedAt != nil {
		ct := uint64(channelClusterConfig.CreatedAt.UnixNano())
		// createdAt second index
		w.Delete(key.NewChannelClusterConfigSecondIndexKey(key.TableChannelClusterConfig.SecondIndex.CreatedAt, ct, primaryKey))
	}

	if channelClusterConfig.UpdatedAt != nil {
		up := uint64(channelClusterConfig.UpdatedAt.UnixNano())
		// updatedAt second index
		w.Delete(key.NewChannelClusterConfigSecondIndexKey(key.TableChannelClusterConfig.SecondIndex.UpdatedAt, up, primaryKey))
	}

	return nil
}

func (wk *wukongDB) iteratorChannelClusterConfig(iter *pebble.Iterator, iterFnc func(cfg ChannelClusterConfig) bool) error {

	var (
		preId                   uint64
		preChannelClusterConfig ChannelClusterConfig
		lastNeedAppend          bool = true
		hasData                 bool = false
	)
	for iter.First(); iter.Valid(); iter.Next() {

		id, columnName, err := key.ParseChannelClusterConfigColumnKey(iter.Key())
		if err != nil {
			return err
		}

		if id != preId {
			if preId != 0 {
				if !iterFnc(preChannelClusterConfig) {
					lastNeedAppend = false
					break
				}

			}
			preId = id
			preChannelClusterConfig = ChannelClusterConfig{
				Id: id,
			}
		}

		switch columnName {
		case key.TableChannelClusterConfig.Column.ChannelId:
			preChannelClusterConfig.ChannelId = string(iter.Value())
		case key.TableChannelClusterConfig.Column.ChannelType:
			preChannelClusterConfig.ChannelType = iter.Value()[0]
		case key.TableChannelClusterConfig.Column.ReplicaMaxCount:
			preChannelClusterConfig.ReplicaMaxCount = wk.endian.Uint16(iter.Value())
		case key.TableChannelClusterConfig.Column.Replicas:
			replicas := make([]uint64, len(iter.Value())/8)
			for i := 0; i < len(replicas); i++ {
				replicas[i] = wk.endian.Uint64(iter.Value()[i*8:])
			}
			preChannelClusterConfig.Replicas = replicas
		case key.TableChannelClusterConfig.Column.Learners:
			learners := make([]uint64, len(iter.Value())/8)
			for i := 0; i < len(learners); i++ {
				learners[i] = wk.endian.Uint64(iter.Value()[i*8:])
			}
			preChannelClusterConfig.Learners = learners
		case key.TableChannelClusterConfig.Column.LeaderId:
			preChannelClusterConfig.LeaderId = wk.endian.Uint64(iter.Value())
		case key.TableChannelClusterConfig.Column.Term:
			preChannelClusterConfig.Term = wk.endian.Uint32(iter.Value())
		case key.TableChannelClusterConfig.Column.MigrateFrom:
			preChannelClusterConfig.MigrateFrom = wk.endian.Uint64(iter.Value())
		case key.TableChannelClusterConfig.Column.MigrateTo:
			preChannelClusterConfig.MigrateTo = wk.endian.Uint64(iter.Value())
		case key.TableChannelClusterConfig.Column.Status:
			preChannelClusterConfig.Status = ChannelClusterStatus(iter.Value()[0])
		case key.TableChannelClusterConfig.Column.ConfVersion:
			preChannelClusterConfig.ConfVersion = wk.endian.Uint64(iter.Value())
		case key.TableChannelClusterConfig.Column.Version:
			preChannelClusterConfig.version = wk.endian.Uint16(iter.Value())
		case key.TableChannelClusterConfig.Column.CreatedAt:
			tm := int64(wk.endian.Uint64(iter.Value()))
			if tm > 0 {
				t := time.Unix(tm/1e9, tm%1e9)
				preChannelClusterConfig.CreatedAt = &t
			}
		case key.TableChannelClusterConfig.Column.UpdatedAt:
			tm := int64(wk.endian.Uint64(iter.Value()))
			if tm > 0 {
				t := time.Unix(tm/1e9, tm%1e9)
				preChannelClusterConfig.UpdatedAt = &t
			}
		}
		hasData = true
	}
	if lastNeedAppend && hasData {
		_ = iterFnc(preChannelClusterConfig)
	}
	return nil
}
