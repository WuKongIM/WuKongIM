package cluster

import "time"

type Options struct {
	NodeID                       uint64
	ChannelGroupScanInterval     time.Duration
	ShardLogStorage              IShardLogStorage
	MaxChannelActivitiesPerGroup int // 每个channelGroup最大处理活动的channel数量
	Transport                    ITransport
	ChannelInactiveTimeout       time.Duration // channel不活跃超时时间, 如果频道超过这个时间没有活跃, 则会被移除，等下次活跃时会重新加入
	AdvanceCountOfBatch          int           // 每批次处理Advance的最大数量
}

func NewOptions() *Options {
	return &Options{
		ChannelGroupScanInterval:     time.Millisecond * 200,
		MaxChannelActivitiesPerGroup: 1000,
		ChannelInactiveTimeout:       time.Hour * 1,
		AdvanceCountOfBatch:          50,
	}
}
