//go:build integration

package diagnostics

import (
	"runtime"
	"testing"
	"unsafe"
)

// F06/F10: measure actual allocator bytes in addition to fixed row/index
// backing. This is Go allocation evidence, not process RSS or a formal gate.
func TestAttemptStoreActualAllocationRemainsWithinFrozenBackingBudget(t *testing.T) {
	if got := testing.AllocsPerRun(100, func() { _ = NewAttemptStore(AttemptStoreOptions{}) }); got != 0 {
		t.Fatalf("default-off allocation count=%v", got)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	store := NewAttemptStore(AttemptStoreOptions{Enabled: true, NodeID: 1, Epoch: [16]byte{1}})
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(store)
	raw := uint64(len(store.rows))*uint64(unsafe.Sizeof(attemptSummaryRow{})) + uint64(len(store.index))*uint64(unsafe.Sizeof(attemptIndexEntry{})) + uint64(len(store.traceIndex))*uint64(unsafe.Sizeof(attemptTraceIndexEntry{}))
	allocated := after.TotalAlloc - before.TotalAlloc
	if allocated < raw || allocated > raw+(128<<10) {
		t.Fatalf("actual allocator bytes=%d vs raw fixed backing=%d (+128KiB allowance); do not hide map/string/unbounded retention", allocated, raw)
	}
	t.Logf("attempt memory: row_size=%d row_count=%d key_index_size=%d key_index_count=%d trace_index_size=%d trace_index_count=%d raw_backing=%d TotalAlloc_delta=%d HeapAlloc_delta=%d", unsafe.Sizeof(attemptSummaryRow{}), len(store.rows), unsafe.Sizeof(attemptIndexEntry{}), len(store.index), unsafe.Sizeof(attemptTraceIndexEntry{}), len(store.traceIndex), raw, allocated, int64(after.HeapAlloc)-int64(before.HeapAlloc))
}
