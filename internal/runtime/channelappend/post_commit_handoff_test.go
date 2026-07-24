package channelappend

import "testing"

func TestPostCommitRetrySchedulerCompactionClearsBackingTail(t *testing.T) {
	const writerCount = 2048

	scheduler := newPostCommitRetryScheduler()
	writers := make([]channelWriter, writerCount)
	for i := range writers {
		scheduler.enqueue(&writers[i])
	}

	for i := 0; i < writerCount/2; i++ {
		if got := scheduler.pop(); got != &writers[i] {
			t.Fatalf("pop %d = %p, want %p", i, got, &writers[i])
		}
	}
	assertPostCommitRetryBackingTailCleared(t, scheduler)

	for i := writerCount / 2; i < writerCount; i++ {
		if got := scheduler.pop(); got != &writers[i] {
			t.Fatalf("pop %d = %p, want %p", i, got, &writers[i])
		}
	}
	assertPostCommitRetryBackingTailCleared(t, scheduler)
	if depth, _ := scheduler.snapshot(); depth != 0 {
		t.Fatalf("retry queue depth = %d, want 0", depth)
	}
}

func assertPostCommitRetryBackingTailCleared(t *testing.T, scheduler *postCommitRetryScheduler) {
	t.Helper()

	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	backing := scheduler.queue[:cap(scheduler.queue)]
	for i := len(scheduler.queue); i < len(backing); i++ {
		if backing[i] != nil {
			t.Fatalf("retry queue backing[%d] = %p beyond visible len %d, want nil", i, backing[i], len(scheduler.queue))
		}
	}
}
