package backup

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
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
	// ErrActivationEvidenceRequired reports activation without a verified source receipt or audited break-glass.
	ErrActivationEvidenceRequired = errors.New("backup restore: activation evidence is required")
)

const (
	RestoreStatusPlanned       = backupcontract.RestoreStatusPlanned
	RestoreStatusInstalling    = backupcontract.RestoreStatusInstalling
	RestoreStatusInstalled     = backupcontract.RestoreStatusInstalled
	RestoreStatusVerified      = backupcontract.RestoreStatusVerified
	RestoreStatusActivating    = backupcontract.RestoreStatusActivating
	RestoreStatusActivated     = backupcontract.RestoreStatusActivated
	RestoreStatusAbandoned     = backupcontract.RestoreStatusAbandoned
	RestorePartitionPending    = backupcontract.RestorePartitionPending
	RestorePartitionInstalling = backupcontract.RestorePartitionInstalling
	RestorePartitionInstalled  = backupcontract.RestorePartitionInstalled
	RestorePartitionConverging = backupcontract.RestorePartitionConverging
	RestorePartitionConverged  = backupcontract.RestorePartitionConverged
	RestorePartitionFailed     = backupcontract.RestorePartitionFailed
)

type RestoreStatus = backupcontract.RestoreStatus
type RestorePartitionStatus = backupcontract.RestorePartitionStatus

// RestorePlanRequest selects one exact checkpoint and immutable operator choices.
type RestorePlanRequest struct {
	CheckpointID     string
	InvalidateTokens bool
	// CatalogHeadToken is the opaque discovery fence exported by the source.
	CatalogHeadToken string
}

// RestoreInspectRequest is the internal authenticated repository selection
// passed from the restore usecase to infrastructure.
type RestoreInspectRequest struct {
	CheckpointID string
	Repository   string
	CatalogHead  backupartifact.CatalogPageReference
}

// RestoreInspection is trusted checkpoint and empty-target evidence returned before plan persistence.
type RestoreInspection struct {
	// CheckpointID and CheckpointSHA256 bind the selected immutable checkpoint.
	CheckpointID     string
	CheckpointSHA256 string
	// CatalogProof proves original checkpoint publication under one signed head.
	CatalogProof *backupartifact.CheckpointCatalogProof
	// CheckpointVersion and timestamps are authenticated checkpoint identity.
	CheckpointVersion               uint16
	CheckpointCreatedAtUnixMillis   int64
	CheckpointEffectiveAtUnixMillis int64
	SourceClusterID                 string
	SourceGeneration                string
	TargetClusterID                 string
	TargetGeneration                string
	HashSlotCount                   uint16
	// ErasureLedgerVersion identifies the authenticated permanent-erasure snapshot schema.
	ErasureLedgerVersion uint32
	// ErasureEventCount is the total number of events selected by ErasureHeads.
	ErasureEventCount uint64
	// ErasureHeads authenticate the exact selected prefix of each Hash Slot stream.
	ErasureHeads []backupartifact.ErasureStreamHead
	// ErasureLedgerSHA256 authenticates the exact pinned ledger prefix.
	ErasureLedgerSHA256  string
	EstimatedPlainBytes  *uint64
	EstimatedCipherBytes *uint64
	TargetEmpty          bool
}

type RestorePartition = backupcontract.RestorePartition
type RestorePlan = backupcontract.RestorePlan

// RestoreProgress is a bounded public projection of per-Slot install,
// verification, convergence, throughput, and ETA.
type RestoreProgress struct {
	// PlanID and Status identify the active durable restore lifecycle.
	PlanID string
	Status RestoreStatus
	// TotalSlots and phase counts are a bounded aggregate over Partitions.
	TotalSlots      uint16
	PendingSlots    uint16
	InstallingSlots uint16
	InstalledSlots  uint16
	ConvergedSlots  uint16
	FailedSlots     uint16
	// DownloadedBytes and ReplicatedBytes are monotonic aggregate work counters.
	DownloadedBytes uint64
	ReplicatedBytes uint64
	// ThroughputBytesPerSecond is observed authenticated download throughput.
	ThroughputBytesPerSecond uint64
	// ETASeconds is nil until at least one Slot converges.
	ETASeconds *uint64
	// Partitions contains one detached progress record per Hash Slot.
	Partitions []RestorePartition
}

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

// RestoreInspector verifies the exact catalog checkpoint and target emptiness.
type RestoreInspector interface {
	Inspect(context.Context, RestoreInspectRequest) (RestoreInspection, error)
}

