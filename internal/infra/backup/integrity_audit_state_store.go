package backup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupruntime "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
)

const (
	maxIntegrityAuditStateRetries    = 8
	generationGCDeleteRequestTimeout = 30 * time.Second
	// The guard lease exceeds the repository deadline plus twice the accepted
	// two-minute cross-node clock skew.
	generationGCDeleteGuardSafetyLease = 10 * time.Minute
)

// ControllerIntegrityAuditStateStore persists the auditor projection without
// overwriting unrelated backup coordination changes.
type ControllerIntegrityAuditStateStore struct {
	state CoordinationStateStore

	operationMu  sync.RWMutex
	projectionMu sync.RWMutex
	projection   backupcontract.IntegrityAuditState
	initialized  bool
}

// NewControllerIntegrityAuditStateStore creates a Controller-backed audit store.
func NewControllerIntegrityAuditStateStore(
	state CoordinationStateStore,
) (*ControllerIntegrityAuditStateStore, error) {
	if state == nil {
		return nil, fmt.Errorf("backup integrity audit store: coordination state is required")
	}
	return &ControllerIntegrityAuditStateStore{state: state}, nil
}

// LoadIntegrityAudit returns one detached bounded auditor state.
func (s *ControllerIntegrityAuditStateStore) LoadIntegrityAudit(
	ctx context.Context,
) (backupcontract.IntegrityAuditState, error) {
	state, err := s.state.Load(ctx)
	if err != nil {
		return backupcontract.IntegrityAuditState{}, err
	}
	s.PublishIntegrityAuditProjection(state.IntegrityAudit)
	return backupcontract.CloneIntegrityAuditState(state.IntegrityAudit), nil
}

// CompareAndSwapIntegrityAudit replaces only the expected audit revision while
// retrying unrelated global Controller state conflicts.
func (s *ControllerIntegrityAuditStateStore) CompareAndSwapIntegrityAudit(
	ctx context.Context,
	expectedRevision uint64,
	next backupcontract.IntegrityAuditState,
) error {
	if next.Revision != expectedRevision+1 {
		return backupcontract.ErrStateConflict
	}
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	for attempt := 0; attempt < maxIntegrityAuditStateRetries; attempt++ {
		state, err := s.state.Load(ctx)
		if err != nil {
			return err
		}
		if state.IntegrityAudit.Revision != expectedRevision {
			return backupcontract.ErrStateConflict
		}
		if next.Cursor != nil &&
			strings.HasPrefix(
				next.Cursor.CycleID, "catalog-segments-",
			) &&
			next.Cursor.CatalogSequence > 0 &&
			next.Cursor.CatalogRootSequence <
				state.CatalogAuditRootSequence {
			// A retention/GC transition advanced after the plan loaded its
			// window. Force a fresh cycle instead of persisting a cursor that
			// can race deletion below the new durable root.
			return backupcontract.ErrStateConflict
		}
		now := time.Now().UTC().UnixMilli()
		currentAuditCycleID := unfinishedCatalogIntegrityAuditCycleID(
			state.IntegrityAudit.Cursor,
		)
		nextAuditCycleID := unfinishedCatalogIntegrityAuditCycleID(
			next.Cursor,
		)
		if nextAuditCycleID != "" &&
			nextAuditCycleID != currentAuditCycleID {
			for _, guard := range state.IntegrityAudit.GCGuards {
				if guard.ExpiresAtUnixMillis > now {
					return backupcontract.ErrStateConflict
				}
				backupcontract.RemoveIntegrityAuditGCGuard(
					&next, guard.HashSlot, guard.Token,
				)
			}
		}
		for _, guard := range state.IntegrityAudit.GCGuards {
			if integrityAuditNewlyFreezesSlot(
				state.IntegrityAudit, next, guard.HashSlot,
			) {
				if guard.ExpiresAtUnixMillis > now {
					return backupcontract.ErrStateConflict
				}
				backupcontract.RemoveIntegrityAuditGCGuard(
					&next, guard.HashSlot, guard.Token,
				)
			}
		}
		replacement := state.Clone()
		replacement.IntegrityAudit = backupcontract.CloneIntegrityAuditState(next)
		err = s.state.CompareAndSwap(ctx, state.Revision, replacement)
		if err == nil {
			s.PublishIntegrityAuditProjection(next)
			return nil
		}
		if !errors.Is(err, backupcontract.ErrStateConflict) {
			return err
		}
	}
	return backupcontract.ErrStateConflict
}

// PublishIntegrityAuditProjection atomically refreshes the narrow hot-path
// projection. Controller apply paths call this on every replicated audit update.
func (s *ControllerIntegrityAuditStateStore) PublishIntegrityAuditProjection(
	state backupcontract.IntegrityAuditState,
) {
	if s == nil {
		return
	}
	s.projectionMu.Lock()
	defer s.projectionMu.Unlock()
	if s.initialized && state.Revision < s.projection.Revision {
		return
	}
	s.projection = backupcontract.CloneIntegrityAuditState(state)
	s.initialized = true
}

