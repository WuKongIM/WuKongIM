//go:build integration

package meta

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/commit"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

func TestMetaBatchCommitWaitsForTerminalDurableOutcomeAfterAdmission(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)

	commitEntered := make(chan struct{})
	releaseCommit := make(chan struct{})
	store.db.committer.SetCommitFunc(func(batch *engine.Batch) error {
		close(commitEntered)
		<-releaseCommit
		return batch.Commit(true)
	})

	batch := store.db.NewBatch()
	defer batch.Close()
	if err := batch.UpsertUser(3, User{UID: "admitted-user", Token: "token"}); err != nil {
		t.Fatalf("UpsertUser() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- batch.Commit(ctx)
	}()
	<-commitEntered
	cancel()

	select {
	case err := <-result:
		close(releaseCommit)
		t.Fatalf("Commit() returned %v before the admitted physical commit completed", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseCommit)
	if err := <-result; err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if _, ok, err := store.db.HashSlot(3).GetUser(context.Background(), "admitted-user"); err != nil || !ok {
		t.Fatalf("GetUser(after commit) ok=%v err=%v, want present", ok, err)
	}
}

func TestMetaBatchGroupsDisjointHashSlotsIntoOnePhysicalCommit(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	store.db.committer.Close()
	store.db.committer = commit.NewCoordinator(store.engine, commit.Config{
		FlushWindow: 100 * time.Millisecond,
		QueueSize:   8,
		MaxRequests: 2,
	})
	var physicalCommits atomic.Int32
	store.db.committer.SetCommitFunc(func(batch *engine.Batch) error {
		physicalCommits.Add(1)
		return batch.Commit(true)
	})

	batches := []*Batch{store.db.NewBatch(), store.db.NewBatch()}
	defer batches[0].Close()
	defer batches[1].Close()
	if err := batches[0].UpsertUser(1, User{UID: "slot-1", Token: "one"}); err != nil {
		t.Fatalf("UpsertUser(slot=1) error = %v", err)
	}
	if err := batches[1].UpsertUser(2, User{UID: "slot-2", Token: "two"}); err != nil {
		t.Fatalf("UpsertUser(slot=2) error = %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, len(batches))
	for _, batch := range batches {
		batch := batch
		go func() {
			<-start
			results <- batch.Commit(context.Background())
		}()
	}
	close(start)
	for range batches {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("Commit() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for grouped MetaDB commits")
		}
	}
	if got := physicalCommits.Load(); got != 1 {
		t.Fatalf("physical commits = %d, want 1 for two disjoint hash slots", got)
	}
	for hashSlot, uid := range map[HashSlot]string{1: "slot-1", 2: "slot-2"} {
		if _, ok, err := store.db.HashSlot(hashSlot).GetUser(context.Background(), uid); err != nil || !ok {
			t.Fatalf("GetUser(slot=%d uid=%q) ok=%v err=%v, want present", hashSlot, uid, ok, err)
		}
	}
}
