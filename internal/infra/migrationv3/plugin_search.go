package migrationv3

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/vfs"
)

func searchSandbox(node migration.TargetNode) string {
	return filepath.Join(node.DataDir, "plugin-sandbox", migration.SearchPluginNo)
}
func hasSearchProfile(node migration.TargetNode, report *migration.PluginArtifactsReport) bool {
	spec, ok := pluginArtifactAssignments(node.NodeID, report)[migration.SearchPluginNo]
	return ok && spec.Profile == migration.SearchPersistRouteProfile
}

// installSearchCheckpoints publishes only sequence-zero startup seeds. The
// audited plugin rebuilds a fresh Bleve index by reading committed native history.
// Old index bytes and sequence cursors are never copied across generations.
func installSearchCheckpoints(ctx context.Context, node migration.TargetNode, w migration.Workspace) (err error) {
	sandbox := searchSandbox(node)
	if _, err := os.Lstat(sandbox); err == nil {
		return migration.VerifySearchCheckpoints(ctx, w, searchCheckpointReader{node})
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(sandbox)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(parent); err != nil {
		return err
	} else if !info.IsDir() {
		return errors.New("search sandbox parent is not a real directory")
	}
	partial := sandbox + ".wkmigrate-partial"
	if err := os.MkdirAll(partial, 0700); err != nil {
		return err
	}
	if err := regularSearchTree(partial); err != nil {
		return err
	}
	cache := pebble.NewCache(16 << 20)
	defer cache.Unref()
	db, err := pebble.Open(filepath.Join(partial, "db"), &pebble.Options{Cache: cache, FormatMajorVersion: pebble.FormatNewest, MaxOpenFiles: 128})
	if err != nil {
		return err
	}
	batch := db.NewBatch()
	pending := 0
	flush := func() error {
		if pending == 0 {
			return nil
		}
		if err := batch.Commit(pebble.Sync); err != nil {
			return err
		}
		batch.Reset()
		pending = 0
		return nil
	}
	walkErr := migration.WalkTargetChannels(ctx, w, func(c migration.TargetChannel) error {
		if c.Count == 0 {
			return nil
		}
		if strings.TrimSpace(c.Channel.ID) == "" {
			return errors.New("search history channel ID is empty")
		}
		key := searchCheckpointKey(c.Channel)
		old, closer, e := db.Get(key)
		if e == nil {
			same := bytes.Equal(old, make([]byte, 8))
			closer.Close()
			if !same {
				return errors.New("search partial cursor differs from import")
			}
			return nil
		}
		if !errors.Is(e, pebble.ErrNotFound) {
			return e
		}
		if err := batch.Set(key, make([]byte, 8), nil); err != nil {
			return err
		}
		pending++
		if pending >= 1024 {
			return flush()
		}
		return nil
	})
	if walkErr == nil {
		walkErr = flush()
	}
	err = errors.Join(walkErr, batch.Close(), db.Close())
	if err != nil {
		return err
	}
	if err := os.Rename(partial, sandbox); err != nil {
		return err
	}
	if err := syncDir(parent); err != nil {
		return err
	}
	return migration.VerifySearchCheckpoints(ctx, w, searchCheckpointReader{node})
}
func searchCheckpointKey(id migration.ChannelIdentity) []byte {
	return []byte(fmt.Sprintf("channel_msg_max_seq:%s:%d", id.ID, id.Type))
}

func regularSearchTree(dir string) error {
	return filepath.WalkDir(dir, func(_ string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.Type()&os.ModeSymlink != 0 || (!e.IsDir() && !e.Type().IsRegular()) {
			return errors.New("search checkpoint tree contains a non-regular entry")
		}
		return nil
	})
}

type searchCheckpointReader struct{ node migration.TargetNode }

func (v *nativeView) WalkSearchCheckpoints(ctx context.Context, visit func(migration.ChannelIdentity, uint64) error) error {
	return (searchCheckpointReader{v.node}).WalkSearchCheckpoints(ctx, visit)
}
func (r searchCheckpointReader) WalkSearchCheckpoints(ctx context.Context, visit func(migration.ChannelIdentity, uint64) error) (err error) {
	sandbox := searchSandbox(r.node)
	if err := regularSearchTree(sandbox); err != nil {
		return err
	}
	entries, err := os.ReadDir(sandbox)
	if err != nil {
		return err
	}
	if len(entries) != 1 || entries[0].Name() != "db" || !entries[0].IsDir() {
		return errors.New("search sandbox must contain only unstarted rebuild checkpoints")
	}
	cache := pebble.NewCache(16 << 20)
	defer cache.Unref()
	db, err := pebble.Open(filepath.Join(sandbox, "db"), &pebble.Options{ReadOnly: true, ErrorIfNotExists: true, FS: searchReadOnlyFS{FS: vfs.Default}, Cache: cache, MaxOpenFiles: 128})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	iter := db.NewIter(nil)
	defer func() { err = errors.Join(err, iter.Close()) }()
	const prefix = "channel_msg_max_seq:"
	for iter.First(); iter.Valid(); iter.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := string(iter.Key())
		if !strings.HasPrefix(key, prefix) {
			return errors.New("unexpected search checkpoint key")
		}
		suffix := strings.TrimPrefix(key, prefix)
		at := strings.LastIndexByte(suffix, ':')
		if at <= 0 {
			return errors.New("invalid search checkpoint channel")
		}
		typ, err := strconv.ParseUint(suffix[at+1:], 10, 8)
		if err != nil {
			return err
		}
		id := migration.ChannelIdentity{ID: suffix[:at], Type: uint8(typ)}
		if !bytes.Equal(iter.Key(), searchCheckpointKey(id)) || len(iter.Value()) != 8 {
			return errors.New("invalid search checkpoint encoding")
		}
		if err := visit(id, binary.BigEndian.Uint64(iter.Value())); err != nil {
			return err
		}
	}
	return iter.Error()
}

// searchReadOnlyFS keeps offline verification from creating or truncating LOCK
// or changing plugin state. The shared fcntl lock excludes the original writer.
type searchReadOnlyFS struct{ vfs.FS }

var errSearchReadOnly = errors.New("search checkpoint verification is read-only")

func (searchReadOnlyFS) Lock(n string) (io.Closer, error) { return lockTarget(n) }
func (searchReadOnlyFS) Create(string) (vfs.File, error)  { return nil, errSearchReadOnly }
func (searchReadOnlyFS) ReuseForWrite(string, string) (vfs.File, error) {
	return nil, errSearchReadOnly
}
func (searchReadOnlyFS) Link(string, string) error          { return errSearchReadOnly }
func (searchReadOnlyFS) Remove(string) error                { return errSearchReadOnly }
func (searchReadOnlyFS) RemoveAll(string) error             { return errSearchReadOnly }
func (searchReadOnlyFS) Rename(string, string) error        { return errSearchReadOnly }
func (searchReadOnlyFS) MkdirAll(string, os.FileMode) error { return errSearchReadOnly }
