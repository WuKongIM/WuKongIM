package controller

import "errors"

// PresenceRejectReason names the closed node-local check that rejected a
// presence generation. It carries no identities or cause text, so owners can
// log it before the compatible RPC status drops the source error (#1097).
type PresenceRejectReason uint8

const (
	// PresenceRejectUnknown means the error carries no closed reason.
	PresenceRejectUnknown PresenceRejectReason = iota
	// PresenceRejectProtocolNotEnabled means business wiring never enabled presence.
	PresenceRejectProtocolNotEnabled
	// PresenceRejectEmptyGeneration means the claim has no committed generation.
	PresenceRejectEmptyGeneration
	// PresenceRejectGenerationRetired means this node already retired the claim.
	PresenceRejectGenerationRetired
	// PresenceRejectReadViewNotReady means no ready committed cut is published locally.
	PresenceRejectReadViewNotReady
	// PresenceRejectOwnerSetEpochMismatch means the local cut has another owner-set epoch.
	PresenceRejectOwnerSetEpochMismatch
	// PresenceRejectGenerationMismatch means the local cut holds another generation.
	PresenceRejectGenerationMismatch
	// PresenceRejectCommittedCutMismatch means a fresh Controller cut differs from the claim.
	PresenceRejectCommittedCutMismatch
	// PresenceRejectOwnBootNotInOwnerSet means the committed owner set lacks this node's boot.
	PresenceRejectOwnBootNotInOwnerSet
	// PresenceRejectServingBootNotInOwnerSet means the owner set lacks the serving leader's boot.
	PresenceRejectServingBootNotInOwnerSet
	// PresenceRejectServingBootMismatch means a serving check ran under another boot.
	PresenceRejectServingBootMismatch
	// PresenceRejectFenceRouteMismatch means the local route no longer matches the fence.
	PresenceRejectFenceRouteMismatch
	// PresenceRejectFenceNotLocalLeader means the local Slot replica is not the fenced leader.
	PresenceRejectFenceNotLocalLeader
	// PresenceRejectNoPresenceState means the Controller state has no presence section yet.
	PresenceRejectNoPresenceState
	// PresenceRejectHashSlotOutOfRange means the requested hash slot exceeds the configured count.
	PresenceRejectHashSlotOutOfRange
	// PresenceRejectRosterNotCurrent means membership changed after the committed owner set.
	PresenceRejectRosterNotCurrent
	// PresenceRejectControllerStateEmpty means the Controller has not materialized any state.
	PresenceRejectControllerStateEmpty
	presenceRejectReasonCount
)

var presenceRejectReasonNames = [presenceRejectReasonCount]string{
	"unknown",
	"protocol_not_enabled",
	"empty_generation",
	"generation_retired",
	"read_view_not_ready",
	"owner_set_epoch_mismatch",
	"generation_mismatch",
	"committed_cut_mismatch",
	"own_boot_not_in_owner_set",
	"serving_boot_not_in_owner_set",
	"serving_boot_mismatch",
	"fence_route_mismatch",
	"fence_not_local_leader",
	"no_presence_state",
	"hash_slot_out_of_range",
	"roster_not_current",
	"controller_state_empty",
}

// String returns the stable snake_case log value of the reason.
func (r PresenceRejectReason) String() string {
	if r >= presenceRejectReasonCount {
		return presenceRejectReasonNames[PresenceRejectUnknown]
	}
	return presenceRejectReasonNames[r]
}

// ParsePresenceRejectReason maps a stable name back to its reason. Unknown
// names, including "unknown", report false so callers keep a plain sentinel.
func ParsePresenceRejectReason(name string) (PresenceRejectReason, bool) {
	for i := PresenceRejectReason(1); i < presenceRejectReasonCount; i++ {
		if presenceRejectReasonNames[i] == name {
			return i, true
		}
	}
	return PresenceRejectUnknown, false
}

// presenceRejection is a one-byte error value, so returning it on the hot
// serving-check failure path does not allocate.
type presenceRejection PresenceRejectReason

func (e presenceRejection) sentinel() error {
	if PresenceRejectReason(e) == PresenceRejectCommittedCutMismatch {
		return ErrPresenceGenerationConflict
	}
	return ErrPresenceGenerationNotReady
}

func (e presenceRejection) Error() string {
	return e.sentinel().Error() + ": " + PresenceRejectReason(e).String()
}

// Unwrap keeps errors.Is compatible with the existing sentinels.
func (e presenceRejection) Unwrap() error { return e.sentinel() }

// PresenceRejection returns the compatible sentinel error annotated with a
// closed reason. Committed-cut mismatches stay conflicts; all others stay not-ready.
func PresenceRejection(reason PresenceRejectReason) error {
	return presenceRejection(reason)
}

// PresenceRejectReasonOf extracts the closed reason from an error chain,
// returning PresenceRejectUnknown when none was recorded.
func PresenceRejectReasonOf(err error) PresenceRejectReason {
	var rejection presenceRejection
	if errors.As(err, &rejection) {
		return PresenceRejectReason(rejection)
	}
	return PresenceRejectUnknown
}
