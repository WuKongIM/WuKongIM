package cluster

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"path"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/key"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/cockroachdb/pebble"
	"go.uber.org/zap"
)

type PebbleShardLogStorage struct {
	db   *pebble.DB
	path string
	wo   *pebble.WriteOptions
	wklog.Log
}

func NewPebbleShardLogStorage(path string) *PebbleShardLogStorage {
	return &PebbleShardLogStorage{
		path: path,
		wo: &pebble.WriteOptions{
			Sync: true,
		},
		Log: wklog.NewWKLog(fmt.Sprintf("PebbleShardLogStorage[%s]", path)),
	}
}

func (p *PebbleShardLogStorage) Open() error {
	var err error
	p.db, err = pebble.Open(p.path, &pebble.Options{})
	if err != nil {
		return err
	}
	return nil
}

func (p *PebbleShardLogStorage) Close() error {
	err := p.db.Close()
	if err != nil {
		return err
	}
	return nil
}

// AppendLog 追加日志
func (p *PebbleShardLogStorage) AppendLog(shardNo string, logs []replica.Log) error {

	lastIndex, err := p.LastIndex(shardNo)
	if err != nil {
		return err
	}
	if logs[len(logs)-1].Index <= lastIndex {
		return nil
	}

	batch := p.db.NewBatch()
	for _, lg := range logs {
		if lg.Index <= lastIndex {
			continue
		}
		logData, err := lg.Marshal()
		if err != nil {
			return err
		}
		keyData := key.NewLogKey(shardNo, lg.Index)
		err = batch.Set(keyData, logData, p.wo)
		if err != nil {
			return err
		}
	}
	err = batch.Commit(p.wo)
	if err != nil {
		return err
	}

	return p.saveMaxIndex(shardNo, logs[len(logs)-1].Index)
}

// TruncateLogTo 截断日志
func (p *PebbleShardLogStorage) TruncateLogTo(shardNo string, index uint64) error {
	if index == 0 {
		return errors.New("index must be greater than 0")
	}
	if index > 0 {
		lastIndex, _, err := p.getMaxIndex(shardNo)
		if err != nil {
			p.Error("get max index error", zap.Error(err), zap.String("shardNo", shardNo))
			return err
		}
		if index > lastIndex {
			return nil
		}
	}
	keyData := key.NewLogKey(shardNo, index)
	maxKeyData := key.NewLogKey(shardNo, math.MaxUint64)
	err := p.db.DeleteRange(keyData, maxKeyData, p.wo)
	if err != nil {
		return err
	}
	if index > 0 {
		return p.saveMaxIndex(shardNo, index-1)
	}
	return p.saveMaxIndex(shardNo, 0)
}

// func (p *PebbleShardLogStorage) realLastIndex(shardNo string) (uint64, error) {
// 	iter := p.db.NewIter(&pebble.IterOptions{
// 		LowerBound: key.NewLogKey(shardNo, 0),
// 		UpperBound: key.NewLogKey(shardNo, math.MaxUint64),
// 	})
// 	defer iter.Close()
// 	for iter.Last(); iter.Valid(); iter.Next() {
// 		var log replica.Log
// 		err := log.Unmarshal(iter.Value())
// 		if err != nil {
// 			return 0, err
// 		}
// 		return log.Index, nil
// 	}
// 	return 0, nil
// }

func (p *PebbleShardLogStorage) Logs(shardNo string, startLogIndex uint64, endLogIndex uint64, limitSize uint64) ([]replica.Log, error) {

	lowKey := key.NewLogKey(shardNo, startLogIndex)
	if endLogIndex == 0 {
		endLogIndex = math.MaxUint64
	}
	highKey := key.NewLogKey(shardNo, endLogIndex)
	iter := p.db.NewIter(&pebble.IterOptions{
		LowerBound: lowKey,
		UpperBound: highKey,
	})
	defer iter.Close()
	var logs []replica.Log

	var size uint64
	for iter.First(); iter.Valid(); iter.Next() {
		var log replica.Log
		err := log.Unmarshal(iter.Value())
		if err != nil {
			return nil, err
		}
		logs = append(logs, log)
		size += uint64(log.LogSize())
		if size >= limitSize {
			break
		}
	}
	return logs, nil
}

