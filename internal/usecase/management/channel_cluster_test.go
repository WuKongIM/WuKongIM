package management

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestGetChannelClusterSummaryAggregatesHealth(t *testing.T) {
	reader := &fakeChannelRuntimeMetaReader{
		pages: map[multiraft.SlotID]map[metadb.ChannelRuntimeMetaCursor]fakeChannelRuntimeMetaPage{
			1: {
				{}: {
					items: []metadb.ChannelRuntimeMeta{
						channelClusterMeta("healthy", 2, 1, []uint64{1, 2}, []uint64{1, 2}, 2, channel.StatusActive),
						channelClusterMeta("low-isr", 2, 1, []uint64{1, 2}, []uint64{1}, 2, channel.StatusActive),
					},
					cursor: metadb.ChannelRuntimeMetaCursor{ChannelID: "low-isr", ChannelType: 2},
					done:   true,
				},
			},
			2: {
				{}: {
					items: []metadb.ChannelRuntimeMeta{
						channelClusterMeta("no-leader", 2, 0, []uint64{1, 2}, []uint64{1}, 1, channel.StatusActive),
						channelClusterMeta("deleting", 2, 2, []uint64{1, 2}, []uint64{1}, 1, channel.StatusDeleting),
					},
					cursor: metadb.ChannelRuntimeMetaCursor{ChannelID: "deleting", ChannelType: 2},
					done:   true,
				},
				{ChannelID: "no-leader", ChannelType: 2}: {
					items: []metadb.ChannelRuntimeMeta{
						channelClusterMeta("deleting", 2, 2, []uint64{1, 2}, []uint64{1}, 1, channel.StatusDeleting),
					},
					cursor: metadb.ChannelRuntimeMetaCursor{ChannelID: "deleting", ChannelType: 2},
					done:   true,
				},
				{ChannelID: "no-leader", ChannelType: 2}: {
					items: []metadb.ChannelRuntimeMeta{
						channelClusterMeta("deleting", 2, 2, []uint64{1, 2}, []uint64{1}, 1, channel.StatusDeleting),
					},
					cursor: metadb.ChannelRuntimeMetaCursor{ChannelID: "deleting", ChannelType: 2},
					done:   true,
				},
			},
		},
	}
	app := New(Options{
		Cluster: fakeClusterReader{
			slotIDs: []multiraft.SlotID{2, 1},
		},
		ChannelRuntimeMeta: reader,
	})

	got, err := app.GetChannelClusterSummary(context.Background())

	require.NoError(t, err)
	require.Equal(t, ChannelClusterSummary{
		Total:           4,
		Healthy:         1,
		ISRInsufficient: 1,
		NoLeader:        1,
		AvgReplicas:     2,
		AvgISR:          1.25,
		LeaderDistribution: []ChannelLeaderDistribution{
			{NodeID: 1, Count: 2},
			{NodeID: 2, Count: 1},
		},
	}, got)
	require.Equal(t, []channelRuntimeMetaScanCall{
		{slotID: 1, after: metadb.ChannelRuntimeMetaCursor{}, limit: channelClusterScanPageLimit},
		{slotID: 2, after: metadb.ChannelRuntimeMetaCursor{}, limit: channelClusterScanPageLimit},
	}, reader.calls)
}

