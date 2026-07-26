package backup

import (
	"context"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// StateStore persists bounded cluster coordination state through optimistic concurrency.
type StateStore interface {
	// Load returns one detached current state snapshot.
	Load(ctx context.Context) (State, error)
	// CompareAndSwap stores next only when revision remains current.
	CompareAndSwap(ctx context.Context, revision uint64, next State) error
}

// SourceFenceConvergence waits until every active source data node reports
// observing the irreversible Controller fence revision.
type SourceFenceConvergence interface {
	WaitForSourceFence(context.Context, backupartifact.SourceFenceRecord) error
}

// RestorePointPublisher verifies repositories and publishes one complete restore point.
type RestorePointPublisher interface {
	// Publish publishes a restore point for a job whose logical partitions are complete.
	Publish(ctx context.Context, job Job) (RestorePoint, error)
}

// RestorePointVerifier performs an explicit repository and cryptographic audit.
type RestorePointVerifier interface {
	Verify(ctx context.Context, restorePointID string) (Verification, error)
}

// CheckpointCatalogPublisher dual-commits one vector cut and immutable catalog append.
type CheckpointCatalogPublisher interface {
	Publish(ctx context.Context, checkpoint backupartifact.Checkpoint, previous *backupartifact.CatalogPageReference) (backupartifact.CheckpointCatalogCommit, error)
}

// SegmentCommitVerifier authenticates one exact dual-repository frontier proof
// and returns its signed logical header for checkpoint identity binding.
type SegmentCommitVerifier interface {
	VerifyCommit(ctx context.Context, reference backupartifact.SegmentReference) (backupartifact.SegmentHeader, error)
}

// SlotCaptureStatusSource reports bounded current health for every capture worker.
type SlotCaptureStatusSource interface {
	Status() []backupcontract.SlotCaptureStatus
}

// CheckpointCatalogBrowser reads checkpoint history through a rebuildable derived index.
type CheckpointCatalogBrowser interface {
	List(ctx context.Context, head backupartifact.CatalogPageReference, request CheckpointListRequest) (CheckpointPage, error)
	Get(ctx context.Context, head backupartifact.CatalogPageReference, checkpointID string) (CheckpointDetail, error)
}
