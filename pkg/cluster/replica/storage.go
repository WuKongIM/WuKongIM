package replica

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type IStorage interface {
	// AppendLog 追加日志
	// AppendLog(logs []Log) error
	// TruncateLog 截断日志, 从index开始截断,index不能等于0 （保留下来的内容不包含index）
	// [1,2,3,4,5,6] truncate to 4 = [1,2,3]
	TruncateLogTo(logIndex uint64) error
	// GetLogs 获取日志 [startLogIndex,endLogIndex)
	// startLogIndex 开始日志索引(结果包含startLogIndex)
	// endLogIndex 结束日志索引(结果不包含endLogIndex) endLogIndex=0表示不限制
	// limitSize 限制返回的日志大小
	Logs(startLogIndex uint64, endLogIndex uint64, limitSize uint64) ([]Log, error)
	FirstIndex() (uint64, error)
	// LastIndex 最后一条日志的索引
	LastIndex() (uint64, error)

	// SetLeaderTermStartIndex 设置领导任期开始的第一条日志索引
	SetLeaderTermStartIndex(term uint32, index uint64) error
	// LeaderLastTerm 获取最新的本地保存的领导任期
	LeaderLastTerm() (uint32, error)
	// LeaderTermStartIndex 获取领导任期开始的第一条日志索引
	LeaderTermStartIndex(term uint32) (uint64, error)

	// 删除比传入的term大的的LeaderTermStartIndex记录
	DeleteLeaderTermStartIndexGreaterThanTerm(term uint32) error
}

type MemoryStorage struct {
	logs              []Log
	termStartIndexMap map[uint32]uint64
	wklog.Log
}

func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		termStartIndexMap: make(map[uint32]uint64),
		Log:               wklog.NewWKLog("replica.MemoryStorage"),
	}
}

func (m *MemoryStorage) AppendLog(logs []Log) error {
	m.logs = append(m.logs, logs...)
	return nil
}

func (m *MemoryStorage) Logs(lo, hi, maxSize uint64) ([]Log, error) {
	if len(m.logs) == 0 {
		return nil, nil
	}
	offset := m.logs[0].Index
	if lo < offset {
		m.Error("lo < offset", zap.Uint64("lo", lo), zap.Uint64("offset", offset))
		return nil, ErrCompacted
	}
	if hi > m.lastIndex()+1 {
		m.Panic(fmt.Sprintf("entries' hi(%d) is out of bound lastindex(%d)", hi, m.lastIndex()))
	}
	logs := limitSize(m.logs[lo-offset:hi-offset], logEncodingSize(maxSize))

	// NB: use the full slice expression to limit what the caller can do with the
	// returned slice. For example, an append will reallocate and copy this slice
	// instead of corrupting the neighbouring m.logs.
	return logs[:len(logs):len(logs)], nil
}

func (m *MemoryStorage) TruncateLogTo(index uint64) error {
	if index > uint64(len(m.logs)) {
		return nil
	}
	if index == 0 {
		m.logs = nil
		return nil
	}
	m.logs = m.logs[:index-1]
	return nil
}

func (m *MemoryStorage) LastIndex() (uint64, error) {
	return m.lastIndex(), nil
}

func (m *MemoryStorage) lastIndex() uint64 {
	if len(m.logs) == 0 {
		return 0
	}
	return m.logs[0].Index + uint64(len(m.logs)) - 1
}

func (m *MemoryStorage) FirstIndex() (uint64, error) {
	return 0, nil
}

func (m *MemoryStorage) SetLeaderTermStartIndex(term uint32, index uint64) error {
	m.termStartIndexMap[term] = index
	return nil
}

func (m *MemoryStorage) LeaderLastTerm() (uint32, error) {
	var lastTerm uint32
	for term := range m.termStartIndexMap {
		if term > lastTerm {
			lastTerm = term
		}
	}
	return lastTerm, nil
}

func (m *MemoryStorage) LeaderTermStartIndex(term uint32) (uint64, error) {
	return m.termStartIndexMap[term], nil
}

func (m *MemoryStorage) DeleteLeaderTermStartIndexGreaterThanTerm(term uint32) error {
	for t := range m.termStartIndexMap {
		if t > term {
			delete(m.termStartIndexMap, t)
		}
	}
	return nil
}

func (m *MemoryStorage) Len() int {
	return len(m.logs)
}
