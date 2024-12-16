package reactor

import "github.com/WuKongIM/WuKongIM/pkg/wkdb"

type UserConfig struct {
	LeaderId uint64
}

type ChannelConfig struct {
	// LeaderId 频道领导节点
	LeaderId uint64
	// ChannelInfo 频道基础信息
	ChannelInfo wkdb.ChannelInfo
}
