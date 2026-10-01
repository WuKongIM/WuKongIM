//go:build darwin && integration

package engine

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/cockroachdb/pebble/v2/vfs"
	"golang.org/x/sys/unix"
)

// Native integration exercises every file-opening path and RawConn's actual
// lifetime fence; isolated errno scripts cannot establish descriptor safety.
func TestDarwinFSNativeFiles(t *testing.T) {
	fs := &darwinFS{FS: vfs.Default}
	dir := t.TempDir()
	name := filepath.Join(dir, "data")
	for _, tc := range []struct {
		name string
		open func() (vfs.File, error)
	}{
		{"create", func() (vfs.File, error) { return fs.Create(name, vfs.WriteCategoryUnspecified) }},
		{"read", func() (vfs.File, error) { return fs.Open(name) }},
		{"read-write", func() (vfs.File, error) { return fs.OpenReadWrite(name, vfs.WriteCategoryUnspecified) }},
		{"reuse", func() (vfs.File, error) { return fs.ReuseForWrite(name, name+".reused", vfs.WriteCategoryUnspecified) }},
		{"directory", func() (vfs.File, error) { return fs.OpenDir(dir) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := tc.open()
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := f.(*darwinFile); !ok {
				t.Fatalf("native file not wrapped: %T", f)
			}
			if err := f.Sync(); err != nil {
				t.Fatal(err)
			}
			if err := f.SyncData(); err != nil {
				t.Fatal(err)
			}
			if full, err := f.SyncTo(17); err != nil || !full {
				t.Fatalf("SyncTo: %v/%v", full, err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			if full, err := f.SyncTo(17); full || !errors.Is(err, os.ErrClosed) {
				t.Fatalf("closed SyncTo: %v/%v", full, err)
			}
		})
	}
	if f, err := fs.Open(filepath.Join(dir, "missing")); f != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("open failure: %v/%v", f, err)
	}
}

func TestDarwinSyncPinsDescriptorAcrossClose(t *testing.T) {
	f, err := vfs.Default.Create(filepath.Join(t.TempDir(), "data"), vfs.WriteCategoryUnspecified)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	entered, release, result := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		result <- syncDarwinFile(f, "data", func(fd uintptr) error {
			close(entered)
			<-release
			var stat unix.Stat_t
			return unix.Fstat(int(fd), &stat)
		})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Control did not enter")
	}
	closed := make(chan error, 1)
	go func() { closed <- f.Close() }()
	conn, err := f.(interface {
		SyscallConn() (syscall.RawConn, error)
	}).SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := conn.Control(func(uintptr) {})
		if err != nil {
			break
		}
		if time.Now().After(deadline) {
			close(release)
			t.Fatalf("Close did not fence new operations: %v", err)
		}
		runtime.Gosched()
	}
	// Close may return before the pinned regular descriptor is destroyed. In
	// either order the callback must retain a valid fd until Control returns.
	close(release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sync did not join")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not join")
	}
	if err := wrapDarwinFile(f, "data").Sync(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed sync: %v", err)
	}
}

func TestDarwinEngineHealthAndReopen(t *testing.T) {
	for _, threshold := range []time.Duration{0, time.Second} {
		t.Run(threshold.String(), func(t *testing.T) {
			path := t.TempDir()
			db, err := Open(path, Options{DiskSlowThreshold: threshold})
			if err != nil {
				t.Fatal(err)
			}
			if db.diskHealth == nil {
				t.Fatal("engine does not own health checker")
			}
			b := db.NewBatch()
			if err := b.Set([]byte("k"), []byte("v")); err != nil {
				t.Fatal(err)
			}
			if err := b.Commit(true); err != nil {
				t.Fatal(err)
			}
			b.Close()
			if db.MetricsSnapshot().WALFsync.Count == 0 {
				t.Fatal("missing WAL sync metric")
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if db.diskHealth != nil {
				t.Fatal("health checker retained")
			}
			db, err = Open(path, Options{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if value, ok, err := db.Get([]byte("k")); err != nil || !ok || string(value) != "v" {
				t.Fatalf("reopen: %q/%v/%v", value, ok, err)
			}
		})
	}
}
