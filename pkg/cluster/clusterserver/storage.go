package cluster

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
)

// 日志分区存储
type IShardLogStorage interface {
	Append(req reactor.AppendLogReq) error

	// AppendLogBatch 批量追加日志
	// AppendLogBatch(reqs []reactor.AppendLogReq) error
	// TruncateLogTo 截断日志, 从index开始截断,index不能为0 （保留下来的内容不包含index）
	TruncateLogTo(shardNo string, index uint64) error
	// 获取日志 [startLogIndex, endLogIndex) 之间的日志
	// limitSize 限制返回的日志大小 (字节) 0表示不限制
	Logs(shardNo string, startLogIndex uint64, endLogIndex uint64, limitSize uint64) ([]replica.Log, error)
	// 最后一条日志的索引
	LastIndex(shardNo string) (uint64, error)
	// LastIndexAndTerm 获取最后一条日志的索引和任期
	LastIndexAndTerm(shardNo string) (uint64, uint32, error)
	// SetLastIndex 设置最后一条日志的索引
	// SetLastIndex(shardNo string, index uint64) error
	// SetAppliedIndex(shardNo string, index uint64) error
	//	 获取最后一条日志的索引和追加时间
	//
	LastIndexAndAppendTime(shardNo string) (lastMsgSeq uint64, lastAppendTime uint64, err error)

	// SetLeaderTermStartIndex 设置领导任期开始的第一条日志索引
	SetLeaderTermStartIndex(shardNo string, term uint32, index uint64) error
	// LeaderLastTerm 获取最新的本地保存的领导任期
	LeaderLastTerm(shardNo string) (uint32, error)
	// LeaderTermStartIndex 获取领导任期开始的第一条日志索引
	LeaderTermStartIndex(shardNo string, term uint32) (uint64, error)

	// LeaderLastTermGreaterThan 获取大于或等于传入的term的最大的term
	LeaderLastTermGreaterThan(shardNo string, term uint32) (uint32, error)

	// 删除比传入的term大的的LeaderTermStartIndex记录
	DeleteLeaderTermStartIndexGreaterThanTerm(shardNo string, term uint32) error

	SetAppliedIndex(shardNo string, index uint64) error

	AppliedIndex(shardNo string) (uint64, error)

	Open() error

	Close() error
}

type MemoryShardLogStorage struct {
	storage                 map[string][]replica.Log
	leaderTermStartIndexMap map[string]map[uint32]uint64
}

func NewMemoryShardLogStorage() *MemoryShardLogStorage {
	return &MemoryShardLogStorage{
		storage:                 make(map[string][]replica.Log),
		leaderTermStartIndexMap: make(map[string]map[uint32]uint64),
	}
}

func (m *MemoryShardLogStorage) AppendLog(shardNo string, logs []replica.Log) error {
	m.storage[shardNo] = append(m.storage[shardNo], logs...)
	return nil
}

func (m *MemoryShardLogStorage) TruncateLogTo(shardNo string, index uint64) error {
	if index == 0 {
		return errors.New("index can not be 0")
	}
	logs := m.storage[shardNo]
	if len(logs) > 0 {
		m.storage[shardNo] = logs[:index-1]
	}
	return nil
}

func (m *MemoryShardLogStorage) Logs(shardNo string, startLogIndex uint64, endLogIndex uint64, limitSize uint64) ([]replica.Log, error) {
	logs := m.storage[shardNo]
	if len(logs) == 0 {
		return nil, nil
	}
	if endLogIndex == 0 {
		return logs[startLogIndex-1:], nil
	}
	if startLogIndex > uint64(len(logs)) {
		return nil, nil
	}
	if endLogIndex > uint64(len(logs)) {
		return logs[startLogIndex-1:], nil
	}
	return logs[startLogIndex-1 : endLogIndex-1], nil
}

func (m *MemoryShardLogStorage) LastIndex(shardNo string) (uint64, error) {
	logs := m.storage[shardNo]
	if len(logs) == 0 {
		return 0, nil
	}
	return uint64(len(logs) - 1), nil
}

