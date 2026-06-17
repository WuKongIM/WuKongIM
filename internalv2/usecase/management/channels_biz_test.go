package management

import (
	"context"
	"fmt"
	"hash/crc32"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestListBusinessChannelsAggregatesAndFiltersBusinessRows(t *testing.T) {
	snapshot := control.Snapshot{
		Slots: []control.SlotAssignment{
			{SlotID: 2, DesiredPeers: []uint64{1}},
			{SlotID: 1, DesiredPeers: []uint64{1}},
		},
		HashSlots: control.HashSlotTable{Count: 4, Ranges: []control.HashSlotRange{
			{From: 0, To: 1, SlotID: 1},
			{From: 2, To: 3, SlotID: 2},
		}},
	}
	keyOne := businessChannelKeyForSlot(t, snapshot.HashSlots, 1, "alpha")
	keyTwo := businessChannelKeyForSlot(t, snapshot.HashSlots, 2, "alpha-remote")
	reader := newFakeBusinessChannelReader()
	reader.slotPages[1] = map[metadb.ChannelCursor]fakeBusinessChannelPage{
		{}: {
			items: []metadb.Channel{
				{ChannelID: "__wk_internal_memberlist__/allow/2/ZzE", ChannelType: 2},
				{ChannelID: keyOne, ChannelType: 2, Ban: 1, SubscriberMutationVersion: 3},
				{ChannelID: keyOne + "____cmd", ChannelType: 2},
			},
			done: true,
		},
	}
	reader.slotPages[2] = map[metadb.ChannelCursor]fakeBusinessChannelPage{
		{}: {
			items: []metadb.Channel{
				{ChannelID: keyTwo, ChannelType: 2, SendBan: 1},
				{ChannelID: "beta", ChannelType: 3},
			},
			done: true,
		},
	}
	app := New(Options{
		Cluster:               fakeNodeSnapshotReader{snapshot: snapshot},
		ChannelBusinessReader: reader,
	})

	got, err := app.ListBusinessChannels(context.Background(), ListBusinessChannelsRequest{Limit: 10, TypeFilter: 2, Keyword: " alpha "})
	if err != nil {
		t.Fatalf("ListBusinessChannels() error = %v", err)
	}
	want := []BusinessChannelListItem{
		{ChannelID: keyOne, ChannelType: 2, SlotID: 1, HashSlot: routing.HashSlotForKey(keyOne, 4), Ban: true, SubscriberMutationVersion: 3},
		{ChannelID: keyTwo, ChannelType: 2, SlotID: 2, HashSlot: routing.HashSlotForKey(keyTwo, 4), SendBan: true},
	}
	if !sameBusinessChannelItems(got.Items, want) {
		t.Fatalf("items = %#v, want %#v", got.Items, want)
	}
	if got.HasMore {
		t.Fatalf("HasMore = true, want false")
	}
}

func TestListBusinessChannelsReturnsCursorAndRejectsFilterMismatch(t *testing.T) {
	snapshot := control.Snapshot{
		Slots:     []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1}}},
		HashSlots: control.HashSlotTable{Count: 4, Ranges: []control.HashSlotRange{{From: 0, To: 3, SlotID: 1}}},
	}
	reader := newFakeBusinessChannelReader()
	reader.slotPages[1] = map[metadb.ChannelCursor]fakeBusinessChannelPage{
		{}: {
			items: []metadb.Channel{
				{ChannelID: "a", ChannelType: 2},
				{ChannelID: "b", ChannelType: 2},
			},
			cursor: metadb.ChannelCursor{ChannelID: "b", ChannelType: 2},
			done:   true,
		},
	}
	app := New(Options{
		Cluster:               fakeNodeSnapshotReader{snapshot: snapshot},
		ChannelBusinessReader: reader,
	})

	got, err := app.ListBusinessChannels(context.Background(), ListBusinessChannelsRequest{Limit: 1})
	if err != nil {
		t.Fatalf("ListBusinessChannels() error = %v", err)
	}
	if !got.HasMore {
		t.Fatalf("HasMore = false, want true")
	}
	wantItem := BusinessChannelListItem{ChannelID: "a", ChannelType: 2, SlotID: 1, HashSlot: routing.HashSlotForKey("a", 4)}
	if !sameBusinessChannelItems(got.Items, []BusinessChannelListItem{wantItem}) {
		t.Fatalf("items = %#v, want %#v", got.Items, []BusinessChannelListItem{wantItem})
	}
	if got.NextCursor != (ChannelListCursor{SlotID: 1, ChannelID: "a", ChannelType: 2, KeywordHash: crc32.ChecksumIEEE(nil)}) {
		t.Fatalf("NextCursor = %#v, want cursor after a", got.NextCursor)
	}

	_, err = app.ListBusinessChannels(context.Background(), ListBusinessChannelsRequest{Limit: 1, Keyword: "b", Cursor: got.NextCursor})
	if err != metadb.ErrInvalidArgument {
		t.Fatalf("ListBusinessChannels() filter mismatch error = %v, want %v", err, metadb.ErrInvalidArgument)
	}
}