// AuditSlotState exposes durable freeze decisions to independent Slot workers.
func (s *ControllerIntegrityAuditStateStore) AuditSlotState(
	ctx context.Context,
	hashSlot uint16,
) (backupcontract.SlotIntegrityAuditState, bool, error) {
	s.projectionMu.RLock()
	if s.initialized {
		slot, found := backupcontract.FindSlotAuditState(s.projection, hashSlot)
		s.projectionMu.RUnlock()
		// Frozen projections are refreshed so a follower Slot worker cannot
		// remain paused after a remote Controller Leader completes recovery.
		if found && slot.Health != backupcontract.SlotAuditHealthy {
			state, err := s.LoadIntegrityAudit(ctx)
			if err != nil {
				return backupcontract.SlotIntegrityAuditState{}, false, err
			}
			slot, found = backupcontract.FindSlotAuditState(state, hashSlot)
		}
		return slot, found, nil
	}
	s.projectionMu.RUnlock()
	state, err := s.LoadIntegrityAudit(ctx)
	if err != nil {
		return backupcontract.SlotIntegrityAuditState{}, false, err
	}
	slot, found := backupcontract.FindSlotAuditState(state, hashSlot)
	return slot, found, nil
}

// RefreshAuditSlotState reloads the durable projection after an atomic frontier
// mutation reports that a remote audit freeze won the Controller CAS.
func (s *ControllerIntegrityAuditStateStore) RefreshAuditSlotState(
	ctx context.Context,
	hashSlot uint16,
) (backupcontract.SlotIntegrityAuditState, bool, error) {
	state, err := s.LoadIntegrityAudit(ctx)
	if err != nil {
		return backupcontract.SlotIntegrityAuditState{}, false, err
	}
	slot, found := backupcontract.FindSlotAuditState(state, hashSlot)
	return slot, found, nil
}

