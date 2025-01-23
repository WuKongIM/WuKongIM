package clusterconfig

import "github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"

type IEvent interface {
	// OnConfigChange 配置变更（不要阻塞此方法）
	OnConfigChange(cfg *types.Config)
}
