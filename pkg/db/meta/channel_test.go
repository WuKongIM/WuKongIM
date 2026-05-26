package meta

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestChannelCRUDAndIDIndex(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(7)

	first := Channel{ChannelID: "group", ChannelType: 1, Ban: 1}
	second := Channel{ChannelID: "group", ChannelType: 2, Ban: 2}
	if err := shard.CreateChannel(context.Background(), first); err != nil {
		t.Fatalf("CreateChannel(first): %v", err)
	}
	if err := shard.CreateChannel(context.Background(), first); !errors.Is(err, dberrors.ErrAlreadyExists) {
		t.Fatalf("CreateChannel(duplicate) err = %v, want already exists", err)
	}
	if err := shard.CreateChannel(context.Background(), second); err != nil {
		t.Fatalf("CreateChannel(second): %v", err)
	}
	list, err := shard.ListChannelsByChannelID(context.Background(), "group")
	if err != nil {
		t.Fatalf("ListChannelsByChannelID(): %v", err)
	}
	if len(list) != 2 || list[0] != first || list[1] != second {
		t.Fatalf("channel list = %+v, want first/second", list)
	}
	updated := Channel{ChannelID: "group", ChannelType: 1, Ban: 9, Disband: 1, SendBan: 1, AllowStranger: 1, SubscriberMutationVersion: 5}
	if err := shard.UpdateChannel(context.Background(), updated); err != nil {
		t.Fatalf("UpdateChannel(): %v", err)
	}
	got, ok, err := shard.GetChannel(context.Background(), "group", 1)
	if err != nil || !ok || got != updated {
		t.Fatalf("GetChannel() = (%+v, %v, %v), want %+v", got, ok, err, updated)
	}
	if err := shard.UpdateChannel(context.Background(), Channel{ChannelID: "missing", ChannelType: 1}); !errors.Is(err, dberrors.ErrNotFound) {
		t.Fatalf("UpdateChannel(missing) err = %v, want not found", err)
	}
	if err := shard.DeleteChannel(context.Background(), "group", 1); err != nil {
		t.Fatalf("DeleteChannel(): %v", err)
	}
	if _, ok, err := shard.GetChannel(context.Background(), "group", 1); err != nil || ok {
		t.Fatalf("GetChannel(deleted) = ok %v err %v, want missing", ok, err)
	}
	list, err = shard.ListChannelsByChannelID(context.Background(), "group")
	if err != nil {
		t.Fatalf("ListChannelsByChannelID(after delete): %v", err)
	}
	if len(list) != 1 || list[0] != second {
		t.Fatalf("channel list after delete = %+v, want second only", list)
	}
}
