package cluster

import "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"

// ChannelMigrationStore returns the hosted ChannelV2 migration facade when the
// ChannelV2 service supports manual migration task management.
func (n *Node) ChannelMigrationStore() *channels.MigrationStore {
	if n == nil || n.channels == nil {
		return nil
	}
	service, ok := n.channels.(interface {
		MigrationStore() *channels.MigrationStore
	})
	if !ok {
		return nil
	}
	return service.MigrationStore()
}
