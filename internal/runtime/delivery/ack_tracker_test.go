package delivery

import (
	"fmt"
	"sync"
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

func TestAckTrackerBindResultReportsAddedAndRejected(t *testing.T) {
	tracker := NewAckTracker(AckTrackerOptions{ShardCount: 4, MaxPendingPerSession: 1})

	first := tracker.BindResult(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001})
	if !first.Bound || !first.Added || first.PendingCount != 1 {
		t.Fatalf("first BindResult() = %#v, want bound added count 1", first)
	}
	if !tracker.FinishBind(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001}, first.Token) {
		t.Fatal("FinishBind(first) = false, want committed")
	}

	overwrite := tracker.BindResult(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001, MessageSeq: 2})
	if !overwrite.Bound || overwrite.Added || overwrite.PendingCount != 1 {
		t.Fatalf("overwrite BindResult() = %#v, want bound not-added count 1", overwrite)
	}
	if !tracker.FinishBind(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001}, overwrite.Token) {
		t.Fatal("FinishBind(overwrite) = false, want committed refresh")
	}

	rejected := tracker.BindResult(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1002})
	if rejected.Bound || rejected.Added || rejected.PendingCount != 1 {
		t.Fatalf("rejected BindResult() = %#v, want rejected count 1", rejected)
	}
}

func TestAckTrackerBindTokensIsolateConcurrentDeliveryRollbacks(t *testing.T) {
	pending := PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001}

	t.Run("A fails B succeeds", func(t *testing.T) {
		tracker := NewAckTracker(AckTrackerOptions{ShardCount: 1})
		a := tracker.BindResult(pending)
		b := tracker.BindResult(pending)
		assertDistinctAckBindTokens(t, a, b)
		if canceled := tracker.CancelBind(pending, a.Token); !canceled.Canceled || canceled.Removed || canceled.PendingCount != 1 {
			t.Fatalf("CancelBind(A) = %#v, want token canceled with B retained", canceled)
		}
		if !tracker.FinishBind(pending, b.Token) {
			t.Fatal("FinishBind(B) = false, want B committed")
		}
		if _, ok := tracker.Ack(Recvack{UID: "u1", SessionID: 10, MessageID: 1001}); !ok {
			t.Fatal("Ack() ok = false, want B success retained")
		}
	})

	t.Run("A succeeds B fails", func(t *testing.T) {
		tracker := NewAckTracker(AckTrackerOptions{ShardCount: 1})
		a := tracker.BindResult(pending)
		b := tracker.BindResult(pending)
		assertDistinctAckBindTokens(t, a, b)
		if !tracker.FinishBind(pending, a.Token) {
			t.Fatal("FinishBind(A) = false, want A committed")
		}
		if canceled := tracker.CancelBind(pending, b.Token); !canceled.Canceled || canceled.Removed || canceled.PendingCount != 1 {
			t.Fatalf("CancelBind(B) = %#v, want token canceled with A retained", canceled)
		}
		if _, ok := tracker.Ack(Recvack{UID: "u1", SessionID: 10, MessageID: 1001}); !ok {
			t.Fatal("Ack() ok = false, want A success retained")
		}
	})

	t.Run("both fail", func(t *testing.T) {
		tracker := NewAckTracker(AckTrackerOptions{ShardCount: 1})
		a := tracker.BindResult(pending)
		b := tracker.BindResult(pending)
		if canceled := tracker.CancelBind(pending, a.Token); !canceled.Canceled || canceled.Removed {
			t.Fatalf("CancelBind(A) = %#v, want B reservation retained", canceled)
		}
		if canceled := tracker.CancelBind(pending, b.Token); !canceled.Canceled || !canceled.Removed || canceled.PendingCount != 0 {
			t.Fatalf("CancelBind(B) = %#v, want last reservation removed", canceled)
		}
		if _, ok := tracker.Ack(Recvack{UID: "u1", SessionID: 10, MessageID: 1001}); ok {
			t.Fatal("Ack() ok = true, want both failed reservations absent")
		}
	})

	t.Run("both succeed then client ack", func(t *testing.T) {
		tracker := NewAckTracker(AckTrackerOptions{ShardCount: 1})
		a := tracker.BindResult(pending)
		b := tracker.BindResult(pending)
		assertDistinctAckBindTokens(t, a, b)
		if !tracker.FinishBind(pending, a.Token) || !tracker.FinishBind(pending, b.Token) {
			t.Fatal("FinishBind(A/B) = false, want both successes committed")
		}
		if got := tracker.PendingCount(); got != 1 {
			t.Fatalf("PendingCount() = %d, want one identity with two reservations", got)
		}
		if _, ok := tracker.Ack(Recvack{UID: "u1", SessionID: 10, MessageID: 1001}); !ok {
			t.Fatal("Ack() ok = false, want identity-based client ack")
		}
		if got := tracker.PendingCount(); got != 0 {
			t.Fatalf("PendingCount() after Ack = %d, want zero", got)
		}
	})
}

