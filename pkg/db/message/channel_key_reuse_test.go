package message

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

func TestChannelReacquireReusesImmutableKeysWithoutRevivingLease(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	id := ChannelID{ID: "key-reuse", Type: 2}
	first := mustAcquireChannel(t, store.db, "key-reuse:2", id)
	prefix := first.appendKeyCache.rowPrefix
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := mustAcquireChannel(t, store.db, "key-reuse:2", id)
	defer second.Close()
	if &prefix[0] != &second.appendKeyCache.rowPrefix[0] {
		t.Fatal("warm reacquisition rebuilt the immutable key prefix")
	}
	if first.channelEntry == second.channelEntry {
		t.Fatal("warm reacquisition revived a reclaimed mutable entry")
	}
	if _, err := first.LEO(context.Background()); !errors.Is(err, dberrors.ErrClosed) {
		t.Fatalf("closed lease LEO = %v", err)
	}
	key := second.appendKeyCache.messageRowKey(1, messageHeaderFamilyID)
	key[0] ^= 0xff
	if !bytes.Equal(second.appendKeyCache.messageRowKey(1, messageHeaderFamilyID), encodeMessageRowKey(second.key, 1, messageHeaderFamilyID)) {
		t.Fatal("caller-owned key mutated the shared prefix")
	}
}

func TestChannelWarmKeyReuseHonorsEvictionAndIdentity(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	store.db.registry.maxWarmEntries = 1
	id := ChannelID{ID: "original", Type: 2}
	first := mustAcquireChannel(t, store.db, "original:2", id)
	prefix := first.appendKeyCache.rowPrefix
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	other := mustAcquireChannel(t, store.db, "other:2", ChannelID{ID: "other", Type: 2})
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := mustAcquireChannel(t, store.db, "original:2", id)
	if &prefix[0] == &reopened.appendKeyCache.rowPrefix[0] {
		t.Fatal("evicted keys were retained")
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	replacementID := ChannelID{ID: "replacement", Type: 2}
	replacement := mustAcquireChannel(t, store.db, "original:2", replacementID)
	defer replacement.Close()
	if !bytes.Equal(replacement.appendKeyCache.catalogValue, encodeCatalogValue(replacementID)) {
		t.Fatal("replacement inherited old catalog identity")
	}
}

func TestChannelWarmKeyByteBudgetAndInvalidation(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	r := store.db.registry
	id := ChannelID{ID: "bounded", Type: 2}
	first := mustAcquireChannel(t, store.db, "bounded:2", id)
	keyBytes := first.appendKeyCache.retainedBytes()
	r.maxWarmKeyBytes = keyBytes
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if r.warmKeyBytes != keyBytes {
		t.Fatalf("retained bytes = %d, want %d", r.warmKeyBytes, keyBytes)
	}
	second := mustAcquireChannel(t, store.db, "bounded:2", id)
	if r.warmKeyBytes != 0 {
		t.Fatal("acquired entry still charged to warm cache")
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	r.invalidateWarm("bounded:2")
	if r.warmKeyBytes != 0 {
		t.Fatal("invalidated entry still charged to warm cache")
	}
	third := mustAcquireChannel(t, store.db, "bounded:2", id)
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
	long := mustAcquireChannel(t, store.db, ChannelKey(string(bytes.Repeat([]byte{'x'}, keyBytes))), ChannelID{ID: "long", Type: 2})
	if err := long.Close(); err != nil {
		t.Fatal(err)
	}
	if r.warmKeyBytes != 0 || len(r.warmEntries) != 0 {
		t.Fatal("over-budget keys survived LRU eviction")
	}
}

// BenchmarkChannelWarmReacquire measures the lease churn used by persisted
// conversation reads, independently of disk row decoding and HTTP work.
func BenchmarkChannelWarmReacquire(b *testing.B) {
	eng, err := engine.Open(b.TempDir(), engine.Options{})
	if err != nil {
		b.Fatal(err)
	}
	db := NewDB(eng)
	defer db.Close()
	id := ChannelID{ID: "conversation-qps-cohort-0-channel-100", Type: 2}
	key := ChannelKey(id.ID + ":2")
	lease, err := db.Channel(key, id)
	if err != nil {
		b.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lease, err := db.Channel(key, id)
		if err != nil {
			b.Fatal(err)
		}
		if err := lease.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
