package core

import (
	"testing"
	"time"
)

func TestIdleTrackerClosesOnlyExpiredSessions(t *testing.T) {
	tracker := newIdleTracker(10 * time.Millisecond)
	now := time.Unix(100, 0)
	expired := testIdleSessionState()
	active := testIdleSessionState()

	tracker.touch(expired, now.Add(-11*time.Millisecond))
	tracker.touch(active, now.Add(-5*time.Millisecond))

	got := tracker.popExpired(now)
	if len(got) != 1 || got[0] != expired {
		t.Fatalf("popExpired() = %v, want only expired state", got)
	}
	if got := tracker.popExpired(now); len(got) != 0 {
		t.Fatalf("popExpired() returned already popped states: %v", got)
	}
}

func TestIdleTrackerSkipsStaleDeadlineAfterTouch(t *testing.T) {
	tracker := newIdleTracker(10 * time.Millisecond)
	now := time.Unix(200, 0)
	state := testIdleSessionState()

	tracker.touch(state, now.Add(-11*time.Millisecond))
	tracker.touch(state, now)

	if got := tracker.popExpired(now); len(got) != 0 {
		t.Fatalf("popExpired() returned stale deadline: %v", got)
	}
	if wait := tracker.nextWait(now); wait != 10*time.Millisecond {
		t.Fatalf("nextWait() = %v, want %v", wait, 10*time.Millisecond)
	}
}

func TestIdleTrackerTouchUpdatesExistingDeadlineWithoutGrowingHeap(t *testing.T) {
	tracker := newIdleTracker(10 * time.Millisecond)
	now := time.Unix(250, 0)
	state := testIdleSessionState()

	for i := 0; i < 1024; i++ {
		tracker.touch(state, now.Add(time.Duration(i)*time.Millisecond))
	}

	if got := tracker.len(); got != 1 {
		t.Fatalf("tracker len = %d, want 1 deadline for repeatedly touched session", got)
	}
}

func TestIdleTrackerRemoveDeletesTrackedDeadline(t *testing.T) {
	tracker := newIdleTracker(10 * time.Millisecond)
	now := time.Unix(260, 0)
	state := testIdleSessionState()

	tracker.touch(state, now)
	tracker.remove(state)

	if got := tracker.len(); got != 0 {
		t.Fatalf("tracker len after remove = %d, want 0", got)
	}
}

func TestIdleTrackerNextWaitDoesNotScanAllSessions(t *testing.T) {
	tracker := newIdleTracker(10 * time.Millisecond)
	now := time.Unix(300, 0)
	staleStates := make([]*sessionState, 128)
	for i := range staleStates {
		staleStates[i] = testIdleSessionState()
		tracker.touch(staleStates[i], now.Add(-time.Second))
		tracker.touch(staleStates[i], now.Add(time.Duration(i)*time.Millisecond))
	}

	if wait := tracker.nextWait(now); wait != 10*time.Millisecond {
		t.Fatalf("nextWait() = %v, want %v", wait, 10*time.Millisecond)
	}
	if got := tracker.popExpired(now); len(got) != 0 {
		t.Fatalf("popExpired() returned stale entries: %v", got)
	}
	if tracker.len() != len(staleStates) {
		t.Fatalf("tracker len = %d, want %d active deadlines after stale pruning", tracker.len(), len(staleStates))
	}
}

func testIdleSessionState() *sessionState {
	return &sessionState{closedCh: make(chan struct{})}
}

func BenchmarkServerIdleMonitorTickCost(b *testing.B) {
	const states = 10_000
	tracker := newIdleTracker(time.Hour)
	now := time.Unix(400, 0)
	for i := 0; i < states; i++ {
		tracker.touch(testIdleSessionState(), now)
	}

	b.ReportAllocs()
	b.ReportMetric(states, "sessions")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tracker.nextWait(now)
		if expired := tracker.popExpired(now); len(expired) != 0 {
			b.Fatalf("popExpired() returned %d states", len(expired))
		}
	}
}
