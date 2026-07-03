package cluster

import (
	"context"
	"encoding/binary"
	"fmt"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	slotStatusRPCVersion = uint8(1)
	slotStatusHeaderSize = 3
	slotStatusItemSize   = 20
)

type slotStatusRuntime interface {
	Status(multiraft.SlotID) (multiraft.Status, error)
}

type slotStatusClient struct {
	caller clusternet.Caller
}

func (c slotStatusClient) Statuses(ctx context.Context, nodeID uint64, slotIDs []uint32) ([]routing.SlotStatus, error) {
	if c.caller == nil {
		return nil, ErrNotStarted
	}
	payload, err := encodeSlotStatusRequest(slotIDs)
	if err != nil {
		return nil, err
	}
	resp, err := clusternet.CallOwnedPayload(ctx, c.caller, nodeID, clusternet.RPCSlotStatus, payload)
	if err != nil {
		return nil, err
	}
	return decodeSlotStatusResponse(resp)
}

type slotStatusHandler struct {
	runtime slotStatusRuntime
}

func (h slotStatusHandler) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if h.runtime == nil {
		return nil, ErrNotStarted
	}
	slotIDs, err := decodeSlotStatusRequest(payload)
	if err != nil {
		return nil, err
	}
	statuses := make([]routing.SlotStatus, 0, len(slotIDs))
	for _, slotID := range slotIDs {
		if slotID == 0 {
			continue
		}
		status, err := h.runtime.Status(multiraft.SlotID(slotID))
		if err != nil || status.LeaderID == 0 {
			continue
		}
		statuses = append(statuses, routing.SlotStatus{
			SlotID:     uint32(status.SlotID),
			Leader:     uint64(status.LeaderID),
			LeaderTerm: status.Term,
		})
	}
	return encodeSlotStatusResponse(statuses)
}

func encodeSlotStatusRequest(slotIDs []uint32) ([]byte, error) {
	if len(slotIDs) > int(^uint16(0)) {
		return nil, fmt.Errorf("%w: too many slot status requests", ErrInvalidConfig)
	}
	out := make([]byte, slotStatusHeaderSize+len(slotIDs)*4)
	out[0] = slotStatusRPCVersion
	binary.BigEndian.PutUint16(out[1:3], uint16(len(slotIDs)))
	offset := slotStatusHeaderSize
	for _, slotID := range slotIDs {
		binary.BigEndian.PutUint32(out[offset:offset+4], slotID)
		offset += 4
	}
	return out, nil
}

func decodeSlotStatusRequest(payload []byte) ([]uint32, error) {
	if len(payload) < slotStatusHeaderSize || payload[0] != slotStatusRPCVersion {
		return nil, fmt.Errorf("%w: slot status request", ErrInvalidConfig)
	}
	count := int(binary.BigEndian.Uint16(payload[1:3]))
	if len(payload) != slotStatusHeaderSize+count*4 {
		return nil, fmt.Errorf("%w: slot status request length", ErrInvalidConfig)
	}
	out := make([]uint32, 0, count)
	offset := slotStatusHeaderSize
	for i := 0; i < count; i++ {
		out = append(out, binary.BigEndian.Uint32(payload[offset:offset+4]))
		offset += 4
	}
	return out, nil
}

func encodeSlotStatusResponse(statuses []routing.SlotStatus) ([]byte, error) {
	if len(statuses) > int(^uint16(0)) {
		return nil, fmt.Errorf("%w: too many slot status responses", ErrInvalidConfig)
	}
	out := make([]byte, slotStatusHeaderSize+len(statuses)*slotStatusItemSize)
	out[0] = slotStatusRPCVersion
	binary.BigEndian.PutUint16(out[1:3], uint16(len(statuses)))
	offset := slotStatusHeaderSize
	for _, status := range statuses {
		binary.BigEndian.PutUint32(out[offset:offset+4], status.SlotID)
		binary.BigEndian.PutUint64(out[offset+4:offset+12], status.Leader)
		binary.BigEndian.PutUint64(out[offset+12:offset+20], status.LeaderTerm)
		offset += slotStatusItemSize
	}
	return out, nil
}

func decodeSlotStatusResponse(payload []byte) ([]routing.SlotStatus, error) {
	if len(payload) < slotStatusHeaderSize || payload[0] != slotStatusRPCVersion {
		return nil, fmt.Errorf("%w: slot status response", ErrInvalidConfig)
	}
	count := int(binary.BigEndian.Uint16(payload[1:3]))
	if len(payload) != slotStatusHeaderSize+count*slotStatusItemSize {
		return nil, fmt.Errorf("%w: slot status response length", ErrInvalidConfig)
	}
	out := make([]routing.SlotStatus, 0, count)
	offset := slotStatusHeaderSize
	for i := 0; i < count; i++ {
		out = append(out, routing.SlotStatus{
			SlotID:     binary.BigEndian.Uint32(payload[offset : offset+4]),
			Leader:     binary.BigEndian.Uint64(payload[offset+4 : offset+12]),
			LeaderTerm: binary.BigEndian.Uint64(payload[offset+12 : offset+20]),
		})
		offset += slotStatusItemSize
	}
	return out, nil
}
