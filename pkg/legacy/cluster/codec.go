package cluster

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

// Raft message types used when registering with transport.Server.
const (
	msgTypeRaft              uint8 = 1
	msgTypeObservationHint   uint8 = 2
	msgTypeRaftBatch         uint8 = 3
	msgTypeRaftSnapshotChunk uint8 = 4
)

// raftBatchItem is one slot-scoped raft message inside a batched transport frame.
type raftBatchItem struct {
	slotID uint64
	data   []byte
}

const raftSnapshotChunkHeaderSize = 80

// raftSnapshotChunk is one bounded wire fragment of a Slot Raft MsgSnap.
type raftSnapshotChunk struct {
	// slotID identifies the Slot Raft group that owns this snapshot.
	slotID uint64
	// chunkID groups all chunks produced for one outbound MsgSnap send attempt.
	chunkID uint64
	// from is the Raft sender node ID.
	from uint64
	// to is the Raft target node ID.
	to uint64
	// index is the snapshot metadata index.
	index uint64
	// term is the snapshot metadata term.
	term uint64
	// total is the full snapshot data length.
	total uint64
	// offset is the starting byte of data within the full snapshot.
	offset uint64
	// message is the marshaled MsgSnap with Snapshot.Data cleared.
	message []byte
	// data is the chunk payload for [offset, offset+len(data)).
	data []byte
}

// Forward error codes (encoded within RPC payload, not wire-level).
const (
	errCodeOK        uint8 = 0
	errCodeNotLeader uint8 = 1
	errCodeTimeout   uint8 = 2
	errCodeNoSlot    uint8 = 3
)

// encodeRaftBody encodes [slotID:8][data:N].
func encodeRaftBody(slotID uint64, data []byte) []byte {
	buf := make([]byte, 8+len(data))
	binary.BigEndian.PutUint64(buf[0:8], slotID)
	copy(buf[8:], data)
	return buf
}

// decodeRaftBody decodes [slotID:8][data:N].
func decodeRaftBody(body []byte) (slotID uint64, data []byte, err error) {
	if len(body) < 8 {
		return 0, nil, fmt.Errorf("raft body too short: %d", len(body))
	}
	slotID = binary.BigEndian.Uint64(body[0:8])
	data = body[8:]
	return slotID, data, nil
}

// encodeRaftBatchBody encodes [count:4] repeated [slotID:8][dataLen:4][data:N].
func encodeRaftBatchBody(items []raftBatchItem) []byte {
	size := 4
	for _, item := range items {
		size += 12 + len(item.data)
	}
	buf := make([]byte, size)
	binary.BigEndian.PutUint32(buf[0:4], uint32(len(items)))
	offset := 4
	for _, item := range items {
		binary.BigEndian.PutUint64(buf[offset:offset+8], item.slotID)
		offset += 8
		binary.BigEndian.PutUint32(buf[offset:offset+4], uint32(len(item.data)))
		offset += 4
		copy(buf[offset:offset+len(item.data)], item.data)
		offset += len(item.data)
	}
	return buf
}

// decodeRaftBatchBody decodes [count:4] repeated [slotID:8][dataLen:4][data:N].
func decodeRaftBatchBody(body []byte) ([]raftBatchItem, error) {
	if len(body) < 4 {
		return nil, fmt.Errorf("raft batch body too short: %d", len(body))
	}
	count := int(binary.BigEndian.Uint32(body[0:4]))
	if count > (len(body)-4)/12 {
		return nil, fmt.Errorf("raft batch item count exceeds body size: count=%d body=%d", count, len(body))
	}
	items := make([]raftBatchItem, 0, count)
	offset := 4
	for i := 0; i < count; i++ {
		if len(body)-offset < 12 {
			return nil, fmt.Errorf("raft batch item %d header truncated", i)
		}
		slotID := binary.BigEndian.Uint64(body[offset : offset+8])
		offset += 8
		dataLen := int(binary.BigEndian.Uint32(body[offset : offset+4]))
		offset += 4
		if dataLen > len(body)-offset {
			return nil, fmt.Errorf("raft batch item %d data truncated", i)
		}
		items = append(items, raftBatchItem{
			slotID: slotID,
			data:   body[offset : offset+dataLen],
		})
		offset += dataLen
	}
	if offset != len(body) {
		return nil, fmt.Errorf("raft batch body has %d trailing bytes", len(body)-offset)
	}
	return items, nil
}

// encodeRaftSnapshotChunkBody encodes a snapshot chunk as:
// [slotID:8][chunkID:8][from:8][to:8][index:8][term:8][total:8][offset:8][msgLen:8][dataLen:8][msg:N][data:N].
func encodeRaftSnapshotChunkBody(chunk raftSnapshotChunk) []byte {
	size := raftSnapshotChunkHeaderSize + len(chunk.message) + len(chunk.data)
	buf := make([]byte, size)
	binary.BigEndian.PutUint64(buf[0:8], chunk.slotID)
	binary.BigEndian.PutUint64(buf[8:16], chunk.chunkID)
	binary.BigEndian.PutUint64(buf[16:24], chunk.from)
	binary.BigEndian.PutUint64(buf[24:32], chunk.to)
	binary.BigEndian.PutUint64(buf[32:40], chunk.index)
	binary.BigEndian.PutUint64(buf[40:48], chunk.term)
	binary.BigEndian.PutUint64(buf[48:56], chunk.total)
	binary.BigEndian.PutUint64(buf[56:64], chunk.offset)
	binary.BigEndian.PutUint64(buf[64:72], uint64(len(chunk.message)))
	binary.BigEndian.PutUint64(buf[72:80], uint64(len(chunk.data)))
	offset := raftSnapshotChunkHeaderSize
	copy(buf[offset:offset+len(chunk.message)], chunk.message)
	offset += len(chunk.message)
	copy(buf[offset:offset+len(chunk.data)], chunk.data)
	return buf
}

