package message

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

func TestEngineForChannelReturnsDistinctLeasesSharingCanonicalEntry(t *testing.T) {
	eng := openCompatEngine(t)
	id := channel.ChannelID{ID: "compat-lease", Type: 1}
	first := mustForChannel(t, eng, "compat-lease:1", id)
	second := mustForChannel(t, eng, "compat-lease:1", id)
	if first == second {
		t.Fatal("ForChannel() returned the same compatibility lease")
	}
	if first.log.channelEntry != second.log.channelEntry {
		t.Fatal("compatibility leases do not share one canonical entry")
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close(): %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second Close(): %v", err)
	}
}

func TestEngineLastStoreCloseReclaimsEntry(t *testing.T) {
	eng := openCompatEngine(t)
	store := mustForChannel(t, eng, "compat-reclaim:1", channel.ChannelID{ID: "compat-reclaim", Type: 1})
	if err := store.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if got := eng.db.registry.snapshot().activeEntries; got != 0 {
		t.Fatalf("registry entries = %d, want 0", got)
	}
}

func TestEngineReacquireRestoresDurableLEO(t *testing.T) {
	eng := openCompatEngine(t)
	id := channel.ChannelID{ID: "compat-durable", Type: 1}
	first := mustForChannel(t, eng, "compat-durable:1", id)
	if _, err := first.Append([]channel.Record{compatTestRecord(t, 9001, id.ID, "first")}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close(): %v", err)
	}
	second := mustForChannel(t, eng, "compat-durable:1", id)
	defer second.Close()
	if leo, err := second.LEOWithError(); err != nil || leo != 1 {
		t.Fatalf("LEOWithError() = %d, %v, want 1, nil", leo, err)
	}
}

func TestEngineReadDoesNotPopulateRegistry(t *testing.T) {
	eng := openCompatEngine(t)
	id := channel.ChannelID{ID: "compat-raw", Type: 1}
	store := mustForChannel(t, eng, "compat-raw:1", id)
	if _, err := store.Append([]channel.Record{compatTestRecord(t, 9011, id.ID, "raw")}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if got := eng.db.registry.snapshot().activeEntries; got != 0 {
		t.Fatalf("registry entries before read = %d, want 0", got)
	}
	records, err := eng.Read("compat-raw:1", 0, 10, 1<<20)
	if err != nil || len(records) != 1 {
		t.Fatalf("Read() = %d records, %v, want 1, nil", len(records), err)
	}
	if got := eng.db.registry.snapshot().activeEntries; got != 0 {
		t.Fatalf("registry entries after read = %d, want 0", got)
	}
}

func TestChannelStoreRejectsUseAfterClose(t *testing.T) {
	eng := openCompatEngine(t)
	store := mustForChannel(t, eng, "compat-closed:1", channel.ChannelID{ID: "compat-closed", Type: 1})
	if err := store.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if _, err := store.LEOWithError(); !errors.Is(err, channel.ErrClosed) {
		t.Fatalf("LEOWithError() err = %v, want %v", err, channel.ErrClosed)
	}
}

func TestCommitCoordinatorCancellationKeepsEntryAndAppendLockOwnedUntilPublish(t *testing.T) {
	eng := openCompatEngine(t)
	store := mustForChannel(t, eng, "commit-cancel:1", channel.ChannelID{ID: "commit-cancel", Type: 1})
	entry := store.log.channelEntry
	record := compatTestRecord(t, 9101, "commit-cancel", "cancel")
	commitEntered := make(chan struct{})
	releaseCommit := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCommit) }) }
	defer release()
	eng.committer.SetCommitFunc(func(batch *engine.Batch) error {
		close(commitEntered)
		<-releaseCommit
		return batch.Commit(false)
	})

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := store.appendRecords(ctx, []channel.Record{record}, AppendStrict)
		result <- err
	}()
	waitSignal(t, commitEntered, "commit coordinator admission")
	cancel()
	if err := waitResult(t, result, "canceled append"); !errors.Is(err, context.Canceled) {
		t.Fatalf("appendRecords() err = %v, want %v", err, context.Canceled)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store Close(): %v", err)
	}
	if entry.appendMu.TryLock() {
		entry.appendMu.Unlock()
		t.Fatal("append lock released before terminal publish")
	}
	if got := eng.db.registry.snapshot().backgroundPins; got != 1 {
		t.Fatalf("background pins = %d, want 1", got)
	}
	release()
	waitForRegistryCondition(t, func() bool { return eng.db.registry.snapshot().activeEntries == 0 }, "commit pin release")
	if !entry.appendMu.TryLock() {
		t.Fatal("append lock remained held after terminal publish")
	}
	entry.appendMu.Unlock()
}

