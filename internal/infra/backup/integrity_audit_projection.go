package backup

import (
	"context"
	"fmt"
	"time"
)

// IntegrityAuditProjectionRunner keeps every node's local Slot freeze
// projection aligned with locally applied Controller state.
type IntegrityAuditProjectionRunner struct {
	store    *ControllerIntegrityAuditStateStore
	interval time.Duration
}

// NewIntegrityAuditProjectionRunner creates an all-node projection loop.
func NewIntegrityAuditProjectionRunner(
	store *ControllerIntegrityAuditStateStore,
	interval time.Duration,
) (*IntegrityAuditProjectionRunner, error) {
	if store == nil || interval <= 0 {
		return nil, fmt.Errorf(
			"backup integrity audit projection: dependencies are invalid",
		)
	}
	return &IntegrityAuditProjectionRunner{
		store: store, interval: interval,
	}, nil
}

// Run refreshes the projection until context cancellation.
func (r *IntegrityAuditProjectionRunner) Run(ctx context.Context) error {
	if r == nil {
		return fmt.Errorf("backup integrity audit projection: runner is nil")
	}
	return r.store.RunProjection(ctx, r.interval)
}