// decodeRaftSnapshotChunkBody decodes a bounded Slot Raft snapshot chunk.
func decodeRaftSnapshotChunkBody(body []byte) (raftSnapshotChunk, error) {
	if len(body) < raftSnapshotChunkHeaderSize {
		return raftSnapshotChunk{}, fmt.Errorf("raft snapshot chunk body too short: %d", len(body))
	}
	chunk := raftSnapshotChunk{
		slotID:  binary.BigEndian.Uint64(body[0:8]),
		chunkID: binary.BigEndian.Uint64(body[8:16]),
		from:    binary.BigEndian.Uint64(body[16:24]),
		to:      binary.BigEndian.Uint64(body[24:32]),
		index:   binary.BigEndian.Uint64(body[32:40]),
		term:    binary.BigEndian.Uint64(body[40:48]),
		total:   binary.BigEndian.Uint64(body[48:56]),
		offset:  binary.BigEndian.Uint64(body[56:64]),
	}
	msgLen := binary.BigEndian.Uint64(body[64:72])
	dataLen := binary.BigEndian.Uint64(body[72:80])
	if msgLen > uint64(math.MaxInt) || dataLen > uint64(math.MaxInt) {
		return raftSnapshotChunk{}, fmt.Errorf("raft snapshot chunk length overflows int")
	}
	want := raftSnapshotChunkHeaderSize + int(msgLen) + int(dataLen)
	if want != len(body) {
		return raftSnapshotChunk{}, fmt.Errorf("raft snapshot chunk body size mismatch: got=%d want=%d", len(body), want)
	}
	offset := raftSnapshotChunkHeaderSize
	chunk.message = body[offset : offset+int(msgLen)]
	offset += int(msgLen)
	chunk.data = body[offset : offset+int(dataLen)]
	if chunk.offset > chunk.total || uint64(len(chunk.data)) > chunk.total-chunk.offset {
		return raftSnapshotChunk{}, fmt.Errorf("raft snapshot chunk range out of bounds: offset=%d len=%d total=%d", chunk.offset, len(chunk.data), chunk.total)
	}
	return chunk, nil
}

// proposalEnvelopeSize is the cluster-side mirror of the Slot Raft proposal
// envelope: [hashSlot:2][createdAtMS:8][cmd:N].
const proposalEnvelopeSize = 10

// encodeProposalPayload encodes [hashSlot:2][createdAtMS:8][cmd:N] for raft proposals.
func encodeProposalPayload(hashSlot uint16, cmd []byte) []byte {
	buf := make([]byte, proposalEnvelopeSize+len(cmd))
	binary.BigEndian.PutUint16(buf[0:2], hashSlot)
	binary.BigEndian.PutUint64(buf[2:proposalEnvelopeSize], uint64(time.Now().UTC().UnixMilli()))
	copy(buf[proposalEnvelopeSize:], cmd)
	return buf
}

// decodeProposalPayload decodes [hashSlot:2][createdAtMS:8][cmd:N] from raft proposals.
func decodeProposalPayload(payload []byte) (hashSlot uint16, cmd []byte, err error) {
	if len(payload) < proposalEnvelopeSize {
		return 0, nil, fmt.Errorf("proposal payload too short: %d", len(payload))
	}
	hashSlot = binary.BigEndian.Uint16(payload[0:2])
	cmd = payload[proposalEnvelopeSize:]
	return hashSlot, cmd, nil
}

// encodeForwardPayload encodes [slotID:8][cmd:N] for RPC payload.
func encodeForwardPayload(slotID uint64, cmd []byte) []byte {
	buf := make([]byte, 8+len(cmd))
	binary.BigEndian.PutUint64(buf[0:8], slotID)
	copy(buf[8:], cmd)
	return buf
}

// decodeForwardPayload decodes [slotID:8][cmd:N] from RPC payload.
func decodeForwardPayload(payload []byte) (slotID uint64, cmd []byte, err error) {
	if len(payload) < 8 {
		return 0, nil, fmt.Errorf("forward payload too short: %d", len(payload))
	}
	slotID = binary.BigEndian.Uint64(payload[0:8])
	cmd = payload[8:]
	return slotID, cmd, nil
}

// encodeForwardResp encodes [errCode:1][data:N] for RPC response payload.
func encodeForwardResp(errCode uint8, data []byte) []byte {
	buf := make([]byte, 1+len(data))
	buf[0] = errCode
	copy(buf[1:], data)
	return buf
}

// decodeForwardResp decodes [errCode:1][data:N] from RPC response payload.
func decodeForwardResp(payload []byte) (errCode uint8, data []byte, err error) {
	if len(payload) < 1 {
		return 0, nil, fmt.Errorf("forward resp too short")
	}
	return payload[0], payload[1:], nil
}
