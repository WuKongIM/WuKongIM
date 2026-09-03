package workload

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExactGroupWindowUsesIdleRoundRobinSenderBeforeDeadline(t *testing.T) {
	clock := newManualExactGroupWindowClock(time.Unix(100, 0))
	releasePreferred := make(chan struct{})
	started := make(chan string, 2)
	var (
		mu          sync.Mutex
		inFlight    = make(map[string]int)
		maxInFlight = make(map[string]int)
	)
	stats := &scheduledMessageStats{}
	done := make(chan error, 1)

	go func() {
		done <- runExactGroupWindow(context.Background(), exactGroupWindowConfig{
			totalMessages:  2,
			streamCount:    2,
			maxConcurrency: 2,
			stopAt:         clock.Now().Add(time.Second),
			clock:          clock,
			credits:        NewAssignmentSenderCredits(),
			intent: func(offset int) exactGroupWindowIntent {
				if offset == 0 {
					return exactGroupWindowIntent{senders: []string{"u-0", "u-1"}, preferred: 0}
				}
				return exactGroupWindowIntent{senders: []string{"u-0", "u-2"}, preferred: 0}
			},
			send: func(ctx context.Context, _ int, senderUID string) error {
				mu.Lock()
				inFlight[senderUID]++
				if inFlight[senderUID] > maxInFlight[senderUID] {
					maxInFlight[senderUID] = inFlight[senderUID]
				}
				mu.Unlock()
				started <- senderUID
				if senderUID == "u-0" {
					select {
					case <-releasePreferred:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				mu.Lock()
				inFlight[senderUID]--
				mu.Unlock()
				return nil
			},
			stats: stats,
		})
	}()

	got := []string{waitExactSender(t, started), waitExactSender(t, started)}
	require.Contains(t, got, "u-0")
	require.NotEqual(t, got[0], got[1],
		"the overlapping groups must use two idle members instead of queueing behind one preferred sender")
	close(releasePreferred)
	require.NoError(t, <-done)
	require.Equal(t, uint64(2), stats.Planned)
	require.Equal(t, uint64(2), stats.Enqueued)
	require.Equal(t, uint64(2), stats.Dispatched)
	require.Zero(t, stats.DroppedPendingWindowExpired)
	for uid, maximum := range maxInFlight {
		require.LessOrEqual(t, maximum, 1, "sender %s admitted overlapping SEND operations", uid)
	}
}

func TestExactGroupWindowPreservesRoundRobinOrderWhenSendersAreIdle(t *testing.T) {
	clock := newManualExactGroupWindowClock(time.Unix(200, 0))
	selected := make([]string, 0, 6)
	stats := &scheduledMessageStats{}

	err := runExactGroupWindow(context.Background(), exactGroupWindowConfig{
		totalMessages:  6,
		maxConcurrency: 1,
		stopAt:         clock.Now().Add(time.Second),
		clock:          clock,
		credits:        NewAssignmentSenderCredits(),
		intent: func(offset int) exactGroupWindowIntent {
			return exactGroupWindowIntent{senders: []string{"u-0", "u-1", "u-2"}, preferred: offset}
		},
		send: func(_ context.Context, _ int, senderUID string) error {
			selected = append(selected, senderUID)
			return nil
		},
		stats: stats,
	})

	require.NoError(t, err)
	require.Equal(t, []string{"u-0", "u-1", "u-2", "u-0", "u-1", "u-2"}, selected)
	require.Equal(t, uint64(6), stats.Dispatched)
}

func TestExactGroupWindowMatchesDueIntentsInsteadOfGreedilyStrandingOne(t *testing.T) {
	clock := newManualExactGroupWindowClock(time.Unix(250, 0))
	selected := make(chan struct {
		offset int
		sender string
	}, 2)
	release := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- runExactGroupWindow(context.Background(), exactGroupWindowConfig{
			totalMessages:  2,
			streamCount:    2,
			maxConcurrency: 2,
			stopAt:         clock.Now().Add(time.Second),
			clock:          clock,
			credits:        NewAssignmentSenderCredits(),
			intent: func(offset int) exactGroupWindowIntent {
				if offset == 0 {
					return exactGroupWindowIntent{senders: []string{"u-0", "u-1"}, preferred: 0}
				}
				return exactGroupWindowIntent{senders: []string{"u-0"}, preferred: 0}
			},
			send: func(ctx context.Context, offset int, senderUID string) error {
				selected <- struct {
					offset int
					sender string
				}{offset: offset, sender: senderUID}
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			},
			stats: &scheduledMessageStats{},
		})
	}()

	assignments := map[int]string{}
	for range 2 {
		select {
		case assignment := <-selected:
			assignments[assignment.offset] = assignment.sender
		case <-time.After(time.Second):
			t.Fatal("exact group matching did not dispatch both feasible intents")
		}
	}
	require.Equal(t, map[int]string{0: "u-1", 1: "u-0"}, assignments)
	close(release)
	require.NoError(t, <-done)
}

func TestExactGroupWindowFailsClosedWhenAllSendersStayBusyUntilDeadline(t *testing.T) {
	clock := newManualExactGroupWindowClock(time.Unix(300, 0))
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	stats := &scheduledMessageStats{}
	done := make(chan error, 1)

	go func() {
		done <- runExactGroupWindow(context.Background(), exactGroupWindowConfig{
			totalMessages:  2,
			maxConcurrency: 2,
			stopAt:         clock.Now().Add(100 * time.Millisecond),
			clock:          clock,
			credits:        NewAssignmentSenderCredits(),
			intent: func(int) exactGroupWindowIntent {
				return exactGroupWindowIntent{senders: []string{"u-0"}}
			},
			send: func(ctx context.Context, _ int, _ string) error {
				started <- struct{}{}
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			},
			stats: stats,
		})
	}()

	waitExactStart(t, started)
	clock.Advance(100 * time.Millisecond)
	close(release)
	err := <-done
	require.ErrorIs(t, err, ErrExactGroupWindowInfeasible)
	var infeasible *ExactGroupWindowInfeasibleError
	require.True(t, errors.As(err, &infeasible))
	require.Equal(t, uint64(2), infeasible.Planned)
	require.Equal(t, uint64(1), infeasible.Admitted)
	require.Equal(t, uint64(1), infeasible.Unadmitted)
	require.Len(t, started, 0, "the pending SEND must never enter after stopAt")
	require.Equal(t, uint64(2), stats.Enqueued)
	require.Equal(t, uint64(1), stats.Dispatched)
	require.Equal(t, uint64(1), stats.DroppedPendingWindowExpired)
	require.Zero(t, stats.DroppedUnstartedWindowExpired)
}

func TestExactGroupWindowDoesNotAdmitAtExactDeadline(t *testing.T) {
	clock := newManualExactGroupWindowClock(time.Unix(350, 0))
	stats := &scheduledMessageStats{}
	sends := 0

	err := runExactGroupWindow(context.Background(), exactGroupWindowConfig{
		totalMessages:  2,
		streamCount:    2,
		maxConcurrency: 2,
		stopAt:         clock.Now(),
		clock:          clock,
		credits:        NewAssignmentSenderCredits(),
		intent: func(int) exactGroupWindowIntent {
			return exactGroupWindowIntent{senders: []string{"u-0", "u-1"}}
		},
		send: func(context.Context, int, string) error {
			sends++
			return nil
		},
		stats: stats,
	})

	require.ErrorIs(t, err, ErrExactGroupWindowInfeasible)
	require.Zero(t, sends)
	require.Zero(t, stats.Enqueued)
	require.Zero(t, stats.Dispatched)
	require.Equal(t, uint64(2), stats.DroppedUnstartedWindowExpired)
}

func TestExactGroupWindowRechecksHardDeadlineAfterMatching(t *testing.T) {
	clock := newManualExactGroupWindowClock(time.Unix(360, 0))
	stopAt := clock.Now().Add(100 * time.Millisecond)
	stats := &scheduledMessageStats{}
	intentCalls := 0
	sends := 0

	err := runExactGroupWindow(context.Background(), exactGroupWindowConfig{
		totalMessages:  2,
		maxConcurrency: 2,
		stopAt:         stopAt,
		clock:          clock,
		credits:        NewAssignmentSenderCredits(),
		intent: func(offset int) exactGroupWindowIntent {
			intentCalls++
			if intentCalls == 1 {
				clock.Advance(100 * time.Millisecond)
			}
			return exactGroupWindowIntent{senders: []string{"u-0", "u-1"}, preferred: offset}
		},
		send: func(context.Context, int, string) error {
			sends++
			return nil
		},
		stats: stats,
	})

	require.ErrorIs(t, err, ErrExactGroupWindowInfeasible)
	require.Zero(t, sends, "matching work must not authorize SEND after the hard deadline")
	require.Zero(t, stats.Dispatched)
	require.Equal(t, uint64(2), stats.DroppedPendingWindowExpired)
}

func TestExactGroupWindowCancellationWinsOverDeadlineShortfall(t *testing.T) {
	clock := newManualExactGroupWindowClock(time.Unix(365, 0))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := runExactGroupWindow(ctx, exactGroupWindowConfig{
		totalMessages:  1,
		maxConcurrency: 1,
		stopAt:         clock.Now(),
		clock:          clock,
		credits:        NewAssignmentSenderCredits(),
		intent: func(int) exactGroupWindowIntent {
			return exactGroupWindowIntent{senders: []string{"u-0"}}
		},
		send: func(context.Context, int, string) error {
			t.Fatal("a canceled window must not call SEND")
			return nil
		},
		stats: &scheduledMessageStats{},
	})

	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, ErrExactGroupWindowInfeasible)
}

