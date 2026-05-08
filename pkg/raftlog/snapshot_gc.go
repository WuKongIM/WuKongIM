package raftlog

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/pebble/v2"
)

// runSnapshotGC removes inactive external snapshot directories that are not referenced by Pebble manifests.
func (db *DB) runSnapshotGC(ctx context.Context) error {
	if db == nil || db.snapshotStore == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	db.snapshotLifecycleMu.Lock()
	defer db.snapshotLifecycleMu.Unlock()

	if db.snapshotGCTestHook != nil {
		db.snapshotGCTestHook()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	live, err := db.liveSnapshotPathsLocked()
	if err != nil {
		return err
	}
	return db.collectSnapshotGarbageLocked(ctx, live, time.Now())
}

func (db *DB) liveSnapshotPathsLocked() (map[string]struct{}, error) {
	live := make(map[string]struct{})
	iter, err := db.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte{currentFormatVersion},
		UpperBound: nextPrefix([]byte{currentFormatVersion}),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	for valid := iter.First(); valid; valid = iter.Next() {
		key := iter.Key()
		if !isSnapshotManifestKey(key) {
			continue
		}
		scope := Scope{Kind: ScopeKind(key[1]), ID: binary.BigEndian.Uint64(key[2:10])}
		value := iter.Value()
		manifest, err := decodeSnapshotManifest(scope, value)
		if err != nil {
			if hasSnapshotManifestMagic(value) {
				return nil, err
			}
			// Task 4 still coexists with legacy inline raftpb.Snapshot values.
			continue
		}
		live[normalizeSnapshotLifecyclePath(filepath.Join(db.snapshotStore.scopeDir(scope), manifest.SnapshotID))] = struct{}{}
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return live, nil
}

func isSnapshotManifestKey(key []byte) bool {
	return len(key) == 11 && key[0] == currentFormatVersion && isValidScopeKind(ScopeKind(key[1])) && key[10] == recordTypeSnapshot
}

func (db *DB) collectSnapshotGarbageLocked(ctx context.Context, live map[string]struct{}, now time.Time) error {
	root := normalizeSnapshotLifecyclePath(db.snapshotStore.root)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() {
			continue
		}
		scope, ok := parseSnapshotScopeDir(entry.Name())
		if !ok {
			continue
		}
		scopeDir := filepath.Join(root, entry.Name())
		if !sameCleanPath(scopeDir, db.snapshotStore.scopeDir(scope)) {
			continue
		}
		db.collectSnapshotScopeGarbageLocked(ctx, root, scopeDir, live, now)
	}
	return nil
}

func (db *DB) collectSnapshotScopeGarbageLocked(ctx context.Context, root, scopeDir string, live map[string]struct{}, now time.Time) {
	entries, err := os.ReadDir(scopeDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		path := filepath.Join(scopeDir, name)
		if !isSnapshotGCPathInsideRoot(root, path) {
			continue
		}

		path = normalizeSnapshotLifecyclePath(path)
		if !db.shouldDeleteSnapshotDirLocked(name, path, live) {
			continue
		}
		if db.activeSnapshotPaths[path] > 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil || !snapshotGCExpired(info.ModTime(), now, db.options.SnapshotGCGrace) {
			continue
		}
		_ = os.RemoveAll(path)
	}
}

func (db *DB) shouldDeleteSnapshotDirLocked(name, path string, live map[string]struct{}) bool {
	if strings.HasPrefix(name, ".tmp-snap-") {
		return validateSnapshotID(strings.TrimPrefix(name, ".tmp-")) == nil
	}
	if validateSnapshotID(name) != nil {
		return false
	}
	_, referenced := live[path]
	return !referenced
}

func parseSnapshotScopeDir(name string) (Scope, bool) {
	if raw, ok := strings.CutPrefix(name, "slot-"); ok {
		id, err := strconv.ParseUint(raw, 10, 64)
		if err == nil {
			return SlotScope(id), true
		}
	}
	if raw, ok := strings.CutPrefix(name, "controller-"); ok {
		id, err := strconv.ParseUint(raw, 10, 64)
		if err == nil {
			return Scope{Kind: ScopeController, ID: id}, true
		}
	}
	return Scope{}, false
}

func snapshotGCExpired(modTime, now time.Time, grace time.Duration) bool {
	if grace <= 0 {
		return true
	}
	return !modTime.After(now.Add(-grace))
}

func isSnapshotGCPathInsideRoot(root, path string) bool {
	root = normalizeSnapshotLifecyclePath(root)
	path = normalizeSnapshotLifecyclePath(path)
	if root == "" || path == "" || sameCleanPath(root, path) {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return false
	}
	return true
}

func normalizeSnapshotLifecyclePath(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func sameCleanPath(a, b string) bool {
	return normalizeSnapshotLifecyclePath(a) == normalizeSnapshotLifecyclePath(b)
}
