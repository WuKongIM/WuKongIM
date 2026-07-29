package backup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"

	artifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const secureDirectoryOpenFlags = unix.O_RDONLY |
	unix.O_DIRECTORY |
	unix.O_CLOEXEC |
	unix.O_NOFOLLOW

// FileArchiveStore stores one archive repository below a fixed local or shared
// filesystem root. Every object operation is anchored to a directory handle
// and refuses symlinks so repository contents cannot redirect I/O elsewhere.
type FileArchiveStore struct {
	root string
}

// NewFileArchiveStore opens a fixed filesystem repository root.
func NewFileArchiveStore(root string) (*FileArchiveStore, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("backup file store: resolve root: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if absolute == filepath.VolumeName(absolute)+string(filepath.Separator) {
		return nil, fmt.Errorf("backup file store: filesystem root is forbidden")
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("backup file store: create root: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, fmt.Errorf("backup file store: inspect root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("backup file store: root must be a directory")
	}
	rootFD, err := unix.Open(absolute, secureDirectoryOpenFlags, 0)
	if err != nil {
		return nil, fmt.Errorf("backup file store: securely open root: %w", err)
	}
	if err := unix.Close(rootFD); err != nil {
		return nil, fmt.Errorf("backup file store: close root: %w", err)
	}
	return &FileArchiveStore{root: absolute}, nil
}

// Put atomically publishes one object, optionally only when absent.
func (s *FileArchiveStore) Put(ctx context.Context, object artifact.PutObject) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if object.Body == nil {
		return fmt.Errorf("%w: object body is required", artifact.ErrInvalidObject)
	}
	parent, base, err := s.openObjectParent(object.Key, true)
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	tempDirectory, err := s.openDirectory(".tmp", true)
	if err != nil {
		return err
	}
	defer unix.Close(tempDirectory)
	tempName, tempFD, err := createSecureTemp(tempDirectory)
	if err != nil {
		return err
	}
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = unix.Unlinkat(tempDirectory, tempName, 0)
		}
	}()
	file := os.NewFile(uintptr(tempFD), tempName)
	if file == nil {
		_ = unix.Close(tempFD)
		return fmt.Errorf("backup file store: wrap temporary object")
	}
	written, copyErr := copyExact(ctx, file, object.Body, object.ExpectedBytes)
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if written != object.ExpectedBytes {
		return fmt.Errorf(
			"%w: object %q bytes %d, expected %d",
			artifact.ErrInvalidObject, object.Key, written, object.ExpectedBytes,
		)
	}
	if syncErr != nil {
		return fmt.Errorf("backup file store: sync object: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("backup file store: close object: %w", closeErr)
	}
	if object.IfAbsent {
		if err := unix.Linkat(tempDirectory, tempName, parent, base, 0); err != nil {
			if errors.Is(err, unix.EEXIST) {
				return artifact.ErrObjectExists
			}
			return fmt.Errorf("backup file store: publish new object: %w", err)
		}
		if err := unix.Unlinkat(tempDirectory, tempName, 0); err != nil {
			return fmt.Errorf("backup file store: remove linked temporary object: %w", err)
		}
		removeTemp = false
	} else {
		if err := unix.Renameat(tempDirectory, tempName, parent, base); err != nil {
			return fmt.Errorf("backup file store: replace object: %w", err)
		}
		removeTemp = false
	}
	if err := unix.Fsync(parent); err != nil {
		return fmt.Errorf("backup file store: sync object directory: %w", err)
	}
	return nil
}

