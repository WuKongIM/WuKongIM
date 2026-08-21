//go:build integration

package meta

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/commit"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

func TestMetaBatchReadYourWritesAndPublishesChannelCache(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()

	batch := store.db.NewBatch()
	channel := Channel{ChannelID: "batch-channel", ChannelType: 1, Ban: 7}
	if err := batch.UpsertChannel(5, channel); err != nil {
		t.Fatalf("UpsertChannel(): %v", err)
	}
	got, ok, err := batch.GetChannel(ctx, 5, channel.ChannelID, channel.ChannelType)
	if err != nil || !ok {
		t.Fatalf("batch GetChannel() ok=%v err=%v", ok, err)
	}
	if got != channel {
		t.Fatalf("batch channel = %+v, want %+v", got, channel)
	}
	if _, ok, err := store.db.HashSlot(5).GetChannel(ctx, channel.ChannelID, channel.ChannelType); err != nil || ok {
		t.Fatalf("shard GetChannel(before commit) ok=%v err=%v, want missing", ok, err)
	}
	if err := batch.Commit(ctx); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	if got := store.db.channelCacheSize(); got != 1 {
		t.Fatalf("channel cache size after batch commit = %d, want 1", got)
	}
	got, ok, err = store.db.HashSlot(5).GetChannel(ctx, channel.ChannelID, channel.ChannelType)
	if err != nil || !ok || got != channel {
		t.Fatalf("shard GetChannel(after commit) = %+v ok=%v err=%v, want %+v", got, ok, err, channel)
	}
}

func TestMetaBatchPersistsSlotAppliedIndexAtomically(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()

	batch := store.db.NewBatch()
	if err := batch.SetSlotAppliedIndex(12, 41); err != nil {
		t.Fatalf("SetSlotAppliedIndex() error = %v", err)
	}
	if got, err := store.db.SlotAppliedIndex(ctx, 12); err != nil || got != 0 {
		t.Fatalf("SlotAppliedIndex(before commit) = %d, %v; want 0, nil", got, err)
	}
	if err := batch.Commit(ctx); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if got, err := store.db.SlotAppliedIndex(ctx, 12); err != nil || got != 41 {
		t.Fatalf("SlotAppliedIndex(after commit) = %d, %v; want 41, nil", got, err)
	}
}

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

func TestMetaBatchCreateUniqueGuardRollback(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()

	batch := store.db.NewBatch()
	user := User{UID: "dup-user", Token: "a", DeviceFlag: 1}
	if err := batch.CreateUser(2, user); err != nil {
		t.Fatalf("CreateUser(first): %v", err)
	}
	user.Token = "b"
	if err := batch.CreateUser(2, user); err != nil {
		t.Fatalf("CreateUser(second stage): %v", err)
	}
	err := batch.Commit(ctx)
	if !errors.Is(err, dberrors.ErrAlreadyExists) {
		t.Fatalf("Commit() err = %v, want already exists", err)
	}
	if _, ok, err := store.db.HashSlot(2).GetUser(ctx, "dup-user"); err != nil || ok {
		t.Fatalf("GetUser(after failed commit) ok=%v err=%v, want missing", ok, err)
	}
}

func TestMetaBatchLocksHashSlotsInOrder(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)

	batch := store.db.NewBatch()
	if err := batch.UpsertUser(9, User{UID: "u9", Token: "9"}); err != nil {
		t.Fatalf("UpsertUser(9): %v", err)
	}
	if err := batch.UpsertUser(1, User{UID: "u1", Token: "1"}); err != nil {
		t.Fatalf("UpsertUser(1): %v", err)
	}
	if err := batch.Commit(context.Background()); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	if got := batch.lockedOrderForTest(); len(got) != 2 || got[0] != 1 || got[1] != 9 {
		t.Fatalf("locked order = %+v, want [1 9]", got)
	}
}

func TestMetaBatchChannelRuntimeMetaGuardRollback(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(6)

	meta := testRuntimeMeta("guarded", 1)
	meta.ChannelEpoch = 10
	meta.LeaderEpoch = 20
	if _, err := shard.UpsertChannelRuntimeMeta(ctx, meta); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(): %v", err)
	}
	current := normalizeChannelRuntimeMeta(meta)

	batch := store.db.NewBatch()
	if err := batch.GuardChannelRuntimeMeta(6, ChannelRuntimeMetaGuard{
		ChannelID:                 current.ChannelID,
		ChannelType:               current.ChannelType,
		ExpectedChannelEpoch:      current.ChannelEpoch,
		ExpectedLeaderEpoch:       current.LeaderEpoch,
		ExpectedLeader:            current.Leader,
		ExpectedRouteGeneration:   current.RouteGeneration,
		ExpectedWriteFenceToken:   current.WriteFenceToken,
		ExpectedWriteFenceVersion: current.WriteFenceVersion,
	}); err != nil {
		t.Fatalf("GuardChannelRuntimeMeta(stage): %v", err)
	}
	next := current
	next.LeaderEpoch++
	if _, err := batch.UpsertChannelRuntimeMeta(6, next); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(stage): %v", err)
	}
	if err := batch.Commit(ctx); err != nil {
		t.Fatalf("Commit(guarded): %v", err)
	}

	failing := store.db.NewBatch()
	if err := failing.GuardChannelRuntimeMeta(6, ChannelRuntimeMetaGuard{
		ChannelID:               current.ChannelID,
		ChannelType:             current.ChannelType,
		ExpectedChannelEpoch:    current.ChannelEpoch,
		ExpectedLeaderEpoch:     current.LeaderEpoch,
		ExpectedLeader:          current.Leader,
		ExpectedRouteGeneration: current.RouteGeneration,
	}); err != nil {
		t.Fatalf("GuardChannelRuntimeMeta(failing stage): %v", err)
	}
	if err := failing.CreateUser(6, User{UID: "guard-rollback", Token: "x"}); err != nil {
		t.Fatalf("CreateUser(stage): %v", err)
	}
	err := failing.Commit(ctx)
	if !errors.Is(err, dberrors.ErrConflict) {
		t.Fatalf("Commit(stale guard) err = %v, want conflict", err)
	}
	if _, ok, err := shard.GetUser(ctx, "guard-rollback"); err != nil || ok {
		t.Fatalf("GetUser(after stale guard) ok=%v err=%v, want missing", ok, err)
	}
}
