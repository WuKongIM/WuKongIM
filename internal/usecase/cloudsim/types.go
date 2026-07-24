// Package cloudsim owns provider-neutral orchestration for temporary cloud
// simulation runs. Provider SDK details remain behind Provider.
package cloudsim

import (
	"context"
	"errors"
	"net/netip"
	"time"
)

const (
	// ManagedByValue is the stable owner tag placed on every run resource.
	ManagedByValue = "wukongim-cloud-sim"
	// MaxAnalysisWindow limits one temporary MCP ingress window.
	MaxAnalysisWindow = 50 * time.Minute
	// MaxDeploymentWindow limits one temporary SSH provisioning window.
	MaxDeploymentWindow = 20 * time.Minute
	// CloudViewPort is the only public browser ingress port for a Simulation Run.
	CloudViewPort uint16 = 19443
	// MinAnalysisLeaseRemaining prevents starting analysis immediately before release.
	MinAnalysisLeaseRemaining = 30 * time.Minute
)

const (
	// TagManagedBy identifies resources owned by this control plane.
	TagManagedBy = "wukongim-managed-by"
	// TagRunID binds a resource to exactly one Simulation Run.
	TagRunID = "wukongim-run-id"
	// TagRepository records the trusted repository identity.
	TagRepository = "wukongim-repository"
	// TagResourceRole records the logical resource role selected by the provider.
	TagResourceRole = "wukongim-resource-role"
	// TagSourceSHA records the immutable source commit.
	TagSourceSHA = "wukongim-source-sha"
	// TagScenarioDigest records the selected Scenario Profile digest.
	TagScenarioDigest = "wukongim-scenario-digest"
	// TagBundleDigest records the verified Deployment Bundle digest.
	TagBundleDigest = "wukongim-bundle-digest"
	// TagExpiresAt records the immutable Run Lease in RFC3339 form.
	TagExpiresAt = "wukongim-expires-at"
	// TagMCPCertificateFingerprint pins the run-specific Analysis MCP certificate.
	TagMCPCertificateFingerprint = "wukongim-mcp-cert-fingerprint"
)

var (
	// ErrInvalidRequest reports an invalid provider-neutral lifecycle request.
	ErrInvalidRequest = errors.New("internal/usecase/cloudsim: invalid request")
	// ErrActiveRunExists reports a repository/account concurrency conflict.
	ErrActiveRunExists = errors.New("internal/usecase/cloudsim: active run exists")
	// ErrPricingUnavailable reports that a bounded worst-case price is unavailable.
	ErrPricingUnavailable = errors.New("internal/usecase/cloudsim: pricing unavailable")
	// ErrCostLimitExceeded reports a quote above the caller's hard cost limit.
	ErrCostLimitExceeded = errors.New("internal/usecase/cloudsim: cost limit exceeded")
	// ErrCapacityUnavailable reports insufficient spot or subnet capacity.
	ErrCapacityUnavailable = errors.New("internal/usecase/cloudsim: capacity unavailable")
	// ErrQuotaUnavailable reports insufficient cloud quota.
	ErrQuotaUnavailable = errors.New("internal/usecase/cloudsim: quota unavailable")
	// ErrPresetTooSmall reports a preset below the Scenario Profile minimum.
	ErrPresetTooSmall = errors.New("internal/usecase/cloudsim: infrastructure preset too small")
	// ErrRunNotFound reports that provider inventory has no matching run.
	ErrRunNotFound = errors.New("internal/usecase/cloudsim: run not found")
	// ErrRunReleased reports that provider inventory proves the run is released.
	ErrRunReleased = errors.New("internal/usecase/cloudsim: run released")
	// ErrInvalidAnalysisWindow reports an unsafe analysis ingress request.
	ErrInvalidAnalysisWindow = errors.New("internal/usecase/cloudsim: invalid analysis window")
	// ErrInvalidDeploymentWindow reports an unsafe deployment ingress request.
	ErrInvalidDeploymentWindow = errors.New("internal/usecase/cloudsim: invalid deployment window")
	// ErrInvalidPublicViewWindow reports an unsafe public browser ingress request.
	ErrInvalidPublicViewWindow = errors.New("internal/usecase/cloudsim: invalid public view window")
)

// Preset is a provider-neutral infrastructure capacity class.
type Preset string

const (
	// PresetSmall is the minimum low-cost validation class.
	PresetSmall Preset = "small"
	// PresetStandard is the normal simulation class.
	PresetStandard Preset = "standard"
	// PresetStress is the high-capacity stress class.
	PresetStress Preset = "stress"
)

