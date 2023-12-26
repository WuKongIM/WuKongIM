package gossip

import "time"

type Options struct {
	NodeID         uint64   // 节点ID
	ListenAddr     string   // 绑定地址 格式：ip:port
	AdvertiseAddr  string   // 广播地址 格式：ip:port
	Seed           []string // 种子节点
	ElectionTick   uint32   // 选举tick次数
	Heartbeat      time.Duration
	OnLeaderChange func(leaderID uint64)
	Epoch          uint32
	OnNodeEvent    func(event NodeEvent) // 节点事件
}

func NewOptions() *Options {
	return &Options{
		ListenAddr:    "0.0.0.0:7946",
		AdvertiseAddr: "",
		Heartbeat:     time.Millisecond * 200,
		ElectionTick:  5,
		Epoch:         0,
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

func WithEpoch(epoch uint32) Option {
	return func(o *Options) {
		o.Epoch = epoch
	}
}

func WithOnNodeEvent(fnc func(event NodeEvent)) Option {
	return func(o *Options) {
		o.OnNodeEvent = fnc
	}
}

type NodeEventType int

const (
	NodeEventJoin NodeEventType = iota
	NodeEventLeave
	NodeEventUpdate
)

type NodeEvent struct {
	NodeID    uint64        // 节点ID
	Addr      string        // 节点地址
	EventType NodeEventType // 节点事件类型
	State     NodeStateType // 节点状态
}
