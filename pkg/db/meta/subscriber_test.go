package meta

import (
	"context"
	"testing"
)

func TestSubscriberAddListContainsAndRemove(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(1)
	channel := Channel{ChannelID: "group-sub", ChannelType: 1}
	if err := shard.CreateChannel(context.Background(), channel); err != nil {
		t.Fatalf("CreateChannel(): %v", err)
	}

	if err := shard.AddSubscribers(context.Background(), channel.ChannelID, channel.ChannelType, []string{"u2", "u1", "u1"}, 7); err != nil {
		t.Fatalf("AddSubscribers(): %v", err)
	}
	list, cursor, done, err := shard.ListSubscribersPage(context.Background(), channel.ChannelID, channel.ChannelType, "", 1)
	if err != nil {
		t.Fatalf("ListSubscribersPage(): %v", err)
	}
	if done || cursor != "u1" || len(list) != 1 || list[0] != "u1" {
		t.Fatalf("first page = %+v cursor=%q done=%v", list, cursor, done)
	}
	list, cursor, done, err = shard.ListSubscribersPage(context.Background(), channel.ChannelID, channel.ChannelType, cursor, 10)
	if err != nil {
		t.Fatalf("ListSubscribersPage(next): %v", err)
	}
	if !done || cursor != "" || len(list) != 1 || list[0] != "u2" {
		t.Fatalf("next page = %+v cursor=%q done=%v", list, cursor, done)
	}
	ok, err := shard.ContainsSubscriber(context.Background(), channel.ChannelID, channel.ChannelType, "u2")
	if err != nil || !ok {
		t.Fatalf("ContainsSubscriber(u2) = %v err %v, want true", ok, err)
	}
	has, err := shard.HasSubscribers(context.Background(), channel.ChannelID, channel.ChannelType)
	if err != nil || !has {
		t.Fatalf("HasSubscribers() = %v err %v, want true", has, err)
	}
	snapshot, err := shard.SnapshotSubscribers(context.Background(), channel.ChannelID, channel.ChannelType)
	if err != nil {
		t.Fatalf("SnapshotSubscribers(): %v", err)
	}
	if len(snapshot) != 2 || snapshot[0] != "u1" || snapshot[1] != "u2" {
		t.Fatalf("snapshot = %+v, want [u1 u2]", snapshot)
	}

	if err := shard.RemoveSubscribers(context.Background(), channel.ChannelID, channel.ChannelType, []string{"u1"}, 9); err != nil {
		t.Fatalf("RemoveSubscribers(): %v", err)
	}
	ok, err = shard.ContainsSubscriber(context.Background(), channel.ChannelID, channel.ChannelType, "u1")
	if err != nil || ok {
		t.Fatalf("ContainsSubscriber(u1) = %v err %v, want false", ok, err)
	}
	snapshot, err = shard.SnapshotSubscribers(context.Background(), channel.ChannelID, channel.ChannelType)
	if err != nil {
		t.Fatalf("SnapshotSubscribers(after remove): %v", err)
	}
	if len(snapshot) != 1 || snapshot[0] != "u2" {
		t.Fatalf("snapshot after remove = %+v, want [u2]", snapshot)
	}
}

func TestSubscriberMutationVersionAdvancesAndInvalidatesChannelCache(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(1)
	channel := Channel{ChannelID: "group-cache-sub", ChannelType: 1, SubscriberMutationVersion: 5}
	if err := shard.CreateChannel(context.Background(), channel); err != nil {
		t.Fatalf("CreateChannel(): %v", err)
	}
	if _, ok, err := shard.GetChannel(context.Background(), channel.ChannelID, channel.ChannelType); err != nil || !ok {
		t.Fatalf("GetChannel() ok=%v err=%v", ok, err)
	}
	if got := store.db.channelCacheSize(); got != 1 {
		t.Fatalf("cache size after read = %d, want 1", got)
	}
	if err := shard.AddSubscribers(context.Background(), channel.ChannelID, channel.ChannelType, []string{"u1"}, 4); err != nil {
		t.Fatalf("AddSubscribers(low version): %v", err)
	}
	if got := store.db.channelCacheSize(); got != 0 {
		t.Fatalf("cache size after subscribers = %d, want 0", got)
	}
	got, ok, err := shard.GetChannel(context.Background(), channel.ChannelID, channel.ChannelType)
	if err != nil || !ok {
		t.Fatalf("GetChannel(after low version) ok=%v err=%v", ok, err)
	}
	if got.SubscriberMutationVersion != 5 {
		t.Fatalf("version after low request = %d, want 5", got.SubscriberMutationVersion)
	}
	if err := shard.AddSubscribers(context.Background(), channel.ChannelID, channel.ChannelType, []string{"u2"}, 8); err != nil {
		t.Fatalf("AddSubscribers(high version): %v", err)
	}
	got, ok, err = shard.GetChannel(context.Background(), channel.ChannelID, channel.ChannelType)
	if err != nil || !ok {
		t.Fatalf("GetChannel(after high version) ok=%v err=%v", ok, err)
	}
	if got.SubscriberMutationVersion != 8 {
		t.Fatalf("version after high request = %d, want 8", got.SubscriberMutationVersion)
	}
}
