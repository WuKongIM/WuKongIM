package presence

import "errors"

// ErrStaleGeneration rejects an operation from a superseded committed claim.
var ErrStaleGeneration = errors.New("internal/runtime/presence: stale generation")

// ErrSlotIncomplete rejects a negative proof or activation before all owner cuts end.
var ErrSlotIncomplete = errors.New("internal/runtime/presence: slot incomplete")

// ErrOwnerVectorChanged reports a valid registration outside the complete owner
// vector. Boot IDs are equality tokens; this does not prove which boot is newer.
var ErrOwnerVectorChanged = errors.New("internal/runtime/presence: owner vector changed")

// GenerationClaim is the committed, monotonic authority identity for one hash
// slot. A caller must validate it against the cluster's applied claim before
// installing it; local counters and diagnostic epochs are not substitutes.
type GenerationClaim struct {
	// Target binds the physical placement and leader, excluding diagnostic epochs.
	Target RouteTarget
	// OwnerSetEpoch changes when the obligated gateway-owner set changes.
	OwnerSetEpoch uint64
	// ServingBoot identifies the authority process that may serve this claim.
	ServingBoot uint64
	// Generation increases across every rebuild, restart, transfer and retry.
	Generation uint64
	// PreviousGeneration is the committed predecessor, never a local guess.
	PreviousGeneration uint64
	// AppliedRaftIndex witnesses application of this claim in the control log.
	AppliedRaftIndex uint64
}

// Valid checks shape only, never committed ownership or current membership.
func (c GenerationClaim) Valid() bool {
	return c.Target.LeaderNodeID != 0 &&
		c.Target.LeaderTerm != 0 && c.Target.ConfigEpoch != 0 && c.OwnerSetEpoch != 0 &&
		c.ServingBoot != 0 && c.Generation != 0 && c.PreviousGeneration < c.Generation && c.AppliedRaftIndex != 0
}

// Equal compares distributed identity; diagnostic revision/epoch do not fence.
func (c GenerationClaim) Equal(other GenerationClaim) bool {
	return sameAuthorityIdentity(c.Target, other.Target) && c.OwnerSetEpoch == other.OwnerSetEpoch &&
		c.ServingBoot == other.ServingBoot && c.Generation == other.Generation &&
		c.PreviousGeneration == other.PreviousGeneration && c.AppliedRaftIndex == other.AppliedRaftIndex
}

// OwnerCut identifies the exact gateway process and its atomic activation cut.
type OwnerCut struct {
	// NodeID is the obligated gateway owner, including non-voter gateway nodes.
	NodeID uint64
	// BootID fences all sessions belonging to one owner process.
	BootID uint64
	// Cut is the owner's monotonic atomic activation barrier within that boot.
	Cut uint64
}

// Valid requires all parts of an actual owner cut, including a nonzero boot.
func (c OwnerCut) Valid() bool { return c.NodeID != 0 && c.BootID != 0 && c.Cut != 0 }

// ActivationReceipt is returned only by the actual successful authority after
// the complete owner vector is installed. Pending conflict results carry none.
type ActivationReceipt struct {
	// Claim is copied from the actual final successful authority mutation.
	Claim GenerationClaim
	// Owner identifies the gate that the local atomic promotion must match.
	Owner OwnerCut
	// Identity and OwnerSeq bind the exact pending session and activation attempt.
	Identity RouteIdentity
	OwnerSeq uint64
}

// Valid checks receipt shape; atomic owner promotion must still compare its gate.
func (r ActivationReceipt) Valid() bool {
	return r.Claim.Valid() && r.Owner.Valid() && r.Identity.UID != "" && r.Identity.SessionID != 0 &&
		r.Identity.OwnerNodeID == r.Owner.NodeID && r.Identity.OwnerBootID == r.Owner.BootID && r.OwnerSeq != 0
}

// SlotPageRequest continues one bounded owner scan without resetting its deadline.
type SlotPageRequest struct {
	Claim                    GenerationClaim
	Owner                    OwnerCut
	Sequence, AfterSessionID uint64
	MaxRows, MaxBytes        int
}

// SlotPage contains one immutable page. RowsTotal is cumulative for this cut,
// not the current live registry size. Consumers must release the owned backing.
type SlotPage struct {
	Claim                                              GenerationClaim
	Owner                                              OwnerCut
	Sequence, AfterSessionID, NextSessionID, RowsTotal uint64
	Exhausted                                          bool
	Routes                                             []Route
}

// SlotEndRequest acknowledges the exact exhausted page before completion.
type SlotEndRequest struct {
	Claim                              GenerationClaim
	Owner                              OwnerCut
	Sequence, LastSessionID, RowsTotal uint64
}

// SlotEnd is an authenticated echo from the owner; absence is never completion.
type SlotEnd struct {
	Claim                              GenerationClaim
	Owner                              OwnerCut
	Sequence, LastSessionID, RowsTotal uint64
}
