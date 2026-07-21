package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/conversationactive"
)

func TestConversationActiveFlushWorkerFlushesPeriodicallyAndOnStop(t *testing.T) {
	flusher := &recordingConversationActiveFlusher{firstFlush: make(chan struct{})}
	worker := newConversationActiveFlushWorker(conversationActiveFlushWorkerOptions{
		Authority:     flusher,
		FlushInterval: time.Millisecond,
		BatchRows:     7,
	})

	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	select {
	case <-flusher.firstFlush:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for periodic conversation active flush")
	}
	if err := worker.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	batches := flusher.batchRows()
	if len(batches) < 2 {
		t.Fatalf("flush calls = %d, want periodic flush plus final stop flush", len(batches))
	}
	for _, batchRows := range batches {
		if batchRows != 7 {
			t.Fatalf("flush batch rows = %#v, want all calls bounded by 7", batches)
		}
	}
}

func TestConversationActiveFlushWorkerWakesOnCachePressure(t *testing.T) {
	observer := &recordingConversationActivePressureObserver{events: make(chan conversationactive.PressureObservation, 1)}
	flusher := &pressureAwareConversationActiveFlusher{
		recordingConversationActiveFlusher: &recordingConversationActiveFlusher{firstFlush: make(chan struct{})},
		pressure:                           make(chan conversationactive.PressureSignal, 1),
	}
	worker := newConversationActiveFlushWorker(conversationActiveFlushWorkerOptions{
		Authority:       flusher,
		FlushInterval:   time.Hour,
		BatchRows:       7,
		PressureSignals: flusher.pressure,
		Observer:        observer,
	})

	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	flusher.pressure <- conversationactive.PressureSignal{EnqueuedAt: time.Now()}
	select {
	case <-flusher.firstFlush:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cache-pressure flush")
	}
	select {
	case event := <-observer.events:
		if event.Event != "signal_received" || event.WakeupWaitDuration < 0 {
			t.Fatalf("pressure observation = %+v, want signal_received with nonnegative wait", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pressure wakeup observation")
	}
	if err := worker.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if got := flusher.batchRows(); len(got) < 2 || got[0] != 7 {
		t.Fatalf("flush batches = %#v, want immediate pressure flush bounded by 7 plus final drain", got)
	}
}

func TestConversationActiveFlushWorkerRetriesZeroProgressPressureWithoutPeriodicTick(t *testing.T) {
	pressureSignals := make(chan conversationactive.PressureSignal, 1)
	flusher := &recordingConversationActiveFlusher{
		firstFlush: make(chan struct{}),
		results: []conversationactive.FlushResult{
			{Selected: 128, Persisted: 128, Requeued: 128, VersionConflicts: 128},
			{Selected: 128, Persisted: 128, Cleared: 128},
			{},
		},
	}
	worker := newConversationActiveFlushWorker(conversationActiveFlushWorkerOptions{
		Authority:       flusher,
		FlushInterval:   time.Hour,
		BatchRows:       128,
		PressureSignals: pressureSignals,
	})

	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		if err := worker.Stop(context.Background()); err != nil {
			t.Errorf("Stop() error = %v", err)
		}
	})
	pressureSignals <- conversationactive.PressureSignal{EnqueuedAt: time.Now()}
	select {
	case <-flusher.firstFlush:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the initial pressure flush")
	}

	deadline := time.Now().Add(250 * time.Millisecond)
	for len(flusher.batchRows()) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := flusher.batchRows(); len(got) < 2 {
		t.Fatalf("flush batches = %#v, want a bounded retry after zero-progress pressure without waiting for the periodic tick", got)
	}
}

