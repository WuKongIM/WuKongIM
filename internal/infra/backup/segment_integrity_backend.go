package backup

import (
	"context"
	"fmt"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupruntime "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// IntegrityAuditArtifactKind identifies one portable object-graph node.
type IntegrityAuditArtifactKind string

const (
	// IntegrityAuditArtifactSegment is a signed continuous segment commit plus payload.
	IntegrityAuditArtifactSegment IntegrityAuditArtifactKind = "segment"
	// IntegrityAuditArtifactPartition is a materialized partition manifest or payload.
	IntegrityAuditArtifactPartition IntegrityAuditArtifactKind = "partition"
	// IntegrityAuditArtifactErasure is one signed or encrypted permanent-erasure node.
	IntegrityAuditArtifactErasure IntegrityAuditArtifactKind = "erasure"
)

// SegmentIntegrityAuditTarget binds one durable opaque cursor to an exact
// content-addressed segment or materialized-partition graph node.
type SegmentIntegrityAuditTarget struct {
	// Administrative advances exactly one catalog-navigation boundary without
	// performing a synthetic repository artifact check.
	Administrative bool
	// Kind selects continuous segment or materialized partition validation.
	Kind IntegrityAuditArtifactKind
	// Reference identifies the exact content-addressed segment to inspect.
	Reference backupartifact.SegmentReference
	// Partition identifies the exact materialized root or base layer.
	Partition backupartifact.PartitionReference
	// PartitionObjectIndex is -1 for the manifest and otherwise selects one
	// encrypted object from its authenticated object list.
	PartitionObjectIndex int
	// Erasure identifies one commit, receipt, record, or encrypted event.
	Erasure ErasureIntegrityAuditTarget
	// Next is the precomputed continuation for simple immutable plans.
	Next backupcontract.IntegrityAuditCursor
	// DebtObjects is the bounded remaining target estimate after Reference.
	DebtObjects uint64
}

// IntegrityAuditArtifactReport carries authenticated navigation data without
// placing large partition manifests in the durable Controller cursor.
type IntegrityAuditArtifactReport struct {
	Segment   backupartifact.SegmentAuditReport
	Partition backupartifact.PartitionArtifactAuditReport
	Erasure   ErasureIntegrityAuditReport
}

// AdvancingSegmentIntegrityAuditPlan derives continuation from authenticated
// artifact plaintext, allowing a concrete catalog plan to walk object graphs.
type AdvancingSegmentIntegrityAuditPlan interface {
	Advance(
		context.Context,
		backupcontract.IntegrityAuditCursor,
		IntegrityAuditArtifactReport,
	) (backupcontract.IntegrityAuditCursor, uint64, error)
}

// SegmentIntegrityAuditPlan turns immutable catalog/frontier decisions into
// one exact segment at a time. The plan owns historical deduplication; the
// backend owns repository bytes and cryptographic validation.
type SegmentIntegrityAuditPlan interface {
	Start(
		context.Context,
		*backupcontract.IntegrityAuditCursor,
	) (backupcontract.IntegrityAuditCursor, uint64, error)
	Resolve(
		context.Context,
		backupcontract.IntegrityAuditCursor,
	) (SegmentIntegrityAuditTarget, error)
}

// SegmentCopyAuditor is the deep portable artifact verification/repair seam.
type SegmentCopyAuditor interface {
	InspectSegmentCopies(
		context.Context,
		backupartifact.SegmentReference,
	) (backupartifact.SegmentAuditReport, error)
	RepairSegmentCopy(
		context.Context,
		backupartifact.SegmentReference,
		string,
	) (int64, error)
}

// PartitionCopyAuditor is the deep materialized baseline verification seam.
type PartitionCopyAuditor interface {
	// BeginPartitionAuditCycle resets byte-bounded authenticated manifest
	// reuse when the durable catalog cycle changes.
	BeginPartitionAuditCycle(string)
	InspectPartitionArtifactCopies(
		context.Context,
		backupartifact.PartitionReference,
		int,
	) (backupartifact.PartitionArtifactAuditReport, error)
	RepairPartitionArtifactCopy(
		context.Context,
		backupartifact.PartitionReference,
		int,
		string,
	) (int64, error)
}

// SegmentIntegrityAuditBackend adapts a durable catalog plan to full replicated
// segment GET/decrypt/digest validation and exact-copy repair.
type SegmentIntegrityAuditBackend struct {
	plan       SegmentIntegrityAuditPlan
	segments   SegmentCopyAuditor
	partitions PartitionCopyAuditor
	erasures   ErasureCopyAuditor
}

// NewSegmentIntegrityAuditBackend creates the production segment audit adapter.
func NewSegmentIntegrityAuditBackend(
	plan SegmentIntegrityAuditPlan,
	segments SegmentCopyAuditor,
	partitions PartitionCopyAuditor,
) (*SegmentIntegrityAuditBackend, error) {
	if plan == nil || segments == nil || partitions == nil {
		return nil, fmt.Errorf("backup segment integrity backend: dependencies are required")
	}
	return &SegmentIntegrityAuditBackend{
		plan: plan, segments: segments, partitions: partitions,
	}, nil
}

// WithErasureAuditor attaches the permanent-erasure graph verifier before the
// backend is shared with the Controller-Leader runtime.
func (b *SegmentIntegrityAuditBackend) WithErasureAuditor(
	erasures ErasureCopyAuditor,
) (*SegmentIntegrityAuditBackend, error) {
	if b == nil || erasures == nil {
		return nil, fmt.Errorf(
			"backup segment integrity backend: erasure auditor is required",
		)
	}
	b.erasures = erasures
	return b, nil
}

// Start delegates historical cycle selection to the immutable catalog plan.
func (b *SegmentIntegrityAuditBackend) Start(
	ctx context.Context,
	previous *backupcontract.IntegrityAuditCursor,
) (backupcontract.IntegrityAuditCursor, uint64, error) {
	cursor, debt, err := b.plan.Start(ctx, previous)
	if err != nil {
		return backupcontract.IntegrityAuditCursor{}, 0, err
	}
	b.partitions.BeginPartitionAuditCycle(cursor.CycleID)
	return cursor, debt, nil
}

// Inspect fully validates both copies and binds the signed logical identity to
// the durable Slot and Generation cursor.
func (b *SegmentIntegrityAuditBackend) Inspect(
	ctx context.Context,
	cursor backupcontract.IntegrityAuditCursor,
) (backupruntime.IntegrityAuditInspection, error) {
	b.partitions.BeginPartitionAuditCycle(cursor.CycleID)
	target, err := b.plan.Resolve(ctx, cursor)
	if err != nil {
		return backupruntime.IntegrityAuditInspection{}, err
	}
	if target.Administrative {
		advancing, ok := b.plan.(AdvancingSegmentIntegrityAuditPlan)
		if !ok {
			return backupruntime.IntegrityAuditInspection{}, fmt.Errorf(
				"%w: administrative audit plan cannot advance",
				backupartifact.ErrInvalidObject,
			)
		}
		next, debt, err := advancing.Advance(
			ctx, cursor, IntegrityAuditArtifactReport{},
		)
		if err != nil {
			return backupruntime.IntegrityAuditInspection{}, err
		}
		return backupruntime.IntegrityAuditInspection{
			Copies: []backupruntime.IntegrityAuditCopy{
				{Repository: "catalog-primary", Healthy: true},
				{Repository: "catalog-secondary", Healthy: true},
			},
			Next: next, DebtObjects: debt, Administrative: true,
		}, nil
	}
	var report IntegrityAuditArtifactReport
	var rawCopies []backupartifact.SegmentAuditCopy
	var bytes int64
	switch target.Kind {
	case IntegrityAuditArtifactSegment:
		segmentReport, inspectErr := b.segments.InspectSegmentCopies(
			ctx, target.Reference,
		)
		if inspectErr != nil {
			return backupruntime.IntegrityAuditInspection{}, inspectErr
		}
		if segmentReport.Header != (backupartifact.SegmentHeader{}) {
			logical := segmentReport.Header.Logical
			if logical.HashSlot != cursor.HashSlot ||
				logical.Generation != cursor.Generation {
				return backupruntime.IntegrityAuditInspection{}, fmt.Errorf(
					"%w: signed segment identity escapes audit cursor",
					backupartifact.ErrObjectCorrupt,
				)
			}
		}
		report.Segment = segmentReport
		rawCopies = segmentReport.Copies
		bytes = target.Reference.PlaintextBytes
	case IntegrityAuditArtifactPartition:
		if target.Partition.HashSlot != cursor.HashSlot {
			return backupruntime.IntegrityAuditInspection{}, fmt.Errorf(
				"%w: partition identity escapes audit cursor",
				backupartifact.ErrObjectCorrupt,
			)
		}
		partitionReport, inspectErr := b.partitions.InspectPartitionArtifactCopies(
			ctx, target.Partition, target.PartitionObjectIndex,
		)
		if inspectErr != nil {
			return backupruntime.IntegrityAuditInspection{}, inspectErr
		}
		report.Partition = partitionReport
		rawCopies = partitionReport.Copies
		bytes = target.Partition.Bytes
	case IntegrityAuditArtifactErasure:
		if b.erasures == nil ||
			target.Erasure.HashSlot != cursor.HashSlot {
			return backupruntime.IntegrityAuditInspection{}, fmt.Errorf(
				"%w: erasure audit dependency or identity is invalid",
				backupartifact.ErrInvalidObject,
			)
		}
		erasureReport, inspectErr := b.erasures.InspectErasureArtifactCopies(
			ctx, target.Erasure,
		)
		if inspectErr != nil {
			return backupruntime.IntegrityAuditInspection{}, inspectErr
		}
		report.Erasure = erasureReport
		rawCopies = erasureReport.Copies
		bytes = 1
	default:
		return backupruntime.IntegrityAuditInspection{}, fmt.Errorf(
			"%w: unknown integrity audit artifact kind",
			backupartifact.ErrInvalidObject,
		)
	}
	copies := make([]backupruntime.IntegrityAuditCopy, len(rawCopies))
	for index, copyResult := range rawCopies {
		copies[index] = backupruntime.IntegrityAuditCopy{
			Repository: copyResult.Repository, Healthy: copyResult.Healthy,
			Category: backupcontract.IntegrityCorruptionCategory(copyResult.Category),
		}
		if copyResult.StoredBytes > bytes {
			bytes = copyResult.StoredBytes
		}
	}
	next := target.Next
	debt := target.DebtObjects
	if advancing, ok := b.plan.(AdvancingSegmentIntegrityAuditPlan); ok {
		next, debt, err = advancing.Advance(ctx, cursor, report)
		if err != nil {
			return backupruntime.IntegrityAuditInspection{}, err
		}
	}
	return backupruntime.IntegrityAuditInspection{
		Copies: copies, Next: next,
		ArtifactBytes: bytes, DebtObjects: debt,
	}, nil
}

// Repair copies the exact current target from its authenticated healthy peer.
func (b *SegmentIntegrityAuditBackend) Repair(
	ctx context.Context,
	cursor backupcontract.IntegrityAuditCursor,
	repository string,
) (int64, error) {
	b.partitions.BeginPartitionAuditCycle(cursor.CycleID)
	target, err := b.plan.Resolve(ctx, cursor)
	if err != nil {
		return 0, err
	}
	if target.Administrative {
		return 0, fmt.Errorf(
			"%w: catalog navigation cannot enter repair",
			backupartifact.ErrInvalidObject,
		)
	}
	switch target.Kind {
	case IntegrityAuditArtifactSegment:
		return b.segments.RepairSegmentCopy(ctx, target.Reference, repository)
	case IntegrityAuditArtifactPartition:
		return b.partitions.RepairPartitionArtifactCopy(
			ctx, target.Partition, target.PartitionObjectIndex, repository,
		)
	case IntegrityAuditArtifactErasure:
		if b.erasures == nil {
			return 0, fmt.Errorf(
				"%w: erasure audit dependency is missing",
				backupartifact.ErrInvalidObject,
			)
		}
		return b.erasures.RepairErasureArtifactCopy(
			ctx, target.Erasure, repository,
		)
	default:
		return 0, fmt.Errorf(
			"%w: unknown integrity audit artifact kind",
			backupartifact.ErrInvalidObject,
		)
	}
}

var _ backupruntime.IntegrityAuditBackend = (*SegmentIntegrityAuditBackend)(nil)
