package backup

import (
	"context"
	"fmt"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
)

// NoActiveRestoreSource reports no in-process restore. Automatic backup and
// explicit restore mode are mutually exclusive; operators use checkpoint hold
// when a stopped source cluster must retain a recovery cut.
type NoActiveRestoreSource struct{}

// ActiveRestoreCheckpointID implements IntegrityAuditActiveRestoreSource.
func (NoActiveRestoreSource) ActiveRestoreCheckpointID(
	context.Context,
) (string, error) {
	return "", nil
}

// GenerationGCMaintenanceOptions configures Leader-only retention and sweep.
type GenerationGCMaintenanceOptions struct {
	// State supplies the complete current Slot frontier and audit fences.
	State CoordinationStateStore
	// Index authenticates the latest-state checkpoint catalog.
	Index *CheckpointCatalogIndex
	// Collector performs independently cursor-fenced repository deletion.
	Collector *GenerationGarbageCollector
	// Policy selects sparse retained checkpoints.
	Policy backupusecase.CheckpointRetentionPolicy
	// Now supplies the UTC retention and cycle instant.
	Now func() time.Time
}

// GenerationGCMaintenance builds one external protection set and advances one
// bounded Generation sweep only on the current Controller Leader.
type GenerationGCMaintenance struct {
	state     CoordinationStateStore
	index     *CheckpointCatalogIndex
	collector *GenerationGarbageCollector
	policy    backupusecase.CheckpointRetentionPolicy
	now       func() time.Time
}

// NewGenerationGCMaintenance creates the production retention coordinator.
func NewGenerationGCMaintenance(
	options GenerationGCMaintenanceOptions,
) (*GenerationGCMaintenance, error) {
	if options.State == nil || options.Index == nil ||
		options.Collector == nil ||
		options.Policy.MonthlyMonths < 0 ||
		options.Policy.MonthlyMonths > 120 {
		return nil, fmt.Errorf(
			"backup generation GC maintenance: dependencies are invalid",
		)
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &GenerationGCMaintenance{
		state: options.State, index: options.Index,
		collector: options.Collector, policy: options.Policy,
		now: options.Now,
	}, nil
}

// RunIfLeader advances the current deterministic repository cycle.
func (m *GenerationGCMaintenance) RunIfLeader(
	ctx context.Context,
	leadership runtimebackup.CoordinatorLeadership,
) (bool, error) {
	if m == nil || leadership == nil {
		return false, runtimebackup.ErrInvalidCapture
	}
	if leadership.NodeID() != leadership.BackupControllerLeaderID() {
		return false, nil
	}
	state, err := m.state.Load(ctx)
	if err != nil {
		return true, err
	}
	if state.CatalogHead == nil {
		return true, nil
	}
	references, err := m.index.References(ctx, *state.CatalogHead)
	if err != nil {
		return true, err
	}
	now := m.now().UTC()
	decision, err := backupusecase.DecideCheckpointRetention(
		now, references, m.policy, "",
	)
	if err != nil {
		return true, err
	}
	protection := GenerationGCProtection{
		RetainedCatalogRootSequence: state.CatalogAuditRootSequence,
		CatalogRetentionRevision:    state.CatalogRetentionRevision,
		Current:                     make([]backupcontract.SlotFrontier, len(state.SlotFrontiers)),
		IntegrityAudit: backupcontract.CloneIntegrityAuditState(
			state.IntegrityAudit,
		),
	}
	for index := range state.SlotFrontiers {
		protection.Current[index] =
			backupcontract.CloneSlotFrontier(state.SlotFrontiers[index])
	}
	for _, reference := range decision.Retain {
		if reference.Held {
			protection.Held = append(protection.Held, reference)
		} else {
			protection.Retained = append(protection.Retained, reference)
		}
	}
	cycleID, err := generationGCCycleID(
		state.GenerationGCCursors, state.CatalogRetentionRevision,
		state.CatalogHead.Sequence, now,
	)
	if err != nil {
		return true, err
	}
	_, err = m.collector.Collect(ctx, cycleID, protection)
	return true, err
}

func generationGCCycleID(
	cursors []backupcontract.GenerationGCCursor,
	retentionRevision uint64,
	headSequence uint64,
	now time.Time,
) (string, error) {
	cycleID := ""
	for _, cursor := range cursors {
		if cursor.Complete ||
			cursor.CatalogRetentionRevision != retentionRevision {
			continue
		}
		if cycleID != "" && cycleID != cursor.CycleID {
			return "", fmt.Errorf(
				"backup generation GC maintenance: repositories have divergent active cycles",
			)
		}
		cycleID = cursor.CycleID
	}
	if cycleID != "" {
		return cycleID, nil
	}
	return fmt.Sprintf(
		"gc-r%d-h%d-%d", retentionRevision, headSequence,
		now.UTC().UnixMilli(),
	), nil
}

var (
	_ IntegrityAuditActiveRestoreSource   = NoActiveRestoreSource{}
	_ runtimebackup.ControllerMaintenance = (*GenerationGCMaintenance)(nil)
)