// Open returns one regular repository object.
func (s *FileArchiveStore) Open(
	ctx context.Context,
	key string,
) (io.ReadCloser, artifact.ArchiveObject, error) {
	if err := ctx.Err(); err != nil {
		return nil, artifact.ArchiveObject{}, err
	}
	parent, base, err := s.openObjectParent(key, false)
	if errors.Is(err, artifact.ErrObjectNotFound) {
		return nil, artifact.ArchiveObject{}, artifact.ErrObjectNotFound
	}
	if err != nil {
		return nil, artifact.ArchiveObject{}, err
	}
	defer unix.Close(parent)
	fd, err := unix.Openat(
		parent, base,
		unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0,
	)
	if errors.Is(err, unix.ENOENT) {
		return nil, artifact.ArchiveObject{}, artifact.ErrObjectNotFound
	}
	if err != nil {
		return nil, artifact.ArchiveObject{},
			fmt.Errorf("%w: securely open object: %v", artifact.ErrInvalidObject, err)
	}
	file := os.NewFile(uintptr(fd), key)
	if file == nil {
		_ = unix.Close(fd)
		return nil, artifact.ArchiveObject{}, fmt.Errorf("backup file store: wrap object")
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, artifact.ArchiveObject{}, fmt.Errorf("backup file store: inspect object: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, artifact.ArchiveObject{},
			fmt.Errorf("%w: object is not regular", artifact.ErrInvalidObject)
	}
	return file, artifact.ArchiveObject{
		Key: key, Bytes: uint64(info.Size()), Modified: info.ModTime().UTC(),
	}, nil
}

// List returns sorted regular objects below prefix without following symlinks.
func (s *FileArchiveStore) List(
	ctx context.Context,
	prefix string,
) ([]artifact.ArchiveObject, error) {
	if err := artifact.ValidateRepositoryKey(prefix); err != nil {
		return nil, fmt.Errorf("%w: %v", artifact.ErrInvalidObject, err)
	}
	fd, err := s.openDirectory(prefix, false)
	if errors.Is(err, artifact.ErrObjectNotFound) {
		return []artifact.ArchiveObject{}, nil
	}
	if err != nil {
		return nil, err
	}
	directory := os.NewFile(uintptr(fd), prefix)
	if directory == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("backup file store: wrap list directory")
	}
	items := make([]artifact.ArchiveObject, 0)
	if err := walkSecureDirectory(ctx, directory, prefix, &items); err != nil {
		return nil, fmt.Errorf("backup file store: list %q: %w", prefix, err)
	}
	sort.Slice(items, func(left, right int) bool {
		return items[left].Key < items[right].Key
	})
	return items, nil
}

// Delete removes one exact repository object without following symlinks.
func (s *FileArchiveStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	parent, base, err := s.openObjectParent(key, false)
	if errors.Is(err, artifact.ErrObjectNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	if err := unix.Unlinkat(parent, base, 0); err != nil &&
		!errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("backup file store: delete object: %w", err)
	}
	return nil
}

// DeletePrefix removes only the regular objects returned below one validated
// prefix. Empty directories are harmless and may remain for later reuse.
func (s *FileArchiveStore) DeletePrefix(ctx context.Context, prefix string) error {
	items, err := s.List(ctx, prefix)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := s.Delete(ctx, item.Key); err != nil {
			return err
		}
	}
	return nil
}

func (s *FileArchiveStore) openObjectParent(
	key string,
	create bool,
) (int, string, error) {
	if err := artifact.ValidateRepositoryKey(key); err != nil {
		return -1, "", fmt.Errorf("%w: %v", artifact.ErrInvalidObject, err)
	}
	parts := strings.Split(key, "/")
	parentKey := strings.Join(parts[:len(parts)-1], "/")
	fd, err := s.openDirectory(parentKey, create)
	return fd, parts[len(parts)-1], err
}