func (p *PebbleShardLogStorage) LastIndex(shardNo string) (uint64, error) {
	lastIndex, _, err := p.getMaxIndex(shardNo)
	return lastIndex, err
}

func (p *PebbleShardLogStorage) LastIndexAndAppendTime(shardNo string) (uint64, uint64, error) {
	return p.getMaxIndex(shardNo)
}

func (p *PebbleShardLogStorage) SetLeaderTermStartIndex(shardNo string, term uint32, index uint64) error {
	leaderTermStartIndexKeyData := key.NewLeaderTermStartIndexKey(shardNo, term)

	var indexData = make([]byte, 8)
	binary.BigEndian.PutUint64(indexData, index)
	err := p.db.Set(leaderTermStartIndexKeyData, indexData, p.wo)
	return err
}

func (p *PebbleShardLogStorage) LeaderLastTerm(shardNo string) (uint32, error) {
	iter := p.db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewLeaderTermStartIndexKey(shardNo, 0),
		UpperBound: key.NewLeaderTermStartIndexKey(shardNo, math.MaxUint32),
	})
	defer iter.Close()
	var maxTerm uint32
	for iter.First(); iter.Valid(); iter.Next() {
		term := key.GetTermFromLeaderTermStartIndexKey(iter.Key())
		if term > maxTerm {
			maxTerm = term
		}
	}
	return maxTerm, nil
}

func (p *PebbleShardLogStorage) LeaderTermStartIndex(shardNo string, term uint32) (uint64, error) {
	leaderTermStartIndexKeyData := key.NewLeaderTermStartIndexKey(shardNo, term)
	leaderTermStartIndexData, closer, err := p.db.Get(leaderTermStartIndexKeyData)
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}
	defer closer.Close()
	if len(leaderTermStartIndexData) == 0 {
		return 0, nil
	}
	return binary.BigEndian.Uint64(leaderTermStartIndexData), nil
}

func (p *PebbleShardLogStorage) DeleteLeaderTermStartIndexGreaterThanTerm(shardNo string, term uint32) error {
	iter := p.db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewLeaderTermStartIndexKey(shardNo, term+1),
		UpperBound: key.NewLeaderTermStartIndexKey(shardNo, math.MaxUint32),
	})
	defer iter.Close()
	batch := p.db.NewBatch()
	var err error
	for iter.First(); iter.Valid(); iter.Next() {
		err = batch.Delete(iter.Key(), p.wo)
		if err != nil {
			return err
		}
	}
	return batch.Commit(p.wo)
}

func (p *PebbleShardLogStorage) saveMaxIndex(shardNo string, index uint64) error {
	maxIndexKeyData := key.NewMaxIndexKey(shardNo)
	maxIndexdata := make([]byte, 8)
	binary.BigEndian.PutUint64(maxIndexdata, index)
	lastTime := time.Now().UnixNano()
	lastTimeData := make([]byte, 8)
	binary.BigEndian.PutUint64(lastTimeData, uint64(lastTime))

	err := p.db.Set(maxIndexKeyData, append(maxIndexdata, lastTimeData...), p.wo)
	return err
}

// GetMaxIndex 获取最大的index 和最后一次写入的时间
func (p *PebbleShardLogStorage) getMaxIndex(shardNo string) (uint64, uint64, error) {
	maxIndexKeyData := key.NewMaxIndexKey(shardNo)
	maxIndexdata, closer, err := p.db.Get(maxIndexKeyData)
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	defer closer.Close()
	if len(maxIndexdata) == 0 {
		return 0, 0, nil
	}
	return binary.BigEndian.Uint64(maxIndexdata[:8]), binary.BigEndian.Uint64(maxIndexdata[8:]), nil
}

type localStorage struct {
	db    *pebble.DB
	dbDir string
	wklog.Log
	wo   *pebble.WriteOptions
	opts *Options
}

