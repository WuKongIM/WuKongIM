package backup

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

var (
	// ErrRestoreModeRequired reports restore control outside explicit recovery mode.
	ErrRestoreModeRequired = errors.New("backup restore: explicit restore mode is required")
	// ErrRestorePlanExists reports an attempt to replace an immutable plan.
	ErrRestorePlanExists = errors.New("backup restore: plan already exists")
	// ErrRestoreTransition reports an invalid recovery state transition.
	ErrRestoreTransition = errors.New("backup restore: invalid state transition")
	// ErrOldClusterFenceRequired reports activation without explicit source fencing evidence.
	ErrOldClusterFenceRequired = errors.New("backup restore: old cluster fence evidence is required")
)

const (
	RestoreStatusPlanned    = backupcontract.RestoreStatusPlanned
	RestoreStatusInstalling = backupcontract.RestoreStatusInstalling
	RestoreStatusInstalled  = backupcontract.RestoreStatusInstalled
	RestoreStatusVerified   = backupcontract.RestoreStatusVerified
	RestoreStatusActivated  = backupcontract.RestoreStatusActivated
	RestoreStatusAbandoned  = backupcontract.RestoreStatusAbandoned
)

type RestoreStatus = backupcontract.RestoreStatus

// RestorePlanRequest selects one exact recovery point and immutable operator choices.
type RestorePlanRequest struct {
	RestorePointID   string
	LatestVerified   bool
	Repository       string
	InvalidateTokens bool
}

// RestoreInspection is trusted manifest and empty-target evidence returned before plan persistence.
type RestoreInspection struct {
	RestorePointID   string
	ManifestSHA256   string
	SourceClusterID  string
	SourceGeneration string
	TargetClusterID  string
	TargetGeneration string
	HashSlotCount    uint16
	// ErasureLedgerVersion identifies the authenticated permanent-erasure snapshot schema.
	ErasureLedgerVersion uint32
	// ErasureLedgerBoundary pins the exact contiguous commit prefix to replay.
	ErasureLedgerBoundary uint64
	// ErasureLedgerSHA256 authenticates the exact pinned ledger prefix.
	ErasureLedgerSHA256  string
	EstimatedPlainBytes  *uint64
	EstimatedCipherBytes *uint64
	TargetEmpty          bool
}

type RestorePartition = backupcontract.RestorePartition
type RestorePlan = backupcontract.RestorePlan

// RestoreState is the locally/Controller persisted recovery state.
type RestoreState struct {
	Revision uint64
	Plan     *RestorePlan
}

// RestorePlanStore persists one immutable plan and bounded progress with CAS.
type RestorePlanStore interface {
	Load(context.Context) (RestoreState, error)
	CompareAndSwap(context.Context, uint64, RestoreState) error
}

// RestoreInspector verifies repository identity, compatibility, and target emptiness.
type RestoreInspector interface {
	Inspect(context.Context, RestorePlanRequest) (RestoreInspection, error)
}

// RestoreFinalVerifier performs post-install semantic verification.
type RestoreFinalVerifier interface {
	VerifyRestore(context.Context, RestorePlan) ([]RestorePartition, error)
}

// RestoreOptions configures the entry-independent explicit recovery state machine.
type RestoreOptions struct {
	Enabled   bool
	Store     RestorePlanStore
	Inspector RestoreInspector
	Verifier  RestoreFinalVerifier
	Now       func() time.Time
	NewPlanID func() string
}

// RestoreApp owns explicit recovery lifecycle transitions.
type RestoreApp struct {
	enabled   bool
	store     RestorePlanStore
	inspector RestoreInspector
	verifier  RestoreFinalVerifier
	now       func() time.Time
	newPlanID func() string
}

// NewRestoreApp creates an explicit recovery state machine.
func NewRestoreApp(options RestoreOptions) (*RestoreApp, error) {
	if options.Store == nil || options.Inspector == nil || options.Verifier == nil || options.Now == nil || options.NewPlanID == nil {
		return nil, fmt.Errorf("%w: restore dependencies are incomplete", ErrInvalidRequest)
	}
	return &RestoreApp{enabled: options.Enabled, store: options.Store, inspector: options.Inspector, verifier: options.Verifier, now: options.Now, newPlanID: options.NewPlanID}, nil
}