func TestAckTrackerStaleBindTokenCannotRollbackRecreatedIdentity(t *testing.T) {
	tracker := NewAckTracker(AckTrackerOptions{ShardCount: 1})
	pending := PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001}
	stale := tracker.BindResult(pending)
	if _, ok := tracker.Ack(Recvack{UID: "u1", SessionID: 10, MessageID: 1001}); !ok {
		t.Fatal("Ack() ok = false, want first identity removed")
	}
	current := tracker.BindResult(pending)
	assertDistinctAckBindTokens(t, stale, current)
	if canceled := tracker.CancelBind(pending, stale.Token); canceled.Canceled || canceled.Removed || canceled.PendingCount != 1 {
		t.Fatalf("CancelBind(stale) = %#v, want recreated identity preserved", canceled)
	}
	if !tracker.FinishBind(pending, current.Token) {
		t.Fatal("FinishBind(current) = false, want recreated identity committed")
	}
	if _, ok := tracker.Ack(Recvack{UID: "u1", SessionID: 10, MessageID: 1001}); !ok {
		t.Fatal("Ack() ok = false, want recreated identity retained")
	}
}

func TestAckTrackerCommittedSuccessSurvivesFailedRefresh(t *testing.T) {
	tracker := NewAckTracker(AckTrackerOptions{ShardCount: 1})
	pending := PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001, MessageSeq: 1, ChannelID: "old", DeliveredAt: 100}
	if !tracker.Bind(pending) {
		t.Fatal("Bind() = false, want compatibility bind committed")
	}
	refreshPending := pending
	refreshPending.MessageSeq = 2
	refreshPending.ChannelID = "failed-refresh"
	refreshPending.DeliveredAt = 200
	refresh := tracker.BindResult(refreshPending)
	if !refresh.Bound || refresh.Added {
		t.Fatalf("BindResult(refresh) = %#v, want in-flight refresh", refresh)
	}
	if canceled := tracker.CancelBind(pending, refresh.Token); !canceled.Canceled || canceled.Removed || canceled.PendingCount != 1 {
		t.Fatalf("CancelBind(refresh) = %#v, want committed identity preserved", canceled)
	}
	got, ok := tracker.Ack(Recvack{UID: "u1", SessionID: 10, MessageID: 1001})
	if !ok {
		t.Fatal("Ack() ok = false, want compatibility success retained")
	}
	if got.DeliveredAt != 100 || got.MessageSeq != 1 || got.ChannelID != "old" {
		t.Fatalf("Ack() pending = %#v, want original committed metadata after failed refresh", got)
	}
}

