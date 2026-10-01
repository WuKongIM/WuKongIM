//go:build darwin

package engine

import (
	"errors"
	"os"
	"syscall"

	"github.com/cockroachdb/pebble/v2/vfs"
	"golang.org/x/sys/unix"
)

// platformFS retains Darwin's full-sync durability while releasing the Go
// scheduler during the blocking fcntl. The adapter stays below disk-health
// checking so WAL and other sync operations retain their existing observations.
func platformFS() vfs.FS { return &darwinFS{FS: vfs.Default} }

type darwinFS struct{ vfs.FS }

func (f *darwinFS) Unwrap() vfs.FS { return f.FS }

func (f *darwinFS) Create(name string, category vfs.DiskWriteCategory) (vfs.File, error) {
	file, err := f.FS.Create(name, category)
	if err != nil {
		return file, err
	}
	return wrapDarwinFile(file, name), nil
}

func (f *darwinFS) Open(name string, opts ...vfs.OpenOption) (vfs.File, error) {
	file, err := f.FS.Open(name, opts...)
	if err != nil {
		return file, err
	}
	return wrapDarwinFile(file, name), nil
}

func (f *darwinFS) OpenReadWrite(name string, category vfs.DiskWriteCategory, opts ...vfs.OpenOption) (vfs.File, error) {
	file, err := f.FS.OpenReadWrite(name, category, opts...)
	if err != nil {
		return file, err
	}
	return wrapDarwinFile(file, name), nil
}

func (f *darwinFS) ReuseForWrite(oldname, newname string, category vfs.DiskWriteCategory) (vfs.File, error) {
	file, err := f.FS.ReuseForWrite(oldname, newname, category)
	if err != nil {
		return file, err
	}
	return wrapDarwinFile(file, newname), nil
}

func (f *darwinFS) OpenDir(name string) (vfs.File, error) {
	file, err := f.FS.OpenDir(name)
	if err != nil {
		return file, err
	}
	return wrapDarwinFile(file, name), nil
}

type syscallFile interface {
	SyscallConn() (syscall.RawConn, error)
}

// darwinFile delegates file lifecycle and data operations unchanged. Native
// descriptors are acquired through RawConn, never the unpinned Fd shortcut;
// directory files also work when Pebble intentionally reports InvalidFd.
type darwinFile struct {
	vfs.File
	name string
}

func wrapDarwinFile(file vfs.File, name string) vfs.File {
	if _, ok := file.(syscallFile); !ok {
		return file
	}
	return &darwinFile{File: file, name: name}
}

// Sync keeps Go's F_FULLFSYNC command and ENOTSUP-only fallback. x/sys uses
// the runtime syscall boundary, unlike Go 1.25.11's internal Darwin fcntl.
func (f *darwinFile) Sync() error {
	return syncDarwinFile(f.File, f.name, func(fd uintptr) error {
		return syncDarwinFD(fd, unix.FcntlInt, unix.Fsync)
	})
}

// Darwin's upstream SyncData and SyncTo both perform a full file sync.
func (f *darwinFile) SyncData() error { return f.Sync() }

func (f *darwinFile) SyncTo(int64) (bool, error) {
	if err := f.Sync(); err != nil {
		return false, err
	}
	return true, nil
}

// syncDarwinFile pins the descriptor until the complete sync/retry operation
// returns. Close fences later acquisitions and cannot recycle this descriptor
// while Control is in flight. Errors keep os.File.Sync's operation and path.
func syncDarwinFile(file vfs.File, name string, syncFD func(uintptr) error) error {
	conn, err := file.(syscallFile).SyscallConn()
	if err == nil {
		var syncErr error
		err = conn.Control(func(fd uintptr) { syncErr = syncFD(fd) })
		// RawControl exposes internal/poll's private file-closing error. Stat
		// observes the same closed-file fence through os.File's public error
		// mapping; do this only for acquisition errors, never a sync I/O error.
		if err != nil && !errors.Is(err, os.ErrClosed) {
			if _, statErr := file.Stat(); errors.Is(statErr, os.ErrClosed) {
				err = os.ErrClosed
			}
		}
		if err == nil {
			err = syncErr
		}
	}
	if err != nil {
		return &os.PathError{Op: "sync", Path: name, Err: err}
	}
	return nil
}

// syncDarwinFD retries the entire operation on EINTR, including a fallback
// interrupted on an unsupported mount, matching Go's existing Fsync loop.
// Narrow syscall arguments make the error contract testable without real faults.
func syncDarwinFD(fd uintptr, fcntl func(uintptr, int, int) (int, error), fsync func(int) error) error {
	for {
		_, err := fcntl(fd, unix.F_FULLFSYNC, 0)
		if errors.Is(err, unix.ENOTSUP) {
			err = fsync(int(fd))
		}
		if !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}
