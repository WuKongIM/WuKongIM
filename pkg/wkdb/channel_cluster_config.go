package wkdb

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
	"go.uber.org/zap"
)

func (wk *wukongDB) SaveChannelClusterConfig(channelClusterConfig ChannelClusterConfig) error {

	if wk.opts.EnableCost {
		start := time.Now()
		defer func() {
			wk.Debug("save channel cluster config", zap.Duration("cost", time.Since(start)), zap.String("channelId", channelClusterConfig.ChannelId), zap.Uint8("channelType", channelClusterConfig.ChannelType))
		}()
	}

	wk.dblock.channelClusterConfig.lockByChannel(channelClusterConfig.ChannelId, channelClusterConfig.ChannelType)
	defer wk.dblock.channelClusterConfig.unlockByChannel(channelClusterConfig.ChannelId, channelClusterConfig.ChannelType)

	primaryKey, err := wk.getChannelClusterConfigPrimaryKey(channelClusterConfig.ChannelId, channelClusterConfig.ChannelType)
	if err != nil {
		return err
	}
	if primaryKey == 0 {
		primaryKey = uint64(wk.prmaryKeyGen.Generate().Int64())
	}
	db := wk.defaultShardDB()

	batch := db.NewBatch()
	defer batch.Close()
	if err := wk.writeChannelClusterConfig(primaryKey, channelClusterConfig, batch); err != nil {
		return err
	}
	return batch.Commit(wk.sync)
}

func (wk *wukongDB) GetChannelClusterConfig(channelId string, channelType uint8) (ChannelClusterConfig, error) {

	primaryKey, err := wk.getChannelClusterConfigPrimaryKey(channelId, channelType)
	if err != nil {
		return EmptyChannelClusterConfig, err
	}
	if primaryKey == 0 {
		return EmptyChannelClusterConfig, nil
	}

	iter := wk.defaultShardDB().NewIter(&pebble.IterOptions{
		LowerBound: key.NewChannelClusterConfigColumnKey(primaryKey, [2]byte{0x00, 0x00}),
		UpperBound: key.NewChannelClusterConfigColumnKey(primaryKey, [2]byte{0xff, 0xff}),
	})
	defer iter.Close()
	clusterConfigs, err := wk.parseChannelClusterConfig(iter, 1, false, 0)
	if err != nil {
		return EmptyChannelClusterConfig, err
	}
	if len(clusterConfigs) == 0 {
		return EmptyChannelClusterConfig, nil
	}
	return clusterConfigs[0], nil
}

func (wk *wukongDB) GetChannelClusterConfigVersion(channelId string, channelType uint8) (uint64, error) {
	primaryKey, err := wk.getChannelClusterConfigPrimaryKey(channelId, channelType)
	if err != nil {
		return 0, err
	}
	if primaryKey == 0 {
		return 0, nil
	}
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

func (wk *wukongDB) DeleteChannelClusterConfig(channelId string, channelType uint8) error {

	primaryKey, err := wk.getChannelClusterConfigPrimaryKey(channelId, channelType)
	if err != nil {
		return err
	}

	if primaryKey == 0 {
		return nil
	}
	return wk.defaultShardDB().DeleteRange(key.NewChannelClusterConfigColumnKey(primaryKey, [2]byte{0x00, 0x00}), key.NewChannelClusterConfigColumnKey(primaryKey, [2]byte{0xff, 0xff}), wk.sync)
}

func (wk *wukongDB) GetChannelClusterConfigs(offsetId uint64, limit int) ([]ChannelClusterConfig, error) {

	iter := wk.defaultShardDB().NewIter(&pebble.IterOptions{
		LowerBound: key.NewChannelClusterConfigColumnKey(offsetId+1, [2]byte{0x00, 0x00}),
		UpperBound: key.NewChannelClusterConfigColumnKey(math.MaxUint64, [2]byte{0xff, 0xff}),
	})
	defer iter.Close()

	return wk.parseChannelClusterConfig(iter, limit, false, 0)
}

func (wk *wukongDB) GetChannelClusterConfigCountWithSlotId(slotId uint32) (int, error) {

	cfgs, err := wk.GetChannelClusterConfigWithSlotId(slotId)
	if err != nil {
		return 0, err
	}
	return len(cfgs), nil
}

func (wk *wukongDB) GetChannelClusterConfigWithSlotId(slotId uint32) ([]ChannelClusterConfig, error) {
	iter := wk.defaultShardDB().NewIter(&pebble.IterOptions{
		LowerBound: key.NewChannelClusterConfigColumnKey(0, [2]byte{0x00, 0x00}),
		UpperBound: key.NewChannelClusterConfigColumnKey(math.MaxUint64, [2]byte{0xff, 0xff}),
	})
	defer iter.Close()

	cfgs, err := wk.parseChannelClusterConfig(iter, 0, true, slotId)
	if err != nil {
		return nil, err
	}
	return cfgs, nil
}

func (wk *wukongDB) getChannelClusterConfigPrimaryKey(channelId string, channelType uint8) (uint64, error) {
	primaryKey, closer, err := wk.defaultShardDB().Get(key.NewChannelClusterConfigIndexKey(channelId, channelType))
	if closer != nil {
		defer closer.Close()
	}
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}

	if len(primaryKey) == 0 {
		return 0, nil
	}
	return binary.BigEndian.Uint64(primaryKey), nil
}

