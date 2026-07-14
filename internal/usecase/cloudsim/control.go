package cloudsim

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"
)

// ControlPlane applies provider-neutral lifecycle and safety invariants.
type ControlPlane struct {
	provider Provider
	now      func() time.Time
}

// NewControlPlane creates a provider-neutral Simulation Run lifecycle authority.
func NewControlPlane(provider Provider, now func() time.Time) *ControlPlane {
	if now == nil {
		now = time.Now
	}
	return &ControlPlane{provider: provider, now: now}
}

// Create validates concurrency, cost, capacity, quota, and immutable tags before mutation.
func (c *ControlPlane) Create(ctx context.Context, req CreateRequest) (Run, error) {
	if err := c.validateCreateRequest(req); err != nil {
		return Run{}, err
	}
	inventory, err := c.provider.Inventory(ctx)
	if err != nil {
		return Run{}, fmt.Errorf("%w: inventory: %v", ErrInvalidRequest, err)
	}
	for _, run := range inventory {
		if run.Repository == req.Repository && run.AccountIDHash == req.AccountIDHash && runIsActive(run, c.now()) {
			return Run{}, fmt.Errorf("%w: %s", ErrActiveRunExists, run.ID)
		}
	}
	quote, err := c.provider.Quote(ctx, req)
	if err != nil {
		return Run{}, fmt.Errorf("%w: %v", ErrPricingUnavailable, err)
	}
	if strings.TrimSpace(quote.Currency) == "" || quote.WorstCaseCostMicros <= 0 {
		return Run{}, ErrPricingUnavailable
	}
	if quote.Currency != req.Currency {
		return Run{}, fmt.Errorf("%w: quote currency %q", ErrPricingUnavailable, quote.Currency)
	}
	if !quote.CapacityAvailable {
		return Run{}, ErrCapacityUnavailable
	}
	if !quote.QuotaAvailable {
		return Run{}, ErrQuotaUnavailable
	}
	if quote.WorstCaseCostMicros > req.MaxTotalCostMicros {
		return Run{}, fmt.Errorf("%w: quote=%d limit=%d", ErrCostLimitExceeded, quote.WorstCaseCostMicros, req.MaxTotalCostMicros)
	}
	req.Tags = mandatoryTags(req)
	run, err := c.provider.Create(ctx, req, quote)
	if err != nil {
		return Run{}, fmt.Errorf("cloudsim create: %w", err)
	}
	return run, nil
}

// Status reconciles one exact run from provider inventory.
func (c *ControlPlane) Status(ctx context.Context, runID string) (Run, error) {
	if c == nil || c.provider == nil || strings.TrimSpace(runID) == "" {
		return Run{}, ErrInvalidRequest
	}
	return c.provider.Status(ctx, runID)
}

// Transition validates and persists one forward lifecycle state change.
func (c *ControlPlane) Transition(ctx context.Context, req TransitionRequest) (Run, error) {
	if c == nil || c.provider == nil || strings.TrimSpace(req.RunID) == "" {
		return Run{}, ErrInvalidRequest
	}
	run, err := c.provider.Status(ctx, req.RunID)
	if err != nil {
		return Run{}, err
	}
	allowed := false
	switch req.Next {
	case StateReady:
		allowed = run.State == StateProvisioning && req.ActiveUntil.IsZero()
	case StateRunning:
		allowed = run.State == StateReady && req.ActiveUntil.After(c.now().UTC()) && !req.ActiveUntil.After(run.ExpiresAt)
	case StateAnalysisGrace:
		allowed = (run.State == StateProvisioning || run.State == StateReady || run.State == StateRunning) && req.ActiveUntil.IsZero()
	}
	if !allowed {
		return Run{}, ErrInvalidRequest
	}
	return c.provider.Transition(ctx, req)
}

// Preflight validates an immutable Run Locator against current provider inventory.
// Only a valid locator plus provider-confirmed empty inventory is released.
func (c *ControlPlane) Preflight(ctx context.Context, locator RunLocator) (PreflightResult, error) {
	if c == nil || c.provider == nil || locator.Validate() != nil || locator.Provider != c.provider.Name() {
		return PreflightResult{}, ErrInvalidRequest
	}
	authority, err := c.providerAuthority(ctx)
	if err != nil {
		return PreflightResult{}, err
	}
	if authority.Provider != locator.Provider || authority.Region != locator.Region || authority.AccountIDHash != locator.AccountIDHash {
		return PreflightResult{}, ErrInvalidRequest
	}
	run, err := c.provider.Status(ctx, locator.RunID)
	if errors.Is(err, ErrRunNotFound) {
		return PreflightResult{State: PreflightReleased}, nil
	}
	if err != nil {
		return PreflightResult{}, err
	}
	if run.State == StateReleased && len(run.Resources) == 0 {
		return PreflightResult{State: PreflightReleased}, nil
	}
	if run.ID != locator.RunID || run.Provider != locator.Provider || run.Region != locator.Region ||
		run.AccountIDHash != locator.AccountIDHash || run.Repository != locator.Repository ||
		run.Tags[TagSourceSHA] != locator.SourceSHA || run.Tags[TagScenarioDigest] != locator.ScenarioDigest ||
		!run.CreatedAt.Equal(locator.CreatedAt) || !run.ExpiresAt.Equal(locator.ExpiresAt) {
		return PreflightResult{}, ErrInvalidRequest
	}
	return PreflightResult{State: PreflightLive, Run: &run}, nil
}

