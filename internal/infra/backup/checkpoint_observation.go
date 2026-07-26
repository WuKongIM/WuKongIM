package backup

import (
	"context"
	"fmt"

	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
)

// CheckpointObservationSource hydrates node-local metrics and publication
// cadence from the authenticated Controller catalog head.
type CheckpointObservationSource struct {
	state CoordinationStateStore
	index *CheckpointCatalogIndex
}

// NewCheckpointObservationSource creates a durable latest-checkpoint reader.
func NewCheckpointObservationSource(
	state CoordinationStateStore,
	index *CheckpointCatalogIndex,
) (*CheckpointObservationSource, error) {
	if state == nil || index == nil {
		return nil, fmt.Errorf(
			"backup checkpoint observation source: dependencies are required",
		)
	}
	return &CheckpointObservationSource{state: state, index: index}, nil
}

// LatestCheckpoint returns the newest latest-state catalog entry. The index
// authenticates a changed head before making the observation visible.
func (s *CheckpointObservationSource) LatestCheckpoint(
	ctx context.Context,
) (runtimebackup.CheckpointObservation, bool, error) {
	state, err := s.state.Load(ctx)
	if err != nil {
		return runtimebackup.CheckpointObservation{}, false, err
	}
	if state.CatalogHead == nil {
		return runtimebackup.CheckpointObservation{}, false, nil
	}
	reference, err := s.index.LatestReference(ctx, *state.CatalogHead)
	if err != nil {
		return runtimebackup.CheckpointObservation{}, false, err
	}
	return runtimebackup.CheckpointObservation{
		EffectiveAtUnixMillis: reference.EffectiveAtUnixMillis,
		CreatedAtUnixMillis:   reference.CreatedAtUnixMillis,
	}, true, nil
}

var _ runtimebackup.ContinuousCheckpointObservationSource = (*CheckpointObservationSource)(nil)
