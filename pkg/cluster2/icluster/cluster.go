package icluster

import "github.com/WuKongIM/WuKongIM/pkg/wkdb"

type ICluster interface {

	// GetOrCreateChannelClusterConfigFromSlotLeader 从频道槽领导获取或创建频道分布式配置
	GetOrCreateChannelClusterConfigFromSlotLeader(channelId string, channelType uint8) (wkdb.ChannelClusterConfig, error)
}
