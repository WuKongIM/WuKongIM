package meta

import (
	"context"
	"errors"
	"testing"
)

func TestChannelLatestUpsertKeepsNewestMessageSeq(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(4)

	if err := shard.UpsertChannelLatest(ctx, ChannelLatest{
		ChannelID:      "g1",
		ChannelType:    2,
		LastMessageID:  100,
		LastMessageSeq: 10,
		LastAt:         1000,
		FromUID:        "u1",
		ClientMsgNo:    "c1",
		Payload:        []byte("newer"),
		UpdatedAt:      1001,
	}); err != nil {
		t.Fatalf("UpsertChannelLatest(initial): %v", err)
	}
	if err := shard.UpsertChannelLatest(ctx, ChannelLatest{
		ChannelID:      "g1",
		ChannelType:    2,
		LastMessageID:  90,
		LastMessageSeq: 9,
		LastAt:         900,
		FromUID:        "u2",
		ClientMsgNo:    "stale",
		Payload:        []byte("stale"),
		UpdatedAt:      901,
	}); err != nil {
		t.Fatalf("UpsertChannelLatest(stale): %v", err)
	}

	got, ok, err := shard.GetChannelLatest(ctx, "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetChannelLatest() ok=%v err=%v, want ok", ok, err)
	}
	if got.LastMessageID != 100 || got.LastMessageSeq != 10 || got.LastAt != 1000 || got.FromUID != "u1" || string(got.Payload) != "newer" {
		t.Fatalf("latest after stale upsert = %+v, want initial latest", got)
	}

	if err := shard.UpsertChannelLatest(ctx, ChannelLatest{
		ChannelID:      "g1",
		ChannelType:    2,
		LastMessageID:  110,
		LastMessageSeq: 11,
		LastAt:         1100,
		FromUID:        "u3",
		ClientMsgNo:    "c3",
		Payload:        []byte("fresh"),
		UpdatedAt:      1101,
	}); err != nil {
		t.Fatalf("UpsertChannelLatest(fresh): %v", err)
	}
	got, ok, err = shard.GetChannelLatest(ctx, "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetChannelLatest(fresh) ok=%v err=%v, want ok", ok, err)
	}
	if got.LastMessageID != 110 || got.LastMessageSeq != 11 || got.LastAt != 1100 || got.FromUID != "u3" || string(got.Payload) != "fresh" {
		t.Fatalf("latest after fresh upsert = %+v, want fresh latest", got)
	}
}

func TestChannelLatestBatchWritesMultipleHashSlots(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()

	batch := store.db.NewBatch()
	if err := batch.UpsertChannelLatest(7, ChannelLatest{ChannelID: "g7", ChannelType: 2, LastMessageID: 7, LastMessageSeq: 1}); err != nil {
		t.Fatalf("UpsertChannelLatest(7): %v", err)
	}
	if err := batch.UpsertChannelLatest(2, ChannelLatest{ChannelID: "g2", ChannelType: 2, LastMessageID: 2, LastMessageSeq: 1}); err != nil {
		t.Fatalf("UpsertChannelLatest(2): %v", err)
	}
	if err := batch.Commit(ctx); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	if got := batch.lockedOrderForTest(); len(got) != 2 || got[0] != 2 || got[1] != 7 {
		t.Fatalf("locked order = %+v, want [2 7]", got)
	}
	if _, ok, err := store.db.HashSlot(7).GetChannelLatest(ctx, "g7", 2); err != nil || !ok {
		t.Fatalf("GetChannelLatest(7) ok=%v err=%v, want ok", ok, err)
	}
	if _, ok, err := store.db.HashSlot(2).GetChannelLatest(ctx, "g2", 2); err != nil || !ok {
		t.Fatalf("GetChannelLatest(2) ok=%v err=%v, want ok", ok, err)
	}
}

func TestChannelLatestShardStoreReturnsNotFound(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer db.Close()

	_, err = db.ForHashSlot(3).GetChannelLatest(context.Background(), "missing", 2)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetChannelLatest() err = %v, want ErrNotFound", err)
	}
}
