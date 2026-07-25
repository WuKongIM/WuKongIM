package backup

import (
	"sort"
	"strings"
)

// IntegrityAuditPhase identifies one crash-resumable artifact audit step.
type IntegrityAuditPhase string

const (
	// IntegrityAuditPhaseInspect performs full GET, authentication, decrypt, and digest checks.
	IntegrityAuditPhaseInspect IntegrityAuditPhase = "inspect"
	// IntegrityAuditPhaseRepair copies one authenticated healthy repository object graph.
	IntegrityAuditPhaseRepair IntegrityAuditPhase = "repair"
	// IntegrityAuditPhaseRevalidate repeats the complete check after repair.
	IntegrityAuditPhaseRevalidate IntegrityAuditPhase = "revalidate"
	// IntegrityAuditPhaseRebase waits for live-source Generation replacement.
	IntegrityAuditPhaseRebase IntegrityAuditPhase = "rebase"
	// IntegrityAuditPhaseComplete marks a fixed audit decision fully consumed.
	IntegrityAuditPhaseComplete IntegrityAuditPhase = "complete"
)

// IntegrityCorruptionCategory is a bounded repository failure classification.
type IntegrityCorruptionCategory string

const (
	// IntegrityCorruptionMissing means a referenced object does not exist.
	IntegrityCorruptionMissing IntegrityCorruptionCategory = "missing"
	// IntegrityCorruptionChecksum means stored size or checksum evidence disagrees.
	IntegrityCorruptionChecksum IntegrityCorruptionCategory = "checksum"
	// IntegrityCorruptionCiphertext means ciphertext, AEAD, or plaintext digest validation failed.
	IntegrityCorruptionCiphertext IntegrityCorruptionCategory = "ciphertext"
	// IntegrityCorruptionCommitProof means a signed visibility proof is invalid.
	IntegrityCorruptionCommitProof IntegrityCorruptionCategory = "commit_proof"
)

// SlotAuditHealth controls only one Hash Slot's capture and GC admission.
type SlotAuditHealth string

const (
	// SlotAuditHealthy permits ordinary capture and Generation collection.
	SlotAuditHealthy SlotAuditHealth = "healthy"
	// SlotAuditDegraded freezes the Slot while a healthy copy repairs its peer.
	SlotAuditDegraded SlotAuditHealth = "degraded"
	// SlotAuditRebaseRequired freezes the old Generation while live source replaces it.
	SlotAuditRebaseRequired SlotAuditHealth = "rebase_required"
	// SlotAuditFailed freezes unrecoverable repository state and requires operator action.
	SlotAuditFailed SlotAuditHealth = "failed"
)

// IntegrityAuditCursor is one opaque, bounded, durable scan position.
type IntegrityAuditCursor struct {
	// CycleID identifies one fixed catalog/frontier audit decision.
	CycleID string
	// ScrubEpoch identifies the periodic latent-damage pass. New catalog
	// checkpoints may advance within the same epoch without restarting history.
	ScrubEpoch uint64
	// CatalogSequence is the immutable catalog head included by CycleID.
	CatalogSequence uint64
	// CatalogRootSequence is the oldest retained page fixed by this cycle.
	CatalogRootSequence uint64
	// HashSlot and Generation identify the artifact's isolation boundary.
	HashSlot   uint16
	Generation string
	// Position is an opaque backend continuation. It must not contain secrets.
	Position string
	// ResumeHashSlot, ResumeGeneration, and ResumePosition retain the next
	// artifact while repair or rebase owns Position.
	ResumeHashSlot   uint16
	ResumeGeneration string
	ResumePosition   string
	// ResumePhase preserves whether the continuation is another artifact or
	// the fixed cycle's terminal cursor.
	ResumePhase IntegrityAuditPhase
	// Phase selects inspect, repair, revalidate, rebase, or complete.
	Phase IntegrityAuditPhase
	// Repository is the damaged copy selected for repair.
	Repository string
	// Category is the bounded detected corruption class.
	Category IntegrityCorruptionCategory
	// UpdatedAtUnixMillis is the latest durable cursor transition time.
	UpdatedAtUnixMillis int64
}

// SlotIntegrityAuditState is one compact durable Slot health projection.
type SlotIntegrityAuditState struct {
	// HashSlot identifies the independently frozen logical partition.
	HashSlot uint16
	// Generation is the affected immutable Slot graph.
	Generation string
	// Health is healthy, degraded, rebase_required, or failed.
	Health SlotAuditHealth
	// Repository and Category describe the current bounded repair reason.
	Repository string
	Category   IntegrityCorruptionCategory
	// LastSuccessAtUnixMillis is the latest complete artifact validation.
	LastSuccessAtUnixMillis int64
	// UpdatedAtUnixMillis is the latest health transition time.
	UpdatedAtUnixMillis int64
}

// IntegrityAuditGCGuard is a durable cross-Controller-Leader exclusion record.
// While present, the auditor cannot newly freeze HashSlot and GC may execute at
// most the exact repository operation identified by Token.
type IntegrityAuditGCGuard struct {
	// HashSlot identifies the Generation graph being considered for deletion.
	HashSlot uint16
	// Token uniquely identifies one in-flight external delete operation.
	Token string
	// AcquiredAtUnixMillis is operator-visible evidence for stuck-guard recovery.
	AcquiredAtUnixMillis int64
	// ExpiresAtUnixMillis is later than the bounded repository request deadline;
	// a new Leader may reclaim the guard only after this safety lease.
	ExpiresAtUnixMillis int64
}

