package propose

import (
	"encoding/binary"
	"fmt"
)

const (
	payloadVersion = uint8(1)
	forwardVersion = uint8(1)
)

// EncodePayload prefixes command with the clusterv2 Slot propose envelope.
func EncodePayload(hashSlot uint16, command []byte) []byte {
	out := make([]byte, 3+len(command))
	out[0] = payloadVersion
	binary.BigEndian.PutUint16(out[1:3], hashSlot)
	copy(out[3:], command)
	return out
}

// DecodePayload decodes a clusterv2 Slot propose envelope.
func DecodePayload(payload []byte) (uint16, []byte, error) {
	if len(payload) < 3 || payload[0] != payloadVersion {
		return 0, nil, fmt.Errorf("%w: propose payload", ErrInvalidPayload)
	}
	return binary.BigEndian.Uint16(payload[1:3]), append([]byte(nil), payload[3:]...), nil
}

// EncodeForwardRequest encodes req for remote forwarding.
func EncodeForwardRequest(req ForwardRequest) ([]byte, error) {
	if req.SlotID == 0 || len(req.Payload) == 0 {
		return nil, ErrInvalidRequest
	}
	out := make([]byte, 11+len(req.Payload))
	out[0] = forwardVersion
	binary.BigEndian.PutUint32(out[1:5], req.SlotID)
	binary.BigEndian.PutUint16(out[5:7], req.HashSlot)
	binary.BigEndian.PutUint32(out[7:11], uint32(len(req.Payload)))
	copy(out[11:], req.Payload)
	return out, nil
}

// DecodeForwardRequest decodes a remote forwarding request.
func DecodeForwardRequest(data []byte) (ForwardRequest, error) {
	if len(data) < 11 || data[0] != forwardVersion {
		return ForwardRequest{}, fmt.Errorf("%w: forward payload", ErrInvalidPayload)
	}
	payloadLen := binary.BigEndian.Uint32(data[7:11])
	if int(payloadLen) != len(data)-11 {
		return ForwardRequest{}, fmt.Errorf("%w: forward payload length", ErrInvalidPayload)
	}
	return ForwardRequest{SlotID: binary.BigEndian.Uint32(data[1:5]), HashSlot: binary.BigEndian.Uint16(data[5:7]), Payload: append([]byte(nil), data[11:]...)}, nil
}
