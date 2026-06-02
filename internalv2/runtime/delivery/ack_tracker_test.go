package delivery

import (
	"testing"
	"time"
)

func TestAckTrackerAckClearsPending(t *testing.T) {
	now := int64(100)
	tracker := NewAckTracker(AckTrackerOptions{
		ShardCount: 4,
		Now: func() int64 {
			return now
		},
	})

	tracker.Bind(PendingRecvAck{
		UID:         "u1",
		SessionID:   10,
		MessageID:   1001,
		MessageSeq:  20,
		ChannelID:   "c1",
		ChannelType: 1,
	})

	pending, ok := tracker.Ack(Recvack{UID: "u1", SessionID: 10, MessageID: 1001, MessageSeq: 20})
	if !ok {
		t.Fatalf("Ack() ok = false, want true")
	}
	if pending.MessageID != 1001 {
		t.Fatalf("pending.MessageID = %d, want 1001", pending.MessageID)
	}
	if pending.DeliveredAt == 0 {
		t.Fatalf("pending.DeliveredAt = 0, want non-zero")
	}
	if count := tracker.PendingCount(); count != 0 {
		t.Fatalf("PendingCount() = %d, want 0", count)
	}
}

func TestAckTrackerSessionClosedClearsOnlyThatSession(t *testing.T) {
	tracker := NewAckTracker(AckTrackerOptions{ShardCount: 4})
	tracker.Bind(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001, MessageSeq: 1, DeliveredAt: 100})
	tracker.Bind(PendingRecvAck{UID: "u1", SessionID: 11, MessageID: 1002, MessageSeq: 2, DeliveredAt: 100})

	removed := tracker.SessionClosed("u1", 10)
	if len(removed) != 1 || removed[0].MessageID != 1001 {
		t.Fatalf("SessionClosed() removed = %#v, want only message 1001", removed)
	}
	if count := tracker.PendingCount(); count != 1 {
		t.Fatalf("PendingCount() = %d, want 1", count)
	}
	if _, ok := tracker.Ack(Recvack{UID: "u1", SessionID: 10, MessageID: 1001}); ok {
		t.Fatalf("Ack(closed session) ok = true, want false")
	}
	if pending, ok := tracker.Ack(Recvack{UID: "u1", SessionID: 11, MessageID: 1002}); !ok || pending.MessageID != 1002 {
		t.Fatalf("Ack(other session) = %#v, %v, want message 1002 true", pending, ok)
	}
}

func TestAckTrackerExpireRemovesOldPending(t *testing.T) {
	now := int64(200)
	tracker := NewAckTracker(AckTrackerOptions{
		ShardCount: 4,
		Now: func() int64 {
			return now
		},
	})
	tracker.Bind(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001, MessageSeq: 1, DeliveredAt: 100})
	tracker.Bind(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1002, MessageSeq: 2, DeliveredAt: 151})

	removed := tracker.Expire(50 * time.Second)
	if len(removed) != 1 || removed[0].MessageID != 1001 {
		t.Fatalf("Expire() removed = %#v, want only message 1001", removed)
	}
	if count := tracker.PendingCount(); count != 1 {
		t.Fatalf("PendingCount() = %d, want 1", count)
	}
	if _, ok := tracker.Ack(Recvack{UID: "u1", SessionID: 10, MessageID: 1001}); ok {
		t.Fatalf("Ack(expired message) ok = true, want false")
	}
	if pending, ok := tracker.Ack(Recvack{UID: "u1", SessionID: 10, MessageID: 1002}); !ok || pending.MessageID != 1002 {
		t.Fatalf("Ack(fresh message) = %#v, %v, want message 1002 true", pending, ok)
	}
}

func TestAckTrackerExpireSubSecondTTLDoesNotExpireCurrentSecond(t *testing.T) {
	tracker := NewAckTracker(AckTrackerOptions{
		ShardCount: 4,
		Now: func() int64 {
			return 100
		},
	})
	tracker.Bind(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001, MessageSeq: 1, DeliveredAt: 100})

	removed := tracker.Expire(500 * time.Millisecond)
	if len(removed) != 0 {
		t.Fatalf("Expire(sub-second ttl) removed = %#v, want none", removed)
	}
	if count := tracker.PendingCount(); count != 1 {
		t.Fatalf("PendingCount() = %d, want 1", count)
	}
}

func TestAckTrackerMaxPendingPerSessionRejectsNewMessages(t *testing.T) {
	tracker := NewAckTracker(AckTrackerOptions{ShardCount: 4, MaxPendingPerSession: 1})

	if ok := tracker.Bind(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001}); !ok {
		t.Fatalf("first Bind() ok = false, want true")
	}
	if ok := tracker.Bind(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1002}); ok {
		t.Fatalf("second Bind() ok = true, want false at session limit")
	}
	if ok := tracker.Bind(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001, MessageSeq: 2}); !ok {
		t.Fatalf("existing-message Bind() ok = false, want true")
	}
	if count := tracker.PendingCount(); count != 1 {
		t.Fatalf("PendingCount() = %d, want 1", count)
	}
}

func TestAckTrackerInvalidBindIsNoop(t *testing.T) {
	tracker := NewAckTracker(AckTrackerOptions{ShardCount: 4})

	if tracker.Bind(PendingRecvAck{UID: "", SessionID: 10, MessageID: 1001}) {
		t.Fatalf("Bind(empty uid) ok = true, want false")
	}
	if tracker.Bind(PendingRecvAck{UID: "u1", SessionID: 0, MessageID: 1001}) {
		t.Fatalf("Bind(empty session) ok = true, want false")
	}
	if tracker.Bind(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 0}) {
		t.Fatalf("Bind(empty message) ok = true, want false")
	}

	if count := tracker.PendingCount(); count != 0 {
		t.Fatalf("PendingCount() = %d, want 0", count)
	}
}
