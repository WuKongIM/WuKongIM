package service

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

func TestServiceRetentionDelegatesToGroup(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := serviceRetentionMeta("service-retention")
	seedServiceRetentionRecords(t, factory, meta, 2)
	clusterAPI, err := New(Config{LocalNode: 1, Store: factory, ReactorCount: 1})
	require.NoError(t, err)
	defer clusterAPI.Close()
	require.NoError(t, clusterAPI.ApplyMeta(meta))

	retention, ok := clusterAPI.(ch.RetentionRuntime)
	require.True(t, ok)
	view, err := retention.RetentionView(context.Background(), meta.ID)
	require.NoError(t, err)
	require.Equal(t, uint64(2), view.LEO)
	require.Equal(t, uint64(0), view.PhysicalRetentionThroughSeq)

	applied, err := retention.ApplyRetentionBoundary(context.Background(), ch.RetentionApplyRequest{
		ChannelID:  meta.ID,
		ThroughSeq: 1,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(1), applied.LocalRetentionThroughSeq)
	require.Equal(t, uint64(1), applied.PhysicalRetentionThroughSeq)
	require.Equal(t, 1, applied.Deleted)
}

func serviceRetentionMeta(id string) ch.Meta {
	channelID := ch.ChannelID{ID: id, Type: 1}
	return ch.Meta{
		Key:         ch.ChannelKeyForID(channelID),
		ID:          channelID,
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []ch.NodeID{1},
		ISR:         []ch.NodeID{1},
		MinISR:      1,
		Status:      ch.StatusActive,
	}
}

func seedServiceRetentionRecords(t *testing.T, factory store.Factory, meta ch.Meta, records int) {
	t.Helper()
	cs, err := factory.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, err)
	items := make([]ch.Record, 0, records)
	for i := 1; i <= records; i++ {
		items = append(items, ch.Record{ID: uint64(i), Payload: []byte{byte(i)}, SizeBytes: 1})
	}
	_, err = cs.AppendLeader(context.Background(), store.AppendLeaderRequest{Records: items})
	require.NoError(t, err)
	require.NoError(t, cs.StoreCheckpoint(context.Background(), ch.Checkpoint{HW: uint64(records)}))
}
