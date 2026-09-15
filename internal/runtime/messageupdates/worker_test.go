package messageupdates

import (
	"context"
	"errors"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"sync"
	"testing"
)

type dispatchStub struct {
	mu   sync.Mutex
	ids  []uint64
	more bool
	err  error
}

func (d *dispatchStub) DispatchMessageUpdate(_ context.Context, row metadb.MessageUpdate) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
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
	if processed != 3 || failures != 0 || remaining != 0 || len(dispatcher.ids) != 20 {
		t.Fatalf("processed=%d errors=%d remaining=%d calls=%d", processed, failures, remaining, len(dispatcher.ids))
	}
	counts := map[uint64]int{}
	for _, id := range dispatcher.ids {
		counts[id]++
	}
	for _, row := range rows {
		if counts[row.MessageID] == 0 || counts[row.MessageID] > 16 {
			t.Fatal("repair skipped a visited target or exceeded its page budget")
		}
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

func TestRepairBudgetDoesNotSkipUnstartedPrefix(t *testing.T) {
	d := &dispatchStub{more: true}
	w := &Worker{dispatcher: d}
	rows := make([]metadb.MessageUpdate, 8)
	for i := range rows {
		rows[i].MessageID = uint64(i + 1)
	}
	remaining := 2
	processed, failures := w.dispatchRows(context.Background(), rows, &remaining)
	if processed != 2 || failures != 0 || remaining != 0 || len(d.ids) != 2 {
		t.Fatalf("processed=%d failures=%d remaining=%d ids=%v", processed, failures, remaining, d.ids)
	}
	for _, id := range d.ids {
		if id > 2 {
			t.Fatal("discovery cursor skipped untouched work")
		}
	}
}
