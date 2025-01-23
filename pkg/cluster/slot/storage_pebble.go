package slot

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/slot/key"
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/cockroachdb/pebble"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type PebbleShardLogStorage struct {
	dbs      []*pebble.DB
	batchDbs []*wkdb.BatchDB
	shardNum uint32 // 分片数量
	path     string
	sync     *pebble.WriteOptions
	noSync   *pebble.WriteOptions
	wklog.Log

	stopper syncutil.Stopper
	s       *Server
}

func NewPebbleShardLogStorage(s *Server, path string, shardNum uint32) *PebbleShardLogStorage {
	return &PebbleShardLogStorage{
		shardNum: shardNum,
		path:     path,
		sync: &pebble.WriteOptions{
			Sync: true,
		},
		noSync: &pebble.WriteOptions{
			Sync: false,
		},
		Log:     wklog.NewWKLog(fmt.Sprintf("PebbleShardLogStorage[%s]", path)),
		stopper: *syncutil.NewStopper(),
		s:       s,
	}
}

func (p *PebbleShardLogStorage) defaultPebbleOptions() *pebble.Options {
	blockSize := 32 * 1024
	sz := 16 * 1024 * 1024
	levelSizeMultiplier := 2

	lopts := make([]pebble.LevelOptions, 0)
	var numOfLevels int64 = 7
	for l := int64(0); l < numOfLevels; l++ {
		opt := pebble.LevelOptions{
			Compression:    pebble.NoCompression,
			BlockSize:      blockSize,
			TargetFileSize: 16 * 1024 * 1024,
		}
		sz = sz * levelSizeMultiplier
		lopts = append(lopts, opt)
	}
	return &pebble.Options{
		Levels:             lopts,
		FormatMajorVersion: pebble.FormatNewest,
		// 控制写缓冲区的大小。较大的写缓冲区可以减少磁盘写入次数，但会占用更多内存。
		MemTableSize: 128 * 1024 * 1024,
		// 当队列中的MemTables的大小超过 MemTableStopWritesThreshold*MemTableSize 时，将停止写入，
		// 直到被刷到磁盘，这个值不能小于2
		MemTableStopWritesThreshold: 4,
		// MANIFEST 文件的大小
		MaxManifestFileSize:       128 * 1024 * 1024,
		LBaseMaxBytes:             4 * 1024 * 1024 * 1024,
		L0CompactionFileThreshold: 8,
		L0StopWritesThreshold:     24,
	}
}

func (p *PebbleShardLogStorage) Open() error {

	opts := p.defaultPebbleOptions()
	for i := 0; i < int(p.shardNum); i++ {
		db, err := pebble.Open(fmt.Sprintf("%s/shard%03d", p.path, i), opts)
		if err != nil {
			return err
		}
		p.dbs = append(p.dbs, db)

		batchDb := wkdb.NewBatchDB(i, db)
		batchDb.Start()
		p.batchDbs = append(p.batchDbs, batchDb)
	}

	return nil
}

func (p *PebbleShardLogStorage) Close() error {
	for _, db := range p.dbs {
		if err := db.Close(); err != nil {
			p.Error("close db error", zap.Error(err))
		}
	}

	for _, db := range p.batchDbs {
		db.Stop()
	}

	p.stopper.Stop()

	return nil
}

func (p *PebbleShardLogStorage) shardDB(v string) *pebble.DB {
	shardId := p.shardId(v)
	if shardId >= uint32(len(p.dbs)) {
		p.Panic("shardId is out of range", zap.Uint32("shardId", shardId), zap.Int("dbs", len(p.dbs)))
	}
	return p.dbs[shardId]
}

func (p *PebbleShardLogStorage) shardBatchDB(v string) *wkdb.BatchDB {
	shardId := p.shardId(v)
	return p.batchDbs[shardId]
}

func (p *PebbleShardLogStorage) shardId(v string) uint32 {
	if v == "" {
		p.Panic("shardId key is empty")
	}
	if p.shardNum == 1 {
		return 0
	}
	h := fnv.New32()
	h.Write([]byte(v))

	return h.Sum32() % p.shardNum
}

func (p *PebbleShardLogStorage) shardBatchDBWithIndex(index uint32) *wkdb.BatchDB {
	return p.batchDbs[index]
}
func (p *PebbleShardLogStorage) GetState(shardNo string) (types.RaftState, error) {

	lastLogIndex, lastLogTerm, err := p.LastIndexAndTerm(shardNo)
	if err != nil {
		return types.RaftState{}, err
	}

	applied, err := p.AppliedIndex(shardNo)
	if err != nil {
		return types.RaftState{}, err
	}

	return types.RaftState{
		LastLogIndex: lastLogIndex,
		LastTerm:     lastLogTerm,
		AppliedIndex: applied,
	}, nil
}

