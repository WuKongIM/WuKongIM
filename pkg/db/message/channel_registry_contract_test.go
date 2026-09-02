//go:build !integration

package message

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestChannelRegistryFirstCloseKeepsCanonicalEntry(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	first := mustAcquireChannel(t, store.db, "registry:1", ChannelID{ID: "registry", Type: 1})
	second := mustAcquireChannel(t, store.db, "registry:1", ChannelID{ID: "registry", Type: 1})
	if first == second || first.channelEntry != second.channelEntry {
		t.Fatal("leases did not share one canonical entry through distinct handles")
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close(): %v", err)
	}
	if got := store.db.registry.snapshot().activeEntries; got != 1 {
		t.Fatalf("registry entries after first close = %d, want 1", got)
	}
	if _, err := second.LEO(context.Background()); err != nil {
		t.Fatalf("second LEO(): %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second Close(): %v", err)
	}
}

func TestChannelRegistryLastCloseAndRepeatedCloseReclaimOnce(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := mustAcquireChannel(t, store.db, "reclaim:1", ChannelID{ID: "reclaim", Type: 1})
	if err := log.Close(); err != nil {
		t.Fatalf("first Close(): %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("second Close(): %v", err)
	}
	if got := store.db.registry.snapshot(); got.activeEntries != 0 || got.releaseTotal != 1 || got.reclaimTotal != 1 {
		t.Fatalf("registry snapshot = %+v, want one release and reclaim", got)
	}
	if _, err := log.LEO(context.Background()); !errors.Is(err, dberrors.ErrClosed) {
		t.Fatalf("LEO() after close error = %v, want closed", err)
	}
}

func TestChannelRegistryRejectsIdentityConflictAndClosingAcquire(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := mustAcquireChannel(t, store.db, "identity:1", ChannelID{ID: "identity", Type: 1})
	defer log.Close()
	conflict, err := store.db.Channel("identity:1", ChannelID{ID: "other", Type: 1})
	if conflict != nil || !errors.Is(err, dberrors.ErrConflict) {
		t.Fatalf("Channel(mismatched) = (%v, %v), want conflict", conflict, err)
	}
	store.db.registry.beginClose()
	closing, err := store.db.Channel("closing:1", ChannelID{ID: "closing", Type: 1})
	if closing != nil || !errors.Is(err, dberrors.ErrClosed) {
		t.Fatalf("Channel() while closing = (%v, %v), want closed", closing, err)
	}
}

func TestChannelRegistryStaleGenerationCannotReclaimReplacement(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	oldLease := mustAcquireChannel(t, store.db, "generation:1", ChannelID{ID: "generation", Type: 1})
	oldEntry := oldLease.channelEntry
	if err := oldLease.Close(); err != nil {
		t.Fatalf("old Close(): %v", err)
	}
	newLease := mustAcquireChannel(t, store.db, "generation:1", ChannelID{ID: "generation", Type: 1})
	defer newLease.Close()
	if newLease.channelEntry == oldEntry {
		t.Fatal("reacquire reused a reclaimed generation")
	}

	store.db.registry.releaseLease(oldEntry)
	if got := store.db.registry.activeEntry("generation:1"); got != newLease.channelEntry {
		t.Fatalf("stale release removed replacement: got %p want %p", got, newLease.channelEntry)
	}
}

func TestChannelRegistryBackgroundPinDefersReclaim(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := mustAcquireChannel(t, store.db, "pin:1", ChannelID{ID: "pin", Type: 1})
	entry := log.channelEntry
	if err := store.db.registry.retainPin(entry); err != nil {
		t.Fatalf("retainPin(): %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if got := store.db.registry.activeEntry("pin:1"); got != entry {
		t.Fatalf("pinned entry = %p, want %p", got, entry)
	}
	store.db.registry.releasePin(entry)
	if got := store.db.registry.snapshot().activeEntries; got != 0 {
		t.Fatalf("registry entries after pin release = %d, want 0", got)
	}
}

func TestChannelRegistryReacquireRestoresDurableState(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	ctx := context.Background()
	id := ChannelID{ID: "durable", Type: 1}

	first := mustAcquireChannel(t, store.db, "durable:1", id)
	if _, err := first.Append(ctx, testRecords(1, "one", "two"), AppendOptions{Mode: AppendStrict}); err != nil {
		t.Fatalf("first Append(): %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close(): %v", err)
	}

	second := mustAcquireChannel(t, store.db, "durable:1", id)
	defer second.Close()
	leo, err := second.LEO(ctx)
	if err != nil || leo != 2 {
		t.Fatalf("reacquired LEO() = %d, %v, want 2, nil", leo, err)
	}
	messages, err := second.Read(ctx, 1, ReadOptions{})
	if err != nil || len(messages) != 2 {
		t.Fatalf("reacquired Read() = %d rows, %v, want 2, nil", len(messages), err)
	}
	result, err := second.Append(ctx, testRecords(3, "three"), AppendOptions{Mode: AppendStrict})
	if err != nil || result.BaseSeq != 3 {
		t.Fatalf("reacquired Append() = %+v, %v, want base seq 3", result, err)
	}
}
