package channels

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

type recreatedRuntimeStore struct {
	uncertainAppliedRuntimeMetaBatchStore
}

func (s *recreatedRuntimeStore) CreateChannelRuntimeMetaBatch(_ context.Context, _ routing.Route, items []RuntimeMetaCreateItem) ([]RuntimeMetaCreateResult, error) {
	s.row = metadb.NormalizeChannelRuntimeMeta(items[0].Meta)
	s.row.ChannelEpoch, s.row.LeaderEpoch, s.row.RouteGeneration, s.row.DirectoryGeneration = 77, 77, 77, 77
	return []RuntimeMetaCreateResult{{HashSlot: items[0].HashSlot, ChannelID: s.row.ChannelID, ChannelType: s.row.ChannelType, Created: true}}, nil
}

func TestRuntimeIncarnationCreateUsesCommittedVersions(t *testing.T) {
	store := &recreatedRuntimeStore{}
	route := routing.Route{HashSlot: 7, SlotID: 3, Leader: 1, LeaderTerm: 4, ConfigEpoch: 2, Revision: 9}
	source := NewSlotMetaSource(store, SlotMetaSourceOptions{Router: fixedRuntimeMetaBatchRouter{route: route}, BatchStore: store, Placement: fakePlacementResolver{placement: ChannelPlacement{Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, MinISR: 2}}})
	t.Cleanup(func() { require.NoError(t, source.Close()) })
	id := ch.ChannelID{ID: "recreated", Type: 1}
	key := metadb.ChannelKey{ChannelID: id.ID, ChannelType: int64(id.Type)}
	owner := &metaCreateSlotOwner{batcher: source.batcher, slotID: 3}
	results := owner.submit([]*metaCreateEntry{{key: key, item: RuntimeMetaCreateItem{HashSlot: 7, Meta: metadb.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: int64(id.Type)}}}})
	require.NoError(t, results[key].err)
	require.Equal(t, uint64(77), results[key].meta.RouteGeneration, "successful create does not prove the candidate survived Slot normalization")
	require.Equal(t, store.row, results[key].meta)
	require.Equal(t, 1, store.readCalls)
}