func newLocalStorage(opts *Options) *localStorage {
	dbDir := path.Join(opts.DataDir, "wukongimdb")
	return &localStorage{
		opts:  opts,
		dbDir: dbDir,
		Log:   wklog.NewWKLog(fmt.Sprintf("localStorage[%s]", dbDir)),
		wo: &pebble.WriteOptions{
			Sync: true,
		},
	}
}

func (l *localStorage) open() error {
	var err error
	l.db, err = pebble.Open(l.dbDir, &pebble.Options{})
	return err
}

func (l *localStorage) close() error {
	return l.db.Close()
}

// func (l *localStorage) saveChannelClusterConfig(channelID string, channelType uint8, clusterInfo *ChannelClusterConfig) error {
// 	key := l.getChannelClusterConfigKey(channelID, channelType)
// 	data, err := clusterInfo.Marshal()
// 	if err != nil {
// 		return err
// 	}
// 	return l.db.Set(key, data, l.wo)
// }

// func (l *localStorage) deleteChannelClusterConfig(channelID string, channelType uint8) error {
// 	key := l.getChannelClusterConfigKey(channelID, channelType)
// 	return l.db.Delete(key, l.wo)
// }

// func (l *localStorage) getChannelClusterConfig(channelID string, channelType uint8) (*ChannelClusterConfig, error) {
// 	key := l.getChannelClusterConfigKey(channelID, channelType)
// 	data, closer, err := l.db.Get(key)
// 	if err != nil {
// 		if err == pebble.ErrNotFound {
// 			return nil, nil
// 		}
// 		return nil, err
// 	}
// 	defer closer.Close()
// 	if len(data) == 0 {
// 		return nil, nil
// 	}
// 	clusterInfo := &ChannelClusterConfig{}
// 	err = clusterInfo.Unmarshal(data)
// 	if err != nil {
// 		return nil, err
// 	}
// 	return clusterInfo, nil
// }

func (l *localStorage) getChannelSyncInfos(channelId string, channelType uint8) ([]*replica.SyncInfo, error) {
	slotID := l.getChannelSlotId(channelId)
	lowKey := l.getSlotSyncInfoKey(slotID, 0)
	highKey := l.getSlotSyncInfoKey(slotID, math.MaxUint64)
	iter := l.db.NewIter(&pebble.IterOptions{
		LowerBound: lowKey,
		UpperBound: highKey,
	})
	defer iter.Close()
	var syncInfos []*replica.SyncInfo
	for iter.First(); iter.Valid(); iter.Next() {
		syncInfo := &replica.SyncInfo{}
		err := syncInfo.Unmarshal(iter.Value())
		if err != nil {
			return nil, err
		}
		syncInfos = append(syncInfos, syncInfo)
	}
	return syncInfos, nil
}

func (l *localStorage) saveSlotSyncInfos(slotID uint32, syncInfos []*replica.SyncInfo) error {
	if len(syncInfos) == 0 {
		return nil
	}
	batch := l.db.NewBatch()
	for _, syncInfo := range syncInfos {
		data, err := syncInfo.Marshal()
		if err != nil {
			return err
		}
		slotSyncKey := l.getSlotSyncInfoKey(slotID, syncInfo.NodeID)
		err = batch.Set(slotSyncKey, data, l.wo)
		if err != nil {
			return err
		}
	}
	return batch.Commit(l.wo)
}

func (l *localStorage) setLeaderTermStartIndex(shardNo string, term uint32, index uint64) error {
	leaderTermStartIndexKeyData := l.leaderTermStartIndexKey(shardNo, term)
	var indexData = make([]byte, 12)
	binary.BigEndian.PutUint32(indexData, term)
	binary.BigEndian.PutUint64(indexData[4:], index)
	err := l.db.Set(leaderTermStartIndexKeyData, indexData, l.wo)
	return err
}

