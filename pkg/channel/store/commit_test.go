package store

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/vfs"
)

func TestCommitCoordinatorBatchesMultipleGroupsIntoSinglePebbleSync(t *testing.T) {
	engine, fs := openCountingCommitCoordinatorTestEngine(t)
	stores := openTestChannelStoresOnEngine(t, engine, "group-1", "group-2")
	engine.commitCoordinator().flushWindow = 5 * time.Millisecond

	before := fs.syncCount.Load()

	baseCh := make(chan uint64, 2)
	errCh := make(chan error, 2)
	ready := make(chan struct{}, len(stores))
	start := make(chan struct{})
	for _, st := range stores {
		go func(st *ChannelStore) {
			ready <- struct{}{}
			<-start
			payload := mustEncodeStoreMessage(t, channel.Message{MessageID: 1, ClientMsgNo: "commit", FromUID: "u1", ChannelID: st.id.ID, ChannelType: st.id.Type, Payload: []byte(st.id.ID)})
			base, err := st.Append([]channel.Record{{Payload: payload, SizeBytes: len(payload)}})
			if err == nil {
				baseCh <- base
			}
			errCh <- err
		}(st)
	}
	for range stores {
		<-ready
	}
	close(start)

	for range 2 {
		if err := <-errCh; err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	for range 2 {
		if base := <-baseCh; base != 0 {
			t.Fatalf("Append() base = %d, want 0", base)
		}
	}

	after := fs.syncCount.Load()
	if got := after - before; got != 1 {
		t.Fatalf("sync count delta = %d, want 1", got)
	}
}

func TestCommitCoordinatorDoesNotPublishBeforeSyncCompletes(t *testing.T) {
	engine, fs := openBlockingSyncTestEngine(t)
	coordinator := engine.commitCoordinator()
	coordinator.flushWindow = 0

	published := make(chan struct{})
	done := make(chan error, 1)

	fs.enableNextSyncBlock()
	go func() {
		done <- coordinator.submit(commitRequest{
			channelKey: "group-1",
			build: func(batch *pebble.Batch) error {
				return batch.Set(
					encodeCheckpointKey("group-1"),
					encodeCheckpoint(channel.Checkpoint{Epoch: 3, HW: 1}),
					pebble.NoSync,
				)
			},
			publish: func() error {
				close(published)
				return nil
			},
		})
	}()

	select {
	case <-fs.syncStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for commit to reach durable sync")
	}

	select {
	case <-published:
		close(fs.syncContinue)
		<-done
		t.Fatal("publish callback ran before sync completed")
	case <-time.After(200 * time.Millisecond):
	}

	close(fs.syncContinue)
	if err := <-done; err != nil {
		t.Fatalf("submit() error = %v", err)
	}

	select {
	case <-published:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for publish callback after sync")
	}
}

func TestCommitCoordinatorFanoutsBatchFailureToAllWaiters(t *testing.T) {
	engine, _ := openCountingCommitCoordinatorTestEngine(t)
	coordinator := engine.commitCoordinator()
	coordinator.flushWindow = 5 * time.Millisecond
	coordinator.commit = func(*pebble.Batch) error {
		return errSyntheticSyncFailure
	}

	publishCalls := 0
	var publishMu sync.Mutex
	errCh := make(chan error, 2)
	start := make(chan struct{})
	for _, channelKey := range []channel.ChannelKey{"group-1", "group-2"} {
		go func(channelKey channel.ChannelKey) {
			<-start
			errCh <- coordinator.submit(commitRequest{
				channelKey: channelKey,
				build: func(batch *pebble.Batch) error {
					return batch.Set(
						encodeCheckpointKey(channelKey),
						encodeCheckpoint(channel.Checkpoint{Epoch: 3, HW: 1}),
						pebble.NoSync,
					)
				},
				publish: func() error {
					publishMu.Lock()
					publishCalls++
					publishMu.Unlock()
					return nil
				},
			})
		}(channelKey)
	}
	close(start)

	for range 2 {
		err := <-errCh
		if err == nil {
			t.Fatal("expected sync failure to fan out to all waiters")
		}
		if !errors.Is(err, errSyntheticSyncFailure) {
			t.Fatalf("submit() error = %v, want synthetic sync failure", err)
		}
	}

	publishMu.Lock()
	defer publishMu.Unlock()
	if publishCalls != 0 {
		t.Fatalf("publish callbacks = %d, want 0", publishCalls)
	}
}

func TestCommitCoordinatorCollectBatchStopsDrainingBufferedRequestsWhenClosed(t *testing.T) {
	engine, _ := openCountingCommitCoordinatorTestEngine(t)

	coordinator := &commitCoordinator{
		db:           engine.db,
		flushWindow:  0,
		requests:     make(chan commitRequest, 2),
		stopAcceptCh: make(chan struct{}),
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}

	coordinator.requests <- commitRequest{channelKey: "group-2", build: func(*pebble.Batch) error { return nil }}
	close(coordinator.stopAcceptCh)

	batch := coordinator.collectBatch(commitRequest{channelKey: "group-1", build: func(*pebble.Batch) error { return nil }})
	if !batch.closed {
		t.Fatal("collectBatch() should mark batch closed when shutdown has started")
	}
	if got := len(batch.requests); got != 1 {
		t.Fatalf("len(batch.requests) = %d, want 1", got)
	}
	if got := len(coordinator.requests); got != 1 {
		t.Fatalf("len(coordinator.requests) = %d, want 1 buffered request left for shutdown fanout", got)
	}
}

func TestCommitCoordinatorSubmitRejectsAfterCloseStarts(t *testing.T) {
	engine, _ := openCountingCommitCoordinatorTestEngine(t)

	coordinator := &commitCoordinator{
		db:           engine.db,
		requests:     make(chan commitRequest, 128),
		stopAcceptCh: make(chan struct{}),
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
	coordinator.stopAccepting()

	var accepted atomic.Int64
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for {
			select {
			case req := <-coordinator.requests:
				accepted.Add(1)
				req.done <- nil
			case <-time.After(10 * time.Millisecond):
				return
			}
		}
	}()

	for i := 0; i < 128; i++ {
		err := coordinator.submit(commitRequest{
			channelKey: "group-1",
			build:      func(*pebble.Batch) error { return nil },
		})
		if !errors.Is(err, channel.ErrInvalidArgument) {
			t.Fatalf("submit() error = %v, want invalid argument after shutdown begins", err)
		}
	}

	<-drainDone
	if got := accepted.Load(); got != 0 {
		t.Fatalf("accepted requests after shutdown = %d, want 0", got)
	}
}

func TestCommitCoordinatorAwaitRequestResultPrefersPublishedResultAfterDoneCloses(t *testing.T) {
	coordinator := &commitCoordinator{doneCh: make(chan struct{})}
	reqDone := make(chan error, 1)
	reqDone <- nil
	close(coordinator.doneCh)

	if err := coordinator.awaitRequestResult(reqDone); err != nil {
		t.Fatalf("awaitRequestResult() error = %v, want nil", err)
	}
}

func TestCommitCoordinatorAwaitRequestResultReturnsShutdownErrorWithoutPublishedResult(t *testing.T) {
	coordinator := &commitCoordinator{doneCh: make(chan struct{})}
	close(coordinator.doneCh)

	if err := coordinator.awaitRequestResult(make(chan error, 1)); !errors.Is(err, channel.ErrInvalidArgument) {
		t.Fatalf("awaitRequestResult() error = %v, want invalid argument", err)
	}
}

func TestCommitCoordinatorPublishesIfCloseStartsDuringSync(t *testing.T) {
	engine, _ := openCountingCommitCoordinatorTestEngine(t)
	coordinator := engine.commitCoordinator()
	coordinator.flushWindow = 0

	commitStarted := make(chan struct{})
	releaseCommit := make(chan struct{})
	publishStarted := make(chan struct{})
	releasePublish := make(chan struct{})
	closeDone := make(chan struct{})
	done := make(chan error, 1)
	t.Cleanup(func() {
		closeOnce(releaseCommit)
		closeOnce(releasePublish)
	})

	coordinator.commit = func(*pebble.Batch) error {
		close(commitStarted)
		<-releaseCommit
		return nil
	}

	go func() {
		done <- coordinator.submit(commitRequest{
			channelKey: "group-1",
			build: func(batch *pebble.Batch) error {
				return batch.Set(
					encodeCheckpointKey("group-1"),
					encodeCheckpoint(channel.Checkpoint{Epoch: 3, HW: 1}),
					pebble.NoSync,
				)
			},
			publish: func() error {
				close(publishStarted)
				<-releasePublish
				return nil
			},
		})
	}()

	select {
	case <-commitStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for sync to start")
	}

	go func() {
		coordinator.close()
		close(closeDone)
	}()

	select {
	case <-coordinator.stopAcceptCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for coordinator to stop accepting requests")
	}

	select {
	case <-coordinator.stopCh:
		t.Fatal("close fully started before the in-flight batch finished")
	case <-time.After(200 * time.Millisecond):
	}

	close(releaseCommit)

	select {
	case <-publishStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for publish after sync release")
	}

	select {
	case <-coordinator.stopCh:
		t.Fatal("close fully started before publish completed")
	case <-time.After(200 * time.Millisecond):
	}

	close(releasePublish)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("submit() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for submit after publish release")
	}

	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for coordinator close")
	}
}

