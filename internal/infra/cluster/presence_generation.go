package cluster

import (
	"context"
	"errors"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

// PresenceGenerationNode exposes committed control operations and separate
// zero-I/O serving checks. Construction alone never activates the protocol.
type PresenceGenerationNode interface {
	NodeID() uint64
	PresenceBootID() uint64
	EnablePresenceProtocol(uint32) error
	EnsurePresenceOwnerSet(context.Context) (controller.PresenceOwnerSet, error)
	GetPresenceGeneration(context.Context, uint16) (controller.PresenceGenerationSnapshot, error)
	GetLocalPresenceGeneration(uint16) (controller.PresenceGenerationSnapshot, error)
	ClaimPresenceGeneration(context.Context, controller.ClaimPresenceGenerationRequest) (controller.PresenceGeneration, error)
	CheckPresenceGeneration(controller.PresenceGeneration) error
	CheckPresenceOwnerGeneration(controller.PresenceGeneration) error
	ValidatePresenceGeneration(context.Context, controller.PresenceGeneration) error
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
}

// PresenceGenerationAdapter translates narrow generation ports. Scheduling,
// all-owner completeness and retries remain inside the presence usecase.
type PresenceGenerationAdapter struct {
	node   PresenceGenerationNode
	local  accessnode.PresenceGenerationOwner
	remote *accessnode.Client
}

// NewPresenceGenerationAdapter binds DTO translation without starting work.
func NewPresenceGenerationAdapter(node PresenceGenerationNode, local accessnode.PresenceGenerationOwner) *PresenceGenerationAdapter {
	return &PresenceGenerationAdapter{node: node, local: local, remote: accessnode.NewClient(node)}
}
func generationToCluster(c presence.GenerationClaim) controller.PresenceGeneration {
	t := c.Target
	return controller.PresenceGeneration{Fence: controller.PresenceFence{HashSlot: t.HashSlot, SlotID: t.SlotID, LeaderNodeID: t.LeaderNodeID, LeaderTerm: t.LeaderTerm, ConfigEpoch: t.ConfigEpoch}, OwnerSetEpoch: c.OwnerSetEpoch, ServingBoot: c.ServingBoot, Generation: c.Generation, PreviousGeneration: c.PreviousGeneration, AppliedRaftIndex: c.AppliedRaftIndex}
}
func generationFromCluster(c controller.PresenceGeneration) presence.GenerationClaim {
	f := c.Fence
	return presence.GenerationClaim{Target: presence.RouteTarget{HashSlot: f.HashSlot, SlotID: f.SlotID, LeaderNodeID: f.LeaderNodeID, LeaderTerm: f.LeaderTerm, ConfigEpoch: f.ConfigEpoch}, OwnerSetEpoch: c.OwnerSetEpoch, ServingBoot: c.ServingBoot, Generation: c.Generation, PreviousGeneration: c.PreviousGeneration, AppliedRaftIndex: c.AppliedRaftIndex}
}
func generationPortError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return errors.Join(authority.ErrStaleGeneration, err)
}

// ClaimSlotGeneration preserves the exact committed CAS receipt and owner vector.
func (a *PresenceGenerationAdapter) ClaimSlotGeneration(ctx context.Context, target presence.RouteTarget) (presence.GenerationClaim, []uint64, error) {
	if a == nil || a.node == nil {
		return presence.GenerationClaim{}, nil, authority.ErrSlotIncomplete
	}
	owners, err := a.node.EnsurePresenceOwnerSet(ctx)
	if err != nil {
		return presence.GenerationClaim{}, nil, generationPortError(err)
	}
	current, err := a.node.GetPresenceGeneration(ctx, target.HashSlot)
	if err != nil {
		return presence.GenerationClaim{}, nil, generationPortError(err)
	}
	if owners.Epoch == 0 || current.OwnerSet.Epoch != owners.Epoch || len(owners.Owners) == 0 || len(owners.Owners) > 256 {
		return presence.GenerationClaim{}, nil, authority.ErrStaleGeneration
	}
	fence := generationToCluster(presence.GenerationClaim{Target: target}).Fence
	claim, err := a.node.ClaimPresenceGeneration(ctx, controller.ClaimPresenceGenerationRequest{Fence: fence, OwnerSetEpoch: owners.Epoch, ServingBoot: a.node.PresenceBootID(), ExpectedGeneration: current.Generation.Generation})
	if err != nil {
		return presence.GenerationClaim{}, nil, generationPortError(err)
	}
	if claim.Fence != fence || claim.OwnerSetEpoch != owners.Epoch || claim.ServingBoot != a.node.PresenceBootID() || claim.PreviousGeneration != current.Generation.Generation {
		return presence.GenerationClaim{}, nil, authority.ErrStaleGeneration
	}
	result := generationFromCluster(claim)
	if !result.Valid() {
		return presence.GenerationClaim{}, nil, authority.ErrStaleGeneration
	}
	ids := make([]uint64, len(owners.Owners))
	for i, owner := range owners.Owners {
		ids[i] = owner.NodeID
	}
	return result, ids, nil
}