// Plan verifies prerequisites and records the only immutable recovery plan.
func (a *RestoreApp) Plan(ctx context.Context, request RestorePlanRequest) (RestorePlan, error) {
	if !a.enabled {
		return RestorePlan{}, ErrRestoreModeRequired
	}
	if (strings.TrimSpace(request.RestorePointID) == "") == !request.LatestVerified || (request.Repository != "primary" && request.Repository != "secondary") {
		return RestorePlan{}, ErrInvalidRequest
	}
	inspection, err := a.inspector.Inspect(ctx, request)
	if err != nil {
		return RestorePlan{}, err
	}
	if !inspection.TargetEmpty || inspection.RestorePointID == "" || !validRestoreDigest(inspection.ManifestSHA256) || inspection.HashSlotCount == 0 || inspection.TargetClusterID == inspection.SourceClusterID || inspection.TargetGeneration == inspection.SourceGeneration ||
		inspection.ErasureLedgerVersion != backupartifact.ErasureLedgerSnapshotVersion || !validRestoreDigest(inspection.ErasureLedgerSHA256) {
		return RestorePlan{}, fmt.Errorf("%w: restore inspection is unsafe", ErrInvalidRequest)
	}
	now := a.now().UTC().UnixMilli()
	plan := RestorePlan{
		ID: strings.TrimSpace(a.newPlanID()), RestorePointID: inspection.RestorePointID,
		ManifestSHA256: inspection.ManifestSHA256, Repository: request.Repository,
		SourceClusterID: inspection.SourceClusterID, SourceGeneration: inspection.SourceGeneration,
		TargetClusterID: inspection.TargetClusterID, TargetGeneration: inspection.TargetGeneration,
		HashSlotCount: inspection.HashSlotCount, InvalidateTokens: request.InvalidateTokens,
		ErasureLedgerVersion: inspection.ErasureLedgerVersion, ErasureLedgerBoundary: inspection.ErasureLedgerBoundary, ErasureLedgerSHA256: inspection.ErasureLedgerSHA256,
		EstimatedPlainBytes: inspection.EstimatedPlainBytes, EstimatedCipherBytes: inspection.EstimatedCipherBytes,
		Status: RestoreStatusPlanned, CreatedAtUnixMillis: now, UpdatedAtUnixMillis: now,
		Partitions: make([]RestorePartition, inspection.HashSlotCount),
	}
	if plan.ID == "" {
		return RestorePlan{}, ErrInvalidRequest
	}
	for hashSlot := range plan.Partitions {
		plan.Partitions[hashSlot].HashSlot = uint16(hashSlot)
	}
	err = a.mutateRestore(ctx, func(state *RestoreState) error {
		if state.Plan != nil {
			return ErrRestorePlanExists
		}
		state.Plan = cloneRestorePlan(&plan)
		return nil
	})
	return plan, err
}

// Start marks a planned restore ready for idempotent partition installation.
func (a *RestoreApp) Start(ctx context.Context, planID string) (RestorePlan, error) {
	return a.transition(ctx, planID, func(plan *RestorePlan) error {
		switch plan.Status {
		case RestoreStatusPlanned:
			plan.Status = RestoreStatusInstalling
			return nil
		case RestoreStatusInstalling:
			return nil
		default:
			return ErrRestoreTransition
		}
	})
}

// ReportPartition records one exact partition installation result.
func (a *RestoreApp) ReportPartition(ctx context.Context, planID string, report RestorePartition) (RestorePlan, error) {
	return a.transition(ctx, planID, func(plan *RestorePlan) error {
		if plan.Status != RestoreStatusInstalling || report.HashSlot >= plan.HashSlotCount || !validRestorePartitionEvidence(report) || !report.Installed || report.FailureCategory != "" || !validRestoreDigest(report.MetadataSHA256) {
			return ErrRestoreTransition
		}
		existing := plan.Partitions[report.HashSlot]
		if existing.Installed {
			if sameRestorePartitionResult(existing, report) {
				return nil
			}
			return ErrStateConflict
		}
		report.UpdatedAtUnixMillis = a.now().UTC().UnixMilli()
		plan.Partitions[report.HashSlot] = report
		complete := true
		for _, partition := range plan.Partitions {
			if !partition.Installed {
				complete = false
				break
			}
		}
		if complete {
			plan.Status = RestoreStatusInstalled
		}
		return nil
	})
}

// Verify runs semantic validation only after all logical partitions install.
func (a *RestoreApp) Verify(ctx context.Context, planID string) (RestorePlan, error) {
	state, err := a.store.Load(ctx)
	if err != nil {
		return RestorePlan{}, err
	}
	if state.Plan == nil || state.Plan.ID != planID || state.Plan.Status != RestoreStatusInstalled {
		if state.Plan != nil && state.Plan.ID == planID && state.Plan.Status == RestoreStatusVerified {
			return *cloneRestorePlan(state.Plan), nil
		}
		return RestorePlan{}, ErrRestoreTransition
	}
	verified, err := a.verifier.VerifyRestore(ctx, *cloneRestorePlan(state.Plan))
	if err != nil {
		return RestorePlan{}, err
	}
	return a.transition(ctx, planID, func(plan *RestorePlan) error {
		if plan.Status != RestoreStatusInstalled || len(verified) != int(plan.HashSlotCount) {
			return ErrRestoreTransition
		}
		for index, partition := range verified {
			if partition.HashSlot != uint16(index) || !validRestorePartitionEvidence(partition) || !partition.Installed || !partition.Verified ||
				!sameRestorePartitionInstallEvidence(plan.Partitions[index], partition) {
				return ErrRestoreTransition
			}
		}
		plan.Partitions = append([]RestorePartition(nil), verified...)
		plan.Status = RestoreStatusVerified
		plan.VerifiedAtUnixMillis = a.now().UTC().UnixMilli()
		return nil
	})
}