func TestAckTrackerFailedRefreshDoesNotDelayCommittedExpiry(t *testing.T) {
	tracker := NewAckTracker(AckTrackerOptions{ShardCount: 1, Now: func() int64 { return 200 }})
	committed := PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001, DeliveredAt: 100}
	if !tracker.Bind(committed) {
		t.Fatal("Bind() = false, want committed identity")
	}
	refresh := tracker.BindResult(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001, DeliveredAt: 190})
	if canceled := tracker.CancelBind(committed, refresh.Token); !canceled.Canceled || canceled.Removed {
		t.Fatalf("CancelBind(refresh) = %#v, want committed identity retained", canceled)
	}
	removed := tracker.Expire(50 * time.Second)
	if len(removed) != 1 || removed[0].DeliveredAt != 100 {
		t.Fatalf("Expire() = %#v, want original committed delivery at 100", removed)
	}
}

func TestAckTrackerBatchFailedRefreshPreservesCommittedMetadata(t *testing.T) {
	tracker := NewAckTracker(AckTrackerOptions{ShardCount: 2})
	committed := []PendingRecvAck{
		{UID: "u1", SessionID: 10, MessageID: 1001, MessageSeq: 1, DeliveredAt: 100},
		{UID: "u2", SessionID: 11, MessageID: 1001, MessageSeq: 1, DeliveredAt: 100},
	}
	initial := tracker.BindBatch(committed)
	if finished := tracker.FinishBindBatch(committed, initial.Tokens, []int{0, 1}); finished != 2 {
		t.Fatalf("FinishBindBatch(initial) = %d, want 2", finished)
	}
	refresh := []PendingRecvAck{
		{UID: "u1", SessionID: 10, MessageID: 1001, MessageSeq: 2, DeliveredAt: 200},
		{UID: "u2", SessionID: 11, MessageID: 1001, MessageSeq: 2, DeliveredAt: 200},
	}
	rebound := tracker.BindBatch(refresh)
	for i := range refresh {
		if canceled := tracker.CancelBind(refresh[i], rebound.Tokens[i]); !canceled.Canceled || canceled.Removed {
			t.Fatalf("CancelBind(refresh %d) = %#v, want committed metadata retained", i, canceled)
		}
		got, ok := tracker.Ack(Recvack{UID: refresh[i].UID, SessionID: refresh[i].SessionID, MessageID: refresh[i].MessageID})
		if !ok || got.MessageSeq != 1 || got.DeliveredAt != 100 {
			t.Fatalf("Ack(refresh %d) = %#v, %v, want original batch metadata", i, got, ok)
		}
	}
}

func TestAckTrackerSuccessfulRefreshMetadataFollowsExtraFinishOrder(t *testing.T) {
	for _, test := range []struct {
		name       string
		finish     []int
		wantSeq    uint64
		wantAtUnix int64
	}{
		{name: "newest finishes last", finish: []int{0, 1}, wantSeq: 3, wantAtUnix: 300},
		{name: "older finishes last", finish: []int{1, 0}, wantSeq: 2, wantAtUnix: 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			tracker := NewAckTracker(AckTrackerOptions{ShardCount: 1})
			base := PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001, MessageSeq: 1, DeliveredAt: 100}
			if !tracker.Bind(base) {
				t.Fatal("Bind(base) = false")
			}
			attempts := []PendingRecvAck{
				{UID: "u1", SessionID: 10, MessageID: 1001, MessageSeq: 2, DeliveredAt: 200},
				{UID: "u1", SessionID: 10, MessageID: 1001, MessageSeq: 3, DeliveredAt: 300},
			}
			tokens := []AckBindToken{
				tracker.BindResult(attempts[0]).Token,
				tracker.BindResult(attempts[1]).Token,
			}
			for _, index := range test.finish {
				if !tracker.FinishBind(attempts[index], tokens[index]) {
					t.Fatalf("FinishBind(%d) = false", index)
				}
			}
			got, ok := tracker.Ack(Recvack{UID: "u1", SessionID: 10, MessageID: 1001})
			if !ok || got.MessageSeq != test.wantSeq || got.DeliveredAt != test.wantAtUnix {
				t.Fatalf("Ack() = %#v, %v, want seq=%d deliveredAt=%d", got, ok, test.wantSeq, test.wantAtUnix)
			}
		})
	}
}

