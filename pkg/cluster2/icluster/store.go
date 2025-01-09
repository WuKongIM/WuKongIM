package icluster

import "github.com/WuKongIM/WuKongIM/pkg/wkdb"

type IStore interface {
	// SaveChannelClusterConfig 保存频道分布式配置
	SaveChannelClusterConfig(cfg wkdb.ChannelClusterConfig) error
	// 获取wukongimdb对象
	WKDB() wkdb.DB
}
