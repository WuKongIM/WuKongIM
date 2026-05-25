package message

import (
	"context"
	"testing"
)

func TestChannelLogLEORecoversAfterReopen(t *testing.T) {
	store := openTestMessageStore(t)
	path := store.path

	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), testRecords(100, "one", "two"), AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	store.close(t)

	store = openTestMessageStoreAt(t, path)
	defer store.close(t)
	log = testChannelLog(store)
	leo, err := log.LEO(context.Background())
	if err != nil {
		t.Fatalf("LEO(): %v", err)
	}
	if leo != 2 {
		t.Fatalf("LEO = %d, want 2", leo)
	}
	result, err := log.Append(context.Background(), testRecords(102, "three"), AppendOptions{})
	if err != nil {
		t.Fatalf("Append() after reopen: %v", err)
	}
	if result.BaseSeq != 3 || result.LastSeq != 3 {
		t.Fatalf("append after reopen result = %#v, want seq 3", result)
	}
}

func TestChannelLogReadForwardFromSeq(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), testRecords(200, "one", "two", "three", "four"), AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	messages, err := log.Read(context.Background(), 2, ReadOptions{Limit: 2})
	if err != nil {
		t.Fatalf("Read(): %v", err)
	}
	assertMessageSeqs(t, messages, 2, 3)
	if string(messages[0].Payload) != "two" || string(messages[1].Payload) != "three" {
		t.Fatalf("payloads = %q/%q, want two/three", messages[0].Payload, messages[1].Payload)
	}
}

func TestChannelLogReadForwardHonorsMaxBytes(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), testRecords(300, "aaaa", "bb", "c"), AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	messages, err := log.Read(context.Background(), 1, ReadOptions{MaxBytes: 5})
	if err != nil {
		t.Fatalf("Read() max 5: %v", err)
	}
	assertMessageSeqs(t, messages, 1)

	messages, err = log.Read(context.Background(), 1, ReadOptions{MaxBytes: 3})
	if err != nil {
		t.Fatalf("Read() max 3: %v", err)
	}
	assertMessageSeqs(t, messages, 1)

	messages, err = log.Read(context.Background(), 1, ReadOptions{MaxBytes: 6})
	if err != nil {
		t.Fatalf("Read() max 6: %v", err)
	}
	assertMessageSeqs(t, messages, 1, 2)
}

func TestChannelLogReadReverse(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), testRecords(400, "one", "two", "three", "four", "five"), AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	messages, err := log.ReadReverse(context.Background(), 4, ReadOptions{Limit: 3})
	if err != nil {
		t.Fatalf("ReadReverse(): %v", err)
	}
	assertMessageSeqs(t, messages, 4, 3, 2)
}

func assertMessageSeqs(t *testing.T, messages []Message, seqs ...uint64) {
	t.Helper()
	if len(messages) != len(seqs) {
		t.Fatalf("len(messages) = %d, want %d", len(messages), len(seqs))
	}
	for i, seq := range seqs {
		if messages[i].MessageSeq != seq {
			t.Fatalf("messages[%d].MessageSeq = %d, want %d", i, messages[i].MessageSeq, seq)
		}
	}
}
