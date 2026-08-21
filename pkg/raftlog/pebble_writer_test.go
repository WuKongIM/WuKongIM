package raftlog

import (
	"testing"
	"time"
)

func TestCollectWriteRequestsWaitsForBoundedCrossScopeBatch(t *testing.T) {
	requests := make(chan *writeRequest, 2)
	deadline := make(chan time.Time, 1)
	first := &writeRequest{scope: SlotScope(1)}
	second := &writeRequest{scope: SlotScope(2)}
	type result struct {
		batch      []*writeRequest
		closed     bool
		timerFired bool
	}
	resultCh := make(chan result, 1)
	go func() {
		batch, closed, timerFired := collectWriteRequests(first, requests, 8, deadline)
		resultCh <- result{batch: batch, closed: closed, timerFired: timerFired}
	}()

	select {
	case got := <-resultCh:
		t.Fatalf("collectWriteRequests returned before its bound: %#v", got)
	default:
	}
	requests <- second
	deadline <- time.Time{}
	got := <-resultCh
	if got.closed || !got.timerFired {
		t.Fatalf("closed/timerFired = %t/%t, want false/true", got.closed, got.timerFired)
	}
	if len(got.batch) != 2 || got.batch[0] != first || got.batch[1] != second {
		t.Fatalf("batch = %#v, want first and second in order", got.batch)
	}
}

func TestCollectWriteRequestsStopsAtItemBound(t *testing.T) {
	requests := make(chan *writeRequest, 2)
	second := &writeRequest{scope: SlotScope(2)}
	third := &writeRequest{scope: SlotScope(3)}
	requests <- second
	requests <- third

	first := &writeRequest{scope: SlotScope(1)}
	batch, closed, timerFired := collectWriteRequests(first, requests, 2, make(chan time.Time))
	if closed || timerFired {
		t.Fatalf("closed/timerFired = %t/%t, want false/false", closed, timerFired)
	}
	if len(batch) != 2 || batch[0] != first || batch[1] != second {
		t.Fatalf("batch = %#v, want first and second in order", batch)
	}
	if got := <-requests; got != third {
		t.Fatalf("remaining request = %p, want third %p", got, third)
	}
}