func TestCommitCoordinatorCancellationKeepsCheckpointLockOwnedUntilPublish(t *testing.T) {
	eng := openCompatEngine(t)
	store := mustForChannel(t, eng, "apply-cancel:1", channel.ChannelID{ID: "apply-cancel", Type: 1})
	entry := store.log.channelEntry
	record := compatTestRecord(t, 9201, "apply-cancel", "cancel")
	commitEntered := make(chan struct{})
	releaseCommit := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCommit) }) }
	defer release()
	eng.committer.SetCommitFunc(func(batch *engine.Batch) error {
		close(commitEntered)
		<-releaseCommit
		return batch.Commit(false)
	})

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := store.applyFetchedRecords(ctx, channel.ApplyFetchStoreRequest{
			Records:    []channel.Record{record},
			Checkpoint: &channel.Checkpoint{Epoch: 1, HW: 1},
		}, nil, AppendTrustedContiguous)
		result <- err
	}()
	waitSignal(t, commitEntered, "apply commit admission")
	cancel()
	if err := waitResult(t, result, "canceled apply"); !errors.Is(err, context.Canceled) {
		t.Fatalf("applyFetchedRecords() err = %v, want %v", err, context.Canceled)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store Close(): %v", err)
	}
	if entry.appendMu.TryLock() {
		entry.appendMu.Unlock()
		t.Fatal("append lock released before apply terminal state")
	}
	if entry.checkpointMu.TryLock() {
		entry.checkpointMu.Unlock()
		t.Fatal("checkpoint lock released before apply terminal state")
	}
	release()
	waitForRegistryCondition(t, func() bool { return eng.db.registry.snapshot().activeEntries == 0 }, "apply ownership release")
	if !entry.checkpointMu.TryLock() {
		t.Fatal("checkpoint lock remained held after apply terminal state")
	}
	entry.checkpointMu.Unlock()
}

func TestCommitCoordinatorBuildErrorReleasesChannelPins(t *testing.T) {
	eng := openCompatEngine(t)
	store := mustForChannel(t, eng, "build-error:1", channel.ChannelID{ID: "build-error", Type: 1})
	entry := store.log.channelEntry
	entry.appendMu.Lock()
	err := commitPreparedRowsBatch(context.Background(), eng, []preparedCommitRows{{
		store: store,
		point: &EpochPoint{},
	}}, commitLaneFollowerApply)
	if !errors.Is(err, channel.ErrCorruptState) {
		t.Fatalf("commitPreparedRowsBatch() err = %v, want corrupt state", err)
	}
	if got := eng.db.registry.snapshot().backgroundPins; got != 0 {
		t.Fatalf("background pins = %d, want 0", got)
	}
	if !entry.appendMu.TryLock() {
		t.Fatal("append lock remained held after build error")
	}
	entry.appendMu.Unlock()
	if err := store.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
}

func TestCommitCoordinatorCommitErrorReleasesChannelPins(t *testing.T) {
	eng := openCompatEngine(t)
	store := mustForChannel(t, eng, "commit-error:1", channel.ChannelID{ID: "commit-error", Type: 1})
	wantErr := errors.New("commit failed")
	eng.committer.SetCommitFunc(func(*engine.Batch) error { return wantErr })
	_, err := store.Append([]channel.Record{compatTestRecord(t, 9301, "commit-error", "failed")})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Append() err = %v, want %v", err, wantErr)
	}
	if got := eng.db.registry.snapshot().backgroundPins; got != 0 {
		t.Fatalf("background pins = %d, want 0", got)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
}