// RestoreFinalVerifier performs post-install semantic verification.
type RestoreFinalVerifier interface {
	VerifyRestore(context.Context, RestorePlan) ([]RestorePartition, error)
}

// RestoreActivationCleaner removes plan-bound plaintext staging from every
// target replica after activation evidence is durable but before service opens.
type RestoreActivationCleaner interface {
	CleanupRestoreStaging(context.Context, RestorePlan) error
}

// RestoreActivationRequest selects exactly one reviewed activation evidence path.
type RestoreActivationRequest struct {
	// SourceFenceReceipt is the normal signed source Controller proof.
	SourceFenceReceipt *backupartifact.SourceFenceReceipt
	// BreakGlassReason is the explicit exceptional justification.
	BreakGlassReason string
	// Operator is the authenticated Manager principal authorizing activation.
	Operator string
}

// RestoreOptions configures the entry-independent explicit recovery state machine.
type RestoreOptions struct {
	Enabled   bool
	Store     RestorePlanStore
	Inspector RestoreInspector
	Verifier  RestoreFinalVerifier
	Cleaner   RestoreActivationCleaner
	// ActivationVerifier authenticates source fence receipts against trusted signing keys.
	ActivationVerifier backupartifact.ManifestSigner
	Now                func() time.Time
	NewPlanID          func() string
	// NewAuditID allocates immutable break-glass audit identities.
	NewAuditID func() string
}

// RestoreApp owns explicit recovery lifecycle transitions.
type RestoreApp struct {
	enabled            bool
	store              RestorePlanStore
	inspector          RestoreInspector
	verifier           RestoreFinalVerifier
	cleaner            RestoreActivationCleaner
	activationVerifier backupartifact.ManifestSigner
	now                func() time.Time
	newPlanID          func() string
	newAuditID         func() string
}

// NewRestoreApp creates an explicit recovery state machine.
func NewRestoreApp(options RestoreOptions) (*RestoreApp, error) {
	if options.Store == nil || options.Inspector == nil ||
		options.Verifier == nil || options.Cleaner == nil ||
		options.Now == nil || options.NewPlanID == nil {
		return nil, fmt.Errorf("%w: restore dependencies are incomplete", ErrInvalidRequest)
	}
	newAuditID := options.NewAuditID
	if newAuditID == nil {
		newAuditID = options.NewPlanID
	}
	return &RestoreApp{
		enabled: options.Enabled, store: options.Store,
		inspector: options.Inspector, verifier: options.Verifier,
		cleaner:            options.Cleaner,
		activationVerifier: options.ActivationVerifier,
		now:                options.Now, newPlanID: options.NewPlanID,
		newAuditID: newAuditID,
	}, nil
}

