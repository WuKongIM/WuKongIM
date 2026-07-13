package cloudanalysis

import (
	"context"
	"fmt"

	analysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
)

// CloudRunStatusSource reads one exact run from a provider-backed inventory.
type CloudRunStatusSource interface {
	// Status returns the current provider-backed record for one Run Identity.
	Status(context.Context, string) (cloudsim.Run, error)
}

// ProviderRunInspector adapts provider-backed inventory to the analysis preflight contract.
type ProviderRunInspector struct {
	// Source is the provider-backed run inventory authority.
	Source CloudRunStatusSource
	// Locator is the previously validated minimal identity record for this run.
	Locator cloudsim.RunLocator
	// Scenario is the exact effective workload contract deployed for this run.
	Scenario analysis.ScenarioInspection
}

// InspectRun returns provider-backed lifecycle and resource-count proof.
func (i ProviderRunInspector) InspectRun(ctx context.Context, runID string) (analysis.RunInspection, error) {
	if i.Source == nil {
		return analysis.RunInspection{}, fmt.Errorf("%w: missing provider inventory", ErrInvalidHTTPConfig)
	}
	if i.Locator.Validate() != nil || runID != i.Locator.RunID {
		return analysis.RunInspection{}, analysis.ErrRunIdentityMismatch
	}
	run, err := i.Source.Status(ctx, runID)
	if err != nil {
		return analysis.RunInspection{}, err
	}
	if run.ID != runID || run.Provider != i.Locator.Provider || run.Region != i.Locator.Region ||
		run.AccountIDHash != i.Locator.AccountIDHash || run.Repository != i.Locator.Repository ||
		run.Tags[cloudsim.TagSourceSHA] != i.Locator.SourceSHA ||
		run.Tags[cloudsim.TagScenarioDigest] != i.Locator.ScenarioDigest ||
		!run.CreatedAt.Equal(i.Locator.CreatedAt) || !run.ExpiresAt.Equal(i.Locator.ExpiresAt) ||
		i.Scenario.Digest != i.Locator.ScenarioDigest {
		return analysis.RunInspection{}, analysis.ErrRunIdentityMismatch
	}
	return analysis.RunInspection{
		RunID:          run.ID,
		State:          analysis.RunState(run.State),
		InventoryCount: len(run.Resources),
		Provider:       run.Provider,
		Region:         run.Region,
		SourceSHA:      run.Tags[cloudsim.TagSourceSHA],
		ExpiresAt:      run.ExpiresAt,
		Scenario:       i.Scenario,
	}, nil
}
