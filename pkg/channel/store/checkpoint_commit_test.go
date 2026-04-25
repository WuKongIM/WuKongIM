package store

import (
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/cockroachdb/pebble/v2"
)

func TestChannelStoreStoreCheckpointBatchesAcrossChannelsWithCommitCoordinator(t *testing.T) {
	engine, fs := openCountingCommitCoordinatorTestEngine(t)
	stores := openTestChannelStoresOnEngine(t, engine, "checkpoint-batch-1", "checkpoint-batch-2")
	engine.checkpointCoordinator().flushWindow = 10 * time.Millisecond

	before := fs.syncCount.Load()
	results := make(chan error, len(stores))
	ready := make(chan struct{}, len(stores))
	start := make(chan struct{})

	for index, st := range stores {
		checkpoint := channel.Checkpoint{Epoch: 3, HW: uint64(index + 1)}
		go func(st *ChannelStore, checkpoint channel.Checkpoint) {
			ready <- struct{}{}
			<-start
			results <- st.StoreCheckpoint(checkpoint)
		}(st, checkpoint)
	}
	for range stores {
		<-ready
	}
	close(start)

	for range stores {
		if err := <-results; err != nil {
			t.Fatalf("StoreCheckpoint() error = %v", err)
		}
	}

	if got := fs.syncCount.Load() - before; got != 1 {
		t.Fatalf("sync count delta = %d, want 1", got)
	}
}

func TestChannelStoreStoreCheckpointBlocksUntilCoordinatorPublishCompletes(t *testing.T) {
	engine := openTestEngine(t)
	st := openTestChannelStoresOnEngine(t, engine, "checkpoint-block")[0]

	commitStarted := make(chan struct{})
	releasePublish := make(chan struct{})
	t.Cleanup(func() {
		closeOnce(releasePublish)
	})
	installTestCheckpointCoordinator(t, engine, func(coordinator *commitCoordinator, req commitRequest) {
		close(commitStarted)
		select {
		case <-releasePublish:
			req.done <- nil
		case <-coordinator.stopCh:
			req.done <- channel.ErrInvalidArgument
		}
	})

	done := make(chan error, 1)
	go func() {
		done <- st.StoreCheckpoint(channel.Checkpoint{Epoch: 4, HW: 1})
	}()

	select {
	case err := <-done:
		t.Fatalf("StoreCheckpoint() returned early: %v", err)
	case <-commitStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for StoreCheckpoint to enter commit coordinator")
	}

	select {
	case err := <-done:
		t.Fatalf("StoreCheckpoint() returned before publish release: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	closeOnce(releasePublish)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("StoreCheckpoint() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for StoreCheckpoint after publish release")
	}
}

func TestChannelStoreStoreCheckpointDoesNotQueueBehindAppendCommitCoordinator(t *testing.T) {
	runChannelStoreCheckpointCoordinatorIndependence(t)
}

func TestChannelStoreCheckpointCoordinatorRemainsIndependentFromAppendCoordinator(t *testing.T) {
	runChannelStoreCheckpointCoordinatorIndependence(t)
}

func runChannelStoreCheckpointCoordinatorIndependence(t *testing.T) {
	engine := openTestEngine(t)
	st := openTestChannelStoresOnEngine(t, engine, "checkpoint-append-hol")[0]

	appendStarted := make(chan struct{})
	releaseAppend := make(chan struct{})
	t.Cleanup(func() {
		closeOnce(releaseAppend)
	})

	installTestCommitCoordinator(t, engine, func(coordinator *commitCoordinator, req commitRequest) {
		closeOnce(appendStarted)
		select {
		case <-releaseAppend:
			req.done <- nil
		case <-coordinator.stopCh:
			req.done <- channel.ErrInvalidArgument
		}
	})

	appendDone := make(chan appendResult, 1)
	go func() {
		payload := mustEncodeStoreMessage(t, channel.Message{MessageID: 1, ClientMsgNo: "commit", FromUID: "u1", ChannelID: st.id.ID, ChannelType: st.id.Type, Payload: []byte("one")})
		base, err := st.Append([]channel.Record{{Payload: payload, SizeBytes: len(payload)}})
		appendDone <- appendResult{base: base, err: err}
	}()

	select {
	case <-appendStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for append to occupy the commit coordinator")
	}

	checkpointDone := make(chan error, 1)
	go func() {
		checkpointDone <- st.StoreCheckpoint(channel.Checkpoint{Epoch: 6, HW: 1})
	}()

	select {
	case err := <-checkpointDone:
		if err != nil {
			t.Fatalf("StoreCheckpoint() error = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("StoreCheckpoint() was blocked behind the append coordinator; checkpoint should use an independent queue")
	}

	closeOnce(releaseAppend)

	select {
	case result := <-appendDone:
		if result.err != nil {
			t.Fatalf("Append() error = %v", result.err)
		}
		if result.base != 0 {
			t.Fatalf("Append() base = %d, want 0", result.base)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for append after coordinator release")
	}
}

func TestChannelStoreStoreCheckpointFanoutsBatchFailureToAllWaiters(t *testing.T) {
	engine, _ := openCountingCommitCoordinatorTestEngine(t)
	stores := openTestChannelStoresOnEngine(t, engine, "checkpoint-fail-1", "checkpoint-fail-2")

	coordinator := engine.checkpointCoordinator()
	coordinator.flushWindow = 10 * time.Millisecond
	coordinator.commit = func(*pebble.Batch) error {
		return errSyntheticSyncFailure
	}

	results := make(chan error, len(stores))
	ready := make(chan struct{}, len(stores))
	start := make(chan struct{})
	for _, st := range stores {
		go func(st *ChannelStore) {
			ready <- struct{}{}
			<-start
			results <- st.StoreCheckpoint(channel.Checkpoint{Epoch: 5, HW: 1})
		}(st)
	}
	for range stores {
		<-ready
	}
	close(start)

	for range stores {
		err := <-results
		if !errors.Is(err, errSyntheticSyncFailure) {
			t.Fatalf("StoreCheckpoint() error = %v, want synthetic sync failure", err)
		}
	}
}

func installTestCheckpointCoordinator(t *testing.T, engine *Engine, handle func(*commitCoordinator, commitRequest)) {
	t.Helper()

	coordinator := &commitCoordinator{
		db:           engine.db,
		requests:     make(chan commitRequest, 16),
		stopAcceptCh: make(chan struct{}),
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}

	engine.mu.Lock()
	engine.checkpoint = coordinator
	engine.mu.Unlock()

	go func() {
		defer close(coordinator.doneCh)
		for {
			select {
			case <-coordinator.stopCh:
				return
			case req := <-coordinator.requests:
				handle(coordinator, req)
			}
		}
	}()
}