func (wk *wukongDB) writeChannelClusterConfig(primaryKey uint64, channelClusterConfig ChannelClusterConfig, w pebble.Writer) error {

	// channelId
	if err := w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.ChannelId), []byte(channelClusterConfig.ChannelId), wk.noSync); err != nil {
		return err
	}

	// channelType
	if err := w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.ChannelType), []byte{channelClusterConfig.ChannelType}, wk.noSync); err != nil {
		return err
	}

	// replicaMaxCount
	var replicaMaxCountBytes = make([]byte, 2)
	binary.BigEndian.PutUint16(replicaMaxCountBytes, channelClusterConfig.ReplicaMaxCount)
	if err := w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.ReplicaMaxCount), replicaMaxCountBytes, wk.noSync); err != nil {
		return err
	}

	// replicas
	var replicasBytes = make([]byte, 8*len(channelClusterConfig.Replicas))
	for i, replica := range channelClusterConfig.Replicas {
		binary.BigEndian.PutUint64(replicasBytes[i*8:], replica)
	}
	if err := w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.Replicas), replicasBytes, wk.noSync); err != nil {
		return err
	}

	// learners
	var learnersBytes = make([]byte, 8*len(channelClusterConfig.Learners))
	for i, learner := range channelClusterConfig.Learners {
		binary.BigEndian.PutUint64(learnersBytes[i*8:], learner)
	}
	if err := w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.Learners), learnersBytes, wk.noSync); err != nil {
		return err
	}

	// leaderId
	leaderIdBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(leaderIdBytes, channelClusterConfig.LeaderId)
	if err := w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.LeaderId), leaderIdBytes, wk.noSync); err != nil {
		return err
	}

	// term
	termBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(termBytes, channelClusterConfig.Term)
	if err := w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.Term), termBytes, wk.noSync); err != nil {
		return err
	}

	// migrateFrom
	migrateFromBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(migrateFromBytes, channelClusterConfig.MigrateFrom)
	if err := w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.MigrateFrom), migrateFromBytes, wk.noSync); err != nil {
		return err
	}

	// migrateTo
	migrateToBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(migrateToBytes, channelClusterConfig.MigrateTo)
	if err := w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.MigrateTo), migrateToBytes, wk.noSync); err != nil {
		return err
	}

	// leaderTransferTo
	leaderTransferToBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(leaderTransferToBytes, channelClusterConfig.LeaderTransferTo)
	if err := w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.LeaderTransferTo), leaderTransferToBytes, wk.noSync); err != nil {
		return err
	}

	// status
	statusBytes := make([]byte, 1)
	statusBytes[0] = uint8(channelClusterConfig.Status)
	if err := w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.Status), statusBytes, wk.noSync); err != nil {
		return err
	}

	// config version
	configVersionBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(configVersionBytes, channelClusterConfig.ConfVersion)
	fmt.Println("channelClusterConfig.ConfVersion---->", channelClusterConfig.ConfVersion)
	if err := w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.ConfVersion), configVersionBytes, wk.noSync); err != nil {
		return err
	}

	//version
	versionBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(versionBytes, channelClusterConfig.version)
	if err := w.Set(key.NewChannelClusterConfigColumnKey(primaryKey, key.TableChannelClusterConfig.Column.Version), versionBytes, wk.noSync); err != nil {
		return err
	}

	// channel index
	primaryKeyBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(primaryKeyBytes, primaryKey)
	if err := w.Set(key.NewChannelClusterConfigIndexKey(channelClusterConfig.ChannelId, channelClusterConfig.ChannelType), primaryKeyBytes, wk.noSync); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) parseChannelClusterConfig(iter *pebble.Iterator, limit int, filterSlot bool, slotId uint32) ([]ChannelClusterConfig, error) {

	var (
		clusterConfigs          = make([]ChannelClusterConfig, 0, limit)
		preId                   uint64
		preChannelClusterConfig ChannelClusterConfig
		lastNeedAppend          bool = true
		hasData                 bool = false
	)
	for iter.First(); iter.Valid(); iter.Next() {

		id, columnName, err := key.ParseChannelClusterConfigColumnKey(iter.Key())
		if err != nil {
			return nil, err
		}

		if id != preId {
			if preId != 0 {
				if filterSlot {
					resultSlotId := wk.channelSlotId(preChannelClusterConfig.ChannelId, preChannelClusterConfig.ChannelType)
					if resultSlotId == slotId {
						clusterConfigs = append(clusterConfigs, preChannelClusterConfig)
						if limit != 0 && len(clusterConfigs) >= limit {
							lastNeedAppend = false
							break
						}
					}
				} else {
					clusterConfigs = append(clusterConfigs, preChannelClusterConfig)
					if limit != 0 && len(clusterConfigs) >= limit {
						lastNeedAppend = false
						break
					}
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
		case key.TableChannelClusterConfig.Column.LeaderTransferTo:
			preChannelClusterConfig.LeaderTransferTo = wk.endian.Uint64(iter.Value())
		case key.TableChannelClusterConfig.Column.Status:
			preChannelClusterConfig.Status = ChannelClusterStatus(iter.Value()[0])
		case key.TableChannelClusterConfig.Column.ConfVersion:
			preChannelClusterConfig.ConfVersion = wk.endian.Uint64(iter.Value())
		case key.TableChannelClusterConfig.Column.Version:
			preChannelClusterConfig.version = wk.endian.Uint16(iter.Value())
		}
		hasData = true
	}
	if lastNeedAppend && hasData {
		if filterSlot {
			resultSlotId := wk.channelSlotId(preChannelClusterConfig.ChannelId, preChannelClusterConfig.ChannelType)
			if resultSlotId == slotId {
				clusterConfigs = append(clusterConfigs, preChannelClusterConfig)
			}
		} else {
			clusterConfigs = append(clusterConfigs, preChannelClusterConfig)
		}
	}
	return clusterConfigs, nil
}
