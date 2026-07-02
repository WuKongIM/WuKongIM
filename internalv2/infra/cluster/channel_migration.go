package cluster

import (
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/channels"
)

// ChannelMigrationStoreNode exposes the ChannelV2 migration store owned by clusterv2.
type ChannelMigrationStoreNode interface {
	ChannelMigrationStore() *channels.MigrationStore
}

// NewChannelMigrationStore adapts the clusterv2 ChannelV2 migration store to manager usecases.
func NewChannelMigrationStore(node ChannelMigrationStoreNode) managementusecase.ChannelMigrationStore {
	if node == nil {
		return nil
	}
	return node.ChannelMigrationStore()
}
