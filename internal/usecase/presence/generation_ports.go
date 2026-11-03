package presence

import (
	"context"
	"sync"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
)

type GenerationClaim = authority.GenerationClaim
type OwnerCut = authority.OwnerCut
type ActivationReceipt = authority.ActivationReceipt
type SlotPageRequest = authority.SlotPageRequest
type SlotPage = authority.SlotPage
type SlotEndRequest = authority.SlotEndRequest
type SlotEnd = authority.SlotEnd

// SlotGenerationSource obtains committed claims, never a node-local counter.
// ValidateSlotGeneration reads the current local applied/serving gate without
// network I/O. The adapter closes old Directory admission before publishing a
// changed target, owner set or serving incarnation in that gate.
type SlotGenerationSource interface {
	ClaimSlotGeneration(context.Context, RouteTarget) (GenerationClaim, []uint64, error)
	ValidateSlotGeneration(context.Context, GenerationClaim) error
}

// SlotGenerationOwners uses only explicit generation operations. Every required
// owner participates, including non-voters; unavailable is never empty/offline.
// Page/UID methods reserve from the supplied budget before decoded allocation.
type SlotGenerationOwners interface {
	BeginSlot(context.Context, GenerationClaim, uint64) (OwnerCut, error)
	ReadSlotPage(context.Context, SlotPageRequest, *RecoveryResultBudget) (*OwnedSlotPage, error)
	EndSlot(context.Context, SlotEndRequest) (SlotEnd, error)
	AbortSlot(context.Context, GenerationClaim, OwnerCut) error
	ReadGenerationUIDs(context.Context, GenerationClaim, OwnerCut, []string, *RecoveryResultBudget) (*OwnedSlotRoutes, error)
}

// OwnedSlotPage transfers one immutable decoded page until Release. Lower-layer
// replay ownership may outlive it and remains charged to that owner's ledger.
type OwnedSlotPage struct {
	Page           SlotPage
	Lease          *RecoveryResultLease
	ReleaseBacking func()
	once           sync.Once
}

func (p *OwnedSlotPage) Release() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		p.Page = SlotPage{}
		if p.ReleaseBacking != nil {
			p.ReleaseBacking()
			p.ReleaseBacking = nil
		}
		p.Lease.Release()
		p.Lease = nil
	})
}

// OwnedSlotRoutes is one exact owner's complete bounded UID recheck. The usecase
// must collect every obligated cut before using it to discharge uncertainty.
// LastSeen values always remain the owner's actual activity time.
type OwnedSlotRoutes struct {
	Routes         []Route
	Lease          *RecoveryResultLease
	ReleaseBacking func()
	once           sync.Once
}

func (p *OwnedSlotRoutes) Release() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		p.Routes = nil
		if p.ReleaseBacking != nil {
			p.ReleaseBacking()
			p.ReleaseBacking = nil
		}
		p.Lease.Release()
		p.Lease = nil
	})
}

// ReceiptAuthorityClient is the explicit strict activation capability. Full
// failures never silently fall back to unreceipted MarkActive or legacy Commit.
type ReceiptAuthorityClient interface {
	RequiresActivationReceipt() bool
	CommitRouteWithReceipt(context.Context, PendingRouteToken) (ActivationReceipt, error)
}

// ReceiptLocalRegistry atomically compares the owner gate and promotes pending.
type ReceiptLocalRegistry interface {
	MarkActiveWithReceipt(uint64, ActivationReceipt) error
}
