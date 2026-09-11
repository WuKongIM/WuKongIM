// Package migrationv2 reads the fixed original v2 storage format. It never
// starts the old server or exposes a write path to its data directories.
package migrationv2

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/cockroachdb/pebble"
)

// SourceCommit identifies the unmodified v2 schema supported by this reader.
const SourceCommit = "a888f89533d0e7d1b2030e06504ca97f1ad891d4"

type Kind = migration.Kind

const (
	Primary        = migration.Primary
	Index          = migration.Index
	SecondaryIndex = migration.SecondaryIndex
	Other          = migration.Other
)

type Row = migration.Row
type Options = migration.Options

// POSIX record locks belong to a process: closing another descriptor for the
// same LOCK file would release an active reader's lock. Serialize source scans
// before opening any descriptors; parallel node scans are intentionally refused.
var sourceScanMu sync.Mutex

// Scan locks all existing source shards against the original v2 writer, opens
// one shard at a time in read-only mode, and visits every durable row/index.
// It validates known column shapes and fails on unknown primary data. This is
// a node reader, not a decision about which cluster replica is authoritative.
func Scan(ctx context.Context, opts Options, visit func(Row) error) (err error) {
	if ctx == nil {
		return errors.New("v2 scan requires a context")
	}
	if !sourceScanMu.TryLock() {
		return errors.New("v2 source scan already active")
	}
	defer sourceScanMu.Unlock()

	if opts.DataDir == "" || opts.ShardCount < 1 || opts.ShardCount > 1024 || visit == nil {
		return errors.New("v2 scan requires a directory, 1..1024 shards and a visitor")
	}
	if opts.MaxRowBytes == 0 {
		opts.MaxRowBytes = 64 << 20
	}
	if opts.MaxRowBytes < 1 {
		return errors.New("v2 max row bytes must be positive")
	}
	root := filepath.Join(opts.DataDir, "db", "wukongimdb")
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	if len(entries) != opts.ShardCount {
		return fmt.Errorf("v2 shard inventory differs: expected %d directories, found %d entries", opts.ShardCount, len(entries))
	}
	locks := make([]*sourceLock, 0, opts.ShardCount)
	defer func() {
		for _, lock := range locks {
			err = errors.Join(err, lock.Close())
		}
	}()
	paths := make([]string, opts.ShardCount)
	for shard := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		paths[shard] = filepath.Join(root, fmt.Sprintf("shard%03d", shard))
		info, err := os.Lstat(paths[shard])
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("v2 shard %d is not a regular directory", shard)
		}
		lock, err := lockSourceDirectory(paths[shard])
		if err != nil {
			return fmt.Errorf("lock v2 shard %d (stop the source first): %w", shard, err)
		}
		locks = append(locks, lock)
	}
	cache := pebble.NewCache(16 << 20)
	defer cache.Unref()
	for shard, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		db, err := openSourceDatabase(path, locks[shard], cache)
		if err != nil {
			return fmt.Errorf("open v2 shard %d: %w", shard, err)
		}
		scanErr := scanShard(ctx, db, shard, opts.MaxRowBytes, visit)
		if err := errors.Join(scanErr, db.Close()); err != nil {
			return fmt.Errorf("scan v2 shard %d: %w", shard, err)
		}
	}
	return nil
}

func scanShard(ctx context.Context, db *sourceDatabase, shard, maxBytes int, visit func(Row) error) (err error) {
	iter, err := db.NewIter()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, iter.Close()) }()
	var row *Row
	var rowBytes int
	flush := func() error {
		if row == nil {
			return nil
		}
		if row.Table == "Message" {
			for _, field := range []string{"MessageId", "MessageSeq", "ChannelId", "ChannelType", "Payload"} {
				if _, ok := row.Fields[field]; !ok {
					return fmt.Errorf("Message: missing %s", field)
				}
			}
		}
		err := visit(*row)
		row = nil
		rowBytes = 0
		return err
	}
	for valid := iter.First(); valid; valid = iter.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, val := iter.Key(), iter.Value()
		if len(key) < 4 || key[3] != 0 {
			return errors.New("invalid v2 key prefix")
		}
		table, ok := tables[binary.BigEndian.Uint16(key)]
		if !ok {
			return fmt.Errorf("unknown v2 table %x", key[:2])
		}
		kind := Kind(key[2])
		if kind < Primary || kind > Other {
			return fmt.Errorf("invalid v2 data kind %d", kind)
		}
		columnRow := kind == Primary && len(table.columns) != 0
		if columnRow {
			if len(key) != table.keyBytes {
				return fmt.Errorf("%s: invalid primary key length", table.name)
			}
			colID := binary.BigEndian.Uint16(key[len(key)-2:])
			col, ok := table.columns[colID]
			if !ok {
				return fmt.Errorf("%s: unknown column %04x", table.name, colID)
			}
			if col.size > 0 && len(val) != col.size {
				return fmt.Errorf("%s.%s: invalid scalar length", table.name, col.name)
			}
			if col.size == -8 && len(val)%8 != 0 {
				return fmt.Errorf("%s.%s: invalid packed integers", table.name, col.name)
			}
			base := key[:len(key)-2]
			if row == nil || !bytes.Equal(row.Key, base) {
				if err := flush(); err != nil {
					return err
				}
				row = &Row{Shard: shard, Table: table.name, Kind: kind, Key: bytes.Clone(base), ID: binary.BigEndian.Uint64(base[len(base)-8:]), Fields: map[string][]byte{}}
				if table.keyBytes == 22 {
					row.Owner = binary.BigEndian.Uint64(base[4:12])
				}
			}
			if len(key) > maxBytes-rowBytes || len(val) > maxBytes-rowBytes-len(key) {
				return fmt.Errorf("%s: row exceeds byte limit", table.name)
			}
			rowBytes += len(key) + len(val)
			row.Fields[col.name] = bytes.Clone(val)
			continue
		}
		if err := flush(); err != nil {
			return err
		}
		if len(key) > maxBytes || len(val) > maxBytes-len(key) {
			return fmt.Errorf("%s: record exceeds byte limit", table.name)
		}
		if kind == Primary && ((table.keyBytes > 0 && len(key) != table.keyBytes) || len(key) < 12) {
			return fmt.Errorf("%s: invalid primary key length", table.name)
		}
		record := Row{Shard: shard, Table: table.name, Kind: kind, Key: bytes.Clone(key), Value: bytes.Clone(val)}
		if isLegacyStream(record) {
			if _, err := decodeLegacyStream(record); err != nil {
				return err
			}
		}
		if err := visit(record); err != nil {
			return err
		}
	}
	if err := iter.Error(); err != nil {
		return err
	}
	return flush()
}
