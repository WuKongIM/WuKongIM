package clusterconfig

import (
	"encoding/binary"
	"errors"
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/key"
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
		path:   path,
		wo:     &pebble.WriteOptions{Sync: true},
		noSync: &pebble.WriteOptions{Sync: false},
		Log:    wklog.NewWKLog("ConfigPebbleShardLogStorage"),
	}
}

func (p *PebbleShardLogStorage) Open() error {
	var err error
	p.db, err = pebble.Open(p.path, &pebble.Options{
		FormatMajorVersion: pebble.FormatNewest,
	})
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
func (p *PebbleShardLogStorage) AppendLog(logs []replica.Log) error {

	batch := p.db.NewBatch()
	defer batch.Close()
	for _, lg := range logs {

		// 日志数据
		logData, err := lg.Marshal()
		if err != nil {
			return err
		}

		timeData := make([]byte, 8)
		binary.BigEndian.PutUint64(timeData, uint64(time.Now().UnixNano()))

		logData = append(logData, timeData...)

		keyData := key.NewLogKey(lg.Index)
		err = batch.Set(keyData, logData, p.noSync)
		if err != nil {
			return err
		}

		// 日志最大index
		err = p.saveMaxIndexWithWriter(lg.Index, batch, p.noSync)
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
func (p *PebbleShardLogStorage) TruncateLogTo(index uint64) error {
	if index == 0 {
		return errors.New("index must be greater than 0")
	}
	appliedIdx, err := p.AppliedIndex()
	if err != nil {
		p.Error("get max index error", zap.Error(err))
		return err
	}
	if index <= appliedIdx {
		p.Error("index must be less than  applied index", zap.Uint64("index", index), zap.Uint64("appliedIdx", appliedIdx))
		return nil
	}
	keyData := key.NewLogKey(index)
	maxKeyData := key.NewLogKey(math.MaxUint64)
	err = p.db.DeleteRange(keyData, maxKeyData, p.wo)
	if err != nil {
		return err
	}

	return p.saveMaxIndex(index - 1)
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

func (p *PebbleShardLogStorage) Logs(startLogIndex uint64, endLogIndex uint64, limitSize uint64) ([]replica.Log, error) {

	// lastIndex, err := p.LastIndex()
	// if err != nil {
	// 	return nil, err
	// }

	lowKey := key.NewLogKey(startLogIndex)
	if endLogIndex == 0 {
		endLogIndex = math.MaxUint64
	}
	// if endLogIndex > lastIndex {
	// 	endLogIndex = lastIndex + 1
	// }
	highKey := key.NewLogKey(endLogIndex)
	iter := p.db.NewIter(&pebble.IterOptions{
		LowerBound: lowKey,
		UpperBound: highKey,
	})
	defer iter.Close()
	var logs []replica.Log

	var size uint64
	for iter.First(); iter.Valid(); iter.Next() {

		// 这里需要复制一份出来，要不然log.Data会重用data切片，导致数据错误
		data := make([]byte, len(iter.Value()))
		copy(data, iter.Value())

		timeData := data[len(data)-8:]
		tm := binary.BigEndian.Uint64(timeData)

		var log replica.Log
		err := log.Unmarshal(data[:len(data)-8])
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

// GetLogsInReverseOrder retrieves a list of logs in reverse order within the specified range and limit.
func (p *PebbleShardLogStorage) GetLogsInReverseOrder(startLogIndex uint64, endLogIndex uint64, limit int) ([]replica.Log, error) {
	lowKey := key.NewLogKey(startLogIndex)
	if endLogIndex == 0 {
		endLogIndex = math.MaxUint64
	}
	highKey := key.NewLogKey(endLogIndex)
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

		timeData := data[len(data)-8:]
		tm := binary.BigEndian.Uint64(timeData)

		var log replica.Log
		err := log.Unmarshal(data[:len(data)-8])
		if err != nil {
			return nil, err
		}

		log.Time = time.Unix(0, int64(tm))

		logs = append(logs, log)
		size++
		if limit != 0 && size >= limit {
			break
		}
	}
	return logs, nil
}
func (p *PebbleShardLogStorage) FirstIndex() (uint64, error) {

	return 0, nil
}

func (p *PebbleShardLogStorage) LastIndex() (uint64, error) {
	lastIndex, _, err := p.getMaxIndex()
	return lastIndex, err
}

func (p *PebbleShardLogStorage) LastIndexAndTerm() (uint64, uint32, error) {
	lastIndex, err := p.LastIndex()
	if err != nil {
		return 0, 0, err
	}
	if lastIndex == 0 {
		return 0, 0, nil
	}
	log, err := p.getLog(lastIndex)
	if err != nil {
		return 0, 0, err
	}
	return lastIndex, log.Term, nil
}

func (p *PebbleShardLogStorage) LastLog() (replica.Log, error) {
	lastIndex, err := p.LastIndex()
	if err != nil {
		return replica.Log{}, err
	}
	if lastIndex == 0 {
		return replica.Log{}, nil
	}
	return p.getLog(lastIndex)
}

func (p *PebbleShardLogStorage) getLog(index uint64) (replica.Log, error) {
	keyData := key.NewLogKey(index)
	logData, closer, err := p.db.Get(keyData)
	if err != nil {
		if err == pebble.ErrNotFound {
			return replica.Log{}, nil
		}
		return replica.Log{}, err
	}
	defer closer.Close()

	timeData := logData[len(logData)-8:]
	tm := binary.BigEndian.Uint64(timeData)

	var log replica.Log
	err = log.Unmarshal(logData[:len(logData)-8])
	if err != nil {
		return replica.EmptyLog, err
	}
	log.Time = time.Unix(0, int64(tm))

	return log, nil

}

func (p *PebbleShardLogStorage) setLastIndex(index uint64) error {
	return p.saveMaxIndex(index)
}

func (p *PebbleShardLogStorage) setAppliedIndex(index uint64) error {
	maxIndexKeyData := key.NewAppliedIndexKey()
	maxIndexdata := make([]byte, 8)
	binary.BigEndian.PutUint64(maxIndexdata, index)
	lastTime := time.Now().UnixNano()
	lastTimeData := make([]byte, 8)
	binary.BigEndian.PutUint64(lastTimeData, uint64(lastTime))

	err := p.db.Set(maxIndexKeyData, append(maxIndexdata, lastTimeData...), p.wo)
	return err

}

func (p *PebbleShardLogStorage) AppliedIndex() (uint64, error) {
	maxIndexKeyData := key.NewAppliedIndexKey()
	maxIndexdata, closer, err := p.db.Get(maxIndexKeyData)
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}
	defer closer.Close()
	if len(maxIndexdata) == 0 {
		return 0, nil
	}
	return binary.BigEndian.Uint64(maxIndexdata[:8]), nil
}

func (p *PebbleShardLogStorage) LastIndexAndAppendTime() (uint64, uint64, error) {
	return p.getMaxIndex()
}

func (p *PebbleShardLogStorage) SetLeaderTermStartIndex(term uint32, index uint64) error {
	leaderTermStartIndexKeyData := key.NewLeaderTermStartIndexKey(term)

	var indexData = make([]byte, 8)
	binary.BigEndian.PutUint64(indexData, index)
	err := p.db.Set(leaderTermStartIndexKeyData, indexData, p.wo)
	return err
}

func (p *PebbleShardLogStorage) LeaderLastTerm() (uint32, error) {
	iter := p.db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewLeaderTermStartIndexKey(0),
		UpperBound: key.NewLeaderTermStartIndexKey(math.MaxUint32),
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

func (p *PebbleShardLogStorage) LeaderTermStartIndex(term uint32) (uint64, error) {
	leaderTermStartIndexKeyData := key.NewLeaderTermStartIndexKey(term)
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

// 获取大于或等于term的lastTerm
func (p *PebbleShardLogStorage) LeaderLastTermGreaterThan(term uint32) (uint32, error) {
	iter := p.db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewLeaderTermStartIndexKey(term),
		UpperBound: key.NewLeaderTermStartIndexKey(math.MaxUint32),
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

func (p *PebbleShardLogStorage) DeleteLeaderTermStartIndexGreaterThanTerm(term uint32) error {
	iter := p.db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewLeaderTermStartIndexKey(term + 1),
		UpperBound: key.NewLeaderTermStartIndexKey(math.MaxUint32),
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

func (p *PebbleShardLogStorage) saveMaxIndex(index uint64) error {
	return p.saveMaxIndexWithWriter(index, p.db, p.wo)
}

func (p *PebbleShardLogStorage) saveMaxIndexWithWriter(index uint64, w pebble.Writer, o *pebble.WriteOptions) error {
	maxIndexKeyData := key.NewMaxIndexKey()
	maxIndexdata := make([]byte, 8)
	binary.BigEndian.PutUint64(maxIndexdata, index)
	lastTime := time.Now().UnixNano()
	lastTimeData := make([]byte, 8)
	binary.BigEndian.PutUint64(lastTimeData, uint64(lastTime))

	err := w.Set(maxIndexKeyData, append(maxIndexdata, lastTimeData...), o)
	return err
}

// GetMaxIndex 获取最大的index 和最后一次写入的时间
func (p *PebbleShardLogStorage) getMaxIndex() (uint64, uint64, error) {
	maxIndexKeyData := key.NewMaxIndexKey()
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
