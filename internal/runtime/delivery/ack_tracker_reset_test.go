package delivery

import "testing"

func TestAckTrackerResetClearsPendingState(t *testing.T) {
	tracker := NewAckTracker(AckTrackerOptions{
		ShardCount:           2,
		MaxPendingPerSession: 4,
	})
	if !tracker.Bind(PendingRecvAck{
		UID:        "u1",
		SessionID:  11,
		MessageID:  22,
		MessageSeq: 33,
	}) {
		t.Fatal("bind = false, want true")
	}

	tracker.Reset()

	if got := tracker.PendingCount(); got != 0 {
		t.Fatalf("pending count = %d, want 0", got)
	}
	if _, ok := tracker.Ack(Recvack{
		UID:       "u1",
		SessionID: 11,
		MessageID: 22,
	}); ok {
		t.Fatal("ack unexpectedly matched reset state")
	}
}

func TestAckTrackerResetIsNilSafe(t *testing.T) {
	var tracker *AckTracker
	tracker.Reset()
}