func TestAckTrackerFreshConcurrentMetadataFollowsPrimaryOutcome(t *testing.T) {
	primaryPending := PendingRecvAck{
		UID: "u1", SessionID: 10, MessageID: 1001, MessageSeq: 1, ChannelID: "primary", DeliveredAt: 100,
	}
	extraPending := PendingRecvAck{
		UID: "u1", SessionID: 10, MessageID: 1001, MessageSeq: 2, ChannelID: "extra", DeliveredAt: 200,
	}

	t.Run("primary finishes after extra", func(t *testing.T) {
		tracker := NewAckTracker(AckTrackerOptions{ShardCount: 1})
		primary := tracker.BindResult(primaryPending)
		extra := tracker.BindResult(extraPending)
		assertDistinctAckBindTokens(t, primary, extra)
		if !tracker.FinishBind(extraPending, extra.Token) {
			t.Fatal("FinishBind(extra) = false")
		}
		if !tracker.FinishBind(primaryPending, primary.Token) {
			t.Fatal("FinishBind(primary) = false")
		}
		got, ok := tracker.Ack(Recvack{UID: "u1", SessionID: 10, MessageID: 1001})
		if !ok || got.MessageSeq != primaryPending.MessageSeq || got.ChannelID != primaryPending.ChannelID || got.DeliveredAt != primaryPending.DeliveredAt {
			t.Fatalf("Ack() = %#v, %v, want last successful primary metadata %#v", got, ok, primaryPending)
		}
	})

	t.Run("primary fails after extra succeeds", func(t *testing.T) {
		tracker := NewAckTracker(AckTrackerOptions{ShardCount: 1})
		primary := tracker.BindResult(primaryPending)
		extra := tracker.BindResult(extraPending)
		assertDistinctAckBindTokens(t, primary, extra)
		if !tracker.FinishBind(extraPending, extra.Token) {
			t.Fatal("FinishBind(extra) = false")
		}
		if canceled := tracker.CancelBind(primaryPending, primary.Token); !canceled.Canceled || canceled.Removed {
			t.Fatalf("CancelBind(primary) = %#v, want committed extra preserved", canceled)
		}
		got, ok := tracker.Ack(Recvack{UID: "u1", SessionID: 10, MessageID: 1001})
		if !ok || got.MessageSeq != extraPending.MessageSeq || got.ChannelID != extraPending.ChannelID || got.DeliveredAt != extraPending.DeliveredAt {
			t.Fatalf("Ack() = %#v, %v, want successful extra metadata %#v", got, ok, extraPending)
		}
	})
}

func TestAckTrackerStaleFinishAndCancelAreNoopsAfterIdentityCleanup(t *testing.T) {
	for _, test := range []struct {
		name    string
		tracker func() *AckTracker
		cleanup func(*AckTracker, PendingRecvAck)
	}{
		{
			name:    "ack",
			tracker: func() *AckTracker { return NewAckTracker(AckTrackerOptions{ShardCount: 1}) },
			cleanup: func(tracker *AckTracker, pending PendingRecvAck) {
				_, _ = tracker.Ack(Recvack{UID: pending.UID, SessionID: pending.SessionID, MessageID: pending.MessageID})
			},
		},
		{
			name:    "session close",
			tracker: func() *AckTracker { return NewAckTracker(AckTrackerOptions{ShardCount: 1}) },
			cleanup: func(tracker *AckTracker, pending PendingRecvAck) {
				_ = tracker.SessionClosed(pending.UID, pending.SessionID)
			},
		},
		{
			name: "expiry",
			tracker: func() *AckTracker {
				return NewAckTracker(AckTrackerOptions{ShardCount: 1, Now: func() int64 { return 200 }})
			},
			cleanup: func(tracker *AckTracker, _ PendingRecvAck) {
				_ = tracker.Expire(50 * time.Second)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			tracker := test.tracker()
			pending := PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001, DeliveredAt: 100}
			bind := tracker.BindResult(pending)
			test.cleanup(tracker, pending)
			if tracker.FinishBind(pending, bind.Token) {
				t.Fatal("FinishBind(stale) = true, want cleanup to win")
			}
			if canceled := tracker.CancelBind(pending, bind.Token); canceled.Canceled || canceled.Removed || canceled.PendingCount != 0 {
				t.Fatalf("CancelBind(stale) = %#v, want cleanup to win", canceled)
			}
		})
	}
}