func TestExactGroupWindowsShareAssignmentSenderCredits(t *testing.T) {
	clock := newManualExactGroupWindowClock(time.Unix(370, 0))
	credits := NewAssignmentSenderCredits()
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	firstDone := make(chan error, 1)

	go func() {
		firstDone <- runExactGroupWindow(context.Background(), exactGroupWindowConfig{
			totalMessages:  1,
			streamCount:    1,
			maxConcurrency: 1,
			stopAt:         clock.Now().Add(time.Second),
			clock:          clock,
			credits:        credits,
			intent: func(int) exactGroupWindowIntent {
				return exactGroupWindowIntent{senders: []string{"u-0"}}
			},
			send: func(context.Context, int, string) error {
				close(firstStarted)
				<-firstRelease
				return nil
			},
			stats: &scheduledMessageStats{},
		})
	}()
	<-firstStarted

	selected := ""
	err := runExactGroupWindow(context.Background(), exactGroupWindowConfig{
		totalMessages:  1,
		streamCount:    1,
		maxConcurrency: 1,
		stopAt:         clock.Now().Add(time.Second),
		clock:          clock,
		credits:        credits,
		intent: func(int) exactGroupWindowIntent {
			return exactGroupWindowIntent{senders: []string{"u-0", "u-1"}}
		},
		send: func(_ context.Context, _ int, senderUID string) error {
			selected = senderUID
			return nil
		},
		stats: &scheduledMessageStats{},
	})
	require.NoError(t, err)
	require.Equal(t, "u-1", selected, "a second workload must not reuse an assignment-busy sender")

	close(firstRelease)
	require.NoError(t, <-firstDone)
}

