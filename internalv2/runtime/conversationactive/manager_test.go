package conversationactive

import "testing"

func TestAdmitActiveBatchUpdatesCacheAndSenderReadSeq(t *testing.T) {
	const activeAtMS int64 = 1781094600000
	m := NewManager(Options{})

	err := m.AdmitActiveBatch(ActiveBatch{
		SenderUID:   "alice",
		ChannelID:   "room-1",
		ChannelType: 2,
		MessageSeq:  42,
		ActiveAtMS:  activeAtMS,
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
	if sender.ActiveAtMS != activeAtMS {
		t.Fatalf("sender ActiveAtMS = %d, want %d", sender.ActiveAtMS, activeAtMS)
	}
	if sender.ReadSeq != 42 {
		t.Fatalf("sender ReadSeq = %d, want 42", sender.ReadSeq)
	}

	receiver, ok := m.EntryForTest("bob", "room-1", 2)
	if !ok {
		t.Fatalf("receiver conversation was not cached")
	}
	if receiver.ActiveAtMS != activeAtMS {
		t.Fatalf("receiver ActiveAtMS = %d, want %d", receiver.ActiveAtMS, activeAtMS)
	}
	if receiver.ReadSeq != 0 {
		t.Fatalf("receiver ReadSeq = %d, want 0", receiver.ReadSeq)
	}
}

func TestAdmitActiveBatchCachesSenderWhenNotRecipient(t *testing.T) {
	const activeAtMS int64 = 1781094600000
	m := NewManager(Options{})

	err := m.AdmitActiveBatch(ActiveBatch{
		SenderUID:   "alice",
		ChannelID:   "room-1",
		ChannelType: 2,
		MessageSeq:  42,
		ActiveAtMS:  activeAtMS,
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
	if sender.ActiveAtMS != activeAtMS {
		t.Fatalf("sender ActiveAtMS = %d, want %d", sender.ActiveAtMS, activeAtMS)
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
	const firstActiveAtMS int64 = 1781094600000
	const secondActiveAtMS int64 = firstActiveAtMS + 5000
	const olderActiveAtMS int64 = firstActiveAtMS - 5000
	m := NewManager(Options{})

	for _, batch := range []ActiveBatch{
		{
			SenderUID:   "alice",
			ChannelID:   "room-1",
			ChannelType: 2,
			MessageSeq:  7,
			ActiveAtMS:  firstActiveAtMS,
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
			ActiveAtMS:  secondActiveAtMS,
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
			ActiveAtMS:  olderActiveAtMS,
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
	if sender.ActiveAtMS != secondActiveAtMS {
		t.Fatalf("sender ActiveAtMS = %d, want %d", sender.ActiveAtMS, secondActiveAtMS)
	}
	if sender.ReadSeq != 11 {
		t.Fatalf("sender ReadSeq = %d, want 11", sender.ReadSeq)
	}

	receiver, ok := m.EntryForTest("bob", "room-1", 2)
	if !ok {
		t.Fatalf("receiver conversation was not cached")
	}
	if receiver.ActiveAtMS != secondActiveAtMS {
		t.Fatalf("receiver ActiveAtMS = %d, want %d", receiver.ActiveAtMS, secondActiveAtMS)
	}
	if receiver.ReadSeq != 0 {
		t.Fatalf("receiver ReadSeq = %d, want 0", receiver.ReadSeq)
	}
}

func TestAdmitActiveBatchUsesNowMSWhenActiveAtMissing(t *testing.T) {
	const nowMS int64 = 1781094600123
	m := NewManager(Options{
		NowMS: func() int64 {
			return nowMS
		},
	})

	err := m.AdmitActiveBatch(ActiveBatch{
		SenderUID:   "alice",
		ChannelID:   "room-1",
		ChannelType: 2,
		MessageSeq:  42,
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
	if sender.ActiveAtMS != nowMS {
		t.Fatalf("sender ActiveAtMS = %d, want %d", sender.ActiveAtMS, nowMS)
	}

	receiver, ok := m.EntryForTest("bob", "room-1", 2)
	if !ok {
		t.Fatalf("receiver conversation was not cached")
	}
	if receiver.ActiveAtMS != nowMS {
		t.Fatalf("receiver ActiveAtMS = %d, want %d", receiver.ActiveAtMS, nowMS)
	}
}
