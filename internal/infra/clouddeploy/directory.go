// Package clouddeploy adapts a local directory to the offline bundle file port.
package clouddeploy

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"

	clouddeployusecase "github.com/WuKongIM/WuKongIM/internal/usecase/clouddeploy"
)

const secureDirectoryOpenFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW

// Directory provides root-anchored, no-follow, atomic bundle file access.
type Directory struct {
	root string
}

// Open validates an existing bundle root without following symlinks.
func Open(root string) (*Directory, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	directory := &Directory{root: filepath.Clean(absolute)}
	if err := directory.validateRoot(); err != nil {
		return nil, err
	}
	return directory, nil
}

// WriteFile atomically replaces one regular file with an exact permission mode.
func (d *Directory) WriteFile(relative string, data []byte, mode uint32) error {
	parent, base, err := d.openParent(relative, true)
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	if err := rejectNonRegularTarget(parent, base); err != nil {
		return err
	}
	name, descriptor, err := createTemp(parent)
	if err != nil {
		return err
	}
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = unix.Unlinkat(parent, name, 0)
		}
	}()
	file := os.NewFile(uintptr(descriptor), name)
	if file == nil {
		_ = unix.Close(descriptor)
		return invalid("wrap temporary file")
	}
	if err := unix.Fchmod(descriptor, mode); err != nil {
		_ = file.Close()
		return err
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := unix.Renameat(parent, name, parent, base); err != nil {
		return err
	}
	removeTemp = false
	return unix.Fsync(parent)
}

// ReadFile reads one regular file and rejects content over maxBytes.
func (d *Directory) ReadFile(relative string, maxBytes int64) ([]byte, error) {
	file, err := d.openRegular(relative)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || int64(len(data)) > maxBytes {
		return nil, invalid("file %s exceeds bounded read", relative)
	}
	return data, nil
}

// ReadPrefix reads exactly bytes from the beginning of one regular file.
func (d *Directory) ReadPrefix(relative string, bytes int) ([]byte, error) {
	file, err := d.openRegular(relative)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data := make([]byte, bytes)
	if _, err := io.ReadFull(file, data); err != nil {
		return nil, err
	}
	return data, nil
}

// Files inventories every regular file and hashes its content in path order.
func (d *Directory) Files(maxFiles int) ([]clouddeployusecase.FileRecord, error) {
	if err := d.validateRoot(); err != nil {
		return nil, err
	}
	records := make([]clouddeployusecase.FileRecord, 0, 128)
	err := filepath.WalkDir(d.root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(d.root, filePath)
		if err != nil || relative == "." {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return invalid("symlink %s", relative)
		}
		if entry.IsDir() {
			return nil
		}
		if len(records) >= maxFiles {
			return invalid("non-regular or excessive entry %s", relative)
		}
		record, err := d.fileRecord(filepath.ToSlash(relative))
		if err != nil {
			return err
		}
		records = append(records, record)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Path < records[j].Path })
	return records, nil
}

func (d *Directory) validateRoot() error {
	info, err := os.Lstat(d.root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return invalid("bundle root must be an existing directory")
	}
	rootFD, err := unix.Open(d.root, secureDirectoryOpenFlags, 0)
	if err != nil {
		return invalid("securely open bundle root")
	}
	if err := unix.Close(rootFD); err != nil {
		return err
	}
	return filepath.WalkDir(d.root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return invalid("symlink %s", filePath)
		}
		if filePath != d.root && !entry.IsDir() {
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				return invalid("non-regular entry %s", filePath)
			}
		}
		return nil
	})
}

func (d *Directory) openParent(relative string, create bool) (int, string, error) {
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || clean == ".." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return -1, "", invalid("unsafe path %s", relative)
	}
	parts := strings.Split(clean, string(filepath.Separator))
	fd, err := unix.Open(d.root, secureDirectoryOpenFlags, 0)
	if err != nil {
		return -1, "", invalid("securely open root")
	}
	for _, part := range parts[:len(parts)-1] {
		next, openErr := unix.Openat(fd, part, secureDirectoryOpenFlags, 0)
		if errors.Is(openErr, unix.ENOENT) && create {
			if mkdirErr := unix.Mkdirat(fd, part, 0o755); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				_ = unix.Close(fd)
				return -1, "", mkdirErr
			}
			next, openErr = unix.Openat(fd, part, secureDirectoryOpenFlags, 0)
		}
		_ = unix.Close(fd)
		if openErr != nil {
			return -1, "", invalid("unsafe directory %s", part)
		}
		fd = next
	}
	return fd, parts[len(parts)-1], nil
}

func (d *Directory) openRegular(relative string) (*os.File, error) {
	parent, base, err := d.openParent(relative, false)
	if err != nil {
		return nil, err
	}
	defer unix.Close(parent)
	descriptor, err := unix.Openat(parent, base, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(descriptor), relative)
	if file == nil {
		_ = unix.Close(descriptor)
		return nil, invalid("wrap file %s", relative)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, invalid("non-regular file %s", relative)
	}
	return file, nil
}

func (d *Directory) fileRecord(relative string) (clouddeployusecase.FileRecord, error) {
	file, err := d.openRegular(relative)
	if err != nil {
		return clouddeployusecase.FileRecord{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return clouddeployusecase.FileRecord{}, err
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return clouddeployusecase.FileRecord{}, err
	}
	return clouddeployusecase.FileRecord{
		Path: relative, Mode: uint32(info.Mode().Perm()), Size: info.Size(), SHA256: hex.EncodeToString(digest.Sum(nil)),
	}, nil
}

func rejectNonRegularTarget(parent int, base string) error {
	var stat unix.Stat_t
	err := unix.Fstatat(parent, base, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return invalid("unsafe target %s", base)
	}
	return nil
}

func createTemp(parent int) (string, int, error) {
	for attempt := 0; attempt < 16; attempt++ {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", -1, err
		}
		name := ".seal-" + hex.EncodeToString(random[:])
		fd, err := unix.Openat(parent, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return "", -1, err
		}
		return name, fd, nil
	}
	return "", -1, invalid("temporary file collision")
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", clouddeployusecase.ErrInvalidBundle, fmt.Sprintf(format, args...))
}
