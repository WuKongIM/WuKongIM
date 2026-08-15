package commit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/commit"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

func TestCoordinatorSubmitResultClassifiesAtomicCommitBoundaries(t *testing.T) {
	t.Run("build_rejected", func(t *testing.T) {
		db := openTestDB(t)
		coordinator := commit.NewCoordinator(db, commit.Config{QueueSize: 1, MaxRequests: 1})
		defer coordinator.Close()

		wantErr := errors.New("build rejected")
		result := coordinator.SubmitWithOutcome(context.Background(), commit.Request{
			Build: func(*engine.Batch) error { return wantErr },
		})
		if !errors.Is(result.Err, wantErr) || result.Outcome != commit.OutcomeDefinitelyNotCommitted {
			t.Fatalf("SubmitWithOutcome() = %+v, want definitely-not-committed build rejection", result)
		}
	})

	t.Run("physical_commit_error", func(t *testing.T) {
		db := openTestDB(t)
		coordinator := commit.NewCoordinator(db, commit.Config{QueueSize: 1, MaxRequests: 1})
		defer coordinator.Close()

		wantErr := errors.New("commit response lost")
		coordinator.SetCommitFunc(func(*engine.Batch) error { return wantErr })
		result := coordinator.SubmitWithOutcome(context.Background(), commit.Request{
			Build: func(batch *engine.Batch) error { return batch.Set([]byte("key"), []byte("value")) },
		})
		if !errors.Is(result.Err, wantErr) || result.Outcome != commit.OutcomeUnknown {
			t.Fatalf("SubmitWithOutcome() = %+v, want unknown physical commit outcome", result)
		}
	})

	t.Run("committed", func(t *testing.T) {
		db := openTestDB(t)
		coordinator := commit.NewCoordinator(db, commit.Config{QueueSize: 1, MaxRequests: 1})
		defer coordinator.Close()

		result := coordinator.SubmitWithOutcome(context.Background(), commit.Request{
			Build: func(batch *engine.Batch) error { return batch.Set([]byte("key"), []byte("value")) },
		})
		if result.Err != nil || result.Outcome != commit.OutcomeCommitted {
			t.Fatalf("SubmitWithOutcome() = %+v, want committed", result)
		}
	})

	t.Run("caller_canceled_after_admission", func(t *testing.T) {
		db := openTestDB(t)
		coordinator := commit.NewCoordinator(db, commit.Config{QueueSize: 1, MaxRequests: 1})
		defer coordinator.Close()

		ctx, cancel := context.WithCancel(context.Background())
		building := make(chan struct{})
		release := make(chan struct{})
		completed := make(chan commit.SubmitResult, 1)
		go func() {
			completed <- coordinator.SubmitWithOutcome(ctx, commit.Request{
				Build: func(batch *engine.Batch) error {
					close(building)
					<-release
					return batch.Set([]byte("key"), []byte("value"))
				},
			})
		}()
		<-building
		cancel()
		result := <-completed
		if !errors.Is(result.Err, context.Canceled) || result.Outcome != commit.OutcomeUnknown {
			t.Fatalf("SubmitWithOutcome() = %+v, want unknown after admitted cancellation", result)
		}
		close(release)
	})
}
