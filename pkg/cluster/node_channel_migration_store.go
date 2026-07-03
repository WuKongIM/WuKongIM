package cluster

import "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"

// ChannelMigrationStore returns the hosted Channel migration facade when the
// Channel service supports manual migration task management.
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
