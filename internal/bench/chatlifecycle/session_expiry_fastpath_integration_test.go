//go:build integration

package chatlifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// The failure inventory precedes this file in the task's FAILURE-MATRIX.md.
// These tests call the real expiry method used by Engine.expireSessions. They
// do not choose an index representation or attribute an observed run's stall.
func newSessionExpiryFastPathFixture(t *testing.T, capacity int) sessionTestFixture {
	t.Helper()
	f := newSessionTestFixture(t)
	if err := f.pool.startExpiryCleanup(capacity); err != nil {
		t.Fatalf("startExpiryCleanup: %v", err)
	}
	t.Cleanup(func() {
		f.pool.stopExpiryCleanup()
		if err := f.pool.CloseAll(); err != nil {
			t.Errorf("CloseAll: %v", err)
		}
	})
	return f
}

// Ordinary identities avoid the two intentionally nonexpiring canary anchors.
// This selection uses the existing catalog contract, not expiry-index state.
func sessionExpiryOrdinaryIndexes(t *testing.T, f sessionTestFixture) [2]uint64 {
	t.Helper()
	var result [2]uint64
	count := 0
	for index := uint64(0); index < 256 && count < len(result); index++ {
		group, ordinal, member, err := f.catalog.GroupForMemberIndex(index)
		if err != nil {
			t.Fatal(err)
		}
		if member && group.Category == GroupVeryLarge && uint64(ordinal) < veryLargeCanaryAnchorLimit*f.identity.Workers() {
			continue
		}
		result[count] = index
		count++
	}
	if count != len(result) {
		t.Fatal("ordinary-session fixture identities unavailable")
	}
	return result
}

func sessionExpiryLogin(t *testing.T, f sessionTestFixture, index, ordinal uint64) SessionSnapshot {
	t.Helper()
	session, err := f.pool.Login(context.Background(), SessionLogin{UID: f.identity.UID(index), UserIndex: index, LoginOrdinal: ordinal})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	return session
}

type sessionExpiryCallResult struct {
	count int
	err   error
}