// Plan verifies prerequisites and records the only immutable recovery plan.
func (a *RestoreApp) Plan(ctx context.Context, request RestorePlanRequest) (RestorePlan, error) {
	if !a.enabled {
		return RestorePlan{}, ErrRestoreModeRequired
	}
	if strings.TrimSpace(request.CheckpointID) == "" {
		return RestorePlan{}, ErrInvalidRequest
	}
	catalogHead, err := DecodeCatalogHeadToken(request.CatalogHeadToken)
	if err != nil {
		return RestorePlan{}, err
	}
	const repository = "primary"
	inspection, err := a.inspector.Inspect(ctx, RestoreInspectRequest{
		CheckpointID: strings.TrimSpace(request.CheckpointID),
		Repository:   repository, CatalogHead: catalogHead,
	})
	if err != nil {
		return RestorePlan{}, err
	}
	if !inspection.TargetEmpty || inspection.CheckpointID == "" || !validRestoreDigest(inspection.CheckpointSHA256) || inspection.HashSlotCount == 0 || inspection.TargetClusterID == inspection.SourceClusterID || inspection.TargetGeneration == inspection.SourceGeneration ||
		inspection.ErasureLedgerVersion != backupartifact.ErasureLedgerSnapshotVersion || !validRestoreDigest(inspection.ErasureLedgerSHA256) ||
		!validRestoreErasureHeads(inspection.ErasureHeads, inspection.HashSlotCount, inspection.ErasureEventCount) ||
		inspection.CatalogProof == nil {
		return RestorePlan{}, fmt.Errorf("%w: restore inspection is unsafe", ErrInvalidRequest)
	}
	proof := *inspection.CatalogProof
	if backupartifact.ValidateCheckpointCatalogProof(proof) != nil ||
		proof.Checkpoint.ID != inspection.CheckpointID ||
		proof.Checkpoint.SHA256 != inspection.CheckpointSHA256 ||
		proof.Checkpoint.GenerationVector.HashSlotCount !=
			inspection.HashSlotCount ||
		inspection.CheckpointVersion != backupartifact.CheckpointVersion ||
		inspection.CheckpointCreatedAtUnixMillis != proof.Checkpoint.CreatedAtUnixMillis ||
		inspection.CheckpointEffectiveAtUnixMillis != proof.Checkpoint.EffectiveAtUnixMillis {
		return RestorePlan{}, fmt.Errorf("%w: restore checkpoint proof is unsafe", ErrInvalidRequest)
	}
	now := a.now().UTC().UnixMilli()
	plan := RestorePlan{
		ID: strings.TrimSpace(a.newPlanID()), CheckpointID: inspection.CheckpointID,
		CheckpointSHA256: inspection.CheckpointSHA256, Repository: repository,
		CatalogProof:                    &proof,
		CheckpointVersion:               inspection.CheckpointVersion,
		CheckpointCreatedAtUnixMillis:   inspection.CheckpointCreatedAtUnixMillis,
		CheckpointEffectiveAtUnixMillis: inspection.CheckpointEffectiveAtUnixMillis,
		SourceClusterID:                 inspection.SourceClusterID, SourceGeneration: inspection.SourceGeneration,
		TargetClusterID: inspection.TargetClusterID, TargetGeneration: inspection.TargetGeneration,
		HashSlotCount: inspection.HashSlotCount, InvalidateTokens: request.InvalidateTokens,
		ErasureLedgerVersion: inspection.ErasureLedgerVersion, ErasureEventCount: inspection.ErasureEventCount,
		ErasureHeads: append([]backupartifact.ErasureStreamHead(nil), inspection.ErasureHeads...), ErasureLedgerSHA256: inspection.ErasureLedgerSHA256,
		EstimatedPlainBytes: inspection.EstimatedPlainBytes, EstimatedCipherBytes: inspection.EstimatedCipherBytes,
		Status: RestoreStatusPlanned, CreatedAtUnixMillis: now, UpdatedAtUnixMillis: now,
		Partitions: make([]RestorePartition, inspection.HashSlotCount),
	}
	if plan.ID == "" {
		return RestorePlan{}, ErrInvalidRequest
	}
	for hashSlot := range plan.Partitions {
		plan.Partitions[hashSlot].HashSlot = uint16(hashSlot)
		plan.Partitions[hashSlot].Status = backupcontract.RestorePartitionPending
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

// RestorePartitionAssignment fences one attempt to the current target Slot Leader.
type RestorePartitionAssignment = backupcontract.RestorePartitionAssignment

// BeginPartitionInstall durably records the exact Leader attempt before any
// repository or key-authority work begins.
func (a *RestoreApp) BeginPartitionInstall(
	ctx context.Context,
	planID string,
	assignment RestorePartitionAssignment,
) (RestorePlan, error) {
	return a.transition(ctx, planID, func(plan *RestorePlan) error {
		if plan.Status != RestoreStatusInstalling || plan.CatalogProof == nil ||
			assignment.HashSlot >= plan.HashSlotCount || assignment.TargetSlotID == 0 ||
			assignment.LeaderNodeID == 0 || assignment.LeaderTerm == 0 ||
			assignment.ConfigEpoch == 0 || assignment.ReplicaCount == 0 {
			return ErrRestoreTransition
		}
		partition := &plan.Partitions[assignment.HashSlot]
		if partition.Status == backupcontract.RestorePartitionConverged {
			return nil
		}
		if (partition.Status == backupcontract.RestorePartitionInstalling ||
			partition.Status == backupcontract.RestorePartitionInstalled ||
			partition.Status == backupcontract.RestorePartitionConverging) &&
			partition.TargetSlotID == assignment.TargetSlotID &&
			partition.LeaderNodeID == assignment.LeaderNodeID &&
			partition.LeaderTerm == assignment.LeaderTerm &&
			partition.ConfigEpoch == assignment.ConfigEpoch {
			return nil
		}
		if partition.InstallAttempt == ^uint64(0) {
			return ErrRestoreTransition
		}
		now := a.now().UTC().UnixMilli()
		startedAt := partition.StartedAtUnixMillis
		if startedAt <= 0 {
			startedAt = now
		}
		*partition = RestorePartition{
			HashSlot: assignment.HashSlot, Status: backupcontract.RestorePartitionInstalling,
			TargetSlotID: assignment.TargetSlotID, LeaderNodeID: assignment.LeaderNodeID,
			LeaderTerm: assignment.LeaderTerm, ConfigEpoch: assignment.ConfigEpoch,
			InstallAttempt: partition.InstallAttempt + 1, ReplicaCount: assignment.ReplicaCount,
			StartedAtUnixMillis: startedAt, UpdatedAtUnixMillis: now,
		}
		return nil
	})
}

// ReportPartitionProgress records one fenced install/convergence result.
func (a *RestoreApp) ReportPartitionProgress(
	ctx context.Context,
	planID string,
	report RestorePartition,
) (RestorePlan, error) {
	return a.transition(ctx, planID, func(plan *RestorePlan) error {
		if plan.CatalogProof == nil || report.HashSlot >= plan.HashSlotCount {
			return ErrRestoreTransition
		}
		existing := plan.Partitions[report.HashSlot]
		if existing.Status == backupcontract.RestorePartitionConverged &&
			sameRestorePartitionResult(existing, report) {
			return nil
		}
		if plan.Status != RestoreStatusInstalling {
			return ErrRestoreTransition
		}
		if existing.Status != backupcontract.RestorePartitionInstalling &&
			existing.Status != backupcontract.RestorePartitionInstalled &&
			existing.Status != backupcontract.RestorePartitionConverging {
			return ErrRestoreTransition
		}
		if report.TargetSlotID != existing.TargetSlotID ||
			report.LeaderNodeID != existing.LeaderNodeID ||
			report.LeaderTerm != existing.LeaderTerm ||
			report.ConfigEpoch != existing.ConfigEpoch ||
			report.InstallAttempt != existing.InstallAttempt ||
			report.ReplicaCount != existing.ReplicaCount ||
			report.StartedAtUnixMillis != existing.StartedAtUnixMillis {
			return ErrStateConflict
		}
		if report.DownloadedBytes < existing.DownloadedBytes ||
			report.ReplicatedBytes < existing.ReplicatedBytes ||
			report.ConvergedReplicas < existing.ConvergedReplicas ||
			restorePartitionPhaseRank(report.Status) <
				restorePartitionPhaseRank(existing.Status) {
			return ErrStateConflict
		}
		if existing.Installed &&
			(!report.Installed ||
				report.InstalledAtUnixMillis != existing.InstalledAtUnixMillis ||
				!sameRestorePartitionInstallEvidence(existing, report)) {
			return ErrStateConflict
		}
		switch report.Status {
		case backupcontract.RestorePartitionInstalling:
			if report.Installed || report.Verified ||
				report.EvidenceVersion != 0 ||
				hasRestoreInstallEvidence(report) ||
				report.InstalledAtUnixMillis != 0 ||
				report.ConvergedReplicas != 0 ||
				report.ReplicatedBytes != 0 ||
				report.FailureCategory != "" {
				return ErrRestoreTransition
			}
		case backupcontract.RestorePartitionFailed:
			if existing.Installed ||
				existing.Status != backupcontract.RestorePartitionInstalling ||
				report.FailureCategory == "" ||
				len(report.FailureCategory) > 128 ||
				report.Verified || report.EvidenceVersion != 0 ||
				hasRestoreInstallEvidence(report) ||
				report.InstalledAtUnixMillis != 0 ||
				report.ConvergedReplicas != 0 ||
				report.ReplicatedBytes != 0 {
				return ErrRestoreTransition
			}
			report.Installed = false
		case backupcontract.RestorePartitionInstalled,
			backupcontract.RestorePartitionConverging,
			backupcontract.RestorePartitionConverged:
			if !validCheckpointRestorePartition(report) {
				return ErrRestoreTransition
			}
		default:
			return ErrRestoreTransition
		}
		report.UpdatedAtUnixMillis = a.now().UTC().UnixMilli()
		plan.Partitions[report.HashSlot] = report
		complete := true
		for _, partition := range plan.Partitions {
			if partition.Status != backupcontract.RestorePartitionConverged {
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

func hasRestoreInstallEvidence(partition RestorePartition) bool {
	return partition.PlainBytes != 0 ||
		partition.MetadataRecordCount != 0 ||
		partition.MessageCount != 0 ||
		partition.MaxMessageID != 0 ||
		partition.MetadataSHA256 != "" ||
		partition.ContentSHA256 != "" ||
		partition.MessageMerkleSHA256 != "" ||
		partition.ChannelBoundaryCount != 0
}

func restorePartitionPhaseRank(
	status backupcontract.RestorePartitionStatus,
) uint8 {
	switch status {
	case backupcontract.RestorePartitionPending:
		return 0
	case backupcontract.RestorePartitionInstalling:
		return 1
	case backupcontract.RestorePartitionInstalled:
		return 2
	case backupcontract.RestorePartitionConverging:
		return 3
	case backupcontract.RestorePartitionConverged:
		return 4
	case backupcontract.RestorePartitionFailed:
		return 1
	default:
		return 0
	}
}

func validCheckpointRestorePartition(partition RestorePartition) bool {
	return partition.Installed && partition.FailureCategory == "" &&
		partition.EvidenceVersion == backupartifact.RestoreEvidenceVersion &&
		validRestoreDigest(partition.ContentSHA256) &&
		validRestoreDigest(partition.MessageMerkleSHA256) &&
		(partition.MessageCount == 0) == (partition.MaxMessageID == 0) &&
		partition.ReplicaCount > 0 &&
		partition.ConvergedReplicas <= partition.ReplicaCount &&
		partition.InstalledAtUnixMillis >= partition.StartedAtUnixMillis &&
		validRestoreReplicaPhase(
			partition.Status,
			partition.ReplicaCount,
			partition.ConvergedReplicas,
		)
}

func validRestoreReplicaPhase(
	status backupcontract.RestorePartitionStatus,
	replicaCount uint32,
	convergedReplicas uint32,
) bool {
	switch status {
	case backupcontract.RestorePartitionInstalled:
		return replicaCount > 1 && convergedReplicas == 1
	case backupcontract.RestorePartitionConverging:
		return convergedReplicas > 1 && convergedReplicas < replicaCount
	case backupcontract.RestorePartitionConverged:
		return convergedReplicas == replicaCount
	default:
		return false
	}
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

// Activate records verified normal or explicitly audited exceptional evidence,
// removes plan-bound plaintext staging, then opens the restored generation.
func (a *RestoreApp) Activate(
	ctx context.Context,
	planID string,
	request RestoreActivationRequest,
) (RestorePlan, error) {
	request.Operator = strings.TrimSpace(request.Operator)
	request.BreakGlassReason = strings.TrimSpace(request.BreakGlassReason)
	if request.Operator == "" ||
		(request.SourceFenceReceipt == nil) ==
			(request.BreakGlassReason == "") {
		return RestorePlan{}, ErrActivationEvidenceRequired
	}
	state, err := a.store.Load(ctx)
	if err != nil {
		return RestorePlan{}, err
	}
	if state.Plan == nil || state.Plan.ID != planID {
		return RestorePlan{}, ErrRestorePlanNotFound
	}
	if state.Plan.Status == RestoreStatusActivated {
		if sameRestoreActivationRequest(state.Plan.Activation, request) {
			return *cloneRestorePlan(state.Plan), nil
		}
		return RestorePlan{}, ErrRestoreTransition
	}
	if state.Plan.Status == RestoreStatusActivating {
		if !sameRestoreActivationRequest(state.Plan.Activation, request) {
			return RestorePlan{}, ErrRestoreTransition
		}
		return a.completeRestoreActivation(ctx, *cloneRestorePlan(state.Plan))
	}
	if state.Plan.Status != RestoreStatusVerified {
		return RestorePlan{}, ErrRestoreTransition
	}
	authorizedAt := a.now().UTC().UnixMilli()
	activation, err := a.buildRestoreActivationEvidence(
		ctx, *state.Plan, request, authorizedAt,
	)
	if err != nil {
		return RestorePlan{}, err
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
			partition.Status != RestorePartitionConverged ||
			partition.ReplicaCount == 0 ||
			partition.ConvergedReplicas != partition.ReplicaCount ||
			!sameRestorePartitionInstallEvidence(state.Plan.Partitions[index], partition) {
			return RestorePlan{}, ErrRestoreTransition
		}
	}
	activating, err := a.transition(ctx, planID, func(plan *RestorePlan) error {
		if plan.Status != RestoreStatusVerified {
			return ErrRestoreTransition
		}
		plan.Status = RestoreStatusActivating
		plan.Activation = backupartifact.CloneRestoreActivationEvidence(&activation)
		return nil
	})
	if err != nil {
		latest, loadErr := a.store.Load(ctx)
		if loadErr != nil || latest.Plan == nil ||
			latest.Plan.ID != planID ||
			latest.Plan.Status != RestoreStatusActivating ||
			!sameRestoreActivationRequest(latest.Plan.Activation, request) {
			return RestorePlan{}, err
		}
		activating = *cloneRestorePlan(latest.Plan)
	}
	return a.completeRestoreActivation(ctx, activating)
}

func (a *RestoreApp) completeRestoreActivation(
	ctx context.Context,
	activating RestorePlan,
) (RestorePlan, error) {
	if activating.Status != RestoreStatusActivating ||
		activating.Activation == nil {
		return RestorePlan{}, ErrRestoreTransition
	}
	if err := a.cleaner.CleanupRestoreStaging(
		ctx, *cloneRestorePlan(&activating),
	); err != nil {
		return activating, fmt.Errorf(
			"backup restore: activation staging cleanup: %w", err,
		)
	}
	completedAt := a.now().UTC().UnixMilli()
	activated, err := a.transition(
		ctx, activating.ID, func(plan *RestorePlan) error {
			if plan.Status != RestoreStatusActivating ||
				plan.Activation == nil ||
				plan.Activation.EvidenceSHA256 !=
					activating.Activation.EvidenceSHA256 {
				return ErrRestoreTransition
			}
			plan.Status = RestoreStatusActivated
			plan.StagingCleanupCompletedAtUnixMillis = completedAt
			plan.ActivatedAtUnixMillis = completedAt
			return nil
		},
	)
	if err == nil {
		return activated, nil
	}
	latest, loadErr := a.store.Load(ctx)
	if loadErr == nil && latest.Plan != nil &&
		latest.Plan.ID == activating.ID &&
		latest.Plan.Status == RestoreStatusActivated &&
		latest.Plan.Activation != nil &&
		latest.Plan.Activation.EvidenceSHA256 ==
			activating.Activation.EvidenceSHA256 {
		return *cloneRestorePlan(latest.Plan), nil
	}
	return RestorePlan{}, err
}

func (a *RestoreApp) buildRestoreActivationEvidence(
	ctx context.Context,
	plan RestorePlan,
	request RestoreActivationRequest,
	activatedAt int64,
) (backupartifact.RestoreActivationEvidence, error) {
	if request.SourceFenceReceipt != nil {
		if a.activationVerifier == nil {
			return backupartifact.RestoreActivationEvidence{},
				ErrActivationEvidenceRequired
		}
		receipt := *request.SourceFenceReceipt
		if err := backupartifact.VerifySourceFenceReceipt(
			ctx, receipt, a.activationVerifier,
		); err != nil {
			return backupartifact.RestoreActivationEvidence{},
				fmt.Errorf("%w: %v", ErrActivationEvidenceRequired, err)
		}
		if receipt.RestorePlanID != plan.ID ||
			receipt.CheckpointID != plan.CheckpointID ||
			receipt.CheckpointSHA256 != plan.CheckpointSHA256 ||
			receipt.SourceClusterID != plan.SourceClusterID ||
			receipt.SourceGeneration != plan.SourceGeneration ||
			receipt.TargetClusterID != plan.TargetClusterID ||
			receipt.TargetGeneration != plan.TargetGeneration {
			return backupartifact.RestoreActivationEvidence{},
				ErrActivationEvidenceRequired
		}
		digest, err := backupartifact.SourceFenceReceiptDigest(receipt)
		if err != nil {
			return backupartifact.RestoreActivationEvidence{}, err
		}
		evidence := backupartifact.RestoreActivationEvidence{
			Kind:                 backupartifact.RestoreActivationSourceFence,
			EvidenceSHA256:       digest,
			Operator:             request.Operator,
			RecordedAtUnixMillis: activatedAt,
			SourceFenceReceipt:   &receipt,
		}
		if err := backupartifact.ValidateRestoreActivationEvidence(evidence); err != nil {
			return backupartifact.RestoreActivationEvidence{}, err
		}
		return evidence, nil
	}
	audit := backupartifact.BreakGlassActivationAudit{
		ID:                     strings.TrimSpace(a.newAuditID()),
		RestorePlanID:          plan.ID,
		Operator:               request.Operator,
		Reason:                 request.BreakGlassReason,
		AuthorizedAtUnixMillis: activatedAt,
	}
	digest, err := backupartifact.BreakGlassActivationDigest(audit)
	if err != nil {
		return backupartifact.RestoreActivationEvidence{},
			fmt.Errorf("%w: %v", ErrActivationEvidenceRequired, err)
	}
	evidence := backupartifact.RestoreActivationEvidence{
		Kind:                 backupartifact.RestoreActivationBreakGlass,
		EvidenceSHA256:       digest,
		Operator:             request.Operator,
		RecordedAtUnixMillis: activatedAt,
		BreakGlass:           &audit,
	}
	return evidence, backupartifact.ValidateRestoreActivationEvidence(evidence)
}

func sameRestoreActivationRequest(
	evidence *backupartifact.RestoreActivationEvidence,
	request RestoreActivationRequest,
) bool {
	if evidence == nil {
		return false
	}
	if request.SourceFenceReceipt != nil &&
		evidence.Kind == backupartifact.RestoreActivationSourceFence &&
		evidence.Operator == request.Operator {
		digest, err := backupartifact.SourceFenceReceiptDigest(
			*request.SourceFenceReceipt,
		)
		return err == nil && digest == evidence.EvidenceSHA256
	}
	return evidence.Kind == backupartifact.RestoreActivationBreakGlass &&
		evidence.BreakGlass != nil &&
		evidence.BreakGlass.Operator == request.Operator &&
		evidence.BreakGlass.Reason == request.BreakGlassReason
}

func sameRestorePartitionResult(left, right RestorePartition) bool {
	return left.Status == right.Status &&
		left.TargetSlotID == right.TargetSlotID &&
		left.LeaderNodeID == right.LeaderNodeID &&
		left.LeaderTerm == right.LeaderTerm &&
		left.ConfigEpoch == right.ConfigEpoch &&
		left.InstallAttempt == right.InstallAttempt &&
		left.ReplicaCount == right.ReplicaCount &&
		left.ConvergedReplicas == right.ConvergedReplicas &&
		left.DownloadedBytes == right.DownloadedBytes &&
		left.ReplicatedBytes == right.ReplicatedBytes &&
		left.StartedAtUnixMillis == right.StartedAtUnixMillis &&
		left.InstalledAtUnixMillis == right.InstalledAtUnixMillis &&
		left.Verified == right.Verified && sameRestorePartitionInstallEvidence(left, right)
}

func sameRestorePartitionInstallEvidence(left, right RestorePartition) bool {
	return left.HashSlot == right.HashSlot && left.EvidenceVersion == right.EvidenceVersion && left.Installed == right.Installed &&
		left.PlainBytes == right.PlainBytes && left.MetadataRecordCount == right.MetadataRecordCount && left.MessageCount == right.MessageCount &&
		left.MaxMessageID == right.MaxMessageID && left.MetadataSHA256 == right.MetadataSHA256 &&
		left.ContentSHA256 == right.ContentSHA256 &&
		left.MessageMerkleSHA256 == right.MessageMerkleSHA256 &&
		left.ChannelBoundaryCount == right.ChannelBoundaryCount &&
		left.FailureCategory == right.FailureCategory
}

func validRestorePartitionEvidence(partition RestorePartition) bool {
	return partition.EvidenceVersion == backupartifact.RestoreEvidenceVersion &&
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

// Progress returns a detached bounded operational projection.
func (a *RestoreApp) Progress(ctx context.Context) (*RestoreProgress, error) {
	plan, err := a.Status(ctx)
	if err != nil || plan == nil {
		return nil, err
	}
	progress := &RestoreProgress{
		PlanID: plan.ID, Status: plan.Status, TotalSlots: plan.HashSlotCount,
		Partitions: append([]RestorePartition(nil), plan.Partitions...),
	}
	downloaded, replicated, startedAtUnixMillis, err :=
		restorePartitionByteProgress(plan.Partitions)
	if err != nil {
		return nil, err
	}
	progress.DownloadedBytes = downloaded
	progress.ReplicatedBytes = replicated
	for _, partition := range plan.Partitions {
		switch partition.Status {
		case backupcontract.RestorePartitionPending, "":
			progress.PendingSlots++
		case backupcontract.RestorePartitionInstalling:
			progress.InstallingSlots++
		case backupcontract.RestorePartitionInstalled,
			backupcontract.RestorePartitionConverging:
			progress.InstalledSlots++
		case backupcontract.RestorePartitionConverged:
			progress.InstalledSlots++
			progress.ConvergedSlots++
		case backupcontract.RestorePartitionFailed:
			progress.FailedSlots++
		}
	}
	elapsedMillis := int64(0)
	if startedAtUnixMillis > 0 {
		elapsedMillis = a.now().UTC().UnixMilli() - startedAtUnixMillis
	}
	progress.ThroughputBytesPerSecond = restoreDownloadThroughput(
		progress.DownloadedBytes, elapsedMillis,
	)
	if progress.ConvergedSlots > 0 && progress.ConvergedSlots < progress.TotalSlots &&
		elapsedMillis > 0 {
		remaining := uint64(progress.TotalSlots - progress.ConvergedSlots)
		elapsedSeconds := (uint64(elapsedMillis) + 999) / 1000
		eta := uint64(math.MaxUint64)
		if elapsedSeconds <= math.MaxUint64/remaining {
			eta = elapsedSeconds * remaining /
				uint64(progress.ConvergedSlots)
		}
		progress.ETASeconds = &eta
	}
	return progress, nil
}

// RestoreThroughput returns the usecase-owned aggregate logical download rate
// for one persisted plan projection.
func (a *RestoreApp) RestoreThroughput(plan RestorePlan) (uint64, error) {
	downloaded, _, startedAtUnixMillis, err :=
		restorePartitionByteProgress(plan.Partitions)
	if err != nil {
		return 0, err
	}
	elapsedMillis := int64(0)
	if startedAtUnixMillis > 0 {
		elapsedMillis = a.now().UTC().UnixMilli() - startedAtUnixMillis
	}
	return restoreDownloadThroughput(downloaded, elapsedMillis), nil
}

func restorePartitionByteProgress(
	partitions []RestorePartition,
) (uint64, uint64, int64, error) {
	var downloaded uint64
	var replicated uint64
	var startedAtUnixMillis int64
	for _, partition := range partitions {
		if math.MaxUint64-downloaded < partition.DownloadedBytes ||
			math.MaxUint64-replicated < partition.ReplicatedBytes {
			return 0, 0, 0, ErrStateConflict
		}
		downloaded += partition.DownloadedBytes
		replicated += partition.ReplicatedBytes
		if partition.StartedAtUnixMillis > 0 &&
			(startedAtUnixMillis == 0 ||
				partition.StartedAtUnixMillis < startedAtUnixMillis) {
			startedAtUnixMillis = partition.StartedAtUnixMillis
		}
	}
	return downloaded, replicated, startedAtUnixMillis, nil
}

func restoreDownloadThroughput(
	downloaded uint64,
	elapsedMillis int64,
) uint64 {
	if downloaded == 0 || elapsedMillis <= 0 {
		return 0
	}
	if downloaded > math.MaxUint64/1000 {
		return math.MaxUint64
	}
	return downloaded * 1000 / uint64(elapsedMillis)
}

func (a *RestoreApp) transition(ctx context.Context, planID string, mutate func(*RestorePlan) error) (RestorePlan, error) {
	if !a.enabled {
		return RestorePlan{}, ErrRestoreModeRequired
	}
	var result RestorePlan
	err := a.mutateRestore(ctx, func(state *RestoreState) error {
		if state.Plan == nil || state.Plan.ID != planID {
			return ErrRestorePlanNotFound
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
	const restoreStateRetries = 64
	for attempt := 0; attempt < restoreStateRetries; attempt++ {
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
				delay := time.Duration(attempt+1) * 100 * time.Microsecond
				if delay > 10*time.Millisecond {
					delay = 10 * time.Millisecond
				}
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
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
	if plan.CatalogProof != nil {
		proof := *plan.CatalogProof
		copy.CatalogProof = &proof
	}
	copy.ErasureHeads = append([]backupartifact.ErasureStreamHead(nil), plan.ErasureHeads...)
	if plan.EstimatedPlainBytes != nil {
		value := *plan.EstimatedPlainBytes
		copy.EstimatedPlainBytes = &value
	}
	if plan.EstimatedCipherBytes != nil {
		value := *plan.EstimatedCipherBytes
		copy.EstimatedCipherBytes = &value
	}
	copy.Activation = backupartifact.CloneRestoreActivationEvidence(
		plan.Activation,
	)
	copy.Partitions = append([]RestorePartition(nil), plan.Partitions...)
	return &copy
}

func validRestoreErasureHeads(heads []backupartifact.ErasureStreamHead, hashSlotCount uint16, eventCount uint64) bool {
	var total uint64
	for index, head := range heads {
		if head.HashSlot >= hashSlotCount || backupartifact.ValidateErasureStreamHead(head) != nil ||
			(index > 0 && heads[index-1].HashSlot >= head.HashSlot) ||
			head.Sequence > uint64(backupartifact.MaxErasureLedgerEvents)-total {
			return false
		}
		total += head.Sequence
	}
	return total == eventCount
}
