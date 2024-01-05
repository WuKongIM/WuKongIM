package replica

import "github.com/WuKongIM/WuKongIM/pkg/cluster/wal"

type IStorage interface {
	// AppendLog 追加日志
	AppendLog(log Log) error
	// 获取日志
	GetLogs(startLogIndex uint64, limit uint32) ([]Log, error)
	// 最后一条日志的索引
	LastIndex() (uint64, error)
	// 获取第一条日志的索引
	FirstIndex() (uint64, error)
	// 应用日志下标
	SetAppliedIndex(index uint64) error
}

type WALStorage struct {
	walLog *wal.Log
	path   string
}

func NewWALStorage(path string) *WALStorage {
	return &WALStorage{
		path: path,
	}
}

func (w *WALStorage) Open() error {
	var err error
	w.walLog, err = wal.Open(w.path, wal.DefaultOptions)
	return err
}

func (w *WALStorage) Close() {
	w.walLog.Close()
}

func (w *WALStorage) AppendLog(log Log) error {
	lastIdx, _ := w.LastIndex()
	if log.Index <= lastIdx {
		return nil
	}
	return w.walLog.Write(log.Index, log.Data)
}

func (w *WALStorage) GetLogs(startLogIndex uint64, limit uint32) ([]Log, error) {
	lastIdx, _ := w.LastIndex()
	if startLogIndex > lastIdx {
		return nil, nil
	}

	logs := make([]Log, 0, limit)
	for i := startLogIndex; i <= lastIdx; i++ {
		lg, err := w.readLog(i)
		if err != nil {
			return nil, err
		}
		logs = append(logs, lg)
		if len(logs) > int(limit) {
			break
		}
	}
	return logs, nil
}

func (w *WALStorage) LastIndex() (uint64, error) {
	return w.walLog.LastIndex()
}

func (w *WALStorage) FirstIndex() (uint64, error) {
	return w.walLog.FirstIndex()
}

func (w *WALStorage) SetAppliedIndex(index uint64) error {
	return nil
}

func (r *WALStorage) readLog(index uint64) (Log, error) {
	data, err := r.walLog.Read(index)
	if err != nil {
		return Log{}, err
	}
	return Log{
		Index: index,
		Data:  data,
	}, err
}
