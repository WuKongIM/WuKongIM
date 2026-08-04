package cluster

import (
	"context"
	"encoding/binary"
	"errors"
	"time"

	clusterchannels "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
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

// defaultSlotProposer adapts cluster propose payloads to Multi-Raft Slot proposals.
type defaultSlotProposer struct {
	// runtime is the local Slot Multi-Raft runtime.
	runtime defaultSlotRuntime
	// acquireAdmission linearizes ordinary Slot proposal admission against the
	// one-way source fence. The returned release must cover the runtime enqueue.
	acquireAdmission func() (release func(), err error)
	// metaCreateObserver receives one result at this authoritative proposal boundary.
	metaCreateObserver clusterchannels.MetaCreateObserver
}

// IsLocalLeader reports whether the local default Slot runtime leads slotID.
func (p defaultSlotProposer) IsLocalLeader(slotID uint32) bool {
	if p.runtime == nil {
		return false
	}
	status, err := p.runtime.Status(multiraft.SlotID(slotID))
	return err == nil && status.Role == multiraft.RoleLeader
}

// Propose submits one decoded cluster Slot command to the local Multi-Raft runtime.
func (p defaultSlotProposer) Propose(ctx context.Context, slotID uint32, payload []byte) error {
	_, err := p.propose(ctx, slotID, payload, false)
	return err
}

// ProposeResult submits one decoded cluster Slot command and returns FSM apply bytes.
func (p defaultSlotProposer) ProposeResult(ctx context.Context, slotID uint32, payload []byte) ([]byte, error) {
	return p.propose(ctx, slotID, payload, true)
}

func (p defaultSlotProposer) propose(ctx context.Context, slotID uint32, payload []byte, wantResult bool) ([]byte, error) {
	if p.runtime == nil {
		return nil, propose.ErrInvalidRequest
	}
	if ctx == nil {
		ctx = context.Background()
	}
	hashSlot, command, err := propose.DecodePayload(payload)
	if err != nil {
		return nil, err
	}
	metaCreate := metafsm.IsCreateChannelRuntimeMetaCommand(command)
	if observer := propose.StageObserverFromContext(ctx); observer != nil {
		ctx = multiraft.WithProposalStageObserver(ctx, defaultSlotProposalStageObserver{observer: observer})
	}
	if propose.ProposalClassFromContext(ctx) == propose.ProposalClassBackground {
		ctx = multiraft.WithProposalClass(ctx, multiraft.ProposalClassBackground)
	}
	release := func() {}
	if p.acquireAdmission != nil {
		release, err = p.acquireAdmission()
		if err != nil {
			return nil, err
		}
		if release == nil {
			release = func() {}
		}
	}
	started := time.Now()
	future, err := p.runtime.Propose(ctx, multiraft.SlotID(slotID), multiraftPayload(hashSlot, command))
	release()
	propose.ObserveStage(ctx, defaultSlotStageMetaCreateSubmit, err, time.Since(started))
	if err != nil {
		return nil, mapMultiraftProposeError(err)
	}
	completionObserved := false
	if metaCreate && p.metaCreateObserver != nil {
		if completionFuture, ok := future.(multiraft.CompletionFuture); ok {
			completionObserved = completionFuture.ObserveCompletion(defaultSlotMetaCreateCompletionObserver{
				slotID:   slotID,
				observer: p.metaCreateObserver,
			})
		}
	}
	started = time.Now()
	result, err := future.Wait(ctx)
	propose.ObserveStage(ctx, defaultSlotStageMetaCreateWait, err, time.Since(started))
	if err == nil {
		if applyErr := mapSlotApplyResult(command, result.Data); applyErr != nil {
			if !completionObserved {
				p.observeMetaCreate(slotID, metaCreate, clusterchannels.MetaCreateError)
			}
			return nil, applyErr
		}
	}
	if err != nil {
		if !completionObserved && (ctx.Err() == nil || !errors.Is(err, ctx.Err())) {
			p.observeMetaCreate(slotID, metaCreate, clusterchannels.MetaCreateError)
		}
		return nil, mapMultiraftProposeError(err)
	}
	if metaCreate && !completionObserved {
		defaultSlotMetaCreateCompletionObserver{slotID: slotID, observer: p.metaCreateObserver}.ObserveFutureCompletion(result, nil)
	}
	if metaCreate {
		if _, decodeErr := metafsm.DecodeCreateChannelRuntimeMetaResult(result.Data); decodeErr != nil {
			return nil, decodeErr
		}
	}
	if !wantResult {
		return nil, nil
	}
	return append([]byte(nil), result.Data...), nil
}

type defaultSlotMetaCreateCompletionObserver struct {
	slotID   uint32
	observer clusterchannels.MetaCreateObserver
}

func (o defaultSlotMetaCreateCompletionObserver) ObserveFutureCompletion(result multiraft.Result, err error) {
	if o.observer == nil {
		return
	}
	observed := clusterchannels.MetaCreateError
	if err == nil {
		if createResult, decodeErr := metafsm.DecodeCreateChannelRuntimeMetaResult(result.Data); decodeErr == nil {
			observed = clusterchannels.MetaCreateAlreadyExisting
			if createResult.Created {
				observed = clusterchannels.MetaCreateCreated
			}
		}
	}
	o.observer.ObserveChannelMetaCreate(o.slotID, observed)
}

func (p defaultSlotProposer) observeMetaCreate(slotID uint32, metaCreate bool, result clusterchannels.MetaCreateResult) {
	if metaCreate && p.metaCreateObserver != nil {
		p.metaCreateObserver.ObserveChannelMetaCreate(slotID, result)
	}
}

// multiraftPayload converts cluster's propose envelope into Multi-Raft's hash-slot envelope.
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
	switch {
	case errors.Is(err, multiraft.ErrNotLeader):
		return propose.ErrNotLeader
	case errors.Is(err, multiraft.ErrBackgroundProposalThrottled):
		return propose.ErrBackgroundProposalThrottled
	case errors.Is(err, multiraft.ErrProposalBackpressure):
		return propose.ErrProposalBackpressure
	default:
		return err
	}
}

func mapSlotApplyResult(command []byte, result []byte) error {
	switch string(result) {
	case metafsm.ApplyResultStaleMeta:
		if !metafsm.IsChannelMigrationCommand(command) {
			return nil
		}
		return metadb.ErrStaleMeta
	default:
		return nil
	}
}

var _ propose.SlotRuntime = defaultSlotProposer{}
var _ propose.ResultSlotRuntime = defaultSlotProposer{}

type defaultSlotProposalStageObserver struct {
	observer propose.StageObserver
}

func (o defaultSlotProposalStageObserver) ObserveProposalStage(stage string, result string, d time.Duration) {
	if o.observer != nil {
		o.observer.ObserveChannelAppendStage(stage, result, d)
	}
}
