package multiraft

import (
	"math"
	"testing"
)

// The isolated ownership seam is necessary to verify hard limits without
// scheduling/timing inference. Failure matrix: multiplication overflow; a
// full Slot/node or byte budget admits another owner; returning one of two
// completions frees payload still retained by the other; duplicate retirement
// refunds twice; legal oversize is queued rather than selected for fallback.
func TestStorageBudgetHoldsBothCompletionsAndRejectsOverflow(t *testing.T) {
	if _, err := newStorageBudget(math.MaxInt, 4); err == nil {
		t.Fatal("overflowing worker bounds accepted")
	}
	b, err := newStorageBudget(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	g := &slot{}
	first, ok := b.reserve(g, 4<<20, storageAppendPart|storageApplyPart)
	if !ok {
		t.Fatal("valid owner denied")
	}
	if _, ok := b.reserve(g, 1, storageAppendPart); ok {
		t.Fatal("MaxApplyingTasks=1 count limit exceeded")
	}
	first.complete(storageAppendPart)
	if _, ok := b.reserve(g, 1, storageAppendPart); ok {
		t.Fatal("append completion released pending apply ownership")
	}
	first.complete(storageApplyPart)
	first.complete(storageApplyPart)
	second, ok := b.reserve(g, 8<<20, storageAppendPart)
	if !ok {
		t.Fatal("completed owner did not release credits exactly once")
	}
	second.retire()
	second.retire()
	if _, ok := b.reserve(g, (8<<20)+1, storageAppendPart); ok {
		t.Fatal("oversize must choose borrowed synchronous fallback")
	}
	if storageChargeAdd(math.MaxInt64-1, 2) != math.MaxInt64 {
		t.Fatal("payload charge wrapped on overflow")
	}
}

func TestStorageBudgetNodeAndSlotLimitsIncludeRetainedCompletions(t *testing.T) {
	b, err := newStorageBudget(1, 4)
	if err != nil {
		t.Fatal(err)
	}
	var leases []*storageLease
	for range 4 {
		l, ok := b.reserve(&slot{}, 2<<20, storageAppendPart)
		if !ok {
			t.Fatal("node allowance denied")
		}
		leases = append(leases, l)
	}
	if _, ok := b.reserve(&slot{}, 1, storageAppendPart); ok {
		t.Fatal("node owner/byte bound exceeded")
	}
	leases[0].retire()
	if _, ok := b.reserve(&slot{}, (2<<20)+1, storageAppendPart); ok {
		t.Fatal("node byte bound exceeded after one retirement")
	}
	l, ok := b.reserve(&slot{}, 2<<20, storageAppendPart)
	if !ok {
		t.Fatal("node credits not returned")
	}
	l.retire()
	for _, lease := range leases {
		lease.retire()
	}
	b, err = newStorageBudget(2, 4)
	if err != nil {
		t.Fatal(err)
	}
	g := &slot{}
	for range 4 {
		l, ok := b.reserve(g, 1, storageAppendPart)
		if !ok {
			t.Fatal("Slot allowance denied")
		}
		leases = append(leases, l)
	}
	if _, ok := b.reserve(g, 1, storageAppendPart); ok {
		t.Fatal("Slot owner bound exceeded")
	}
	other, ok := b.reserve(&slot{}, 1, storageAppendPart)
	if !ok {
		t.Fatal("Slot saturation incorrectly exhausted remaining node allowance")
	}
	other.retire()
	for _, lease := range leases {
		lease.retire()
	}
}
