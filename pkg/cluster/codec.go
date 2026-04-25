package cluster

import (
	"encoding/binary"
	"fmt"
)

// Raft message types used when registering with transport.Server.
const (
	msgTypeRaft            uint8 = 1
	msgTypeObservationHint uint8 = 2
)

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
