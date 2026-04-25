package cluster

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestClusterHashSlotsOfReturnsAssignedHashSlots(t *testing.T) {
	table := NewHashSlotTable(8, 2)
	cluster := &Cluster{
		router: NewRouter(table, 1, nil),
	}

	require.Equal(t, table.HashSlotsOf(multiraft.SlotID(2)), cluster.HashSlotsOf(multiraft.SlotID(2)))
}