// State is the provider-neutral lifecycle state of one Simulation Run.
type State string

const (
	// StateProvisioning means resource creation or bootstrap is in progress.
	StateProvisioning State = "provisioning"
	// StateReady means the bootstrap gate passed and the workload may start.
	StateReady State = "ready"
	// StateRunning means the remote workload is active.
	StateRunning State = "running"
	// StateAnalysisGrace means workload traffic stopped but live diagnosis remains allowed.
	StateAnalysisGrace State = "analysis_grace"
	// StateInfrastructureInterrupted means temporary infrastructure ended unexpectedly.
	StateInfrastructureInterrupted State = "infrastructure_interrupted"
	// StateReleasePending means cleanup is required or in progress.
	StateReleasePending State = "release_pending"
	// StateReleased means provider inventory proves that run resources are absent.
	StateReleased State = "released"
)

// Quote is the provider's bounded price and capacity decision for a run.
type Quote struct {
	// Currency is the ISO-like currency code used by cost micros.
	Currency string `json:"currency"`
	// WorstCaseCostMicros is the maximum lease cost in millionths of Currency.
	WorstCaseCostMicros int64 `json:"worst_case_cost_micros"`
	// SelectedSKU is the concrete allowlisted spot type selected by the provider.
	SelectedSKU string `json:"selected_sku"`
	// SpotPriceMicrosPerHour is the current per-instance hourly price in currency micros.
	SpotPriceMicrosPerHour int64 `json:"spot_price_micros_per_hour"`
	// CapacityAvailable reports whether all required spot and subnet capacity is available.
	CapacityAvailable bool `json:"capacity_available"`
	// QuotaAvailable reports whether all required cloud quota is available.
	QuotaAvailable bool `json:"quota_available"`
}

// CreateRequest contains the immutable provider-neutral inputs for one run.
type CreateRequest struct {
	// RunID is the globally unique Simulation Run identity.
	RunID string `json:"run_id"`
	// Provider identifies the selected Cloud Provider Adapter.
	Provider string `json:"provider"`
	// Region is the provider region in which the isolated run is created.
	Region string `json:"region"`
	// AccountIDHash is a non-secret stable hash used for concurrency checks.
	AccountIDHash string `json:"account_id_hash"`
	// Repository is the trusted owner/name identity.
	Repository string `json:"repository"`
	// SourceSHA is the immutable source commit deployed to all hosts.
	SourceSHA string `json:"source_sha"`
	// ScenarioDigest identifies the versioned Scenario Profile.
	ScenarioDigest string `json:"scenario_digest"`
	// DeploymentBundleDigest identifies the verified immutable deployment bundle.
	DeploymentBundleDigest string `json:"deployment_bundle_digest"`
	// MCPCertificateFingerprint pins the run-specific simulator certificate.
	MCPCertificateFingerprint string `json:"mcp_certificate_fingerprint"`
	// BootstrapSSHPublicKey is the ephemeral deployment key installed on all hosts.
	// It is never copied into provider tags or the Run Locator.
	BootstrapSSHPublicKey string `json:"bootstrap_ssh_public_key,omitempty"`
	// Preset is the requested provider-neutral capacity class.
	Preset Preset `json:"preset"`
	// ScenarioMinimumPreset is the minimum capacity class allowed by the scenario.
	ScenarioMinimumPreset Preset `json:"scenario_minimum_preset"`
	// ExpiresAt is the immutable Run Lease deadline.
	ExpiresAt time.Time `json:"expires_at"`
	// MaxTotalCostMicros is the hard worst-case lease cost ceiling.
	MaxTotalCostMicros int64 `json:"max_total_cost_micros"`
	// Currency is the required quote currency.
	Currency string `json:"currency"`
	// Tags contains normalized mandatory logical resource tags after validation.
	Tags map[string]string `json:"tags"`
}

// AnalysisWindow is the currently open run-scoped MCP network window.
type AnalysisWindow struct {
	// SourcePrefix is the single admitted GitHub runner IPv4 host prefix.
	SourcePrefix netip.Prefix `json:"source_prefix"`
	// Until is the non-renewable window expiry.
	Until time.Time `json:"until"`
}

// DeploymentWindow is the currently open simulator-only SSH provisioning window.
type DeploymentWindow struct {
	// SourcePrefix is the single admitted GitHub runner IPv4 host prefix.
	SourcePrefix netip.Prefix `json:"source_prefix"`
	// Until is the non-renewable window expiry.
	Until time.Time `json:"until"`
}

