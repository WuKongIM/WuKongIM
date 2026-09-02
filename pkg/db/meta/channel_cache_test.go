package meta

import (
	"context"
	"fmt"
	"testing"
)

func TestChannelCacheRetainsAtMostItsReportedCapacity(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close(): %v", err)
		}
	}()

	capacity := db.MetricsSnapshot().ChannelCacheCapacity
	if capacity <= 0 {
		t.Fatalf("reported channel cache capacity = %d, want positive fixed bound", capacity)
	}
	batch := db.NewWriteBatch()
	defer batch.Close()
	for i := 0; i <= capacity; i++ {
		if err := batch.UpsertChannel(1, Channel{ChannelID: fmt.Sprintf("cache-bound-%05d", i), ChannelType: 2}); err != nil {
			t.Fatalf("UpsertChannel(%d): %v", i, err)
		}
	}
	if err := batch.Commit(); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	for i := 0; i <= capacity; i++ {
		channelID := fmt.Sprintf("cache-bound-%05d", i)
		if _, err := db.ForHashSlot(1).GetChannel(context.Background(), channelID, 2); err != nil {
			t.Fatalf("GetChannel(%d): %v", i, err)
		}
	}
	snapshot := db.MetricsSnapshot()
	if snapshot.ChannelCacheEntries > snapshot.ChannelCacheCapacity {
		t.Fatalf("channel cache entries = %d, capacity = %d", snapshot.ChannelCacheEntries, snapshot.ChannelCacheCapacity)
	}
}

func TestChannelCacheWarmsOnReadAndInvalidatesOnMutation(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(1)

	channel := Channel{ChannelID: "group-cache", ChannelType: 1, Ban: 1}
	if err := shard.CreateChannel(context.Background(), channel); err != nil {
		t.Fatalf("CreateChannel(): %v", err)
	}
	if got := store.db.channelCacheSize(); got != 0 {
		t.Fatalf("channel cache size after create = %d, want 0", got)
	}
	if _, ok, err := shard.GetChannel(context.Background(), channel.ChannelID, channel.ChannelType); err != nil || !ok {
		t.Fatalf("GetChannel() ok=%v err=%v", ok, err)
	}
	if got := store.db.channelCacheSize(); got != 1 {
		t.Fatalf("channel cache size after read = %d, want 1", got)
	}
	updated := channel
	updated.Ban = 7
	if err := shard.UpdateChannel(context.Background(), updated); err != nil {
		t.Fatalf("UpdateChannel(): %v", err)
	}
	if got := store.db.channelCacheSize(); got != 0 {
		t.Fatalf("channel cache size after update = %d, want 0", got)
	}
}