// RunProjection keeps follower Slot workers aligned with locally applied
// Controller snapshots. The final frontier CAS remains the correctness fence;
// this loop makes freeze and unfreeze propagation prompt without per-Slot loads.
func (s *ControllerIntegrityAuditStateStore) RunProjection(
	ctx context.Context,
	interval time.Duration,
) error {
	if s == nil || ctx == nil || interval <= 0 {
		return fmt.Errorf("backup integrity audit store: projection interval is invalid")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		_, _ = s.LoadIntegrityAudit(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// WithGenerationGCDelete linearizes one destructive repository operation with
// a durable Controller guard. The guard survives Controller Leader changes, so
// a new auditor cannot persist a freeze or begin an unmarked sparse selection
// while an old Leader's delete is in flight.
func (s *ControllerIntegrityAuditStateStore) WithGenerationGCDelete(
	ctx context.Context,
	hashSlot uint16,
	protectedAuditCycleID string,
	catalogRetentionRevision uint64,
	deleteObject func(context.Context) (int, error),
) (bool, int, error) {
	if s == nil || deleteObject == nil {
		return false, 0, fmt.Errorf("backup integrity audit store: delete guard is invalid")
	}
	token, err := newIntegrityAuditGCGuardToken()
	if err != nil {
		return false, 0, err
	}
	acquiredAt := time.Now().UTC()
	allowed, err := s.acquireGenerationGCGuard(
		ctx, hashSlot, strings.TrimSpace(protectedAuditCycleID),
		catalogRetentionRevision, token, acquiredAt,
	)
	if err != nil || !allowed {
		return allowed, 0, err
	}
	deleteCtx, cancel := context.WithTimeout(ctx, generationGCDeleteRequestTimeout)
	used, deleteErr := deleteObject(deleteCtx)
	cancel()
	releaseErr := s.releaseGenerationGCGuard(ctx, hashSlot, token)
	return true, used, errors.Join(deleteErr, releaseErr)
}

func (s *ControllerIntegrityAuditStateStore) acquireGenerationGCGuard(
	ctx context.Context,
	hashSlot uint16,
	protectedAuditCycleID string,
	catalogRetentionRevision uint64,
	token string,
	acquiredAt time.Time,
) (bool, error) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	for attempt := 0; attempt < maxIntegrityAuditStateRetries; attempt++ {
		state, err := s.state.Load(ctx)
		if err != nil {
			return false, err
		}
		if state.CatalogRetentionRevision !=
			catalogRetentionRevision {
			return false, nil
		}
		if activeAuditCycleID := unfinishedCatalogIntegrityAuditCycleID(
			state.IntegrityAudit.Cursor,
		); activeAuditCycleID != "" &&
			activeAuditCycleID != protectedAuditCycleID {
			return false, nil
		}
		slot, found := backupcontract.FindSlotAuditState(
			state.IntegrityAudit, hashSlot,
		)
		if found && slot.Health != backupcontract.SlotAuditHealthy {
			s.PublishIntegrityAuditProjection(state.IntegrityAudit)
			return false, nil
		}
		existing, found := backupcontract.FindIntegrityAuditGCGuard(
			state.IntegrityAudit, hashSlot,
		)
		if found && existing.ExpiresAtUnixMillis > acquiredAt.UnixMilli() {
			return false, backupcontract.ErrStateConflict
		}
		next := backupcontract.CloneIntegrityAuditState(state.IntegrityAudit)
		if found {
			backupcontract.RemoveIntegrityAuditGCGuard(
				&next, hashSlot, existing.Token,
			)
		}
		if next.Revision == ^uint64(0) {
			return false, backupcontract.ErrStateConflict
		}
		now := acquiredAt.UnixMilli()
		next.Revision++
		next.UpdatedAtUnixMillis = max(next.UpdatedAtUnixMillis, now)
		backupcontract.UpsertIntegrityAuditGCGuard(
			&next,
			backupcontract.IntegrityAuditGCGuard{
				HashSlot: hashSlot, Token: token,
				AcquiredAtUnixMillis: now,
				ExpiresAtUnixMillis: acquiredAt.Add(
					generationGCDeleteGuardSafetyLease,
				).UnixMilli(),
			},
		)
		replacement := state.Clone()
		replacement.IntegrityAudit = next
		err = s.state.CompareAndSwap(ctx, state.Revision, replacement)
		if err == nil {
			s.PublishIntegrityAuditProjection(next)
			return true, nil
		}
		if !errors.Is(err, backupcontract.ErrStateConflict) {
			return false, err
		}
	}
	return false, backupcontract.ErrStateConflict
}

func unfinishedCatalogIntegrityAuditCycleID(
	cursor *backupcontract.IntegrityAuditCursor,
) string {
	if cursor == nil ||
		!strings.HasPrefix(cursor.CycleID, "catalog-segments-") ||
		cursor.Phase == backupcontract.IntegrityAuditPhaseComplete {
		return ""
	}
	return cursor.CycleID
}

func (s *ControllerIntegrityAuditStateStore) releaseGenerationGCGuard(
	ctx context.Context,
	hashSlot uint16,
	token string,
) error {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	for attempt := 0; attempt < maxIntegrityAuditStateRetries; attempt++ {
		state, err := s.state.Load(ctx)
		if err != nil {
			return err
		}
		next := backupcontract.CloneIntegrityAuditState(state.IntegrityAudit)
		if !backupcontract.RemoveIntegrityAuditGCGuard(&next, hashSlot, token) {
			if existing, found := backupcontract.FindIntegrityAuditGCGuard(
				state.IntegrityAudit, hashSlot,
			); !found {
				return nil
			} else if existing.Token != token {
				return backupcontract.ErrStateConflict
			}
		}
		if next.Revision == ^uint64(0) {
			return backupcontract.ErrStateConflict
		}
		next.Revision++
		next.UpdatedAtUnixMillis = max(
			next.UpdatedAtUnixMillis, time.Now().UTC().UnixMilli(),
		)
		replacement := state.Clone()
		replacement.IntegrityAudit = next
		err = s.state.CompareAndSwap(ctx, state.Revision, replacement)
		if err == nil {
			s.PublishIntegrityAuditProjection(next)
			return nil
		}
		if !errors.Is(err, backupcontract.ErrStateConflict) {
			return err
		}
	}
	return backupcontract.ErrStateConflict
}

func integrityAuditNewlyFreezesSlot(
	current backupcontract.IntegrityAuditState,
	next backupcontract.IntegrityAuditState,
	hashSlot uint16,
) bool {
	before, beforeFound := backupcontract.FindSlotAuditState(current, hashSlot)
	after, afterFound := backupcontract.FindSlotAuditState(next, hashSlot)
	beforeFrozen := beforeFound && before.Health != backupcontract.SlotAuditHealthy
	afterFrozen := afterFound && after.Health != backupcontract.SlotAuditHealthy
	return !beforeFrozen && afterFrozen
}

func newIntegrityAuditGCGuardToken() (string, error) {
	var body [16]byte
	if _, err := rand.Read(body[:]); err != nil {
		return "", fmt.Errorf("backup integrity audit store: create GC guard: %w", err)
	}
	return "gc-" + hex.EncodeToString(body[:]), nil
}

var _ backupruntime.IntegrityAuditStateStore = (*ControllerIntegrityAuditStateStore)(nil)
var _ backupruntime.SlotIntegrityAuditGate = (*ControllerIntegrityAuditStateStore)(nil)
var _ GenerationGCIntegrityGuard = (*ControllerIntegrityAuditStateStore)(nil)
