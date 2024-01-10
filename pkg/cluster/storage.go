package cluster

import "github.com/WuKongIM/WuKongIM/pkg/cluster/replica"

// 日志分区存储
type IShardLogStorage interface {
	// AppendLog 追加日志
	AppendLog(shardNo string, log replica.Log) error
	// 获取日志
	GetLogs(shardNo string, startLogIndex uint64, limit uint32) ([]replica.Log, error)
	// 最后一条日志的索引
	LastIndex(shardNo string) (uint64, error)
	// 获取第一条日志的索引
	FirstIndex(shardNo string) (uint64, error)
	// 设置成功被状态机应用的日志索引
	SetAppliedIndex(shardNo string, index uint64) error
}

type proxyReplicaStorage struct {
	shardNo string
	storage IShardLogStorage
}

func newProxyReplicaStorage(shardNo string, storage IShardLogStorage) *proxyReplicaStorage {
	return &proxyReplicaStorage{
		shardNo: shardNo,
		storage: storage,
	}
}

func (p *proxyReplicaStorage) AppendLog(log replica.Log) error {
	return p.storage.AppendLog(p.shardNo, log)
}
func (p *proxyReplicaStorage) GetLogs(startLogIndex uint64, limit uint32) ([]replica.Log, error) {
	return p.storage.GetLogs(p.shardNo, startLogIndex, limit)
}

func (p *proxyReplicaStorage) LastIndex() (uint64, error) {
	return p.storage.LastIndex(p.shardNo)
}

func (p *proxyReplicaStorage) FirstIndex() (uint64, error) {
	return p.storage.FirstIndex(p.shardNo)
}

func (p *proxyReplicaStorage) SetAppliedIndex(index uint64) error {
	return p.storage.SetAppliedIndex(p.shardNo, index)
}