// PublicViewWindow is the public HTTP and WebSocket ingress window for the
// run-scoped Cloud View process on the simulator host.
type PublicViewWindow struct {
	// SourcePrefix is exactly the public IPv4 Internet prefix 0.0.0.0/0.
	SourcePrefix netip.Prefix `json:"source_prefix"`
	// Until is bounded by the immutable Run Lease.
	Until time.Time `json:"until"`
}

// Resource is one provider inventory item belonging to a run.
type Resource struct {
	// ID is the provider resource identifier.
	ID string `json:"id"`
	// Kind is the provider-neutral resource kind such as compute, disk, or VPC.
	Kind string `json:"kind"`
	// Role is the logical run role such as node-1, sim, or network.
	Role string `json:"role"`
	// Billable reports whether the resource can accrue cost.
	Billable bool `json:"billable"`
	// PrivateAddress is returned only by live provider inventory and is never stored in a Run Locator.
	PrivateAddress string `json:"private_address,omitempty"`
	// PublicAddress is returned only for the simulator's temporary public endpoint.
	PublicAddress string `json:"public_address,omitempty"`
	// Tags contains normalized provider inventory tags.
	Tags map[string]string `json:"tags"`
}

// Run is the reconciled provider-neutral view of one Simulation Run.
type Run struct {
	// ID is the exact Run Identity.
	ID string `json:"id"`
	// Provider identifies the adapter that owns inventory reconciliation.
	Provider string `json:"provider"`
	// Region is the cloud region containing run resources.
	Region string `json:"region"`
	// AccountIDHash is the non-secret cloud account binding.
	AccountIDHash string `json:"account_id_hash"`
	// Repository is the trusted repository identity.
	Repository string `json:"repository"`
	// State is the reconciled lifecycle state.
	State State `json:"state"`
	// CreatedAt records when provider creation began.
	CreatedAt time.Time `json:"created_at"`
	// ExpiresAt is the immutable Run Lease deadline.
	ExpiresAt time.Time `json:"expires_at"`
	// ActiveUntil is the workload deadline recorded when the run enters running.
	ActiveUntil time.Time `json:"active_until,omitempty"`
	// Tags contains the run's normalized mandatory tags.
	Tags map[string]string `json:"tags"`
	// Resources contains the currently discovered provider inventory.
	Resources []Resource `json:"resources"`
	// Quote contains the accepted bounded price decision.
	Quote Quote `json:"quote"`
	// AnalysisWindow is non-nil only while temporary MCP ingress is open.
	AnalysisWindow *AnalysisWindow `json:"analysis_window,omitempty"`
	// DeploymentWindow is non-nil only while temporary simulator SSH ingress is open.
	DeploymentWindow *DeploymentWindow `json:"deployment_window,omitempty"`
	// PublicViewWindow is non-nil only while public Cloud View ingress is open.
	PublicViewWindow *PublicViewWindow `json:"public_view_window,omitempty"`
}

// OpenDeploymentRequest requests temporary simulator-only SSH ingress.
type OpenDeploymentRequest struct {
	// RunID is the exact provisioning Simulation Run identity.
	RunID string `json:"run_id"`
	// SourcePrefix is the GitHub runner IPv4 /32 admitted to TCP/22.
	SourcePrefix netip.Prefix `json:"source_prefix"`
	// Until is the requested non-renewable ingress expiry.
	Until time.Time `json:"until"`
}

// OpenAnalysisRequest requests temporary run-scoped MCP ingress.
type OpenAnalysisRequest struct {
	// RunID is the exact live Run Identity.
	RunID string `json:"run_id"`
	// SourcePrefix is the GitHub runner public IPv4 /32.
	SourcePrefix netip.Prefix `json:"source_prefix"`
	// Until is the requested non-renewable ingress expiry.
	Until time.Time `json:"until"`
}

// OpenPublicViewRequest requests public Cloud View ingress for one live run.
type OpenPublicViewRequest struct {
	// RunID is the exact live Simulation Run identity.
	RunID string `json:"run_id"`
	// SourcePrefix must be exactly the public IPv4 prefix 0.0.0.0/0.
	SourcePrefix netip.Prefix `json:"source_prefix"`
	// Until must not exceed the immutable Run Lease.
	Until time.Time `json:"until"`
}