func (m *MemoryShardLogStorage) SetLastIndex(shardNo string, index uint64) error {

	return nil
}

func (m *MemoryShardLogStorage) SetAppliedIndex(shardNo string, index uint64) error {
	return nil
}

func (m *MemoryShardLogStorage) AppliedIndex(shardNo string) (uint64, error) {
	return 0, nil
}

func (m *MemoryShardLogStorage) LastIndexAndAppendTime(shardNo string) (uint64, uint64, error) {
	return 0, 0, nil
}

func (m *MemoryShardLogStorage) SetLeaderTermStartIndex(shardNo string, term uint32, index uint64) error {
	if _, ok := m.leaderTermStartIndexMap[shardNo]; !ok {
		m.leaderTermStartIndexMap[shardNo] = make(map[uint32]uint64)
	}
	m.leaderTermStartIndexMap[shardNo][term] = index
	return nil
}

func (m *MemoryShardLogStorage) LeaderLastTerm(shardNo string) (uint32, error) {
	if _, ok := m.leaderTermStartIndexMap[shardNo]; !ok {
		return 0, nil
	}
	var maxTerm uint32
	for term := range m.leaderTermStartIndexMap[shardNo] {
		if term > maxTerm {
			maxTerm = term
		}
	}
	return maxTerm, nil
}

func (m *MemoryShardLogStorage) LeaderTermStartIndex(shardNo string, term uint32) (uint64, error) {
	if _, ok := m.leaderTermStartIndexMap[shardNo]; !ok {
		return 0, nil
	}
	return m.leaderTermStartIndexMap[shardNo][term], nil
}

func (m *MemoryShardLogStorage) DeleteLeaderTermStartIndexGreaterThanTerm(shardNo string, term uint32) error {
	if _, ok := m.leaderTermStartIndexMap[shardNo]; !ok {
		return nil
	}
	for t := range m.leaderTermStartIndexMap[shardNo] {
		if t > term {
			delete(m.leaderTermStartIndexMap[shardNo], t)
		}
	}
	return nil
}

func (m *MemoryShardLogStorage) Open() error {
	return nil
}

func (m *MemoryShardLogStorage) Close() error {

	return nil
}

type proxyReplicaStorage struct {
	storage IShardLogStorage
	shardNo string
}

func newProxyReplicaStorage(shardNo string, storage IShardLogStorage) *proxyReplicaStorage {
	return &proxyReplicaStorage{
		storage: storage,
		shardNo: shardNo,
	}
}

func (p *proxyReplicaStorage) TruncateLogTo(index uint64) error {
	return p.storage.TruncateLogTo(p.shardNo, index)
}

func (p *proxyReplicaStorage) Logs(startLogIndex uint64, endLogIndex uint64, limitSize uint64) ([]replica.Log, error) {
	return p.storage.Logs(p.shardNo, startLogIndex, endLogIndex, limitSize)
}

func (p *proxyReplicaStorage) LastIndexAndTerm() (uint64, uint32, error) {
	return p.storage.LastIndexAndTerm(p.shardNo)
}

func (p *proxyReplicaStorage) FirstIndex() (uint64, error) {
	return 0, nil
}

func (p *proxyReplicaStorage) LastIndexAndAppendTime() (uint64, uint64, error) {
	return p.storage.LastIndexAndAppendTime(p.shardNo)
}

func (p *proxyReplicaStorage) SetLeaderTermStartIndex(term uint32, index uint64) error {
	return p.storage.SetLeaderTermStartIndex(p.shardNo, term, index)
}

func (p *proxyReplicaStorage) LeaderLastTerm() (uint32, error) {
	return p.storage.LeaderLastTerm(p.shardNo)
}

func (p *proxyReplicaStorage) LeaderTermStartIndex(term uint32) (uint64, error) {
	return p.storage.LeaderTermStartIndex(p.shardNo, term)
}

func (p *proxyReplicaStorage) DeleteLeaderTermStartIndexGreaterThanTerm(term uint32) error {
	return p.storage.DeleteLeaderTermStartIndexGreaterThanTerm(p.shardNo, term)
}
