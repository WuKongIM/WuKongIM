package clusterv2

import (
	"context"
	"encoding/binary"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/propose"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// defaultSlotProposer adapts clusterv2 propose payloads to Multi-Raft Slot proposals.
type defaultSlotProposer struct {
	// runtime is the local single-node Slot Multi-Raft runtime.
	runtime *multiraft.Runtime
}

// IsLocalLeader reports whether the local default Slot runtime leads slotID.
func (p defaultSlotProposer) IsLocalLeader(slotID uint32) bool {
	if p.runtime == nil {
		return false
	}
	status, err := p.runtime.Status(multiraft.SlotID(slotID))
	return err == nil && status.Role == multiraft.RoleLeader
}

// Propose submits one decoded clusterv2 Slot command to the local Multi-Raft runtime.
func (p defaultSlotProposer) Propose(ctx context.Context, slotID uint32, payload []byte) error {
	if p.runtime == nil {
		return propose.ErrInvalidRequest
	}
	if ctx == nil {
		ctx = context.Background()
	}
	hashSlot, command, err := propose.DecodePayload(payload)
	if err != nil {
		return err
	}
	future, err := p.runtime.Propose(ctx, multiraft.SlotID(slotID), multiraftPayload(hashSlot, command))
	if err != nil {
		return mapMultiraftProposeError(err)
	}
	_, err = future.Wait(ctx)
	return mapMultiraftProposeError(err)
}

// multiraftPayload converts clusterv2's propose envelope into Multi-Raft's hash-slot envelope.
func multiraftPayload(hashSlot uint16, command []byte) []byte {
	out := make([]byte, 2+len(command))
	binary.BigEndian.PutUint16(out[:2], hashSlot)
	copy(out[2:], command)
	return out
}

// mapMultiraftProposeError preserves the public propose package error contract.
func mapMultiraftProposeError(err error) error {
	if errors.Is(err, multiraft.ErrNotLeader) {
		return propose.ErrNotLeader
	}
	return err
}

var _ propose.SlotRuntime = defaultSlotProposer{}
