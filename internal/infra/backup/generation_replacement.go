package backup

import (
	"context"
	"fmt"
	"math"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// GenerationArtifactAuditor performs independent dual-copy checks for one
// materialized partition graph and one continuous segment.
type GenerationArtifactAuditor interface {
	InspectPartitionArtifactCopies(
		context.Context,
		backupartifact.PartitionReference,
		int,
	) (backupartifact.PartitionArtifactAuditReport, error)
	InspectSegmentCopies(
		context.Context,
		backupartifact.SegmentReference,
	) (backupartifact.SegmentAuditReport, error)
}

// GenerationReplacementValidator fully authenticates a pending materialized
// Generation before the Slot frontier may promote it.
type GenerationReplacementValidator struct {
	auditor GenerationArtifactAuditor
}

// NewGenerationReplacementValidator creates the production promotion gate.
func NewGenerationReplacementValidator(
	auditor GenerationArtifactAuditor,
) (*GenerationReplacementValidator, error) {
	if auditor == nil {
		return nil, fmt.Errorf("backup generation validator: artifact auditor is required")
	}
	return &GenerationReplacementValidator{auditor: auditor}, nil
}

// ValidateGenerationReplacement checks both repositories for the complete
// partition manifest, every payload object, and its full message cursor.
func (v *GenerationReplacementValidator) ValidateGenerationReplacement(
	ctx context.Context,
	current backupcontract.SlotFrontier,
	baseline runtimebackup.MaterializedBaseline,
) error {
	if v == nil || v.auditor == nil || baseline.Generation == "" ||
		baseline.Reference.Partition.HashSlot != current.HashSlot ||
		baseline.Messages.BaselineCursorHead == nil {
		return runtimebackup.ErrInvalidCapture
	}
	manifest, err := v.auditor.InspectPartitionArtifactCopies(
		ctx, baseline.Reference.Partition, -1,
	)
	if err != nil {
		return err
	}
	if manifest.Navigation.HashSlot != current.HashSlot ||
		!allGenerationCopiesHealthy(manifest.Copies) {
		return fmt.Errorf("%w: materialized manifest copies are not healthy", runtimebackup.ErrInvalidCapture)
	}
	for index := uint64(0); index < manifest.Navigation.ObjectCount; index++ {
		if index > math.MaxInt {
			return runtimebackup.ErrInvalidCapture
		}
		report, err := v.auditor.InspectPartitionArtifactCopies(
			ctx, baseline.Reference.Partition, int(index),
		)
		if err != nil {
			return err
		}
		if report.Navigation.HashSlot != current.HashSlot ||
			!allGenerationCopiesHealthy(report.Copies) {
			return fmt.Errorf(
				"%w: materialized object %d copies are not healthy",
				runtimebackup.ErrInvalidCapture, index,
			)
		}
	}
	cursor := *baseline.Messages.BaselineCursorHead
	cursorReport, err := v.auditor.InspectSegmentCopies(ctx, cursor)
	if err != nil {
		return err
	}
	logical := cursorReport.Header.Logical
	if logical.HashSlot != current.HashSlot ||
		logical.Generation != baseline.Generation ||
		logical.Stream != backupartifact.SegmentStreamMessageBaselineCursor ||
		!allGenerationCopiesHealthy(cursorReport.Copies) {
		return fmt.Errorf("%w: materialized cursor copies are not healthy", runtimebackup.ErrInvalidCapture)
	}
	return nil
}

func allGenerationCopiesHealthy(copies []backupartifact.SegmentAuditCopy) bool {
	if len(copies) != 2 {
		return false
	}
	seen := make(map[string]struct{}, 2)
	for _, copy := range copies {
		if !copy.Healthy || copy.Repository == "" {
			return false
		}
		if _, exists := seen[copy.Repository]; exists {
			return false
		}
		seen[copy.Repository] = struct{}{}
	}
	return true
}

// ConservativeGenerationCostPlanner charges the whole configured admission
// capacity. This is deliberately independent of historical delta size and
// prevents under-admitting a full source snapshot whose exact byte count is
// only known while streaming.
type ConservativeGenerationCostPlanner struct {
	ioBytes      int64
	networkBytes int64
}

// NewConservativeGenerationCostPlanner creates an exclusive worst-case plan.
func NewConservativeGenerationCostPlanner(
	ioBytes, networkBytes int64,
) (*ConservativeGenerationCostPlanner, error) {
	if ioBytes <= 0 || networkBytes <= 0 {
		return nil, fmt.Errorf("backup generation cost planner: capacities must be positive")
	}
	return &ConservativeGenerationCostPlanner{
		ioBytes: ioBytes, networkBytes: networkBytes,
	}, nil
}

// PlanGenerationCompaction returns conservative whole-capacity admission.
func (p *ConservativeGenerationCostPlanner) PlanGenerationCompaction(
	context.Context,
	backupcontract.SlotFrontier,
) (runtimebackup.GenerationCompactionCost, error) {
	if p == nil || p.ioBytes <= 0 || p.networkBytes <= 0 {
		return runtimebackup.GenerationCompactionCost{}, runtimebackup.ErrInvalidCapture
	}
	return runtimebackup.GenerationCompactionCost{
		IOBytes: p.ioBytes, NetworkBytes: p.networkBytes,
	}, nil
}

var (
	_ runtimebackup.GenerationPromotionValidator    = (*GenerationReplacementValidator)(nil)
	_ runtimebackup.GenerationCompactionCostPlanner = (*ConservativeGenerationCostPlanner)(nil)
)
