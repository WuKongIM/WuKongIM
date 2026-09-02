package message

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestLatestMessagePageOrdersAcrossChannelsAndUsesExclusiveCursor(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	left := mustAcquireChannel(t, store.db, "latest-left", ChannelID{ID: "room-left", Type: 2})
	right := mustAcquireChannel(t, store.db, "latest-right", ChannelID{ID: "room-right", Type: 1})
	defer left.Close()
	defer right.Close()
	if _, err := left.Append(context.Background(), []Record{{ID: 101, Payload: []byte("left-1")}, {ID: 103, Payload: []byte("left-2")}}, AppendOptions{}); err != nil {
		t.Fatalf("Append(left) error = %v", err)
	}
	if _, err := right.Append(context.Background(), []Record{{ID: 102, Payload: []byte("right-1")}, {ID: 104, Payload: []byte("right-2")}}, AppendOptions{}); err != nil {
		t.Fatalf("Append(right) error = %v", err)
	}

	first, err := store.db.ListLatestMessages(context.Background(), 0, 2)
	if err != nil {
		t.Fatalf("ListLatestMessages(first) error = %v", err)
	}
	if len(first.Messages) != 2 || first.Messages[0].MessageID != 104 || first.Messages[1].MessageID != 103 ||
		first.Messages[0].ChannelID != "room-right" || first.Messages[0].ChannelType != 1 ||
		!first.HasMore || first.NextBeforeMessageID != 103 {
		t.Fatalf("first page = %#v", first)
	}
	second, err := store.db.ListLatestMessages(context.Background(), first.NextBeforeMessageID, 2)
	if err != nil {
		t.Fatalf("ListLatestMessages(second) error = %v", err)
	}
	if len(second.Messages) != 2 || second.Messages[0].MessageID != 102 || second.Messages[1].MessageID != 101 ||
		second.HasMore || second.NextBeforeMessageID != 0 {
		t.Fatalf("second page = %#v", second)
	}
}

func TestLatestMessagePageCleansStaleGlobalIndexWithoutHidingValidRows(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	log := mustAcquireChannel(t, store.db, "latest-stale", ChannelID{ID: "latest-stale", Type: 1})
	defer log.Close()
	if _, err := log.Append(context.Background(), []Record{{ID: 201, Payload: []byte("visible")}}, AppendOptions{}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	staleKey := encodeGlobalMessageIDIndexKey(999)
	batch := store.engine.NewBatch()
	if err := batch.Set(staleKey, encodeGlobalMessageIDIndexValue("missing-channel", 1)); err != nil {
		batch.Close()
		t.Fatalf("Set(stale index) error = %v", err)
	}
	if err := batch.Commit(false); err != nil {
		batch.Close()
		t.Fatalf("Commit(stale index) error = %v", err)
	}
	if err := batch.Close(); err != nil {
		t.Fatalf("Close(stale batch) error = %v", err)
	}

	page, err := store.db.ListLatestMessages(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("ListLatestMessages() error = %v", err)
	}
	if len(page.Messages) != 1 || page.Messages[0].MessageID != 201 || page.HasMore {
		t.Fatalf("page after stale cleanup = %#v", page)
	}
	if _, present, err := store.engine.Get(staleKey); err != nil || present {
		t.Fatalf("stale global index present = %v, error = %v", present, err)
	}
}

func TestLatestMessageReadinessAndInputGuardsFailClosed(t *testing.T) {
	store := openTestMessageStore(t)
	originalState := store.db.latestIndex
	building := newLatestMessageIndexState()
	store.db.latestIndex = building
	if _, err := store.db.ListLatestMessages(context.Background(), 0, 1); !errors.Is(err, ErrLatestMessageIndexBuilding) {
		t.Fatalf("ListLatestMessages(building) error = %v", err)
	}
	startupErr := errors.New("latest index startup failed")
	building.finish(startupErr)
	if _, err := store.db.ListLatestMessages(context.Background(), 0, 1); !errors.Is(err, startupErr) {
		t.Fatalf("ListLatestMessages(failed startup) error = %v", err)
	}
	store.db.latestIndex = originalState
	if _, err := store.db.ListLatestMessages(context.Background(), 0, 0); !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("ListLatestMessages(zero limit) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.db.ListLatestMessages(ctx, 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListLatestMessages(canceled) error = %v", err)
	}
	store.close(t)
	if _, err := store.db.ListLatestMessages(context.Background(), 0, 1); !errors.Is(err, dberrors.ErrClosed) {
		t.Fatalf("ListLatestMessages(closed) error = %v", err)
	}
}

func TestLatestIndexWaitAndValueCodecPreserveCanonicalIdentity(t *testing.T) {
	state := newLatestMessageIndexState()
	db := &MessageDB{latestIndex: state}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := db.WaitLatestMessageIndex(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitLatestMessageIndex(canceled) error = %v", err)
	}
	startupErr := errors.New("startup failed")
	state.finish(startupErr)
	if err := db.WaitLatestMessageIndex(context.Background()); !errors.Is(err, startupErr) {
		t.Fatalf("WaitLatestMessageIndex(finished) error = %v", err)
	}
	var nilDB *MessageDB
	if err := nilDB.WaitLatestMessageIndex(context.Background()); !errors.Is(err, dberrors.ErrClosed) {
		t.Fatalf("WaitLatestMessageIndex(nil) error = %v", err)
	}

	encoded := encodeGlobalMessageIDIndexValue("channel-key", 42)
	channelKey, seq, err := decodeGlobalMessageIDIndexValue(encoded)
	if err != nil || channelKey != "channel-key" || seq != 42 {
		t.Fatalf("latest index round trip = %q/%d, %v", channelKey, seq, err)
	}
	for _, malformed := range [][]byte{nil, encodeGlobalMessageIDIndexValue("", 1), encodeGlobalMessageIDIndexValue("channel-key", 0)} {
		if _, _, err := decodeGlobalMessageIDIndexValue(malformed); !errors.Is(err, dberrors.ErrCorruptValue) {
			t.Fatalf("decodeGlobalMessageIDIndexValue(%x) error = %v", malformed, err)
		}
	}
}
