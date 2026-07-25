package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// MaterializedBaselineOptions configures a full-partition worker as a
// continuous-generation baseline producer.
type MaterializedBaselineOptions struct {
	Worker *DistributedWorker
	// Segments dual-commits the complete Channel cursor index.
	Segments SegmentCommitter
	// RepositoryID and source identity fence cursor commit proofs.
	RepositoryID     string
	SourceClusterID  string
	SourceGeneration string
	// KMSKeyID seals the baseline cursor.
	KMSKeyID string
}

// DistributedBaselineCapturer reuses the full logical partition snapshot path
// and publishes its resume cursor before the immutable partition manifest.
type DistributedBaselineCapturer struct {
	options MaterializedBaselineOptions
}

// NewDistributedBaselineCapturer creates a dual-repository materialized producer.
func NewDistributedBaselineCapturer(options MaterializedBaselineOptions) (*DistributedBaselineCapturer, error) {
	options.RepositoryID = strings.TrimSpace(options.RepositoryID)
	options.SourceClusterID = strings.TrimSpace(options.SourceClusterID)
	options.SourceGeneration = strings.TrimSpace(options.SourceGeneration)
	options.KMSKeyID = strings.TrimSpace(options.KMSKeyID)
	if options.Worker == nil || options.Segments == nil ||
		!validContinuousIdentity(options.RepositoryID, 128) ||
		!validContinuousIdentity(options.SourceClusterID, 128) ||
		!validContinuousIdentity(options.SourceGeneration, 128) ||
		options.KMSKeyID == "" || len(options.KMSKeyID) > 512 {
		return nil, fmt.Errorf("%w: materialized baseline dependencies are incomplete", ErrInvalidCapture)
	}
	return &DistributedBaselineCapturer{options: options}, nil
}

// CaptureBaseline creates or reloads one immutable full partition and cursor.
func (c *DistributedBaselineCapturer) CaptureBaseline(
	ctx context.Context,
	hashSlot uint16,
	generation string,
	epoch uint64,
	lease backupcontract.SlotCaptureLease,
	pinCut func(context.Context, uint64) error,
) (MaterializedBaseline, error) {
	if c == nil || c.options.Worker == nil || c.options.Segments == nil ||
		!validContinuousIdentity(generation, 128) || generation == lease.Generation ||
		epoch == 0 || pinCut == nil {
		return MaterializedBaseline{}, ErrInvalidCapture
	}
	fingerprintInput := fmt.Sprintf("%s:%05d:%020d", generation, hashSlot, epoch)
	fingerprint := sha256.Sum256([]byte(fingerprintInput))
	request := CaptureRequest{
		JobID: generation, BackupEpoch: epoch, HashSlot: hashSlot,
		ConfigFingerprint: hex.EncodeToString(fingerprint[:]),
		Kind:              backupartifact.RestorePointMaterializedFull,
	}
	result, err := c.options.Worker.capturePartition(
		ctx,
		request,
		func(
			ctx context.Context,
			request CaptureRequest,
			cut backupartifact.PartitionCut,
			boundaries []backupartifact.ChannelBoundary,
		) (backupartifact.SegmentReference, error) {
			body, err := backupartifact.MarshalChannelIndex(request.HashSlot, boundaries)
			if err != nil {
				return backupartifact.SegmentReference{}, err
			}
			recordCount := uint64(len(boundaries))
			if recordCount == 0 {
				recordCount = 1
			}
			reference, err := c.options.Segments.Commit(ctx, backupartifact.SegmentDescriptor{
				Logical: backupartifact.SegmentLogicalDescriptor{
					RepositoryID: c.options.RepositoryID, SourceClusterID: c.options.SourceClusterID,
					SourceGeneration: c.options.SourceGeneration, Generation: generation,
					HashSlot: request.HashSlot, Stream: backupartifact.SegmentStreamMessageBaselineCursor,
					Sequence: 1, RecordCount: recordCount,
				},
				KMSKeyID: c.options.KMSKeyID,
			}, body)
			if err != nil {
				return backupartifact.SegmentReference{}, err
			}
			if err := validateCommittedSegmentReference(reference); err != nil {
				return backupartifact.SegmentReference{}, err
			}
			_ = cut
			return reference, nil
		},
		func(ctx context.Context, cut backupartifact.PartitionCut) error {
			if cut.PhysicalSlotID != lease.SlotID {
				return ErrStaleCapture
			}
			return pinCut(ctx, cut.RaftIndex)
		},
	)
	if err != nil {
		return MaterializedBaseline{}, err
	}
	manifest := result.manifest
	if manifest.BaselineCursor == nil || manifest.Base != nil ||
		manifest.Cut.HashSlot != hashSlot || manifest.BackupEpoch != epoch ||
		manifest.JobID != generation || manifest.Cut.PhysicalSlotID != lease.SlotID {
		return MaterializedBaseline{}, fmt.Errorf("%w: materialized partition result is invalid", ErrInvalidCapture)
	}
	body, err := backupartifact.MarshalPartitionManifest(manifest)
	if err != nil {
		return MaterializedBaseline{}, err
	}
	partition := backupartifact.PartitionReference{
		HashSlot: hashSlot, Key: result.report.ManifestKey,
		SHA256: result.report.ManifestSHA256, Bytes: int64(len(body)),
		ObjectCount: result.report.ObjectCount, CiphertextBytes: result.report.CiphertextBytes,
		Evidence: manifest.Evidence,
	}
	cursor := *manifest.BaselineCursor
	capturedAt := manifest.Cut.CommittedAtMillis
	return MaterializedBaseline{
		Generation: generation,
		Reference:  backupcontract.SlotBaselineReference{Partition: partition},
		Metadata: backupcontract.StreamFrontier{
			SourceCursor:          strconv.FormatUint(manifest.Cut.RaftIndex, 10),
			SourceHighWatermark:   manifest.Cut.RaftIndex,
			WatermarkAtUnixMillis: capturedAt,
		},
		Messages: backupcontract.StreamFrontier{
			BaselineCursorHead:    &cursor,
			WatermarkAtUnixMillis: capturedAt,
		},
		WatermarkAtUnixMillis: capturedAt,
	}, nil
}

var _ MaterializedBaselineCapturer = (*DistributedBaselineCapturer)(nil)
