package message

import (
	"context"
	"testing"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
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

func TestChannelRetentionTrimPrefixBoundedDeletesRowsAndIndexes(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), []Record{
		{ID: 301, ClientMsgNo: "c-1", FromUID: "u1", Payload: []byte("one")},
		{ID: 302, ClientMsgNo: "c-2", FromUID: "u2", Payload: []byte("two")},
		{ID: 303, ClientMsgNo: "c-3", FromUID: "u3", Payload: []byte("three")},
		{ID: 304, ClientMsgNo: "c-4", FromUID: "u4", Payload: []byte("four")},
	}, AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	if err := log.StoreRetentionState(context.Background(), RetentionState{LocalRetentionThroughSeq: 3, RetainedMaxSeq: 4}); err != nil {
		t.Fatalf("StoreRetentionState(): %v", err)
	}

	result, err := log.TrimPrefixThroughLimit(context.Background(), 3, RetentionTrimOptions{MaxMessages: 2})
	if err != nil {
		t.Fatalf("TrimPrefixThroughLimit(): %v", err)
	}
	if result.DeletedThroughSeq != 2 || result.Deleted != 2 || !result.More {
		t.Fatalf("trim result = %#v, want through=2 deleted=2 more=true", result)
	}
	messages, err := log.Read(context.Background(), 1, ReadOptions{})
	if err != nil {
		t.Fatalf("Read(): %v", err)
	}
	assertMessageSeqs(t, messages, 3, 4)
	if _, ok, err := log.GetByMessageID(context.Background(), 301); err != nil || ok {
		t.Fatalf("GetByMessageID(301) = ok %v err %v, want missing", ok, err)
	}
	if _, ok, err := log.LookupIdempotency(context.Background(), IdempotencyKey{FromUID: "u2", ClientMsgNo: "c-2"}); err != nil || ok {
		t.Fatalf("LookupIdempotency(c-2) = ok %v err %v, want missing", ok, err)
	}
	page, err := log.ListByClientMsgNo(context.Background(), "c-1", 0, 10)
	if err != nil {
		t.Fatalf("ListByClientMsgNo(c-1): %v", err)
	}
	if len(page.Messages) != 0 {
		t.Fatalf("ListByClientMsgNo(c-1) = %+v, want empty", page.Messages)
	}

	result, err = log.TrimPrefixThroughLimit(context.Background(), 3, RetentionTrimOptions{MaxMessages: 2})
	if err != nil {
		t.Fatalf("TrimPrefixThroughLimit() second: %v", err)
	}
	if result.DeletedThroughSeq != 3 || result.Deleted != 1 || result.More {
		t.Fatalf("second trim result = %#v, want through=3 deleted=1 more=false", result)
	}
	state, ok, err := log.LoadRetentionState(context.Background())
	if err != nil {
		t.Fatalf("LoadRetentionState(): %v", err)
	}
	if !ok || state.PhysicalRetentionThroughSeq != 3 || state.LocalRetentionThroughSeq != 3 || state.RetainedMaxSeq != 4 {
		t.Fatalf("retention state = (%+v, %v), want local=3 physical=3 retained=4", state, ok)
	}
}

func TestChannelRetentionTrimPrefixPreservesLEOAfterAllRowsDeleted(t *testing.T) {
	store := openTestMessageStore(t)
	path := store.path

	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), []Record{
		{ID: 401, ClientMsgNo: "c-1", FromUID: "u1", Payload: []byte("one")},
		{ID: 402, ClientMsgNo: "c-2", FromUID: "u2", Payload: []byte("two")},
		{ID: 403, ClientMsgNo: "c-3", FromUID: "u3", Payload: []byte("three")},
	}, AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}

	result, err := log.TrimPrefixThroughLimit(context.Background(), 3, RetentionTrimOptions{MaxMessages: 10})
	if err != nil {
		t.Fatalf("TrimPrefixThroughLimit(): %v", err)
	}
	if result.DeletedThroughSeq != 3 || result.Deleted != 3 || result.More {
		t.Fatalf("trim result = %#v, want through=3 deleted=3 more=false", result)
	}
	messages, err := log.Read(context.Background(), 1, ReadOptions{})
	if err != nil {
		t.Fatalf("Read(): %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("messages = %+v, want empty", messages)
	}
	leo, err := log.LEO(context.Background())
	if err != nil {
		t.Fatalf("LEO(): %v", err)
	}
	if leo != 3 {
		t.Fatalf("LEO = %d, want 3", leo)
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

func TestChannelRetentionTrimPrefixRepeatedCallNoop(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), []Record{
		{ID: 501, ClientMsgNo: "c-1", FromUID: "u1", Payload: []byte("one")},
		{ID: 502, ClientMsgNo: "c-2", FromUID: "u2", Payload: []byte("two")},
	}, AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	if _, err := log.TrimPrefixThroughLimit(context.Background(), 2, RetentionTrimOptions{MaxMessages: 10}); err != nil {
		t.Fatalf("TrimPrefixThroughLimit(): %v", err)
	}

	result, err := log.TrimPrefixThroughLimit(context.Background(), 2, RetentionTrimOptions{MaxMessages: 10})
	if err != nil {
		t.Fatalf("TrimPrefixThroughLimit() repeated: %v", err)
	}
	if result.DeletedThroughSeq != 0 || result.Deleted != 0 || result.More {
		t.Fatalf("repeated trim result = %#v, want zero no-op", result)
	}
	state, ok, err := log.LoadRetentionState(context.Background())
	if err != nil {
		t.Fatalf("LoadRetentionState(): %v", err)
	}
	if !ok || state.PhysicalRetentionThroughSeq != 2 || state.LocalRetentionThroughSeq != 2 || state.RetainedMaxSeq != 2 {
		t.Fatalf("retention state = (%+v, %v), want local=2 physical=2 retained=2", state, ok)
	}
}

func TestChannelStoreTrimMessagesThroughLimit(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer func() {
		if err := engine.Close(); err != nil {
			t.Fatalf("Close(): %v", err)
		}
	}()

	store := mustForChannel(t, engine, channel.ChannelKey("compat-retention:1"), channel.ChannelID{ID: "compat-retention", Type: 1})
	if _, err := store.Append([]channel.Record{compatTestRecord(t, 601, "compat-retention", "c-1")}); err != nil {
		t.Fatalf("Append first: %v", err)
	}
	if _, err := store.Append([]channel.Record{compatTestRecord(t, 602, "compat-retention", "c-2")}); err != nil {
		t.Fatalf("Append second: %v", err)
	}
	if err := store.AdoptRetentionBoundary(context.Background(), 2, "committed"); err != nil {
		t.Fatalf("AdoptRetentionBoundary(): %v", err)
	}

	result, err := store.TrimMessagesThroughLimit(context.Background(), 2, RetentionTrimOptions{MaxMessages: 1})
	if err != nil {
		t.Fatalf("TrimMessagesThroughLimit(): %v", err)
	}
	if result.DeletedThroughSeq != 1 || result.Deleted != 1 || !result.More {
		t.Fatalf("trim result = %#v, want through=1 deleted=1 more=true", result)
	}
	result, err = store.TrimMessagesThroughLimit(context.Background(), 2, RetentionTrimOptions{MaxMessages: 1})
	if err != nil {
		t.Fatalf("TrimMessagesThroughLimit() second: %v", err)
	}
	if result.DeletedThroughSeq != 2 || result.Deleted != 1 || result.More {
		t.Fatalf("second trim result = %#v, want through=2 deleted=1 more=false", result)
	}
}
