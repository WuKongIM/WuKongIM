package management

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestNodeChannelDrainInventoryCountsTargetRolesAcrossSlots(t *testing.T) {
	reader := newChannelDrainMetaReader()
	reader.pages[1] = map[metadb.ChannelRuntimeMetaCursor]channelDrainMetaPage{
		{}: {
			items: []metadb.ChannelRuntimeMeta{
				{ChannelID: "leader", ChannelType: 1, Leader: 4, Replicas: []uint64{2, 3, 4}, ISR: []uint64{2, 3, 4}},
				{ChannelID: "replica", ChannelType: 1, Leader: 2, Replicas: []uint64{2, 3, 4}, ISR: []uint64{2, 3}},
			},
			done: true,
		},
	}
	reader.pages[2] = map[metadb.ChannelRuntimeMetaCursor]channelDrainMetaPage{
		{}: {
			items: []metadb.ChannelRuntimeMeta{
				{ChannelID: "isr", ChannelType: 1, Leader: 2, Replicas: []uint64{2, 3, 5}, ISR: []uint64{3, 4}},
			},
			done: true,
		},
	}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: control.Snapshot{
			Slots: []control.SlotAssignment{{SlotID: 2}, {SlotID: 1}},
		}},
		ChannelRuntimeMeta: reader,
	})

	inv, err := app.NodeChannelDrainInventory(context.Background(), NodeChannelDrainInventoryRequest{NodeID: 4, PageLimit: 2})
	if err != nil {
		t.Fatalf("NodeChannelDrainInventory() error = %v", err)
	}
	if inv.Unknown || inv.Safe || inv.LeaderCount != 1 || inv.ReplicaCount != 2 || inv.ISRCount != 2 || inv.ScannedSlotCount != 2 {
		t.Fatalf("inventory = %#v, want counted unsafe target roles", inv)
	}
}

func TestNodeChannelDrainInventoryPaginatesOneSlot(t *testing.T) {
	reader := newChannelDrainMetaReader()
	reader.pages[1] = map[metadb.ChannelRuntimeMetaCursor]channelDrainMetaPage{
		{}: {
			items:  []metadb.ChannelRuntimeMeta{{ChannelID: "a", ChannelType: 1, Replicas: []uint64{4}}},
			cursor: metadb.ChannelRuntimeMetaCursor{ChannelID: "a", ChannelType: 1},
			done:   false,
		},
		{ChannelID: "a", ChannelType: 1}: {
			items: []metadb.ChannelRuntimeMeta{{ChannelID: "b", ChannelType: 1, ISR: []uint64{4}}},
			done:  true,
		},
	}
	app := New(Options{
		Cluster:            fakeNodeSnapshotReader{snapshot: control.Snapshot{Slots: []control.SlotAssignment{{SlotID: 1}}}},
		ChannelRuntimeMeta: reader,
	})

	inv, err := app.NodeChannelDrainInventory(context.Background(), NodeChannelDrainInventoryRequest{NodeID: 4, PageLimit: 1})
	if err != nil {
		t.Fatalf("NodeChannelDrainInventory() error = %v", err)
	}
	if inv.ReplicaCount != 1 || inv.ISRCount != 1 || reader.calls != 2 {
		t.Fatalf("inventory = %#v calls=%d, want paged replica/isr counts", inv, reader.calls)
	}
}

func TestNodeChannelDrainInventoryFailsClosedWhenReaderMissingOrScanFails(t *testing.T) {
	missing := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: control.Snapshot{Slots: []control.SlotAssignment{{SlotID: 1}}}}})
	inv, err := missing.NodeChannelDrainInventory(context.Background(), NodeChannelDrainInventoryRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("missing reader inventory error = %v", err)
	}
	if !inv.Unknown || inv.Safe {
		t.Fatalf("missing reader inventory = %#v, want unknown unsafe", inv)
	}

	reader := newChannelDrainMetaReader()
	reader.err = errors.New("scan failed")
	app := New(Options{
		Cluster:            fakeNodeSnapshotReader{snapshot: control.Snapshot{Slots: []control.SlotAssignment{{SlotID: 1}}}},
		ChannelRuntimeMeta: reader,
	})
	inv, err = app.NodeChannelDrainInventory(context.Background(), NodeChannelDrainInventoryRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("scan error inventory error = %v", err)
	}
	if !inv.Unknown || inv.Safe || inv.LastError == "" {
		t.Fatalf("scan error inventory = %#v, want unknown unsafe with error", inv)
	}
}

