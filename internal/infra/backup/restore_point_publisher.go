package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const maxLoadedPartitionManifestBytes = 16 << 20

// RestorePointPublisherOptions configures top-level signed restore-point publication.
type RestorePointPublisherOptions struct {
	// Primary is the first explicit repository copy.
	Primary backupartifact.Repository
	// Secondary is the independent recovery repository copy.
	Secondary backupartifact.Repository
	// Signer authenticates the canonical top-level manifest.
	Signer backupartifact.ManifestSigner
	// SigningKeyID identifies the external signing key.
	SigningKeyID string
	// ApplicationVersion records the producing WuKongIM version.
	ApplicationVersion string
	// RepositoryID is the stable logical repository identity shared by both copies.
	RepositoryID string
	// SourceClusterID is the source cluster identity.
	SourceClusterID string
	// SourceGeneration fences the source disaster-recovery generation.
	SourceGeneration string
	// ErasureLedgerBoundary is the permanent-erasure sequence included by publication.
	ErasureLedgerBoundary uint64
	// Now returns the UTC publication time.
	Now func() time.Time
	// NewRestorePointID returns a globally unique restore-point identity.
	NewRestorePointID func() string
}

// RestorePointPublisher verifies partition manifests and publishes one signed complete point.
type RestorePointPublisher struct {
	primary               backupartifact.Repository
	secondary             backupartifact.Repository
	publisher             *backupartifact.ReplicatedPublisher
	signer                backupartifact.ManifestSigner
	signingKeyID          string
	applicationVersion    string
	repositoryID          string
	sourceClusterID       string
	sourceGeneration      string
	erasureLedgerBoundary uint64
	now                   func() time.Time
	newRestorePointID     func() string
}

// NewRestorePointPublisher creates a partition-reference publisher.
func NewRestorePointPublisher(options RestorePointPublisherOptions) (*RestorePointPublisher, error) {
	if options.Primary == nil || options.Secondary == nil || options.Signer == nil ||
		strings.TrimSpace(options.SigningKeyID) == "" || strings.TrimSpace(options.ApplicationVersion) == "" ||
		strings.TrimSpace(options.RepositoryID) == "" || strings.TrimSpace(options.SourceClusterID) == "" ||
		strings.TrimSpace(options.SourceGeneration) == "" || options.Now == nil || options.NewRestorePointID == nil {
		return nil, fmt.Errorf("backup restore-point publisher: invalid options")
	}
	if options.Primary.Name() == "" || options.Secondary.Name() == "" || options.Primary.Name() == options.Secondary.Name() {
		return nil, fmt.Errorf("%w: repositories must be distinct", backupartifact.ErrRepositoryIncomplete)
	}
	return &RestorePointPublisher{
		primary:               options.Primary,
		secondary:             options.Secondary,
		publisher:             backupartifact.NewReplicatedPublisher(options.Primary, options.Secondary),
		signer:                options.Signer,
		signingKeyID:          options.SigningKeyID,
		applicationVersion:    options.ApplicationVersion,
		repositoryID:          options.RepositoryID,
		sourceClusterID:       options.SourceClusterID,
		sourceGeneration:      options.SourceGeneration,
		erasureLedgerBoundary: options.ErasureLedgerBoundary,
		now:                   options.Now,
		newRestorePointID:     options.NewRestorePointID,
	}, nil
}