// SweepResult reports deterministic expiry reconciliation outcomes.
type SweepResult struct {
	// Destroyed contains run IDs whose cleanup was requested successfully.
	Destroyed []string `json:"destroyed"`
	// Failed contains run IDs whose cleanup failed.
	Failed []string `json:"failed"`
}

// PreflightState is the provider-backed Analysis Workflow lookup result.
type PreflightState string

const (
	// PreflightLive means exact matching resources still exist.
	PreflightLive PreflightState = "live"
	// PreflightReleased means a valid locator exists and provider inventory is empty.
	PreflightReleased PreflightState = "released"
)

// PreflightResult binds a Run Locator to current provider inventory.
type PreflightResult struct {
	// State is live or provider-confirmed released.
	State PreflightState `json:"state"`
	// Resources is the exact provider inventory snapshot. Released results always encode an empty array.
	Resources []Resource `json:"resources"`
	// Run is present only when the exact locator-bound inventory is live.
	Run *Run `json:"run,omitempty"`
}

// ProviderAuthority identifies the account and region queried by one adapter.
type ProviderAuthority struct {
	// Provider is the stable provider adapter name.
	Provider string `json:"provider"`
	// Region is the exact inventory region queried by the adapter.
	Region string `json:"region"`
	// AccountIDHash is the non-secret account binding queried by the adapter.
	AccountIDHash string `json:"account_id_hash"`
}

// InventorySnapshot is a read-only, authority-bound candidate-discovery view.
// An absent run is not proof that it was released; callers must use Preflight
// with an exact Run Locator before making a released-state decision.
type InventorySnapshot struct {
	// Authority identifies the provider account and region queried for Runs.
	Authority ProviderAuthority `json:"authority"`
	// Runs contains only discovery candidates bound to Authority.
	// An empty slice cannot prove that any previously known run was released.
	Runs []Run `json:"runs"`
}

// TransitionRequest asks the provider to persist one allowed lifecycle step.
type TransitionRequest struct {
	// RunID is the exact Simulation Run identity.
	RunID string `json:"run_id"`
	// Next is the requested lifecycle state.
	Next State `json:"next"`
	// ActiveUntil is required only when entering running.
	ActiveUntil time.Time `json:"active_until,omitempty"`
}

// Provider is the narrow lifecycle boundary implemented by each cloud adapter.
type Provider interface {
	// Name returns the stable provider identifier.
	Name() string
	// Authority returns the exact account and region queried by this adapter.
	Authority(context.Context) (ProviderAuthority, error)
	// Inventory returns all run inventory visible to the adapter's scoped role.
	Inventory(context.Context) ([]Run, error)
	// Quote performs pricing, quota, spot-capacity, and subnet-capacity checks.
	Quote(context.Context, CreateRequest) (Quote, error)
	// Create atomically begins resource creation for an accepted quote.
	Create(context.Context, CreateRequest, Quote) (Run, error)
	// Status reconciles one exact run from provider inventory.
	Status(context.Context, string) (Run, error)
	// Transition persists one provider-backed lifecycle state change.
	Transition(context.Context, TransitionRequest) (Run, error)
	// OpenDeployment admits one temporary IPv4 /32 to simulator SSH only.
	OpenDeployment(context.Context, OpenDeploymentRequest) (Run, error)
	// CloseDeployment removes temporary simulator SSH ingress.
	CloseDeployment(context.Context, string) (Run, error)
	// OpenAnalysis admits one temporary IPv4 /32 to the simulator MCP.
	OpenAnalysis(context.Context, OpenAnalysisRequest) (Run, error)
	// CloseAnalysis removes temporary simulator MCP ingress.
	CloseAnalysis(context.Context, string) (Run, error)
	// OpenPublicView admits public IPv4 HTTP and WebSocket traffic to Cloud View.
	OpenPublicView(context.Context, OpenPublicViewRequest) (Run, error)
	// ClosePublicView removes public Cloud View ingress.
	ClosePublicView(context.Context, string) (Run, error)
	// Destroy releases every resource tagged with one exact Run Identity.
	Destroy(context.Context, string) (Run, error)
}

// MandatoryTagKeys returns the stable logical tag contract in deterministic order.
func MandatoryTagKeys() []string {
	return []string{
		TagManagedBy,
		TagRunID,
		TagRepository,
		TagResourceRole,
		TagSourceSHA,
		TagScenarioDigest,
		TagBundleDigest,
		TagExpiresAt,
		TagMCPCertificateFingerprint,
	}
}
