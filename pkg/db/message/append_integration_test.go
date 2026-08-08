//go:build integration

package message

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestChannelLogAppendEmptyDoesNotAdvanceLEO(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	result, err := log.Append(context.Background(), nil, AppendOptions{})
	if err != nil {
		t.Fatalf("Append(): %v", err)
	}
	if result.Count != 0 || result.BaseSeq != 0 || result.LastSeq != 0 {
		t.Fatalf("result = %#v, want empty append result", result)
	}
	leo, err := log.LEO(context.Background())
	if err != nil {
		t.Fatalf("LEO(): %v", err)
	}
	if leo != 0 {
		t.Fatalf("LEO = %d, want 0", leo)
	}
}

func TestChannelLogAppendAssignsContiguousSeq(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	result, err := log.Append(context.Background(), testRecords(10, "one", "two"), AppendOptions{})
	if err != nil {
		t.Fatalf("Append(): %v", err)
	}
	if result.BaseSeq != 1 || result.LastSeq != 2 || result.Count != 2 {
		t.Fatalf("first append result = %#v, want base=1 last=2 count=2", result)
	}
	result, err = log.Append(context.Background(), testRecords(12, "three"), AppendOptions{})
	if err != nil {
		t.Fatalf("Append() second: %v", err)
	}
	if result.BaseSeq != 3 || result.LastSeq != 3 || result.Count != 1 {
		t.Fatalf("second append result = %#v, want base=3 last=3 count=1", result)
	}
	leo, err := log.LEO(context.Background())
	if err != nil {
		t.Fatalf("LEO(): %v", err)
	}
	if leo != 3 {
		t.Fatalf("LEO = %d, want 3", leo)
	}
}

func TestChannelLogAppendStoresOnePrimaryRow(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), testRecords(21, "one-row"), AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	if _, present, err := store.engine.Get(encodeMessageRowKey(log.key, 1, messageHeaderFamilyID)); err != nil {
		t.Fatalf("Get(primary row): %v", err)
	} else if !present {
		t.Fatal("primary row is missing")
	}
	if _, present, err := store.engine.Get(encodeMessageRowKey(log.key, 1, messagePayloadFamilyID)); err != nil {
		t.Fatalf("Get(legacy payload row): %v", err)
	} else if present {
		t.Fatal("append stored a separate payload row")
	}
	message, present, err := log.GetBySeq(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetBySeq(): %v", err)
	}
	if !present || string(message.Payload) != "one-row" {
		t.Fatalf("GetBySeq() = (%+v, %v), want materialized payload", message, present)
	}
}

func TestChannelLogRecordToRowKeepsPayloadReferenceForAppendStaging(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	payload := []byte("payload")
	row := log.recordToRow(1, Record{ID: 1, Payload: payload}, 123)
	if len(row.Payload) == 0 || &row.Payload[0] != &payload[0] {
		t.Fatal("recordToRow copied payload, want append staging to copy directly into the storage batch")
	}
}

func TestChannelLogPrepareAndStageAppendLeavesNoRowsWhenValidationFails(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	batch := log.db.engine.NewBatch()
	defer batch.Close()

	log.appendMu.Lock()
	_, err := log.prepareAndStageAppendLocked(context.Background(), batch, []Record{
		{ID: 81, ClientMsgNo: "c-1", FromUID: "u1", Payload: []byte("one")},
		{ID: 81, ClientMsgNo: "c-2", FromUID: "u2", Payload: []byte("two")},
	}, AppendOptions{})
	log.appendMu.Unlock()

	if !errors.Is(err, dberrors.ErrConflict) {
		t.Fatalf("prepareAndStageAppendLocked() err = %v, want conflict", err)
	}
	messages, err := log.Read(context.Background(), 1, ReadOptions{Limit: 2})
	if err != nil {
		t.Fatalf("Read(): %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("staged failed append left %d durable messages, want 0", len(messages))
	}
	leo, err := log.LEO(context.Background())
	if err != nil {
		t.Fatalf("LEO(): %v", err)
	}
	if leo != 0 {
		t.Fatalf("LEO = %d, want 0", leo)
	}
}

func TestChannelLogAppendSerializesSameChannel(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	const writers = 32
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			log, err := store.db.Channel(ChannelKey("serialized"), ChannelID{ID: "serialized", Type: 1})
			if err != nil {
				errs <- err
				return
			}
			defer log.Close()
			_, err = log.Append(context.Background(), []Record{{
				ID:      uint64(i + 1),
				Payload: []byte{byte(i)},
			}}, AppendOptions{})
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("Append(): %v", err)
	}

	log := mustAcquireChannel(t, store.db, ChannelKey("serialized"), ChannelID{ID: "serialized", Type: 1})
	defer log.Close()
	leo, err := log.LEO(context.Background())
	if err != nil {
		t.Fatalf("LEO(): %v", err)
	}
	if leo != writers {
		t.Fatalf("LEO = %d, want %d", leo, writers)
	}
	messages, err := log.Read(context.Background(), 1, ReadOptions{Limit: writers + 1})
	if err != nil {
		t.Fatalf("Read(): %v", err)
	}
	if len(messages) != writers {
		t.Fatalf("len(messages) = %d, want %d", len(messages), writers)
	}
	seenIDs := make(map[uint64]struct{}, writers)
	for i, msg := range messages {
		if msg.MessageSeq != uint64(i+1) {
			t.Fatalf("messages[%d].MessageSeq = %d, want %d", i, msg.MessageSeq, i+1)
		}
		seenIDs[msg.MessageID] = struct{}{}
	}
	if len(seenIDs) != writers {
		t.Fatalf("unique message IDs = %d, want %d", len(seenIDs), writers)
	}
}
