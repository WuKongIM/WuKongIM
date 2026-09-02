package message

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

func TestAppendOmitsRedundantChannelLookupIndexes(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), []Record{{
		ID: 10, ClientMsgNo: "client-1", FromUID: "u1", Payload: []byte("one"),
	}}, AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}

	assertMessageIndexEntryCount(t, log, messageIndexIDMessageID, 0)
	assertMessageIndexEntryCount(t, log, messageIndexIDClientMsgNo, 0)
	assertMessageIndexEntryCount(t, log, messageIndexIDFromUIDClientMsgNo, 1)

	canonicalKey := encodeMessageIndexPrefix(log.key, messageIndexIDFromUIDClientMsgNo)
	canonicalKey = keycodec.AppendString(canonicalKey, "client-1")
	canonicalKey = keycodec.AppendString(canonicalKey, "u1")
	if _, ok, err := store.engine.Get(canonicalKey); err != nil || !ok {
		t.Fatalf("canonical client/sender index ok=%v err=%v, want present", ok, err)
	}
}

func assertMessageIndexEntryCount(t *testing.T, log *ChannelLog, indexID uint16, want int) {
	t.Helper()
	prefix := encodeMessageIndexPrefix(log.key, indexID)
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := log.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		t.Fatalf("NewIter(index %d): %v", indexID, err)
	}
	defer iter.Close()
	got := 0
	for ok := iter.First(); ok; ok = iter.Next() {
		got++
	}
	if err := iter.Error(); err != nil {
		t.Fatalf("iter(index %d): %v", indexID, err)
	}
	if got != want {
		t.Fatalf("index %d entry count = %d, want %d", indexID, got, want)
	}
}

func TestMessageIndexGetByMessageID(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), []Record{
		{ID: 11, ClientMsgNo: "c-1", FromUID: "u1", Payload: []byte("one")},
		{ID: 12, ClientMsgNo: "c-2", FromUID: "u2", Payload: []byte("two")},
	}, AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}

	msg, ok, err := log.GetByMessageID(context.Background(), 12)
	if err != nil {
		t.Fatalf("GetByMessageID(): %v", err)
	}
	if !ok {
		t.Fatal("GetByMessageID() ok = false, want true")
	}
	if msg.MessageSeq != 2 || msg.MessageID != 12 || msg.ClientMsgNo != "c-2" || string(msg.Payload) != "two" {
		t.Fatalf("message = %#v, want seq=2 id=12 client=c-2 payload=two", msg)
	}

	_, ok, err = log.GetByMessageID(context.Background(), 404)
	if err != nil {
		t.Fatalf("GetByMessageID() missing: %v", err)
	}
	if ok {
		t.Fatal("GetByMessageID() missing ok = true, want false")
	}
}

func TestMessageIndexListsClientMsgNoNewestFirst(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), []Record{
		{ID: 21, ClientMsgNo: "same", FromUID: "u1", Payload: []byte("one")},
		{ID: 22, ClientMsgNo: "other", FromUID: "u2", Payload: []byte("two")},
		{ID: 23, ClientMsgNo: "same", FromUID: "u3", Payload: []byte("three")},
		{ID: 24, ClientMsgNo: "same", FromUID: "u4", Payload: []byte("four")},
	}, AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}

	page, err := log.ListByClientMsgNo(context.Background(), "same", 0, 2)
	if err != nil {
		t.Fatalf("ListByClientMsgNo(): %v", err)
	}
	assertMessageSeqs(t, page.Messages, 4, 3)
	if !page.HasMore || page.NextBeforeSeq != 3 {
		t.Fatalf("page cursor = (hasMore=%v next=%d), want (true, 3)", page.HasMore, page.NextBeforeSeq)
	}

	page, err = log.ListByClientMsgNo(context.Background(), "same", page.NextBeforeSeq, 2)
	if err != nil {
		t.Fatalf("ListByClientMsgNo() next: %v", err)
	}
	assertMessageSeqs(t, page.Messages, 1)
	if page.HasMore || page.NextBeforeSeq != 0 {
		t.Fatalf("next page cursor = (hasMore=%v next=%d), want (false, 0)", page.HasMore, page.NextBeforeSeq)
	}
}

func TestMessageIndexPreservesLegacyClientNumberWithoutSenderIdentity(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	log := testChannelLog(store)
	ctx := context.Background()
	if _, err := log.Append(ctx, []Record{{
		ID: 25, ClientMsgNo: "legacy-client-number", Payload: []byte("legacy"),
	}}, AppendOptions{Mode: AppendStrict}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	page, err := log.ListByClientMsgNo(ctx, "legacy-client-number", 0, 10)
	if err != nil || len(page.Messages) != 1 || page.Messages[0].MessageID != 25 {
		t.Fatalf("ListByClientMsgNo() = (%+v, %v)", page, err)
	}
	if err := log.TruncateFrom(ctx, 1); err != nil {
		t.Fatalf("TruncateFrom(): %v", err)
	}
	page, err = log.ListByClientMsgNo(ctx, "legacy-client-number", 0, 10)
	if err != nil || len(page.Messages) != 0 {
		t.Fatalf("ListByClientMsgNo() after truncate = (%+v, %v)", page, err)
	}
}

func TestMessageIndexFindsLatestSenderSequenceThroughCommittedBoundary(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), []Record{
		{ID: 31, ClientMsgNo: "u1-1", FromUID: "u1", Payload: []byte("one")},
		{ID: 32, ClientMsgNo: "u2-1", FromUID: "u2", Payload: []byte("two")},
		{ID: 33, ClientMsgNo: "u1-2", FromUID: "u1", Payload: []byte("three")},
	}, AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}

	seq, ok, err := log.GetLastSenderMessageSeq(context.Background(), "u1", 2)
	if err != nil {
		t.Fatalf("GetLastSenderMessageSeq(): %v", err)
	}
	if !ok || seq != 1 {
		t.Fatalf("GetLastSenderMessageSeq() = (%d, %v), want (1, true)", seq, ok)
	}

	seq, ok, err = log.GetLastSenderMessageSeq(context.Background(), "u1", 3)
	if err != nil {
		t.Fatalf("GetLastSenderMessageSeq(latest): %v", err)
	}
	if !ok || seq != 3 {
		t.Fatalf("GetLastSenderMessageSeq(latest) = (%d, %v), want (3, true)", seq, ok)
	}

	_, ok, err = log.GetLastSenderMessageSeq(context.Background(), "missing", 3)
	if err != nil {
		t.Fatalf("GetLastSenderMessageSeq(missing): %v", err)
	}
	if ok {
		t.Fatal("GetLastSenderMessageSeq(missing) ok = true, want false")
	}
}

