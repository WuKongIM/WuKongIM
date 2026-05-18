package store

import (
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/cockroachdb/pebble/v2"
)

func TestChannelStoreAppendUsesCommitCoordinatorAcrossChannels(t *testing.T) {
	engine, fs := openCountingCommitCoordinatorTestEngine(t)
	stores := openTestChannelStoresOnEngine(t, engine, "append-1", "append-2")
	engine.commitCoordinator().cfg.FlushWindow = 5 * time.Millisecond

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

func TestChannelStoreAppendBatchesConcurrentSameChannelAppends(t *testing.T) {
	engine, fs := openCountingCommitCoordinatorTestEngine(t)
	st := openTestChannelStoresOnEngine(t, engine, "append-same-channel-batch")[0]
	engine.commitCoordinator().cfg.FlushWindow = 5 * time.Millisecond

	const appendCount = 8
	before := fs.syncCount.Load()
	results := make(chan appendResult, appendCount)
	ready := make(chan struct{}, appendCount)
	start := make(chan struct{})
	st.writeMu.Lock()
	writeMuLocked := true
	defer func() {
		if writeMuLocked {
			st.writeMu.Unlock()
		}
	}()
	for i := range appendCount {
		go func(i int) {
			ready <- struct{}{}
			<-start
			messageID := uint64(i + 1)
			payload := mustEncodeStoreMessage(t, channel.Message{
				MessageID:   messageID,
				ClientMsgNo: fmt.Sprintf("same-channel-%02d", i),
				FromUID:     "u1",
				ChannelID:   st.id.ID,
				ChannelType: st.id.Type,
				Payload:     []byte(fmt.Sprintf("payload-%02d", i)),
			})
			base, err := st.Append([]channel.Record{{ID: messageID, Payload: payload, SizeBytes: len(payload)}})
			results <- appendResult{base: base, err: err}
		}(i)
	}
	for range appendCount {
		<-ready
	}
	close(start)

	deadline := time.After(2 * time.Second)
	for {
		st.appendBatchMu.Lock()
		got := 0
		if st.appendBatch != nil {
			got = len(st.appendBatch.requests)
		}
		st.appendBatchMu.Unlock()
		if got == appendCount {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("same-channel append batch size = %d, want %d", got, appendCount)
		case <-time.After(time.Millisecond):
		}
	}
	st.writeMu.Unlock()
	writeMuLocked = false

	bases := make([]uint64, 0, appendCount)
	for range appendCount {
		result := <-results
		if result.err != nil {
			t.Fatalf("Append() error = %v", result.err)
		}
		bases = append(bases, result.base)
	}
	sort.Slice(bases, func(i, j int) bool { return bases[i] < bases[j] })
	for i, base := range bases {
		if want := uint64(i); base != want {
			t.Fatalf("Append() bases = %v, want contiguous bases 0..%d", bases, appendCount-1)
		}
	}
	if got := st.LEO(); got != appendCount {
		t.Fatalf("LEO() = %d, want %d", got, appendCount)
	}
	for seq := uint64(1); seq <= appendCount; seq++ {
		msg, ok, err := st.GetMessageBySeq(seq)
		if err != nil {
			t.Fatalf("GetMessageBySeq(%d) error = %v", seq, err)
		}
		if !ok {
			t.Fatalf("GetMessageBySeq(%d) missing", seq)
		}
		if msg.MessageSeq != seq {
			t.Fatalf("GetMessageBySeq(%d) MessageSeq = %d, want %d", seq, msg.MessageSeq, seq)
		}
	}
	if got := fs.syncCount.Load() - before; got != 1 {
		t.Fatalf("sync count delta = %d, want 1", got)
	}
}

func TestChannelStoreAppendEntersCommitCoordinatorWithoutSameChannelFlushWait(t *testing.T) {
	engine := openTestEngine(t)
	st := openTestChannelStoresOnEngine(t, engine, "append-no-same-channel-wait")[0]

	commitEntered := make(chan commitRequest, 1)
	installTestCommitCoordinator(t, engine, func(coordinator *commitCoordinator, req commitRequest) {
		commitEntered <- req
		req.done <- commitAppendRequest(t, engine, req)
	})
	engine.commitCoordinator().cfg.FlushWindow = 500 * time.Millisecond

	done := make(chan appendResult, 1)
	go func() {
		payload := mustEncodeStoreMessage(t, channel.Message{MessageID: 1, ClientMsgNo: "commit", FromUID: "u1", ChannelID: st.id.ID, ChannelType: st.id.Type, Payload: []byte("one")})
		base, err := st.Append([]channel.Record{{Payload: payload, SizeBytes: len(payload)}})
		done <- appendResult{base: base, err: err}
	}()

	select {
	case <-commitEntered:
	case <-time.After(75 * time.Millisecond):
		t.Fatal("append waited for same-channel flush window before entering commit coordinator")
	}

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("Append() error = %v", result.err)
		}
		if result.base != 0 {
			t.Fatalf("Append() base = %d, want 0", result.base)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for append result")
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
	coordinator.cfg.FlushWindow = 5 * time.Millisecond
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
