package message

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestChannelRegistryFirstCloseKeepsCanonicalEntry(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	first := mustAcquireChannel(t, store.db, "registry:1", ChannelID{ID: "registry", Type: 1})
	second := mustAcquireChannel(t, store.db, "registry:1", ChannelID{ID: "registry", Type: 1})
	if first == second {
		t.Fatal("Channel() returned the same lease wrapper")
	}
	if first.channelEntry != second.channelEntry {
		t.Fatal("Channel() leases do not share one canonical entry")
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

func TestChannelRegistryLastCloseReclaimsEntry(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := mustAcquireChannel(t, store.db, "reclaim:1", ChannelID{ID: "reclaim", Type: 1})
	if err := log.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if got := store.db.registry.snapshot().activeEntries; got != 0 {
		t.Fatalf("registry entries = %d, want 0", got)
	}
}

func TestChannelRegistryCloseIsIdempotent(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := mustAcquireChannel(t, store.db, "idempotent:1", ChannelID{ID: "idempotent", Type: 1})
	if err := log.Close(); err != nil {
		t.Fatalf("first Close(): %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("second Close(): %v", err)
	}
	if got := store.db.registry.snapshot().releaseTotal; got != 1 {
		t.Fatalf("release total = %d, want 1", got)
	}
}

func TestChannelLeaseRejectsUseAfterClose(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := mustAcquireChannel(t, store.db, "closed:1", ChannelID{ID: "closed", Type: 1})
	if err := log.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if _, err := log.LEO(context.Background()); !errors.Is(err, dberrors.ErrClosed) {
		t.Fatalf("LEO() err = %v, want %v", err, dberrors.ErrClosed)
	}
}

func TestChannelRegistryRejectsMismatchedIdentity(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := mustAcquireChannel(t, store.db, "identity:1", ChannelID{ID: "identity", Type: 1})
	defer log.Close()
	conflict, err := store.db.Channel("identity:1", ChannelID{ID: "other", Type: 1})
	if conflict != nil || !errors.Is(err, dberrors.ErrConflict) {
		t.Fatalf("Channel(mismatched) = (%v, %v), want nil conflict", conflict, err)
	}
}

func TestChannelRegistryOlderReleaseCannotDeleteReacquiredEntry(t *testing.T) {
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
		t.Fatal("reacquire reused a reclaimed entry")
	}

	store.db.registry.releaseLease(oldEntry)
	if got := store.db.registry.activeEntry("generation:1"); got != newLease.channelEntry {
		t.Fatalf("stale release removed new entry: got %p want %p", got, newLease.channelEntry)
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

func TestChannelRegistryRejectsAcquireAfterBeginClose(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	store.db.registry.beginClose()

	log, err := store.db.Channel("closing:1", ChannelID{ID: "closing", Type: 1})
	if log != nil || !errors.Is(err, dberrors.ErrClosed) {
		t.Fatalf("Channel() = (%v, %v), want nil closed", log, err)
	}
}

func TestChannelRegistryReacquireRestoresDurableLEOAndMessages(t *testing.T) {
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
	if got := store.db.registry.snapshot().activeEntries; got != 0 {
		t.Fatalf("registry entries after first close = %d, want 0", got)
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

func TestChannelLeaseCloseWaitsForInflightOperation(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	ctx := context.Background()
	id := ChannelID{ID: "inflight", Type: 1}
	log := mustAcquireChannel(t, store.db, "inflight:1", id)
	entry := log.channelEntry
	entry.appendMu.Lock()

	appendResult := make(chan error, 1)
	go func() {
		_, err := log.Append(ctx, testRecords(1, "value"), AppendOptions{Mode: AppendStrict})
		appendResult <- err
	}()
	waitForRegistryCondition(t, func() bool { return log.inflight.Load() == 1 }, "lease inflight operation")

	closed := make(chan error, 1)
	go func() { closed <- log.Close() }()
	waitForRegistryCondition(t, log.closed.Load, "lease close marker")
	select {
	case err := <-closed:
		t.Fatalf("Close() returned %v while operation was inflight", err)
	default:
	}

	reacquired := mustAcquireChannel(t, store.db, "inflight:1", id)
	if reacquired.channelEntry != entry {
		t.Fatal("reacquire created a second entry while old operation was inflight")
	}
	entry.appendMu.Unlock()
	if err := <-appendResult; err != nil {
		t.Fatalf("Append(): %v", err)
	}
	if err := <-closed; err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if err := reacquired.Close(); err != nil {
		t.Fatalf("reacquired Close(): %v", err)
	}
	if got := store.db.registry.snapshot().activeEntries; got != 0 {
		t.Fatalf("registry entries = %d, want 0", got)
	}
}

func TestMessageDBCloseWaitsForActiveOperation(t *testing.T) {
	store := openTestMessageStore(t)
	if err := store.db.beginUse(); err != nil {
		t.Fatalf("beginUse(): %v", err)
	}

	closed := make(chan error, 1)
	go func() { closed <- store.db.Close() }()
	waitForRegistryCondition(t, store.db.registry.closing.Load, "registry close marker")
	select {
	case err := <-closed:
		t.Fatalf("MessageDB.Close() returned %v with an active operation", err)
	default:
	}
	store.db.endUse()
	if err := <-closed; err != nil {
		t.Fatalf("MessageDB.Close(): %v", err)
	}
	store.engine = nil
}

func TestChannelLeaseSteadyLEOGuardDoesNotAllocate(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	log := mustAcquireChannel(t, store.db, "guard-alloc:1", ChannelID{ID: "guard-alloc", Type: 1})
	defer log.Close()
	ctx := context.Background()
	if _, err := log.LEO(ctx); err != nil {
		t.Fatalf("seed LEO(): %v", err)
	}
	var operationErr error
	allocs := testing.AllocsPerRun(1000, func() {
		_, operationErr = log.LEO(ctx)
	})
	if operationErr != nil {
		t.Fatalf("LEO(): %v", operationErr)
	}
	if allocs != 0 {
		t.Fatalf("steady LEO allocations = %.2f, want 0", allocs)
	}
}

func mustAcquireChannel(t *testing.T, db *MessageDB, key ChannelKey, id ChannelID) *ChannelLog {
	t.Helper()
	log, err := db.Channel(key, id)
	if err != nil {
		t.Fatalf("Channel(%q): %v", key, err)
	}
	return log
}

func waitForRegistryCondition(t *testing.T, condition func() bool, name string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", name)
		}
		time.Sleep(time.Millisecond)
	}
}
