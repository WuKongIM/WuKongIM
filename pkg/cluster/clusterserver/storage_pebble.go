package cluster

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterserver/key"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/cockroachdb/pebble"
	"go.uber.org/zap"
)

type PebbleShardLogStorage struct {
	db     *pebble.DB
	path   string
	wo     *pebble.WriteOptions
	noSync *pebble.WriteOptions
	wklog.Log
}

func NewPebbleShardLogStorage(path string) *PebbleShardLogStorage {
	return &PebbleShardLogStorage{
		path: path,
		wo: &pebble.WriteOptions{
			Sync: true,
		},
		noSync: &pebble.WriteOptions{
			Sync: false,
		},
		Log: wklog.NewWKLog(fmt.Sprintf("PebbleShardLogStorage[%s]", path)),
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
	var err error
	p.db, err = pebble.Open(p.path, p.defaultPebbleOptions())
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

func (p *PebbleShardLogStorage) AppendLogs(shardNo string, logs []replica.Log) error {
	batch := p.db.NewBatch()
	defer batch.Close()

	for _, lg := range logs {
		logData, err := lg.Marshal()
		if err != nil {
			return err
		}

		timeData := make([]byte, 8)
		binary.BigEndian.PutUint64(timeData, uint64(time.Now().UnixNano()))

		logData = append(logData, timeData...)

		keyData := key.NewLogKey(shardNo, lg.Index)
		err = batch.Set(keyData, logData, p.noSync)
		if err != nil {
			return err
		}
	}
	lastLog := logs[len(logs)-1]
	err := p.saveMaxIndexWrite(shardNo, lastLog.Index, batch, p.noSync)
	if err != nil {
		return err
	}
	err = batch.Commit(p.wo)
	if err != nil {
		return err
	}
	return nil
}

func (p *PebbleShardLogStorage) AppendLogBatch(reqs []reactor.AppendLogReq) error {

	start := time.Now()
	defer func() {
		end := time.Since(start)
		if end > time.Millisecond*250 {
			p.Info("Slot appendLogBatch done", zap.Duration("cost", end))
		}

	}()

	batch := p.db.NewBatch()
	defer batch.Close()
	for _, req := range reqs {
		for _, lg := range req.Logs {
			logData, err := lg.Marshal()
			if err != nil {
				return err
			}

			timeData := make([]byte, 8)
			binary.BigEndian.PutUint64(timeData, uint64(time.Now().UnixNano()))

			logData = append(logData, timeData...)

			keyData := key.NewLogKey(req.HandleKey, lg.Index)
			err = batch.Set(keyData, logData, p.noSync)
			if err != nil {
				return err
			}
		}
		lastLog := req.Logs[len(req.Logs)-1]
		err := p.saveMaxIndexWrite(req.HandleKey, lastLog.Index, batch, p.noSync)
		if err != nil {
			return err
		}
	}
	err := batch.Commit(p.wo)
	if err != nil {
		return err
	}
	return nil
}

// TruncateLogTo 截断日志
func (p *PebbleShardLogStorage) TruncateLogTo(shardNo string, index uint64) error {
	if index == 0 {
		return errors.New("index must be greater than 0")
	}
	appliedIdx, err := p.AppliedIndex(shardNo)
	if err != nil {
		p.Error("get max index error", zap.Error(err))
		return err
	}
	if index <= appliedIdx {
		p.Panic(" applied must be less than  index index", zap.Uint64("index", index), zap.Uint64("appliedIdx", appliedIdx))
		return nil
	}
	keyData := key.NewLogKey(shardNo, index)
	maxKeyData := key.NewLogKey(shardNo, math.MaxUint64)
	err = p.db.DeleteRange(keyData, maxKeyData, p.wo)
	if err != nil {
		return err
	}
	return p.saveMaxIndex(shardNo, index-1)
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
	iter := p.db.NewIter(&pebble.IterOptions{
		LowerBound: lowKey,
		UpperBound: highKey,
	})
	defer iter.Close()
	var logs []replica.Log

	var size uint64
	for iter.First(); iter.Valid(); iter.Next() {

		data := make([]byte, len(iter.Value()))
		copy(data, iter.Value())

		logData := data[:len(data)-8]

		timeData := data[len(data)-8:]

		tm := binary.BigEndian.Uint64(timeData)

		var log replica.Log
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

func (p *PebbleShardLogStorage) GetLogsInReverseOrder(shardNo string, startLogIndex uint64, endLogIndex uint64, limit int) ([]replica.Log, error) {

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
	iter := p.db.NewIter(&pebble.IterOptions{
		LowerBound: lowKey,
		UpperBound: highKey,
	})
	defer iter.Close()
	var logs []replica.Log

	var size int
	for iter.Last(); iter.Valid(); iter.Prev() {

		data := make([]byte, len(iter.Value()))
		copy(data, iter.Value())

		logData := data[:len(data)-8]

		timeData := data[len(data)-8:]

		tm := binary.BigEndian.Uint64(timeData)

		var log replica.Log
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
	lastIndex, _, err := p.getMaxIndex(shardNo)
	return lastIndex, err
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

func (p *PebbleShardLogStorage) getLog(shardNo string, index uint64) (replica.Log, error) {
	keyData := key.NewLogKey(shardNo, index)
	resultData, closer, err := p.db.Get(keyData)
	if err != nil {
		if err == pebble.ErrNotFound {
			return replica.Log{}, nil
		}
		return replica.Log{}, err
	}
	defer closer.Close()

	data := make([]byte, len(resultData))
	copy(data, resultData)

	logData := data[:len(data)-8]

	timeData := data[len(data)-8:]

	tm := binary.BigEndian.Uint64(timeData)

	var log replica.Log
	err = log.Unmarshal(logData)
	if err != nil {
		return replica.Log{}, err
	}
	log.Time = time.Unix(0, int64(tm))
	return log, nil

}

func (p *PebbleShardLogStorage) SetLastIndex(shardNo string, index uint64) error {
	return p.saveMaxIndex(shardNo, index)
}

func (p *PebbleShardLogStorage) SetAppliedIndex(shardNo string, index uint64) error {
	maxIndexKeyData := key.NewAppliedIndexKey(shardNo)
	maxIndexdata := make([]byte, 8)
	binary.BigEndian.PutUint64(maxIndexdata, index)
	lastTime := time.Now().UnixNano()
	lastTimeData := make([]byte, 8)
	binary.BigEndian.PutUint64(lastTimeData, uint64(lastTime))

	err := p.db.Set(maxIndexKeyData, append(maxIndexdata, lastTimeData...), p.wo)
	return err

}

func (p *PebbleShardLogStorage) AppliedIndex(shardNo string) (uint64, error) {
	maxIndexKeyData := key.NewAppliedIndexKey(shardNo)
	maxIndexdata, closer, err := p.db.Get(maxIndexKeyData)
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

func (p *PebbleShardLogStorage) LeaderLastTermGreaterThan(shardNo string, term uint32) (uint32, error) {
	iter := p.db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewLeaderTermStartIndexKey(shardNo, term+1),
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
	iter := p.db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewLeaderTermStartIndexKey(shardNo, term+1),
		UpperBound: key.NewLeaderTermStartIndexKey(shardNo, math.MaxUint32),
	})
	defer iter.Close()
	batch := p.db.NewBatch()
	defer batch.Close()
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

	return p.saveMaxIndexWrite(shardNo, index, p.db, p.wo)
}

func (p *PebbleShardLogStorage) saveMaxIndexWrite(shardNo string, index uint64, w pebble.Writer, o *pebble.WriteOptions) error {
	maxIndexKeyData := key.NewMaxIndexKey(shardNo)
	maxIndexdata := make([]byte, 8)
	binary.BigEndian.PutUint64(maxIndexdata, index)
	lastTime := time.Now().UnixNano()
	lastTimeData := make([]byte, 8)
	binary.BigEndian.PutUint64(lastTimeData, uint64(lastTime))

	err := w.Set(maxIndexKeyData, append(maxIndexdata, lastTimeData...), o)
	return err
}

// GetMaxIndex 获取最大的index 和最后一次写入的时间
func (p *PebbleShardLogStorage) getMaxIndex(shardNo string) (uint64, uint64, error) {
	maxIndexKeyData := key.NewMaxIndexKey(shardNo)
	maxIndexdata, closer, err := p.db.Get(maxIndexKeyData)
	if closer != nil {
		defer closer.Close()
	}
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, 0, nil
		}
		return 0, 0, err
	}

	if len(maxIndexdata) == 0 {
		return 0, 0, nil
	}

	return binary.BigEndian.Uint64(maxIndexdata[:8]), binary.BigEndian.Uint64(maxIndexdata[8:]), nil
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
