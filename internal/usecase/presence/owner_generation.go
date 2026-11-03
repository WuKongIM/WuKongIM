package presence

import (
	"context"
	"unsafe"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
)

// SlotOwnerRegistry is a node-local activation barrier plus bounded projection.
// Claim validation belongs outside its locks in this usecase.
type SlotOwnerRegistry interface {
	ActiveRouteRegistry
	ActiveRouteRegistryInto
	BeginSlot(GenerationClaim, uint64, uint64) (OwnerCut, error)
	ReadSlotPage(SlotPageRequest) (*online.OwnedSlotPage, error)
	EndSlot(SlotEndRequest) (SlotEnd, error)
	AbortSlot(GenerationClaim, OwnerCut) error
	ValidateSlotCut(GenerationClaim, OwnerCut) error
}

// OwnerGeneration authenticates the current committed claim around real local
// registry operations, without importing entry adapters or cluster runtimes.
type OwnerGeneration struct {
	local          SlotOwnerRegistry
	nodeID, bootID uint64
	// begin authenticates a fresh claim once; validate is the local applied gate.
	begin    func(context.Context, GenerationClaim) error
	validate func(context.Context, GenerationClaim) error
}

func NewOwnerGeneration(local SlotOwnerRegistry, nodeID, bootID uint64, validate func(context.Context, GenerationClaim) error) *OwnerGeneration {
	return NewOwnerGenerationWithChecks(local, nodeID, bootID, validate, validate)
}

// NewOwnerGenerationWithChecks separates Begin freshness from subsequent
// no-network gate checks. Neither function is called while registry locks hold.
func NewOwnerGenerationWithChecks(local SlotOwnerRegistry, nodeID, bootID uint64, begin, current func(context.Context, GenerationClaim) error) *OwnerGeneration {
	return &OwnerGeneration{local: local, nodeID: nodeID, bootID: bootID, begin: begin, validate: current}
}

func (r *OwnerGeneration) check(ctx context.Context, claim GenerationClaim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil || r.local == nil || r.validate == nil || r.nodeID == 0 || r.bootID == 0 || !claim.Valid() {
		return authority.ErrStaleGeneration
	}
	return r.validate(ctx, claim)
}

func (r *OwnerGeneration) checkCut(ctx context.Context, claim GenerationClaim, cut OwnerCut) error {
	if err := r.check(ctx, claim); err != nil {
		return err
	}
	if cut.NodeID != r.nodeID || cut.BootID != r.bootID {
		return authority.ErrStaleGeneration
	}
	return r.local.ValidateSlotCut(claim, cut)
}

// BeginSlot validates before installing an owner gate. A changed claim after
// installation aborts that unfinished scan and never returns a usable cut.
func (r *OwnerGeneration) BeginSlot(ctx context.Context, claim GenerationClaim) (OwnerCut, error) {
	if err := ctx.Err(); err != nil {
		return OwnerCut{}, err
	}
	if r == nil || r.local == nil || r.begin == nil || r.validate == nil || r.nodeID == 0 || r.bootID == 0 || !claim.Valid() {
		return OwnerCut{}, authority.ErrStaleGeneration
	}
	if err := r.begin(ctx, claim); err != nil {
		return OwnerCut{}, err
	}
	cut, err := r.local.BeginSlot(claim, r.nodeID, r.bootID)
	if err != nil {
		return OwnerCut{}, err
	}
	if err = r.check(ctx, claim); err != nil {
		_ = r.local.AbortSlot(claim, cut)
		return OwnerCut{}, err
	}
	return cut, nil
}

func slotRouteBytes(routes []Route) uint64 {
	n := uint64(cap(routes)) * uint64(unsafe.Sizeof(Route{}))
	for _, route := range routes {
		n += ownedRouteStringBytes(route)
	}
	return n
}

// ReadSlotPage retains the real registry page until its consumer releases it.
// The independent owner cache ledger remains in force across RPC reply lifetime.
func (r *OwnerGeneration) ReadSlotPage(ctx context.Context, req SlotPageRequest, budget *RecoveryResultBudget) (*OwnedSlotPage, error) {
	if err := r.checkCut(ctx, req.Claim, req.Owner); err != nil {
		return nil, err
	}
	page, err := r.local.ReadSlotPage(req)
	if err != nil {
		return nil, err
	}
	lease, err := budget.TryReserve(uint64(cap(page.Page.Routes)), slotRouteBytes(page.Page.Routes)+uint64(unsafe.Sizeof(OwnedSlotPage{})))
	if err != nil {
		page.Release()
		return nil, err
	}
	if err = r.checkCut(ctx, req.Claim, req.Owner); err != nil {
		page.Release()
		lease.Release()
		return nil, err
	}
	return &OwnedSlotPage{Page: page.Page, Lease: lease, ReleaseBacking: page.Release}, nil
}

func (r *OwnerGeneration) EndSlot(ctx context.Context, req SlotEndRequest) (SlotEnd, error) {
	if err := r.checkCut(ctx, req.Claim, req.Owner); err != nil {
		return SlotEnd{}, err
	}
	end, err := r.local.EndSlot(req)
	if err != nil {
		return SlotEnd{}, err
	}
	if err = r.checkCut(ctx, req.Claim, req.Owner); err != nil {
		return SlotEnd{}, err
	}
	return end, nil
}

// AbortSlot is exact cleanup. It may abort an obsolete cut after the serving
// claim advances, but the registry never lets it erase a successor/completed cut.
func (r *OwnerGeneration) AbortSlot(ctx context.Context, claim GenerationClaim, cut OwnerCut) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil || r.local == nil || cut.NodeID != r.nodeID || cut.BootID != r.bootID {
		return authority.ErrStaleGeneration
	}
	return r.local.AbortSlot(claim, cut)
}

// ReadGenerationUIDs is a current-cut local answer; the authority must combine
// every obligated owner before using it to discharge TTL uncertainty.
func (r *OwnerGeneration) ReadGenerationUIDs(ctx context.Context, claim GenerationClaim, cut OwnerCut, uids []string, budget *RecoveryResultBudget) (*OwnedSlotRoutes, error) {
	if err := r.checkCut(ctx, claim, cut); err != nil {
		return nil, err
	}
	if len(uids) > authority.RecoveryBatchSize {
		return nil, authority.ErrRouteNotReady
	}
	reader := NewOwnerRecovery(r.local, r.nodeID, r.bootID, func(target RouteTarget) error {
		if target != claim.Target {
			return authority.ErrStaleGeneration
		}
		return r.checkCut(ctx, claim, cut)
	})
	owned, err := reader.ReadOwnerRouteCallsBounded(ctx, []RecoveryCall{{Groups: []EndpointLookupGroup{{Target: claim.Target, UIDs: uids}}}}, 0, budget)
	if err != nil {
		return nil, err
	}
	if len(owned.Page.Results) != 1 || len(owned.Page.Results[0]) != 1 {
		owned.Release()
		return nil, authority.ErrRouteNotReady
	}
	result := owned.Page.Results[0][0]
	if result.Err != nil {
		owned.Release()
		return nil, result.Err
	}
	if err = r.checkCut(ctx, claim, cut); err != nil {
		owned.Release()
		return nil, err
	}
	return &OwnedSlotRoutes{Routes: result.Snapshot.Routes, ReleaseBacking: owned.Release}, nil
}