func TestCommitCoordinatorCloseReleasesChannelPins(t *testing.T) {
	eng := openCompatEngine(t)
	store := mustForChannel(t, eng, "engine-close-pin:1", channel.ChannelID{ID: "engine-close-pin", Type: 1})
	commitEntered := make(chan struct{})
	releaseCommit := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCommit) }) }
	defer release()
	eng.committer.SetCommitFunc(func(batch *engine.Batch) error {
		close(commitEntered)
		<-releaseCommit
		return batch.Commit(false)
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	record := compatTestRecord(t, 9401, "engine-close-pin", "close")
	go func() {
		_, err := store.appendRecords(ctx, []channel.Record{record}, AppendStrict)
		result <- err
	}()
	waitSignal(t, commitEntered, "commit before engine close")
	cancel()
	if err := waitResult(t, result, "canceled append before close"); !errors.Is(err, context.Canceled) {
		t.Fatalf("appendRecords() err = %v, want %v", err, context.Canceled)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store Close(): %v", err)
	}
	registry := eng.db.registry
	closed := make(chan error, 1)
	go func() { closed <- eng.Close() }()
	waitForRegistryCondition(t, registry.closing.Load, "engine registry close")
	release()
	if err := waitResult(t, closed, "engine close"); err != nil {
		t.Fatalf("Engine.Close(): %v", err)
	}
	if got := registry.snapshot().backgroundPins; got != 0 {
		t.Fatalf("background pins after Close = %d, want 0", got)
	}
}

func TestConfigureCommitCoordinatorDrainsOldChannelPins(t *testing.T) {
	eng := openCompatEngine(t)
	store := mustForChannel(t, eng, "configure-pin:1", channel.ChannelID{ID: "configure-pin", Type: 1})
	commitEntered := make(chan struct{})
	releaseCommit := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCommit) }) }
	defer release()
	eng.committer.SetCommitFunc(func(batch *engine.Batch) error {
		close(commitEntered)
		<-releaseCommit
		return batch.Commit(false)
	})

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	record := compatTestRecord(t, 9451, "configure-pin", "configure")
	go func() {
		_, err := store.appendRecords(ctx, []channel.Record{record}, AppendStrict)
		result <- err
	}()
	waitSignal(t, commitEntered, "old coordinator commit")
	cancel()
	if err := waitResult(t, result, "canceled append before configure"); !errors.Is(err, context.Canceled) {
		t.Fatalf("appendRecords() err = %v, want %v", err, context.Canceled)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store Close(): %v", err)
	}
	if got := eng.db.registry.snapshot().backgroundPins; got != 1 {
		t.Fatalf("background pins before configure = %d, want 1", got)
	}

	configured := make(chan struct{})
	go func() {
		eng.ConfigureCommitCoordinator(CommitCoordinatorConfig{QueueSize: 8})
		close(configured)
	}()
	select {
	case <-configured:
		t.Fatal("ConfigureCommitCoordinator returned before old coordinator drained")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	waitSignal(t, configured, "coordinator reconfiguration")
	waitForRegistryCondition(t, func() bool {
		snapshot := eng.db.registry.snapshot()
		return snapshot.backgroundPins == 0 && snapshot.activeEntries == 0
	}, "old coordinator pin release")
}

func TestStoreAppendBatchRejectsDuplicateCanonicalEntry(t *testing.T) {
	eng := openCompatEngine(t)
	id := channel.ChannelID{ID: "append-duplicate", Type: 1}
	first := mustForChannel(t, eng, "append-duplicate:1", id)
	second := mustForChannel(t, eng, "append-duplicate:1", id)
	defer first.Close()
	defer second.Close()
	results := StoreAppendBatch(context.Background(), []AppendBatchItem{
		{Store: first, Records: []channel.Record{compatTestRecord(t, 9501, id.ID, "first")}},
		{Store: second, Records: []channel.Record{compatTestRecord(t, 9502, id.ID, "second")}},
	})
	if len(results) != 2 || !errors.Is(results[0].Err, channel.ErrInvalidArgument) || !errors.Is(results[1].Err, channel.ErrInvalidArgument) {
		t.Fatalf("StoreAppendBatch() = %+v, want duplicate canonical errors", results)
	}
	if leo, err := first.LEOWithError(); err != nil || leo != 0 {
		t.Fatalf("LEOWithError() = %d, %v, want 0, nil", leo, err)
	}
}