func TestCommitCoordinatorDoesNotStartCloseWhilePublishInFlight(t *testing.T) {
	engine, _ := openCountingCommitCoordinatorTestEngine(t)
	coordinator := engine.commitCoordinator()
	coordinator.flushWindow = 0

	publishStarted := make(chan struct{})
	releasePublish := make(chan struct{})
	closeDone := make(chan struct{})
	done := make(chan error, 1)
	t.Cleanup(func() {
		closeOnce(releasePublish)
	})

	go func() {
		done <- coordinator.submit(commitRequest{
			channelKey: "group-1",
			build: func(batch *pebble.Batch) error {
				return batch.Set(
					encodeCheckpointKey("group-1"),
					encodeCheckpoint(channel.Checkpoint{Epoch: 3, HW: 1}),
					pebble.NoSync,
				)
			},
			publish: func() error {
				close(publishStarted)
				<-releasePublish
				return nil
			},
		})
	}()

	select {
	case <-publishStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for publish to start")
	}

	go func() {
		coordinator.close()
		close(closeDone)
	}()

	select {
	case <-coordinator.stopCh:
		t.Fatal("close started while publish was still in flight")
	case <-time.After(200 * time.Millisecond):
	}

	closeOnce(releasePublish)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("submit() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for submit after publish release")
	}

	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for coordinator close after publish release")
	}
}

