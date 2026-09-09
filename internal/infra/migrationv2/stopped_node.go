package migrationv2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/cockroachdb/pebble"
)

type NodeOptions = migration.NodeOptions
type SourceConfig = migration.SourceConfig
type SourceNode = migration.SourceNode
type SourceSlot = migration.SourceSlot
type SourceFile = migration.SourceFile
type NodeSnapshot = migration.NodeSnapshot

// ReadStoppedNode locks every business, Slot and configuration DB before
// visiting immutable files and rows. Visitors must not mutate or reopen source
// LOCK files. Missing, changing, active, or ambiguous source state fails closed.
// Only one source reader may run in this process; different nodes are scanned
// sequentially and must be rechecked against their bound digests at export.
func ReadStoppedNode(ctx context.Context, opts NodeOptions, visit func(Row) error, files func(SourceFile) error) (snapshot NodeSnapshot, err error) {
	return readStoppedNode(ctx, opts, visit, files, nil)
}

func readStoppedNode(ctx context.Context, opts NodeOptions, visit func(Row) error, files func(SourceFile) error, logs func(uint32, uint64, uint32, []byte) error) (snapshot NodeSnapshot, err error) {
	if ctx == nil || opts.NodeID == 0 || opts.DataDir == "" || opts.ShardCount < 1 || opts.ShardCount > 1024 || visit == nil {
		return snapshot, errors.New("stopped v2 reader requires node identity, directory, 1..1024 shards and visitor")
	}
	if !sourceScanMu.TryLock() {
		return snapshot, errors.New("v2 source scan already active")
	}
	defer sourceScanMu.Unlock()
	if opts.MaxRowBytes == 0 {
		opts.MaxRowBytes = 64 << 20
	}
	if opts.MaxRowBytes < 1 {
		return snapshot, errors.New("v2 max row bytes must be positive")
	}
	root, err := filepath.Abs(opts.DataDir)
	if err != nil {
		return snapshot, err
	}
	if err := regularDirectory(root); err != nil {
		return snapshot, err
	}
	business, err := shardPaths(filepath.Join(root, "db", "wukongimdb"), opts.ShardCount)
	if err != nil {
		return snapshot, err
	}
	slotPaths, err := shardPaths(filepath.Join(root, "cluster", "logdb"), 0)
	if err != nil {
		return snapshot, err
	}
	cfgPath := filepath.Join(root, "cluster", "config", "cfglogdb")
	if err := regularDirectory(cfgPath); err != nil {
		return snapshot, err
	}
	paths := append(append(append([]string{}, business...), slotPaths...), cfgPath)
	locks := make([]*sourceLock, 0, len(paths))
	defer func() {
		for _, lock := range locks {
			err = errors.Join(err, lock.Close())
		}
		if err != nil {
			snapshot = NodeSnapshot{}
		}
	}()
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return snapshot, err
		}
		info, err := os.Lstat(filepath.Join(p, "LOCK"))
		if err != nil {
			return snapshot, err
		}
		if !info.Mode().IsRegular() || info.Size() != 0 {
			return snapshot, fmt.Errorf("invalid v2 LOCK file: %s", p)
		}
		lock, err := lockSourceDirectory(p)
		if err != nil {
			return snapshot, fmt.Errorf("stop all source processes before reading %s: %w", p, err)
		}
		locks = append(locks, lock)
	}
	before, count, size, err := digestSourceTree(ctx, root, files)
	if err != nil {
		return snapshot, err
	}
	data, err := readBoundedFile(filepath.Join(root, "cluster", "config", "remote.json"), 16<<20)
	if err != nil {
		return snapshot, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&snapshot.Config); err != nil {
		return snapshot, fmt.Errorf("v2 source configuration: %w", err)
	}
	if err := requireJSONEnd(dec); err != nil {
		return snapshot, err
	}
	if err := validateSourceConfig(snapshot.Config, opts.NodeID); err != nil {
		return snapshot, err
	}
	snapshot.NotificationDepth, snapshot.NotificationMetadataPresent, err = readNotificationDepth(root)
	if err != nil {
		return snapshot, err
	}
	cache := pebble.NewCache(16 << 20)
	defer cache.Unref()
	for shard, p := range business {
		db, err := openSourceDatabase(p, locks[shard], cache)
		if err != nil {
			return snapshot, err
		}
		scanErr := scanShard(ctx, db, shard, opts.MaxRowBytes, func(row Row) error { snapshot.RowCount++; return visit(row) })
		if err := errors.Join(scanErr, db.Close()); err != nil {
			return snapshot, err
		}
	}

	if err := scanConversationTail(ctx, root, opts.MaxRowBytes, func(row Row) error { snapshot.RowCount++; return visit(row) }); err != nil {
		return snapshot, err
	}
	snapshot.SlotProgress, err = readRaftProgressVisit(ctx, slotPaths, locks[len(business):len(business)+len(slotPaths)], snapshot.Config.SlotCount, opts.MaxRowBytes, cache, logs)
	if err != nil {
		return snapshot, err
	}
	cfgProgress, err := readRaftProgress(ctx, []string{cfgPath}, locks[len(locks)-1:], 0, opts.MaxRowBytes, cache)
	if err != nil {
		return snapshot, err
	}
	snapshot.ConfigProgress = cfgProgress[0]
	after, _, _, err := digestSourceTree(ctx, root, nil)
	if err != nil {
		return snapshot, err
	}
	if before != after {
		return snapshot, errors.New("v2 source changed during stopped scan")
	}
	snapshot.NodeID, snapshot.SlotShardCount = opts.NodeID, len(slotPaths)
	snapshot.DataDigest, snapshot.FileCount, snapshot.FileBytes = before, count, size
	return snapshot, nil
}

