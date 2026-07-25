package backup

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
)

var (
	// ErrIntegrityAuditFrozen reports that durable audit recovery paused one Slot.
	ErrIntegrityAuditFrozen = errors.New("backup runtime: Slot frozen by integrity audit")
	// ErrIntegrityAuditUnrecoverable reports dual-repository loss without live source.
	ErrIntegrityAuditUnrecoverable = errors.New("backup runtime: integrity audit is unrecoverable")
)

// IntegrityAuditCopy is one explicit repository's full artifact validation result.
type IntegrityAuditCopy struct {
	// Repository identifies one configured failure domain.
	Repository string
	// Healthy means commit proof, stored bytes, decrypt, and plaintext digest all passed.
	Healthy bool
	// Category classifies missing, checksum, ciphertext, or commit-proof failure.
	Category backupcontract.IntegrityCorruptionCategory
}

// IntegrityAuditInspection reports one artifact and its durable continuation.
type IntegrityAuditInspection struct {
	// Copies must contain exactly two distinct repository results.
	Copies []IntegrityAuditCopy
	// Next is the next artifact or a complete cursor for the same fixed cycle.
	Next backupcontract.IntegrityAuditCursor
	// ArtifactBytes is the stored byte size used for repair accounting.
	ArtifactBytes int64
	// DebtObjects is the bounded remaining-artifact estimate after this inspection.
	DebtObjects uint64
	// Administrative means Next only advances a bounded catalog-navigation
	// cursor and does not represent a Slot artifact success.
	Administrative bool
}

// IntegrityAuditBackend plans fixed cycles and fully validates or repairs one artifact.
type IntegrityAuditBackend interface {
	// Start starts after the last durable cursor. Implementations use its catalog
	// sequence to avoid rescanning immutable history on every checkpoint.
	Start(context.Context, *backupcontract.IntegrityAuditCursor) (backupcontract.IntegrityAuditCursor, uint64, error)
	// Inspect performs GET, signature, ciphertext, decrypt, and plaintext digest checks.
	Inspect(context.Context, backupcontract.IntegrityAuditCursor) (IntegrityAuditInspection, error)
	// Repair copies exact authenticated bytes from the healthy repository into target.
	Repair(context.Context, backupcontract.IntegrityAuditCursor, string) (int64, error)
}

// IntegrityAuditStateStore persists one independently fenced bounded audit state.
type IntegrityAuditStateStore interface {
	LoadIntegrityAudit(context.Context) (backupcontract.IntegrityAuditState, error)
	CompareAndSwapIntegrityAudit(
		context.Context,
		uint64,
		backupcontract.IntegrityAuditState,
	) error
}

// IntegrityAuditRecovery handles dual-copy loss outside the auditor state machine.
type IntegrityAuditRecovery interface {
	// SourceAvailable reports whether the live authoritative Slot can be materialized.
	SourceAvailable(context.Context, uint16, string) (bool, error)
	// RequestRebase requests replacement of the damaged Generation and reports
	// completion only after the new Generation passed promotion validation.
	RequestRebase(context.Context, uint16, string) (IntegrityAuditRebaseResult, error)
}

// IntegrityAuditRebaseResult is bounded evidence from Slot-local recovery.
type IntegrityAuditRebaseResult struct {
	// Complete is false while the asynchronous replacement remains in progress.
	Complete bool
	// Generation identifies the validated promoted replacement when Complete.
	Generation string
}

// IntegrityAuditObserver receives low-cardinality progress and repair evidence.
type IntegrityAuditObserver interface {
	SetBackupAuditDebt(uint64)
	SetBackupAuditLastSuccess(int64)
	ObserveBackupAuditCorruption(string, string)
	AddBackupAuditRepairBytes(string, int64)
	ObserveBackupAuditUnrecoverable()
}

// IntegrityAuditorOptions configures one Controller-Leader-driven background auditor.
type IntegrityAuditorOptions struct {
	// Backend plans and validates one committed artifact per step.
	Backend IntegrityAuditBackend
	// State persists the independently revision-fenced continuation.
	State IntegrityAuditStateStore
	// Recovery replaces a Generation after dual-repository loss.
	Recovery IntegrityAuditRecovery
	// Observer receives low-cardinality durable progress projections.
	Observer IntegrityAuditObserver
	// Now supplies deterministic UTC transition timestamps.
	Now func() time.Time
}