func (s *FileArchiveStore) openDirectory(key string, create bool) (int, error) {
	fd, err := unix.Open(s.root, secureDirectoryOpenFlags, 0)
	if err != nil {
		return -1, fmt.Errorf("backup file store: securely open root: %w", err)
	}
	if key == "" {
		return fd, nil
	}
	if err := artifact.ValidateRepositoryKey(key); err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("%w: %v", artifact.ErrInvalidObject, err)
	}
	for _, part := range strings.Split(key, "/") {
		next, openErr := unix.Openat(fd, part, secureDirectoryOpenFlags, 0)
		if errors.Is(openErr, unix.ENOENT) && create {
			if mkdirErr := unix.Mkdirat(fd, part, 0o700); mkdirErr != nil &&
				!errors.Is(mkdirErr, unix.EEXIST) {
				_ = unix.Close(fd)
				return -1, fmt.Errorf(
					"backup file store: create object directory: %w",
					mkdirErr,
				)
			}
			next, openErr = unix.Openat(fd, part, secureDirectoryOpenFlags, 0)
		}
		_ = unix.Close(fd)
		if errors.Is(openErr, unix.ENOENT) {
			return -1, artifact.ErrObjectNotFound
		}
		if openErr != nil {
			return -1, fmt.Errorf(
				"%w: unsafe repository directory %q: %v",
				artifact.ErrInvalidObject, part, openErr,
			)
		}
		fd = next
	}
	return fd, nil
}

func createSecureTemp(directory int) (string, int, error) {
	for attempt := 0; attempt < 16; attempt++ {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", -1, fmt.Errorf("backup file store: random temporary name: %w", err)
		}
		name := "put-" + hex.EncodeToString(random[:])
		fd, err := unix.Openat(
			directory, name,
			unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW,
			0o600,
		)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return "", -1, fmt.Errorf("backup file store: create temporary object: %w", err)
		}
		return name, fd, nil
	}
	return "", -1, fmt.Errorf("backup file store: temporary name collision")
}

func walkSecureDirectory(
	ctx context.Context,
	directory *os.File,
	relative string,
	items *[]artifact.ArchiveObject,
) error {
	defer directory.Close()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		key := relative + "/" + name
		childFD, openErr := unix.Openat(
			int(directory.Fd()), name, secureDirectoryOpenFlags, 0,
		)
		if openErr == nil {
			child := os.NewFile(uintptr(childFD), key)
			if child == nil {
				_ = unix.Close(childFD)
				return fmt.Errorf("wrap directory %q", key)
			}
			if err := walkSecureDirectory(ctx, child, key, items); err != nil {
				return err
			}
			continue
		}
		fd, openErr := unix.Openat(
			int(directory.Fd()), name,
			unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
			0,
		)
		if openErr != nil {
			return fmt.Errorf(
				"%w: non-regular repository entry %q",
				artifact.ErrInvalidObject, key,
			)
		}
		file := os.NewFile(uintptr(fd), key)
		if file == nil {
			_ = unix.Close(fd)
			return fmt.Errorf("wrap object %q", key)
		}
		info, statErr := file.Stat()
		closeErr := file.Close()
		if statErr != nil || closeErr != nil {
			return errors.Join(statErr, closeErr)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf(
				"%w: non-regular repository entry %q",
				artifact.ErrInvalidObject, key,
			)
		}
		*items = append(*items, artifact.ArchiveObject{
			Key: key, Bytes: uint64(info.Size()), Modified: info.ModTime().UTC(),
		})
	}
	return nil
}

func copyExact(ctx context.Context, dst io.Writer, src io.Reader, expected uint64) (uint64, error) {
	buffer := make([]byte, 128<<10)
	var written uint64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		remaining := expected + 1 - written
		if remaining == 0 {
			return written, fmt.Errorf("%w: object exceeds expected size", artifact.ErrInvalidObject)
		}
		readSize := uint64(len(buffer))
		if remaining < readSize {
			readSize = remaining
		}
		read, readErr := src.Read(buffer[:readSize])
		if read > 0 {
			count, writeErr := dst.Write(buffer[:read])
			written += uint64(count)
			if writeErr != nil {
				return written, fmt.Errorf("backup file store: write object: %w", writeErr)
			}
			if count != read {
				return written, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return written, nil
		}
		if readErr != nil {
			return written, fmt.Errorf("backup file store: read object: %w", readErr)
		}
	}
}
