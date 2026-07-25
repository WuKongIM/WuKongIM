package backup

import (
	"context"
	"errors"
	"fmt"
	"strings"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
)

var (
	// ErrCatalogAuditRootBusy means an older retained graph is still owned by
	// an in-progress durable audit cycle and cannot yet be collected.
	ErrCatalogAuditRootBusy = errors.New(
		"backup catalog audit root: older audit cycle is still active",
	)
)

// CatalogAuditRootStore durably advances the oldest retention-protected page
// before Generation GC is allowed to delete an expired graph.
type CatalogAuditRootStore interface {
	AdvanceCatalogAuditRoot(context.Context, uint64) error
}

// ControllerCatalogAuditRootStore linearizes retention with the audit cursor.
type ControllerCatalogAuditRootStore struct {
	state CoordinationStateStore
}

// NewControllerCatalogAuditRootStore creates a Controller-backed retention fence.
func NewControllerCatalogAuditRootStore(
	state CoordinationStateStore,
) (*ControllerCatalogAuditRootStore, error) {
	if state == nil {
		return nil, fmt.Errorf(
			"backup catalog audit root store: state is required",
		)
	}
	return &ControllerCatalogAuditRootStore{state: state}, nil
}

// AdvanceCatalogAuditRoot monotonically advances the retained lower bound. It
// refuses to overtake an unfinished cycle so GC cannot create false corruption.
func (s *ControllerCatalogAuditRootStore) AdvanceCatalogAuditRoot(
	ctx context.Context,
	rootSequence uint64,
) error {
	if s == nil || rootSequence == 0 {
		return fmt.Errorf(
			"backup catalog audit root store: root is invalid",
		)
	}
	for attempt := 0; attempt < maxIntegrityAuditStateRetries; attempt++ {
		state, err := s.state.Load(ctx)
		if err != nil {
			return err
		}
		if state.CatalogHead == nil ||
			rootSequence > state.CatalogHead.Sequence ||
			state.CatalogAuditRootSequence == 0 ||
			rootSequence < state.CatalogAuditRootSequence {
			return fmt.Errorf(
				"backup catalog audit root store: transition is invalid",
			)
		}
		if cursor := state.IntegrityAudit.Cursor; cursor != nil &&
			strings.HasPrefix(cursor.CycleID, "catalog-segments-") &&
			cursor.Phase != backupcontract.IntegrityAuditPhaseComplete &&
			cursor.CatalogRootSequence < rootSequence {
			return ErrCatalogAuditRootBusy
		}
		if rootSequence == state.CatalogAuditRootSequence {
			return nil
		}
		next := state.Clone()
		next.CatalogAuditRootSequence = rootSequence
		err = s.state.CompareAndSwap(ctx, state.Revision, next)
		if err == nil {
			return nil
		}
		if !errors.Is(err, backupcontract.ErrStateConflict) {
			return err
		}
	}
	return backupcontract.ErrStateConflict
}
