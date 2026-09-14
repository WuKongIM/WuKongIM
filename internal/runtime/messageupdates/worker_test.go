package messageupdates

import (
	"context"
	"errors"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"testing"
)

type dispatchStub struct {
	ids  []uint64
	more bool
	err  error
}

func (d *dispatchStub) DispatchMessageUpdate(_ context.Context, row metadb.MessageUpdate) (bool, error) {
	d.ids = append(d.ids, row.MessageID)
	return d.more, d.err
}
func (*dispatchStub) PruneMessageUpdate(context.Context, metadb.MessageUpdate) error { return nil }
func TestRepairBudgetAdvancesOnlyVisitedTargets(t *testing.T) {
	dispatcher := &dispatchStub{more: true}
	w := &Worker{dispatcher: dispatcher}
	rows := []metadb.MessageUpdate{{MessageID: 1}, {MessageID: 2}, {MessageID: 3}}
	remaining := 20
	processed, failures := w.dispatchRows(context.Background(), rows, &remaining)
	if processed != 2 || failures != 0 || remaining != 0 || len(dispatcher.ids) != 20 {
		t.Fatalf("processed=%d errors=%d remaining=%d calls=%d", processed, failures, remaining, len(dispatcher.ids))
	}
	if dispatcher.ids[15] != 1 || dispatcher.ids[16] != 2 {
		t.Fatal("hot target did not yield after 16 pages")
	}
	dispatcher.err = errors.New("retry")
	remaining = 32
	processed, failures = w.dispatchRows(context.Background(), rows, &remaining)
	if processed != 3 || failures != 3 || remaining != 29 {
		t.Fatal("one failed target starved subsequent work")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	processed, _ = w.dispatchRows(ctx, rows, &remaining)
	if processed != 0 || remaining != 29 {
		t.Fatal("canceled turn advanced untouched targets")
	}
}

func TestRepairSlotsAlwaysDiscoverColdOwnedSlot(t *testing.T) {
	for _, count := range []int{1, 2, 3, 256} {
		slots := make([]metadb.HashSlot, count)
		for i := range slots {
			slots[i] = metadb.HashSlot(i)
		}
		seen := map[metadb.HashSlot]bool{}
		offset, activeOffset := 0, 0
		for turn := 0; turn < count; turn++ {
			selected, next, nextActive := selectRepairSlots(slots, slots[:1], offset, activeOffset)
			offset, activeOffset = next, nextActive
			for _, slot := range selected {
				seen[slot] = true
			}
		}
		if len(seen) != count {
			t.Fatalf("owned=%d discovered=%d", count, len(seen))
		}
	}
}
