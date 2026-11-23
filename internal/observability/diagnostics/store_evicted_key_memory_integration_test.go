//go:build integration

package diagnostics_test

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
)

func TestStoreReleasesEvictedIndexKeyMemory(t *testing.T) {
	const maxKeys = 64
	const budget = 2 << 20
	const backingBytes = 8 << 20
	heap := func() uint64 {
		runtime.GC()
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		return stats.HeapAlloc
	}
	for _, test := range []struct {
		name  string
		event func(string) diagnostics.Event
		query func(string) diagnostics.Query
	}{
		{
			name:  "trace",
			event: func(key string) diagnostics.Event { return diagnostics.Event{Stage: "memory.boundary", TraceID: key} },
			query: func(key string) diagnostics.Query { return diagnostics.Query{TraceID: key} },
		},
		{
			name: "channel_range",
			event: func(key string) diagnostics.Event {
				return diagnostics.Event{Stage: "memory.boundary", ChannelKey: key, RangeStart: 1, RangeEnd: 3}
			},
			query: func(key string) diagnostics.Query { return diagnostics.Query{ChannelKey: key, MessageSeq: 2} },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := diagnostics.NewStore(diagnostics.StoreOptions{Capacity: maxKeys, MaxKeysPerIndex: maxKeys, MaxEventsPerKey: 2})
			before := heap()
			// Model an indexed ID that is a substring of a larger owned buffer.
			store.Record(test.event(strings.Repeat("x", backingBytes)[:32]))
			var newest string
			for index := range maxKeys {
				newest = fmt.Sprintf("recent-%d", index)
				store.Record(test.event(newest))
			}
			delta := int64(heap()) - int64(before)
			if result := store.Query(context.Background(), test.query(strings.Repeat("x", 32))); len(result.Events) != 0 {
				t.Fatal("evicted event remained queryable")
			}
			if result := store.Query(context.Background(), test.query(newest)); len(result.Events) != 1 {
				t.Fatal("latest event was lost")
			}
			t.Logf("heap after eviction: %d bytes below/above empty-store baseline, allowance=%d", delta, budget)
			if delta > budget {
				t.Fatalf("evicted index retained %d bytes, allowance=%d", delta, budget)
			}
		})
	}
}
