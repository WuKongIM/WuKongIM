package cluster

import (
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/auth"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"go.uber.org/zap/zapcore"
)

type Options struct {
	NodeId        uint64
	Role          pb.NodeRole // 节点角色
	Addr          string      // 分布式监听地址
	ServerAddr    string      // 分布式可访问地址
	ApiServerAddr string      // api服务地址
	JaegerApiUrl  string      // jaeger api地址
	ServiceName   string      // 服务名称
	AppVersion    string      // 当前应用版本
	// InitNodes 集群初始节点，key为节点id，value为节点内网通信地址
	InitNodes map[uint64]string
	// SlotCount 槽位数量
	SlotCount uint32
	// SlotMaxReplicaCount 每个槽位最大副本数量
	SlotMaxReplicaCount uint32
	// Seed  种子节点，可以引导新节点加入集群  格式：nodeId@ip:port （nodeId为种子节点的nodeId）
	Seed                  string
	DataDir               string
	ReqTimeout            time.Duration         // 请求超时时间
	ProposeTimeout        time.Duration         // 提案超时时间
	LogLevel              zapcore.Level         // 日志级别
	ChannelClusterStorage ChannelClusterStorage // 频道分布式存储
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
	OnSlotApply       func(slotId uint32, logs []replica.Log) error
	// Send 发送消息
	Send func(shardType ShardType, m reactor.Message)
	// ChannelElectionPoolSize 频道选举协程池大小(意味着同时在选举的频道数量)
	ChannelElectionPoolSize int
	// MaxChannelElectionBatchLen 批量选举，每次最多选举多少个频道（默认100）
	MaxChannelElectionBatchLen int

	// ChannelMaxReplicaCount 频道最大副本数量
	ChannelMaxReplicaCount int

	// ChannelLoadPoolSize 加载频道的协程池大小
	ChannelLoadPoolSize int

	//  LeaderTransferMinLogGap  转移领导的最小日志差距（ 当日志差距小于这个值时，可以进行领导转移了）
	LeaderTransferMinLogGap uint64

	// LearnerMinLogGap  学习者最小日志差距（ 当日志差距小于这个值时，可以认为已学习达到要求）
	LearnerMinLogGap uint64

	DB wkdb.DB

	SlotDbShardNum int // 槽位数据库分片数量

	PageSize int

	TickInterval          time.Duration // 分布式tick间隔
	HeartbeatIntervalTick int           // 心跳间隔tick
	ElectionIntervalTick  int           // 选举间隔tick

	ChannelReactorSubCount int // 频道reactor sub的数量
	SlotReactorSubCount    int // 槽reactor sub的数量

	PongMaxTick int // 节点超过多少tick没有回应心跳就认为是掉线

	Auth auth.AuthConfig

	LokiUrl string // loki url example: http://localhost:3100
	LokiJob string
}

func NewOptions(opt ...Option) *Options {
	opts := &Options{
		SlotCount:                  128,
		SlotMaxReplicaCount:        3,
		DataDir:                    "clusterdata",
		ReqTimeout:                 10 * time.Second,
		ProposeTimeout:             5 * time.Minute,
		SendQueueLength:            1024 * 10,
		MaxMessageBatchSize:        64 * 1024 * 1024, // 64M
		ReceiveQueueLength:         1024,
		LazyFreeCycle:              1,
		InitialTaskQueueCap:        24,
		LogSyncLimitSizeOfEach:     1024 * 1024 * 20, // 20M
		Addr:                       "tcp://127.0.0.1:11110",
		ChannelElectionPoolSize:    1000,
		MaxChannelElectionBatchLen: 100,
		ChannelMaxReplicaCount:     3,
		ChannelLoadPoolSize:        1000,
		LeaderTransferMinLogGap:    20,
		LearnerMinLogGap:           100,
		PageSize:                   20,

		TickInterval:          150 * time.Millisecond,
		HeartbeatIntervalTick: 1,
		ElectionIntervalTick:  10,

		ChannelReactorSubCount: 128,
		SlotReactorSubCount:    128,
		PongMaxTick:            30,
		SlotDbShardNum:         8,

		LokiJob: "wk",
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

func WithDataDir(dir string) Option {
	return func(o *Options) {
		o.DataDir = dir
	}
}

func WithChannelClusterStorage(storage ChannelClusterStorage) Option {
	return func(o *Options) {
		o.ChannelClusterStorage = storage
	}
}

func WithProposeTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		o.ProposeTimeout = timeout
	}
}

