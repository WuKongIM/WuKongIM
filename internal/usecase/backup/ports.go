package backup

import "context"

// StateStore persists bounded cluster coordination state through optimistic concurrency.
type StateStore interface {
	// Load returns one detached current state snapshot.
	Load(ctx context.Context) (State, error)
	// CompareAndSwap stores next only when revision remains current.
	CompareAndSwap(ctx context.Context, revision uint64, next State) error
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
