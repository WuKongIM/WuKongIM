package message

import (
	"context"
	"testing"
)

func TestTruncateDeletesRowsAndIndexes(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), []Record{
		{ID: 101, ClientMsgNo: "c-1", FromUID: "u1", Payload: []byte("one")},
		{ID: 102, ClientMsgNo: "c-2", FromUID: "u2", Payload: []byte("two")},
		{ID: 103, ClientMsgNo: "c-3", FromUID: "u3", Payload: []byte("three")},
	}, AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}

	if err := log.TruncateFrom(context.Background(), 2); err != nil {
		t.Fatalf("TruncateFrom(): %v", err)
	}
	leo, err := log.LEO(context.Background())
	if err != nil {
		t.Fatalf("LEO(): %v", err)
	}
	if leo != 1 {
		t.Fatalf("LEO = %d, want 1", leo)
	}
	messages, err := log.Read(context.Background(), 1, ReadOptions{})
	if err != nil {
		t.Fatalf("Read(): %v", err)
	}
	assertMessageSeqs(t, messages, 1)
	if _, ok, err := log.GetByMessageID(context.Background(), 102); err != nil || ok {
		t.Fatalf("GetByMessageID(102) = ok %v err %v, want missing", ok, err)
	}
	if _, ok, err := log.LookupIdempotency(context.Background(), IdempotencyKey{FromUID: "u2", ClientMsgNo: "c-2"}); err != nil || ok {
		t.Fatalf("LookupIdempotency(c-2) = ok %v err %v, want missing", ok, err)
	}
	page, err := log.ListByClientMsgNo(context.Background(), "c-3", 0, 10)
	if err != nil {
		t.Fatalf("ListByClientMsgNo(): %v", err)
	}
	if len(page.Messages) != 0 {
		t.Fatalf("client c-3 messages = %+v, want empty", page.Messages)
	}
}
