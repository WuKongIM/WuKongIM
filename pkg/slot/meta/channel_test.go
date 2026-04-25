package meta

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestChannelIndexScanIsSlotScoped(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	left := db.ForSlot(1)
	right := db.ForSlot(2)

	ch1 := Channel{ChannelID: "group-001", ChannelType: 1, Ban: 0}
	ch2 := Channel{ChannelID: "group-001", ChannelType: 2, Ban: 1}
	ch3 := Channel{ChannelID: "group-002", ChannelType: 1, Ban: 0}

	if err := left.CreateChannel(ctx, ch1); err != nil {
		t.Fatalf("create ch1: %v", err)
	}
	if err := left.CreateChannel(ctx, ch2); err != nil {
		t.Fatalf("create ch2: %v", err)
	}
	if err := left.CreateChannel(ctx, ch3); err != nil {
		t.Fatalf("create ch3: %v", err)
	}

	if err := left.CreateChannel(ctx, ch1); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}

	list, err := right.ListChannelsByChannelID(ctx, "group-001")
	if err != nil {
		t.Fatalf("right list before create: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty right list, got %#v", list)
	}

	got, err := left.GetChannel(ctx, ch1.ChannelID, ch1.ChannelType)
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	if !reflect.DeepEqual(got, ch1) {
		t.Fatalf("unexpected channel:\n got: %#v\nwant: %#v", got, ch1)
	}

	updated := Channel{ChannelID: "group-001", ChannelType: 1, Ban: 9}
	if err := left.UpdateChannel(ctx, updated); err != nil {
		t.Fatalf("update channel: %v", err)
	}

	got, err = left.GetChannel(ctx, updated.ChannelID, updated.ChannelType)
	if err != nil {
		t.Fatalf("get updated channel: %v", err)
	}
	if !reflect.DeepEqual(got, updated) {
		t.Fatalf("unexpected updated channel:\n got: %#v\nwant: %#v", got, updated)
	}

	list, err = left.ListChannelsByChannelID(ctx, "group-001")
	if err != nil {
		t.Fatalf("list by channel id: %v", err)
	}
	wantList := []Channel{updated, ch2}
	if !reflect.DeepEqual(list, wantList) {
		t.Fatalf("unexpected channel list:\n got: %#v\nwant: %#v", list, wantList)
	}

	if err := right.CreateChannel(ctx, Channel{ChannelID: "group-001", ChannelType: 1, Ban: 7}); err != nil {
		t.Fatalf("create same channel in right slot: %v", err)
	}

	if err := left.DeleteChannel(ctx, updated.ChannelID, updated.ChannelType); err != nil {
		t.Fatalf("delete updated channel: %v", err)
	}

	_, err = left.GetChannel(ctx, updated.ChannelID, updated.ChannelType)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}

	list, err = left.ListChannelsByChannelID(ctx, "group-001")
	if err != nil {
		t.Fatalf("list after one delete: %v", err)
	}
	wantList = []Channel{ch2}
	if !reflect.DeepEqual(list, wantList) {
		t.Fatalf("unexpected channel list after one delete:\n got: %#v\nwant: %#v", list, wantList)
	}

	if err := left.DeleteChannel(ctx, ch2.ChannelID, ch2.ChannelType); err != nil {
		t.Fatalf("delete ch2: %v", err)
	}

	list, err = left.ListChannelsByChannelID(ctx, "group-001")
	if err != nil {
		t.Fatalf("list after deleting indexed rows: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %#v", list)
	}

	list, err = right.ListChannelsByChannelID(ctx, "group-001")
	if err != nil {
		t.Fatalf("right list after left delete: %v", err)
	}
	wantList = []Channel{{ChannelID: "group-001", ChannelType: 1, Ban: 7}}
	if !reflect.DeepEqual(list, wantList) {
		t.Fatalf("unexpected right slot channel list:\n got: %#v\nwant: %#v", list, wantList)
	}
}

