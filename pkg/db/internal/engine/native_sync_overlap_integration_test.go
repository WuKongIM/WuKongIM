//go:build integration

package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/vfs"
	"github.com/stretchr/testify/require"
)

const syncMechanismLimit = 64 << 10

// syncMechanismEvents records ordering, not a wall-clock throughput gate.
type syncMechanismEvents struct {
	mu       sync.Mutex
	start    time.Time
	rows     []syncMechanismEvent
	overflow bool
}
type syncMechanismEvent struct {
	Seq    int    `json:"seq"`
	Nanos  int64  `json:"elapsed_ns"`
	Kind   string `json:"kind"`
	Method string `json:"method,omitempty"`
	Path   string `json:"path,omitempty"`
	Bytes  int    `json:"bytes,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (e *syncMechanismEvents) add(kind, method, path string, size int, err error) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.rows) == 256 {
		e.overflow = true
		return 0
	}
	row := syncMechanismEvent{Seq: len(e.rows) + 1, Nanos: time.Since(e.start).Nanoseconds(), Kind: kind, Method: method, Path: path, Bytes: size}
	if err != nil {
		row.Error = err.Error()
	}
	e.rows = append(e.rows, row)
	return row.Seq
}

type syncMechanismPoint struct {
	method  string
	begin   int
	end     atomic.Int64
	release chan error
}

// syncMechanismFS changes only the test filesystem. Production Open has no new seam.
type syncMechanismFS struct {
	vfs.FS
	events    *syncMechanismEvents
	armed     atomic.Bool
	points    chan *syncMechanismPoint
	abort     chan struct{}
	abortOnce sync.Once
}

func (f *syncMechanismFS) wrap(name string, file vfs.File, err error) (vfs.File, error) {
	if err != nil || !strings.HasSuffix(name, ".log") {
		return file, err
	}
	return &syncMechanismFile{File: file, owner: f, path: name}, nil
}
func (f *syncMechanismFS) Create(name string, category vfs.DiskWriteCategory) (vfs.File, error) {
	file, err := f.FS.Create(name, category)
	return f.wrap(name, file, err)
}
func (f *syncMechanismFS) ReuseForWrite(old, name string, category vfs.DiskWriteCategory) (vfs.File, error) {
	file, err := f.FS.ReuseForWrite(old, name, category)
	return f.wrap(name, file, err)
}
func (f *syncMechanismFS) unblock() { f.armed.Store(false); f.abortOnce.Do(func() { close(f.abort) }) }

type syncMechanismFile struct {
	vfs.File
	owner *syncMechanismFS
	path  string
}

func (f *syncMechanismFile) Write(p []byte) (int, error) {
	f.owner.events.add("fs_begin", "Write", f.path, len(p), nil)
	n, err := f.File.Write(p)
	f.owner.events.add("fs_end", "Write", f.path, n, err)
	return n, err
}
func (f *syncMechanismFile) sync(method string, call func() error) error {
	begin := f.owner.events.add("fs_begin", method, f.path, 0, nil)
	var point *syncMechanismPoint
	var err error
	if f.owner.armed.Load() {
		point = &syncMechanismPoint{method: method, begin: begin, release: make(chan error, 1)}
		select {
		case f.owner.points <- point:
		case <-f.owner.abort:
		}
		select {
		case err = <-point.release:
		case <-f.owner.abort:
		}
	}
	if err == nil {
		err = call()
	}
	end := f.owner.events.add("fs_end", method, f.path, 0, err)
	if point != nil {
		point.end.Store(int64(end))
	}
	return err
}
func (f *syncMechanismFile) Sync() error     { return f.sync("Sync", f.File.Sync) }
func (f *syncMechanismFile) SyncData() error { return f.sync("SyncData", f.File.SyncData) }
func (f *syncMechanismFile) SyncTo(length int64) (bool, error) {
	f.owner.events.add("fs_begin", "SyncTo", f.path, int(length), nil)
	full, err := f.File.SyncTo(length)
	method := "SyncTo_partial"
	if full {
		method = "SyncTo_full"
	}
	f.owner.events.add("fs_end", method, f.path, int(length), err)
	return full, err
}

type syncMechanismResult struct {
	ID     string                  `json:"id"`
	Ack    int                     `json:"ack_event"`
	Stats  NativeCommitStats       `json:"engine_stats"`
	Native pebble.BatchCommitStats `json:"native_stats"`
	Err    error                   `json:"-"`
}
type syncMechanismFixture struct {
	db      *DB
	mem     *vfs.MemFS
	fs      *syncMechanismFS
	credits chan struct{}
	wg      sync.WaitGroup
	active  atomic.Int64
	peak    atomic.Int64
	closed  bool
}

func newSyncMechanismFixture(t *testing.T, capacity int) *syncMechanismFixture {
	t.Helper()
	mem := vfs.NewCrashableMem()
	events := &syncMechanismEvents{start: time.Now()}
	fs := &syncMechanismFS{FS: mem, events: events, points: make(chan *syncMechanismPoint, 4), abort: make(chan struct{})}
	opts, err := pebbleOptions(Options{}, nil)
	require.NoError(t, err)
	opts.FS = fs
	pdb, err := pebble.Open("/db", opts)
	require.NoError(t, err)
	f := &syncMechanismFixture{db: &DB{pdb: pdb}, mem: mem, fs: fs, credits: make(chan struct{}, capacity)}
	t.Cleanup(func() {
		f.fs.unblock()
		if !f.join(t) {
			return
		} // Never Close underneath a live native owner.
		if !f.closed {
			require.NoError(t, f.db.Close())
			f.closed = true
		}
	})
	fs.armed.Store(true)
	return f
}
func (f *syncMechanismFixture) join(t *testing.T) bool {
	t.Helper()
	done := make(chan struct{})
	go func() { f.wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(5 * time.Second):
		t.Error("native owners did not join after all gates opened")
		return false
	}
}
func (f *syncMechanismFixture) launch(t *testing.T, id string) <-chan syncMechanismResult {
	t.Helper()
	select {
	case f.credits <- struct{}{}:
	default:
		t.Fatal("fixture exceeded its fixed owner bound")
	}
	batch := f.db.NewBatch()
	require.NoError(t, batch.Set([]byte(id), bytes.Repeat([]byte(id), 8<<10)))
	done := make(chan syncMechanismResult, 1)
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		n := f.active.Add(1)
		for old := f.peak.Load(); n > old && !f.peak.CompareAndSwap(old, n); old = f.peak.Load() {
		}
		f.fs.events.add("commit_start", id, "", 0, nil)
		err := batch.Commit(true)
		result := syncMechanismResult{ID: id, Err: err, Stats: batch.CommitStats(), Native: batch.batch.CommitStats()}
		if err == nil {
			if os.Getenv("WK_TEST_ENGINE_SYNC_FATAL_HELPER") == "1" {
				fmt.Printf("commit_ack:%s\n", id)
			}
			result.Ack = f.fs.events.add("commit_ack", id, "", 0, nil)
		}
		if closeErr := batch.Close(); result.Err == nil {
			result.Err = closeErr
		}
		<-f.credits
		f.active.Add(-1)
		done <- result
	}()
	return done
}
func syncMechanismPointWait(t *testing.T, f *syncMechanismFixture) *syncMechanismPoint {
	t.Helper()
	select {
	case p := <-f.fs.points:
		require.Contains(t, []string{"Sync", "SyncData"}, p.method)
		return p
	case <-time.After(5 * time.Second):
		t.Fatal("no WAL sync method reached the gate")
		return nil
	}
}
func syncMechanismNoAck(t *testing.T, done <-chan syncMechanismResult) {
	t.Helper()
	select {
	case r := <-done:
		t.Fatalf("Commit(true) completed while its WAL sync was gated: %+v", r)
	default:
	}
}
func syncMechanismWait(ctx context.Context, done <-chan syncMechanismResult) (syncMechanismResult, error) {
	select {
	case r := <-done:
		return r, r.Err
	case <-ctx.Done():
		return syncMechanismResult{}, ctx.Err()
	}
}
func syncMechanismFinish(t *testing.T, done <-chan syncMechanismResult, point *syncMechanismPoint) syncMechanismResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := syncMechanismWait(ctx, done)
	require.NoError(t, err)
	require.Positive(t, point.end.Load())
	require.Greater(t, int64(r.Ack), point.end.Load(), "ACK must follow its successful underlying sync")
	require.True(t, r.Stats.Available)
	require.Positive(t, r.Stats.CommitWaitDuration)
	require.Equal(t, NativeCommitStats{Available: true, TotalDuration: r.Native.TotalDuration, SemaphoreWaitDuration: r.Native.SemaphoreWaitDuration, WALQueueWaitDuration: r.Native.WALQueueWaitDuration, MemTableWriteStallDuration: r.Native.MemTableWriteStallDuration, L0ReadAmpWriteStallDuration: r.Native.L0ReadAmpWriteStallDuration, WALRotationDuration: r.Native.WALRotationDuration, CommitWaitDuration: r.Native.CommitWaitDuration}, r.Stats)
	return r
}
func syncMechanismVisible(t *testing.T, f *syncMechanismFixture, key string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		_, found, err := f.db.Get([]byte(key))
		require.NoError(t, err)
		if found {
			f.fs.events.add("visible_before_durable_ack", key, "", 0, nil)
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("second independent batch did not enter native visibility while first sync waited")
		}
	}
}
func syncMechanismCrashRead(t *testing.T, f *syncMechanismFixture, expected map[string]bool) {
	t.Helper()
	clone := f.mem.CrashClone(vfs.CrashCloneCfg{})
	opts, err := pebbleOptions(Options{ReadOnly: true}, nil)
	require.NoError(t, err)
	opts.FS = clone
	pdb, err := pebble.Open("/db", opts)
	require.NoError(t, err)
	db := &DB{pdb: pdb}
	defer func() { require.NoError(t, db.Close()) }()
	for key, want := range expected {
		got, found, err := db.Get([]byte(key))
		require.NoError(t, err)
		require.Equal(t, want, found, "last-synced crash clone key %s", key)
		if found {
			require.Equal(t, bytes.Repeat([]byte(key), 8<<10), got)
		}
	}
	f.fs.events.add("crash_clone_verified", "", "", 0, nil)
}
func syncMechanismArtifact(t *testing.T, name string, report any) {
	t.Helper()
	raw, err := json.MarshalIndent(report, "", "  ")
	require.NoError(t, err)
	require.LessOrEqual(t, len(raw), syncMechanismLimit)
	hash := sha256.Sum256(raw)
	t.Logf("mechanism artifact %s sha256=%s bytes=%d\n%s", name, hex.EncodeToString(hash[:]), len(raw), raw)
	if dir := os.Getenv("WK_TEST_ENGINE_SYNC_ARTIFACT_DIR"); dir != "" {
		require.NoError(t, os.MkdirAll(dir, 0700))
		out, err := os.OpenFile(filepath.Join(dir, name+".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		require.NoError(t, err)
		_, err = out.Write(raw)
		require.NoError(t, err)
		require.NoError(t, out.Close())
	}
}

// The FS hook runs after Pebble takes its sync-waiter snapshot. A second batch
// submitted at that gate needs a later sync: this test does not promise sharing.
func TestNativeSyncGateIndependentOverlap(t *testing.T) {
	for _, test := range []struct {
		name                string
		overlap, cancelWait bool
	}{
		{name: "serial_owner"}, {name: "two_independent_owners", overlap: true}, {name: "canceled_waiter_retains_owner", overlap: true, cancelWait: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			capacity := 1
			if test.overlap {
				capacity = 2
			}
			f := newSyncMechanismFixture(t, capacity)
			a := f.launch(t, "a")
			first := syncMechanismPointWait(t, f)
			var b <-chan syncMechanismResult
			if test.overlap {
				b = f.launch(t, "b")
				syncMechanismVisible(t, f, "b") // Visibility is not a durability ACK.
				syncMechanismNoAck(t, b)
				require.EqualValues(t, 2, f.active.Load())
			}
			syncMechanismNoAck(t, a)
			if test.cancelWait {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				_, err := syncMechanismWait(ctx, a)
				require.ErrorIs(t, err, context.Canceled)
				require.EqualValues(t, 2, f.active.Load(), "caller cancellation does not release admitted native ownership")
			}
			syncMechanismCrashRead(t, f, map[string]bool{"a": false, "b": false})
			first.release <- nil
			ra := syncMechanismFinish(t, a, first)
			if !test.overlap {
				b = f.launch(t, "b")
			}
			second := syncMechanismPointWait(t, f)
			syncMechanismNoAck(t, b)
			syncMechanismCrashRead(t, f, map[string]bool{"a": true, "b": false})
			second.release <- nil
			rb := syncMechanismFinish(t, b, second)
			require.True(t, f.join(t))
			syncMechanismCrashRead(t, f, map[string]bool{"a": true, "b": true})
			require.LessOrEqual(t, f.peak.Load(), int64(capacity))
			f.fs.unblock()
			require.NoError(t, f.db.Close())
			f.closed = true
			f.fs.events.mu.Lock()
			overflow := f.fs.events.overflow
			rows := append([]syncMechanismEvent(nil), f.fs.events.rows...)
			f.fs.events.mu.Unlock()
			require.False(t, overflow)
			syncMechanismArtifact(t, test.name, map[string]any{"owners_joined_before_close": true, "owner_capacity": capacity, "owner_peak": f.peak.Load(), "sync_methods": []string{first.method, second.method}, "records": 2, "payload_bytes": 2 * (8 << 10), "commits": []syncMechanismResult{ra, rb}, "events": rows, "guaranteed_same_sync_or_throughput_improvement": false, "scope": "native engine ownership and durability only; not product coordinator semantics"})
		})
	}
}

// The pinned native DB treats a WAL sync error as fatal. Preserve that behavior
// in an isolated test process instead of returning through a no-op fatal logger.
func TestNativeSyncGateFatalHelper(t *testing.T) {
	if os.Getenv("WK_TEST_ENGINE_SYNC_FATAL_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	f := newSyncMechanismFixture(t, 2)
	a := f.launch(t, "a")
	first := syncMechanismPointWait(t, f)
	b := f.launch(t, "b")
	syncMechanismVisible(t, f, "b")
	syncMechanismNoAck(t, a)
	syncMechanismNoAck(t, b)
	fmt.Println("sync_error_injected_before_any_ack")
	first.release <- errors.New("test injected WAL sync failure")
	select {
	case r := <-a:
		fmt.Printf("unexpected_commit_ack_or_return: %+v\n", r)
		t.Fatal("native fatal unexpectedly returned")
	case r := <-b:
		fmt.Printf("unexpected_commit_ack_or_return: %+v\n", r)
		t.Fatal("native fatal unexpectedly returned")
	case <-time.After(5 * time.Second):
		t.Fatal("native fatal did not terminate")
	}
}

type syncMechanismOutput struct {
	mu sync.Mutex
	bytes.Buffer
	overflow bool
}

func (b *syncMechanismOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if len(p) > syncMechanismLimit-b.Len() {
		b.overflow = true
		p = p[:syncMechanismLimit-b.Len()]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}
func TestNativeSyncGateFatalNeverAcknowledges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	exe, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestNativeSyncGateFatalHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "WK_TEST_ENGINE_SYNC_FATAL_HELPER=1")
	cmd.WaitDelay = 2 * time.Second
	var output syncMechanismOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	err = cmd.Run()
	require.NoError(t, ctx.Err(), "fatal helper must exit itself, not be killed at its deadline")
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	require.Equal(t, 1, exit.ExitCode())
	require.False(t, output.overflow)
	text := output.String()
	require.Contains(t, text, "sync_error_injected_before_any_ack")
	require.Contains(t, text, "pebble: fatal commit error")
	require.Contains(t, text, "test injected WAL sync failure")
	require.NotContains(t, text, "unexpected_commit_ack_or_return")
	require.NotContains(t, text, "commit_ack:")
	syncMechanismArtifact(t, "native_sync_fatal", map[string]any{"exit_code": exit.ExitCode(), "process_wait_joined": true, "default_native_fatal_preserved": true, "ack_return_marker_absent": true, "combined_output": text, "output_cap_bytes": syncMechanismLimit, "deadline_seconds": 15})
}