func TestAckTrackerFinishBindBatchSkipsMixedOutOfRangeAndMismatchedInputs(t *testing.T) {
	tracker := NewAckTracker(AckTrackerOptions{ShardCount: 4})
	pending := []PendingRecvAck{
		{UID: "u0", SessionID: 10, MessageID: 1001},
		{UID: "u1", SessionID: 11, MessageID: 1001},
		{UID: "u2", SessionID: 12, MessageID: 1001},
		{UID: "u3", SessionID: 13, MessageID: 1001},
	}
	bound := tracker.BindBatch(pending)
	if finished := tracker.FinishBindBatch(pending[:3], bound.Tokens[:2], []int{-1, 0, 1, 2, 3, 99}); finished != 2 {
		t.Fatalf("FinishBindBatch(mixed) = %d, want only aligned indexes 0 and 1", finished)
	}
	if finished := tracker.FinishBindBatch(pending, bound.Tokens, []int{2, 3}); finished != 2 {
		t.Fatalf("FinishBindBatch(remaining) = %d, want 2", finished)
	}
	for _, item := range pending {
		if _, ok := tracker.Ack(Recvack{UID: item.UID, SessionID: item.SessionID, MessageID: item.MessageID}); !ok {
			t.Fatalf("Ack(%s) ok = false, want all valid finishes committed", item.UID)
		}
	}
}

func TestAckTrackerRepeatedSuccessfulRefreshReleasesAllInflightTokenStorage(t *testing.T) {
	tracker := NewAckTracker(AckTrackerOptions{ShardCount: 1})
	pending := PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001}
	first := tracker.BindResult(pending)
	second := tracker.BindResult(pending)
	if !tracker.FinishBind(pending, first.Token) || !tracker.FinishBind(pending, second.Token) {
		t.Fatal("FinishBind(initial concurrent tokens) = false")
	}
	for i := 0; i < 10_000; i++ {
		refresh := tracker.BindResult(pending)
		if !refresh.Bound || !tracker.FinishBind(pending, refresh.Token) {
			t.Fatalf("successful refresh %d failed: %#v", i, refresh)
		}
	}

	shard := tracker.shard(pending.SessionID)
	shard.mu.Lock()
	entry := shard.byMessage[ackMessageKey{uid: pending.UID, sessionID: pending.SessionID, messageID: pending.MessageID}]
	shard.mu.Unlock()
	if !entry.committed || entry.primary.Valid() || len(entry.extraAttempts) != 0 || cap(entry.extraAttempts) != 0 {
		t.Fatalf("entry after successful refreshes = %#v, want committed with zero retained in-flight storage", entry)
	}
}

func assertDistinctAckBindTokens(t *testing.T, first, second AckBindResult) {
	t.Helper()
	if !first.Bound || !second.Bound || first.Token.id == 0 || second.Token.id == 0 || first.Token == second.Token {
		t.Fatalf("bind results = %#v, %#v, want distinct non-zero tokens", first, second)
	}
}

