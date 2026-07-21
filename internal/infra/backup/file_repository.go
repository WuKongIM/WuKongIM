package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// FileRepository is a development and test repository backed by immutable local files.
type FileRepository struct {
	name string
	root string
}

// NewFileRepository creates a local immutable repository rooted at root.
func NewFileRepository(name, root string) (*FileRepository, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("backup file repository: name and root are required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o750); err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	return &FileRepository{name: name, root: resolved}, nil
}

// Name returns the configured operator-facing repository name.
func (r *FileRepository) Name() string {
	if r == nil {
		return ""
	}
	return r.name
}

// PutImmutable verifies body before atomically linking it at key without replacement.
func (r *FileRepository) PutImmutable(ctx context.Context, key string, size int64, checksum string, body io.Reader) error {
	if size < 0 || !validFileChecksum(checksum) || body == nil {
		return fmt.Errorf("%w: file repository object metadata is invalid", backupartifact.ErrInvalidObject)
	}
	target, err := r.objectPath(key, true)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".backup-object-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(contextReader{ctx: ctx, reader: body}, size+1))
	if copyErr != nil {
		_ = temporary.Close()
		return copyErr
	}
	actualChecksum := hex.EncodeToString(hash.Sum(nil))
	if written != size || actualChecksum != checksum {
		_ = temporary.Close()
		return fmt.Errorf("%w: file repository body verification mismatch", backupartifact.ErrObjectCorrupt)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o640); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, target); err != nil {
		if errors.Is(err, os.ErrExist) {
			return backupartifact.ErrObjectExists
		}
		return err
	}
	if err := syncDirectory(filepath.Dir(target)); err != nil {
		return err
	}
	return nil
}

// Open returns a streaming reader after checking that key is a regular immutable file.
func (r *FileRepository) Open(ctx context.Context, key string) (io.ReadCloser, backupartifact.RepositoryObject, error) {
	object, err := r.Stat(ctx, key)
	if err != nil {
		return nil, backupartifact.RepositoryObject{}, err
	}
	target, err := r.objectPath(key, false)
	if err != nil {
		return nil, backupartifact.RepositoryObject{}, err
	}
	file, err := os.Open(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil, backupartifact.RepositoryObject{}, backupartifact.ErrObjectNotFound
	}
	if err != nil {
		return nil, backupartifact.RepositoryObject{}, err
	}
	return file, object, nil
}

// Stat returns verified local file metadata for key.
func (r *FileRepository) Stat(ctx context.Context, key string) (backupartifact.RepositoryObject, error) {
	target, err := r.objectPath(key, false)
	if err != nil {
		return backupartifact.RepositoryObject{}, err
	}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return backupartifact.RepositoryObject{}, backupartifact.ErrObjectNotFound
	}
	if err != nil {
		return backupartifact.RepositoryObject{}, err
	}
	if !info.Mode().IsRegular() {
		return backupartifact.RepositoryObject{}, fmt.Errorf("%w: repository object is not a regular file", backupartifact.ErrObjectCorrupt)
	}
	file, err := os.Open(target)
	if err != nil {
		return backupartifact.RepositoryObject{}, err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, contextReader{ctx: ctx, reader: file})
	closeErr := file.Close()
	if copyErr != nil {
		return backupartifact.RepositoryObject{}, copyErr
	}
	if closeErr != nil {
		return backupartifact.RepositoryObject{}, closeErr
	}
	return backupartifact.RepositoryObject{Key: key, Size: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

// DeleteGarbageObject removes one already-unreachable immutable development
// object. Production authorization remains separate in the S3 adapter.
func (r *FileRepository) DeleteGarbageObject(_ context.Context, key string) error {
	target, err := r.objectPath(key, false)
	if errors.Is(err, backupartifact.ErrObjectNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(filepath.Dir(target))
}

// WalkGarbageObjects streams regular immutable files older than before while
// rejecting symlinks and paths outside the repository root.
func (r *FileRepository) WalkGarbageObjects(ctx context.Context, before time.Time, visit func(backupartifact.RepositoryObject) (bool, error)) error {
	if r == nil || r.root == "" || visit == nil || before.IsZero() {
		return fmt.Errorf("backup file repository: garbage walk options are invalid")
	}
	errStop := errors.New("backup file repository: stop garbage walk")
	err := filepath.WalkDir(r.root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if current == r.root || entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: repository contains symlink", backupartifact.ErrObjectCorrupt)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: repository contains non-regular object", backupartifact.ErrObjectCorrupt)
		}
		if !info.ModTime().UTC().Before(before.UTC()) {
			return nil
		}
		relative, err := filepath.Rel(r.root, current)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(relative)
		if !safeRepositoryKey(key) {
			return fmt.Errorf("%w: unsafe repository walk key", backupartifact.ErrInvalidObject)
		}
		keepWalking, err := visit(backupartifact.RepositoryObject{Key: key, Size: info.Size()})
		if err != nil {
			return err
		}
		if !keepWalking {
			return errStop
		}
		return nil
	})
	if errors.Is(err, errStop) {
		return nil
	}
	return err
}

// ListRestorePointIDs returns bounded restore-point directories that expose a
// regular immutable manifest file. Manifest contents are verified separately.
func (r *FileRepository) ListRestorePointIDs(ctx context.Context) ([]string, error) {
	if r == nil || r.root == "" {
		return nil, fmt.Errorf("backup file repository: repository is required")
	}
	root := filepath.Join(r.root, "restore-points")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(entries) > maxListedRestorePoints {
		return nil, fmt.Errorf("backup file repository: restore-point listing exceeds limit")
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() || !safeRestorePointID(entry.Name()) {
			continue
		}
		info, err := os.Lstat(filepath.Join(root, entry.Name(), "manifest.json"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode().IsRegular() {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (r *FileRepository) objectPath(key string, createParent bool) (string, error) {
	if r == nil || r.root == "" || key == "" || strings.Contains(key, "\\") || strings.HasPrefix(key, "/") || path.Clean(key) != key || key == "." || key == ".." || strings.HasPrefix(key, "../") {
		return "", fmt.Errorf("%w: unsafe repository key", backupartifact.ErrInvalidObject)
	}
	target := filepath.Join(r.root, filepath.FromSlash(key))
	parent := filepath.Dir(target)
	if createParent {
		if err := os.MkdirAll(parent, 0o750); err != nil {
			return "", err
		}
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", backupartifact.ErrObjectNotFound
		}
		return "", err
	}
	relative, err := filepath.Rel(r.root, resolvedParent)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: repository key escapes root", backupartifact.ErrInvalidObject)
	}
	return filepath.Join(resolvedParent, filepath.Base(target)), nil
}

func validFileChecksum(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func syncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if r.ctx != nil {
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
	}
	return r.reader.Read(buffer)
}

var _ backupartifact.Repository = (*FileRepository)(nil)
var _ RestorePointLister = (*FileRepository)(nil)
