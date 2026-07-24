package management

import (
	"context"
	"testing"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestListChannelRuntimeMetaFiltersByNodeAndChannelID(t *testing.T) {
	snapshot := control.Snapshot{
		Slots: []control.SlotAssignment{
			{SlotID: 1, DesiredPeers: []uint64{1, 2}, PreferredLeader: 2},
			{SlotID: 2, DesiredPeers: []uint64{2, 3}},
		},
		HashSlots: control.HashSlotTable{Count: 4, Ranges: []control.HashSlotRange{
			{From: 0, To: 1, SlotID: 1},
			{From: 2, To: 3, SlotID: 2},
		}},
	}
	reader := newFakeChannelRuntimeMetaReader()
	reader.slotPages[1] = map[metadb.ChannelRuntimeMetaCursor]fakeChannelRuntimeMetaPage{
		{}: {
			items: []metadb.ChannelRuntimeMeta{
				{ChannelID: "alpha", ChannelType: 1, Leader: 1, Replicas: []uint64{1, 2}, ISR: []uint64{1, 2}, MinISR: 2, Status: uint8(channelruntime.StatusActive)},
				{ChannelID: "beta", ChannelType: 2, Leader: 2, Replicas: []uint64{2, 3}, ISR: []uint64{2}, MinISR: 2, Status: uint8(channelruntime.StatusCreating)},
			},
			done: true,
		},
	}
	app := New(Options{
		Cluster:            fakeNodeSnapshotReader{snapshot: snapshot},
		ChannelRuntimeMeta: reader,
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{statuses: map[uint32]SlotRuntimeStatus{
			1: {SlotID: 1, LeaderID: 2, CurrentVoters: []uint64{1, 2}},
		}},
	})

	got, err := app.ListChannelRuntimeMeta(context.Background(), ListChannelRuntimeMetaRequest{
		Limit:          10,
		NodeID:         2,
		ChannelIDQuery: "alp",
	})
	if err != nil {
		t.Fatalf("ListChannelRuntimeMeta() error = %v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("items = %#v, want one node/channel match", got.Items)
	}
	item := got.Items[0]
	if item.ChannelID != "alpha" || item.ChannelType != 1 || item.SlotID != 1 || item.Leader != 1 || item.Status != "active" {
		t.Fatalf("item = %#v, want alpha runtime row", item)
	}
	if item.SlotLeader != 2 || item.PreferredLeader != 2 {
		t.Fatalf("slot leaders = actual:%d preferred:%d, want 2/2", item.SlotLeader, item.PreferredLeader)
	}
	if got.HasMore {
		t.Fatalf("HasMore = true, want false")
	}
}

func TestListChannelRuntimeMetaReturnsCursorAfterLastEmittedMatch(t *testing.T) {
	snapshot := control.Snapshot{
		Slots: []control.SlotAssignment{
			{SlotID: 1, DesiredPeers: []uint64{1, 2}},
			{SlotID: 2, DesiredPeers: []uint64{2, 3}},
		},
		HashSlots: control.HashSlotTable{Count: 4, Ranges: []control.HashSlotRange{
			{From: 0, To: 1, SlotID: 1},
			{From: 2, To: 3, SlotID: 2},
		}},
	}
	reader := newFakeChannelRuntimeMetaReader()
	reader.slotPages[1] = map[metadb.ChannelRuntimeMetaCursor]fakeChannelRuntimeMetaPage{
		{}: {
			items: []metadb.ChannelRuntimeMeta{
				{ChannelID: "a", ChannelType: 1, Leader: 2, Replicas: []uint64{2}, ISR: []uint64{2}, MinISR: 1, Status: uint8(channelruntime.StatusActive)},
				{ChannelID: "b", ChannelType: 1, Leader: 2, Replicas: []uint64{2}, ISR: []uint64{2}, MinISR: 1, Status: uint8(channelruntime.StatusActive)},
			},
			done: true,
		},
	}
	app := New(Options{
		Cluster:            fakeNodeSnapshotReader{snapshot: snapshot},
		ChannelRuntimeMeta: reader,
	})

	got, err := app.ListChannelRuntimeMeta(context.Background(), ListChannelRuntimeMetaRequest{Limit: 1, NodeID: 2})
	if err != nil {
		t.Fatalf("ListChannelRuntimeMeta() error = %v", err)
	}
	if !got.HasMore {
		t.Fatalf("HasMore = false, want true")
	}
	if got.NextCursor != (ChannelRuntimeMetaListCursor{SlotID: 1, ChannelID: "a", ChannelType: 1}) {
		t.Fatalf("NextCursor = %#v, want after first emitted row", got.NextCursor)
	}
}

func TestGetChannelRuntimeMetaUsesOnePointLookupWithoutScanning(t *testing.T) {
	snapshot := control.Snapshot{
		Slots: []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2}, PreferredLeader: 2}},
		HashSlots: control.HashSlotTable{Count: 4, Ranges: []control.HashSlotRange{
			{From: 0, To: 3, SlotID: 1},
		}},
	}
	scanner := newFakeChannelRuntimeMetaReader()
	point := &fakeChannelRuntimeMetaPointReader{meta: metadb.ChannelRuntimeMeta{
		ChannelID: "room-a", ChannelType: 2, Leader: 1, Replicas: []uint64{1, 2},
		ISR: []uint64{1, 2}, MinISR: 2, Status: uint8(channelruntime.StatusActive),
	}}
	app := New(Options{
		Cluster:                 fakeNodeSnapshotReader{snapshot: snapshot},
		ChannelRuntimeMeta:      scanner,
		ChannelRuntimeMetaPoint: point,
	})
	got, err := app.GetChannelRuntimeMeta(context.Background(), "room-a", 2)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}
	if got.ChannelID != "room-a" || got.SlotID != 1 || got.Status != "active" {
		t.Fatalf("runtime meta = %#v", got)
	}
	if point.calls != 1 || len(scanner.calls) != 0 {
		t.Fatalf("point calls=%d scan calls=%v", point.calls, scanner.calls)
	}
}

type fakeChannelRuntimeMetaReader struct {
	slotPages map[uint32]map[metadb.ChannelRuntimeMetaCursor]fakeChannelRuntimeMetaPage
	calls     []uint32
}

type fakeChannelRuntimeMetaPage struct {
	items  []metadb.ChannelRuntimeMeta
	cursor metadb.ChannelRuntimeMetaCursor
	done   bool
}

func newFakeChannelRuntimeMetaReader() *fakeChannelRuntimeMetaReader {
	return &fakeChannelRuntimeMetaReader{slotPages: map[uint32]map[metadb.ChannelRuntimeMetaCursor]fakeChannelRuntimeMetaPage{}}
}

func (f *fakeChannelRuntimeMetaReader) ScanChannelRuntimeMetaSlotPage(_ context.Context, slotID uint32, after metadb.ChannelRuntimeMetaCursor, _ int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error) {
	f.calls = append(f.calls, slotID)
	if pages := f.slotPages[slotID]; pages != nil {
		if page, ok := pages[after]; ok {
			return append([]metadb.ChannelRuntimeMeta(nil), page.items...), page.cursor, page.done, nil
		}
	}
	return nil, after, true, nil
}

type fakeChannelRuntimeMetaPointReader struct {
	meta  metadb.ChannelRuntimeMeta
	calls int
}

func (f *fakeChannelRuntimeMetaPointReader) GetChannelRuntimeMeta(_ context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	f.calls++
	if f.meta.ChannelID != channelID || f.meta.ChannelType != channelType {
		return metadb.ChannelRuntimeMeta{}, metadb.ErrNotFound
	}
	return f.meta, nil
}
