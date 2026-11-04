package db

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/dataformat"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/shirou/gopsutil/v4/disk"
)

const upgradeDir = "format-upgrade-v1-v2"

type upgradeState struct {
	// Version fixes the migration journal contract.
	Version int `json:"version"`
	// SourceSHA256 binds retries to the original immutable creation marker.
	SourceSHA256 string `json:"source_sha256"`
	// Controller binds the original canonical durable path across retries.
	Controller string `json:"controller"`
	// BackupSHA256 is published only after every backup file and manifest sync.
	BackupSHA256 string `json:"backup_sha256,omitempty"`
}
type heldUpgradeLock struct {
	path   string
	closer io.Closer
}

type backupFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// UpgradeFormat1 performs a cold, one-way startup migration before any runtime
// or listener opens. A sealed byte-for-byte backup precedes the first database
// open. Metadata batch cursors are crash-atomic. Creation provenance is kept.
// All cluster processes must be stopped and replaced with matching binaries.
func UpgradeFormat1(ctx context.Context, root, controller string, report func(string)) error {
	r, err := dataformat.Inspect(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if r.Metadata == nil || r.Metadata.Format != dataformat.CurrentFormat || r.Metadata.FormatVersion != 1 {
		return nil
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	guard, err := engine.LockFile(filepath.Join(root, ".format-upgrade.lock"))
	if err != nil {
		return fmt.Errorf("format upgrade already active: %w", err)
	}
	defer guard.Close()
	// Reinspect after acquiring ownership; another process may have completed.
	r, err = dataformat.Inspect(root)
	if err != nil {
		return err
	}
	if r.Metadata == nil || r.Metadata.FormatVersion != 1 {
		return dataformat.Check(root)
	}
	if controller == "" {
		controller = filepath.Join(root, "controller")
	}
	controller, err = filepath.Abs(controller)
	if err != nil {
		return err
	}
	if resolved, e := filepath.EvalSymlinks(controller); e == nil {
		controller = resolved
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if rel, e := filepath.Rel(controller, root); e == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("Controller state directory must not contain the node data directory")
	}
	if rel, e := filepath.Rel(root, controller); e == nil {
		for _, reserved := range []string{upgradeDir, "backup-repository"} {
			if rel == reserved || strings.HasPrefix(rel, reserved+string(os.PathSeparator)) {
				return fmt.Errorf("Controller state directory overlaps reserved upgrade/archive path")
			}
		}
	}
	dir := filepath.Join(root, upgradeDir)
	if err := privateDirectory(dir); err != nil {
		return err
	}
	marker, err := os.ReadFile(filepath.Join(root, dataformat.FileName))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(marker)
	want := upgradeState{Version: 1, SourceSHA256: hex.EncodeToString(sum[:]), Controller: controller}
	statePath := filepath.Join(dir, "state.json")
	state := want
	if info, e := os.Lstat(statePath); e == nil && (!info.Mode().IsRegular() || info.Size() > 16<<10) {
		return fmt.Errorf("invalid format upgrade state")
	}
	if raw, e := os.ReadFile(statePath); e == nil {
		if len(raw) > 16<<10 {
			return fmt.Errorf("invalid format upgrade state")
		}
		if e = json.Unmarshal(raw, &state); e != nil {
			return e
		}
		if state.Version != want.Version || state.SourceSHA256 != want.SourceSHA256 || state.Controller != want.Controller {
			return fmt.Errorf("format upgrade source/configuration changed")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	} else if err := writeUpgradeJSON(statePath, state); err != nil {
		return err
	}
	sources := map[string]string{"data": root}
	if rel, e := filepath.Rel(root, controller); e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		sources["controller"] = controller
	}
	var locks []heldUpgradeLock
	defer func() {
		for i := len(locks) - 1; i >= 0; i-- {
			_ = locks[i].closer.Close()
		}
	}()
	// These are the existing engine locks, also respected by beta.21. Do not
	// open Pebble before the cold copy: even recovery/open may modify its files.
	for _, source := range sources {
		err = walkUpgradeSource(source, func(path, rel string, d os.DirEntry) error {
			if !d.IsDir() && d.Type()&(os.ModeSymlink|os.ModeType) != 0 {
				return fmt.Errorf("format upgrade refuses special durable path: %s", path)
			}
			if d.IsDir() || d.Name() != "LOCK" {
				return nil
			}
			lock, e := engine.LockFile(path)
			if e != nil {
				return fmt.Errorf("stop all old processes before upgrade: %w", e)
			}
			locks = append(locks, heldUpgradeLock{path: path, closer: lock})
			return nil
		})
		if err != nil {
			return err
		}
	}
	backup := filepath.Join(dir, "backup")
	if state.BackupSHA256 == "" {
		if report != nil {
			report("backup")
		}
		if err := privateDirectory(backup); err != nil {
			return err
		}
		if err := cleanupUpgradePartial(dir, backup); err != nil {
			return err
		}
		var bytes, remaining int64
		for name, source := range sources {
			if err := walkUpgradeSource(source, func(path, rel string, d os.DirEntry) error {
				if d.IsDir() {
					return nil
				}
				info, err := d.Info()
				if err != nil {
					return err
				}
				bytes += info.Size()
				target := filepath.Join(backup, name, rel)
				copied := false
				if existing, e := os.Lstat(target); e == nil && existing.Mode().IsRegular() && existing.Size() == info.Size() {
					a, e := hashUpgradeFile(ctx, path)
					if e != nil {
						return e
					}
					b, e := hashUpgradeFile(ctx, target)
					if e != nil {
						return e
					}
					copied = a == b
				}
				if !copied {
					remaining += info.Size()
				}
				return nil
			}); err != nil {
				return err
			}
		}
		usage, err := disk.Usage(root)
		if err != nil {
			return err
		}
		if bytes < 0 || remaining < 0 || uint64(bytes)+uint64(remaining) > usage.Free {
			return fmt.Errorf("format upgrade needs cold-backup and rewrite space (source %d, uncopied %d, free %d bytes)", bytes, remaining, usage.Free)
		}
		manifest, err := os.CreateTemp(dir, "manifest-")
		if err != nil {
			return err
		}
		defer os.Remove(manifest.Name())
		defer manifest.Close()
		h := sha256.New()
		enc := json.NewEncoder(io.MultiWriter(manifest, h))
		for name, source := range sources {
			if err := walkUpgradeSource(source, func(path, rel string, d os.DirEntry) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				target := filepath.Join(backup, name, rel)
				if d.IsDir() {
					return privateDirectory(target)
				}
				info, err := d.Info()
				if err != nil {
					return err
				}
				if !info.Mode().IsRegular() {
					return fmt.Errorf("format upgrade refuses non-regular durable path: %s", path)
				}
				hash, err := copyUpgradeFile(ctx, path, target)
				if err != nil {
					return err
				}
				if err := enc.Encode(backupFile{Path: filepath.Join(name, rel), Size: info.Size(), SHA256: hash}); err != nil {
					return err
				}
				// gofail: var formatUpgradeBackupFile struct{}
				return nil
			}); err != nil {
				return err
			}
		}
		if err := manifest.Sync(); err != nil {
			return err
		}
		if err := manifest.Close(); err != nil {
			return err
		}
		if err := os.Rename(manifest.Name(), filepath.Join(dir, "backup-manifest.jsonl")); err != nil {
			return err
		}
		if err := syncUpgradeDir(dir); err != nil {
			return err
		}
		state.BackupSHA256 = hex.EncodeToString(h.Sum(nil))
		if err := writeUpgradeJSON(statePath, state); err != nil {
			return err
		}
	}
	if err := verifyUpgradeBackup(ctx, dir, state.BackupSHA256); err != nil {
		return err
	}
	// Release only the metadata lock before opening that engine. Message and
	// Raft locks remain owned through publication; all old nodes stay stopped.
	for i := len(locks) - 1; i >= 0; i-- {
		if locks[i].path == filepath.Join(root, "slotmeta", "LOCK") {
			if err := locks[i].closer.Close(); err != nil {
				return err
			}
			locks = append(locks[:i], locks[i+1:]...)
		}
	}
	if report != nil {
		report("metadata")
	}
	path := filepath.Join(root, "slotmeta")
	if entries, err := os.ReadDir(path); err != nil || len(entries) == 0 {
		return fmt.Errorf("format upgrade missing metadata engine: %w", err)
	}
	store, err := meta.Open(path)
	if err != nil {
		return err
	}
	err = store.UpgradeFormat1(ctx, func() {
		if report != nil {
			report("metadata batch committed")
		}
	})
	err = errors.Join(err, store.Close())
	if err != nil {
		return err
	}
	// gofail: var formatUpgradePublish struct{}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.Metadata.FormatVersion = 2
	if err := writeUpgradeJSON(filepath.Join(root, dataformat.FileName), r.Metadata); err != nil {
		return err
	}
	if report != nil {
		report("complete; cold backup: " + backup)
	}
	return nil
}

func walkUpgradeSource(root string, visit func(string, string, os.DirEntry) error) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == upgradeDir || rel == "backup-repository" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == ".format-upgrade.lock" {
			return nil
		}
		return visit(path, rel, d)
	})
}
func privateDirectory(path string) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe upgrade directory: %s", path)
		}
		return os.Chmod(path, 0700)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	return syncUpgradeDir(filepath.Dir(path))
}
func syncUpgradeDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func writeUpgradeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeUpgradeFile(path, append(b, '\n'))
}
func writeUpgradeFile(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".upgrade-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(b); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncUpgradeDir(filepath.Dir(path))
}
func hashUpgradeFile(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, upgradeReader{ctx: ctx, reader: f}); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func copyUpgradeFile(ctx context.Context, src, dst string) (string, error) {
	sourceHash, err := hashUpgradeFile(ctx, src)
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(dst); err == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("unsafe backup file: %s", dst)
		}
		hash, err := hashUpgradeFile(ctx, dst)
		if err != nil {
			return "", err
		}
		if hash == sourceHash {
			if err := os.Chmod(dst, 0600); err != nil {
				return "", err
			}
			return hash, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(dst), ".copy-")
	if err != nil {
		return "", err
	}
	defer os.Remove(out.Name())
	defer out.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), upgradeReader{ctx: ctx, reader: in}); err != nil {
		return "", err
	}
	if hex.EncodeToString(h.Sum(nil)) != sourceHash {
		return "", fmt.Errorf("source changed during backup")
	}
	if err := out.Sync(); err != nil {
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(out.Name(), dst); err != nil {
		return "", err
	}
	return sourceHash, syncUpgradeDir(filepath.Dir(dst))
}
func verifyUpgradeBackup(ctx context.Context, dir, want string) error {
	f, err := os.Open(filepath.Join(dir, "backup-manifest.jsonl"))
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	scan := bufio.NewScanner(io.TeeReader(f, h))
	scan.Buffer(make([]byte, 4096), 64<<10)
	records := 0
	for scan.Scan() {
		records++
		var row backupFile
		if err := json.Unmarshal(scan.Bytes(), &row); err != nil {
			return err
		}
		if row.Path == "" || filepath.IsAbs(row.Path) || filepath.Clean(row.Path) != row.Path || strings.HasPrefix(row.Path, "..") {
			return fmt.Errorf("invalid backup path")
		}
		path := filepath.Join(dir, "backup", row.Path)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() != row.Size {
			return fmt.Errorf("backup file changed: %s", row.Path)
		}
		got, err := hashUpgradeFile(ctx, path)
		if err != nil {
			return err
		}
		if got != row.SHA256 {
			return fmt.Errorf("backup checksum mismatch: %s", row.Path)
		}
	}
	if err := scan.Err(); err != nil {
		return err
	}
	files := 0
	if err := filepath.WalkDir(filepath.Join(dir, "backup"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files++
		}
		return nil
	}); err != nil {
		return err
	}
	if files != records {
		return fmt.Errorf("backup file inventory changed")
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		return fmt.Errorf("backup manifest checksum mismatch")
	}
	return nil
}

type upgradeReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r upgradeReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// Incomplete copies never enter the sealed manifest and can be discarded on
// retry. Source paths are never visited by this cleanup.
func cleanupUpgradePartial(dir, backup string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() && (strings.HasPrefix(e.Name(), "manifest-") || strings.HasPrefix(e.Name(), ".upgrade-")) {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return filepath.WalkDir(backup, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasPrefix(d.Name(), ".copy-") {
			return os.Remove(path)
		}
		return nil
	})
}