func openCountingCommitCoordinatorTestEngine(tb testing.TB) (*Engine, *countingFS) {
	tb.Helper()

	fs := newCountingFS(vfs.NewMem())
	pdb, err := pebble.Open("test", &pebble.Options{FS: fs})
	if err != nil {
		tb.Fatalf("pebble.Open() error = %v", err)
	}

	engine := &Engine{
		db:     pdb,
		stores: make(map[channel.ChannelKey]*ChannelStore),
	}
	tb.Cleanup(func() {
		if err := engine.Close(); err != nil {
			tb.Fatalf("Close() error = %v", err)
		}
	})
	return engine, fs
}

func openBlockingSyncTestEngine(tb testing.TB) (*Engine, *blockingSyncFS) {
	tb.Helper()

	dir := tb.TempDir()
	fs := newBlockingSyncFS(vfs.Default)
	pdb, err := pebble.Open(dir, &pebble.Options{FS: fs})
	if err != nil {
		tb.Fatalf("pebble.Open() error = %v", err)
	}

	engine := &Engine{
		db:     pdb,
		stores: make(map[channel.ChannelKey]*ChannelStore),
	}
	tb.Cleanup(func() {
		if err := engine.Close(); err != nil {
			tb.Fatalf("Close() error = %v", err)
		}
	})
	return engine, fs
}

var errSyntheticSyncFailure = errors.New("synthetic sync failure")

type countingFS struct {
	vfs.FS
	syncCount atomic.Int64
}

func newCountingFS(base vfs.FS) *countingFS {
	return &countingFS{FS: base}
}

func (fs *countingFS) Create(name string, category vfs.DiskWriteCategory) (vfs.File, error) {
	file, err := fs.FS.Create(name, category)
	if err != nil {
		return nil, err
	}
	return &countingFile{File: file, syncCount: &fs.syncCount}, nil
}

func (fs *countingFS) Open(name string, opts ...vfs.OpenOption) (vfs.File, error) {
	file, err := fs.FS.Open(name, opts...)
	if err != nil {
		return nil, err
	}
	return &countingFile{File: file, syncCount: &fs.syncCount}, nil
}

