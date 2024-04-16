package cluster

import (
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
)

type Options struct {
	NodeId              uint64
	Addr                string // 分布式监听地址
	InitNodes           map[uint64]string
	SlotCount           uint32 // 槽位数量
	SlotMaxReplicaCount uint32 // 每个槽位最大副本数量
	ConfigDir           string
	ReqTimeout          time.Duration
	// LogSyncLimitSizeOfEach 每次日志同步大小
	LogSyncLimitSizeOfEach int
	// SendQueueLength 是用于在节点主机之间交换消息的发送队列的长度。
	SendQueueLength int
	// MaxSendQueueSize 是每个发送队列的最大大小(以字节为单位)。
	// 如果达到最大大小,后续复制消息将被丢弃以限制内存使用。
	// 当设置为0时,表示发送队列大小没有限制。
	MaxSendQueueSize uint64
	// MaxMessageBatchSize 节点之间每次发送消息的最大大小（单位字节）
	MaxMessageBatchSize uint64
	// ReceiveQueueLength 副本接收队列的长度。
	ReceiveQueueLength uint64
	// LazyFreeCycle defines how often should entry queue and message queue
	// to be freed.
	LazyFreeCycle uint64
	// MaxReceiveQueueSize is the maximum size in bytes of each receive queue.
	// Once the maximum size is reached, further replication messages will be
	// dropped to restrict memory usage. When set to 0, it means the queue size
	// is unlimited.
	MaxReceiveQueueSize uint64
	// InitialTaskQueueCap is the initial capacity of the task queue.
	InitialTaskQueueCap int
	// SlotLogStorage 槽位日志存储
	SlotLogStorage IShardLogStorage
	// MessageLogStorage 消息日志存储
	MessageLogStorage IShardLogStorage

	OnSlotApply func(slotId uint32, logs []replica.Log) error

	// Send 发送消息
	Send func(shardType ShardType, m reactor.Message)
}

func NewOptions(opt ...Option) *Options {
	opts := &Options{
		SlotCount:              128,
		SlotMaxReplicaCount:    3,
		ConfigDir:              "clusterconfig",
		ReqTimeout:             3 * time.Second,
		SendQueueLength:        1024 * 10,
		MaxMessageBatchSize:    64 * 1024 * 1024, // 64M
		ReceiveQueueLength:     1024,
		LazyFreeCycle:          1,
		InitialTaskQueueCap:    24,
		LogSyncLimitSizeOfEach: 1024 * 1024 * 20, // 20M
		Addr:                   "tcp://127.0.0.1:10001",
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

type Option func(*Options)

func WithNodeId(nodeId uint64) Option {
	return func(o *Options) {
		o.NodeId = nodeId
	}
}

func WithInitNodes(initNodes map[uint64]string) Option {
	return func(o *Options) {
		o.InitNodes = initNodes
	}

}
func WithSlotCount(slotCount uint32) Option {
	return func(o *Options) {
		o.SlotCount = slotCount
	}
}
func WithSlotMaxReplicaCount(slotMaxReplicaCount uint32) Option {
	return func(o *Options) {
		o.SlotMaxReplicaCount = slotMaxReplicaCount
	}
}

func WithOnSlotApply(fn func(slotId uint32, logs []replica.Log) error) Option {
	return func(o *Options) {
		o.OnSlotApply = fn
	}
}

func WithLogSyncLimitSizeOfEach(size int) Option {
	return func(o *Options) {
		o.LogSyncLimitSizeOfEach = size
	}
}

func WithSendQueueLength(length int) Option {
	return func(o *Options) {
		o.SendQueueLength = length
	}
}

func WithMaxMessageBatchSize(size uint64) Option {
	return func(o *Options) {
		o.MaxMessageBatchSize = size
	}
}

func WithReceiveQueueLength(length uint64) Option {
	return func(o *Options) {
		o.ReceiveQueueLength = length
	}
}

func WithLazyFreeCycle(cycle uint64) Option {
	return func(o *Options) {
		o.LazyFreeCycle = cycle
	}
}

func WithMaxReceiveQueueSize(size uint64) Option {
	return func(o *Options) {
		o.MaxReceiveQueueSize = size
	}
}

func WithInitialTaskQueueCap(cap int) Option {
	return func(o *Options) {
		o.InitialTaskQueueCap = cap
	}
}

func WithSlotLogStorage(storage IShardLogStorage) Option {
	return func(o *Options) {
		o.SlotLogStorage = storage
	}
}

func WithReqTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		o.ReqTimeout = timeout
	}
}

func WithSend(fn func(shardType ShardType, m reactor.Message)) Option {
	return func(o *Options) {
		o.Send = fn
	}
}

func WithAddr(addr string) Option {
	return func(o *Options) {
		if !strings.HasPrefix(addr, "tcp://") {
			addr = "tcp://" + addr
		}
		o.Addr = addr
	}
}

func WithConfigDir(dir string) Option {
	return func(o *Options) {
		o.ConfigDir = dir
	}
}
