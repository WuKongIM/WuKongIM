package cluster

import "github.com/WuKongIM/WuKongIM/pkg/wkdb"

// GetOrCreateChannelClusterConfig 获取或创建频道分布式配置
func (s *Server) GetOrCreateChannelClusterConfigFromSlotLeader(channelId string, channelType uint8) (wkdb.ChannelClusterConfig, error) {

	return wkdb.EmptyChannelClusterConfig, nil
}