func validateSourceConfig(c SourceConfig, nodeID uint64) error {
	if c.Version == 0 || c.Term == 0 || c.SlotCount == 0 || c.SlotCount > 65536 || len(c.Slots) != int(c.SlotCount) || len(c.Nodes) == 0 || len(c.Nodes) > 1024 {
		return errors.New("uninitialized or incomplete v2 cluster configuration")
	}
	if c.MigrateFrom != 0 || c.MigrateTo != 0 || len(c.Learners) != 0 {
		return errors.New("v2 cluster has unfinished membership changes")
	}
	nodes := map[uint64]SourceNode{}
	for _, n := range c.Nodes {
		if _, exists := nodes[n.ID]; n.ID == 0 || exists || n.Status != 3 || n.Role < 0 || n.Role > 1 {
			return errors.New("invalid, duplicate or transitioning v2 node")
		}
		nodes[n.ID] = n
	}
	if _, ok := nodes[nodeID]; !ok {
		return errors.New("source node identity absent from v2 cluster configuration")
	}
	seen := map[uint32]bool{}
	for _, s := range c.Slots {
		if s.ID >= c.SlotCount || seen[s.ID] || s.Leader == 0 || s.Term == 0 || len(s.Replicas) == 0 {
			return errors.New("invalid or incomplete v2 Slot ownership")
		}
		seen[s.ID] = true
		if s.MigrateFrom != 0 || s.MigrateTo != 0 || s.ExpectLeader != 0 || s.Status != 0 || len(s.Learners) != 0 {
			return fmt.Errorf("v2 Slot %d has unfinished ownership changes", s.ID)
		}
		replicas := map[uint64]bool{}
		for _, id := range s.Replicas {
			n, ok := nodes[id]
			if !ok || replicas[id] || !n.AllowVote || n.Role != 0 {
				return fmt.Errorf("invalid v2 Slot %d replica", s.ID)
			}
			replicas[id] = true
		}
		if !replicas[s.Leader] {
			return fmt.Errorf("v2 Slot %d leader is outside its replica group", s.ID)
		}
	}
	return nil
}

func regularDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("source is not a regular directory: %s", path)
	}
	return nil
}

