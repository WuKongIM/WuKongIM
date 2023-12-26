package cluster

import (
	"time"

	"go.uber.org/zap/zapcore"
)

type Options struct {
	NodeID              uint64   // 节点ID
	ListenAddr          string   // 监听地址 例如： ip:port
	Seed                []string // 种子节点 例如： ip:port
	Heartbeat           time.Duration
	ElectionTimeoutTick uint32 // 选举超时tick次数，超过这个次数就开始选举
	LogLevel            int8

	OnLeaderChange func(leaderID uint64)

	clusterAddr string
	offsetPort  int
}

func NewOptions() *Options {
	return &Options{
		ListenAddr:          "0.0.0.0:1001",
		Heartbeat:           time.Millisecond * 200,
		offsetPort:          1000,
		ElectionTimeoutTick: 6,
		LogLevel:            int8(zapcore.DebugLevel),
	}
}

type Option func(*Options)

func WithSeed(seed []string) Option {
	return func(o *Options) {
		o.Seed = seed
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
