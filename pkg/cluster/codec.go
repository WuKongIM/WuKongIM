package cluster

import (
	"encoding/binary"
	"fmt"
)

// Raft message types used when registering with transport.Server.
const (
	msgTypeRaft            uint8 = 1
	msgTypeObservationHint uint8 = 2
	msgTypeRaftBatch       uint8 = 3
)

// raftBatchItem is one slot-scoped raft message inside a batched transport frame.
type raftBatchItem struct {
	slotID uint64
	data   []byte
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

// encodeProposalPayload encodes [hashSlot:2][cmd:N] for raft proposals.
func encodeProposalPayload(hashSlot uint16, cmd []byte) []byte {
	buf := make([]byte, 2+len(cmd))
	binary.BigEndian.PutUint16(buf[0:2], hashSlot)
	copy(buf[2:], cmd)
	return buf
}

// decodeProposalPayload decodes [hashSlot:2][cmd:N] from raft proposals.
func decodeProposalPayload(payload []byte) (hashSlot uint16, cmd []byte, err error) {
	if len(payload) < 2 {
		return 0, nil, fmt.Errorf("proposal payload too short: %d", len(payload))
	}
	hashSlot = binary.BigEndian.Uint16(payload[0:2])
	cmd = payload[2:]
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