func TestNodeChannelDrainInventoryFailsClosedWhenCursorDoesNotAdvance(t *testing.T) {
	reader := newChannelDrainMetaReader()
	reader.pages[1] = map[metadb.ChannelRuntimeMetaCursor]channelDrainMetaPage{
		{}: {
			items:  []metadb.ChannelRuntimeMeta{{ChannelID: "a", ChannelType: 1}},
			cursor: metadb.ChannelRuntimeMetaCursor{},
			done:   false,
		},
	}
	app := New(Options{
		Cluster:            fakeNodeSnapshotReader{snapshot: control.Snapshot{Slots: []control.SlotAssignment{{SlotID: 1}}}},
		ChannelRuntimeMeta: reader,
	})

	inv, err := app.NodeChannelDrainInventory(context.Background(), NodeChannelDrainInventoryRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("NodeChannelDrainInventory() error = %v", err)
	}
	if !inv.Unknown || inv.Safe || inv.LastError == "" {
		t.Fatalf("inventory = %#v, want unknown unsafe on stalled cursor", inv)
	}
}

func TestNodeChannelDrainInventoryFailsClosedWhenPageBudgetExceeded(t *testing.T) {
	reader := newChannelDrainMetaReader()
	reader.pages[1] = map[metadb.ChannelRuntimeMetaCursor]channelDrainMetaPage{
		{}: {
			items:  []metadb.ChannelRuntimeMeta{{ChannelID: "a", ChannelType: 1}},
			cursor: metadb.ChannelRuntimeMetaCursor{ChannelID: "a", ChannelType: 1},
			done:   false,
		},
		{ChannelID: "a", ChannelType: 1}: {
			items:  []metadb.ChannelRuntimeMeta{{ChannelID: "b", ChannelType: 1}},
			cursor: metadb.ChannelRuntimeMetaCursor{ChannelID: "b", ChannelType: 1},
			done:   false,
		},
	}
	app := New(Options{
		Cluster:            fakeNodeSnapshotReader{snapshot: control.Snapshot{Slots: []control.SlotAssignment{{SlotID: 1}}}},
		ChannelRuntimeMeta: reader,
	})

	inv, err := app.NodeChannelDrainInventory(context.Background(), NodeChannelDrainInventoryRequest{NodeID: 4, PageLimit: 1, MaxPages: 1})
	if err != nil {
		t.Fatalf("NodeChannelDrainInventory() error = %v", err)
	}
	if !inv.Unknown || inv.Safe || inv.LastError == "" || inv.ScannedPageCount != 1 || reader.calls != 1 {
		t.Fatalf("inventory = %#v calls=%d, want unknown unsafe after one-page budget", inv, reader.calls)
	}
}

type channelDrainMetaReader struct {
	pages map[uint32]map[metadb.ChannelRuntimeMetaCursor]channelDrainMetaPage
	err   error
	calls int
}

type channelDrainMetaPage struct {
	items  []metadb.ChannelRuntimeMeta
	cursor metadb.ChannelRuntimeMetaCursor
	done   bool
}

func newChannelDrainMetaReader() *channelDrainMetaReader {
	return &channelDrainMetaReader{pages: map[uint32]map[metadb.ChannelRuntimeMetaCursor]channelDrainMetaPage{}}
}

func (r *channelDrainMetaReader) ScanChannelRuntimeMetaSlotPage(_ context.Context, slotID uint32, after metadb.ChannelRuntimeMetaCursor, _ int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error) {
	r.calls++
	if r.err != nil {
		return nil, metadb.ChannelRuntimeMetaCursor{}, false, r.err
	}
	if pages := r.pages[slotID]; pages != nil {
		if page, ok := pages[after]; ok {
			return append([]metadb.ChannelRuntimeMeta(nil), page.items...), page.cursor, page.done, nil
		}
	}
	return nil, after, true, nil
}