func TestStoreAppendBatchRejectsClosedAndLiveSiblingBeforeLeaseValidation(t *testing.T) {
	eng := openCompatEngine(t)
	duplicateID := channel.ChannelID{ID: "append-closed-sibling", Type: 1}
	closed := mustForChannel(t, eng, "append-closed-sibling:1", duplicateID)
	live := mustForChannel(t, eng, "append-closed-sibling:1", duplicateID)
	uniqueID := channel.ChannelID{ID: "append-unique", Type: 1}
	unique := mustForChannel(t, eng, "append-unique:1", uniqueID)
	defer live.Close()
	defer unique.Close()
	if err := closed.Close(); err != nil {
		t.Fatalf("closed sibling Close(): %v", err)
	}

	results := StoreAppendBatch(context.Background(), []AppendBatchItem{
		{Store: closed, Records: []channel.Record{compatTestRecord(t, 9511, duplicateID.ID, "closed")}},
		{Store: live, Records: []channel.Record{compatTestRecord(t, 9512, duplicateID.ID, "live")}},
		{Store: unique, Records: []channel.Record{compatTestRecord(t, 9513, uniqueID.ID, "unique")}},
	})
	if len(results) != 3 || !errors.Is(results[0].Err, channel.ErrInvalidArgument) || !errors.Is(results[1].Err, channel.ErrInvalidArgument) {
		t.Fatalf("StoreAppendBatch() duplicate results = %+v, want both sibling items invalid", results)
	}
	if results[2].Err != nil {
		t.Fatalf("StoreAppendBatch() unique result = %+v, want success", results[2])
	}
	if leo, err := live.LEOWithError(); err != nil || leo != 0 {
		t.Fatalf("duplicate LEOWithError() = %d, %v, want 0, nil", leo, err)
	}
	if leo, err := unique.LEOWithError(); err != nil || leo != 1 {
		t.Fatalf("unique LEOWithError() = %d, %v, want 1, nil", leo, err)
	}
}

func TestStoreApplyFetchBatchRejectsDuplicateCanonicalEntry(t *testing.T) {
	eng := openCompatEngine(t)
	id := channel.ChannelID{ID: "apply-duplicate", Type: 1}
	first := mustForChannel(t, eng, "apply-duplicate:1", id)
	second := mustForChannel(t, eng, "apply-duplicate:1", id)
	defer first.Close()
	defer second.Close()
	results := StoreApplyFetchTrustedBatch(context.Background(), []ApplyFetchBatchItem{
		{Store: first, Request: channel.ApplyFetchStoreRequest{Records: []channel.Record{compatTestRecord(t, 9601, id.ID, "first")}}},
		{Store: second, Request: channel.ApplyFetchStoreRequest{Records: []channel.Record{compatTestRecord(t, 9602, id.ID, "second")}}},
	})
	if len(results) != 2 || !errors.Is(results[0].Err, channel.ErrInvalidArgument) || !errors.Is(results[1].Err, channel.ErrInvalidArgument) {
		t.Fatalf("StoreApplyFetchTrustedBatch() = %+v, want duplicate canonical errors", results)
	}
	if leo, err := first.LEOWithError(); err != nil || leo != 0 {
		t.Fatalf("LEOWithError() = %d, %v, want 0, nil", leo, err)
	}
}

func TestStoreApplyFetchBatchRejectsClosedAndLiveSiblingBeforeLeaseValidation(t *testing.T) {
	eng := openCompatEngine(t)
	duplicateID := channel.ChannelID{ID: "apply-closed-sibling", Type: 1}
	closed := mustForChannel(t, eng, "apply-closed-sibling:1", duplicateID)
	live := mustForChannel(t, eng, "apply-closed-sibling:1", duplicateID)
	uniqueID := channel.ChannelID{ID: "apply-unique", Type: 1}
	unique := mustForChannel(t, eng, "apply-unique:1", uniqueID)
	defer live.Close()
	defer unique.Close()
	if err := closed.Close(); err != nil {
		t.Fatalf("closed sibling Close(): %v", err)
	}

	results := StoreApplyFetchTrustedBatch(context.Background(), []ApplyFetchBatchItem{
		{Store: closed, Request: channel.ApplyFetchStoreRequest{Records: []channel.Record{compatTestRecord(t, 9611, duplicateID.ID, "closed")}}},
		{Store: live, Request: channel.ApplyFetchStoreRequest{Records: []channel.Record{compatTestRecord(t, 9612, duplicateID.ID, "live")}}},
		{Store: unique, Request: channel.ApplyFetchStoreRequest{Records: []channel.Record{compatTestRecord(t, 9613, uniqueID.ID, "unique")}}},
	})
	if len(results) != 3 || !errors.Is(results[0].Err, channel.ErrInvalidArgument) || !errors.Is(results[1].Err, channel.ErrInvalidArgument) {
		t.Fatalf("StoreApplyFetchTrustedBatch() duplicate results = %+v, want both sibling items invalid", results)
	}
	if results[2].Err != nil {
		t.Fatalf("StoreApplyFetchTrustedBatch() unique result = %+v, want success", results[2])
	}
	if leo, err := live.LEOWithError(); err != nil || leo != 0 {
		t.Fatalf("duplicate LEOWithError() = %d, %v, want 0, nil", leo, err)
	}
	if leo, err := unique.LEOWithError(); err != nil || leo != 1 {
		t.Fatalf("unique LEOWithError() = %d, %v, want 1, nil", leo, err)
	}
}