func shardPaths(root string, expected int) ([]string, error) {
	if err := regularDirectory(root); err != nil {
		return nil, err
	}
	entries, err := boundedDirectory(root, 1024)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 || (expected != 0 && len(entries) != expected) {
		return nil, fmt.Errorf("v2 shard inventory differs at %s", root)
	}
	result := make([]string, len(entries))
	for i, ent := range entries {
		if ent.Name() != fmt.Sprintf("shard%03d", i) || !ent.IsDir() {
			return nil, fmt.Errorf("unexpected v2 shard entry %s", ent.Name())
		}
		result[i] = filepath.Join(root, ent.Name())
	}
	return result, nil
}

func boundedDirectory(path string, limit int) ([]os.DirEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	entries, readErr := f.ReadDir(limit + 1)
	closeErr := f.Close()
	if readErr == io.EOF {
		readErr = nil
	}
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	if len(entries) > limit {
		return nil, fmt.Errorf("source directory exceeds %d entries: %s", limit, path)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

func digestSourceTree(ctx context.Context, root string, visit func(SourceFile) error) (string, uint64, uint64, error) {
	h := sha256.New()
	enc := json.NewEncoder(h)
	var count, size uint64
	buffer := make([]byte, 256<<10)
	var walk func(string, int) error
	walk = func(dir string, depth int) error {
		if depth > 32 {
			return errors.New("source directory nesting exceeds limit")
		}
		entries, err := boundedDirectory(dir, 65536)
		if err != nil {
			return err
		}
		for _, ent := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			p := filepath.Join(dir, ent.Name())
			info, err := ent.Info()
			if err != nil {
				return err
			}
			if info.IsDir() {
				if err := walk(p, depth+1); err != nil {
					return err
				}
				continue
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("source contains non-regular file: %s", p)
			}
			count++
			if count > 1000000 {
				return errors.New("source exceeds one million files")
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			record := SourceFile{Path: filepath.ToSlash(rel), Size: info.Size()}
			if ent.Name() != "LOCK" {
				f, err := os.Open(p)
				if err != nil {
					return err
				}
				fh := sha256.New()
				n, readErr := io.CopyBuffer(fh, contextReader{ctx, f}, buffer)
				after, statErr := f.Stat()
				closeErr := f.Close()
				if err := errors.Join(readErr, statErr, closeErr); err != nil {
					return err
				}
				if n != info.Size() || !os.SameFile(info, after) || after.Size() != n || !after.ModTime().Equal(info.ModTime()) {
					return fmt.Errorf("source file changed while reading: %s", rel)
				}
				record.SHA256 = hex.EncodeToString(fh.Sum(nil))
			} else if info.Size() != 0 {
				return errors.New("source LOCK is not empty")
			}
			size += uint64(info.Size())
			if err := enc.Encode(record); err != nil {
				return err
			}
			if visit != nil {
				if err := visit(record); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root, 0); err != nil {
		return "", 0, 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), count, size, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func readBoundedFile(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("source file exceeds byte limit: %s", path)
	}
	return b, nil
}
func requireJSONEnd(dec *json.Decoder) error {
	var extra any
	err := dec.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return errors.New("unexpected trailing JSON value")
	}
	return err
}
func readNotificationDepth(root string) (int64, bool, error) {
	p := filepath.Join(root, "diskqueue", "wk_webhook_q.diskqueue.meta.dat")
	data, err := readBoundedFile(p, 4096)
	if errors.Is(err, os.ErrNotExist) {
		entries, listErr := os.ReadDir(filepath.Dir(p))
		if errors.Is(listErr, os.ErrNotExist) || (listErr == nil && len(entries) == 0) {
			return 0, false, nil
		}
		return 0, false, errors.New("v2 notification queue files lack durable queue metadata")
	}
	if err != nil {
		return 0, false, err
	}
	var depth, rfile, rpos, wfile, wpos int64
	var suffix string
	n, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d\n%d,%d\n%d,%d%s", &depth, &rfile, &rpos, &wfile, &wpos, &suffix)
	if n != 5 || (err != nil && err != io.EOF) || depth < 0 || rfile < 0 || rpos < 0 || wfile < rfile || wpos < 0 || (wfile == rfile && wpos < rpos) || (depth == 0 && (rfile != wfile || rpos != wpos)) {
		return 0, false, errors.New("invalid v2 notification queue metadata")
	}
	return depth, true, nil
}