// OpenDeployment validates a single-host bounded SSH ingress window before mutation.
func (c *ControlPlane) OpenDeployment(ctx context.Context, req OpenDeploymentRequest) (Run, error) {
	if c == nil || c.provider == nil || strings.TrimSpace(req.RunID) == "" {
		return Run{}, ErrInvalidDeploymentWindow
	}
	now := c.now()
	if !validHostWindow(req.SourcePrefix, req.Until, now, MaxDeploymentWindow) {
		return Run{}, ErrInvalidDeploymentWindow
	}
	run, err := c.provider.Status(ctx, req.RunID)
	if err != nil {
		return Run{}, err
	}
	if run.State == StateReleased {
		return Run{}, ErrRunReleased
	}
	if req.Until.After(run.ExpiresAt) {
		return Run{}, ErrInvalidDeploymentWindow
	}
	return c.provider.OpenDeployment(ctx, req)
}

// CloseDeployment removes temporary simulator SSH ingress for one exact run.
func (c *ControlPlane) CloseDeployment(ctx context.Context, runID string) (Run, error) {
	if c == nil || c.provider == nil || strings.TrimSpace(runID) == "" {
		return Run{}, ErrInvalidRequest
	}
	return c.provider.CloseDeployment(ctx, runID)
}

// OpenAnalysis validates a single-host bounded ingress window before mutation.
func (c *ControlPlane) OpenAnalysis(ctx context.Context, req OpenAnalysisRequest) (Run, error) {
	if c == nil || c.provider == nil || strings.TrimSpace(req.RunID) == "" {
		return Run{}, ErrInvalidAnalysisWindow
	}
	now := c.now()
	if !validHostWindow(req.SourcePrefix, req.Until, now, MaxAnalysisWindow) {
		return Run{}, ErrInvalidAnalysisWindow
	}
	run, err := c.provider.Status(ctx, req.RunID)
	if err != nil {
		return Run{}, err
	}
	if run.State != StateRunning && run.State != StateAnalysisGrace && run.State != StateInfrastructureInterrupted {
		return Run{}, ErrInvalidAnalysisWindow
	}
	if run.AnalysisWindow != nil && run.AnalysisWindow.Until.After(now) {
		return Run{}, ErrInvalidAnalysisWindow
	}
	if run.ExpiresAt.Sub(now) < MinAnalysisLeaseRemaining || req.Until.After(run.ExpiresAt) {
		return Run{}, ErrInvalidAnalysisWindow
	}
	return c.provider.OpenAnalysis(ctx, req)
}

func validHostWindow(source netip.Prefix, until, now time.Time, maximum time.Duration) bool {
	return validHostPrefix(source) &&
		until.After(now) && !until.After(now.Add(maximum))
}

func validHostPrefix(source netip.Prefix) bool {
	return source.IsValid() && source.Addr().Is4() && source.Bits() == 32 && source == source.Masked()
}

func validObservedHostWindow(source netip.Prefix, until, now, leaseEnd time.Time, maximum time.Duration) bool {
	return validHostPrefix(source) && until.After(now) && !until.After(now.Add(maximum)) && !until.After(leaseEnd)
}

// CloseAnalysis removes temporary ingress for one exact run.
func (c *ControlPlane) CloseAnalysis(ctx context.Context, runID string) (Run, error) {
	if c == nil || c.provider == nil || strings.TrimSpace(runID) == "" {
		return Run{}, ErrInvalidRequest
	}
	return c.provider.CloseAnalysis(ctx, runID)
}

// Destroy releases all provider resources tagged with one exact Run Identity.
func (c *ControlPlane) Destroy(ctx context.Context, runID string) (Run, error) {
	if c == nil || c.provider == nil || strings.TrimSpace(runID) == "" {
		return Run{}, ErrInvalidRequest
	}
	if _, err := c.providerAuthority(ctx); err != nil {
		return Run{}, err
	}
	return c.provider.Destroy(ctx, runID)
}

