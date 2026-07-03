package propose

import (
	"encoding/binary"
	"fmt"
)

const (
	payloadVersion       = uint8(1)
	forwardVersionLegacy = uint8(1)
	forwardVersion       = uint8(2)
)

// EncodePayload prefixes command with the cluster Slot propose envelope.
func EncodePayload(hashSlot uint16, command []byte) []byte {
	out := make([]byte, 3+len(command))
	out[0] = payloadVersion
	binary.BigEndian.PutUint16(out[1:3], hashSlot)
	copy(out[3:], command)
	return out
}

// DecodePayload decodes a cluster Slot propose envelope.
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
	req.Class = normalizeProposalClass(req.Class)
	out := make([]byte, 12+len(req.Payload))
	out[0] = forwardVersion
	out[1] = byte(req.Class)
	binary.BigEndian.PutUint32(out[2:6], req.SlotID)
	binary.BigEndian.PutUint16(out[6:8], req.HashSlot)
	binary.BigEndian.PutUint32(out[8:12], uint32(len(req.Payload)))
	copy(out[12:], req.Payload)
	return out, nil
}

// DecodeForwardRequest decodes a remote forwarding request.
func DecodeForwardRequest(data []byte) (ForwardRequest, error) {
	if len(data) < 11 {
		return ForwardRequest{}, fmt.Errorf("%w: forward payload", ErrInvalidPayload)
	}
	switch data[0] {
	case forwardVersionLegacy:
		payloadLen := binary.BigEndian.Uint32(data[7:11])
		if int(payloadLen) != len(data)-11 {
			return ForwardRequest{}, fmt.Errorf("%w: forward payload length", ErrInvalidPayload)
		}
		return ForwardRequest{
			SlotID:   binary.BigEndian.Uint32(data[1:5]),
			HashSlot: binary.BigEndian.Uint16(data[5:7]),
			Class:    ProposalClassForeground,
			Payload:  append([]byte(nil), data[11:]...),
		}, nil
	case forwardVersion:
		if len(data) < 12 {
			return ForwardRequest{}, fmt.Errorf("%w: forward payload", ErrInvalidPayload)
		}
		payloadLen := binary.BigEndian.Uint32(data[8:12])
		if int(payloadLen) != len(data)-12 {
			return ForwardRequest{}, fmt.Errorf("%w: forward payload length", ErrInvalidPayload)
		}
		return ForwardRequest{
			SlotID:   binary.BigEndian.Uint32(data[2:6]),
			HashSlot: binary.BigEndian.Uint16(data[6:8]),
			Class:    normalizeProposalClass(ProposalClass(data[1])),
			Payload:  append([]byte(nil), data[12:]...),
		}, nil
	default:
		return ForwardRequest{}, fmt.Errorf("%w: forward payload", ErrInvalidPayload)
	}
}
