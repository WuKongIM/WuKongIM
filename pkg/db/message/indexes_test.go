package message

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

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
				value := make([]byte, 8)
				binary.BigEndian.PutUint64(value, 1)
				setRawMessageValue(t, log, encodeMessageIDIndexKey(log.key, 52), value)
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
