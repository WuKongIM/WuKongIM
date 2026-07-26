package backup

import (
	"context"
	"fmt"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
)

// IntegrityAuditSourceProbe reports whether one damaged Slot can still be materialized.
type IntegrityAuditSourceProbe interface {
	SourceAvailable(context.Context, uint16, string) (bool, error)
}

// IntegrityAuditFrontierObserver reads the durable replacement produced by the
// target Slot Leader's normal capture worker.
type IntegrityAuditFrontierObserver interface {
	Load(context.Context, uint16) (FrontierSnapshot, error)
}

// FrontierIntegrityAuditSourceProbe verifies that the routed live source can
// still expose committed cuts for the damaged Slot Generation.
type FrontierIntegrityAuditSourceProbe struct {
	frontiers IntegrityAuditFrontierObserver
	source    ContinuousSource
}

// NewFrontierIntegrityAuditSourceProbe creates a routed live-source probe.
func NewFrontierIntegrityAuditSourceProbe(
	frontiers IntegrityAuditFrontierObserver,
	source ContinuousSource,
) (*FrontierIntegrityAuditSourceProbe, error) {
	if frontiers == nil || source == nil {
		return nil, fmt.Errorf("backup integrity audit source probe: dependencies are required")
	}
	return &FrontierIntegrityAuditSourceProbe{
		frontiers: frontiers, source: source,
	}, nil
}

// SourceAvailable returns false only when no durable Slot source exists.
// Routed high-watermark errors remain retryable instead of being misclassified
// as permanent dual-copy loss.
func (p *FrontierIntegrityAuditSourceProbe) SourceAvailable(
	ctx context.Context,
	hashSlot uint16,
	damagedGeneration string,
) (bool, error) {
	snapshot, err := p.frontiers.Load(ctx, hashSlot)
	if err != nil {
		return false, err
	}
	if !snapshot.Found {
		return false, nil
	}
	if snapshot.Frontier.Generation != damagedGeneration {
		return exactIntegrityAuditPromotion(
			snapshot.Frontier, damagedGeneration,
		), nil
	}
	if _, err := p.source.HighWatermarks(
		ctx, hashSlot, snapshot.Frontier,
	); err != nil {
		return false, err
	}
	return true, nil
}

// CaptureIntegrityAuditRecovery drives dual-copy loss through the existing
// validated capture rebase path and reports only a promoted replacement.
type CaptureIntegrityAuditRecovery struct {
	source    IntegrityAuditSourceProbe
	frontiers IntegrityAuditFrontierObserver
}

// NewCaptureIntegrityAuditRecovery creates the production recovery adapter.
func NewCaptureIntegrityAuditRecovery(
	source IntegrityAuditSourceProbe,
	frontiers IntegrityAuditFrontierObserver,
) (*CaptureIntegrityAuditRecovery, error) {
	if source == nil || frontiers == nil {
		return nil, fmt.Errorf("backup integrity audit recovery: dependencies are required")
	}
	return &CaptureIntegrityAuditRecovery{
		source: source, frontiers: frontiers,
	}, nil
}

// SourceAvailable delegates the authoritative live-source decision.
func (r *CaptureIntegrityAuditRecovery) SourceAvailable(
	ctx context.Context,
	hashSlot uint16,
	generation string,
) (bool, error) {
	return r.source.SourceAvailable(ctx, hashSlot, generation)
}

// RequestRebase observes the target Slot Leader's durable replacement. Capture
// workers on the owning Slot Leader independently consume rebase_required, so
// the Controller Leader never assumes it is also the target Slot Leader.
func (r *CaptureIntegrityAuditRecovery) RequestRebase(
	ctx context.Context,
	hashSlot uint16,
	damagedGeneration string,
) (IntegrityAuditRebaseResult, error) {
	snapshot, err := r.frontiers.Load(ctx, hashSlot)
	if err != nil {
		return IntegrityAuditRebaseResult{}, err
	}
	if !snapshot.Found {
		return IntegrityAuditRebaseResult{}, nil
	}
	frontier := snapshot.Frontier
	if frontier.HashSlot != hashSlot {
		return IntegrityAuditRebaseResult{}, fmt.Errorf(
			"%w: integrity audit rebase returned a different Slot",
			ErrInvalidCapture,
		)
	}
	if exactIntegrityAuditPromotion(frontier, damagedGeneration) {
		return IntegrityAuditRebaseResult{
			Complete: true, Generation: frontier.Generation,
		}, nil
	}
	return IntegrityAuditRebaseResult{}, nil
}

func exactIntegrityAuditPromotion(
	frontier backupcontract.SlotFrontier,
	damagedGeneration string,
) bool {
	return frontier.Generation != "" &&
		frontier.Generation != damagedGeneration &&
		frontier.Rebase == nil &&
		frontier.LastPromotion != nil &&
		frontier.LastPromotion.PreviousGeneration == damagedGeneration &&
		frontier.LastPromotion.Reason == backupcontract.RebaseReasonAuditCorruption &&
		frontier.LastPromotion.PromotedAtUnixMillis ==
			frontier.GenerationStartedAtUnixMillis
}

// IntegrityAuditLeadership identifies the local node and Controller Leader.
type IntegrityAuditLeadership = CoordinatorLeadership

type integrityAuditFailureObserver interface {
	ObserveBackupFailure(string)
}

// RunIfLeader advances one bounded step only on the current Controller Leader.
func (a *IntegrityAuditor) RunIfLeader(
	ctx context.Context,
	leadership IntegrityAuditLeadership,
) (bool, error) {
	if a == nil || leadership == nil {
		return false, ErrInvalidCapture
	}
	if leadership.NodeID() != leadership.BackupControllerLeaderID() {
		return false, nil
	}
	_, err := a.RunStep(ctx)
	if err != nil {
		if observer, ok := a.observer.(integrityAuditFailureObserver); ok {
			observer.ObserveBackupFailure("audit")
		}
		return true, err
	}
	return true, nil
}

// Run executes the crash-resumable auditor in the background. Operational
// errors are retried on the next bounded interval; context cancellation stops it.
func (a *IntegrityAuditor) Run(
	ctx context.Context,
	leadership IntegrityAuditLeadership,
	interval time.Duration,
) error {
	if a == nil || leadership == nil || interval <= 0 {
		return ErrInvalidCapture
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		_, _ = a.RunIfLeader(ctx, leadership)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