func TestListChannelClusterUnhealthyFiltersReasonsAndPaginates(t *testing.T) {
	reader := &fakeChannelRuntimeMetaReader{
		pages: map[multiraft.SlotID]map[metadb.ChannelRuntimeMetaCursor]fakeChannelRuntimeMetaPage{
			1: {
				{}: {
					items: []metadb.ChannelRuntimeMeta{
						channelClusterMeta("healthy", 2, 1, []uint64{1, 2}, []uint64{1, 2}, 2, channel.StatusActive),
						channelClusterMeta("low-isr", 2, 1, []uint64{1, 2}, []uint64{1}, 2, channel.StatusActive),
					},
					cursor: metadb.ChannelRuntimeMetaCursor{ChannelID: "low-isr", ChannelType: 2},
					done:   true,
				},
				{ChannelID: "low-isr", ChannelType: 2}: {
					items: []metadb.ChannelRuntimeMeta{},
					done:  true,
				},
			},
			2: {
				{}: {
					items: []metadb.ChannelRuntimeMeta{
						channelClusterMeta("no-leader", 2, 0, []uint64{1, 2}, []uint64{1}, 1, channel.StatusActive),
						channelClusterMeta("deleting", 2, 2, []uint64{1, 2}, []uint64{1}, 1, channel.StatusDeleting),
					},
					cursor: metadb.ChannelRuntimeMetaCursor{ChannelID: "deleting", ChannelType: 2},
					done:   true,
				},
				{ChannelID: "no-leader", ChannelType: 2}: {
					items: []metadb.ChannelRuntimeMeta{
						channelClusterMeta("deleting", 2, 2, []uint64{1, 2}, []uint64{1}, 1, channel.StatusDeleting),
					},
					cursor: metadb.ChannelRuntimeMetaCursor{ChannelID: "deleting", ChannelType: 2},
					done:   true,
				},
			},
		},
	}
	app := New(Options{
		Cluster: fakeClusterReader{
			slotIDs: []multiraft.SlotID{1, 2},
		},
		ChannelRuntimeMeta: reader,
	})

	got, err := app.ListChannelClusterUnhealthy(context.Background(), ListChannelClusterUnhealthyRequest{Limit: 2})

	require.NoError(t, err)
	require.Equal(t, ListChannelClusterUnhealthyResponse{
		Items: []ChannelClusterUnhealthyItem{
			{
				ChannelRuntimeMeta: ChannelRuntimeMeta{
					ChannelID:     "low-isr",
					ChannelType:   2,
					SlotID:        1,
					Leader:        1,
					Replicas:      []uint64{1, 2},
					ISR:           []uint64{1},
					MinISR:        2,
					MaxMessageSeq: uint64Ptr(0),
					Status:        "active",
				},
				Reasons: []string{ChannelClusterUnhealthyReasonISRInsufficient},
			},
			{
				ChannelRuntimeMeta: ChannelRuntimeMeta{
					ChannelID:     "no-leader",
					ChannelType:   2,
					SlotID:        2,
					Leader:        0,
					Replicas:      []uint64{1, 2},
					ISR:           []uint64{1},
					MinISR:        1,
					MaxMessageSeq: uint64Ptr(0),
					Status:        "active",
				},
				Reasons: []string{ChannelClusterUnhealthyReasonNoLeader},
			},
		},
		HasMore:    true,
		NextCursor: ChannelRuntimeMetaListCursor{SlotID: 2, ChannelID: "no-leader", ChannelType: 2},
	}, got)

	next, err := app.ListChannelClusterUnhealthy(context.Background(), ListChannelClusterUnhealthyRequest{
		Limit:  2,
		Cursor: got.NextCursor,
	})

	require.NoError(t, err)
	require.Equal(t, ListChannelClusterUnhealthyResponse{
		Items: []ChannelClusterUnhealthyItem{{
			ChannelRuntimeMeta: ChannelRuntimeMeta{
				ChannelID:     "deleting",
				ChannelType:   2,
				SlotID:        2,
				Leader:        2,
				Replicas:      []uint64{1, 2},
				ISR:           []uint64{1},
				MinISR:        1,
				MaxMessageSeq: uint64Ptr(0),
				Status:        "deleting",
			},
			Reasons: []string{ChannelClusterUnhealthyReasonStatusNotActive},
		}},
	}, next)
}

func TestListChannelClusterUnhealthyRejectsInvalidLimit(t *testing.T) {
	app := New(Options{
		Cluster:            fakeClusterReader{slotIDs: []multiraft.SlotID{1}},
		ChannelRuntimeMeta: &fakeChannelRuntimeMetaReader{},
	})

	_, err := app.ListChannelClusterUnhealthy(context.Background(), ListChannelClusterUnhealthyRequest{Limit: 0})

	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}

func channelClusterMeta(channelID string, channelType int64, leader uint64, replicas []uint64, isr []uint64, minISR int64, status channel.Status) metadb.ChannelRuntimeMeta {
	return metadb.ChannelRuntimeMeta{
		ChannelID:   channelID,
		ChannelType: channelType,
		Leader:      leader,
		Replicas:    replicas,
		ISR:         isr,
		MinISR:      minISR,
		Status:      uint8(status),
	}
}
