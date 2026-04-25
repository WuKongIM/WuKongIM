package store

import (
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/cockroachdb/pebble/v2"
)

func TestChannelStoreAppendUsesCommitCoordinatorAcrossChannels(t *testing.T) {
	engine, fs := openCountingCommitCoordinatorTestEngine(t)
	stores := openTestChannelStoresOnEngine(t, engine, "append-1", "append-2")
	engine.commitCoordinator().flushWindow = 5 * time.Millisecond

	before := fs.syncCount.Load()

	results := make(chan appendResult, len(stores))
	ready := make(chan struct{}, len(stores))
	start := make(chan struct{})
	for _, st := range stores {
		go func(st *ChannelStore) {
			ready <- struct{}{}
			<-start
			payload := mustEncodeStoreMessage(t, channel.Message{MessageID: 1, ClientMsgNo: "commit", FromUID: "u1", ChannelID: st.id.ID, ChannelType: st.id.Type, Payload: []byte(st.id.ID)})
			base, err := st.Append([]channel.Record{{Payload: payload, SizeBytes: len(payload)}})
			results <- appendResult{base: base, err: err}
		}(st)
	}
	for range stores {
		<-ready
	}
	close(start)

	for range stores {
		result := <-results
		if result.err != nil {
			t.Fatalf("Append() error = %v", result.err)
		}
		if result.base != 0 {
			t.Fatalf("Append() base = %d, want 0", result.base)
		}
	}

	if got := fs.syncCount.Load() - before; got != 1 {
		t.Fatalf("sync count delta = %d, want 1", got)
	}
}

func TestChannelStoreAppendBlocksUntilSyncCompletes(t *testing.T) {
	engine := openTestEngine(t)
	st := openTestChannelStoresOnEngine(t, engine, "append-block")[0]

	commitStarted := make(chan commitRequest, 1)
	releaseCommit := make(chan struct{})
	t.Cleanup(func() {
		closeOnce(releaseCommit)
	})
	installTestCommitCoordinator(t, engine, func(coordinator *commitCoordinator, req commitRequest) {
		commitStarted <- req
		select {
		case <-releaseCommit:
			req.done <- commitAppendRequest(t, engine, req)
		case <-coordinator.stopCh:
			req.done <- channel.ErrInvalidArgument
		}
	})

	done := make(chan appendResult, 1)
	go func() {
		payload := mustEncodeStoreMessage(t, channel.Message{MessageID: 1, ClientMsgNo: "commit", FromUID: "u1", ChannelID: st.id.ID, ChannelType: st.id.Type, Payload: []byte("one")})
		base, err := st.Append([]channel.Record{{Payload: payload, SizeBytes: len(payload)}})
		done <- appendResult{base: base, err: err}
	}()

	select {
	case <-commitStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for append to enter commit coordinator")
	}

	select {
	case result := <-done:
		t.Fatalf("Append() returned before sync completed: base=%d err=%v", result.base, result.err)
	case <-time.After(200 * time.Millisecond):
	}

	closeOnce(releaseCommit)

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("Append() error = %v", result.err)
		}
		if result.base != 0 {
			t.Fatalf("Append() base = %d, want 0", result.base)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for append after sync release")
	}
}

func TestChannelStoreAppendLEOStaysHiddenUntilPublishCompletes(t *testing.T) {
	engine := openTestEngine(t)
	st := openTestChannelStoresOnEngine(t, engine, "append-leo")[0]

	commitDone := make(chan struct{})
	releasePublish := make(chan struct{})
	t.Cleanup(func() {
		closeOnce(releasePublish)
	})
	installTestCommitCoordinator(t, engine, func(coordinator *commitCoordinator, req commitRequest) {
		batch := engine.db.NewBatch()
		defer batch.Close()

		if err := req.build(batch); err != nil {
			req.done <- err
			return
		}
		if err := batch.Commit(pebble.Sync); err != nil {
			req.done <- err
			return
		}
		close(commitDone)
		select {
		case <-releasePublish:
		case <-coordinator.stopCh:
			req.done <- channel.ErrInvalidArgument
			return
		}
		if req.publish != nil {
			req.done <- req.publish()
			return
		}
		req.done <- nil
	})

	done := make(chan appendResult, 1)
	go func() {
		payload := mustEncodeStoreMessage(t, channel.Message{MessageID: 1, ClientMsgNo: "commit", FromUID: "u1", ChannelID: st.id.ID, ChannelType: st.id.Type, Payload: []byte("one")})
		base, err := st.Append([]channel.Record{{Payload: payload, SizeBytes: len(payload)}})
		done <- appendResult{base: base, err: err}
	}()

	select {
	case <-commitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for append batch commit before publish")
	}

	if got := st.LEO(); got != 0 {
		t.Fatalf("LEO() while publish blocked = %d, want 0", got)
	}
	select {
	case result := <-done:
		t.Fatalf("Append() returned before publish completed: base=%d err=%v", result.base, result.err)
	case <-time.After(200 * time.Millisecond):
	}

	closeOnce(releasePublish)

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("Append() error = %v", result.err)
		}
		if result.base != 0 {
			t.Fatalf("Append() base = %d, want 0", result.base)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for append after publish release")
	}

	if got := st.LEO(); got != 1 {
		t.Fatalf("LEO() after publish = %d, want 1", got)
	}
}

func TestChannelStoreAppendBatchFailureFailsAllWaiters(t *testing.T) {
	engine := openTestEngine(t)
	stores := openTestChannelStoresOnEngine(t, engine, "append-fail-1", "append-fail-2")

	coordinator := engine.commitCoordinator()
	coordinator.flushWindow = 5 * time.Millisecond
	coordinator.commit = func(*pebble.Batch) error {
		return errSyntheticSyncFailure
	}

	results := make(chan appendResult, len(stores))
	ready := make(chan struct{}, len(stores))
	start := make(chan struct{})
	for _, st := range stores {
		go func(st *ChannelStore) {
			ready <- struct{}{}
			<-start
			payload := mustEncodeStoreMessage(t, channel.Message{MessageID: 1, ClientMsgNo: "commit", FromUID: "u1", ChannelID: st.id.ID, ChannelType: st.id.Type, Payload: []byte(st.id.ID)})
			base, err := st.Append([]channel.Record{{Payload: payload, SizeBytes: len(payload)}})
			results <- appendResult{base: base, err: err}
		}(st)
	}
	for range stores {
		<-ready
	}
	close(start)

	for _, st := range stores {
		result := <-results
		if !errors.Is(result.err, errSyntheticSyncFailure) {
			t.Fatalf("Append() error = %v, want synthetic sync failure", result.err)
		}
		if got := st.LEO(); got != 0 {
			t.Fatalf("LEO() after failed append = %d, want 0", got)
		}
	}
}

type appendResult struct {
	base uint64
	err  error
}

func installTestCommitCoordinator(t *testing.T, engine *Engine, handle func(*commitCoordinator, commitRequest)) {
	t.Helper()

	coordinator := &commitCoordinator{
		db:           engine.db,
		requests:     make(chan commitRequest, 16),
		stopAcceptCh: make(chan struct{}),
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}

	engine.mu.Lock()
	engine.coordinator = coordinator
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

func commitAppendRequest(t *testing.T, engine *Engine, req commitRequest) error {
	t.Helper()

	batch := engine.db.NewBatch()
	defer batch.Close()

	if err := req.build(batch); err != nil {
		return err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return err
	}
	if req.publish != nil {
		return req.publish()
	}
	return nil
}

func closeOnce(ch chan struct{}) {
	defer func() {
		_ = recover()
	}()
	close(ch)
}
