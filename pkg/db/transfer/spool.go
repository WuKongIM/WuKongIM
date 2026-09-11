package transfer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// SpoolRow is one owned record in an offline migration's disk-backed sort/join
// workspace. These keys are not a product storage schema.
type SpoolRow struct{ Key, Value []byte }

// Spool bounds cache, memtables and each synchronous ingest batch. The caller
// serializes operations and Close. Walk callbacks may Get and Put keys outside
// their scanned prefix; the engine pins the iterator view. Identity is checked
// before its engine is opened for resume.
type Spool struct {
	db       *engine.DB
	maxBytes int
}

// OpenSpool creates or resumes only a workspace with the same immutable
// migration identity. It never adopts a nonempty unidentified directory.
func OpenSpool(path, identity string, maxBytes int) (*Spool, error) {
	if path == "" || identity == "" || len(identity) > 256 || maxBytes < 1 || maxBytes > 128<<20 {
		return nil, errors.New("invalid migration spool options")
	}
	created := false
	if err := os.Mkdir(path, 0700); err == nil {
		created = true
	} else if !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("migration spool must be a regular directory")
	}
	marker := filepath.Join(path, "IDENTITY")
	if created {
		f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		_, writeErr := f.WriteString(identity)
		err = errors.Join(writeErr, f.Sync(), f.Close())
		if err != nil {
			return nil, err
		}
		d, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		err = errors.Join(d.Sync(), d.Close())
		if err != nil {
			return nil, err
		}
	} else {
		info, err := os.Lstat(marker)
		if err != nil {
			return nil, fmt.Errorf("migration spool identity missing: %w", err)
		}
		if !info.Mode().IsRegular() || info.Size() > 256 {
			return nil, errors.New("invalid migration spool identity file")
		}
		data, err := os.ReadFile(marker)
		if err != nil {
			return nil, err
		}
		if string(data) != identity {
			return nil, errors.New("migration spool identity mismatch")
		}
	}
	dbPath := filepath.Join(path, "rows")
	if info, err := os.Lstat(dbPath); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("invalid migration spool rows directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	// Pebble charges active, pinned and recycled memtables against its cache
	// budget. One full 16 MiB memtable exhausted the old 16 MiB budget, making
	// every index-join lookup reread its SST blocks. Keep bounded headroom for
	// block reads while the migration walks source rows and writes indexes.
	db, err := engine.Open(dbPath, engine.Options{CacheSize: 128 << 20, MemTableSize: 16 << 20})
	if err != nil {
		return nil, err
	}
	return &Spool{db: db, maxBytes: maxBytes}, nil
}

// Put atomically persists an exact bounded batch; retries may repeat identical
// rows. A conflicting value aborts the entire batch without overwriting data.
func (s *Spool) Put(ctx context.Context, rows []SpoolRow) error {
	if ctx == nil {
		return errors.New("migration spool requires context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.db == nil {
		return errors.New("migration spool closed")
	}
	if len(rows) > 4096 {
		return errors.New("migration spool batch exceeds row limit")
	}
	used := 0
	seen := make(map[string][]byte, len(rows))
	for _, row := range rows {
		if len(row.Key) == 0 || len(row.Key) > s.maxBytes-used || len(row.Value) > s.maxBytes-used-len(row.Key) {
			return errors.New("migration spool batch exceeds byte limit")
		}
		used += len(row.Key) + len(row.Value)
		if v, exists := seen[string(row.Key)]; exists && !bytes.Equal(v, row.Value) {
			return errors.New("migration spool batch key conflict")
		}
		seen[string(row.Key)] = row.Value
	}
	batch := s.db.NewBatch()
	defer batch.Close()
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		old, exists, err := s.db.Get(row.Key)
		if err != nil {
			return err
		}
		if exists {
			if !bytes.Equal(old, row.Value) {
				return errors.New("migration spool durable key conflict")
			}
			continue
		}
		if err := batch.Set(row.Key, row.Value); err != nil {
			return err
		}
	}
	return batch.Commit(true)
}

// Get returns owned bytes for an exact migration key.
func (s *Spool) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	if ctx == nil {
		return nil, false, errors.New("migration spool requires context")
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if s == nil || s.db == nil {
		return nil, false, errors.New("migration spool closed")
	}
	return s.db.Get(key)
}

// Walk streams lexicographically ordered rows under prefix without building
// an in-memory table. A nil or empty prefix visits the complete workspace.
func (s *Spool) Walk(ctx context.Context, prefix []byte, visit func(SpoolRow) error) (err error) {
	if ctx == nil || visit == nil {
		return errors.New("migration spool requires context and visitor")
	}
	if s == nil || s.db == nil {
		return errors.New("migration spool closed")
	}
	iter, err := s.db.NewIter(spoolPrefixSpan(prefix), engine.IterOptions{})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, iter.Close()) }()
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := iter.Key()
		if !bytes.HasPrefix(key, prefix) {
			break
		}
		val, err := iter.Value()
		if err != nil {
			return err
		}
		if err := visit(SpoolRow{Key: key, Value: val}); err != nil {
			return err
		}
	}
	return iter.Error()
}

// Close releases the offline workspace's engine and exclusive file lock.
func (s *Spool) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	db := s.db
	s.db = nil
	return db.Close()
}
