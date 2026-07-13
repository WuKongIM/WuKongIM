package app

import (
	"net/http"
	"time"

	cloudanalysismcp "github.com/WuKongIM/WuKongIM/internal/access/cloudanalysismcp"
	cloudanalysisinfra "github.com/WuKongIM/WuKongIM/internal/infra/cloudanalysis"
	cloudsimfake "github.com/WuKongIM/WuKongIM/internal/infra/cloudsim/fake"
	cloudanalysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
)

// CloudAnalysisGatewayConfig contains the complete Phase 1 gateway composition.
type CloudAnalysisGatewayConfig struct {
	// RunID is the exact Run Identity bound to this gateway.
	RunID string
	// RunState is the currently proven local or provider lifecycle state.
	RunState cloudanalysis.RunState
	// RunInventoryCount is the currently proven relevant resource count.
	RunInventoryCount int
	// Provider identifies the run inventory authority.
	Provider string
	// Region identifies the run location.
	Region string
	// SourceSHA is the immutable deployed source commit.
	SourceSHA string
	// Scenario is the exact effective wkbench contract bound to this gateway.
	Scenario cloudanalysis.ScenarioInspection
	// RunLocator is required for provider-backed release proof.
	RunLocator *cloudsim.RunLocator
	// RunExpiresAt is the immutable Run Lease deadline.
	RunExpiresAt time.Time
	// MCPToken is the random run-scoped bearer credential.
	MCPToken string
	// MCPTokenExpiresAt is the non-renewable Analysis Token deadline.
	MCPTokenExpiresAt time.Time
	// ManagerBaseURL is the fixed private manager origin.
	ManagerBaseURL string
	// ManagerAuth is the dedicated internal analysis capability credential.
	ManagerAuth cloudanalysisinfra.ManagerAuth
	// PrometheusBaseURL is the fixed simulator-local Prometheus origin.
	PrometheusBaseURL string
	// NodeAPIBaseURLs maps allowlisted node IDs to private API origins.
	NodeAPIBaseURLs map[uint64]string
	// MetricQueries maps stable MCP query IDs to server-owned PromQL.
	MetricQueries map[string]string
	// HTTPClient optionally overrides private-source HTTP behavior.
	HTTPClient *http.Client
	// FakeInventoryPath optionally enables provider-backed Phase 1 run inspection.
	FakeInventoryPath string
	// Now optionally supplies deterministic timestamps.
	Now func() time.Time
}

// NewCloudAnalysisGatewayHandler composes private HTTP adapters, usecase, and MCP access.
func NewCloudAnalysisGatewayHandler(cfg CloudAnalysisGatewayConfig) (http.Handler, error) {
	var inspector cloudanalysisinfra.RunInspector = cloudanalysisinfra.StaticRunInspector{Inspection: cloudanalysis.RunInspection{
		RunID: cfg.RunID, State: cfg.RunState, InventoryCount: cfg.RunInventoryCount,
		Provider: cfg.Provider, Region: cfg.Region, SourceSHA: cfg.SourceSHA, ExpiresAt: cfg.RunExpiresAt,
		Scenario: cfg.Scenario,
		Warnings: []string{"Phase 1 uses explicit local run state; cloud phases replace this with provider inventory proof"},
	}}
	if cfg.FakeInventoryPath != "" {
		provider, err := cloudsimfake.Open(cloudsimfake.Options{StatePath: cfg.FakeInventoryPath, Now: cfg.Now})
		if err != nil {
			return nil, err
		}
		if cfg.RunLocator == nil || cfg.RunLocator.Validate() != nil {
			return nil, cloudanalysis.ErrRunContractMismatch
		}
		inspector = cloudanalysisinfra.ProviderRunInspector{Source: provider, Locator: *cfg.RunLocator, Scenario: cfg.Scenario}
	}
	sources, err := cloudanalysisinfra.NewHTTPSources(cloudanalysisinfra.HTTPConfig{
		Inspector: inspector, ManagerBaseURL: cfg.ManagerBaseURL, ManagerAuth: cfg.ManagerAuth,
		PrometheusBaseURL: cfg.PrometheusBaseURL, NodeAPIBaseURLs: cfg.NodeAPIBaseURLs,
		HTTPClient: cfg.HTTPClient, Now: cfg.Now,
	})
	if err != nil {
		return nil, err
	}
	nodes := make([]uint64, 0, len(cfg.NodeAPIBaseURLs))
	for nodeID := range cfg.NodeAPIBaseURLs {
		nodes = append(nodes, nodeID)
	}
	service, err := cloudanalysis.New(cloudanalysis.Config{
		RunID: cfg.RunID, Nodes: nodes, MetricQueries: cfg.MetricQueries, Now: cfg.Now,
	}, sources)
	if err != nil {
		return nil, err
	}
	return cloudanalysismcp.NewHandler(cloudanalysismcp.Config{
		RunID: cfg.RunID, Token: cfg.MCPToken, TokenExpiresAt: cfg.MCPTokenExpiresAt, Service: service,
	})
}
