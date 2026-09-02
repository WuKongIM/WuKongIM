package message

import (
	"testing"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

func TestChannelRegistryMetricsDescribeLeaseAndPinOwnership(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	wantSnapshot := func(want ChannelEntryMetricsSnapshot) {
		t.Helper()
		if got := store.db.ChannelEntryMetricsSnapshot(); got != want {
			t.Fatalf("ChannelEntryMetricsSnapshot() = %+v, want %+v", got, want)
		}
	}
	wantSnapshot(ChannelEntryMetricsSnapshot{})

	key := ChannelKey("metrics:1")
	id := ChannelID{ID: "metrics", Type: 1}
	first := mustAcquireChannel(t, store.db, key, id)
	wantSnapshot(ChannelEntryMetricsSnapshot{
		ActiveEntries:     1,
		OutstandingLeases: 1,
		AcquireTotal:      1,
	})

	second := mustAcquireChannel(t, store.db, key, id)
	if got := store.db.registry.activeEntry(key); got != first.channelEntry {
		t.Fatalf("activeEntry() = %p, want canonical entry %p", got, first.channelEntry)
	}
	if err := store.db.registry.retainPin(first.channelEntry); err != nil {
		t.Fatalf("retainPin(): %v", err)
	}
	wantSnapshot(ChannelEntryMetricsSnapshot{
		ActiveEntries:     1,
		OutstandingLeases: 2,
		BackgroundPins:    1,
		AcquireTotal:      2,
	})

	if err := first.Close(); err != nil {
		t.Fatalf("first Close(): %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second Close(): %v", err)
	}
	wantSnapshot(ChannelEntryMetricsSnapshot{
		ActiveEntries:  1,
		BackgroundPins: 1,
		AcquireTotal:   2,
		ReleaseTotal:   2,
	})

	store.db.registry.releasePin(first.channelEntry)
	wantSnapshot(ChannelEntryMetricsSnapshot{
		AcquireTotal: 2,
		ReleaseTotal: 2,
		ReclaimTotal: 1,
	})
	if got := store.db.registry.activeEntry(key); got != nil {
		t.Fatalf("activeEntry() after final release = %p, want nil", got)
	}
}

func TestEngineChannelRegistryMetricsHandleLifecycleStates(t *testing.T) {
	var nilEngine *Engine
	if got := nilEngine.ChannelEntryMetricsSnapshot(); got != (ChannelEntryMetricsSnapshot{}) {
		t.Fatalf("nil Engine snapshot = %+v, want zero", got)
	}
	var nilDB *MessageDB
	if got := nilDB.ChannelEntryMetricsSnapshot(); got != (ChannelEntryMetricsSnapshot{}) {
		t.Fatalf("nil MessageDB snapshot = %+v, want zero", got)
	}

	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	store, err := engine.ForChannel("engine-metrics:1", channel.ChannelID{ID: "engine-metrics", Type: 1})
	if err != nil {
		t.Fatalf("ForChannel(): %v", err)
	}
	if got, want := engine.ChannelEntryMetricsSnapshot(), (ChannelEntryMetricsSnapshot{
		ActiveEntries:     1,
		OutstandingLeases: 1,
		AcquireTotal:      1,
	}); got != want {
		t.Fatalf("ChannelEntryMetricsSnapshot() = %+v, want %+v", got, want)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("ChannelStore.Close(): %v", err)
	}
	if got, want := engine.ChannelEntryMetricsSnapshot(), (ChannelEntryMetricsSnapshot{
		AcquireTotal: 1,
		ReleaseTotal: 1,
		ReclaimTotal: 1,
	}); got != want {
		t.Fatalf("snapshot after release = %+v, want %+v", got, want)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("Engine.Close(): %v", err)
	}
	if got := engine.ChannelEntryMetricsSnapshot(); got != (ChannelEntryMetricsSnapshot{}) {
		t.Fatalf("closed Engine snapshot = %+v, want zero", got)
	}
}
