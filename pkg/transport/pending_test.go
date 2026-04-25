package transport

import (
	"sync"
	"testing"
)

func TestPendingMapStoreLoadAndDelete(t *testing.T) {
	pm := newPendingMap(16)
	ch := make(chan rpcResponse, 1)

	pm.Store(7, ch)

	got, ok := pm.LoadAndDelete(7)
	if !ok {
		t.Fatal("LoadAndDelete() ok = false, want true")
	}
	if got != ch {
		t.Fatal("LoadAndDelete() returned wrong channel")
	}
	if _, ok := pm.LoadAndDelete(7); ok {
		t.Fatal("LoadAndDelete() ok = true after delete, want false")
	}
}

func TestPendingMapRangeVisitsAllEntries(t *testing.T) {
	pm := newPendingMap(16)
	want := map[uint64]chan rpcResponse{
		1: make(chan rpcResponse, 1),
		2: make(chan rpcResponse, 1),
		3: make(chan rpcResponse, 1),
	}
	for id, ch := range want {
		pm.Store(id, ch)
	}

	got := make(map[uint64]chan rpcResponse)
	pm.Range(func(id uint64, ch chan rpcResponse) {
		got[id] = ch
	})

	if len(got) != len(want) {
		t.Fatalf("Range() visited %d entries, want %d", len(got), len(want))
	}
	for id, ch := range want {
		if got[id] != ch {
			t.Fatalf("Range() missing id %d", id)
		}
	}
}

func TestPendingMapConcurrentStoreAndDelete(t *testing.T) {
	pm := newPendingMap(16)

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			ch := make(chan rpcResponse, 1)
			pm.Store(id, ch)
			got, ok := pm.LoadAndDelete(id)
			if !ok {
				t.Errorf("LoadAndDelete(%d) ok = false, want true", id)
				return
			}
			if got != ch {
				t.Errorf("LoadAndDelete(%d) returned wrong channel", id)
			}
		}(uint64(i + 1))
	}
	wg.Wait()

	count := 0
	pm.Range(func(id uint64, ch chan rpcResponse) {
		count++
	})
	if count != 0 {
		t.Fatalf("Range() found %d entries after deletes, want 0", count)
	}
}