func (p *PebbleShardLogStorage) AppendLogs(shardNo string, logs []types.Log, termStartIndexInfo *types.TermStartIndexInfo) error {
	batch := p.shardDB(shardNo).NewBatch()
	defer batch.Close()

	if termStartIndexInfo != nil {
		err := p.SetLeaderTermStartIndex(shardNo, termStartIndexInfo.Term, termStartIndexInfo.Index)
		if err != nil {
			p.Panic("SetLeaderTermStartIndex failed", zap.Error(err))
			return err
		}
	}

	for _, lg := range logs {
		logData, err := lg.Marshal()
		if err != nil {
			p.Panic("log marshal failed", zap.Error(err))
			return err
		}

		timeData := make([]byte, 8)
		binary.BigEndian.PutUint64(timeData, uint64(time.Now().UnixNano()))

		logData = append(logData, timeData...)

		keyData := key.NewLogKey(shardNo, lg.Index)
		err = batch.Set(keyData, logData, p.noSync)
		if err != nil {
			p.Panic("batch set failed", zap.Error(err))
			return err
		}
	}

	return batch.Commit(p.sync)
}

// TruncateLogTo 截断日志
func (p *PebbleShardLogStorage) TruncateLogTo(shardNo string, index uint64) error {
	if index == 0 {
		return errors.New("index must be greater than 0")
	}

	lastLog, err := p.lastLog(shardNo)
	if err != nil {
		p.Error("TruncateLogTo: getMaxIndex error", zap.Error(err))
		return err
	}
	lastIndex := lastLog.Index
	if index >= lastIndex {
		return nil
	}

	appliedIdx, err := p.AppliedIndex(shardNo)
	if err != nil {
		p.Error("get max index error", zap.Error(err))
		return err
	}
	if index < appliedIdx {
		p.Foucs(" applied must be less than  index", zap.Uint64("index", index), zap.Uint64("appliedIdx", appliedIdx), zap.Uint64("lastIndex", lastIndex), zap.String("shardNo", shardNo))
		return nil
	}

	keyData := key.NewLogKey(shardNo, index+1)
	maxKeyData := key.NewLogKey(shardNo, math.MaxUint64)
	err = p.shardDB(shardNo).DeleteRange(keyData, maxKeyData, p.sync)
	if err != nil {
		return err
	}
	return nil
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

func (p *PebbleShardLogStorage) GetLogs(shardNo string, startLogIndex uint64, endLogIndex uint64, limitSize uint64) ([]types.Log, error) {

	// lastIndex, err := p.LastIndex(shardNo)
	// if err != nil {
	// 	return nil, err
	// }

	lowKey := key.NewLogKey(shardNo, startLogIndex)
	if endLogIndex == 0 {
		endLogIndex = math.MaxUint64
	}
	// if endLogIndex > lastIndex {
	// 	endLogIndex = lastIndex + 1
	// }
	highKey := key.NewLogKey(shardNo, endLogIndex)
	iter := p.shardDB(shardNo).NewIter(&pebble.IterOptions{
		LowerBound: lowKey,
		UpperBound: highKey,
	})
	defer iter.Close()
	var logs []types.Log

	var size uint64
	for iter.First(); iter.Valid(); iter.Next() {

		data := make([]byte, len(iter.Value()))
		copy(data, iter.Value())

		logData := data[:len(data)-8]

		timeData := data[len(data)-8:]

		tm := binary.BigEndian.Uint64(timeData)

		var log types.Log
		err := log.Unmarshal(logData)
		if err != nil {
			return nil, err
		}
		log.Time = time.Unix(0, int64(tm))
		logs = append(logs, log)
		size += uint64(log.LogSize())
		if limitSize != 0 && size >= limitSize {
			break
		}
	}
	return logs, nil
}

func (p *PebbleShardLogStorage) Apply(shardNo string, logs []types.Log) error {
	if p.s.opts.OnApply != nil {
		slotId := KeyToSlotId(shardNo)
		err := p.s.opts.OnApply(slotId, logs)
		if err != nil {
			return err
		}
		err = p.SetAppliedIndex(shardNo, logs[len(logs)-1].Index)
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *PebbleShardLogStorage) SaveConfig(shardNo string, cfg types.Config) error {
	if p.s.opts.OnSaveConfig != nil {
		slotId := KeyToSlotId(shardNo)
		err := p.s.opts.OnSaveConfig(slotId, cfg)
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *PebbleShardLogStorage) GetLogsInReverseOrder(shardNo string, startLogIndex uint64, endLogIndex uint64, limit int) ([]types.Log, error) {

	// lastIndex, err := p.LastIndex(shardNo)
	// if err != nil {
	// 	return nil, err
	// }

	lowKey := key.NewLogKey(shardNo, startLogIndex)
	if endLogIndex == 0 {
		endLogIndex = math.MaxUint64
	}
	// if endLogIndex > lastIndex {
	// 	endLogIndex = lastIndex + 1
	// }
	highKey := key.NewLogKey(shardNo, endLogIndex)
	iter := p.shardDB(shardNo).NewIter(&pebble.IterOptions{
		LowerBound: lowKey,
		UpperBound: highKey,
	})
	defer iter.Close()
	var logs []types.Log

	var size int
	for iter.Last(); iter.Valid(); iter.Prev() {

		data := make([]byte, len(iter.Value()))
		copy(data, iter.Value())

		logData := data[:len(data)-8]

		timeData := data[len(data)-8:]

		tm := binary.BigEndian.Uint64(timeData)

		var log types.Log
		err := log.Unmarshal(logData)
		if err != nil {
			return nil, err
		}
		log.Time = time.Unix(0, int64(tm))

		logs = append(logs, log)
		size += 1
		if limit != 0 && size >= limit {
			break
		}
	}
	return logs, nil
}

func (p *PebbleShardLogStorage) LastIndex(shardNo string) (uint64, error) {
	lastLog, err := p.lastLog(shardNo)
	if err != nil {
		return 0, err
	}
	return lastLog.Index, err
}

func (p *PebbleShardLogStorage) LastIndexAndTerm(shardNo string) (uint64, uint32, error) {
	lastIndex, err := p.LastIndex(shardNo)
	if err != nil {
		return 0, 0, err
	}
	if lastIndex == 0 {
		return 0, 0, nil
	}
	log, err := p.getLog(shardNo, lastIndex)
	if err != nil {
		return 0, 0, err
	}
	return lastIndex, log.Term, nil
}

func (p *PebbleShardLogStorage) getLog(shardNo string, index uint64) (types.Log, error) {
	keyData := key.NewLogKey(shardNo, index)
	resultData, closer, err := p.shardDB(shardNo).Get(keyData)
	if err != nil {
		if err == pebble.ErrNotFound {
			return types.Log{}, nil
		}
		return types.Log{}, err
	}
	defer closer.Close()

	data := make([]byte, len(resultData))
	copy(data, resultData)

	logData := data[:len(data)-8]

	timeData := data[len(data)-8:]

	tm := binary.BigEndian.Uint64(timeData)

	var log types.Log
	err = log.Unmarshal(logData)
	if err != nil {
		return types.Log{}, err
	}
	log.Time = time.Unix(0, int64(tm))
	return log, nil

}

func (p *PebbleShardLogStorage) lastLog(shardNo string) (types.Log, error) {
	iter := p.shardDB(shardNo).NewIter(&pebble.IterOptions{
		LowerBound: key.NewLogKey(shardNo, 0),
		UpperBound: key.NewLogKey(shardNo, math.MaxUint64),
	})
	defer iter.Close()

	if iter.Last() && iter.Valid() {
		data := make([]byte, len(iter.Value()))
		copy(data, iter.Value())

		logData := data[:len(data)-8]

		timeData := data[len(data)-8:]

		tm := binary.BigEndian.Uint64(timeData)

		var log types.Log
		err := log.Unmarshal(logData)
		if err != nil {
			return types.EmptyLog, err
		}
		log.Time = time.Unix(0, int64(tm))
		return log, nil
	}
	return types.EmptyLog, nil
}

func (p *PebbleShardLogStorage) SetAppliedIndex(shardNo string, index uint64) error {
	maxIndexKeyData := key.NewAppliedIndexKey(shardNo)
	maxIndexdata := make([]byte, 8)
	binary.BigEndian.PutUint64(maxIndexdata, index)
	lastTime := time.Now().UnixNano()
	lastTimeData := make([]byte, 8)
	binary.BigEndian.PutUint64(lastTimeData, uint64(lastTime))

	err := p.shardDB(shardNo).Set(maxIndexKeyData, append(maxIndexdata, lastTimeData...), p.sync)
	return err

}

func (p *PebbleShardLogStorage) AppliedIndex(shardNo string) (uint64, error) {
	maxIndexKeyData := key.NewAppliedIndexKey(shardNo)
	maxIndexdata, closer, err := p.shardDB(shardNo).Get(maxIndexKeyData)
	if closer != nil {
		defer closer.Close()
	}
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}
	if len(maxIndexdata) == 0 {
		return 0, nil
	}
	return binary.BigEndian.Uint64(maxIndexdata[:8]), nil
}

func (p *PebbleShardLogStorage) SetLeaderTermStartIndex(shardNo string, term uint32, index uint64) error {
	leaderTermStartIndexKeyData := key.NewLeaderTermStartIndexKey(shardNo, term)

	var indexData = make([]byte, 8)
	binary.BigEndian.PutUint64(indexData, index)
	err := p.shardDB(shardNo).Set(leaderTermStartIndexKeyData, indexData, p.sync)
	return err
}

func (p *PebbleShardLogStorage) LeaderLastLogTerm(shardNo string) (uint32, error) {
	iter := p.shardDB(shardNo).NewIter(&pebble.IterOptions{
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

func (p *PebbleShardLogStorage) GetTermStartIndex(shardNo string, term uint32) (uint64, error) {
	leaderTermStartIndexKeyData := key.NewLeaderTermStartIndexKey(shardNo, term)
	leaderTermStartIndexData, closer, err := p.shardDB(shardNo).Get(leaderTermStartIndexKeyData)
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

func (p *PebbleShardLogStorage) LeaderTermGreaterEqThan(shardNo string, term uint32) (uint32, error) {
	iter := p.shardDB(shardNo).NewIter(&pebble.IterOptions{
		LowerBound: key.NewLeaderTermStartIndexKey(shardNo, term),
		UpperBound: key.NewLeaderTermStartIndexKey(shardNo, math.MaxUint32),
	})
	defer iter.Close()
	var maxTerm uint32 = term
	for iter.First(); iter.Valid(); iter.Next() {
		term := key.GetTermFromLeaderTermStartIndexKey(iter.Key())
		if term >= maxTerm {
			maxTerm = term
			break
		}
	}
	return maxTerm, nil
}

func (p *PebbleShardLogStorage) DeleteLeaderTermStartIndexGreaterThanTerm(shardNo string, term uint32) error {
	iter := p.shardDB(shardNo).NewIter(&pebble.IterOptions{
		LowerBound: key.NewLeaderTermStartIndexKey(shardNo, term+1),
		UpperBound: key.NewLeaderTermStartIndexKey(shardNo, math.MaxUint32),
	})
	defer iter.Close()
	batch := p.shardDB(shardNo).NewBatch()
	defer batch.Close()
	var err error
	for iter.First(); iter.Valid(); iter.Next() {
		err = batch.Delete(iter.Key(), p.sync)
		if err != nil {
			return err
		}
	}
	return batch.Commit(p.sync)
}

// type localStorage struct {
// 	db    *pebble.DB
// 	dbDir string
// 	wklog.Log
// 	wo   *pebble.WriteOptions
// 	opts *Options
// }

// func newLocalStorage(opts *Options) *localStorage {
// 	dbDir := path.Join(opts.DataDir, "localWukongimdb")
// 	return &localStorage{
// 		opts:  opts,
// 		dbDir: dbDir,
// 		Log:   wklog.NewWKLog(fmt.Sprintf("localStorage[%s]", dbDir)),
// 		wo: &pebble.WriteOptions{
// 			Sync: true,
// 		},
// 	}
// }

// func (l *localStorage) open() error {
// 	var err error
// 	l.db, err = pebble.Open(l.dbDir, &pebble.Options{})
// 	return err
// }

// func (l *localStorage) close() error {
// 	return l.db.Close()
// }

// func (l *localStorage) setLeaderTermStartIndex(shardNo string, term uint32, index uint64) error {
// 	leaderTermStartIndexKeyData := l.leaderTermStartIndexKey(shardNo, term)
// 	var indexData = make([]byte, 12)
// 	binary.BigEndian.PutUint32(indexData, term)
// 	binary.BigEndian.PutUint64(indexData[4:], index)
// 	err := l.db.Set(leaderTermStartIndexKeyData, indexData, l.wo)
// 	return err
// }

// func (l *localStorage) leaderLastTerm(shardNo string) (uint32, error) {
// 	iter := l.db.NewIter(&pebble.IterOptions{
// 		LowerBound: l.leaderTermStartIndexKey(shardNo, 0),
// 		UpperBound: l.leaderTermStartIndexKey(shardNo, math.MaxUint32),
// 	})
// 	defer iter.Close()
// 	var maxTerm uint32
// 	for iter.First(); iter.Valid(); iter.Next() {
// 		data := iter.Value()
// 		term := binary.BigEndian.Uint32(data[:4])
// 		if term > maxTerm {
// 			maxTerm = term
// 		}
// 	}
// 	return maxTerm, nil
// }

// func (l *localStorage) leaderTermStartIndex(shardNo string, term uint32) (uint64, error) {
// 	leaderTermStartIndexKeyData := l.leaderTermStartIndexKey(shardNo, term)
// 	leaderTermStartIndexData, closer, err := l.db.Get(leaderTermStartIndexKeyData)
// 	if err != nil {
// 		if err == pebble.ErrNotFound {
// 			return 0, nil
// 		}
// 		return 0, err
// 	}
// 	defer closer.Close()
// 	if len(leaderTermStartIndexData) == 0 {
// 		return 0, nil
// 	}
// 	return binary.BigEndian.Uint64(leaderTermStartIndexData[4:]), nil
// }

// func (l *localStorage) deleteLeaderTermStartIndexGreaterThanTerm(shardNo string, term uint32) error {
// 	iter := l.db.NewIter(&pebble.IterOptions{
// 		LowerBound: l.leaderTermStartIndexKey(shardNo, term+1),
// 		UpperBound: l.leaderTermStartIndexKey(shardNo, math.MaxUint32),
// 	})
// 	defer iter.Close()
// 	batch := l.db.NewBatch()
// 	var err error
// 	for iter.First(); iter.Valid(); iter.Next() {
// 		err = batch.Delete(iter.Key(), l.wo)
// 		if err != nil {
// 			return err
// 		}
// 	}
// 	return batch.Commit(l.wo)
// }

// func (l *localStorage) setAppliedIndex(shardNo string, index uint64) error {
// 	appliedIndexKeyData := l.getAppliedIndexKey(shardNo)
// 	appliedIndexdata := make([]byte, 8)
// 	binary.BigEndian.PutUint64(appliedIndexdata, index)

// 	err := l.db.Set(appliedIndexKeyData, appliedIndexdata, l.wo)
// 	return err
// }

// func (l *localStorage) getAppliedIndex(shardNo string) (uint64, error) {
// 	appliedIndexKeyData := l.getAppliedIndexKey(shardNo)
// 	appliedIndexdata, closer, err := l.db.Get(appliedIndexKeyData)
// 	if err != nil {
// 		if err == pebble.ErrNotFound {
// 			return 0, nil
// 		}
// 		return 0, err
// 	}
// 	defer closer.Close()
// 	if len(appliedIndexdata) == 0 {
// 		return 0, nil
// 	}
// 	index := binary.BigEndian.Uint64(appliedIndexdata)
// 	return index, nil
// }

// func (l *localStorage) getAppointLeaderNotifyResultKey(slotID uint32, nodeID uint64) []byte {
// 	return []byte(fmt.Sprintf("/slots/%s/appointleadernotifyresults/nodes/%020d", l.getSlotFillFormat(slotID), nodeID))
// }

// func (l *localStorage) getSlotSyncInfoKey(slotID uint32, nodeID uint64) []byte {
// 	keyStr := fmt.Sprintf("/slots/%s/slotsyncs/nodes/%020d", l.getSlotFillFormat(slotID), nodeID)
// 	return []byte(keyStr)
// }

// func (l *localStorage) leaderTermStartIndexKey(shardNo string, term uint32) []byte {
// 	keyStr := fmt.Sprintf("/leadertermstartindex/%s/%010d", shardNo, term)
// 	return []byte(keyStr)
// }

// func (l *localStorage) getAppliedIndexKey(shardNo string) []byte {
// 	keyStr := fmt.Sprintf("/appliedindex/%s", shardNo)
// 	return []byte(keyStr)
// }

// func (l *localStorage) getSlotFillFormat(slotID uint32) string {
// 	return wkutil.GetSlotFillFormat(int(slotID), int(l.opts.SlotCount))
// }

// func (l *localStorage) getSlotFillFormatMax() string {
// 	return wkutil.GetSlotFillFormat(int(l.opts.SlotCount), int(l.opts.SlotCount))
// }

// func (l *localStorage) getSlotFillFormatMin() string {
// 	return wkutil.GetSlotFillFormat(0, int(l.opts.SlotCount))
// }

// func (l *localStorage) getChannelSlotId(channelId string) uint32 {
// 	return wkutil.GetSlotNum(int(l.opts.SlotCount), channelId)
// }
