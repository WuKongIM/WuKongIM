package app

import (
	"time"

	cloudsimfake "github.com/WuKongIM/WuKongIM/internal/infra/cloudsim/fake"
	cloudsim "github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
)

// NewFakeCloudSimulationControlPlane composes the persistent Phase 1 fake inventory adapter.
func NewFakeCloudSimulationControlPlane(statePath string, now func() time.Time) (*cloudsim.ControlPlane, error) {
	provider, err := cloudsimfake.Open(cloudsimfake.Options{StatePath: statePath, Now: now})
	if err != nil {
		return nil, err
	}
	return cloudsim.NewControlPlane(provider, now), nil
}
