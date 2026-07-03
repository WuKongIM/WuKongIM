package raft

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"time"

	"go.etcd.io/raft/v3/raftpb"
)

const (
	defaultControllerRaftSnapshotChunkTTL = 2 * time.Minute
	controllerRaftSnapshotChunkHeaderSize = 72
)

type controllerRaftSnapshotChunkKey struct {
	chunkID uint64
	from    uint64
	to      uint64
	index   uint64
	term    uint64
}

// controllerRaftSnapshotChunk is one bounded fragment of a Controller Raft MsgSnap.
type controllerRaftSnapshotChunk struct {
	chunkID uint64
	from    uint64
	to      uint64
	index   uint64
	term    uint64
	total   uint64
	offset  uint64
	message []byte
	data    []byte
}

type controllerRaftSnapshotChunkAssembly struct {
	message    []byte
	total      uint64
	data       []byte
	chunks     map[uint64]int
	received   uint64
	lastUpdate time.Time
}

// controllerRaftSnapshotAssembler rebuilds chunked Controller Raft snapshots before stepping Raft.
type controllerRaftSnapshotAssembler struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	pending map[controllerRaftSnapshotChunkKey]*controllerRaftSnapshotChunkAssembly
}

func newControllerRaftSnapshotAssembler(ttl time.Duration, now func() time.Time) *controllerRaftSnapshotAssembler {
	if ttl <= 0 {
		ttl = defaultControllerRaftSnapshotChunkTTL
	}
	if now == nil {
		now = time.Now
	}
	return &controllerRaftSnapshotAssembler{
		ttl:     ttl,
		now:     now,
		pending: make(map[controllerRaftSnapshotChunkKey]*controllerRaftSnapshotChunkAssembly),
	}
}

func (a *controllerRaftSnapshotAssembler) add(chunk controllerRaftSnapshotChunk) (raftpb.Message, bool, error) {
	if a == nil {
		return raftpb.Message{}, false, fmt.Errorf("controller raft snapshot assembler is nil")
	}
	if chunk.total == 0 {
		return raftpb.Message{}, false, fmt.Errorf("controller raft snapshot chunk total is zero")
	}
	if len(chunk.message) == 0 {
		return raftpb.Message{}, false, fmt.Errorf("controller raft snapshot chunk message is empty")
	}
	if chunk.offset > chunk.total || uint64(len(chunk.data)) > chunk.total-chunk.offset {
		return raftpb.Message{}, false, fmt.Errorf("controller raft snapshot chunk range out of bounds: offset=%d len=%d total=%d", chunk.offset, len(chunk.data), chunk.total)
	}

	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneExpiredLocked(now)

	key := controllerRaftSnapshotChunkKey{
		chunkID: chunk.chunkID,
		from:    chunk.from,
		to:      chunk.to,
		index:   chunk.index,
		term:    chunk.term,
	}
	entry := a.pending[key]
	if entry == nil {
		if chunk.total > uint64(math.MaxInt) {
			return raftpb.Message{}, false, fmt.Errorf("controller raft snapshot total overflows int: %d", chunk.total)
		}
		entry = &controllerRaftSnapshotChunkAssembly{
			message:    append([]byte(nil), chunk.message...),
			total:      chunk.total,
			data:       make([]byte, int(chunk.total)),
			chunks:     make(map[uint64]int),
			lastUpdate: now,
		}
		a.pending[key] = entry
	}
	if entry.total != chunk.total {
		return raftpb.Message{}, false, fmt.Errorf("controller raft snapshot chunk total changed: got=%d want=%d", chunk.total, entry.total)
	}
	if !bytes.Equal(entry.message, chunk.message) {
		return raftpb.Message{}, false, fmt.Errorf("controller raft snapshot chunk message changed")
	}
	if existingLen, ok := entry.chunks[chunk.offset]; ok {
		if existingLen == len(chunk.data) && bytes.Equal(entry.data[chunk.offset:chunk.offset+uint64(existingLen)], chunk.data) {
			entry.lastUpdate = now
			return raftpb.Message{}, false, nil
		}
		return raftpb.Message{}, false, fmt.Errorf("controller raft snapshot chunk duplicate offset has different data: offset=%d", chunk.offset)
	}
	if overlapsControllerRaftSnapshotChunk(entry.chunks, chunk.offset, uint64(len(chunk.data))) {
		return raftpb.Message{}, false, fmt.Errorf("controller raft snapshot chunks overlap at offset %d", chunk.offset)
	}

	copy(entry.data[chunk.offset:chunk.offset+uint64(len(chunk.data))], chunk.data)
	entry.chunks[chunk.offset] = len(chunk.data)
	entry.received += uint64(len(chunk.data))
	entry.lastUpdate = now
	if entry.received < entry.total {
		return raftpb.Message{}, false, nil
	}

	assembled, err := assembleControllerRaftSnapshotChunks(entry)
	if err != nil {
		delete(a.pending, key)
		return raftpb.Message{}, false, err
	}
	delete(a.pending, key)
	return assembled, true, nil
}

