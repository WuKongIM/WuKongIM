package message

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

func TestMessageDBListsLatestMessagesAcrossChannels(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	left, err := store.db.Channel("channel-a", ChannelID{ID: "room-a", Type: 2})
	if err != nil {
		t.Fatalf("Channel(left): %v", err)
	}
	right, err := store.db.Channel("channel-b", ChannelID{ID: "room-b", Type: 1})
	if err != nil {
		t.Fatalf("Channel(right): %v", err)
	}
	if _, err := left.Append(context.Background(), []Record{{ID: 101, Payload: []byte("a1")}, {ID: 103, Payload: []byte("a2")}}, AppendOptions{}); err != nil {
		t.Fatalf("Append(left): %v", err)
	}
	if _, err := right.Append(context.Background(), []Record{{ID: 102, Payload: []byte("b1")}, {ID: 104, Payload: []byte("b2")}}, AppendOptions{}); err != nil {
		t.Fatalf("Append(right): %v", err)
	}

	first, err := store.db.ListLatestMessages(context.Background(), 0, 2)
	if err != nil {
		t.Fatalf("ListLatestMessages(first): %v", err)
	}
	if len(first.Messages) != 2 || first.Messages[0].MessageID != 104 || first.Messages[1].MessageID != 103 {
		t.Fatalf("first messages = %#v, want 104,103", first.Messages)
	}
	if first.Messages[0].ChannelID != "room-b" || first.Messages[0].ChannelType != 1 || !first.HasMore || first.NextBeforeMessageID != 103 {
		t.Fatalf("first page = %#v, want room-b:1 and cursor 103", first)
	}

	second, err := store.db.ListLatestMessages(context.Background(), first.NextBeforeMessageID, 2)
	if err != nil {
		t.Fatalf("ListLatestMessages(second): %v", err)
	}
	if len(second.Messages) != 2 || second.Messages[0].MessageID != 102 || second.Messages[1].MessageID != 101 || second.HasMore {
		t.Fatalf("second page = %#v, want 102,101 final page", second)
	}
}

func TestMessageDBLatestIndexFollowsTruncate(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), testRecords(201, "one", "two", "three"), AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	if err := log.TruncateFrom(context.Background(), 2); err != nil {
		t.Fatalf("TruncateFrom(): %v", err)
	}
	page, err := store.db.ListLatestMessages(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("ListLatestMessages(): %v", err)
	}
	if len(page.Messages) != 1 || page.Messages[0].MessageID != 201 {
		t.Fatalf("messages = %#v, want only 201", page.Messages)
	}
}

func TestMessageDBBackfillsLatestIndexForExistingRows(t *testing.T) {
	path := t.TempDir()
	store := openTestMessageStoreAt(t, path)
	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), testRecords(301, "one", "two"), AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	store.close(t)

	raw, err := engine.Open(path, engine.Options{})
	if err != nil {
		t.Fatalf("engine.Open(raw): %v", err)
	}
	batch := raw.NewBatch()
	span := keycodec.NewPrefixSpan(encodeGlobalMessageIDIndexPrefix())
	if err := batch.DeleteRange(engine.Span{Start: span.Start, End: span.End}); err != nil {
		t.Fatalf("DeleteRange(global index): %v", err)
	}
	if err := batch.Delete(encodeGlobalLatestIndexStateKey()); err != nil {
		t.Fatalf("Delete(index state): %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("Commit(remove current index): %v", err)
	}
	if err := batch.Close(); err != nil {
		t.Fatalf("Batch.Close(): %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("engine.Close(raw): %v", err)
	}

	reopened := openTestMessageStoreAt(t, path)
	defer reopened.close(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := reopened.db.WaitLatestMessageIndex(ctx); err != nil {
		t.Fatalf("WaitLatestMessageIndex(): %v", err)
	}
	if _, ok, err := reopened.engine.Get(encodeGlobalLatestIndexProgressKey()); err != nil || ok {
		t.Fatalf("latest index progress after completion ok=%v err=%v, want deleted", ok, err)
	}
	page, err := reopened.db.ListLatestMessages(ctx, 0, 10)
	if err != nil {
		t.Fatalf("ListLatestMessages(): %v", err)
	}
	if len(page.Messages) != 2 || page.Messages[0].MessageID != 302 || page.Messages[1].MessageID != 301 {
		t.Fatalf("messages = %#v, want backfilled 302,301", page.Messages)
	}
}

func TestMessageDBLatestReadBoundsAndCleansStaleIndexRows(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), testRecords(1, "visible"), AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}

	batch := store.engine.NewBatch()
	for messageID := uint64(1000); messageID <= 1512; messageID++ {
		if err := batch.Set(encodeGlobalMessageIDIndexKey(messageID), encodeGlobalMessageIDIndexValue("missing", messageID)); err != nil {
			t.Fatalf("Set(stale index): %v", err)
		}
	}
	if err := batch.Commit(false); err != nil {
		t.Fatalf("Commit(stale indexes): %v", err)
	}
	if err := batch.Close(); err != nil {
		t.Fatalf("Batch.Close(): %v", err)
	}

	if _, err := store.db.ListLatestMessages(context.Background(), 0, 1); !errors.Is(err, ErrLatestMessageIndexMaintenance) {
		t.Fatalf("ListLatestMessages(stale) error = %v, want bounded maintenance", err)
	}
	page, err := store.db.ListLatestMessages(context.Background(), 0, 1)
	if err != nil {
		t.Fatalf("ListLatestMessages(after cleanup): %v", err)
	}
	if len(page.Messages) != 1 || page.Messages[0].MessageID != 1 {
		t.Fatalf("messages = %#v, want visible message 1", page.Messages)
	}
}

func TestLatestMessageIndexProgressCodecRoundTrips(t *testing.T) {
	want := latestMessageIndexProgress{afterChannel: "channel-a", currentChannel: "channel-b", lastMessageID: 401}
	got, err := decodeLatestMessageIndexProgress(encodeLatestMessageIndexProgress(want))
	if err != nil {
		t.Fatalf("decodeLatestMessageIndexProgress(): %v", err)
	}
	if got != want {
		t.Fatalf("progress = %#v, want %#v", got, want)
	}
}

func TestMessageDBLatestReadDoesNotClaimEndAtRawScanBudget(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	log := testChannelLog(store)
	payloads := make([]string, 51)
	for i := range payloads {
		payloads[i] = "visible"
	}
	if _, err := log.Append(context.Background(), testRecords(1, payloads...), AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}

	batch := store.engine.NewBatch()
	for messageID := uint64(1000); messageID < 1750; messageID++ {
		if err := batch.Set(encodeGlobalMessageIDIndexKey(messageID), encodeGlobalMessageIDIndexValue("missing", messageID)); err != nil {
			t.Fatalf("Set(stale index): %v", err)
		}
	}
	if err := batch.Commit(false); err != nil {
		t.Fatalf("Commit(stale indexes): %v", err)
	}
	if err := batch.Close(); err != nil {
		t.Fatalf("Batch.Close(): %v", err)
	}

	if _, err := store.db.ListLatestMessages(context.Background(), 0, 50); !errors.Is(err, ErrLatestMessageIndexMaintenance) {
		t.Fatalf("ListLatestMessages(budget boundary) error = %v, want maintenance instead of false end", err)
	}
	page, err := store.db.ListLatestMessages(context.Background(), 0, 50)
	if err != nil {
		t.Fatalf("ListLatestMessages(after cleanup): %v", err)
	}
	if len(page.Messages) != 50 || !page.HasMore {
		t.Fatalf("page = %#v, want 50 messages with more", page)
	}
}
