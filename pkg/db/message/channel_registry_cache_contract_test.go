package message

import (
	"sync"
	"testing"
)

func TestChannelWarmCacheEvictsOldestAndNeverCrossesIdentity(t *testing.T) {
	registry := newChannelRegistry()
	registry.maxWarmEntries = 0
	disabled := &channelEntry{key: "disabled:1", id: ChannelID{ID: "disabled", Type: 1}}
	registry.retainWarmLocked(disabled)
	if len(registry.warmEntries) != 0 {
		t.Fatalf("disabled warm cache retained %d entries", len(registry.warmEntries))
	}

	registry.maxWarmEntries = 1
	first := &channelEntry{key: "first:1", id: ChannelID{ID: "first", Type: 1}}
	first.leo.Store(7)
	first.loaded.Store(true)
	registry.retainWarmLocked(first)
	replacement := &channelEntry{key: "first:1", id: first.id}
	replacement.leo.Store(8)
	replacement.loaded.Store(true)
	registry.retainWarmLocked(replacement)
	if len(registry.warmEntries) != 1 {
		t.Fatalf("same-key replacement left %d warm entries", len(registry.warmEntries))
	}

	second := &channelEntry{key: "second:1", id: ChannelID{ID: "second", Type: 1}}
	second.leo.Store(11)
	second.loaded.Store(true)
	registry.retainWarmLocked(second)
	if registry.warmEntries[first.key] != nil || registry.warmEntries[second.key] == nil {
		t.Fatalf("warm cache did not evict oldest: %+v", registry.warmEntries)
	}
	state, err := registry.takeWarmLocked(second.key, ChannelID{ID: "wrong", Type: 1})
	if err != nil || state != nil || len(registry.warmEntries) != 0 {
		t.Fatalf("cross-identity take = (%+v, %v), cache=%+v", state, err, registry.warmEntries)
	}

	registry.retainWarmLocked(second)
	state, err = registry.takeWarmLocked(second.key, second.id)
	if err != nil || state == nil || state.leo != 11 || !state.loaded || len(registry.warmEntries) != 0 {
		t.Fatalf("matching warm take = (%+v, %v), cache=%+v", state, err, registry.warmEntries)
	}
}

func TestChannelWarmCacheInvalidationAndOperationAdmissionLifecycle(t *testing.T) {
	var nilRegistry *channelRegistry
	nilRegistry.invalidateWarm("ignored")
	nilRegistry.endOperation()
	nilRegistry.waitForDrain()
	if nilRegistry.beginOperation() {
		t.Fatal("nil registry admitted an operation")
	}

	registry := newChannelRegistry()
	entry := &channelEntry{key: "invalidate:1", id: ChannelID{ID: "invalidate", Type: 1}}
	registry.retainWarmLocked(entry)
	registry.invalidateWarm("")
	registry.invalidateWarm("missing:1")
	registry.invalidateWarm(entry.key)
	if len(registry.warmEntries) != 0 || registry.warmOrder.Len() != 0 {
		t.Fatalf("invalidate left warm state: entries=%d order=%d", len(registry.warmEntries), registry.warmOrder.Len())
	}

	if !registry.beginOperation() {
		t.Fatal("open registry rejected an operation")
	}
	registry.beginClose()
	registry.endOperation()
	registry.waitForDrain()
	if registry.beginOperation() {
		t.Fatal("closing registry admitted an operation")
	}
}

func TestChannelLogFinalLocalUseSignalsClosedLease(t *testing.T) {
	log := &ChannelLog{}
	log.useCond.L = &log.useMu
	log.inflight.Store(1)
	log.closed.Store(true)
	log.endLocalUse()
	if got := log.inflight.Load(); got != 0 {
		t.Fatalf("inflight = %d, want 0", got)
	}

	// A non-final operation must only decrement the count; it must not require
	// a waiter or a registry to be present.
	open := &ChannelLog{}
	open.useCond.L = &sync.Mutex{}
	open.inflight.Store(2)
	open.endLocalUse()
	if got := open.inflight.Load(); got != 1 {
		t.Fatalf("non-final inflight = %d, want 1", got)
	}
}