// IntegrityAuditor advances at most one durable state-machine transition per call.
type IntegrityAuditor struct {
	backend  IntegrityAuditBackend
	state    IntegrityAuditStateStore
	recovery IntegrityAuditRecovery
	observer IntegrityAuditObserver
	now      func() time.Time
}

// NewIntegrityAuditor creates a crash-resumable, Slot-isolated auditor.
func NewIntegrityAuditor(options IntegrityAuditorOptions) (*IntegrityAuditor, error) {
	if options.Backend == nil || options.State == nil || options.Recovery == nil {
		return nil, fmt.Errorf("%w: integrity auditor dependencies are incomplete", ErrInvalidCapture)
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &IntegrityAuditor{
		backend: options.Backend, state: options.State, recovery: options.Recovery,
		observer: options.Observer, now: options.Now,
	}, nil
}

// RunStep advances one bounded audit transition. A detected repair is first
// persisted as degraded; copying happens only on a later call.
func (a *IntegrityAuditor) RunStep(ctx context.Context) (backupcontract.IntegrityAuditState, error) {
	if a == nil {
		return backupcontract.IntegrityAuditState{}, ErrInvalidCapture
	}
	state, err := a.state.LoadIntegrityAudit(ctx)
	if err != nil {
		return backupcontract.IntegrityAuditState{}, err
	}
	a.projectState(state)
	if state.Cursor == nil || state.Cursor.Phase == backupcontract.IntegrityAuditPhaseComplete {
		return a.startCycle(ctx, state)
	}
	switch state.Cursor.Phase {
	case backupcontract.IntegrityAuditPhaseInspect, backupcontract.IntegrityAuditPhaseRevalidate:
		return a.inspect(ctx, state)
	case backupcontract.IntegrityAuditPhaseRepair:
		return a.repair(ctx, state)
	case backupcontract.IntegrityAuditPhaseRebase:
		return a.rebase(ctx, state)
	default:
		return state, fmt.Errorf("%w: unknown integrity audit phase", ErrInvalidCapture)
	}
}

func (a *IntegrityAuditor) startCycle(
	ctx context.Context,
	state backupcontract.IntegrityAuditState,
) (backupcontract.IntegrityAuditState, error) {
	cursor, debt, err := a.backend.Start(ctx, state.Cursor)
	if err != nil {
		return state, err
	}
	if err := validateAuditCursor(cursor); err != nil {
		return state, err
	}
	if state.Cursor != nil && *state.Cursor == cursor &&
		state.DebtObjects == debt {
		return backupcontract.CloneIntegrityAuditState(state), nil
	}
	next := backupcontract.CloneIntegrityAuditState(state)
	next.Cursor = &cursor
	next.DebtObjects = debt
	return a.persist(ctx, state.Revision, next)
}

func (a *IntegrityAuditor) inspect(
	ctx context.Context,
	state backupcontract.IntegrityAuditState,
) (backupcontract.IntegrityAuditState, error) {
	current := *state.Cursor
	inspection, err := a.backend.Inspect(ctx, current)
	if err != nil {
		return state, err
	}
	if err := validateAuditInspection(current, inspection); err != nil {
		return state, err
	}
	healthy, damaged := splitAuditCopies(inspection.Copies)
	next := backupcontract.CloneIntegrityAuditState(state)
	next.DebtObjects = inspection.DebtObjects
	now := a.now().UTC().UnixMilli()
	if inspection.Administrative {
		advanceAuditCursor(&next, inspection.Next, now)
		return a.persist(ctx, state.Revision, next)
	}
	switch len(healthy) {
	case 2:
		advanceAuditCursor(&next, inspection.Next, now)
		slot, found := backupcontract.FindSlotAuditState(next, current.HashSlot)
		if !found ||
			slot.Health == backupcontract.SlotAuditHealthy ||
			(slot.Generation == current.Generation &&
				slot.Health != backupcontract.SlotAuditRebaseRequired &&
				slot.Health != backupcontract.SlotAuditFailed) {
			backupcontract.UpsertSlotAuditState(&next, backupcontract.SlotIntegrityAuditState{
				HashSlot: current.HashSlot, Generation: current.Generation,
				Health:                  backupcontract.SlotAuditHealthy,
				LastSuccessAtUnixMillis: now, UpdatedAtUnixMillis: now,
			})
		}
		next.LastSuccessAtUnixMillis = now
		return a.persist(ctx, state.Revision, next)
	case 1:
		target := damaged[0]
		firstDetection := current.Phase == backupcontract.IntegrityAuditPhaseInspect
		current.Phase = backupcontract.IntegrityAuditPhaseRepair
		current.Repository = target.Repository
		current.Category = target.Category
		setAuditResume(&current, inspection.Next)
		current.UpdatedAtUnixMillis = now
		next.Cursor = &current
		backupcontract.UpsertSlotAuditState(&next, backupcontract.SlotIntegrityAuditState{
			HashSlot: current.HashSlot, Generation: current.Generation,
			Health:     backupcontract.SlotAuditDegraded,
			Repository: target.Repository, Category: target.Category,
			UpdatedAtUnixMillis: now,
		})
		persisted, err := a.persist(ctx, state.Revision, next)
		if err == nil && firstDetection && a.observer != nil {
			a.observer.ObserveBackupAuditCorruption(
				string(target.Category), target.Repository,
			)
		}
		return persisted, err
	case 0:
		return a.handleDualCorruption(ctx, state, next, current, inspection, now)
	default:
		return state, ErrInvalidCapture
	}
}

func (a *IntegrityAuditor) repair(
	ctx context.Context,
	state backupcontract.IntegrityAuditState,
) (backupcontract.IntegrityAuditState, error) {
	cursor := *state.Cursor
	repairedBytes, err := a.backend.Repair(ctx, cursor, cursor.Repository)
	if err != nil {
		return state, err
	}
	if repairedBytes < 0 {
		return state, fmt.Errorf("%w: repair byte evidence is invalid", ErrInvalidCapture)
	}
	now := a.now().UTC().UnixMilli()
	next := backupcontract.CloneIntegrityAuditState(state)
	cursor.Phase = backupcontract.IntegrityAuditPhaseRevalidate
	cursor.UpdatedAtUnixMillis = now
	next.Cursor = &cursor
	persisted, persistErr := a.persist(ctx, state.Revision, next)
	if persistErr == nil && a.observer != nil && repairedBytes > 0 {
		a.observer.AddBackupAuditRepairBytes(cursor.Repository, repairedBytes)
	}
	return persisted, persistErr
}

func (a *IntegrityAuditor) handleDualCorruption(
	ctx context.Context,
	state backupcontract.IntegrityAuditState,
	next backupcontract.IntegrityAuditState,
	current backupcontract.IntegrityAuditCursor,
	inspection IntegrityAuditInspection,
	now int64,
) (backupcontract.IntegrityAuditState, error) {
	firstDetection := current.Phase == backupcontract.IntegrityAuditPhaseInspect
	category := strongestAuditCategory(inspection.Copies)
	current.Phase = backupcontract.IntegrityAuditPhaseRebase
	current.Repository = ""
	current.Category = category
	setAuditResume(&current, inspection.Next)
	current.UpdatedAtUnixMillis = now
	next.Cursor = &current
	backupcontract.UpsertSlotAuditState(&next, backupcontract.SlotIntegrityAuditState{
		HashSlot: current.HashSlot, Generation: current.Generation,
		Health:   backupcontract.SlotAuditRebaseRequired,
		Category: category, UpdatedAtUnixMillis: now,
	})
	persisted, persistErr := a.persist(ctx, state.Revision, next)
	if persistErr == nil && firstDetection {
		a.observeDualCorruption(inspection.Copies)
	}
	return persisted, persistErr
}

func (a *IntegrityAuditor) observeDualCorruption(copies []IntegrityAuditCopy) {
	if a.observer == nil {
		return
	}
	for _, copyResult := range copies {
		a.observer.ObserveBackupAuditCorruption(
			string(copyResult.Category), copyResult.Repository,
		)
	}
}

func (a *IntegrityAuditor) rebase(
	ctx context.Context,
	state backupcontract.IntegrityAuditState,
) (backupcontract.IntegrityAuditState, error) {
	cursor := *state.Cursor
	available, err := a.recovery.SourceAvailable(
		ctx, cursor.HashSlot, cursor.Generation,
	)
	if err != nil {
		return state, err
	}
	now := a.now().UTC().UnixMilli()
	if !available {
		next := backupcontract.CloneIntegrityAuditState(state)
		resumeAuditCursor(&next, cursor, now)
		backupcontract.UpsertSlotAuditState(&next, backupcontract.SlotIntegrityAuditState{
			HashSlot: cursor.HashSlot, Generation: cursor.Generation,
			Health:   backupcontract.SlotAuditFailed,
			Category: cursor.Category, UpdatedAtUnixMillis: now,
		})
		persisted, persistErr := a.persist(ctx, state.Revision, next)
		if persistErr != nil {
			return persisted, persistErr
		}
		if a.observer != nil {
			a.observer.ObserveBackupAuditUnrecoverable()
		}
		return persisted, ErrIntegrityAuditUnrecoverable
	}
	result, err := a.recovery.RequestRebase(
		ctx, cursor.HashSlot, cursor.Generation,
	)
	if err != nil {
		return state, err
	}
	if !result.Complete {
		return backupcontract.CloneIntegrityAuditState(state), nil
	}
	if strings.TrimSpace(result.Generation) == "" ||
		result.Generation == cursor.Generation {
		return state, fmt.Errorf("%w: completed audit rebase has no replacement Generation", ErrInvalidCapture)
	}
	next := backupcontract.CloneIntegrityAuditState(state)
	resumeAuditCursor(&next, cursor, now)
	backupcontract.UpsertSlotAuditState(&next, backupcontract.SlotIntegrityAuditState{
		HashSlot: cursor.HashSlot, Generation: result.Generation,
		Health:                  backupcontract.SlotAuditHealthy,
		LastSuccessAtUnixMillis: now, UpdatedAtUnixMillis: now,
	})
	next.LastSuccessAtUnixMillis = now
	return a.persist(ctx, state.Revision, next)
}

func (a *IntegrityAuditor) persist(
	ctx context.Context,
	expectedRevision uint64,
	next backupcontract.IntegrityAuditState,
) (backupcontract.IntegrityAuditState, error) {
	if expectedRevision == math.MaxUint64 {
		return next, fmt.Errorf("%w: integrity audit revision exhausted", ErrInvalidCapture)
	}
	now := a.now().UTC().UnixMilli()
	next.Revision = expectedRevision + 1
	next.UpdatedAtUnixMillis = now
	if next.Cursor != nil {
		next.Cursor.UpdatedAtUnixMillis = now
	}
	if err := a.state.CompareAndSwapIntegrityAudit(ctx, expectedRevision, next); err != nil {
		return next, err
	}
	a.projectState(next)
	return backupcontract.CloneIntegrityAuditState(next), nil
}

func (a *IntegrityAuditor) projectState(state backupcontract.IntegrityAuditState) {
	if a.observer == nil {
		return
	}
	a.observer.SetBackupAuditDebt(state.DebtObjects)
	a.observer.SetBackupAuditLastSuccess(state.LastSuccessAtUnixMillis)
}

func validateAuditCursor(cursor backupcontract.IntegrityAuditCursor) error {
	if strings.TrimSpace(cursor.CycleID) == "" ||
		(strings.HasPrefix(cursor.CycleID, "catalog-segments-") &&
			(cursor.ScrubEpoch == 0 ||
				(cursor.CatalogSequence > 0 &&
					(cursor.CatalogRootSequence == 0 ||
						cursor.CatalogRootSequence >
							cursor.CatalogSequence)))) ||
		strings.TrimSpace(cursor.Generation) == "" ||
		strings.TrimSpace(cursor.Position) == "" ||
		(cursor.Phase != backupcontract.IntegrityAuditPhaseInspect &&
			cursor.Phase != backupcontract.IntegrityAuditPhaseComplete) {
		return fmt.Errorf("%w: integrity audit cursor is invalid", ErrInvalidCapture)
	}
	return nil
}

func validateAuditInspection(
	current backupcontract.IntegrityAuditCursor,
	inspection IntegrityAuditInspection,
) error {
	if len(inspection.Copies) != 2 ||
		(!inspection.Administrative && inspection.ArtifactBytes <= 0) ||
		(inspection.Administrative && inspection.ArtifactBytes != 0) ||
		strings.TrimSpace(inspection.Copies[0].Repository) == "" ||
		strings.TrimSpace(inspection.Copies[1].Repository) == "" ||
		inspection.Copies[0].Repository == inspection.Copies[1].Repository ||
		strings.TrimSpace(inspection.Next.CycleID) == "" ||
		inspection.Next.CycleID != current.CycleID ||
		inspection.Next.ScrubEpoch != current.ScrubEpoch ||
		inspection.Next.CatalogSequence != current.CatalogSequence ||
		inspection.Next.CatalogRootSequence !=
			current.CatalogRootSequence ||
		strings.TrimSpace(inspection.Next.Generation) == "" ||
		strings.TrimSpace(inspection.Next.Position) == "" ||
		(inspection.Next.Phase != backupcontract.IntegrityAuditPhaseInspect &&
			inspection.Next.Phase != backupcontract.IntegrityAuditPhaseComplete) ||
		(inspection.Next.Phase == backupcontract.IntegrityAuditPhaseInspect &&
			inspection.Next.HashSlot == current.HashSlot &&
			inspection.Next.Generation == current.Generation &&
			inspection.Next.Position == current.Position) {
		return fmt.Errorf("%w: integrity audit inspection is invalid", ErrInvalidCapture)
	}
	for _, copyResult := range inspection.Copies {
		if inspection.Administrative && !copyResult.Healthy {
			return fmt.Errorf("%w: administrative audit step is unhealthy", ErrInvalidCapture)
		}
		if copyResult.Healthy {
			if copyResult.Category != "" {
				return fmt.Errorf("%w: healthy audit copy has a failure category", ErrInvalidCapture)
			}
			continue
		}
		switch copyResult.Category {
		case backupcontract.IntegrityCorruptionMissing,
			backupcontract.IntegrityCorruptionChecksum,
			backupcontract.IntegrityCorruptionCiphertext,
			backupcontract.IntegrityCorruptionCommitProof:
		default:
			return fmt.Errorf("%w: audit copy category is invalid", ErrInvalidCapture)
		}
	}
	return nil
}

func splitAuditCopies(copies []IntegrityAuditCopy) (healthy, damaged []IntegrityAuditCopy) {
	for _, copyResult := range copies {
		if copyResult.Healthy {
			healthy = append(healthy, copyResult)
		} else {
			damaged = append(damaged, copyResult)
		}
	}
	return healthy, damaged
}

func strongestAuditCategory(copies []IntegrityAuditCopy) backupcontract.IntegrityCorruptionCategory {
	rank := map[backupcontract.IntegrityCorruptionCategory]int{
		backupcontract.IntegrityCorruptionMissing:     1,
		backupcontract.IntegrityCorruptionChecksum:    2,
		backupcontract.IntegrityCorruptionCiphertext:  3,
		backupcontract.IntegrityCorruptionCommitProof: 4,
	}
	var selected backupcontract.IntegrityCorruptionCategory
	for _, copyResult := range copies {
		if rank[copyResult.Category] > rank[selected] {
			selected = copyResult.Category
		}
	}
	return selected
}

func setAuditResume(cursor *backupcontract.IntegrityAuditCursor, next backupcontract.IntegrityAuditCursor) {
	cursor.ResumeHashSlot = next.HashSlot
	cursor.ResumeGeneration = next.Generation
	cursor.ResumePosition = next.Position
	cursor.ResumePhase = next.Phase
}

func advanceAuditCursor(
	state *backupcontract.IntegrityAuditState,
	next backupcontract.IntegrityAuditCursor,
	now int64,
) {
	next.Repository = ""
	next.Category = ""
	next.ResumeHashSlot = 0
	next.ResumeGeneration = ""
	next.ResumePosition = ""
	next.ResumePhase = ""
	next.UpdatedAtUnixMillis = now
	state.Cursor = &next
}

func resumeAuditCursor(
	state *backupcontract.IntegrityAuditState,
	current backupcontract.IntegrityAuditCursor,
	now int64,
) {
	next := backupcontract.IntegrityAuditCursor{
		CycleID: current.CycleID, ScrubEpoch: current.ScrubEpoch,
		CatalogSequence:     current.CatalogSequence,
		CatalogRootSequence: current.CatalogRootSequence,
		HashSlot:            current.ResumeHashSlot, Generation: current.ResumeGeneration,
		Position: current.ResumePosition, Phase: current.ResumePhase,
		UpdatedAtUnixMillis: now,
	}
	state.Cursor = &next
}