func TestStoreAppendBatchOppositeEngineOrderDoesNotHoldCrossEngineLocks(t *testing.T) {
	engineA := openCompatEngine(t)
	engineB := openCompatEngine(t)
	id := channel.ChannelID{ID: "opposite-append", Type: 1}
	storeA := mustForChannel(t, engineA, "opposite-append:1", id)
	storeB := mustForChannel(t, engineB, "opposite-append:1", id)
	defer storeA.Close()
	defer storeB.Close()

	entered := make(chan string, 4)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseCommits := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseCommits()
	blockCommit := func(name string) func(*engine.Batch) error {
		return func(batch *engine.Batch) error {
			entered <- name
			<-release
			return batch.Commit(false)
		}
	}
	engineA.committer.SetCommitFunc(blockCommit("a"))
	engineB.committer.SetCommitFunc(blockCommit("b"))

	start := make(chan struct{})
	completed := make(chan []AppendBatchResult, 2)
	itemsAB := []AppendBatchItem{
		{Store: storeA, Records: []channel.Record{compatTestRecord(t, 9701, id.ID, "append-a-1")}},
		{Store: storeB, Records: []channel.Record{compatTestRecord(t, 9702, id.ID, "append-b-1")}},
	}
	itemsBA := []AppendBatchItem{
		{Store: storeB, Records: []channel.Record{compatTestRecord(t, 9703, id.ID, "append-b-2")}},
		{Store: storeA, Records: []channel.Record{compatTestRecord(t, 9704, id.ID, "append-a-2")}},
	}
	go func() {
		<-start
		completed <- StoreAppendBatch(context.Background(), itemsAB)
	}()
	go func() {
		<-start
		completed <- StoreAppendBatch(context.Background(), itemsBA)
	}()
	close(start)

	starts := waitEngineCommitStarts(t, entered, 2)
	if starts["a"] != 1 || starts["b"] != 1 {
		t.Fatalf("first physical commits = %v, want one per engine", starts)
	}
	releaseCommits()
	for i := 0; i < 2; i++ {
		select {
		case results := <-completed:
			assertAppendBatchSucceeded(t, results)
		case <-time.After(2 * time.Second):
			t.Fatal("opposite-order append batches deadlocked")
		}
	}
	for name, store := range map[string]*ChannelStore{"a": storeA, "b": storeB} {
		if leo, err := store.LEOWithError(); err != nil || leo != 2 {
			t.Fatalf("engine %s LEOWithError() = %d, %v, want 2, nil", name, leo, err)
		}
	}
}

func TestStoreAppendBatchAdmittedCancellationDoesNotEnterNextEngine(t *testing.T) {
	engineA := openCompatEngine(t)
	engineB := openCompatEngine(t)
	id := channel.ChannelID{ID: "cancel-append", Type: 1}
	storeA := mustForChannel(t, engineA, "cancel-append:1", id)
	storeB := mustForChannel(t, engineB, "cancel-append:1", id)
	defer storeA.Close()
	defer storeB.Close()

	entered := make(chan string, 4)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseCommits := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseCommits()
	blockCommit := func(name string) func(*engine.Batch) error {
		return func(batch *engine.Batch) error {
			entered <- name
			<-release
			return batch.Commit(false)
		}
	}
	engineA.committer.SetCommitFunc(blockCommit("a"))
	engineB.committer.SetCommitFunc(blockCommit("b"))

	ctxAB, cancelAB := context.WithCancel(context.Background())
	ctxBA, cancelBA := context.WithCancel(context.Background())
	completed := make(chan []AppendBatchResult, 2)
	itemsAB := []AppendBatchItem{
		{Store: storeA, Records: []channel.Record{compatTestRecord(t, 9751, id.ID, "cancel-a-1")}},
		{Store: storeB, Records: []channel.Record{compatTestRecord(t, 9752, id.ID, "cancel-b-1")}},
	}
	itemsBA := []AppendBatchItem{
		{Store: storeB, Records: []channel.Record{compatTestRecord(t, 9753, id.ID, "cancel-b-2")}},
		{Store: storeA, Records: []channel.Record{compatTestRecord(t, 9754, id.ID, "cancel-a-2")}},
	}
	go func() {
		completed <- StoreAppendBatch(ctxAB, itemsAB)
	}()
	go func() {
		completed <- StoreAppendBatch(ctxBA, itemsBA)
	}()
	starts := waitEngineCommitStarts(t, entered, 2)
	if starts["a"] != 1 || starts["b"] != 1 {
		t.Fatalf("first physical commits = %v, want one per engine", starts)
	}
	cancelAB()
	cancelBA()
	for i := 0; i < 2; i++ {
		select {
		case results := <-completed:
			for index, result := range results {
				if !errors.Is(result.Err, context.Canceled) {
					t.Fatalf("append result[%d] = %+v, want context canceled", index, result)
				}
			}
		case <-time.After(2 * time.Second):
			t.Fatal("canceled append batch entered the next Engine lock domain")
		}
	}
	releaseCommits()
	waitForRegistryCondition(t, func() bool {
		return engineA.db.registry.snapshot().backgroundPins == 0 && engineB.db.registry.snapshot().backgroundPins == 0
	}, "canceled append commit finalization")
}

