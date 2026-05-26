package meta

import (
	"context"
	"testing"
)

func TestChannelRuntimeMetaPageReturnsOrderedPage(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(9)

	metas := []ChannelRuntimeMeta{
		testRuntimeMeta("g2", 2),
		testRuntimeMeta("g1", 2),
		testRuntimeMeta("g1", 1),
	}
	for i := range metas {
		metas[i].ChannelEpoch = uint64(10 + i)
		if _, err := shard.UpsertChannelRuntimeMeta(context.Background(), metas[i]); err != nil {
			t.Fatalf("UpsertChannelRuntimeMeta(%+v): %v", metas[i], err)
		}
	}

	page, cursor, done, err := shard.ListChannelRuntimeMetaPage(context.Background(), ChannelRuntimeMetaCursor{}, 2)
	if err != nil {
		t.Fatalf("ListChannelRuntimeMetaPage(): %v", err)
	}
	if done {
		t.Fatal("first page done = true, want false")
	}
	if len(page) != 2 || page[0].ChannelID != "g1" || page[0].ChannelType != 1 || page[1].ChannelID != "g1" || page[1].ChannelType != 2 {
		t.Fatalf("first page = %+v, want g1/1 g1/2", page)
	}
	if cursor != (ChannelRuntimeMetaCursor{ChannelID: "g1", ChannelType: 2}) {
		t.Fatalf("cursor = %+v, want g1/2", cursor)
	}

	page, cursor, done, err = shard.ListChannelRuntimeMetaPage(context.Background(), cursor, 2)
	if err != nil {
		t.Fatalf("ListChannelRuntimeMetaPage(next): %v", err)
	}
	if !done {
		t.Fatal("next page done = false, want true")
	}
	if len(page) != 1 || page[0].ChannelID != "g2" || page[0].ChannelType != 2 {
		t.Fatalf("next page = %+v, want g2/2", page)
	}
	if cursor != (ChannelRuntimeMetaCursor{ChannelID: "g2", ChannelType: 2}) {
		t.Fatalf("next cursor = %+v, want g2/2", cursor)
	}
}

func TestChannelRuntimeMetaPageExactLimitIsDone(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(9)

	for _, meta := range []ChannelRuntimeMeta{
		testRuntimeMeta("exact-a", 1),
		testRuntimeMeta("exact-b", 1),
	} {
		if _, err := shard.UpsertChannelRuntimeMeta(context.Background(), meta); err != nil {
			t.Fatalf("UpsertChannelRuntimeMeta(%+v): %v", meta, err)
		}
	}

	page, cursor, done, err := shard.ListChannelRuntimeMetaPage(context.Background(), ChannelRuntimeMetaCursor{}, 2)
	if err != nil {
		t.Fatalf("ListChannelRuntimeMetaPage(): %v", err)
	}
	if !done {
		t.Fatal("done = false, want true for exact final page")
	}
	if len(page) != 2 || cursor != (ChannelRuntimeMetaCursor{ChannelID: "exact-b", ChannelType: 1}) {
		t.Fatalf("page=%+v cursor=%+v, want two rows ending exact-b/1", page, cursor)
	}
}

func TestChannelRuntimeMetaPageRejectsInvalidCursor(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(9)

	_, _, _, err := shard.ListChannelRuntimeMetaPage(context.Background(), ChannelRuntimeMetaCursor{ChannelType: 1}, 2)
	if err == nil {
		t.Fatal("ListChannelRuntimeMetaPage(empty cursor channel id) err = nil, want error")
	}
}
