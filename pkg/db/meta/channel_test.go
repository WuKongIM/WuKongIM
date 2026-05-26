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

func TestChannelListByChannelIDSkipsStaleRuntimeIndex(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(31)

	if err := shard.UpsertChannel(ctx, Channel{ChannelID: "ch", ChannelType: 1, Ban: 1}); err != nil {
		t.Fatalf("UpsertChannel: %v", err)
	}

	staleKey := encodeChannelIDIndexKey(31, "stale", 99)
	batch := store.engine.NewBatch()
	if err := batch.Set(staleKey, nil); err != nil {
		t.Fatalf("set stale index: %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("commit stale index: %v", err)
	}
	batch.Close()

	rows, err := shard.ListChannelsByChannelID(ctx, "stale")
	if err != nil || len(rows) != 0 {
		t.Fatalf("stale rows=%#v err=%v", rows, err)
	}
}

func TestChannelListByChannelIDReadsExistingIndexLayout(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(33)

	channel := Channel{ChannelID: "legacy", ChannelType: 2, Ban: 1}
	batch := store.engine.NewBatch()
	if err := batch.Set(encodeChannelRowKey(33, channel.ChannelID, channel.ChannelType, channelPrimaryFamilyID), encodeChannelValue(channel)); err != nil {
		t.Fatalf("set primary row: %v", err)
	}
	if err := batch.Set(encodeChannelIDIndexKey(33, channel.ChannelID, channel.ChannelType), encodeChannelValue(channel)); err != nil {
		t.Fatalf("set channel id index: %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("commit existing layout: %v", err)
	}
	batch.Close()

	rows, err := shard.ListChannelsByChannelID(ctx, channel.ChannelID)
	if err != nil || len(rows) != 1 || rows[0] != channel {
		t.Fatalf("existing layout rows=%#v err=%v", rows, err)
	}
}

func TestChannelIDIndexStoresLegacyValue(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(44)

	channel := Channel{ChannelID: "legacy-value", ChannelType: 1, Ban: 1}
	if err := shard.UpsertChannel(ctx, channel); err != nil {
		t.Fatalf("UpsertChannel: %v", err)
	}
	updated := channel
	updated.Ban = 2
	if err := shard.UpdateChannel(ctx, updated); err != nil {
		t.Fatalf("UpdateChannel: %v", err)
	}

	value, ok, err := store.db.get(encodeChannelIDIndexKey(44, updated.ChannelID, updated.ChannelType))
	if err != nil || !ok {
		t.Fatalf("channel id index ok=%v err=%v", ok, err)
	}
	got, err := decodeChannelValue(updated.ChannelID, updated.ChannelType, value)
	if err != nil || got != updated {
		t.Fatalf("channel id index value = %+v err=%v, want %+v", got, err, updated)
	}
}

func TestChannelDeleteRemovesOrphanIDIndex(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(34)

	indexKey := encodeChannelIDIndexKey(34, "orphan", 3)
	batch := store.engine.NewBatch()
	if err := batch.Set(indexKey, nil); err != nil {
		t.Fatalf("set orphan index: %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("commit orphan index: %v", err)
	}
	batch.Close()

	if err := shard.DeleteChannel(ctx, "orphan", 3); err != nil {
		t.Fatalf("DeleteChannel: %v", err)
	}
	if _, ok, err := store.db.get(indexKey); err != nil || ok {
		t.Fatalf("orphan index after delete ok=%v err=%v", ok, err)
	}
}

func TestChannelDeleteRemovesCorruptPrimary(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(35)

	primaryKey := encodeChannelRowKey(35, "corrupt", 4, channelPrimaryFamilyID)
	indexKey := encodeChannelIDIndexKey(35, "corrupt", 4)
	batch := store.engine.NewBatch()
	if err := batch.Set(primaryKey, []byte{1}); err != nil {
		t.Fatalf("set corrupt primary: %v", err)
	}
	if err := batch.Set(indexKey, nil); err != nil {
		t.Fatalf("set channel id index: %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("commit corrupt primary: %v", err)
	}
	batch.Close()

	if err := shard.DeleteChannel(ctx, "corrupt", 4); err != nil {
		t.Fatalf("DeleteChannel: %v", err)
	}
	if _, ok, err := store.db.get(primaryKey); err != nil || ok {
		t.Fatalf("corrupt primary after delete ok=%v err=%v", ok, err)
	}
	if _, ok, err := store.db.get(indexKey); err != nil || ok {
		t.Fatalf("index after delete ok=%v err=%v", ok, err)
	}
}

func TestChannelCreateRejectsCorruptExistingPrimary(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(38)

	primaryKey := encodeChannelRowKey(38, "corrupt-create", 1, channelPrimaryFamilyID)
	batch := store.engine.NewBatch()
	if err := batch.Set(primaryKey, []byte{1}); err != nil {
		t.Fatalf("set corrupt primary: %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("commit corrupt primary: %v", err)
	}
	batch.Close()

	err := shard.CreateChannel(ctx, Channel{ChannelID: "corrupt-create", ChannelType: 1})
	if !errors.Is(err, dberrors.ErrAlreadyExists) {
		t.Fatalf("CreateChannel err = %v, want ErrAlreadyExists", err)
	}
}

func TestChannelUpsertOverwritesCorruptPrimary(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(39)

	primaryKey := encodeChannelRowKey(39, "corrupt-upsert", 2, channelPrimaryFamilyID)
	batch := store.engine.NewBatch()
	if err := batch.Set(primaryKey, []byte{1}); err != nil {
		t.Fatalf("set corrupt primary: %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("commit corrupt primary: %v", err)
	}
	batch.Close()

	channel := Channel{ChannelID: "corrupt-upsert", ChannelType: 2, Ban: 9}
	if err := shard.UpsertChannel(ctx, channel); err != nil {
		t.Fatalf("UpsertChannel: %v", err)
	}
	got, ok, err := shard.GetChannel(ctx, channel.ChannelID, channel.ChannelType)
	if err != nil || !ok || got != channel {
		t.Fatalf("GetChannel after upsert = %+v ok=%v err=%v, want %+v", got, ok, err, channel)
	}
}

func TestChannelUpdateOverwritesCorruptPrimary(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(40)

	primaryKey := encodeChannelRowKey(40, "corrupt-update", 3, channelPrimaryFamilyID)
	batch := store.engine.NewBatch()
	if err := batch.Set(primaryKey, []byte{1}); err != nil {
		t.Fatalf("set corrupt primary: %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("commit corrupt primary: %v", err)
	}
	batch.Close()

	channel := Channel{ChannelID: "corrupt-update", ChannelType: 3, Ban: 7}
	if err := shard.UpdateChannel(ctx, channel); err != nil {
		t.Fatalf("UpdateChannel: %v", err)
	}
	got, ok, err := shard.GetChannel(ctx, channel.ChannelID, channel.ChannelType)
	if err != nil || !ok || got != channel {
		t.Fatalf("GetChannel after update = %+v ok=%v err=%v, want %+v", got, ok, err, channel)
	}
}