func TestStoreApplyFetchBatchOppositeEngineOrderDoesNotHoldCrossEngineLocks(t *testing.T) {
	engineA := openCompatEngine(t)
	engineB := openCompatEngine(t)
	id := channel.ChannelID{ID: "opposite-apply", Type: 1}
	storeA := mustForChannel(t, engineA, "opposite-apply:1", id)
	storeB := mustForChannel(t, engineB, "opposite-apply:1", id)
	defer storeA.Close()
	defer storeB.Close()

	entered := make(chan string, 4)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseCommits := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseCommits()
	blockCommit := func(name string) func(*engine.Batch) error {
		return func(batch *engine.Batch) error {
			entered <- name
			<-release
			return batch.Commit(false)
		}
	}
	engineA.committer.SetCommitFunc(blockCommit("a"))
	engineB.committer.SetCommitFunc(blockCommit("b"))
	checkpoint := func() *channel.Checkpoint { return &channel.Checkpoint{Epoch: 1} }

	start := make(chan struct{})
	completed := make(chan []ApplyFetchBatchResult, 2)
	itemsAB := []ApplyFetchBatchItem{
		{Store: storeA, Request: channel.ApplyFetchStoreRequest{Records: []channel.Record{compatTestRecord(t, 9801, id.ID, "apply-a-1")}, Checkpoint: checkpoint()}},
		{Store: storeB, Request: channel.ApplyFetchStoreRequest{Records: []channel.Record{compatTestRecord(t, 9802, id.ID, "apply-b-1")}, Checkpoint: checkpoint()}},
	}
	itemsBA := []ApplyFetchBatchItem{
		{Store: storeB, Request: channel.ApplyFetchStoreRequest{Records: []channel.Record{compatTestRecord(t, 9803, id.ID, "apply-b-2")}, Checkpoint: checkpoint()}},
		{Store: storeA, Request: channel.ApplyFetchStoreRequest{Records: []channel.Record{compatTestRecord(t, 9804, id.ID, "apply-a-2")}, Checkpoint: checkpoint()}},
	}
	go func() {
		<-start
		completed <- StoreApplyFetchTrustedBatch(context.Background(), itemsAB)
	}()
	go func() {
		<-start
		completed <- StoreApplyFetchTrustedBatch(context.Background(), itemsBA)
	}()
	close(start)

	starts := waitEngineCommitStarts(t, entered, 2)
	if starts["a"] != 1 || starts["b"] != 1 {
		t.Fatalf("first physical commits = %v, want one per engine", starts)
	}
	releaseCommits()
	for i := 0; i < 2; i++ {
		select {
		case results := <-completed:
			for index, result := range results {
				if result.Err != nil {
					t.Fatalf("apply result[%d] = %+v", index, result)
				}
			}
		case <-time.After(2 * time.Second):
			t.Fatal("opposite-order apply batches deadlocked")
		}
	}
	for name, store := range map[string]*ChannelStore{"a": storeA, "b": storeB} {
		if leo, err := store.LEOWithError(); err != nil || leo != 2 {
			t.Fatalf("engine %s LEOWithError() = %d, %v, want 2, nil", name, leo, err)
		}
	}
}