// Publish verifies every bounded report against both repositories before exposing a restore point.
func (p *RestorePointPublisher) Publish(ctx context.Context, job backupusecase.Job) (backupusecase.RestorePoint, error) {
	if job.ID == "" || job.Epoch == 0 || job.HashSlotCount == 0 || len(job.Partitions) != int(job.HashSlotCount) {
		return backupusecase.RestorePoint{}, backupusecase.ErrPartitionsIncomplete
	}
	cuts := make([]backupartifact.PartitionCut, 0, job.HashSlotCount)
	partitions := make([]backupartifact.PartitionReference, 0, job.HashSlotCount)
	for index, report := range job.Partitions {
		if report.HashSlot != uint16(index) || report.JobID != job.ID || report.BackupEpoch != job.Epoch {
			return backupusecase.RestorePoint{}, fmt.Errorf("%w: partition report fence mismatch", backupusecase.ErrStateConflict)
		}
		primaryBody, primaryManifest, err := loadPartitionManifestCopy(ctx, p.primary, report)
		if err != nil {
			return backupusecase.RestorePoint{}, err
		}
		secondaryBody, _, err := loadPartitionManifestCopy(ctx, p.secondary, report)
		if err != nil {
			return backupusecase.RestorePoint{}, err
		}
		if !bytes.Equal(primaryBody, secondaryBody) {
			return backupusecase.RestorePoint{}, fmt.Errorf("%w: partition manifest copies differ", backupartifact.ErrRepositoryIncomplete)
		}
		if primaryManifest.JobID != job.ID || primaryManifest.BackupEpoch != job.Epoch ||
			primaryManifest.Cut.HashSlot != report.HashSlot || primaryManifest.Cut.RaftIndex != report.RaftIndex ||
			primaryManifest.Cut.CommittedAtMillis != report.CommittedAtUnixMillis {
			return backupusecase.RestorePoint{}, fmt.Errorf("%w: partition manifest fence mismatch", backupusecase.ErrStateConflict)
		}
		var ciphertextBytes uint64
		for _, object := range primaryManifest.Objects {
			ciphertextBytes += uint64(object.CiphertextBytes)
		}
		if uint64(len(primaryManifest.Objects)) != report.ObjectCount || ciphertextBytes != report.CiphertextBytes {
			return backupusecase.RestorePoint{}, fmt.Errorf("%w: partition summary mismatch", backupusecase.ErrStateConflict)
		}
		cuts = append(cuts, primaryManifest.Cut)
		partitions = append(partitions, backupartifact.PartitionReference{
			HashSlot: report.HashSlot, Key: report.ManifestKey, SHA256: report.ManifestSHA256,
			Bytes: int64(len(primaryBody)), ObjectCount: report.ObjectCount, CiphertextBytes: report.CiphertextBytes,
			Evidence: primaryManifest.Evidence,
		})
	}
	effectiveAt := cuts[0].CommittedAtMillis
	for _, cut := range cuts[1:] {
		if cut.CommittedAtMillis < effectiveAt {
			effectiveAt = cut.CommittedAtMillis
		}
	}
	restorePointID := strings.TrimSpace(job.RestorePointID)
	if restorePointID == "" {
		restorePointID = strings.TrimSpace(p.newRestorePointID())
	}
	createdAt := p.now().UTC().UnixMilli()
	manifest := backupartifact.Manifest{
		Format:                backupartifact.ManifestFormat,
		Version:               backupartifact.ManifestVersion,
		ApplicationVersion:    p.applicationVersion,
		RepositoryID:          p.repositoryID,
		SourceClusterID:       p.sourceClusterID,
		SourceGeneration:      p.sourceGeneration,
		RestorePointID:        restorePointID,
		BackupEpoch:           job.Epoch,
		Kind:                  job.Kind,
		HashSlotCount:         job.HashSlotCount,
		CreatedAtUnixMillis:   createdAt,
		EffectiveAtMillis:     effectiveAt,
		Cuts:                  cuts,
		Partitions:            partitions,
		ErasureLedgerBoundary: p.erasureLedgerBoundary,
	}
	signed, err := p.publisher.PublishReferences(ctx, manifest, p.signer, p.signingKeyID)
	if err != nil {
		return backupusecase.RestorePoint{}, err
	}
	body, err := backupartifact.MarshalManifest(signed)
	if err != nil {
		return backupusecase.RestorePoint{}, err
	}
	hash := sha256.Sum256(body)
	return backupusecase.RestorePoint{
		ID:                    signed.RestorePointID,
		JobID:                 job.ID,
		BackupEpoch:           job.Epoch,
		Kind:                  job.Kind,
		EffectiveAtUnixMillis: signed.EffectiveAtMillis,
		CreatedAtUnixMillis:   signed.CreatedAtUnixMillis,
		ManifestSHA256:        hex.EncodeToString(hash[:]),
		PrimaryVerified:       true,
		SecondaryVerified:     true,
	}, nil
}

func loadPartitionManifestCopy(ctx context.Context, repository backupartifact.Repository, report backupusecase.PartitionReport) ([]byte, backupartifact.PartitionManifest, error) {
	reader, object, err := repository.Open(ctx, report.ManifestKey)
	if err != nil {
		return nil, backupartifact.PartitionManifest{}, fmt.Errorf("%w: %s partition manifest: %v", backupartifact.ErrRepositoryIncomplete, repository.Name(), err)
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, maxLoadedPartitionManifestBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, backupartifact.PartitionManifest{}, readErr
	}
	if closeErr != nil {
		return nil, backupartifact.PartitionManifest{}, closeErr
	}
	if len(body) > maxLoadedPartitionManifestBytes || object.Key != report.ManifestKey || object.Size != int64(len(body)) || object.SHA256 != report.ManifestSHA256 {
		return nil, backupartifact.PartitionManifest{}, fmt.Errorf("%w: %s partition manifest metadata mismatch", backupartifact.ErrRepositoryIncomplete, repository.Name())
	}
	hash := sha256.Sum256(body)
	if hex.EncodeToString(hash[:]) != report.ManifestSHA256 {
		return nil, backupartifact.PartitionManifest{}, fmt.Errorf("%w: partition manifest checksum mismatch", backupartifact.ErrRepositoryIncomplete)
	}
	manifest, err := backupartifact.LoadPartitionManifest(body)
	if err != nil {
		return nil, backupartifact.PartitionManifest{}, err
	}
	return body, manifest, nil
}

var _ backupusecase.RestorePointPublisher = (*RestorePointPublisher)(nil)
