package channelwrite

import (
	"context"
	"testing"
	"time"
)

func TestEffectSchedulerCapacityUsesConfiguredWorkersOnly(t *testing.T) {
	scheduler := newEffectScheduler(effectSchedulerOptions{
		ReactorCount:      4,
		PrepareWorkers:    2,
		AppendWorkers:     3,
		PostCommitWorkers: 5,
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := scheduler.stop(ctx); err != nil {
			t.Fatalf("scheduler.stop() error = %v", err)
		}
	})

	if got, want := scheduler.prepareWorkerCapacity(), 2; got != want {
		t.Fatalf("prepareWorkerCapacity() = %d, want %d", got, want)
	}
	if got, want := scheduler.appendWorkerCapacity(), 3; got != want {
		t.Fatalf("appendWorkerCapacity() = %d, want %d", got, want)
	}
	if got, want := scheduler.postCommitWorkerCapacity(), 5; got != want {
		t.Fatalf("postCommitWorkerCapacity() = %d, want %d", got, want)
	}
}
