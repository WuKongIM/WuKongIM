package meta

import (
	"context"
	"errors"
	"reflect"
	"strconv"
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

func TestChannelStatusFlagsRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)

	ch := Channel{ChannelID: "group-status", ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1, AllowStranger: 1, SubscriberMutationVersion: 5}
	if err := shard.CreateChannel(ctx, ch); err != nil {
		t.Fatalf("CreateChannel(): %v", err)
	}

	got, err := shard.GetChannel(ctx, ch.ChannelID, ch.ChannelType)
	if err != nil {
		t.Fatalf("GetChannel(): %v", err)
	}
	if !reflect.DeepEqual(got, ch) {
		t.Fatalf("unexpected channel after create:\n got: %#v\nwant: %#v", got, ch)
	}

	updated := Channel{ChannelID: ch.ChannelID, ChannelType: ch.ChannelType, Ban: 0, Disband: 1, SendBan: 0, AllowStranger: 1}
	if err := shard.UpdateChannel(ctx, updated); err != nil {
		t.Fatalf("UpdateChannel(): %v", err)
	}

	got, err = shard.GetChannel(ctx, ch.ChannelID, ch.ChannelType)
	if err != nil {
		t.Fatalf("GetChannel() after update: %v", err)
	}
	updated.SubscriberMutationVersion = ch.SubscriberMutationVersion
	if !reflect.DeepEqual(got, updated) {
		t.Fatalf("unexpected channel after update:\n got: %#v\nwant: %#v", got, updated)
	}
}

func TestShardListChannelsPagePreservesAllowStranger(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)

	want := Channel{ChannelID: "person-a", ChannelType: 1, AllowStranger: 1}
	if err := shard.CreateChannel(ctx, want); err != nil {
		t.Fatalf("CreateChannel(): %v", err)
	}

	got, _, done, err := shard.ListChannelsPage(ctx, ChannelCursor{}, 10)
	if err != nil {
		t.Fatalf("ListChannelsPage(): %v", err)
	}
	if !done || len(got) != 1 {
		t.Fatalf("unexpected page: got=%#v done=%v", got, done)
	}
	if !reflect.DeepEqual(got[0], want) {
		t.Fatalf("listed channel = %#v, want %#v", got[0], want)
	}
}

func TestUpsertChannelPreservesAllowStranger(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)

	want := Channel{ChannelID: "person-upsert", ChannelType: 1, AllowStranger: 1}
	if err := shard.UpsertChannel(ctx, want); err != nil {
		t.Fatalf("UpsertChannel(): %v", err)
	}

	got, err := shard.GetChannel(ctx, want.ChannelID, want.ChannelType)
	if err != nil {
		t.Fatalf("GetChannel(): %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("upserted channel = %#v, want %#v", got, want)
	}
}

func TestShardListChannelsPageReturnsStableCursor(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)

	channels := []Channel{
		{ChannelID: "a", ChannelType: 2, Ban: 1},
		{ChannelID: "b", ChannelType: 1, Disband: 1},
		{ChannelID: "b", ChannelType: 2, SendBan: 1},
	}
	for _, ch := range channels {
		if err := shard.CreateChannel(ctx, ch); err != nil {
			t.Fatalf("CreateChannel(%s:%d): %v", ch.ChannelID, ch.ChannelType, err)
		}
	}

	page1, cursor, done, err := shard.ListChannelsPage(ctx, ChannelCursor{}, 2)
	if err != nil {
		t.Fatalf("ListChannelsPage(page1): %v", err)
	}
	if done {
		t.Fatal("expected page1 to have more data")
	}
	if got, want := channelKeysForTest(page1), []string{"a:2", "b:1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("page1 keys = %#v, want %#v", got, want)
	}
	if want := (ChannelCursor{ChannelID: "b", ChannelType: 1}); cursor != want {
		t.Fatalf("page1 cursor = %#v, want %#v", cursor, want)
	}

	page2, cursor, done, err := shard.ListChannelsPage(ctx, cursor, 2)
	if err != nil {
		t.Fatalf("ListChannelsPage(page2): %v", err)
	}
	if !done {
		t.Fatal("expected page2 to be done")
	}
	if got, want := channelKeysForTest(page2), []string{"b:2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("page2 keys = %#v, want %#v", got, want)
	}
	if want := (ChannelCursor{ChannelID: "b", ChannelType: 2}); cursor != want {
		t.Fatalf("page2 cursor = %#v, want %#v", cursor, want)
	}
}

func TestShardListChannelsPageRejectsInvalidLimit(t *testing.T) {
	db := openTestDB(t)
	_, _, _, err := db.ForSlot(7).ListChannelsPage(context.Background(), ChannelCursor{}, 0)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ListChannelsPage(limit=0) error = %v, want ErrInvalidArgument", err)
	}
}

func TestChannelSubscriberMutationVersionRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)

	ch := Channel{ChannelID: "group-version", ChannelType: 4, Ban: 1, SubscriberMutationVersion: 11}
	if err := shard.CreateChannel(ctx, ch); err != nil {
		t.Fatalf("CreateChannel(): %v", err)
	}

	got, err := shard.GetChannel(ctx, ch.ChannelID, ch.ChannelType)
	if err != nil {
		t.Fatalf("GetChannel(): %v", err)
	}
	if got.SubscriberMutationVersion != ch.SubscriberMutationVersion || got.Ban != ch.Ban {
		t.Fatalf("unexpected channel after create: %#v", got)
	}

	updated := Channel{ChannelID: ch.ChannelID, ChannelType: ch.ChannelType, Ban: 9}
	if err := shard.UpdateChannel(ctx, updated); err != nil {
		t.Fatalf("UpdateChannel(): %v", err)
	}

	got, err = shard.GetChannel(ctx, ch.ChannelID, ch.ChannelType)
	if err != nil {
		t.Fatalf("GetChannel() after update: %v", err)
	}
	if got.SubscriberMutationVersion != ch.SubscriberMutationVersion {
		t.Fatalf("subscriber mutation version after update = %d, want %d", got.SubscriberMutationVersion, ch.SubscriberMutationVersion)
	}
	if got.Ban != updated.Ban {
		t.Fatalf("ban after update = %d, want %d", got.Ban, updated.Ban)
	}
}

func TestGetChannelUsesWarmCacheForRepeatedVersionReads(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)

	ch := Channel{ChannelID: "group-cache", ChannelType: 2, Ban: 1, SubscriberMutationVersion: 11}
	if err := shard.CreateChannel(ctx, ch); err != nil {
		t.Fatalf("CreateChannel(): %v", err)
	}

	var gets int
	db.testHooks.beforeGetValue = func(key []byte) {
		if string(key) == string(encodeChannelPrimaryKey(7, ch.ChannelID, ch.ChannelType, channelPrimaryFamilyID)) {
			gets++
		}
	}
	if _, err := shard.GetChannel(ctx, ch.ChannelID, ch.ChannelType); err != nil {
		t.Fatalf("first GetChannel(): %v", err)
	}
	if _, err := shard.GetChannel(ctx, ch.ChannelID, ch.ChannelType); err != nil {
		t.Fatalf("second GetChannel(): %v", err)
	}
	if gets != 0 {
		t.Fatalf("channel primary Pebble reads after repeated GetChannel = %d, want 0", gets)
	}

	if err := shard.AddSubscribers(ctx, ch.ChannelID, ch.ChannelType, []string{"u1"}, 12); err != nil {
		t.Fatalf("AddSubscribers(version 12): %v", err)
	}
	gets = 0
	got, err := shard.GetChannel(ctx, ch.ChannelID, ch.ChannelType)
	if err != nil {
		t.Fatalf("GetChannel() after subscriber mutation: %v", err)
	}
	if got.SubscriberMutationVersion != 12 {
		t.Fatalf("subscriber mutation version after add = %d, want 12", got.SubscriberMutationVersion)
	}
	if gets != 0 {
		t.Fatalf("channel primary Pebble reads after cached subscriber mutation = %d, want 0", gets)
	}

	wb := db.NewWriteBatch()
	if err := wb.AddSubscribers(7, ch.ChannelID, ch.ChannelType, []string{"u2"}, 13); err != nil {
		t.Fatalf("WriteBatch AddSubscribers(version 13): %v", err)
	}
	if err := wb.Commit(); err != nil {
		t.Fatalf("WriteBatch Commit(): %v", err)
	}
	wb.Close()
	gets = 0
	got, err = shard.GetChannel(ctx, ch.ChannelID, ch.ChannelType)
	if err != nil {
		t.Fatalf("GetChannel() after batch subscriber mutation: %v", err)
	}
	if got.SubscriberMutationVersion != 13 {
		t.Fatalf("subscriber mutation version after batch add = %d, want 13", got.SubscriberMutationVersion)
	}
	if gets != 0 {
		t.Fatalf("channel primary Pebble reads after batch cached mutation = %d, want 0", gets)
	}
}

func channelKeysForTest(channels []Channel) []string {
	out := make([]string, 0, len(channels))
	for _, ch := range channels {
		out = append(out, ch.ChannelID+":"+strconv.FormatInt(ch.ChannelType, 10))
	}
	return out
}

func TestSubscriberMutationVersionAdvancesMonotonically(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(8)

	ch := Channel{ChannelID: "group-monotonic", ChannelType: 2, Ban: 0, SubscriberMutationVersion: 1}
	if err := shard.CreateChannel(ctx, ch); err != nil {
		t.Fatalf("CreateChannel(): %v", err)
	}

	if err := shard.AddSubscribers(ctx, ch.ChannelID, ch.ChannelType, []string{"u1", "u2"}, 2); err != nil {
		t.Fatalf("AddSubscribers(version 2): %v", err)
	}

	got, err := shard.GetChannel(ctx, ch.ChannelID, ch.ChannelType)
	if err != nil {
		t.Fatalf("GetChannel() after add: %v", err)
	}
	if got.SubscriberMutationVersion != 2 {
		t.Fatalf("subscriber mutation version after add = %d, want 2", got.SubscriberMutationVersion)
	}

	if err := shard.RemoveSubscribers(ctx, ch.ChannelID, ch.ChannelType, []string{"u1"}, 1); !errors.Is(err, ErrStaleMeta) {
		t.Fatalf("RemoveSubscribers(version 1) error = %v, want ErrStaleMeta", err)
	}

	if err := shard.RemoveSubscribers(ctx, ch.ChannelID, ch.ChannelType, []string{"u1"}, 3); err != nil {
		t.Fatalf("RemoveSubscribers(version 3): %v", err)
	}

	got, err = shard.GetChannel(ctx, ch.ChannelID, ch.ChannelType)
	if err != nil {
		t.Fatalf("GetChannel() after remove: %v", err)
	}
	if got.SubscriberMutationVersion != 3 {
		t.Fatalf("subscriber mutation version after remove = %d, want 3", got.SubscriberMutationVersion)
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