// Activate records explicit old-cluster fencing evidence and opens the restored generation.
func (a *RestoreApp) Activate(ctx context.Context, planID, fenceDigest string) (RestorePlan, error) {
	fenceDigest = strings.TrimSpace(fenceDigest)
	if !validRestoreDigest(fenceDigest) {
		return RestorePlan{}, ErrOldClusterFenceRequired
	}
	state, err := a.store.Load(ctx)
	if err != nil {
		return RestorePlan{}, err
	}
	if state.Plan == nil || state.Plan.ID != planID {
		return RestorePlan{}, ErrRestorePointNotFound
	}
	if state.Plan.Status == RestoreStatusActivated && state.Plan.ActivationFenceDigest == fenceDigest {
		return *cloneRestorePlan(state.Plan), nil
	}
	if state.Plan.Status != RestoreStatusVerified {
		return RestorePlan{}, ErrRestoreTransition
	}
	verified, err := a.verifier.VerifyRestore(ctx, *cloneRestorePlan(state.Plan))
	if err != nil {
		return RestorePlan{}, err
	}
	if len(verified) != int(state.Plan.HashSlotCount) {
		return RestorePlan{}, ErrRestoreTransition
	}
	for index, partition := range verified {
		if partition.HashSlot != uint16(index) || !validRestorePartitionEvidence(partition) || !partition.Installed || !partition.Verified ||
			!sameRestorePartitionInstallEvidence(state.Plan.Partitions[index], partition) {
			return RestorePlan{}, ErrRestoreTransition
		}
	}
	return a.transition(ctx, planID, func(plan *RestorePlan) error {
		if plan.Status != RestoreStatusVerified {
			return ErrRestoreTransition
		}
		plan.Status = RestoreStatusActivated
		plan.ActivationFenceDigest = fenceDigest
		plan.ActivatedAtUnixMillis = a.now().UTC().UnixMilli()
		return nil
	})
}

func sameRestorePartitionResult(left, right RestorePartition) bool {
	return left.Verified == right.Verified && sameRestorePartitionInstallEvidence(left, right)
}

func sameRestorePartitionInstallEvidence(left, right RestorePartition) bool {
	return left.HashSlot == right.HashSlot && left.EvidenceVersion == right.EvidenceVersion && left.Installed == right.Installed &&
		left.PlainBytes == right.PlainBytes && left.MetadataRecordCount == right.MetadataRecordCount && left.MessageCount == right.MessageCount &&
		left.MaxMessageID == right.MaxMessageID && left.MetadataSHA256 == right.MetadataSHA256 && left.FailureCategory == right.FailureCategory
}

func validRestorePartitionEvidence(partition RestorePartition) bool {
	return partition.EvidenceVersion == backupartifact.PartitionEvidenceVersion &&
		(partition.MessageCount == 0) == (partition.MaxMessageID == 0)
}

func validRestoreDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

// Status returns a detached recovery plan preserving absence as absence.
func (a *RestoreApp) Status(ctx context.Context) (*RestorePlan, error) {
	if !a.enabled {
		return nil, ErrRestoreModeRequired
	}
	state, err := a.store.Load(ctx)
	if err != nil {
		return nil, err
	}
	return cloneRestorePlan(state.Plan), nil
}

func (a *RestoreApp) transition(ctx context.Context, planID string, mutate func(*RestorePlan) error) (RestorePlan, error) {
	if !a.enabled {
		return RestorePlan{}, ErrRestoreModeRequired
	}
	var result RestorePlan
	err := a.mutateRestore(ctx, func(state *RestoreState) error {
		if state.Plan == nil || state.Plan.ID != planID {
			return ErrRestorePointNotFound
		}
		if err := mutate(state.Plan); err != nil {
			return err
		}
		state.Plan.UpdatedAtUnixMillis = a.now().UTC().UnixMilli()
		result = *cloneRestorePlan(state.Plan)
		return nil
	})
	return result, err
}

func (a *RestoreApp) mutateRestore(ctx context.Context, mutate func(*RestoreState) error) error {
	for attempt := 0; attempt < maxStateRetries; attempt++ {
		state, err := a.store.Load(ctx)
		if err != nil {
			return err
		}
		next := cloneRestoreState(state)
		if err := mutate(&next); err != nil {
			return err
		}
		if err := a.store.CompareAndSwap(ctx, state.Revision, next); err != nil {
			if errors.Is(err, ErrStateConflict) {
				continue
			}
			return err
		}
		return nil
	}
	return ErrStateConflict
}

func cloneRestoreState(state RestoreState) RestoreState {
	state.Plan = cloneRestorePlan(state.Plan)
	return state
}

func cloneRestorePlan(plan *RestorePlan) *RestorePlan {
	if plan == nil {
		return nil
	}
	copy := *plan
	if plan.EstimatedPlainBytes != nil {
		value := *plan.EstimatedPlainBytes
		copy.EstimatedPlainBytes = &value
	}
	if plan.EstimatedCipherBytes != nil {
		value := *plan.EstimatedCipherBytes
		copy.EstimatedCipherBytes = &value
	}
	copy.Partitions = append([]RestorePartition(nil), plan.Partitions...)
	return &copy
}