func TestStoreApplyFetchBatchAdmittedCancellationDoesNotEnterNextEngine(t *testing.T) {
	engineA := openCompatEngine(t)
	engineB := openCompatEngine(t)
	id := channel.ChannelID{ID: "cancel-apply", Type: 1}
	storeA := mustForChannel(t, engineA, "cancel-apply:1", id)
	storeB := mustForChannel(t, engineB, "cancel-apply:1", id)
	defer storeA.Close()
	defer storeB.Close()

	entered := make(chan string, 4)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseCommits := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseCommits()
	blockCommit := func(name string) func(*engine.Batch) error {
		return func(batch *engine.Batch) error {
			entered <- name
			<-release
			return batch.Commit(false)
		}
	}
	engineA.committer.SetCommitFunc(blockCommit("a"))
	engineB.committer.SetCommitFunc(blockCommit("b"))

	ctxAB, cancelAB := context.WithCancel(context.Background())
	ctxBA, cancelBA := context.WithCancel(context.Background())
	completed := make(chan []ApplyFetchBatchResult, 2)
	itemsAB := []ApplyFetchBatchItem{
		{Store: storeA, Request: channel.ApplyFetchStoreRequest{Records: []channel.Record{compatTestRecord(t, 9851, id.ID, "cancel-a-1")}}},
		{Store: storeB, Request: channel.ApplyFetchStoreRequest{Records: []channel.Record{compatTestRecord(t, 9852, id.ID, "cancel-b-1")}}},
	}
	itemsBA := []ApplyFetchBatchItem{
		{Store: storeB, Request: channel.ApplyFetchStoreRequest{Records: []channel.Record{compatTestRecord(t, 9853, id.ID, "cancel-b-2")}}},
		{Store: storeA, Request: channel.ApplyFetchStoreRequest{Records: []channel.Record{compatTestRecord(t, 9854, id.ID, "cancel-a-2")}}},
	}
	go func() {
		completed <- StoreApplyFetchTrustedBatch(ctxAB, itemsAB)
	}()
	go func() {
		completed <- StoreApplyFetchTrustedBatch(ctxBA, itemsBA)
	}()
	starts := waitEngineCommitStarts(t, entered, 2)
	if starts["a"] != 1 || starts["b"] != 1 {
		t.Fatalf("first physical commits = %v, want one per engine", starts)
	}
	cancelAB()
	cancelBA()
	for i := 0; i < 2; i++ {
		select {
		case results := <-completed:
			for index, result := range results {
				if !errors.Is(result.Err, context.Canceled) {
					t.Fatalf("apply result[%d] = %+v, want context canceled", index, result)
				}
			}
		case <-time.After(2 * time.Second):
			t.Fatal("canceled apply batch entered the next Engine lock domain")
		}
	}
	releaseCommits()
	waitForRegistryCondition(t, func() bool {
		return engineA.db.registry.snapshot().backgroundPins == 0 && engineB.db.registry.snapshot().backgroundPins == 0
	}, "canceled apply commit finalization")
}

func TestStoreCheckpointHWMonotonicPreservesFieldsAndNeverRegresses(t *testing.T) {
	eng := openCompatEngine(t)
	store := mustForChannel(t, eng, "checkpoint-rmw:1", channel.ChannelID{ID: "checkpoint-rmw", Type: 1})
	defer store.Close()
	if err := store.StoreCheckpoint(channel.Checkpoint{Epoch: 7, LogStartOffset: 2, HW: 5}); err != nil {
		t.Fatalf("StoreCheckpoint(): %v", err)
	}

	start := make(chan struct{})
	values := []uint64{6, 10, 8, 9, 7}
	errs := make(chan error, len(values))
	var wg sync.WaitGroup
	for _, hw := range values {
		hw := hw
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- store.StoreCheckpointHWMonotonic(context.Background(), hw)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("StoreCheckpointHWMonotonic(): %v", err)
		}
	}
	checkpoint, err := store.LoadCheckpoint()
	if err != nil {
		t.Fatalf("LoadCheckpoint(): %v", err)
	}
	if checkpoint != (channel.Checkpoint{Epoch: 7, LogStartOffset: 2, HW: 10}) {
		t.Fatalf("checkpoint = %+v, want preserved fields and HW 10", checkpoint)
	}
}