func (l *localStorage) leaderLastTerm(shardNo string) (uint32, error) {
	iter := l.db.NewIter(&pebble.IterOptions{
		LowerBound: l.leaderTermStartIndexKey(shardNo, 0),
		UpperBound: l.leaderTermStartIndexKey(shardNo, math.MaxUint32),
	})
	defer iter.Close()
	var maxTerm uint32
	for iter.First(); iter.Valid(); iter.Next() {
		data := iter.Value()
		term := binary.BigEndian.Uint32(data[:4])
		if term > maxTerm {
			maxTerm = term
		}
	}
	return maxTerm, nil
}

func (l *localStorage) leaderTermStartIndex(shardNo string, term uint32) (uint64, error) {
	leaderTermStartIndexKeyData := l.leaderTermStartIndexKey(shardNo, term)
	leaderTermStartIndexData, closer, err := l.db.Get(leaderTermStartIndexKeyData)
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}
	defer closer.Close()
	if len(leaderTermStartIndexData) == 0 {
		return 0, nil
	}
	return binary.BigEndian.Uint64(leaderTermStartIndexData[4:]), nil
}

func (l *localStorage) deleteLeaderTermStartIndexGreaterThanTerm(shardNo string, term uint32) error {
	iter := l.db.NewIter(&pebble.IterOptions{
		LowerBound: l.leaderTermStartIndexKey(shardNo, term+1),
		UpperBound: l.leaderTermStartIndexKey(shardNo, math.MaxUint32),
	})
	defer iter.Close()
	batch := l.db.NewBatch()
	var err error
	for iter.First(); iter.Valid(); iter.Next() {
		err = batch.Delete(iter.Key(), l.wo)
		if err != nil {
			return err
		}
	}
	return batch.Commit(l.wo)
}

func (l *localStorage) setAppliedIndex(shardNo string, index uint64) error {
	appliedIndexKeyData := l.getAppliedIndexKey(shardNo)
	appliedIndexdata := make([]byte, 8)
	binary.BigEndian.PutUint64(appliedIndexdata, index)
	err := l.db.Set(appliedIndexKeyData, appliedIndexdata, l.wo)
	return err
}

func (l *localStorage) getAppliedIndex(shardNo string) (uint64, error) {
	appliedIndexKeyData := l.getAppliedIndexKey(shardNo)
	appliedIndexdata, closer, err := l.db.Get(appliedIndexKeyData)
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}
	defer closer.Close()
	if len(appliedIndexdata) == 0 {
		return 0, nil
	}
	return binary.BigEndian.Uint64(appliedIndexdata), nil
}

func (l *localStorage) getAppointLeaderNotifyResultKey(slotID uint32, nodeID uint64) []byte {
	return []byte(fmt.Sprintf("/slots/%s/appointleadernotifyresults/nodes/%020d", l.getSlotFillFormat(slotID), nodeID))
}

func (l *localStorage) getSlotSyncInfoKey(slotID uint32, nodeID uint64) []byte {
	keyStr := fmt.Sprintf("/slots/%s/slotsyncs/nodes/%020d", l.getSlotFillFormat(slotID), nodeID)
	return []byte(keyStr)
}

func (l *localStorage) leaderTermStartIndexKey(shardNo string, term uint32) []byte {
	keyStr := fmt.Sprintf("/leadertermstartindex/%s/%010d", shardNo, term)
	return []byte(keyStr)
}

func (l *localStorage) getAppliedIndexKey(shardNo string) []byte {
	keyStr := fmt.Sprintf("/appliedindex/%s", shardNo)
	return []byte(keyStr)
}

func (l *localStorage) getSlotFillFormat(slotID uint32) string {
	return wkutil.GetSlotFillFormat(int(slotID), int(l.opts.SlotCount))
}

func (l *localStorage) getSlotFillFormatMax() string {
	return wkutil.GetSlotFillFormat(int(l.opts.SlotCount), int(l.opts.SlotCount))
}

func (l *localStorage) getSlotFillFormatMin() string {
	return wkutil.GetSlotFillFormat(0, int(l.opts.SlotCount))
}

func (l *localStorage) getChannelSlotId(channelId string) uint32 {
	return wkutil.GetSlotNum(int(l.opts.SlotCount), channelId)
}
