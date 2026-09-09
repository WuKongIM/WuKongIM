package migrationv2

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"hash/fnv"
	"strconv"

	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/cockroachdb/pebble"
)

type LogProgress = migration.LogProgress

type logGroup struct {
	progress LogProgress
	digest   hash.Hash
	shard    int
}

func readRaftProgress(ctx context.Context, paths []string, locks []*sourceLock, slots uint32, maxBytes int, cache *pebble.Cache) ([]LogProgress, error) {
	return readRaftProgressVisit(ctx, paths, locks, slots, maxBytes, cache, nil)
}

func readRaftProgressVisit(ctx context.Context, paths []string, locks []*sourceLock, slots uint32, maxBytes int, cache *pebble.Cache, visit func(uint32, uint64, uint32, []byte) error) ([]LogProgress, error) {
	// A single configuration DB uses the empty key suffix. Slot DBs hash the
	// decimal Slot ID using original FNV-1 32 (placement) and FNV-1a 64 (keys).
	groups := map[uint64]*logGroup{}
	count := int(slots)
	if slots == 0 {
		count = 1
	}
	ordered := make([]*logGroup, count)
	for i := range ordered {
		group := strconv.Itoa(i)
		var keyHash uint64
		shard := 0
		if slots > 0 {
			h64 := fnv.New64a()
			_, _ = h64.Write([]byte(group))
			keyHash = h64.Sum64()
			h32 := fnv.New32()
			_, _ = h32.Write([]byte(group))
			shard = int(h32.Sum32() % uint32(len(paths)))
		} else {
			group = "clusterconfig"
		}
		if _, exists := groups[keyHash]; exists {
			return nil, errors.New("v2 Slot key hash collision")
		}
		g := &logGroup{progress: LogProgress{Group: group}, digest: sha256.New(), shard: shard}
		groups[keyHash] = g
		ordered[i] = g
	}
	for shard, p := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		db, err := openSourceDatabase(p, locks[shard], cache)
		if err != nil {
			return nil, err
		}
		err = scanRaftDBVisit(ctx, db, shard, slots != 0, maxBytes, groups, visit)
		if err = errors.Join(err, db.Close()); err != nil {
			return nil, fmt.Errorf("v2 raft %s: %w", p, err)
		}
	}
	result := make([]LogProgress, len(ordered))
	for i, g := range ordered {
		if g.progress.AppliedIndex > g.progress.LastIndex || (g.progress.AppliedIndex > 0 && g.progress.FirstIndex > g.progress.AppliedIndex) {
			return nil, fmt.Errorf("v2 raft %s applied index lacks retained log evidence", g.progress.Group)
		}
		g.progress.LogDigest = hex.EncodeToString(g.digest.Sum(nil))
		result[i] = g.progress
	}
	return result, nil
}

func scanRaftDB(ctx context.Context, db *sourceDatabase, shard int, slotted bool, maxBytes int, groups map[uint64]*logGroup) (err error) {
	return scanRaftDBVisit(ctx, db, shard, slotted, maxBytes, groups, nil)
}

func scanRaftDBVisit(ctx context.Context, db *sourceDatabase, shard int, slotted bool, maxBytes int, groups map[uint64]*logGroup, visit func(uint32, uint64, uint32, []byte) error) (err error) {
	iter, err := db.NewIter()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, iter.Close()) }()
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		k, v := iter.Key(), iter.Value()
		prefix := 4
		if slotted {
			prefix = 12
		}
		if len(k) < prefix || k[2] != 0 || k[3] != 0 || len(v) > maxBytes {
			return errors.New("invalid v2 raft key or record size")
		}
		var groupHash uint64
		if slotted {
			groupHash = binary.BigEndian.Uint64(k[4:12])
		}
		g, ok := groups[groupHash]
		if !ok || g.shard != shard {
			return errors.New("v2 raft group outside configured Slot inventory")
		}
		switch binary.BigEndian.Uint16(k) {
		case 0x0101:
			if len(k) != prefix+8 || len(v) < 28 {
				return errors.New("invalid v2 raft log encoding")
			}
			index := binary.BigEndian.Uint64(k[prefix:])
			term := binary.BigEndian.Uint32(v[16:20])
			if index == 0 || index != binary.BigEndian.Uint64(v[8:16]) || term == 0 || (g.progress.LastIndex != 0 && index != g.progress.LastIndex+1) {
				return errors.New("v2 raft log index, term or continuity mismatch")
			}
			digest := sha256.Sum256(v[:len(v)-8])
			_, _ = g.digest.Write(digest[:])
			if g.progress.FirstIndex == 0 {
				g.progress.FirstIndex = index
			}
			g.progress.LastIndex, g.progress.LastTerm, g.progress.LastDigest = index, term, hex.EncodeToString(digest[:])
			if visit != nil && slotted {
				slot, err := strconv.ParseUint(g.progress.Group, 10, 32)
				if err != nil {
					return err
				}
				if err := visit(uint32(slot), index, term, v[20:len(v)-8]); err != nil {
					return err
				}
			}
		case 0x0202:
			if len(k) != prefix || len(v) != 16 {
				return errors.New("invalid v2 raft applied encoding")
			}
			g.progress.AppliedIndex = binary.BigEndian.Uint64(v)
		case 0x0303:
			if len(k) != prefix || len(v) != 16 {
				return errors.New("invalid v2 raft max index encoding")
			}
		case 0x0404:
			keySize := prefix + 4
			if !slotted {
				keySize = 12
			}
			if len(k) != keySize || len(v) != 8 || binary.BigEndian.Uint32(k[prefix:]) == 0 {
				return errors.New("invalid v2 raft term boundary encoding")
			}
		default:
			return fmt.Errorf("unknown v2 raft key %x", k[:2])
		}
	}
	return iter.Error()
}