// Sweep closes expired temporary ingress on active runs and destroys every run
// whose immutable Run Lease expired. Provider inventory carries window expiry
// so local Analysis Sessions remain valid without holding a workflow lock.
func (c *ControlPlane) Sweep(ctx context.Context) (SweepResult, error) {
	if c == nil || c.provider == nil {
		return SweepResult{}, ErrInvalidRequest
	}
	if _, err := c.providerAuthority(ctx); err != nil {
		return SweepResult{}, err
	}
	runs, err := c.provider.Inventory(ctx)
	if err != nil {
		return SweepResult{}, err
	}
	result := SweepResult{Destroyed: make([]string, 0), Failed: make([]string, 0)}
	errs := make([]error, 0)
	now := c.now()
	for _, run := range runs {
		if run.State == StateReleased || run.ExpiresAt.IsZero() {
			continue
		}
		if run.ExpiresAt.After(now) {
			if run.DeploymentWindow != nil && !validObservedHostWindow(
				run.DeploymentWindow.SourcePrefix, run.DeploymentWindow.Until, now, run.ExpiresAt, MaxDeploymentWindow,
			) {
				if _, closeErr := c.provider.CloseDeployment(ctx, run.ID); closeErr != nil {
					result.Failed = append(result.Failed, run.ID)
					errs = append(errs, fmt.Errorf("close deployment ingress %s: %w", run.ID, closeErr))
					continue
				}
			}
			if run.AnalysisWindow != nil && !validObservedHostWindow(
				run.AnalysisWindow.SourcePrefix, run.AnalysisWindow.Until, now, run.ExpiresAt, MaxAnalysisWindow,
			) {
				if _, closeErr := c.provider.CloseAnalysis(ctx, run.ID); closeErr != nil {
					result.Failed = append(result.Failed, run.ID)
					errs = append(errs, fmt.Errorf("close analysis ingress %s: %w", run.ID, closeErr))
				}
			}
			continue
		}
		if _, destroyErr := c.provider.Destroy(ctx, run.ID); destroyErr != nil {
			result.Failed = append(result.Failed, run.ID)
			errs = append(errs, fmt.Errorf("destroy %s: %w", run.ID, destroyErr))
			continue
		}
		result.Destroyed = append(result.Destroyed, run.ID)
	}
	return result, errors.Join(errs...)
}

// providerAuthority proves that the active credential is bound to the
// configured provider before lifecycle code interprets inventory or mutates it.
func (c *ControlPlane) providerAuthority(ctx context.Context) (ProviderAuthority, error) {
	if c == nil || c.provider == nil {
		return ProviderAuthority{}, ErrInvalidRequest
	}
	authority, err := c.provider.Authority(ctx)
	if err != nil {
		return ProviderAuthority{}, err
	}
	if authority.Provider != c.provider.Name() || strings.TrimSpace(authority.Region) == "" || strings.TrimSpace(authority.AccountIDHash) == "" {
		return ProviderAuthority{}, ErrInvalidRequest
	}
	return authority, nil
}

func (c *ControlPlane) validateCreateRequest(req CreateRequest) error {
	if c == nil || c.provider == nil {
		return ErrInvalidRequest
	}
	if strings.TrimSpace(req.RunID) == "" || strings.TrimSpace(req.Provider) == "" || req.Provider != c.provider.Name() ||
		strings.TrimSpace(req.Region) == "" || strings.TrimSpace(req.AccountIDHash) == "" || strings.TrimSpace(req.Repository) == "" ||
		strings.TrimSpace(req.SourceSHA) == "" || strings.TrimSpace(req.ScenarioDigest) == "" || strings.TrimSpace(req.DeploymentBundleDigest) == "" ||
		strings.TrimSpace(req.MCPCertificateFingerprint) == "" || strings.TrimSpace(req.Currency) == "" || req.MaxTotalCostMicros <= 0 ||
		!req.ExpiresAt.After(c.now()) || !validPreset(req.Preset) {
		return ErrInvalidRequest
	}
	minimum := req.ScenarioMinimumPreset
	if minimum == "" {
		minimum = PresetSmall
	}
	if !validPreset(minimum) {
		return ErrInvalidRequest
	}
	if presetRank(req.Preset) < presetRank(minimum) {
		return ErrPresetTooSmall
	}
	return nil
}

func mandatoryTags(req CreateRequest) map[string]string {
	return map[string]string{
		TagManagedBy:                 ManagedByValue,
		TagRunID:                     req.RunID,
		TagRepository:                req.Repository,
		TagResourceRole:              "run",
		TagSourceSHA:                 req.SourceSHA,
		TagScenarioDigest:            req.ScenarioDigest,
		TagBundleDigest:              req.DeploymentBundleDigest,
		TagExpiresAt:                 req.ExpiresAt.UTC().Format(time.RFC3339),
		TagMCPCertificateFingerprint: req.MCPCertificateFingerprint,
	}
}

func runIsActive(run Run, now time.Time) bool {
	return run.State != StateReleased && (run.ExpiresAt.IsZero() || run.ExpiresAt.After(now))
}

func validPreset(preset Preset) bool {
	return preset == PresetSmall || preset == PresetStandard || preset == PresetStress
}

func presetRank(preset Preset) int {
	switch preset {
	case PresetSmall:
		return 1
	case PresetStandard:
		return 2
	case PresetStress:
		return 3
	default:
		return 0
	}
}