func WithChannelElectionPoolSize(size int) Option {
	return func(o *Options) {
		o.ChannelElectionPoolSize = size
	}
}

func WithMaxChannelElectionBatchLen(len int) Option {
	return func(o *Options) {
		o.MaxChannelElectionBatchLen = len
	}
}

func WithChannelMaxReplicaCount(count int) Option {
	return func(o *Options) {
		o.ChannelMaxReplicaCount = count
	}
}

func WithMaxSendQueueSize(size uint64) Option {
	return func(o *Options) {
		o.MaxSendQueueSize = size
	}
}

func WithRole(role pb.NodeRole) Option {
	return func(o *Options) {
		o.Role = role
	}
}

func WithSeed(seed string) Option {
	return func(o *Options) {
		o.Seed = seed
	}
}

func WithServerAddr(addr string) Option {
	return func(o *Options) {
		o.ServerAddr = addr
	}
}

func WithJaegerApiUrl(url string) Option {
	return func(o *Options) {
		o.JaegerApiUrl = url
	}
}

func WithMessageLogStorage(storage IShardLogStorage) Option {
	return func(o *Options) {
		o.MessageLogStorage = storage
	}
}

func WithApiServerAddr(addr string) Option {
	return func(o *Options) {
		o.ApiServerAddr = addr
	}
}

func WithLogLevel(level zapcore.Level) Option {
	return func(o *Options) {
		o.LogLevel = level
	}
}

func WithAppVersion(version string) Option {
	return func(o *Options) {
		o.AppVersion = version
	}
}

func WithDB(db wkdb.DB) Option {
	return func(o *Options) {
		o.DB = db
	}
}

func WithPageSize(pageSize int) Option {

	return func(o *Options) {
		o.PageSize = pageSize
	}
}

func WithHeartbeatIntervalTick(interval int) Option {
	return func(o *Options) {
		o.HeartbeatIntervalTick = interval
	}
}

func WithElectionIntervalTick(interval int) Option {
	return func(o *Options) {
		o.ElectionIntervalTick = interval
	}
}

func WithTickInterval(interval time.Duration) Option {
	return func(o *Options) {
		o.TickInterval = interval
	}
}

func WithChannelReactorSubCount(count int) Option {
	return func(o *Options) {
		o.ChannelReactorSubCount = count
	}
}

func WithSlotReactorSubCount(count int) Option {
	return func(o *Options) {
		o.SlotReactorSubCount = count
	}
}

func WithChannelLoadPoolSize(size int) Option {
	return func(o *Options) {
		o.ChannelLoadPoolSize = size
	}
}

func WithLeaderTransferMinLogGap(gap uint64) Option {
	return func(o *Options) {
		o.LeaderTransferMinLogGap = gap
	}
}

func WithLearnerMinLogGap(gap uint64) Option {
	return func(o *Options) {
		o.LearnerMinLogGap = gap
	}
}

func WithPongMaxTick(tick int) Option {
	return func(o *Options) {
		o.PongMaxTick = tick
	}
}

func WithSlotDbShardNum(num int) Option {
	return func(o *Options) {
		o.SlotDbShardNum = num
	}
}

func WithAuth(auth auth.AuthConfig) Option {
	return func(o *Options) {
		o.Auth = auth
	}
}

func WithServiceName(serviceName string) Option {
	return func(o *Options) {
		o.ServiceName = serviceName
	}
}

func WithLokiUrl(url string) Option {
	return func(o *Options) {
		o.LokiUrl = url
	}
}

func WithLokiJob(job string) Option {
	return func(o *Options) {
		o.LokiJob = job
	}
}
