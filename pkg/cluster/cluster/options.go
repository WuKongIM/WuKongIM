package cluster

import "time"

type Options struct {
	NodeID                       uint64
	InitNodes                    map[uint64]string // 初始化节点列表
	ChannelGroupScanInterval     time.Duration
	ShardLogStorage              IShardLogStorage
	MaxChannelActivitiesPerGroup int // 每个channelGroup最大处理活动的channel数量
	Transport                    ITransport
	ChannelInactiveTimeout       time.Duration // channel不活跃超时时间, 如果频道超过这个时间没有活跃, 则会被移除，等下次活跃时会重新加入
	SlotInactiveTimeout          time.Duration // slot不活跃超时时间，如果槽超过这个时间没有活跃，则会被移除，等下次活跃时会重新加入
	AdvanceCountOfBatch          int           // 每批次处理Advance的最大数量
	DataDir                      string        // 数据存储目录
	SlotCount                    uint32        // 槽数量
	SlotMaxReplicaCount          uint32        // 每个槽位最大副本数量
	proposeTimeout               time.Duration
}

func NewOptions() *Options {
	return &Options{
		ChannelGroupScanInterval:     time.Millisecond * 200,
		SlotInactiveTimeout:          time.Millisecond * 200,
		MaxChannelActivitiesPerGroup: 1000,
		ChannelInactiveTimeout:       time.Hour * 1,
		AdvanceCountOfBatch:          50,
		SlotCount:                    256,
		SlotMaxReplicaCount:          3,
		proposeTimeout:               time.Second * 5,
	}
}

func (o *Options) Replicas() []uint64 {
	replicas := make([]uint64, 0, len(o.InitNodes))
	for nodeID := range o.InitNodes {
		replicas = append(replicas, nodeID)
	}
	return replicas
}
