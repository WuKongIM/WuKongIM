package backup

import (
	"context"
	"fmt"
	"strings"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// CheckpointRestoreInspectorOptions configures vNext checkpoint admission.
type CheckpointRestoreInspectorOptions struct {
	// Primary and Secondary are the independent immutable repository copies.
	Primary   backupartifact.Repository
	Secondary backupartifact.Repository
	// Signer and Codec authenticate catalog, erasure, and encrypted object evidence.
	Signer backupartifact.ManifestSigner
	Codec  *backupartifact.ObjectCodec
	// RepositoryID is the logical identity shared by both repository copies.
	RepositoryID string
	// Target proves the successor cluster is a different, empty generation.
	Target RestoreTargetProbe
	// Catalog resolves and pins exact checkpoint membership under the
	// operator-supplied immutable head.
	Catalog *ReplicatedCheckpointCatalog
	// Auditor performs a current full-graph dual-repository preflight.
	Auditor *CheckpointRestoreGraphAuditor
}

// CheckpointRestoreInspector pins one signed catalog proof, vector cut, and the
// latest dual-committed permanent-erasure heads before plan persistence.
type CheckpointRestoreInspector struct {
	options CheckpointRestoreInspectorOptions
}

// NewCheckpointRestoreInspector creates a fail-closed vNext restore inspector.
func NewCheckpointRestoreInspector(
	options CheckpointRestoreInspectorOptions,
) (*CheckpointRestoreInspector, error) {
	if options.Primary == nil || options.Secondary == nil ||
		options.Primary.Name() == options.Secondary.Name() ||
		options.Signer == nil || options.Codec == nil ||
		options.Target == nil || options.Catalog == nil ||
		options.Auditor == nil ||
		strings.TrimSpace(options.RepositoryID) == "" {
		return nil, fmt.Errorf("backup checkpoint restore inspector: invalid options")
	}
	options.RepositoryID = strings.TrimSpace(options.RepositoryID)
	return &CheckpointRestoreInspector{options: options}, nil
}

// Inspect proves the target is empty and returns immutable checkpoint and
// current erasure evidence. Latest selects the newest original checkpoint
// publication, never a later hold/release state append.
func (i *CheckpointRestoreInspector) Inspect(
	ctx context.Context,
	request backupusecase.RestorePlanRequest,
) (backupusecase.RestoreInspection, error) {
	if i == nil || (request.Repository != "primary" && request.Repository != "secondary") ||
		(strings.TrimSpace(request.RestorePointID) == "") == !request.LatestVerified ||
		request.CatalogHead == nil {
		return backupusecase.RestoreInspection{}, backupusecase.ErrInvalidRequest
	}
	target, err := i.options.Target.InspectRestoreTarget(ctx)
	if err != nil {
		return backupusecase.RestoreInspection{}, fmt.Errorf(
			"backup checkpoint restore inspector: target: %w", err,
		)
	}
	if strings.TrimSpace(target.ClusterID) == "" ||
		strings.TrimSpace(target.Generation) == "" ||
		target.HashSlotCount == 0 || !target.Empty {
		return backupusecase.RestoreInspection{}, fmt.Errorf(
			"backup checkpoint restore inspector: target is not a proven empty cluster",
		)
	}
	head := *request.CatalogHead
	proof, checkpoint, err := i.options.Catalog.
		ResolveCheckpointForRestoreDual(
			ctx, head, strings.TrimSpace(request.RestorePointID),
			request.LatestVerified,
		)
	if err != nil {
		return backupusecase.RestoreInspection{}, err
	}
	if checkpoint.RepositoryID != i.options.RepositoryID ||
		checkpoint.SourceClusterID == target.ClusterID ||
		checkpoint.SourceGeneration == target.Generation ||
		checkpoint.HashSlotCount != target.HashSlotCount ||
		len(checkpoint.Slots) != int(target.HashSlotCount) {
		return backupusecase.RestoreInspection{}, fmt.Errorf(
			"%w: checkpoint and target identities are incompatible",
			backupartifact.ErrInvalidObject,
		)
	}
	if err := i.options.Auditor.Audit(ctx, checkpoint); err != nil {
		return backupusecase.RestoreInspection{}, err
	}
	ledgerLoader, err := NewErasureLedgerLoader(ErasureLedgerLoaderOptions{
		Primary: i.options.Primary, Secondary: i.options.Secondary,
		Signer: i.options.Signer, Codec: i.options.Codec,
		RepositoryID:     i.options.RepositoryID,
		SourceClusterID:  checkpoint.SourceClusterID,
		SourceGeneration: checkpoint.SourceGeneration,
		HashSlotCount:    checkpoint.HashSlotCount,
	})
	if err != nil {
		return backupusecase.RestoreInspection{}, err
	}
	ledger, err := ledgerLoader.LoadDualSnapshotProof(
		ctx, checkpoint.ErasureHeads,
	)
	if err != nil {
		return backupusecase.RestoreInspection{}, err
	}
	if !ledger.ContainsHeads(checkpoint.ErasureHeads) {
		return backupusecase.RestoreInspection{}, fmt.Errorf(
			"%w: checkpoint requires unavailable erasure heads",
			backupartifact.ErrRepositoryIncomplete,
		)
	}
	return backupusecase.RestoreInspection{
		RestorePointID:                  checkpoint.ID,
		ManifestSHA256:                  proof.Checkpoint.SHA256,
		CatalogProof:                    &proof,
		CheckpointVersion:               checkpoint.Version,
		CheckpointCreatedAtUnixMillis:   checkpoint.CreatedAtUnixMillis,
		CheckpointEffectiveAtUnixMillis: checkpoint.EffectiveAtUnixMillis,
		SourceClusterID:                 checkpoint.SourceClusterID,
		SourceGeneration:                checkpoint.SourceGeneration,
		TargetClusterID:                 target.ClusterID,
		TargetGeneration:                target.Generation,
		HashSlotCount:                   target.HashSlotCount,
		ErasureLedgerVersion:            ledger.Version,
		ErasureEventCount:               ledger.EventCount,
		ErasureHeads: append(
			[]backupartifact.ErasureStreamHead(nil), ledger.Heads...,
		),
		ErasureLedgerSHA256: ledger.SHA256,
		TargetEmpty:         true,
	}, nil
}

var _ backupusecase.RestoreInspector = (*CheckpointRestoreInspector)(nil)
