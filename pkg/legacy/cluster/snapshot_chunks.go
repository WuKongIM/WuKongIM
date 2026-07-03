package cluster

import (
	"bytes"
	"fmt"
	"math"
	"sync"
	"time"

	"go.etcd.io/raft/v3/raftpb"
)

const defaultRaftSnapshotChunkTTL = 2 * time.Minute

type raftSnapshotChunkKey struct {
	slotID  uint64
	chunkID uint64
	from    uint64
	to      uint64
	index   uint64
	term    uint64
}

type raftSnapshotChunkAssembly struct {
	message    []byte
	total      uint64
	data       []byte
	chunks     map[uint64]int
	received   uint64
	lastUpdate time.Time
}

type assembledRaftSnapshot struct {
	slotID  uint64
	message raftpb.Message
}

// raftSnapshotAssembler rebuilds chunked Slot Raft snapshots before stepping Raft.
type raftSnapshotAssembler struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	pending map[raftSnapshotChunkKey]*raftSnapshotChunkAssembly
}

func newRaftSnapshotAssembler(ttl time.Duration, now func() time.Time) *raftSnapshotAssembler {
	if ttl <= 0 {
		ttl = defaultRaftSnapshotChunkTTL
	}
	if now == nil {
		now = time.Now
	}
	return &raftSnapshotAssembler{
		ttl:     ttl,
		now:     now,
		pending: make(map[raftSnapshotChunkKey]*raftSnapshotChunkAssembly),
	}
}

func (a *raftSnapshotAssembler) add(chunk raftSnapshotChunk) (assembledRaftSnapshot, bool, error) {
	if a == nil {
		return assembledRaftSnapshot{}, false, fmt.Errorf("raft snapshot assembler is nil")
	}
	if chunk.total == 0 {
		return assembledRaftSnapshot{}, false, fmt.Errorf("raft snapshot chunk total is zero")
	}
	if len(chunk.message) == 0 {
		return assembledRaftSnapshot{}, false, fmt.Errorf("raft snapshot chunk message is empty")
	}
	if chunk.offset > chunk.total || uint64(len(chunk.data)) > chunk.total-chunk.offset {
		return assembledRaftSnapshot{}, false, fmt.Errorf("raft snapshot chunk range out of bounds: offset=%d len=%d total=%d", chunk.offset, len(chunk.data), chunk.total)
	}

	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneExpiredLocked(now)

	key := raftSnapshotChunkKey{
		slotID:  chunk.slotID,
		chunkID: chunk.chunkID,
		from:    chunk.from,
		to:      chunk.to,
		index:   chunk.index,
		term:    chunk.term,
	}
	entry := a.pending[key]
	if entry == nil {
		if chunk.total > uint64(math.MaxInt) {
			return assembledRaftSnapshot{}, false, fmt.Errorf("raft snapshot total overflows int: %d", chunk.total)
		}
		entry = &raftSnapshotChunkAssembly{
			message:    append([]byte(nil), chunk.message...),
			total:      chunk.total,
			data:       make([]byte, int(chunk.total)),
			chunks:     make(map[uint64]int),
			lastUpdate: now,
		}
		a.pending[key] = entry
	}
	if entry.total != chunk.total {
		return assembledRaftSnapshot{}, false, fmt.Errorf("raft snapshot chunk total changed: got=%d want=%d", chunk.total, entry.total)
	}
	if !bytes.Equal(entry.message, chunk.message) {
		return assembledRaftSnapshot{}, false, fmt.Errorf("raft snapshot chunk message changed")
	}
	if existingLen, ok := entry.chunks[chunk.offset]; ok {
		if existingLen == len(chunk.data) && bytes.Equal(entry.data[chunk.offset:chunk.offset+uint64(existingLen)], chunk.data) {
			entry.lastUpdate = now
			return assembledRaftSnapshot{}, false, nil
		}
		return assembledRaftSnapshot{}, false, fmt.Errorf("raft snapshot chunk duplicate offset has different data: offset=%d", chunk.offset)
	}
	if overlapsExistingChunk(entry.chunks, chunk.offset, uint64(len(chunk.data))) {
		return assembledRaftSnapshot{}, false, fmt.Errorf("raft snapshot chunks overlap at offset %d", chunk.offset)
	}

	copy(entry.data[chunk.offset:chunk.offset+uint64(len(chunk.data))], chunk.data)
	entry.chunks[chunk.offset] = len(chunk.data)
	entry.received += uint64(len(chunk.data))
	entry.lastUpdate = now
	if entry.received < entry.total {
		return assembledRaftSnapshot{}, false, nil
	}

	assembled, err := assembleRaftSnapshotChunks(key.slotID, entry)
	if err != nil {
		delete(a.pending, key)
		return assembledRaftSnapshot{}, false, err
	}
	delete(a.pending, key)
	return assembled, true, nil
}

func (a *raftSnapshotAssembler) pruneExpiredLocked(now time.Time) {
	for key, entry := range a.pending {
		if now.Sub(entry.lastUpdate) > a.ttl {
			delete(a.pending, key)
		}
	}
}

func assembleRaftSnapshotChunks(slotID uint64, entry *raftSnapshotChunkAssembly) (assembledRaftSnapshot, error) {
	if entry.received != entry.total {
		return assembledRaftSnapshot{}, fmt.Errorf("raft snapshot chunks incomplete: got=%d want=%d", entry.received, entry.total)
	}

	var msg raftpb.Message
	if err := msg.Unmarshal(entry.message); err != nil {
		return assembledRaftSnapshot{}, fmt.Errorf("unmarshal raft snapshot chunk message: %w", err)
	}
	if msg.Type != raftpb.MsgSnap || msg.Snapshot == nil {
		return assembledRaftSnapshot{}, fmt.Errorf("raft snapshot chunk message is %s without snapshot", msg.Type)
	}
	msg.Snapshot.Data = entry.data
	return assembledRaftSnapshot{slotID: slotID, message: msg}, nil
}

func overlapsExistingChunk(chunks map[uint64]int, offset, length uint64) bool {
	if length == 0 {
		return false
	}
	end := offset + length
	for existingOffset, existingLen := range chunks {
		existingEnd := existingOffset + uint64(existingLen)
		if offset < existingEnd && existingOffset < end {
			return true
		}
	}
	return false
}