// IntegrityAuditState is bounded Controller coordination for one background auditor.
type IntegrityAuditState struct {
	// Revision fences independent audit transitions from unrelated Controller changes.
	Revision uint64
	// Cursor is nil before the first cycle.
	Cursor *IntegrityAuditCursor
	// Slots contains at most one sorted health record per configured Hash Slot.
	Slots []SlotIntegrityAuditState
	// GCGuards contains at most one sorted in-flight delete guard per Hash Slot.
	GCGuards []IntegrityAuditGCGuard
	// DebtObjects is the latest bounded estimate of artifacts awaiting full validation.
	DebtObjects uint64
	// LastSuccessAtUnixMillis is the latest successful full artifact validation.
	LastSuccessAtUnixMillis int64
	// UpdatedAtUnixMillis is the latest durable auditor progress time.
	UpdatedAtUnixMillis int64
}

// CloneIntegrityAuditState returns a detached state safe for mutation.
func CloneIntegrityAuditState(state IntegrityAuditState) IntegrityAuditState {
	out := state
	if state.Cursor != nil {
		cursor := *state.Cursor
		out.Cursor = &cursor
	}
	out.Slots = append([]SlotIntegrityAuditState(nil), state.Slots...)
	out.GCGuards = append([]IntegrityAuditGCGuard(nil), state.GCGuards...)
	return out
}

// FindIntegrityAuditGCGuard returns one detached durable delete guard.
func FindIntegrityAuditGCGuard(
	state IntegrityAuditState,
	hashSlot uint16,
) (IntegrityAuditGCGuard, bool) {
	index := sort.Search(len(state.GCGuards), func(index int) bool {
		return state.GCGuards[index].HashSlot >= hashSlot
	})
	if index >= len(state.GCGuards) || state.GCGuards[index].HashSlot != hashSlot {
		return IntegrityAuditGCGuard{}, false
	}
	return state.GCGuards[index], true
}

// UpsertIntegrityAuditGCGuard inserts one sorted durable delete guard.
func UpsertIntegrityAuditGCGuard(
	state *IntegrityAuditState,
	guard IntegrityAuditGCGuard,
) {
	if state == nil {
		return
	}
	guard.Token = strings.TrimSpace(guard.Token)
	index := sort.Search(len(state.GCGuards), func(index int) bool {
		return state.GCGuards[index].HashSlot >= guard.HashSlot
	})
	if index < len(state.GCGuards) && state.GCGuards[index].HashSlot == guard.HashSlot {
		state.GCGuards[index] = guard
		return
	}
	state.GCGuards = append(state.GCGuards, IntegrityAuditGCGuard{})
	copy(state.GCGuards[index+1:], state.GCGuards[index:])
	state.GCGuards[index] = guard
}

// RemoveIntegrityAuditGCGuard removes only the matching operation token.
func RemoveIntegrityAuditGCGuard(
	state *IntegrityAuditState,
	hashSlot uint16,
	token string,
) bool {
	if state == nil {
		return false
	}
	index := sort.Search(len(state.GCGuards), func(index int) bool {
		return state.GCGuards[index].HashSlot >= hashSlot
	})
	if index >= len(state.GCGuards) ||
		state.GCGuards[index].HashSlot != hashSlot ||
		state.GCGuards[index].Token != token {
		return false
	}
	copy(state.GCGuards[index:], state.GCGuards[index+1:])
	state.GCGuards = state.GCGuards[:len(state.GCGuards)-1]
	return true
}

// FrozenAuditHashSlots returns sorted Slots whose frontier and every Generation
// must remain protected while audit recovery is incomplete.
func FrozenAuditHashSlots(state IntegrityAuditState) []uint16 {
	result := make([]uint16, 0, len(state.Slots))
	for _, slot := range state.Slots {
		switch slot.Health {
		case SlotAuditDegraded, SlotAuditRebaseRequired, SlotAuditFailed:
			result = append(result, slot.HashSlot)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

// FindSlotAuditState returns a detached per-Slot audit projection.
func FindSlotAuditState(state IntegrityAuditState, hashSlot uint16) (SlotIntegrityAuditState, bool) {
	index := sort.Search(len(state.Slots), func(index int) bool {
		return state.Slots[index].HashSlot >= hashSlot
	})
	if index >= len(state.Slots) || state.Slots[index].HashSlot != hashSlot {
		return SlotIntegrityAuditState{}, false
	}
	return state.Slots[index], true
}

// UpsertSlotAuditState replaces one Slot state while preserving sorted bounded storage.
func UpsertSlotAuditState(state *IntegrityAuditState, slot SlotIntegrityAuditState) {
	if state == nil {
		return
	}
	slot.Generation = strings.TrimSpace(slot.Generation)
	index := sort.Search(len(state.Slots), func(index int) bool {
		return state.Slots[index].HashSlot >= slot.HashSlot
	})
	if index < len(state.Slots) && state.Slots[index].HashSlot == slot.HashSlot {
		state.Slots[index] = slot
		return
	}
	state.Slots = append(state.Slots, SlotIntegrityAuditState{})
	copy(state.Slots[index+1:], state.Slots[index:])
	state.Slots[index] = slot
}
