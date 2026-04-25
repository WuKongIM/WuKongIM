package management

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestListChannelRuntimeMetaReturnsFirstPageInGlobalOrder(t *testing.T) {
	reader := &fakeChannelRuntimeMetaReader{
		pages: map[multiraft.SlotID]map[metadb.ChannelRuntimeMetaCursor]fakeChannelRuntimeMetaPage{
			1: {
				{}: {
					items: []metadb.ChannelRuntimeMeta{
						testChannelRuntimeMeta("s1-a", 1, channel.StatusCreating),
						testChannelRuntimeMeta("s1-b", 1, channel.StatusActive),
					},
					cursor: metadb.ChannelRuntimeMetaCursor{ChannelID: "s1-b", ChannelType: 1},
					done:   true,
				},
			},
			2: {
				{}: {
					items: []metadb.ChannelRuntimeMeta{
						testChannelRuntimeMeta("s2-a", 1, channel.StatusDeleting),
					},
					cursor: metadb.ChannelRuntimeMetaCursor{ChannelID: "s2-a", ChannelType: 1},
					done:   false,
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

	got, err := app.ListChannelRuntimeMeta(context.Background(), ListChannelRuntimeMetaRequest{Limit: 3})
	require.NoError(t, err)
	require.Equal(t, ListChannelRuntimeMetaResponse{
		Items: []ChannelRuntimeMeta{
			{ChannelID: "s1-a", ChannelType: 1, SlotID: 1, Status: "creating"},
			{ChannelID: "s1-b", ChannelType: 1, SlotID: 1, Status: "active"},
			{ChannelID: "s2-a", ChannelType: 1, SlotID: 2, Status: "deleting"},
		},
		HasMore:    true,
		NextCursor: ChannelRuntimeMetaListCursor{SlotID: 2, ChannelID: "s2-a", ChannelType: 1},
	}, summarizeChannelRuntimeMetaResponse(got))
}

func TestListChannelRuntimeMetaContinuesWithNextCursorAcrossSlots(t *testing.T) {
	reader := &fakeChannelRuntimeMetaReader{
		pages: map[multiraft.SlotID]map[metadb.ChannelRuntimeMetaCursor]fakeChannelRuntimeMetaPage{
			2: {
				{ChannelID: "s2-a", ChannelType: 1}: {
					items: []metadb.ChannelRuntimeMeta{
						testChannelRuntimeMeta("s2-b", 1, channel.StatusActive),
					},
					cursor: metadb.ChannelRuntimeMetaCursor{ChannelID: "s2-b", ChannelType: 1},
					done:   true,
				},
			},
			3: {
				{}: {
					items: []metadb.ChannelRuntimeMeta{
						testChannelRuntimeMeta("s3-a", 1, channel.StatusDeleted),
					},
					cursor: metadb.ChannelRuntimeMetaCursor{ChannelID: "s3-a", ChannelType: 1},
					done:   true,
				},
			},
		},
	}
	app := New(Options{
		Cluster: fakeClusterReader{
			slotIDs: []multiraft.SlotID{1, 2, 3},
		},
		ChannelRuntimeMeta: reader,
	})

	got, err := app.ListChannelRuntimeMeta(context.Background(), ListChannelRuntimeMetaRequest{
		Limit: 2,
		Cursor: ChannelRuntimeMetaListCursor{
			SlotID:      2,
			ChannelID:   "s2-a",
			ChannelType: 1,
		},
	})
	require.NoError(t, err)
	require.Equal(t, ListChannelRuntimeMetaResponse{
		Items: []ChannelRuntimeMeta{
			{ChannelID: "s2-b", ChannelType: 1, SlotID: 2, Status: "active"},
			{ChannelID: "s3-a", ChannelType: 1, SlotID: 3, Status: "deleted"},
		},
		HasMore: false,
	}, summarizeChannelRuntimeMetaResponse(got))
	require.Equal(t, []channelRuntimeMetaScanCall{
		{slotID: 2, after: metadb.ChannelRuntimeMetaCursor{ChannelID: "s2-a", ChannelType: 1}, limit: 2},
		{slotID: 3, after: metadb.ChannelRuntimeMetaCursor{}, limit: 1},
	}, reader.calls)
}

func TestListChannelRuntimeMetaMapsStableStatusStrings(t *testing.T) {
	reader := &fakeChannelRuntimeMetaReader{
		pages: map[multiraft.SlotID]map[metadb.ChannelRuntimeMetaCursor]fakeChannelRuntimeMetaPage{
			5: {
				{}: {
					items: []metadb.ChannelRuntimeMeta{
						testChannelRuntimeMeta("c1", 1, channel.StatusCreating),
						testChannelRuntimeMeta("c2", 1, channel.StatusActive),
						testChannelRuntimeMeta("c3", 1, channel.StatusDeleting),
						testChannelRuntimeMeta("c4", 1, channel.StatusDeleted),
						{ChannelID: "c5", ChannelType: 1, Status: 99},
					},
					cursor: metadb.ChannelRuntimeMetaCursor{ChannelID: "c5", ChannelType: 1},
					done:   true,
				},
			},
		},
	}
	app := New(Options{
		Cluster: fakeClusterReader{
			slotIDs: []multiraft.SlotID{5},
		},
		ChannelRuntimeMeta: reader,
	})

	got, err := app.ListChannelRuntimeMeta(context.Background(), ListChannelRuntimeMetaRequest{Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []string{"creating", "active", "deleting", "deleted", "unknown"}, channelRuntimeMetaStatuses(got.Items))
}

func TestListChannelRuntimeMetaPropagatesAuthoritativeErrors(t *testing.T) {
	reader := &fakeChannelRuntimeMetaReader{
		pages: map[multiraft.SlotID]map[metadb.ChannelRuntimeMetaCursor]fakeChannelRuntimeMetaPage{
			1: {
				{}: {
					items: []metadb.ChannelRuntimeMeta{
						testChannelRuntimeMeta("s1-a", 1, channel.StatusActive),
					},
					cursor: metadb.ChannelRuntimeMetaCursor{ChannelID: "s1-a", ChannelType: 1},
					done:   true,
				},
			},
		},
		errBySlot: map[multiraft.SlotID]error{
			2: raftcluster.ErrNoLeader,
		},
	}
	app := New(Options{
		Cluster: fakeClusterReader{
			slotIDs: []multiraft.SlotID{1, 2},
		},
		ChannelRuntimeMeta: reader,
	})

	_, err := app.ListChannelRuntimeMeta(context.Background(), ListChannelRuntimeMetaRequest{Limit: 2})
	require.ErrorIs(t, err, raftcluster.ErrNoLeader)
}

func TestGetChannelRuntimeMetaRejectsInvalidChannelType(t *testing.T) {
	app := New(Options{
		Cluster: fakeClusterReader{
			slotForKey: map[string]multiraft.SlotID{"g1": 7},
			hashSlotForKey: map[string]uint16{
				"g1": 129,
			},
		},
		ChannelRuntimeMeta: &fakeChannelRuntimeMetaReader{},
	})

	_, err := app.GetChannelRuntimeMeta(context.Background(), "g1", 0)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}

func TestGetChannelRuntimeMetaReturnsDetailWithSlotAndHashSlot(t *testing.T) {
	reader := &fakeChannelRuntimeMetaReader{
		metaByKey: map[metadb.ConversationKey]metadb.ChannelRuntimeMeta{
			{ChannelID: "g1", ChannelType: 2}: {
				ChannelID:    "g1",
				ChannelType:  2,
				ChannelEpoch: 12,
				LeaderEpoch:  6,
				Leader:       3,
				Replicas:     []uint64{3, 5, 8},
				ISR:          []uint64{3, 5},
				MinISR:       2,
				Status:       uint8(channel.StatusActive),
				Features:     1,
				LeaseUntilMS: 1700000000000,
			},
		},
	}
	app := New(Options{
		Cluster: fakeClusterReader{
			slotForKey: map[string]multiraft.SlotID{"g1": 7},
			hashSlotForKey: map[string]uint16{
				"g1": 129,
			},
		},
		ChannelRuntimeMeta: reader,
	})

	got, err := app.GetChannelRuntimeMeta(context.Background(), "g1", 2)
	require.NoError(t, err)
	require.Equal(t, ChannelRuntimeMetaDetail{
		ChannelRuntimeMeta: ChannelRuntimeMeta{
			ChannelID:    "g1",
			ChannelType:  2,
			SlotID:       7,
			ChannelEpoch: 12,
			LeaderEpoch:  6,
			Leader:       3,
			Replicas:     []uint64{3, 5, 8},
			ISR:          []uint64{3, 5},
			MinISR:       2,
			Status:       "active",
		},
		HashSlot:     129,
		Features:     1,
		LeaseUntilMS: 1700000000000,
	}, got)
	require.Equal(t, []channelRuntimeMetaGetCall{{
		channelID:   "g1",
		channelType: 2,
	}}, reader.getCalls)
}

func TestGetChannelRuntimeMetaPropagatesNotFound(t *testing.T) {
	reader := &fakeChannelRuntimeMetaReader{getErr: metadb.ErrNotFound}
	app := New(Options{
		Cluster: fakeClusterReader{
			slotForKey: map[string]multiraft.SlotID{"g1": 7},
			hashSlotForKey: map[string]uint16{
				"g1": 129,
			},
		},
		ChannelRuntimeMeta: reader,
	})

	_, err := app.GetChannelRuntimeMeta(context.Background(), "g1", 2)
	require.ErrorIs(t, err, metadb.ErrNotFound)
}

func TestGetChannelRuntimeMetaPropagatesAuthoritativeReadErrors(t *testing.T) {
	reader := &fakeChannelRuntimeMetaReader{getErr: raftcluster.ErrNoLeader}
	app := New(Options{
		Cluster: fakeClusterReader{
			slotForKey: map[string]multiraft.SlotID{"g1": 7},
			hashSlotForKey: map[string]uint16{
				"g1": 129,
			},
		},
		ChannelRuntimeMeta: reader,
	})

	_, err := app.GetChannelRuntimeMeta(context.Background(), "g1", 2)
	require.ErrorIs(t, err, raftcluster.ErrNoLeader)
}

type fakeChannelRuntimeMetaReader struct {
	pages     map[multiraft.SlotID]map[metadb.ChannelRuntimeMetaCursor]fakeChannelRuntimeMetaPage
	errBySlot map[multiraft.SlotID]error
	calls     []channelRuntimeMetaScanCall
	metaByKey map[metadb.ConversationKey]metadb.ChannelRuntimeMeta
	getErr    error
	getCalls  []channelRuntimeMetaGetCall
}

type fakeChannelRuntimeMetaPage struct {
	items  []metadb.ChannelRuntimeMeta
	cursor metadb.ChannelRuntimeMetaCursor
	done   bool
}

type channelRuntimeMetaScanCall struct {
	slotID multiraft.SlotID
	after  metadb.ChannelRuntimeMetaCursor
	limit  int
}

type channelRuntimeMetaGetCall struct {
	channelID   string
	channelType int64
}

func (f *fakeChannelRuntimeMetaReader) ScanChannelRuntimeMetaSlotPage(_ context.Context, slotID multiraft.SlotID, after metadb.ChannelRuntimeMetaCursor, limit int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error) {
	f.calls = append(f.calls, channelRuntimeMetaScanCall{slotID: slotID, after: after, limit: limit})
	if err := f.errBySlot[slotID]; err != nil {
		return nil, metadb.ChannelRuntimeMetaCursor{}, false, err
	}
	if slotPages := f.pages[slotID]; slotPages != nil {
		if page, ok := slotPages[after]; ok {
			return append([]metadb.ChannelRuntimeMeta(nil), page.items...), page.cursor, page.done, nil
		}
	}
	return nil, after, true, nil
}

func (f *fakeChannelRuntimeMetaReader) GetChannelRuntimeMeta(_ context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	f.getCalls = append(f.getCalls, channelRuntimeMetaGetCall{channelID: channelID, channelType: channelType})
	if f.getErr != nil {
		return metadb.ChannelRuntimeMeta{}, f.getErr
	}
	meta, ok := f.metaByKey[metadb.ConversationKey{ChannelID: channelID, ChannelType: channelType}]
	if !ok {
		return metadb.ChannelRuntimeMeta{}, metadb.ErrNotFound
	}
	return meta, nil
}

func testChannelRuntimeMeta(channelID string, channelType int64, status channel.Status) metadb.ChannelRuntimeMeta {
	return metadb.ChannelRuntimeMeta{
		ChannelID:   channelID,
		ChannelType: channelType,
		Status:      uint8(status),
	}
}

func summarizeChannelRuntimeMetaResponse(resp ListChannelRuntimeMetaResponse) ListChannelRuntimeMetaResponse {
	out := ListChannelRuntimeMetaResponse{
		Items:      make([]ChannelRuntimeMeta, 0, len(resp.Items)),
		HasMore:    resp.HasMore,
		NextCursor: resp.NextCursor,
	}
	for _, item := range resp.Items {
		out.Items = append(out.Items, ChannelRuntimeMeta{
			ChannelID:   item.ChannelID,
			ChannelType: item.ChannelType,
			SlotID:      item.SlotID,
			Status:      item.Status,
		})
	}
	return out
}

func channelRuntimeMetaStatuses(items []ChannelRuntimeMeta) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Status)
	}
	return out
}
