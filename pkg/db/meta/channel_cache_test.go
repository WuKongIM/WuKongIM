package meta

import (
	"context"
	"testing"
)

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