func TestAckTrackerBindBatchPreservesAlignmentDuplicatesAndSessionLimits(t *testing.T) {
	tracker := NewAckTracker(AckTrackerOptions{
		ShardCount:           4,
		MaxPendingPerSession: 1,
		Now: func() int64 {
			return 100
		},
	})

	result := tracker.BindBatch([]PendingRecvAck{
		{UID: "", SessionID: 10, MessageID: 1001},
		{UID: "u1", SessionID: 10, MessageID: 1001, MessageSeq: 1},
		{UID: "u1", SessionID: 10, MessageID: 1001, MessageSeq: 2},
		{UID: "u1", SessionID: 10, MessageID: 1002, MessageSeq: 3},
		{UID: "u2", SessionID: 11, MessageID: 2001, MessageSeq: 4},
	})
	wantBound := []bool{false, true, true, false, true}
	if len(result.Tokens) != len(wantBound) {
		t.Fatalf("BindBatch() result count = %d, want %d", len(result.Tokens), len(wantBound))
	}
	for i := range wantBound {
		if result.Tokens[i].Valid() != wantBound[i] {
			t.Fatalf("BindBatch() Tokens[%d] = %#v, want valid=%v; result = %#v", i, result.Tokens[i], wantBound[i], result)
		}
	}
	if result.Tokens[1] == result.Tokens[2] || result.Tokens[1] == result.Tokens[4] || result.Tokens[2] == result.Tokens[4] {
		t.Fatalf("BindBatch() tokens = %#v, want one distinct token per successful item", result.Tokens)
	}
	if result.Added != 2 || result.PendingCount != 2 {
		t.Fatalf("BindBatch() = %#v, want added 2 pending 2", result)
	}
	if finished := tracker.FinishBindBatch(
		[]PendingRecvAck{
			{UID: "", SessionID: 10, MessageID: 1001},
			{UID: "u1", SessionID: 10, MessageID: 1001, MessageSeq: 1},
			{UID: "u1", SessionID: 10, MessageID: 1001, MessageSeq: 2},
			{UID: "u1", SessionID: 10, MessageID: 1002, MessageSeq: 3},
			{UID: "u2", SessionID: 11, MessageID: 2001, MessageSeq: 4},
		}, result.Tokens, []int{1, 2, 4}); finished != 3 {
		t.Fatalf("FinishBindBatch() = %d, want 3 successful reservations", finished)
	}
	pending, ok := tracker.Ack(Recvack{UID: "u1", SessionID: 10, MessageID: 1001})
	if !ok || pending.MessageSeq != 2 || pending.DeliveredAt != 100 {
		t.Fatalf("Ack(overwritten batch entry) = %#v, %v, want seq 2 delivered at 100", pending, ok)
	}
	if _, ok := tracker.Ack(Recvack{UID: "u1", SessionID: 10, MessageID: 1002}); ok {
		t.Fatal("Ack(rejected batch entry) ok = true, want false")
	}
}

func TestAckTrackerBindBatchIsSafeWithConcurrentAckAndSessionClose(t *testing.T) {
	tracker := NewAckTracker(AckTrackerOptions{ShardCount: 32})
	const goroutines = 16
	const entriesPerGoroutine = 64

	var wg sync.WaitGroup
	for worker := 0; worker < goroutines; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			batch := make([]PendingRecvAck, entriesPerGoroutine)
			for i := range batch {
				batch[i] = PendingRecvAck{
					UID:       fmt.Sprintf("u-%d-%d", worker, i),
					SessionID: uint64(worker*entriesPerGoroutine + i + 1),
					MessageID: uint64(i + 1),
				}
			}
			bound := tracker.BindBatch(batch)
			indexes := make([]int, len(batch))
			for i := range indexes {
				indexes[i] = i
			}
			if finished := tracker.FinishBindBatch(batch, bound.Tokens, indexes); finished != len(batch) {
				t.Errorf("FinishBindBatch() = %d, want %d", finished, len(batch))
			}
			for i, pending := range batch {
				if !bound.Tokens[i].Valid() {
					t.Errorf("BindBatch() Tokens[%d] invalid", i)
					continue
				}
				if i%2 == 0 {
					_, _ = tracker.Ack(Recvack{UID: pending.UID, SessionID: pending.SessionID, MessageID: pending.MessageID})
				} else {
					_ = tracker.SessionClosed(pending.UID, pending.SessionID)
				}
			}
		}()
	}
	wg.Wait()
	if got := tracker.PendingCount(); got != 0 {
		t.Fatalf("PendingCount() = %d, want 0 after concurrent cleanup", got)
	}
}

