package cluster

import (
	"time"

	"go.uber.org/zap/zapcore"
)

type Options struct {
	NodeID              uint64            // 节点ID
	ListenAddr          string            // 监听地址 例如： ip:port
	InitNodes           map[uint64]string // 初始节点 例如： key为节点ID value为 ip:port
	Join                string            // 加入的节点 例如： ip:port
	Heartbeat           time.Duration
	ElectionTimeoutTick uint32 // 选举超时tick次数，超过这个次数就开始选举
	LogLevel            int8
	DataDir             string // 数据存储目录

	OnLeaderChange func(leaderID uint64)

	SlotCount        uint32 // 槽的数量
	SlotReplicaCount uint32 // 槽的副本数量

	clusterAddr string
	offsetPort  int
}

func NewOptions() *Options {
	return &Options{
		ListenAddr:          "0.0.0.0:1001",
		Heartbeat:           time.Millisecond * 500,
		offsetPort:          1000,
		ElectionTimeoutTick: 6,
		LogLevel:            int8(zapcore.DebugLevel),
		SlotCount:           256,
		SlotReplicaCount:    3,
	}
}

type Option func(*Options)

func WithJoin(join string) Option {
	return func(o *Options) {
		o.Join = join
	}
}

func WithOnLeaderChange(fnc func(leaderID uint64)) Option {

	return func(o *Options) {
		o.OnLeaderChange = fnc
	}
}

func WithElectionTimeoutTick(tick uint32) Option {
	return func(o *Options) {
		o.ElectionTimeoutTick = tick
	}
}

func WithHeartbeat(heartbeat time.Duration) Option {
	return func(o *Options) {
		o.Heartbeat = heartbeat
	}
}

func WithListenAddr(addr string) Option {
	return func(o *Options) {
		o.ListenAddr = addr
	}
}
func WithInitNodes(nodes map[uint64]string) Option {
	return func(o *Options) {
		o.InitNodes = nodes
	}
}

func WithDataDir(dataDir string) Option {
	return func(o *Options) {
		o.DataDir = dataDir
	}
}

func WithSlotCount(slotCount uint32) Option {
	return func(o *Options) {
		o.SlotCount = slotCount
	}
}