func TestExactGroupWindowWakesWhenAnotherWindowReleasesSenderCredit(t *testing.T) {
	clock := newManualExactGroupWindowClock(time.Unix(380, 0))
	credits := NewAssignmentSenderCredits()
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- runExactGroupWindow(context.Background(), exactGroupWindowConfig{
			totalMessages: 1, streamCount: 1, maxConcurrency: 1,
			stopAt: clock.Now().Add(time.Second), clock: clock, credits: credits,
			intent: func(int) exactGroupWindowIntent { return exactGroupWindowIntent{senders: []string{"u-0"}} },
			send: func(context.Context, int, string) error {
				close(firstStarted)
				<-firstRelease
				return nil
			},
			stats: &scheduledMessageStats{},
		})
	}()
	<-firstStarted

	secondIntent := make(chan struct{}, 1)
	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- runExactGroupWindow(context.Background(), exactGroupWindowConfig{
			totalMessages: 1, streamCount: 1, maxConcurrency: 1,
			stopAt: clock.Now().Add(time.Second), clock: clock, credits: credits,
			intent: func(int) exactGroupWindowIntent {
				select {
				case secondIntent <- struct{}{}:
				default:
				}
				return exactGroupWindowIntent{senders: []string{"u-0"}}
			},
			send: func(context.Context, int, string) error {
				close(secondStarted)
				return nil
			},
			stats: &scheduledMessageStats{},
		})
	}()
	<-secondIntent
	select {
	case <-secondStarted:
		t.Fatal("a shared sender credit was admitted twice")
	default:
	}
	close(firstRelease)
	require.NoError(t, <-firstDone)
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("sender release did not wake the waiting exact window")
	}
	require.NoError(t, <-secondDone)
}

