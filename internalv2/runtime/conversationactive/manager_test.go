package conversationactive

import (
	"testing"
	"time"
)

func TestAdmitActiveBatchUpdatesCacheAndSenderReadSeq(t *testing.T) {
	activeAt := time.Date(2026, 6, 10, 15, 30, 0, 0, time.UTC)
	m := NewManager(Options{})

	err := m.AdmitActiveBatch(ActiveBatch{
		SenderUID:   "alice",
		ChannelID:   "room-1",
		ChannelType: 2,
		MessageSeq:  42,
		ActiveAt:    activeAt,
		Recipients: []ActiveEntry{
			{UID: "alice", IsSender: true},
			{UID: "bob"},
		},
	})
	if err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}

	sender, ok := m.EntryForTest("alice", "room-1", 2)
	if !ok {
		t.Fatalf("sender conversation was not cached")
	}
	if !sender.ActiveAt.Equal(activeAt) {
		t.Fatalf("sender ActiveAt = %v, want %v", sender.ActiveAt, activeAt)
	}
	if sender.ReadSeq != 42 {
		t.Fatalf("sender ReadSeq = %d, want 42", sender.ReadSeq)
	}

	receiver, ok := m.EntryForTest("bob", "room-1", 2)
	if !ok {
		t.Fatalf("receiver conversation was not cached")
	}
	if !receiver.ActiveAt.Equal(activeAt) {
		t.Fatalf("receiver ActiveAt = %v, want %v", receiver.ActiveAt, activeAt)
	}
	if receiver.ReadSeq != 0 {
		t.Fatalf("receiver ReadSeq = %d, want 0", receiver.ReadSeq)
	}
}

func TestAdmitActiveBatchCachesSenderWhenNotRecipient(t *testing.T) {
	activeAt := time.Date(2026, 6, 10, 15, 30, 0, 0, time.UTC)
	m := NewManager(Options{})

	err := m.AdmitActiveBatch(ActiveBatch{
		SenderUID:   "alice",
		ChannelID:   "room-1",
		ChannelType: 2,
		MessageSeq:  42,
		ActiveAt:    activeAt,
		Recipients: []ActiveEntry{
			{UID: "bob"},
		},
	})
	if err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}

	sender, ok := m.EntryForTest("alice", "room-1", 2)
	if !ok {
		t.Fatalf("sender conversation was not cached")
	}
	if !sender.ActiveAt.Equal(activeAt) {
		t.Fatalf("sender ActiveAt = %v, want %v", sender.ActiveAt, activeAt)
	}
	if sender.ReadSeq != 42 {
		t.Fatalf("sender ReadSeq = %d, want 42", sender.ReadSeq)
	}

	receiver, ok := m.EntryForTest("bob", "room-1", 2)
	if !ok {
		t.Fatalf("receiver conversation was not cached")
	}
	if receiver.ReadSeq != 0 {
		t.Fatalf("receiver ReadSeq = %d, want 0", receiver.ReadSeq)
	}
}

func TestAdmitActiveBatchPreservesReceiverReadSeq(t *testing.T) {
	firstActiveAt := time.Date(2026, 6, 10, 15, 30, 0, 0, time.UTC)
	secondActiveAt := firstActiveAt.Add(5 * time.Second)
	olderActiveAt := firstActiveAt.Add(-5 * time.Second)
	m := NewManager(Options{})

	for _, batch := range []ActiveBatch{
		{
			SenderUID:   "alice",
			ChannelID:   "room-1",
			ChannelType: 2,
			MessageSeq:  7,
			ActiveAt:    firstActiveAt,
			Recipients: []ActiveEntry{
				{UID: "alice", IsSender: true},
				{UID: "bob"},
			},
		},
		{
			SenderUID:   "alice",
			ChannelID:   "room-1",
			ChannelType: 2,
			MessageSeq:  11,
			ActiveAt:    secondActiveAt,
			Recipients: []ActiveEntry{
				{UID: "alice", IsSender: true},
				{UID: "bob"},
			},
		},
		{
			SenderUID:   "alice",
			ChannelID:   "room-1",
			ChannelType: 2,
			MessageSeq:  9,
			ActiveAt:    olderActiveAt,
			Recipients: []ActiveEntry{
				{UID: "alice", IsSender: true},
				{UID: "bob"},
			},
		},
	} {
		if err := m.AdmitActiveBatch(batch); err != nil {
			t.Fatalf("AdmitActiveBatch() error = %v", err)
		}
	}

	sender, ok := m.EntryForTest("alice", "room-1", 2)
	if !ok {
		t.Fatalf("sender conversation was not cached")
	}
	if !sender.ActiveAt.Equal(secondActiveAt) {
		t.Fatalf("sender ActiveAt = %v, want %v", sender.ActiveAt, secondActiveAt)
	}
	if sender.ReadSeq != 11 {
		t.Fatalf("sender ReadSeq = %d, want 11", sender.ReadSeq)
	}

	receiver, ok := m.EntryForTest("bob", "room-1", 2)
	if !ok {
		t.Fatalf("receiver conversation was not cached")
	}
	if !receiver.ActiveAt.Equal(secondActiveAt) {
		t.Fatalf("receiver ActiveAt = %v, want %v", receiver.ActiveAt, secondActiveAt)
	}
	if receiver.ReadSeq != 0 {
		t.Fatalf("receiver ReadSeq = %d, want 0", receiver.ReadSeq)
	}
}