func (a *controllerRaftSnapshotAssembler) pruneExpiredLocked(now time.Time) {
	for key, entry := range a.pending {
		if now.Sub(entry.lastUpdate) > a.ttl {
			delete(a.pending, key)
		}
	}
}

func assembleControllerRaftSnapshotChunks(entry *controllerRaftSnapshotChunkAssembly) (raftpb.Message, error) {
	if entry.received != entry.total {
		return raftpb.Message{}, fmt.Errorf("controller raft snapshot chunks incomplete: got=%d want=%d", entry.received, entry.total)
	}

	var msg raftpb.Message
	if err := msg.Unmarshal(entry.message); err != nil {
		return raftpb.Message{}, fmt.Errorf("unmarshal controller raft snapshot chunk message: %w", err)
	}
	if msg.Type != raftpb.MsgSnap || msg.Snapshot == nil {
		return raftpb.Message{}, fmt.Errorf("controller raft snapshot chunk message is %s without snapshot", msg.Type)
	}
	msg.Snapshot.Data = entry.data
	return msg, nil
}

func overlapsControllerRaftSnapshotChunk(chunks map[uint64]int, offset, length uint64) bool {
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

// encodeControllerRaftSnapshotChunkBody encodes:
// [chunkID:8][from:8][to:8][index:8][term:8][total:8][offset:8][msgLen:8][dataLen:8][msg:N][data:N].
func encodeControllerRaftSnapshotChunkBody(chunk controllerRaftSnapshotChunk) []byte {
	size := controllerRaftSnapshotChunkHeaderSize + len(chunk.message) + len(chunk.data)
	buf := make([]byte, size)
	binary.BigEndian.PutUint64(buf[0:8], chunk.chunkID)
	binary.BigEndian.PutUint64(buf[8:16], chunk.from)
	binary.BigEndian.PutUint64(buf[16:24], chunk.to)
	binary.BigEndian.PutUint64(buf[24:32], chunk.index)
	binary.BigEndian.PutUint64(buf[32:40], chunk.term)
	binary.BigEndian.PutUint64(buf[40:48], chunk.total)
	binary.BigEndian.PutUint64(buf[48:56], chunk.offset)
	binary.BigEndian.PutUint64(buf[56:64], uint64(len(chunk.message)))
	binary.BigEndian.PutUint64(buf[64:72], uint64(len(chunk.data)))
	offset := controllerRaftSnapshotChunkHeaderSize
	copy(buf[offset:offset+len(chunk.message)], chunk.message)
	offset += len(chunk.message)
	copy(buf[offset:], chunk.data)
	return buf
}

func decodeControllerRaftSnapshotChunkBody(body []byte) (controllerRaftSnapshotChunk, error) {
	if len(body) < controllerRaftSnapshotChunkHeaderSize {
		return controllerRaftSnapshotChunk{}, fmt.Errorf("controller raft snapshot chunk body too short: %d", len(body))
	}
	chunk := controllerRaftSnapshotChunk{
		chunkID: binary.BigEndian.Uint64(body[0:8]),
		from:    binary.BigEndian.Uint64(body[8:16]),
		to:      binary.BigEndian.Uint64(body[16:24]),
		index:   binary.BigEndian.Uint64(body[24:32]),
		term:    binary.BigEndian.Uint64(body[32:40]),
		total:   binary.BigEndian.Uint64(body[40:48]),
		offset:  binary.BigEndian.Uint64(body[48:56]),
	}
	msgLen := binary.BigEndian.Uint64(body[56:64])
	dataLen := binary.BigEndian.Uint64(body[64:72])
	if msgLen > uint64(math.MaxInt) || dataLen > uint64(math.MaxInt) || msgLen+dataLen > uint64(math.MaxInt-controllerRaftSnapshotChunkHeaderSize) {
		return controllerRaftSnapshotChunk{}, fmt.Errorf("controller raft snapshot chunk length overflows int")
	}
	want := controllerRaftSnapshotChunkHeaderSize + int(msgLen) + int(dataLen)
	if len(body) != want {
		return controllerRaftSnapshotChunk{}, fmt.Errorf("controller raft snapshot chunk body size mismatch: got=%d want=%d", len(body), want)
	}
	offset := controllerRaftSnapshotChunkHeaderSize
	chunk.message = body[offset : offset+int(msgLen)]
	offset += int(msgLen)
	chunk.data = body[offset : offset+int(dataLen)]
	if chunk.offset > chunk.total || uint64(len(chunk.data)) > chunk.total-chunk.offset {
		return controllerRaftSnapshotChunk{}, fmt.Errorf("controller raft snapshot chunk range out of bounds: offset=%d len=%d total=%d", chunk.offset, len(chunk.data), chunk.total)
	}
	return chunk, nil
}
