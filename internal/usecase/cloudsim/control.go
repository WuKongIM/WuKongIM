package cloudsim

import (
	"context"
	"errors"
	"fmt"
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

// OpenAnalysis validates a single-host bounded ingress window before mutation.
func (c *ControlPlane) OpenAnalysis(ctx context.Context, req OpenAnalysisRequest) (Run, error) {
	if c == nil || c.provider == nil || strings.TrimSpace(req.RunID) == "" {
		return Run{}, ErrInvalidAnalysisWindow
	}
	now := c.now()
	if !req.SourcePrefix.IsValid() || !req.SourcePrefix.Addr().Is4() || req.SourcePrefix.Bits() != 32 || req.SourcePrefix != req.SourcePrefix.Masked() {
		return Run{}, ErrInvalidAnalysisWindow
	}
	if !req.Until.After(now) || req.Until.After(now.Add(MaxAnalysisWindow)) {
		return Run{}, ErrInvalidAnalysisWindow
	}
	run, err := c.provider.Status(ctx, req.RunID)
	if err != nil {
		return Run{}, err
	}
	if run.State == StateReleased {
		return Run{}, ErrRunReleased
	}
	if run.ExpiresAt.Sub(now) < MinAnalysisLeaseRemaining || req.Until.After(run.ExpiresAt) {
		return Run{}, ErrInvalidAnalysisWindow
	}
	return c.provider.OpenAnalysis(ctx, req)
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
	return c.provider.Destroy(ctx, runID)
}

// Sweep destroys every unreleased run whose immutable Run Lease expired.
func (c *ControlPlane) Sweep(ctx context.Context) (SweepResult, error) {
	if c == nil || c.provider == nil {
		return SweepResult{}, ErrInvalidRequest
	}
	runs, err := c.provider.Inventory(ctx)
	if err != nil {
		return SweepResult{}, err
	}
	result := SweepResult{Destroyed: make([]string, 0), Failed: make([]string, 0)}
	errs := make([]error, 0)
	now := c.now()
	for _, run := range runs {
		if run.State == StateReleased || run.ExpiresAt.IsZero() || run.ExpiresAt.After(now) {
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
