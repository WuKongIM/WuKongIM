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
			{Selected: 3, Flushed: 3},
			{Selected: 1, Flushed: 1},
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
}

func (f *recordingConversationActiveFlusher) FlushActiveRows(_ context.Context, batchRows int) (conversationactive.FlushResult, error) {
	f.mu.Lock()
	var result conversationactive.FlushResult
	if len(f.results) > len(f.batches) {
		result = f.results[len(f.batches)]
	}
	f.batches = append(f.batches, batchRows)
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