func TestAppendStrictRejectsDuplicateMessageID(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), []Record{{ID: 31, ClientMsgNo: "c-1", FromUID: "u1", Payload: []byte("one")}}, AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	_, err := log.Append(context.Background(), []Record{{ID: 31, ClientMsgNo: "c-2", FromUID: "u2", Payload: []byte("two")}}, AppendOptions{})
	if !errors.Is(err, dberrors.ErrConflict) {
		t.Fatalf("Append() err = %v, want conflict", err)
	}
}

func TestAppendStrictRejectsMessageIDStoredInAnotherChannel(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	left, err := store.db.Channel("left:1", ChannelID{ID: "left", Type: 1})
	if err != nil {
		t.Fatalf("Channel(left): %v", err)
	}
	defer left.Close()
	right, err := store.db.Channel("right:1", ChannelID{ID: "right", Type: 1})
	if err != nil {
		t.Fatalf("Channel(right): %v", err)
	}
	defer right.Close()
	if _, err := left.Append(context.Background(), []Record{{ID: 35, Payload: []byte("one")}}, AppendOptions{}); err != nil {
		t.Fatalf("Append(left): %v", err)
	}
	if _, err := right.Append(context.Background(), []Record{{ID: 35, Payload: []byte("two")}}, AppendOptions{}); !errors.Is(err, dberrors.ErrConflict) {
		t.Fatalf("Append(right) err = %v, want conflict", err)
	}
}

func TestAppendStrictRejectsInBatchDuplicates(t *testing.T) {
	tests := []struct {
		name    string
		records []Record
	}{
		{
			name: "message_id",
			records: []Record{
				{ID: 41, ClientMsgNo: "c-1", FromUID: "u1", Payload: []byte("one")},
				{ID: 41, ClientMsgNo: "c-2", FromUID: "u2", Payload: []byte("two")},
			},
		},
		{
			name: "idempotency",
			records: []Record{
				{ID: 42, ClientMsgNo: "same", FromUID: "u1", Payload: []byte("one")},
				{ID: 43, ClientMsgNo: "same", FromUID: "u1", Payload: []byte("two")},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := openTestMessageStore(t)
			defer store.close(t)

			log := testChannelLog(store)
			_, err := log.Append(context.Background(), tt.records, AppendOptions{})
			if !errors.Is(err, dberrors.ErrConflict) {
				t.Fatalf("Append() err = %v, want conflict", err)
			}
		})
	}
}

func TestAppendTrustedSkipsExistingIndexReads(t *testing.T) {
	tests := []struct {
		name   string
		poison func(t *testing.T, log *ChannelLog)
	}{
		{
			name: "message_id",
			poison: func(t *testing.T, log *ChannelLog) {
				setRawMessageValue(t, log, encodeGlobalMessageIDIndexKey(52), encodeGlobalMessageIDIndexValue(log.key, 1))
			},
		},
		{
			name: "idempotency",
			poison: func(t *testing.T, log *ChannelLog) {
				value := make([]byte, 24)
				binary.BigEndian.PutUint64(value[0:8], 1)
				binary.BigEndian.PutUint64(value[8:16], 51)
				binary.BigEndian.PutUint64(value[16:24], hashPayload([]byte("one")))
				setRawMessageValue(t, log, encodeMessageIdempotencyIndexKey(log.key, "u2", "c-2"), value)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := openTestMessageStore(t)
			defer store.close(t)

			log := testChannelLog(store)
			if _, err := log.Append(context.Background(), []Record{{ID: 51, ClientMsgNo: "c-1", FromUID: "u1", Payload: []byte("one")}}, AppendOptions{}); err != nil {
				t.Fatalf("Append(): %v", err)
			}
			tt.poison(t, log)

			result, err := log.Append(context.Background(), []Record{{ID: 52, ClientMsgNo: "c-2", FromUID: "u2", Payload: []byte("two")}}, AppendOptions{Mode: AppendTrustedContiguous})
			if err != nil {
				t.Fatalf("trusted Append(): %v", err)
			}
			if result.BaseSeq != 2 || result.LastSeq != 2 {
				t.Fatalf("trusted append result = %#v, want seq 2", result)
			}
			msg, ok, err := log.GetByMessageID(context.Background(), 52)
			if err != nil {
				t.Fatalf("GetByMessageID(): %v", err)
			}
			if !ok || msg.MessageSeq != 2 {
				t.Fatalf("GetByMessageID() = (%#v, %v), want seq 2", msg, ok)
			}
		})
	}
}

func setRawMessageValue(t *testing.T, log *ChannelLog, key []byte, value []byte) {
	t.Helper()
	batch := log.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(key, value); err != nil {
		t.Fatalf("batch.Set(): %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("batch.Commit(): %v", err)
	}
}
