package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// localSingleNodeArtifactRoot opens every artifact component with O_NOFOLLOW.
// The root itself is canonicalized once so ordinary platform aliases (for
// example macOS /var -> /private/var) do not make safe temporary roots unusable.
type localSingleNodeArtifactRoot struct {
	path         string
	requestedAbs string
	device       uint64
	inode        uint64
}

func openLocalSingleNodeArtifactRoot(path string) (localSingleNodeArtifactRoot, error) {
	absolute, err := filepath.Abs(filepath.Clean(strings.TrimSpace(path)))
	if err != nil {
		return localSingleNodeArtifactRoot{}, err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return localSingleNodeArtifactRoot{}, err
	}
	fd, err := unix.Open(canonical, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return localSingleNodeArtifactRoot{}, fmt.Errorf("open artifact root: %w", err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return localSingleNodeArtifactRoot{}, fmt.Errorf("identify artifact root: %w", err)
	}
	if err := unix.Close(fd); err != nil {
		return localSingleNodeArtifactRoot{}, fmt.Errorf("close artifact root: %w", err)
	}
	return localSingleNodeArtifactRoot{
		path: canonical, requestedAbs: absolute, device: uint64(stat.Dev), inode: stat.Ino,
	}, nil
}

func (root localSingleNodeArtifactRoot) relative(path string) (string, error) {
	absolute, err := filepath.Abs(filepath.Clean(strings.TrimSpace(path)))
	if err != nil {
		return "", err
	}
	for _, base := range []string{root.requestedAbs, root.path} {
		relative, relativeErr := filepath.Rel(base, absolute)
		if relativeErr != nil {
			continue
		}
		relative = filepath.ToSlash(relative)
		if safeLocalSingleNodeRelativePath(relative) {
			return relative, nil
		}
	}
	return "", errors.New("path is outside artifact root")
}

func (root localSingleNodeArtifactRoot) read(relative string, maximum int64) ([]byte, error) {
	file, err := root.openRegular(relative)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("file exceeds %d bytes", maximum)
	}
	return data, nil
}

func (root localSingleNodeArtifactRoot) digest(relative string, maximum int64) (string, error) {
	file, err := root.openRegular(relative)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	reader := io.Reader(file)
	if maximum > 0 {
		reader = io.LimitReader(file, maximum+1)
	}
	written, err := io.Copy(hash, reader)
	if err != nil {
		return "", err
	}
	if maximum > 0 && written > maximum {
		return "", fmt.Errorf("file exceeds %d bytes", maximum)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (root localSingleNodeArtifactRoot) openRegular(relative string) (*os.File, error) {
	parentFD, base, err := root.openParent(relative)
	if err != nil {
		return nil, err
	}
	defer unix.Close(parentFD)
	fd, err := unix.Openat(parentFD, base, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open artifact without following symlink %q: %w", relative, err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)
		return nil, errors.New("artifact is not a regular file")
	}
	return os.NewFile(uintptr(fd), filepath.Join(root.path, filepath.FromSlash(relative))), nil
}

func (root localSingleNodeArtifactRoot) openParent(relative string) (int, string, error) {
	if !safeLocalSingleNodeRelativePath(relative) {
		return -1, "", errors.New("artifact path is unsafe")
	}
	parts := strings.Split(relative, "/")
	base := parts[len(parts)-1]
	fd, err := unix.Open(root.path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, "", err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return -1, "", err
	}
	if uint64(stat.Dev) != root.device || stat.Ino != root.inode {
		_ = unix.Close(fd)
		return -1, "", errors.New("artifact root identity changed")
	}
	for _, part := range parts[:len(parts)-1] {
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		_ = unix.Close(fd)
		if openErr != nil {
			return -1, "", fmt.Errorf("open artifact parent without following symlink %q: %w", part, openErr)
		}
		fd = next
	}
	return fd, base, nil
}

func (root localSingleNodeArtifactRoot) writeJSONExclusive(relative string, value any) error {
	data, err := marshalLocalSingleNodeJSON(value)
	if err != nil {
		return err
	}
	return root.writeExclusive(relative, data)
}

func marshalLocalSingleNodeJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func (root localSingleNodeArtifactRoot) writeExclusive(relative string, data []byte) error {
	parentFD, base, err := root.openParent(relative)
	if err != nil {
		return err
	}
	defer unix.Close(parentFD)
	var existing unix.Stat_t
	if err := unix.Fstatat(parentFD, base, &existing, unix.AT_SYMLINK_NOFOLLOW); err == nil {
		return errors.New("output already exists")
	} else if !errors.Is(err, unix.ENOENT) {
		return err
	}
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	temporary := ".local-single-node-" + hex.EncodeToString(random) + ".tmp"
	fd, err := unix.Openat(parentFD, temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), temporary)
	removeTemporary := true
	defer func() {
		_ = file.Close()
		if removeTemporary {
			_ = unix.Unlinkat(parentFD, temporary, 0)
		}
	}()
	if _, err := io.Copy(file, bytes.NewReader(data)); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := unix.Linkat(parentFD, temporary, parentFD, base, 0); err != nil {
		return err
	}
	if err := unix.Unlinkat(parentFD, temporary, 0); err != nil {
		return err
	}
	removeTemporary = false
	return unix.Fsync(parentFD)
}