func TestSessionExpiryFastPathNoDueDoesNotWaitForOnlineStateLock(t *testing.T) {
	f := newSessionExpiryFastPathFixture(t, 2)
	session := sessionExpiryLogin(t, f, sessionExpiryOrdinaryIndexes(t, f)[0], 0)
	now := f.clock.Now()
	if !session.Deadline.After(now) {
		t.Fatal("fixture must have a future-deadline online session")
	}

	held, release, holderJoined := make(chan struct{}), make(chan struct{}), make(chan struct{})
	started, expireJoined := make(chan struct{}), make(chan struct{})
	result := make(chan sessionExpiryCallResult, 1)
	var releaseOnce sync.Once
	expireLaunched := false
	t.Cleanup(func() {
		// Release first, including the expected RED path, before joining either
		// goroutine or the fixture's generation cleanup owner.
		releaseOnce.Do(func() { close(release) })
		select {
		case <-holderJoined:
		case <-time.After(time.Second):
			t.Error("state-lock holder did not join")
		}
		if expireLaunched {
			select {
			case <-expireJoined:
			case <-time.After(time.Second):
				t.Error("expireAsync did not join after lock release")
			}
		}
	})
	go func() {
		f.pool.mu.Lock()
		close(held)
		<-release
		f.pool.mu.Unlock()
		close(holderJoined)
	}()
	select {
	case <-held:
	case <-time.After(time.Second):
		t.Fatal("state-lock holder was not admitted")
	}
	expireLaunched = true
	go func() {
		close(started)
		n, err := f.pool.expireAsync(now)
		result <- sessionExpiryCallResult{count: n, err: err}
		close(expireJoined)
	}()
	<-started
	var got sessionExpiryCallResult
	completedWithoutStateLock := false
	select {
	case got = <-result:
		completedWithoutStateLock = true
	case <-time.After(250 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	if !completedWithoutStateLock {
		select {
		case got = <-result:
		case <-time.After(time.Second):
			t.Fatal("expireAsync remained blocked after releasing unrelated state lock")
		}
	}
	select {
	case <-holderJoined:
	case <-time.After(time.Second):
		t.Fatal("state-lock holder did not join after release")
	}
	select {
	case <-expireJoined:
	case <-time.After(time.Second):
		t.Fatal("expireAsync result did not join its owner")
	}
	if got.err != nil || got.count != 0 {
		t.Fatalf("no-due expireAsync = %d, %v; want0,nil", got.count, got.err)
	}
	if !f.pool.IsOnline(session.UID) {
		t.Fatal("no-due check detached a future-deadline session")
	}
	if !completedWithoutStateLock {
		t.Fatal("no-due expireAsync waited for the unrelated online-table state mutex")
	}
}

func TestSessionExpiryFastPathNeverBypassesCleanupConfiguration(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		var pool *SessionPool
		if n, err := pool.expireAsync(time.Unix(1_700_000_000, 0)); n != 0 || !errors.Is(err, errSessionConfig) {
			t.Fatalf("nil expireAsync = %d,%v", n, err)
		}
	})
	for _, mode := range []string{"unstarted", "stopped", "overreserved"} {
		t.Run(mode, func(t *testing.T) {
			f := newSessionTestFixture(t)
			t.Cleanup(func() {
				if err := f.pool.CloseAll(); err != nil {
					t.Errorf("CloseAll: %v", err)
				}
			})
			if mode != "unstarted" {
				if err := f.pool.startExpiryCleanup(2); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(f.pool.stopExpiryCleanup)
			}
			session := sessionExpiryLogin(t, f, sessionExpiryOrdinaryIndexes(t, f)[0], 0)
			if !session.Deadline.After(f.clock.Now()) {
				t.Fatal("invalid-cleanup fixture must have a future deadline")
			}
			if mode == "stopped" {
				f.pool.stopExpiryCleanup()
			}
			if mode == "overreserved" {
				f.pool.cleanupMu.Lock()
				f.pool.cleanupPending = 3
				f.pool.cleanupMu.Unlock()
				defer func() { f.pool.cleanupMu.Lock(); f.pool.cleanupPending = 0; f.pool.cleanupMu.Unlock() }()
			}
			if n, err := f.pool.expireAsync(f.clock.Now()); n != 0 || !errors.Is(err, errSessionConfig) {
				t.Fatalf("%s expireAsync = %d,%v; want config error", mode, n, err)
			}
			if !f.pool.IsOnline(session.UID) {
				t.Fatal("invalid cleanup configuration detached the future session")
			}
		})
	}
}

// Find two actual deterministic schedules instead of rewriting session state
// or assuming a new index's private fields. Publication time stays unchanged.
func sessionExpiryDurationOrdinals(t *testing.T, f sessionTestFixture) (long, short uint64) {
	t.Helper()
	first, err := f.schedule.Login(0)
	if err != nil {
		t.Fatal(err)
	}
	minimum, maximum := first.SessionDuration, first.SessionDuration
	for ordinal := uint64(1); ordinal < distributionCycle; ordinal++ {
		schedule, err := f.schedule.Login(ordinal)
		if err != nil {
			t.Fatal(err)
		}
		if schedule.SessionDuration < minimum {
			minimum = schedule.SessionDuration
			short = ordinal
		}
		if schedule.SessionDuration > maximum {
			maximum = schedule.SessionDuration
			long = ordinal
		}
	}
	if minimum <= 0 || maximum <= minimum {
		t.Fatal("fixture must have distinct positive session durations")
	}
	return long, short
}

func TestSessionExpiryFastPathEarlierPublishedLoginIsNotHidden(t *testing.T) {
	for _, mode := range []string{"additional", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			f := newSessionExpiryFastPathFixture(t, 2)
			indexes := sessionExpiryOrdinaryIndexes(t, f)
			longOrdinal, shortOrdinal := sessionExpiryDurationOrdinals(t, f)
			first := sessionExpiryLogin(t, f, indexes[0], longOrdinal)
			if n, err := f.pool.expireAsync(f.clock.Now()); n != 0 || err != nil {
				t.Fatalf("initial future check = %d,%v", n, err)
			}
			index := indexes[1]
			if mode == "replacement" {
				if err := f.pool.Logout(first.UID); err != nil {
					t.Fatal(err)
				}
				index = indexes[0]
			}
			second := sessionExpiryLogin(t, f, index, shortOrdinal)
			if !second.Deadline.Before(first.Deadline) {
				t.Fatal("published second session must expire before prior future deadline")
			}
			f.clock.Set(second.Deadline)
			if n, err := f.pool.expireAsync(second.Deadline); n != 1 || err != nil {
				t.Fatalf("earlier published deadline = %d,%v; want1,nil", n, err)
			}
			if f.pool.IsOnline(second.UID) {
				t.Fatal("new earlier deadline was hidden by an older future hint")
			}
			if mode == "additional" && !f.pool.IsOnline(first.UID) {
				t.Fatal("earlier expiry detached the unrelated later-deadline session")
			}
		})
	}
}