func (fs *countingFS) OpenReadWrite(name string, category vfs.DiskWriteCategory, opts ...vfs.OpenOption) (vfs.File, error) {
	file, err := fs.FS.OpenReadWrite(name, category, opts...)
	if err != nil {
		return nil, err
	}
	return &countingFile{File: file, syncCount: &fs.syncCount}, nil
}

func (fs *countingFS) OpenDir(name string) (vfs.File, error) {
	file, err := fs.FS.OpenDir(name)
	if err != nil {
		return nil, err
	}
	return &countingFile{File: file, syncCount: &fs.syncCount}, nil
}

func (fs *countingFS) ReuseForWrite(oldname, newname string, category vfs.DiskWriteCategory) (vfs.File, error) {
	file, err := fs.FS.ReuseForWrite(oldname, newname, category)
	if err != nil {
		return nil, err
	}
	return &countingFile{File: file, syncCount: &fs.syncCount}, nil
}

type countingFile struct {
	vfs.File
	syncCount *atomic.Int64
}

func (f *countingFile) Sync() error {
	f.syncCount.Add(1)
	return f.File.Sync()
}

func (f *countingFile) SyncData() error {
	f.syncCount.Add(1)
	return f.File.SyncData()
}

func (f *countingFile) SyncTo(length int64) (bool, error) {
	f.syncCount.Add(1)
	return f.File.SyncTo(length)
}

type blockingSyncFS struct {
	vfs.FS

	enabled      atomic.Bool
	syncStarted  chan struct{}
	syncContinue chan struct{}
	once         sync.Once
}

func newBlockingSyncFS(base vfs.FS) *blockingSyncFS {
	return &blockingSyncFS{
		FS:           base,
		syncStarted:  make(chan struct{}),
		syncContinue: make(chan struct{}),
	}
}

func (fs *blockingSyncFS) enableNextSyncBlock() {
	fs.enabled.Store(true)
}

func (fs *blockingSyncFS) Create(name string, category vfs.DiskWriteCategory) (vfs.File, error) {
	file, err := fs.FS.Create(name, category)
	if err != nil {
		return nil, err
	}
	return fs.wrap(file), nil
}

func (fs *blockingSyncFS) Open(name string, opts ...vfs.OpenOption) (vfs.File, error) {
	file, err := fs.FS.Open(name, opts...)
	if err != nil {
		return nil, err
	}
	return fs.wrap(file), nil
}

func (fs *blockingSyncFS) OpenReadWrite(name string, category vfs.DiskWriteCategory, opts ...vfs.OpenOption) (vfs.File, error) {
	file, err := fs.FS.OpenReadWrite(name, category, opts...)
	if err != nil {
		return nil, err
	}
	return fs.wrap(file), nil
}

func (fs *blockingSyncFS) OpenDir(name string) (vfs.File, error) {
	file, err := fs.FS.OpenDir(name)
	if err != nil {
		return nil, err
	}
	return fs.wrap(file), nil
}

func (fs *blockingSyncFS) ReuseForWrite(oldname, newname string, category vfs.DiskWriteCategory) (vfs.File, error) {
	file, err := fs.FS.ReuseForWrite(oldname, newname, category)
	if err != nil {
		return nil, err
	}
	return fs.wrap(file), nil
}

func (fs *blockingSyncFS) wrap(file vfs.File) vfs.File {
	if file == nil {
		return nil
	}
	return &blockingSyncFile{File: file, fs: fs}
}

func (fs *blockingSyncFS) maybeBlockSync() {
	if !fs.enabled.Load() {
		return
	}
	fs.once.Do(func() {
		close(fs.syncStarted)
		<-fs.syncContinue
	})
}

type blockingSyncFile struct {
	vfs.File
	fs *blockingSyncFS
}

func (f *blockingSyncFile) Sync() error {
	f.fs.maybeBlockSync()
	return f.File.Sync()
}

func (f *blockingSyncFile) SyncTo(length int64) (bool, error) {
	f.fs.maybeBlockSync()
	return f.File.SyncTo(length)
}

func (f *blockingSyncFile) SyncData() error {
	f.fs.maybeBlockSync()
	return f.File.SyncData()
}