// ValidateSlotGeneration checks the current local serving gate without I/O.
func (a *PresenceGenerationAdapter) ValidateSlotGeneration(ctx context.Context, claim presence.GenerationClaim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return generationPortError(a.node.CheckPresenceGeneration(generationToCluster(claim)))
}

// ValidateOwnerBegin performs the rare committed read; page checks below do no I/O.
func (a *PresenceGenerationAdapter) ValidateOwnerBegin(ctx context.Context, claim presence.GenerationClaim) error {
	return generationPortError(a.node.ValidatePresenceGeneration(ctx, generationToCluster(claim)))
}
func (a *PresenceGenerationAdapter) CheckOwnerGeneration(ctx context.Context, claim presence.GenerationClaim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return generationPortError(a.node.CheckPresenceOwnerGeneration(generationToCluster(claim)))
}
func (a *PresenceGenerationAdapter) BeginSlot(ctx context.Context, claim presence.GenerationClaim, nodeID uint64) (presence.OwnerCut, error) {
	if nodeID == a.node.NodeID() {
		if a.local == nil {
			return presence.OwnerCut{}, authority.ErrSlotIncomplete
		}
		return a.local.BeginSlot(ctx, claim)
	}
	return a.remote.BeginPresenceSlot(ctx, nodeID, claim)
}
func (a *PresenceGenerationAdapter) ReadSlotPage(ctx context.Context, req presence.SlotPageRequest, budget *presence.RecoveryResultBudget) (*presence.OwnedSlotPage, error) {
	if req.Owner.NodeID == a.node.NodeID() {
		if a.local == nil {
			return nil, authority.ErrSlotIncomplete
		}
		return a.local.ReadSlotPage(ctx, req, budget)
	}
	return a.remote.ReadPresenceSlotPage(ctx, req, budget)
}
func (a *PresenceGenerationAdapter) EndSlot(ctx context.Context, req presence.SlotEndRequest) (presence.SlotEnd, error) {
	if req.Owner.NodeID == a.node.NodeID() {
		if a.local == nil {
			return presence.SlotEnd{}, authority.ErrSlotIncomplete
		}
		return a.local.EndSlot(ctx, req)
	}
	return a.remote.EndPresenceSlot(ctx, req)
}
func (a *PresenceGenerationAdapter) AbortSlot(ctx context.Context, claim presence.GenerationClaim, cut presence.OwnerCut) error {
	if cut.NodeID == a.node.NodeID() {
		if a.local == nil {
			return authority.ErrSlotIncomplete
		}
		return a.local.AbortSlot(ctx, claim, cut)
	}
	return a.remote.AbortPresenceSlot(ctx, claim, cut)
}
func (a *PresenceGenerationAdapter) ReadGenerationUIDs(ctx context.Context, claim presence.GenerationClaim, cut presence.OwnerCut, uids []string, budget *presence.RecoveryResultBudget) (*presence.OwnedSlotRoutes, error) {
	if cut.NodeID == a.node.NodeID() {
		if a.local == nil {
			return nil, authority.ErrSlotIncomplete
		}
		return a.local.ReadGenerationUIDs(ctx, claim, cut, uids, budget)
	}
	return a.remote.ReadPresenceGenerationUIDs(ctx, claim, cut, uids, budget)
}
