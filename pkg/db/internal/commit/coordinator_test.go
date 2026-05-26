package commit_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/commit"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

func TestCoordinatorPublishesAfterCommit(t *testing.T) {
	db := openTestDB(t)
	c := commit.NewCoordinator(db, commit.Config{FlushWindow: time.Millisecond, QueueSize: 8})
	defer c.Close()

	var committed atomic.Bool
	c.SetCommitFunc(func(batch *engine.Batch) error {
		committed.Store(true)
		return batch.Commit(false)
	})

	var published atomic.Bool
	err := c.Submit(context.Background(), commit.Request{
		Build: func(batch *engine.Batch) error {
			return batch.Set([]byte("k"), []byte("v"))
		},
		Publish: func() error {
			if !committed.Load() {
				t.Fatal("published before commit")
			}
			published.Store(true)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Submit(): %v", err)
	}
	if !published.Load() {
		t.Fatal("publish was not called")
	}
}

func TestCoordinatorFailedCommitDoesNotPublish(t *testing.T) {
	db := openTestDB(t)
	c := commit.NewCoordinator(db, commit.Config{FlushWindow: time.Millisecond, QueueSize: 8})
	defer c.Close()

	wantErr := errors.New("commit failed")
	c.SetCommitFunc(func(batch *engine.Batch) error { return wantErr })

	var published atomic.Bool
	err := c.Submit(context.Background(), commit.Request{
		Build:   func(batch *engine.Batch) error { return batch.Set([]byte("k"), []byte("v")) },
		Publish: func() error { published.Store(true); return nil },
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Submit() err = %v, want %v", err, wantErr)
	}
	if published.Load() {
		t.Fatal("publish called after failed commit")
	}
}

func TestCoordinatorFlushWindowBatchesConcurrentRequests(t *testing.T) {
	db := openTestDB(t)
	c := commit.NewCoordinator(db, commit.Config{FlushWindow: 20 * time.Millisecond, QueueSize: 8, MaxRequests: 4})
	defer c.Close()

	var commits atomic.Int64
	c.SetCommitFunc(func(batch *engine.Batch) error {
		commits.Add(1)
		return batch.Commit(false)
	})

	var wg sync.WaitGroup
	for _, key := range []string{"k1", "k2"} {
		key := key
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := c.Submit(context.Background(), commit.Request{
				Build: func(batch *engine.Batch) error { return batch.Set([]byte(key), []byte("v")) },
			})
			if err != nil {
				t.Errorf("Submit(%s): %v", key, err)
			}
		}()
	}
	wg.Wait()
	if commits.Load() != 1 {
		t.Fatalf("commits = %d, want 1", commits.Load())
	}
}

func TestCoordinatorCloseRejectsSubmit(t *testing.T) {
	db := openTestDB(t)
	c := commit.NewCoordinator(db, commit.Config{FlushWindow: time.Millisecond, QueueSize: 8})
	c.Close()
	err := c.Submit(context.Background(), commit.Request{Build: func(batch *engine.Batch) error { return nil }})
	if !errors.Is(err, commit.ErrClosed) {
		t.Fatalf("Submit() err = %v, want closed", err)
	}
}

func openTestDB(t *testing.T) *engine.DB {
	t.Helper()
	db, err := engine.Open(t.TempDir(), engine.Options{})
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