func TestAckTrackerCommittedRefreshBatch55AllocationAndStorageBudget(t *testing.T) {
	tracker := NewAckTracker(AckTrackerOptions{ShardCount: 32})
	pending := make([]PendingRecvAck, 55)
	for i := range pending {
		pending[i] = PendingRecvAck{UID: fmt.Sprintf("u-%d", i), SessionID: uint64(i + 1), MessageID: 1001}
	}
	indexes := make([]int, len(pending))
	for i := range indexes {
		indexes[i] = i
	}
	warm := tracker.BindBatch(pending)
	if warm.Added != len(pending) {
		t.Fatalf("warm BindBatch() Added = %d, want %d", warm.Added, len(pending))
	}
	if finished := tracker.FinishBindBatch(pending, warm.Tokens, indexes); finished != len(pending) {
		t.Fatalf("warm FinishBindBatch() = %d, want %d", finished, len(pending))
	}
	allocs := testing.AllocsPerRun(1000, func() {
		result := tracker.BindBatch(pending)
		if len(result.Tokens) != len(pending) || !result.Tokens[len(result.Tokens)-1].Valid() {
			panic("BindBatch returned unaligned result")
		}
		if finished := tracker.FinishBindBatch(pending, result.Tokens, indexes); finished != len(pending) {
			panic("FinishBindBatch returned incomplete result")
		}
	})
	// A committed refresh must retain its own tentative PendingRecvAck until the
	// write succeeds. The truthful upper bound is one lazy one-element attempt
	// slice per key plus the item-aligned result token slice.
	maxAllocs := float64(len(pending) + 2)
	if allocs > maxAllocs {
		t.Fatalf("committed BindBatch(55) allocations = %.1f, want <= %.0f", allocs, maxAllocs)
	}
	for _, item := range pending {
		shard := tracker.shard(item.SessionID)
		shard.mu.Lock()
		entry := shard.byMessage[ackMessageKey{uid: item.UID, sessionID: item.SessionID, messageID: item.MessageID}]
		shard.mu.Unlock()
		if !entry.committed || entry.primary.Valid() || len(entry.extraAttempts) != 0 || cap(entry.extraAttempts) != 0 {
			t.Fatalf("entry after committed refresh = %#v, want committed with no retained attempt storage", entry)
		}
	}
}

func TestAckTrackerPendingCountStaysConsistentAcrossMutations(t *testing.T) {
	now := int64(200)
	tracker := NewAckTracker(AckTrackerOptions{
		ShardCount: 4,
		Now: func() int64 {
			return now
		},
	})

	tracker.Bind(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1001, DeliveredAt: 100})
	tracker.Bind(PendingRecvAck{UID: "u1", SessionID: 10, MessageID: 1002, DeliveredAt: 190})
	tracker.Bind(PendingRecvAck{UID: "u2", SessionID: 20, MessageID: 2001, DeliveredAt: 100})
	if got := tracker.PendingCount(); got != 3 {
		t.Fatalf("PendingCount() after binds = %d, want 3", got)
	}

	if _, ok := tracker.Ack(Recvack{UID: "u1", SessionID: 10, MessageID: 1002}); !ok {
		t.Fatalf("Ack() ok = false, want true")
	}
	if got := tracker.PendingCount(); got != 2 {
		t.Fatalf("PendingCount() after ack = %d, want 2", got)
	}

	removed := tracker.SessionClosed("u2", 20)
	if len(removed) != 1 {
		t.Fatalf("SessionClosed() removed %d, want 1", len(removed))
	}
	if got := tracker.PendingCount(); got != 1 {
		t.Fatalf("PendingCount() after close = %d, want 1", got)
	}

	expired := tracker.Expire(50 * time.Second)
	if len(expired) != 1 || expired[0].MessageID != 1001 {
		t.Fatalf("Expire() = %#v, want message 1001", expired)
	}
	if got := tracker.PendingCount(); got != 0 {
		t.Fatalf("PendingCount() after expire = %d, want 0", got)
	}
}
