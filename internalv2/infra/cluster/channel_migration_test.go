package cluster

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/channels"
)

func TestNewChannelMigrationStoreReturnsNodeStore(t *testing.T) {
	store := channels.NewMigrationStore(channels.MigrationStoreConfig{})
	node := fakeChannelMigrationStoreNode{store: store}

	got := NewChannelMigrationStore(node)
	if got != store {
		t.Fatalf("NewChannelMigrationStore() = %p, want %p", got, store)
	}
}

type fakeChannelMigrationStoreNode struct {
	store *channels.MigrationStore
}

func (f fakeChannelMigrationStoreNode) ChannelMigrationStore() *channels.MigrationStore {
	return f.store
}