func TestSessionExpiryFastPathCleanupReservationFailureDoesNotDetachPartOfDueSet(t *testing.T) {
	f := newSessionExpiryFastPathFixture(t, 2)
	indexes := sessionExpiryOrdinaryIndexes(t, f)
	first := sessionExpiryLogin(t, f, indexes[0], 0)
	second := sessionExpiryLogin(t, f, indexes[1], 1)
	now := first.Deadline
	if second.Deadline.After(now) {
		now = second.Deadline
	}
	f.clock.Set(now)
	// One reserved entry leaves capacity for only one member of the two-session
	// due set. Inject the existing reservation invariant, not a new index state.
	f.pool.cleanupMu.Lock()
	f.pool.cleanupPending = 1
	f.pool.cleanupMu.Unlock()
	if n, err := f.pool.expireAsync(now); n != 0 || !errors.Is(err, errSessionConfig) {
		t.Fatalf("insufficient cleanup reservation = %d,%v; want atomic config rejection", n, err)
	}
	if got := f.pool.Counts(); got != (SessionCounts{Online: 2}) {
		t.Fatalf("reservation failure partially detached due ownership: %+v", got)
	}
	if got := f.pool.Snapshot().CloseReasons.Expired; got != 0 {
		t.Fatalf("reservation failure recorded %d expired sessions", got)
	}
	f.pool.cleanupMu.Lock()
	f.pool.cleanupPending = 0
	f.pool.cleanupMu.Unlock()
	if n, err := f.pool.expireAsync(now); n != 2 || err != nil {
		t.Fatalf("restored reservation = %d,%v; want2,nil", n, err)
	}
	if f.pool.IsOnline(first.UID) || f.pool.IsOnline(second.UID) {
		t.Fatal("a due session remained routable after cleanup reservation succeeded")
	}
}

func TestSessionExpiryFastPathOverduePinnedSessionDoesNotHideOtherDueOrReleasedExpiry(t *testing.T) {
	f := newSessionExpiryFastPathFixture(t, 2)
	indexes := sessionExpiryOrdinaryIndexes(t, f)
	longOrdinal, shortOrdinal := sessionExpiryDurationOrdinals(t, f)
	short := sessionExpiryLogin(t, f, indexes[0], shortOrdinal)
	long := sessionExpiryLogin(t, f, indexes[1], longOrdinal)
	if !short.Deadline.Before(long.Deadline) {
		t.Fatal("fixture must have strictly ordered real login deadlines")
	}
	if !f.pool.acquireSendLease(short.UID) {
		t.Fatal("short session SEND lease was not admitted before its deadline")
	}
	leased := true
	t.Cleanup(func() {
		if leased {
			f.pool.releaseSendLease(short.UID)
		}
	})

	// Both deadlines are due. The older pinned one must not stop discovery of
	// other due sessions, and must remain eligible after its lease is released.
	now := long.Deadline
	f.clock.Set(now)
	if n, err := f.pool.expireAsync(now); n != 1 || err != nil {
		t.Fatalf("expiry past pinned minimum = %d,%v; want only unleased long session", n, err)
	}
	if !f.pool.IsOnline(short.UID) {
		t.Fatal("expiry detached the overdue session while its SEND lease still owned it")
	}
	if f.pool.IsOnline(long.UID) {
		t.Fatal("overdue pinned minimum hid the other due session")
	}
	if !f.pool.releaseSendLease(short.UID) {
		t.Fatal("overdue session SEND lease was not released")
	}
	leased = false
	if n, err := f.pool.expireAsync(now); n != 1 || err != nil {
		t.Fatalf("same-clock expiry after lease release = %d,%v; want1,nil", n, err)
	}
	if f.pool.IsOnline(short.UID) {
		t.Fatal("a future or empty hint hid the just-unpinned overdue session")
	}
}