func TestExactGroupDueLedgerCompressesThreeHundredThousandMessagesByStream(t *testing.T) {
	ledger := newExactGroupDueLedger(1000)
	ledger.enqueueRange(0, 300000)

	require.Equal(t, 300000, ledger.pendingCount)
	require.Len(t, ledger.pendingByStream, 1000)
	require.Equal(t, 1000, ledger.ready.len())
	for _, pending := range ledger.pendingByStream {
		require.Equal(t, 300, pending)
	}
}

func TestExactGroupWindowReleasesEverySenderLeaseAfterOperationError(t *testing.T) {
	clock := newManualExactGroupWindowClock(time.Unix(375, 0))
	credits := NewAssignmentSenderCredits()
	wantErr := errors.New("structural send failure")

	err := runExactGroupWindow(context.Background(), exactGroupWindowConfig{
		totalMessages:  2,
		maxConcurrency: 2,
		stopAt:         clock.Now().Add(time.Second),
		clock:          clock,
		credits:        credits,
		intent: func(offset int) exactGroupWindowIntent {
			return exactGroupWindowIntent{senders: []string{"u-0", "u-1"}, preferred: offset}
		},
		send: func(ctx context.Context, offset int, _ string) error {
			if offset == 0 {
				return wantErr
			}
			<-ctx.Done()
			return ctx.Err()
		},
		stats: &scheduledMessageStats{},
	})

	require.ErrorIs(t, err, wantErr)
	require.Zero(t, credits.busyCount(), "all operation completion paths must release sender credits")
}

func TestExactGroupWindowSenderSelectionIsBoundedForHundredThousandMembers(t *testing.T) {
	const (
		memberCount = 100000
		busyCount   = 2800
	)
	members := make([]string, memberCount)
	for index := range members {
		members[index] = "u-" + strconv.Itoa(index)
	}
	credits := NewAssignmentSenderCredits()
	for index := 0; index < busyCount; index++ {
		require.True(t, credits.tryAcquire(members[index]))
	}
	window := exactGroupWindow{cfg: exactGroupWindowConfig{credits: credits}}
	intent := exactGroupWindowIntent{senders: members, preferred: 0}
	var selected string

	allocations := testing.AllocsPerRun(100, func() {
		var ok bool
		selected, ok = window.firstAvailableSender(intent)
		if !ok {
			panic("no sender selected")
		}
	})

	require.Equal(t, "u-2800", selected)
	require.Zero(t, allocations)
	require.Equal(t, busyCount, credits.busyCount(), "the lease table must retain only active sender credits")
	require.Equal(t, assignmentSenderCreditShardCount, len(credits.shards))
}

func waitExactSender(t *testing.T, started <-chan string) string {
	t.Helper()
	select {
	case sender := <-started:
		return sender
	case <-time.After(time.Second):
		t.Fatal("exact group window did not admit the expected SEND")
		return ""
	}
}

func waitExactStart(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("exact group window did not admit the first SEND")
	}
}

type manualExactGroupWindowClock struct {
	mu     sync.Mutex
	now    time.Time
	timers map[*manualExactGroupWindowTimer]struct{}
}

type manualExactGroupWindowTimer struct {
	clock    *manualExactGroupWindowClock
	deadline time.Time
	c        chan time.Time
	stopped  bool
	fired    bool
}

func newManualExactGroupWindowClock(now time.Time) *manualExactGroupWindowClock {
	return &manualExactGroupWindowClock{now: now, timers: make(map[*manualExactGroupWindowTimer]struct{})}
}

func (c *manualExactGroupWindowClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualExactGroupWindowClock) NewTimer(wait time.Duration) exactGroupWindowTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &manualExactGroupWindowTimer{
		clock:    c,
		deadline: c.now.Add(wait),
		c:        make(chan time.Time, 1),
	}
	c.timers[timer] = struct{}{}
	if wait <= 0 {
		timer.fired = true
		timer.c <- c.now
	}
	return timer
}

func (c *manualExactGroupWindowClock) Advance(elapsed time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(elapsed)
	now := c.now
	for timer := range c.timers {
		if timer.stopped || timer.fired || now.Before(timer.deadline) {
			continue
		}
		timer.fired = true
		timer.c <- now
	}
	c.mu.Unlock()
}

func (t *manualExactGroupWindowTimer) C() <-chan time.Time { return t.c }

func (t *manualExactGroupWindowTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.stopped || t.fired {
		return false
	}
	t.stopped = true
	delete(t.clock.timers, t)
	return true
}
