package clusterv2

import (
	"context"
	"encoding/binary"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/propose"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	defaultSlotStageMetaCreateSubmit = "meta_create_slot_propose_submit"
	defaultSlotStageMetaCreateWait   = "meta_create_slot_propose_wait"
	// slotProposalEnvelopeSize is [hashSlot:2][createdAtMS:8] before the Slot FSM command.
	slotProposalEnvelopeSize = 10
)

type defaultSlotRuntime interface {
	Propose(context.Context, multiraft.SlotID, []byte) (multiraft.Future, error)
	Status(multiraft.SlotID) (multiraft.Status, error)
}

// defaultSlotProposer adapts clusterv2 propose payloads to Multi-Raft Slot proposals.
type defaultSlotProposer struct {
	// runtime is the local Slot Multi-Raft runtime.
	runtime defaultSlotRuntime
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
	if observer := propose.StageObserverFromContext(ctx); observer != nil {
		ctx = multiraft.WithProposalStageObserver(ctx, defaultSlotProposalStageObserver{observer: observer})
	}
	started := time.Now()
	future, err := p.runtime.Propose(ctx, multiraft.SlotID(slotID), multiraftPayload(hashSlot, command))
	propose.ObserveStage(ctx, defaultSlotStageMetaCreateSubmit, err, time.Since(started))
	if err != nil {
		return mapMultiraftProposeError(err)
	}
	started = time.Now()
	_, err = future.Wait(ctx)
	propose.ObserveStage(ctx, defaultSlotStageMetaCreateWait, err, time.Since(started))
	return mapMultiraftProposeError(err)
}

// multiraftPayload converts clusterv2's propose envelope into Multi-Raft's hash-slot envelope.
func multiraftPayload(hashSlot uint16, command []byte) []byte {
	return multiraftPayloadWithCreatedAt(hashSlot, time.Now().UTC().UnixMilli(), command)
}

func multiraftPayloadWithCreatedAt(hashSlot uint16, createdAtMS int64, command []byte) []byte {
	out := make([]byte, slotProposalEnvelopeSize+len(command))
	binary.BigEndian.PutUint16(out[:2], hashSlot)
	binary.BigEndian.PutUint64(out[2:slotProposalEnvelopeSize], uint64(createdAtMS))
	copy(out[slotProposalEnvelopeSize:], command)
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

type defaultSlotProposalStageObserver struct {
	observer propose.StageObserver
}

func (o defaultSlotProposalStageObserver) ObserveProposalStage(stage string, result string, d time.Duration) {
	if o.observer != nil {
		o.observer.ObserveChannelAppendStage(stage, result, d)
	}
}