func TestListBusinessChannelsRoutesRemoteNodeReads(t *testing.T) {
	localReader := newFakeBusinessChannelReader()
	remoteReader := &fakeRemoteBusinessChannelReader{
		response: ListBusinessChannelsResponse{
			Items: []BusinessChannelListItem{{
				ChannelID:   "remote",
				ChannelType: 2,
				SlotID:      9,
				HashSlot:    3,
			}},
		},
	}
	app := New(Options{
		Cluster:                fakeNodeSnapshotReader{nodeID: 1},
		ChannelBusinessReader:  localReader,
		RemoteBusinessChannels: remoteReader,
	})

	got, err := app.ListBusinessChannels(context.Background(), ListBusinessChannelsRequest{NodeID: 2, Limit: 50, TypeFilter: 2, Keyword: "remote"})
	if err != nil {
		t.Fatalf("ListBusinessChannels() error = %v", err)
	}

	if len(localReader.calls) != 0 {
		t.Fatalf("local reader calls = %#v, want none for remote node", localReader.calls)
	}
	if remoteReader.req != (ListBusinessChannelsRequest{NodeID: 2, Limit: 50, TypeFilter: 2, Keyword: "remote"}) {
		t.Fatalf("remote request = %#v, want node 2 request", remoteReader.req)
	}
	if !sameBusinessChannelItems(got.Items, remoteReader.response.Items) {
		t.Fatalf("items = %#v, want remote response %#v", got.Items, remoteReader.response.Items)
	}
}

type fakeBusinessChannelReader struct {
	slotPages map[uint32]map[metadb.ChannelCursor]fakeBusinessChannelPage
	calls     []uint32
}

type fakeBusinessChannelPage struct {
	items  []metadb.Channel
	cursor metadb.ChannelCursor
	done   bool
}

func newFakeBusinessChannelReader() *fakeBusinessChannelReader {
	return &fakeBusinessChannelReader{slotPages: map[uint32]map[metadb.ChannelCursor]fakeBusinessChannelPage{}}
}

func (f *fakeBusinessChannelReader) ScanChannelsSlotPage(_ context.Context, slotID uint32, after metadb.ChannelCursor, _ int) ([]metadb.Channel, metadb.ChannelCursor, bool, error) {
	f.calls = append(f.calls, slotID)
	if pages := f.slotPages[slotID]; pages != nil {
		if page, ok := pages[after]; ok {
			return append([]metadb.Channel(nil), page.items...), page.cursor, page.done, nil
		}
	}
	return nil, after, true, nil
}

type fakeRemoteBusinessChannelReader struct {
	req      ListBusinessChannelsRequest
	response ListBusinessChannelsResponse
	err      error
}

func (f *fakeRemoteBusinessChannelReader) NodeBusinessChannels(_ context.Context, req ListBusinessChannelsRequest) (ListBusinessChannelsResponse, error) {
	f.req = req
	return f.response, f.err
}

func sameBusinessChannelItems(left, right []BusinessChannelListItem) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func businessChannelKeyForSlot(t *testing.T, table control.HashSlotTable, slotID uint32, prefix string) string {
	t.Helper()
	for i := 0; i < 10000; i++ {
		key := prefix
		if i > 0 {
			key = fmt.Sprintf("%s-%d", prefix, i)
		}
		hashSlot := routing.HashSlotForKey(key, table.Count)
		if slotIDForHashSlot(table, hashSlot) == slotID {
			return key
		}
	}
	t.Fatalf("no key found for slot %d", slotID)
	return ""
}
