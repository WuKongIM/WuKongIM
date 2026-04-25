package cluster

import (
	"context"
	"encoding/binary"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	managedSlotCodecVersion      byte = 1
	managedSlotRequestHeaderSize      = 23

	managedSlotFlagNotLeader byte = 1 << iota
	managedSlotFlagNotFound
	managedSlotFlagTimeout

	managedSlotKindUnknown byte = iota
	managedSlotKindStatus
	managedSlotKindChangeConfig
	managedSlotKindTransferLeader
	managedSlotKindImportSnapshot
)

func encodeManagedSlotRequest(req managedSlotRPCRequest) ([]byte, error) {
	kind, err := managedSlotKindCode(req.Kind)
	if err != nil {
		return nil, err
	}

	payload, err := encodeManagedSlotRequestPayload(req)
	if err != nil {
		return nil, err
	}

	body := make([]byte, 0, managedSlotRequestHeaderSize+binary.MaxVarintLen64+len(payload))
	header := make([]byte, managedSlotRequestHeaderSize)
	header[0] = managedSlotCodecVersion
	header[1] = kind
	binary.BigEndian.PutUint32(header[2:6], req.SlotID)
	binary.BigEndian.PutUint64(header[6:14], req.TargetNode)
	header[14] = byte(req.ChangeType)
	binary.BigEndian.PutUint64(header[15:23], req.NodeID)
	body = append(body, header...)
	body = binary.AppendUvarint(body, uint64(len(payload)))
	body = append(body, payload...)
	return body, nil
}

func decodeManagedSlotRequest(body []byte) (managedSlotRPCRequest, error) {
	if len(body) < managedSlotRequestHeaderSize {
		return managedSlotRPCRequest{}, ErrInvalidConfig
	}
	if body[0] != managedSlotCodecVersion {
		return managedSlotRPCRequest{}, ErrInvalidConfig
	}
	kind, err := managedSlotKindName(body[1])
	if err != nil {
		return managedSlotRPCRequest{}, err
	}
	payloadLen, n := binary.Uvarint(body[managedSlotRequestHeaderSize:])
	if n <= 0 {
		return managedSlotRPCRequest{}, ErrInvalidConfig
	}
	offset := managedSlotRequestHeaderSize + n
	if len(body[offset:]) != int(payloadLen) {
		return managedSlotRPCRequest{}, ErrInvalidConfig
	}

	req := managedSlotRPCRequest{
		Kind:       kind,
		SlotID:     binary.BigEndian.Uint32(body[2:6]),
		TargetNode: binary.BigEndian.Uint64(body[6:14]),
		ChangeType: multiraft.ChangeType(body[14]),
		NodeID:     binary.BigEndian.Uint64(body[15:23]),
	}
	if err := decodeManagedSlotRequestPayload(&req, body[offset:]); err != nil {
		return managedSlotRPCRequest{}, err
	}
	return req, nil
}

func encodeManagedSlotRequestPayload(req managedSlotRPCRequest) ([]byte, error) {
	switch req.Kind {
	case managedSlotRPCStatus, managedSlotRPCChangeConfig, managedSlotRPCTransferLeader:
		return nil, nil
	case managedSlotRPCImportSnapshot:
		body := make([]byte, 0, 2+len(req.Snapshot))
		body = binary.BigEndian.AppendUint16(body, req.HashSlot)
		body = append(body, req.Snapshot...)
		return body, nil
	default:
		return nil, ErrInvalidConfig
	}
}

func decodeManagedSlotRequestPayload(req *managedSlotRPCRequest, payload []byte) error {
	switch req.Kind {
	case managedSlotRPCStatus, managedSlotRPCChangeConfig, managedSlotRPCTransferLeader:
		if len(payload) != 0 {
			return ErrInvalidConfig
		}
		return nil
	case managedSlotRPCImportSnapshot:
		if len(payload) < 2 {
			return ErrInvalidConfig
		}
		req.HashSlot = binary.BigEndian.Uint16(payload[:2])
		req.Snapshot = append([]byte(nil), payload[2:]...)
		return nil
	default:
		return ErrInvalidConfig
	}
}

func encodeManagedSlotResponse(resp managedSlotRPCResponse) ([]byte, error) {
	flags := byte(0)
	if resp.NotLeader {
		flags |= managedSlotFlagNotLeader
	}
	if resp.NotFound {
		flags |= managedSlotFlagNotFound
	}
	if resp.Timeout {
		flags |= managedSlotFlagTimeout
	}

	message := []byte(resp.Message)
	body := make([]byte, 0, 1+1+8+8+8+binary.MaxVarintLen64+len(message))
	body = append(body, managedSlotCodecVersion, flags)

	var fixed [24]byte
	binary.BigEndian.PutUint64(fixed[0:8], resp.LeaderID)
	binary.BigEndian.PutUint64(fixed[8:16], resp.CommitIndex)
	binary.BigEndian.PutUint64(fixed[16:24], resp.AppliedIndex)
	body = append(body, fixed[:]...)
	body = binary.AppendUvarint(body, uint64(len(message)))
	body = append(body, message...)
	return body, nil
}

func decodeManagedSlotResponse(body []byte) (managedSlotRPCResponse, error) {
	if len(body) < 26 {
		return managedSlotRPCResponse{}, ErrInvalidConfig
	}
	if body[0] != managedSlotCodecVersion {
		return managedSlotRPCResponse{}, ErrInvalidConfig
	}

	resp := managedSlotRPCResponse{
		NotLeader:    body[1]&managedSlotFlagNotLeader != 0,
		NotFound:     body[1]&managedSlotFlagNotFound != 0,
		Timeout:      body[1]&managedSlotFlagTimeout != 0,
		LeaderID:     binary.BigEndian.Uint64(body[2:10]),
		CommitIndex:  binary.BigEndian.Uint64(body[10:18]),
		AppliedIndex: binary.BigEndian.Uint64(body[18:26]),
	}
	messageLen, n := binary.Uvarint(body[26:])
	if n <= 0 {
		return managedSlotRPCResponse{}, ErrInvalidConfig
	}
	offset := 26 + n
	if len(body[offset:]) != int(messageLen) {
		return managedSlotRPCResponse{}, ErrInvalidConfig
	}
	resp.Message = string(body[offset:])

	switch {
	case resp.NotLeader:
		return resp, ErrNotLeader
	case resp.NotFound:
		return resp, ErrSlotNotFound
	case resp.Timeout:
		return resp, context.DeadlineExceeded
	case resp.Message != "":
		return resp, errors.New(resp.Message)
	default:
		return resp, nil
	}
}

func managedSlotKindCode(kind string) (byte, error) {
	switch kind {
	case managedSlotRPCStatus:
		return managedSlotKindStatus, nil
	case managedSlotRPCChangeConfig:
		return managedSlotKindChangeConfig, nil
	case managedSlotRPCTransferLeader:
		return managedSlotKindTransferLeader, nil
	case managedSlotRPCImportSnapshot:
		return managedSlotKindImportSnapshot, nil
	default:
		return managedSlotKindUnknown, ErrInvalidConfig
	}
}

func managedSlotKindName(kind byte) (string, error) {
	switch kind {
	case managedSlotKindStatus:
		return managedSlotRPCStatus, nil
	case managedSlotKindChangeConfig:
		return managedSlotRPCChangeConfig, nil
	case managedSlotKindTransferLeader:
		return managedSlotRPCTransferLeader, nil
	case managedSlotKindImportSnapshot:
		return managedSlotRPCImportSnapshot, nil
	default:
		return "", ErrInvalidConfig
	}
}