func TestStoreCheckpointHWMonotonicInitializesMissingCheckpointAtZero(t *testing.T) {
	eng := openCompatEngine(t)
	store := mustForChannel(t, eng, "checkpoint-zero:1", channel.ChannelID{ID: "checkpoint-zero", Type: 1})
	defer store.Close()

	if err := store.StoreCheckpointHWMonotonic(context.Background(), 0); err != nil {
		t.Fatalf("StoreCheckpointHWMonotonic(0): %v", err)
	}
	checkpoint, err := store.LoadCheckpoint()
	if err != nil {
		t.Fatalf("LoadCheckpoint(): %v", err)
	}
	if checkpoint != (channel.Checkpoint{}) {
		t.Fatalf("checkpoint = %+v, want persisted zero checkpoint", checkpoint)
	}
}

func TestStoreCheckpointHWMonotonicDistinctLeasesInitializeToMaximum(t *testing.T) {
	eng := openCompatEngine(t)
	id := channel.ChannelID{ID: "checkpoint-distinct-leases", Type: 1}
	first := mustForChannel(t, eng, "checkpoint-distinct-leases:1", id)
	second := mustForChannel(t, eng, "checkpoint-distinct-leases:1", id)
	defer first.Close()
	defer second.Close()

	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() {
		<-start
		errs <- first.StoreCheckpointHWMonotonic(context.Background(), 12)
	}()
	go func() {
		<-start
		errs <- second.StoreCheckpointHWMonotonic(context.Background(), 4)
	}()
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("StoreCheckpointHWMonotonic(): %v", err)
		}
	}
	checkpoint, err := first.LoadCheckpoint()
	if err != nil {
		t.Fatalf("LoadCheckpoint(): %v", err)
	}
	if checkpoint.HW != 12 {
		t.Fatalf("checkpoint HW = %d, want 12", checkpoint.HW)
	}
}

func TestEngineCloseDetachesOutstandingLeaseAndIsIdempotent(t *testing.T) {
	eng, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	store := mustForChannel(t, eng, "engine-close:1", channel.ChannelID{ID: "engine-close", Type: 1})
	if err := eng.Close(); err != nil {
		t.Fatalf("first Engine.Close(): %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("second Engine.Close(): %v", err)
	}
	if _, err := store.LEOWithError(); !errors.Is(err, channel.ErrClosed) {
		t.Fatalf("outstanding store LEOWithError() err = %v, want %v", err, channel.ErrClosed)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("detached store Close(): %v", err)
	}
}

func TestEngineListChannelsPageMapsClosedDatabaseError(t *testing.T) {
	eng := openCompatEngine(t)
	if err := eng.Close(); err != nil {
		t.Fatalf("Engine.Close(): %v", err)
	}
	if _, _, _, err := eng.ListChannelsPage(context.Background(), "", 10); !errors.Is(err, channel.ErrClosed) {
		t.Fatalf("ListChannelsPage() err = %v, want %v", err, channel.ErrClosed)
	}
}

func TestEngineMetricsSnapshotConcurrentClose(t *testing.T) {
	eng, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}

	const readers = 8
	start := make(chan struct{})
	ready := make(chan struct{}, readers)
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ready <- struct{}{}
			for j := 0; j < 2000; j++ {
				_ = eng.MetricsSnapshot()
			}
		}()
	}
	close(start)
	for i := 0; i < readers; i++ {
		<-ready
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	wg.Wait()
}

func openCompatEngine(t *testing.T) *Engine {
	t.Helper()
	eng, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() {
		if err := eng.Close(); err != nil {
			t.Fatalf("Engine.Close(): %v", err)
		}
	})
	return eng
}

func mustForChannel(t testing.TB, eng *Engine, key channel.ChannelKey, id channel.ChannelID) *ChannelStore {
	t.Helper()
	store, err := eng.ForChannel(key, id)
	if err != nil {
		t.Fatalf("ForChannel(%q): %v", key, err)
	}
	return store
}

func waitSignal(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func waitResult(t *testing.T, ch <-chan error, name string) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
		return nil
	}
}

func waitEngineCommitStarts(t *testing.T, entered <-chan string, count int) map[string]int {
	t.Helper()
	starts := make(map[string]int, count)
	for i := 0; i < count; i++ {
		select {
		case name := <-entered:
			starts[name]++
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for physical commit %d of %d", i+1, count)
		}
	}
	return starts
}

func assertAppendBatchSucceeded(t *testing.T, results []AppendBatchResult) {
	t.Helper()
	for index, result := range results {
		if result.Err != nil {
			t.Fatalf("append result[%d] = %+v", index, result)
		}
	}
}
