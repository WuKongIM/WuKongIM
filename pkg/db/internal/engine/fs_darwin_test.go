//go:build darwin

package engine

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"syscall"
	"testing"

	"github.com/cockroachdb/pebble/v2/vfs"
	"golang.org/x/sys/unix"
)

// Failure contracts precede the adapter: full durability must survive syscall
// scheduling changes, interrupted calls, unsupported mounts and close races.
func TestDarwinFullSyncErrorContract(t *testing.T) {
	for _, tc := range []struct {
		name           string
		full, fallback []error
		calls          []string
		want           error
	}{
		{"full", []error{nil}, nil, []string{"full"}, nil},
		{"full-interrupted", []error{unix.EINTR, nil}, nil, []string{"full", "full"}, nil},
		{"unsupported", []error{unix.ENOTSUP}, []error{nil}, []string{"full", "fallback"}, nil},
		{"wrapped-unsupported", []error{fmt.Errorf("mount: %w", unix.ENOTSUP)}, []error{nil}, []string{"full", "fallback"}, nil},
		{"fallback-interrupted", []error{unix.ENOTSUP, unix.ENOTSUP}, []error{unix.EINTR, nil}, []string{"full", "fallback", "full", "fallback"}, nil},
		{"full-io-error", []error{unix.EIO}, nil, []string{"full"}, unix.EIO},
		{"invalid-command", []error{unix.EINVAL}, nil, []string{"full"}, unix.EINVAL},
		{"permission", []error{unix.EPERM}, nil, []string{"full"}, unix.EPERM},
		{"bad-descriptor", []error{unix.EBADF}, nil, []string{"full"}, unix.EBADF},
		{"fallback-io-error", []error{unix.ENOTSUP}, []error{unix.EIO}, []string{"full", "fallback"}, unix.EIO},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			full, fallback := 0, 0
			got := syncDarwinFD(123, func(fd uintptr, cmd, arg int) (int, error) {
				if fd != 123 || cmd != unix.F_FULLFSYNC || arg != 0 {
					t.Fatalf("full sync arguments: %d/%d/%d", fd, cmd, arg)
				}
				calls = append(calls, "full")
				if full >= len(tc.full) {
					t.Fatal("unexpected full-sync retry")
				}
				err := tc.full[full]
				full++
				return 0, err
			}, func(fd int) error {
				if fd != 123 {
					t.Fatalf("fallback fd: %d", fd)
				}
				calls = append(calls, "fallback")
				if fallback >= len(tc.fallback) {
					t.Fatal("unexpected fallback")
				}
				err := tc.fallback[fallback]
				fallback++
				return err
			})
			if !errors.Is(got, tc.want) || !reflect.DeepEqual(calls, tc.calls) {
				t.Fatalf("got %v/%v, want %v/%v", got, calls, tc.want, tc.calls)
			}
		})
	}
}

type syncContractConn struct {
	err   error
	calls int
}

func (c *syncContractConn) Control(fn func(uintptr)) error {
	c.calls++
	if c.err == nil {
		fn(123)
	}
	return c.err
}
func (*syncContractConn) Read(func(uintptr) bool) error  { panic("unexpected Read") }
func (*syncContractConn) Write(func(uintptr) bool) error { panic("unexpected Write") }

type syncContractFile struct {
	vfs.File
	conn *syncContractConn
	err  error
}

func (f *syncContractFile) SyscallConn() (syscall.RawConn, error) { return f.conn, f.err }

func TestDarwinSyncFileErrorIdentity(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		connection, control, sync error
		calls                     int
	}{
		{"success", nil, nil, nil, 1},
		{"connection", os.ErrClosed, nil, nil, 0},
		{"control", nil, os.ErrClosed, nil, 0},
		{"sync", nil, nil, unix.EIO, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := &syncContractConn{err: tc.control}
			f := &syncContractFile{conn: conn, err: tc.connection}
			calls := 0
			got := syncDarwinFile(f, "data.log", func(fd uintptr) error {
				calls++
				if fd != 123 {
					t.Fatal("wrong fd")
				}
				return tc.sync
			})
			want := errors.Join(tc.connection, tc.control, tc.sync)
			if calls != tc.calls || (got == nil) != (want == nil) {
				t.Fatalf("got %v, calls %d", got, calls)
			}
			if want != nil {
				var pathErr *os.PathError
				if !errors.As(got, &pathErr) || pathErr.Op != "sync" || pathErr.Path != "data.log" {
					t.Fatalf("lost PathError: %v", got)
				}
				for _, err := range []error{tc.connection, tc.control, tc.sync} {
					if err != nil && !errors.Is(got, err) {
						t.Fatalf("lost error identity: %v", got)
					}
				}
			}
		})
	}
}

func TestDarwinMemoryFilesKeepOriginalSync(t *testing.T) {
	mem := vfs.NewMem()
	f, err := mem.Create("data", vfs.WriteCategoryUnspecified)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got := wrapDarwinFile(f, "data"); got != f {
		t.Fatalf("non-native file changed: %T", got)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
}
