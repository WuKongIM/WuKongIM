package message

import (
	"context"
	"testing"
)

func TestRetentionTrimDeletesIndexesAndPreservesLEO(t *testing.T) {
	store := openTestMessageStore(t)
	path := store.path

	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), []Record{
		{ID: 201, ClientMsgNo: "c-1", FromUID: "u1", Payload: []byte("one")},
		{ID: 202, ClientMsgNo: "c-2", FromUID: "u2", Payload: []byte("two")},
		{ID: 203, ClientMsgNo: "c-3", FromUID: "u3", Payload: []byte("three")},
	}, AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	result, err := log.TrimPrefixThrough(context.Background(), 2)
	if err != nil {
		t.Fatalf("TrimPrefixThrough(): %v", err)
	}
	if result.DeletedThroughSeq != 2 || result.Deleted != 2 {
		t.Fatalf("trim result = %#v, want through=2 deleted=2", result)
	}
	leo, err := log.LEO(context.Background())
	if err != nil {
		t.Fatalf("LEO(): %v", err)
	}
	if leo != 3 {
		t.Fatalf("LEO = %d, want 3", leo)
	}
	messages, err := log.Read(context.Background(), 1, ReadOptions{})
	if err != nil {
		t.Fatalf("Read(): %v", err)
	}
	assertMessageSeqs(t, messages, 3)
	if _, ok, err := log.GetByMessageID(context.Background(), 201); err != nil || ok {
		t.Fatalf("GetByMessageID(201) = ok %v err %v, want missing", ok, err)
	}
	if _, ok, err := log.LookupIdempotency(context.Background(), IdempotencyKey{FromUID: "u2", ClientMsgNo: "c-2"}); err != nil || ok {
		t.Fatalf("LookupIdempotency(c-2) = ok %v err %v, want missing", ok, err)
	}
	state, ok, err := log.LoadRetentionState(context.Background())
	if err != nil {
		t.Fatalf("LoadRetentionState(): %v", err)
	}
	if !ok || state.PhysicalRetentionThroughSeq != 2 || state.RetainedMaxSeq != 3 {
		t.Fatalf("retention state = (%+v, %v), want physical=2 retained=3", state, ok)
	}

	store.close(t)
	store = openTestMessageStoreAt(t, path)
	defer store.close(t)
	log = testChannelLog(store)
	leo, err = log.LEO(context.Background())
	if err != nil {
		t.Fatalf("LEO() after reopen: %v", err)
	}
	if leo != 3 {
		t.Fatalf("LEO after reopen = %d, want 3", leo)
	}
}
