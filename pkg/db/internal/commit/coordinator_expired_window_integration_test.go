//go:build integration

package commit

import (
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

// Failure cases: an owner pause can split already queued work across extra
// synchronous commits; draining can exceed size limits, ignore shutdown, lose
// lane fairness or queue credits, or follow new arrivals without a finite end.
func TestCoordinatorExpiredWindowCollectsQueuedCohort(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := expiredWindowCoordinator(Config{})
		c.consecutiveHigh = maxConsecutiveHigh
		first := expiredWindowRequest(c, "normal-first", PriorityNormal, 1)
		second := expiredWindowRequest(c, "high-second", PriorityHigh, 1)
		paused, resume := pauseExpiredWindowRelease(&second)
		c.enqueuePending(first)
		c.enqueuePending(second)
		selected, ok := c.nextRequest()
		if !ok || selected.Partition != first.Partition || c.consecutiveHigh != 0 {
			t.Fatal("first cohort request did not preserve normal-lane fairness")
		}
		result := make(chan requestBatch, 1)
		go func() { result <- c.collect(selected) }()
		<-paused
		third := expiredWindowRequest(c, "high-third", PriorityHigh, 1)
		fourth := expiredWindowRequest(c, "normal-fourth", PriorityNormal, 1)
		c.enqueuePending(third)
		c.requests <- fourth
		time.Sleep(2 * c.cfg.FlushWindow)
		close(resume)
		batch := <-result
		assertExpiredWindowCohort(t, batch, "normal-first", "high-second", "high-third", "normal-fourth")
		if batch.closed || len(c.queueSlots) != 0 {
			t.Fatalf("cohort closed=%v, remaining queue credits=%d", batch.closed, len(c.queueSlots))
		}
		for _, request := range batch.requests {
			select {
			case <-request.done:
				t.Fatal("collection completed a receipt before physical commit")
			default:
			}
		}
	})
}

func TestCoordinatorExpiredWindowPreservesSizeLimits(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cfg       Config
		thirdSize int
		want      []string
		remaining int
		next      string
	}{
		{"requests", Config{MaxRequests: 3}, 1, []string{"first", "second", "third"}, 1, "fourth"},
		{"records", Config{MaxRecords: 3}, 2, []string{"first", "second"}, 2, "third"},
		{"bytes", Config{MaxBytes: 3}, 2, []string{"first", "second"}, 2, "third"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, result, resume := startExpiredWindowCollection(t, tc.cfg)
				c.enqueuePending(expiredWindowRequest(c, "third", PriorityNormal, tc.thirdSize))
				c.requests <- expiredWindowRequest(c, "fourth", PriorityNormal, 1)
				time.Sleep(2 * c.cfg.FlushWindow)
				close(resume)
				batch := <-result
				assertExpiredWindowCohort(t, batch, tc.want...)
				if len(c.queueSlots) != tc.remaining {
					t.Fatalf("remaining queue credits=%d, want %d", len(c.queueSlots), tc.remaining)
				}
				next, ok := c.nextRequest()
				if !ok || next.Partition != tc.next {
					t.Fatalf("deferred request order changed: %q", next.Partition)
				}
			})
		})
	}
}

func TestCoordinatorExpiredWindowStopsBeforeQueuedDrain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, result, resume := startExpiredWindowCollection(t, Config{})
		third := expiredWindowRequest(c, "third", PriorityNormal, 1)
		c.requests <- third
		time.Sleep(2 * c.cfg.FlushWindow)
		close(c.stopCh)
		close(resume)
		batch := <-result
		if !batch.closed {
			t.Fatal("expired window ignored shutdown")
		}
		assertExpiredWindowCohort(t, batch, "first", "second")
		batch.completeAll(SubmitResult{Outcome: OutcomeDefinitelyNotCommitted, Err: ErrClosed})
		c.failQueued()
		if len(c.queueSlots) != 0 {
			t.Fatalf("shutdown retained %d queue credits", len(c.queueSlots))
		}
		for _, req := range append(batch.requests, third) {
			if outcome := <-req.done; outcome.Outcome != OutcomeDefinitelyNotCommitted || outcome.Err != ErrClosed {
				t.Fatalf("shutdown receipt=%+v", outcome)
			}
		}
	})
}

func TestCoordinatorExpiredWindowDoesNotFollowNewArrivals(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, result, resume := startExpiredWindowCollection(t, Config{})
		third := expiredWindowRequest(c, "third", PriorityNormal, 1)
		release := third.releaseQueueSlot
		third.releaseQueueSlot = func() {
			release()
			c.requests <- expiredWindowRequest(c, "late", PriorityHigh, 1)
		}
		c.enqueuePending(third)
		c.requests <- expiredWindowRequest(c, "fourth", PriorityNormal, 1)
		time.Sleep(2 * c.cfg.FlushWindow)
		close(resume)
		batch := <-result
		assertExpiredWindowCohort(t, batch, "first", "second", "third", "fourth")
		if len(c.queueSlots) != 1 || len(c.requests) != 1 {
			t.Fatalf("late arrival was consumed: queue credits=%d intake=%d", len(c.queueSlots), len(c.requests))
		}
		if next, ok := c.nextRequest(); !ok || next.Partition != "late" {
			t.Fatalf("late arrival not retained for next cohort: %q", next.Partition)
		}
	})
}

func expiredWindowCoordinator(cfg Config) *Coordinator {
	cfg.FlushWindow = defaultFlushWindow
	cfg.QueueSize = 8
	return &Coordinator{cfg: cfg, requests: make(chan pendingRequest, cfg.QueueSize), queueSlots: make(chan struct{}, cfg.QueueSize), stopCh: make(chan struct{})}
}

func expiredWindowRequest(c *Coordinator, name string, priority Priority, size int) pendingRequest {
	req := newPendingRequest(Request{Partition: name, Lane: Lane{Priority: priority}, Records: size, Bytes: size})
	c.queueSlots <- struct{}{}
	req.releaseQueueSlot = func() { <-c.queueSlots }
	return req
}

func pauseExpiredWindowRelease(req *pendingRequest) (<-chan struct{}, chan<- struct{}) {
	paused, resume := make(chan struct{}), make(chan struct{})
	release := req.releaseQueueSlot
	req.releaseQueueSlot = func() { release(); close(paused); <-resume }
	return paused, resume
}

func startExpiredWindowCollection(t *testing.T, cfg Config) (*Coordinator, <-chan requestBatch, chan<- struct{}) {
	t.Helper()
	c := expiredWindowCoordinator(cfg)
	first := expiredWindowRequest(c, "first", PriorityNormal, 1)
	first.releaseQueued()
	second := expiredWindowRequest(c, "second", PriorityNormal, 1)
	paused, resume := pauseExpiredWindowRelease(&second)
	c.enqueuePending(second)
	result := make(chan requestBatch, 1)
	go func() { result <- c.collect(first) }()
	<-paused
	return c, result, resume
}

func assertExpiredWindowCohort(t *testing.T, batch requestBatch, want ...string) {
	t.Helper()
	got := make([]string, len(batch.requests))
	for i, req := range batch.requests {
		got[i] = req.Partition
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cohort=%v, want %v", got, want)
	}
}
