//go:build integration

package diagnostics_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
)

// The ring owns fixed-capacity Event storage at construction. Filling it must
// not allocate another retained copy of every complete Event in its indexes.
func TestStoreRetainedEventsStayWithinAdditionalHeapBudget(t *testing.T) {
	const capacity = 50000
	const total = 150000
	const budget = 8 << 20
	base := time.Unix(1700000000, 0)
	store := diagnostics.NewStore(diagnostics.StoreOptions{Capacity: capacity})
	record := func(i int) {
		trace := "retained"
		if i == 0 {
			trace = "expired"
		}
		store.Record(diagnostics.Event{TraceID: trace, Stage: "memory.boundary", At: base.Add(time.Duration(i)), MessageSeq: uint64(i + 1)})
	}
	for i := 0; i < 256; i++ {
		record(i)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 256; i < total; i++ {
		record(i)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	delta := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	runtime.KeepAlive(store)

	passed := true
	if delta > budget {
		t.Errorf("additional retained heap = %d bytes, limit = %d", delta, budget)
		passed = false
	}
	if got := store.UsageRatio(); got != 1 {
		t.Errorf("usage = %v, want 1", got)
		passed = false
	}
	if got := store.Query(context.Background(), diagnostics.Query{TraceID: "expired"}); got.Status != diagnostics.StatusNotFound || len(got.Events) != 0 {
		t.Error("overwritten event remained queryable")
		passed = false
	}
	result := store.Query(context.Background(), diagnostics.Query{Limit: 500})
	if len(result.Events) != 500 {
		t.Errorf("recent events = %d, want 500", len(result.Events))
		passed = false
	} else {
		for i, event := range result.Events {
			if event.MessageSeq != uint64(total-499+i) {
				t.Errorf("recent event %d: sequence %d", i, event.MessageSeq)
				passed = false
				break
			}
		}
	}
	indexed := store.Query(context.Background(), diagnostics.Query{Stage: "memory.boundary", Limit: 500})
	if len(indexed.Events) != 256 || indexed.Events[len(indexed.Events)-1].MessageSeq != total {
		t.Error("stage lookup lost its retained window")
		passed = false
	}
	artifact := struct {
		Passed              bool  `json:"passed"`
		Capacity            int   `json:"capacity"`
		Records             int   `json:"records"`
		AdditionalHeapBytes int64 `json:"additional_heap_bytes"`
		LimitBytes          int64 `json:"limit_bytes"`
	}{passed, capacity, total, delta, budget}
	body, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(body))
	if path := os.Getenv("R70_DIAGNOSTICS_MEMORY_ARTIFACT"); path != "" {
		// Publish only a completely written artifact; never replace prior evidence.
		f, err := os.CreateTemp(filepath.Dir(path), ".store-memory-*.json")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(f.Name())
		if _, err = f.Write(append(body, '\n')); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
		if err = f.Sync(); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
		if err = f.Close(); err != nil {
			t.Fatal(err)
		}
		if err = os.Link(f.Name(), path); err != nil {
			t.Fatal(err)
		}
	}
}
