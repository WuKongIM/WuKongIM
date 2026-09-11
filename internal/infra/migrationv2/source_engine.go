package migrationv2

import (
	"errors"
	"io"
	"os"

	"github.com/cockroachdb/pebble"
	pebble2 "github.com/cockroachdb/pebble/v2"
	vfs2 "github.com/cockroachdb/pebble/v2/vfs"
)

// sourceLock retains the actual engine lock until the complete stopped-node
// capture finishes. Never reopen its LOCK descriptor: fcntl locks are process-owned.
type sourceLock struct {
	legacy *pebble.Lock
	modern *pebble2.Lock
}

func (l *sourceLock) Close() error {
	if l.modern != nil {
		return l.modern.Close()
	}
	return l.legacy.Close()
}

// lockSourceDirectory selects an engine by its read-only format inspection.
// Format 19 is the existing Pebble v2 deployment format; other newer formats
// remain unsupported. Engine readability never certifies business compatibility.
func lockSourceDirectory(path string) (*sourceLock, error) {
	_, legacyErr := pebble.Peek(path, sourceFS{})
	if legacyErr == nil {
		lock, err := pebble.LockDirectory(path, sourceFS{})
		if err != nil {
			return nil, err
		}
		return &sourceLock{legacy: lock}, nil
	}
	fs := sourceFS2{FS: vfs2.Default}
	info, err := pebble2.Peek(path, fs)
	if err != nil || !info.Exists || info.FormatMajorVersion != 19 {
		return nil, legacyErr
	}
	lock, err := pebble2.LockDirectory(path, fs)
	if err != nil {
		return nil, err
	}
	return &sourceLock{modern: lock}, nil
}

// sourceIterator exposes only the forward, read-only scan used by the decoder.
type sourceIterator interface {
	First() bool
	Next() bool
	Key() []byte
	Value() []byte
	Error() error
	Close() error
}

type sourceDatabase struct {
	legacy *pebble.DB
	modern *pebble2.DB
}

func openSourceDatabase(path string, lock *sourceLock, cache *pebble.Cache) (*sourceDatabase, error) {
	if lock == nil {
		return nil, errors.New("source database requires its held directory lock")
	}
	if lock.modern != nil {
		info, err := pebble2.Peek(path, sourceFS2{FS: vfs2.Default})
		if err != nil {
			return nil, err
		}
		if !info.Exists || info.FormatMajorVersion != 19 {
			return nil, errors.New("unsupported stopped source Pebble format; expected format 19")
		}
		modernCache := pebble2.NewCache(16 << 20)
		defer modernCache.Unref()
		db, err := pebble2.Open(path, &pebble2.Options{ReadOnly: true, ErrorIfNotExists: true, Lock: lock.modern, FS: sourceFS2{FS: vfs2.Default}, Cache: modernCache, MaxOpenFiles: 128})
		if err != nil {
			return nil, err
		}
		return &sourceDatabase{modern: db}, nil
	}
	db, err := pebble.Open(path, &pebble.Options{ReadOnly: true, ErrorIfNotExists: true, Lock: lock.legacy, FS: sourceFS{}, Cache: cache, MaxOpenFiles: 128})
	if err != nil {
		return nil, err
	}
	return &sourceDatabase{legacy: db}, nil
}

func (d *sourceDatabase) NewIter() (sourceIterator, error) {
	if d.modern != nil {
		return d.modern.NewIter(nil)
	}
	return d.legacy.NewIter(nil), nil
}

func (d *sourceDatabase) Close() error {
	if d.modern != nil {
		return d.modern.Close()
	}
	return d.legacy.Close()
}

// sourceFS2 exposes the Pebble v2 filesystem contract with every mutation
// rejected. Lock acquisition opens only the existing file, as for the v1 reader.
type sourceFS2 struct{ vfs2.FS }

func (sourceFS2) Lock(path string) (io.Closer, error)                      { return lockSource(path) }
func (sourceFS2) Create(string, vfs2.DiskWriteCategory) (vfs2.File, error) { return nil, errReadOnly }
func (sourceFS2) OpenReadWrite(string, vfs2.DiskWriteCategory, ...vfs2.OpenOption) (vfs2.File, error) {
	return nil, errReadOnly
}
func (sourceFS2) ReuseForWrite(string, string, vfs2.DiskWriteCategory) (vfs2.File, error) {
	return nil, errReadOnly
}
func (sourceFS2) Link(string, string) error          { return errReadOnly }
func (sourceFS2) Remove(string) error                { return errReadOnly }
func (sourceFS2) RemoveAll(string) error             { return errReadOnly }
func (sourceFS2) Rename(string, string) error        { return errReadOnly }
func (sourceFS2) MkdirAll(string, os.FileMode) error { return errReadOnly }