func TestConversationActiveFlushWorkerBacksOffRepeatedZeroProgressPressure(t *testing.T) {
	pressureSignals := make(chan conversationactive.PressureSignal, 1)
	flusher := &recordingConversationActiveFlusher{
		firstFlush: make(chan struct{}),
		results: []conversationactive.FlushResult{
			{Selected: 128, Persisted: 128, Requeued: 128, VersionConflicts: 128},
			{Selected: 128, Persisted: 128, Requeued: 128, VersionConflicts: 128},
			{Selected: 128, Persisted: 128, Requeued: 128, VersionConflicts: 128},
			{Selected: 128, Persisted: 128, Cleared: 128},
			{},
		},
	}
	worker := newConversationActiveFlushWorker(conversationActiveFlushWorkerOptions{
		Authority:             flusher,
		FlushInterval:         time.Hour,
		BatchRows:             128,
		PressureSignals:       pressureSignals,
		PressureRetryMinDelay: 20 * time.Millisecond,
		PressureRetryMaxDelay: 80 * time.Millisecond,
	})

	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		if err := worker.Stop(context.Background()); err != nil {
			t.Errorf("Stop() error = %v", err)
		}
	})
	pressureSignals <- conversationactive.PressureSignal{EnqueuedAt: time.Now()}
	deadline := time.Now().Add(time.Second)
	for len(flusher.batchRows()) < 4 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	callTimes := flusher.flushCallTimes()
	if len(callTimes) != 4 {
		t.Fatalf("flush calls = %d, want three delayed zero-progress retries followed by progress", len(callTimes))
	}
	wantMinimumDelays := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond}
	for index, wantMinimum := range wantMinimumDelays {
		if elapsed := callTimes[index+1].Sub(callTimes[index]); elapsed < wantMinimum {
			t.Fatalf("pressure retry delay[%d] = %v, want at least %v to avoid a busy loop", index, elapsed, wantMinimum)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if got := len(flusher.batchRows()); got != 4 {
		t.Fatalf("flush calls after progress = %d, want retry loop to stop at 4", got)
	}
}

func TestConversationActiveFlushWorkerStopCancelsPendingPressureRetry(t *testing.T) {
	pressureSignals := make(chan conversationactive.PressureSignal, 1)
	flusher := &recordingConversationActiveFlusher{
		firstFlush: make(chan struct{}),
		results: []conversationactive.FlushResult{
			{Selected: 128, Persisted: 128, Requeued: 128, VersionConflicts: 128},
			{},
		},
	}
	worker := newConversationActiveFlushWorker(conversationActiveFlushWorkerOptions{
		Authority:             flusher,
		FlushInterval:         time.Hour,
		BatchRows:             128,
		PressureSignals:       pressureSignals,
		PressureRetryMinDelay: 250 * time.Millisecond,
		PressureRetryMaxDelay: 250 * time.Millisecond,
	})

	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	pressureSignals <- conversationactive.PressureSignal{EnqueuedAt: time.Now()}
	select {
	case <-flusher.firstFlush:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the initial pressure flush")
	}
	if err := worker.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if got := len(flusher.batchRows()); got != 2 {
		t.Fatalf("flush calls after Stop() = %d, want initial pressure attempt plus final empty drain", got)
	}
	time.Sleep(300 * time.Millisecond)
	if got := len(flusher.batchRows()); got != 2 {
		t.Fatalf("flush calls after pending retry deadline = %d, want canceled retry to stay stopped", got)
	}
}

func TestConversationActiveFlushWorkerStopReturnsFinalFlushError(t *testing.T) {
	flushErr := errors.New("store unavailable")
	worker := newConversationActiveFlushWorker(conversationActiveFlushWorkerOptions{
		Authority:     &recordingConversationActiveFlusher{err: flushErr},
		FlushInterval: time.Hour,
		BatchRows:     3,
	})

	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	err := worker.Stop(context.Background())
	if !errors.Is(err, flushErr) {
		t.Fatalf("Stop() error = %v, want %v", err, flushErr)
	}
}

func TestConversationActiveFlushWorkerStopDrainsDirtyRows(t *testing.T) {
	flusher := &recordingConversationActiveFlusher{
		results: []conversationactive.FlushResult{
			{Selected: 3, Persisted: 3},
			{Selected: 1, Persisted: 1},
			{},
		},
	}
	worker := newConversationActiveFlushWorker(conversationActiveFlushWorkerOptions{
		Authority:     flusher,
		FlushInterval: time.Hour,
		BatchRows:     2,
	})

	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := worker.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if got := flusher.batchRows(); len(got) != 3 {
		t.Fatalf("flush calls = %d, want drain until no selected rows", len(got))
	}
}

func TestConversationActiveFlushWorkerStopHonorsContextWhileTickFlushBlocks(t *testing.T) {
	flusher := &recordingConversationActiveFlusher{
		firstFlush: make(chan struct{}),
		blockFlush: make(chan struct{}),
	}
	worker := newConversationActiveFlushWorker(conversationActiveFlushWorkerOptions{
		Authority:     flusher,
		FlushInterval: time.Millisecond,
		BatchRows:     2,
	})

	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	select {
	case <-flusher.firstFlush:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocking periodic flush")
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	err := worker.Stop(stopCtx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want deadline exceeded", err)
	}
	close(flusher.blockFlush)
}

func TestConversationActiveFlushWorkerTimesOutBlockedPeriodicFlush(t *testing.T) {
	flusher := &contextBlockingConversationActiveFlusher{}
	worker := newConversationActiveFlushWorker(conversationActiveFlushWorkerOptions{
		Authority:     flusher,
		FlushInterval: time.Hour,
		FlushTimeout:  10 * time.Millisecond,
		BatchRows:     2,
	})

	startedAt := time.Now()
	_, err := worker.flushOnce(context.Background(), false)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("flushOnce() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("flushOnce() elapsed = %v, want timeout-bounded return", elapsed)
	}
	if got := flusher.calls(); got != 1 {
		t.Fatalf("flush calls = %d, want 1", got)
	}
}

type recordingConversationActiveFlusher struct {
	mu         sync.Mutex
	firstOnce  sync.Once
	firstFlush chan struct{}
	blockFlush chan struct{}
	err        error
	results    []conversationactive.FlushResult
	batches    []int
	callTimes  []time.Time
}

type pressureAwareConversationActiveFlusher struct {
	*recordingConversationActiveFlusher
	pressure chan conversationactive.PressureSignal
}

type recordingConversationActivePressureObserver struct {
	events chan conversationactive.PressureObservation
}

func (*recordingConversationActivePressureObserver) ObserveConversationActiveCache(conversationactive.CacheObservation) {
}

func (*recordingConversationActivePressureObserver) ObserveConversationActiveMutation(conversationactive.MutationObservation) {
}

func (*recordingConversationActivePressureObserver) ObserveConversationActiveFlush(conversationactive.FlushObservation) {
}

func (o *recordingConversationActivePressureObserver) ObserveConversationActivePressure(event conversationactive.PressureObservation) {
	o.events <- event
}

func (f *recordingConversationActiveFlusher) FlushActiveRows(_ context.Context, batchRows int) (conversationactive.FlushResult, error) {
	f.mu.Lock()
	var result conversationactive.FlushResult
	if len(f.results) > len(f.batches) {
		result = f.results[len(f.batches)]
	}
	f.batches = append(f.batches, batchRows)
	f.callTimes = append(f.callTimes, time.Now())
	f.mu.Unlock()
	f.firstOnce.Do(func() {
		if f.firstFlush != nil {
			close(f.firstFlush)
		}
	})
	if f.blockFlush != nil {
		<-f.blockFlush
	}
	return result, f.err
}

func (f *recordingConversationActiveFlusher) batchRows() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.batches...)
}

func (f *recordingConversationActiveFlusher) flushCallTimes() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Time(nil), f.callTimes...)
}

type contextBlockingConversationActiveFlusher struct {
	mu      sync.Mutex
	batches []int
}

func (f *contextBlockingConversationActiveFlusher) FlushActiveRows(ctx context.Context, batchRows int) (conversationactive.FlushResult, error) {
	f.mu.Lock()
	f.batches = append(f.batches, batchRows)
	f.mu.Unlock()
	<-ctx.Done()
	return conversationactive.FlushResult{Selected: batchRows}, ctx.Err()
}

func (f *contextBlockingConversationActiveFlusher) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.batches)
}