func TestDeleteChannelRemovesOnlySlotLocalIndex(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	left := db.ForSlot(1)
	right := db.ForSlot(2)

	leftChannel := Channel{ChannelID: "group-001", ChannelType: 1, Ban: 0}
	rightChannel := Channel{ChannelID: "group-001", ChannelType: 1, Ban: 1}

	if err := left.CreateChannel(ctx, leftChannel); err != nil {
		t.Fatalf("left create: %v", err)
	}
	if err := right.CreateChannel(ctx, rightChannel); err != nil {
		t.Fatalf("right create: %v", err)
	}

	if err := left.DeleteChannel(ctx, leftChannel.ChannelID, leftChannel.ChannelType); err != nil {
		t.Fatalf("left delete: %v", err)
	}

	leftList, err := left.ListChannelsByChannelID(ctx, leftChannel.ChannelID)
	if err != nil {
		t.Fatalf("left list: %v", err)
	}
	if len(leftList) != 0 {
		t.Fatalf("expected empty left list, got %#v", leftList)
	}

	rightList, err := right.ListChannelsByChannelID(ctx, rightChannel.ChannelID)
	if err != nil {
		t.Fatalf("right list: %v", err)
	}
	if !reflect.DeepEqual(rightList, []Channel{rightChannel}) {
		t.Fatalf("unexpected right list: %#v", rightList)
	}
}

func TestDeleteChannelRemovesSubscribers(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(1)

	channel := Channel{ChannelID: "group-cleanup", ChannelType: 1}
	if err := shard.CreateChannel(ctx, channel); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := shard.AddSubscribers(ctx, channel.ChannelID, channel.ChannelType, []string{"u1", "u2"}); err != nil {
		t.Fatalf("add subscribers: %v", err)
	}

	if err := shard.DeleteChannel(ctx, channel.ChannelID, channel.ChannelType); err != nil {
		t.Fatalf("delete channel: %v", err)
	}

	subscribers, cursor, done, err := shard.ListSubscribersPage(ctx, channel.ChannelID, channel.ChannelType, "", 10)
	if err != nil {
		t.Fatalf("list subscribers: %v", err)
	}
	if len(subscribers) != 0 {
		t.Fatalf("expected subscribers to be removed, got %#v", subscribers)
	}
	if cursor != "" {
		t.Fatalf("expected empty cursor, got %q", cursor)
	}
	if !done {
		t.Fatal("expected done after subscriber cleanup")
	}
}

func TestChannelIndexValueTracksListPayload(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)

	ch := Channel{ChannelID: "group-001", ChannelType: 3, Ban: 1}
	if err := shard.CreateChannel(ctx, ch); err != nil {
		t.Fatalf("CreateChannel(): %v", err)
	}

	indexKey := encodeChannelIDIndexKey(7, ch.ChannelID, ch.ChannelType)
	value, err := db.getValue(indexKey)
	if err != nil {
		t.Fatalf("getValue(indexKey): %v", err)
	}
	ban, err := decodeChannelIndexValue(indexKey, value)
	if err != nil {
		t.Fatalf("decodeChannelIndexValue(create): %v", err)
	}
	if ban != ch.Ban {
		t.Fatalf("create index ban = %d, want %d", ban, ch.Ban)
	}

	updated := Channel{ChannelID: ch.ChannelID, ChannelType: ch.ChannelType, Ban: 9}
	if err := shard.UpdateChannel(ctx, updated); err != nil {
		t.Fatalf("UpdateChannel(): %v", err)
	}

	value, err = db.getValue(indexKey)
	if err != nil {
		t.Fatalf("getValue(indexKey) after update: %v", err)
	}
	ban, err = decodeChannelIndexValue(indexKey, value)
	if err != nil {
		t.Fatalf("decodeChannelIndexValue(update): %v", err)
	}
	if ban != updated.Ban {
		t.Fatalf("updated index ban = %d, want %d", ban, updated.Ban)
	}
}

func TestListChannelsByChannelIDHonorsCanceledContext(t *testing.T) {
	db := openTestDB(t)
	shard := db.ForSlot(1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := shard.ListChannelsByChannelID(ctx, "group-001")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestCreateChannelRejectsOverlongChannelID(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(1)

	ch := Channel{ChannelID: strings.Repeat("c", maxKeyStringLen+1), ChannelType: 1}
	err := shard.CreateChannel(ctx, ch)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
}
