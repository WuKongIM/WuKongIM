package cloudsim

import (
	"context"
	"errors"
	"maps"
	"net/netip"
	"testing"
	"time"
)

func TestControlPlaneCreateRejectsCostBeforeProviderMutation(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	provider := &providerStub{
		quote: Quote{Currency: "CNY", WorstCaseCostMicros: 25_000_000, CapacityAvailable: true, QuotaAvailable: true},
	}
	control := NewControlPlane(provider, func() time.Time { return now })

	_, err := control.Create(context.Background(), validCreateRequest(now, 20_000_000))
	if !errors.Is(err, ErrCostLimitExceeded) {
		t.Fatalf("Create() error = %v, want ErrCostLimitExceeded", err)
	}
	if provider.createCalls != 0 {
		t.Fatalf("provider Create calls = %d, want 0", provider.createCalls)
	}
}

func TestControlPlaneCreateRejectsConcurrentRepositoryRun(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	provider := &providerStub{
		quote: Quote{Currency: "CNY", WorstCaseCostMicros: 5_000_000, CapacityAvailable: true, QuotaAvailable: true},
		inventory: []Run{{
			ID:            "run-active",
			Repository:    "WuKongIM/WuKongIM",
			AccountIDHash: "sha256:account",
			State:         StateRunning,
			ExpiresAt:     now.Add(time.Hour),
		}},
	}
	control := NewControlPlane(provider, func() time.Time { return now })

	_, err := control.Create(context.Background(), validCreateRequest(now, 20_000_000))
	if !errors.Is(err, ErrActiveRunExists) {
		t.Fatalf("Create() error = %v, want ErrActiveRunExists", err)
	}
	if provider.quoteCalls != 0 || provider.createCalls != 0 {
		t.Fatalf("provider calls quote=%d create=%d, want both 0", provider.quoteCalls, provider.createCalls)
	}
}

func TestControlPlaneCreateRequiresCapacityAndQuota(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		quote Quote
		want  error
	}{
		{name: "pricing unavailable", quote: Quote{CapacityAvailable: true, QuotaAvailable: true}, want: ErrPricingUnavailable},
		{name: "capacity unavailable", quote: Quote{Currency: "CNY", WorstCaseCostMicros: 1, QuotaAvailable: true}, want: ErrCapacityUnavailable},
		{name: "quota unavailable", quote: Quote{Currency: "CNY", WorstCaseCostMicros: 1, CapacityAvailable: true}, want: ErrQuotaUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &providerStub{quote: tt.quote}
			control := NewControlPlane(provider, func() time.Time { return now })
			_, err := control.Create(context.Background(), validCreateRequest(now, 20_000_000))
			if !errors.Is(err, tt.want) {
				t.Fatalf("Create() error = %v, want %v", err, tt.want)
			}
			if provider.createCalls != 0 {
				t.Fatalf("provider Create calls = %d, want 0", provider.createCalls)
			}
		})
	}
}

func TestControlPlaneCreateAddsMandatoryTags(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	provider := &providerStub{
		quote: Quote{Currency: "CNY", WorstCaseCostMicros: 5_000_000, CapacityAvailable: true, QuotaAvailable: true, SelectedSKU: "fake.small"},
	}
	control := NewControlPlane(provider, func() time.Time { return now })

	run, err := control.Create(context.Background(), validCreateRequest(now, 20_000_000))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	for _, key := range MandatoryTagKeys() {
		if provider.created.Tags[key] == "" {
			t.Errorf("mandatory tag %q is empty", key)
		}
	}
	if run.Quote.SelectedSKU != "fake.small" {
		t.Fatalf("selected SKU = %q, want fake.small", run.Quote.SelectedSKU)
	}
}

func TestControlPlaneOpenAnalysisRequiresIPv4HostPrefixAndLease(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	provider := &providerStub{status: Run{ID: "run-1", State: StateRunning, ExpiresAt: now.Add(time.Hour)}}
	control := NewControlPlane(provider, func() time.Time { return now })

	_, err := control.OpenAnalysis(context.Background(), OpenAnalysisRequest{
		RunID: "run-1", SourcePrefix: netip.MustParsePrefix("203.0.113.0/24"), Until: now.Add(30 * time.Minute),
	})
	if !errors.Is(err, ErrInvalidAnalysisWindow) {
		t.Fatalf("OpenAnalysis(/24) error = %v, want ErrInvalidAnalysisWindow", err)
	}
	_, err = control.OpenAnalysis(context.Background(), OpenAnalysisRequest{
		RunID: "run-1", SourcePrefix: netip.MustParsePrefix("203.0.113.8/32"), Until: now.Add(61 * time.Minute),
	})
	if !errors.Is(err, ErrInvalidAnalysisWindow) {
		t.Fatalf("OpenAnalysis(after lease) error = %v, want ErrInvalidAnalysisWindow", err)
	}
	if provider.openCalls != 0 {
		t.Fatalf("provider OpenAnalysis calls = %d, want 0", provider.openCalls)
	}
	provider.status.State = StateProvisioning
	_, err = control.OpenAnalysis(context.Background(), OpenAnalysisRequest{
		RunID: "run-1", SourcePrefix: netip.MustParsePrefix("203.0.113.8/32"), Until: now.Add(30 * time.Minute),
	})
	if !errors.Is(err, ErrInvalidAnalysisWindow) {
		t.Fatalf("OpenAnalysis(provisioning) error = %v, want ErrInvalidAnalysisWindow", err)
	}
}

func TestControlPlaneDeploymentAccessWindowIsBoundedAndSingleHost(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	provider := &providerStub{status: Run{ID: "run-1", State: StateProvisioning, ExpiresAt: now.Add(time.Hour)}}
	control := NewControlPlane(provider, func() time.Time { return now })

	opened, err := control.OpenDeployment(context.Background(), OpenDeploymentRequest{
		RunID: "run-1", SourcePrefix: netip.MustParsePrefix("203.0.113.8/32"), Until: now.Add(15 * time.Minute),
	})
	if err != nil {
		t.Fatalf("OpenDeployment() error = %v", err)
	}
	if opened.DeploymentWindow == nil || opened.DeploymentWindow.SourcePrefix.String() != "203.0.113.8/32" {
		t.Fatalf("deployment window = %#v, want exact /32", opened.DeploymentWindow)
	}

	_, err = control.OpenDeployment(context.Background(), OpenDeploymentRequest{
		RunID: "run-1", SourcePrefix: netip.MustParsePrefix("203.0.113.0/24"), Until: now.Add(15 * time.Minute),
	})
	if !errors.Is(err, ErrInvalidDeploymentWindow) {
		t.Fatalf("OpenDeployment(/24) error = %v, want ErrInvalidDeploymentWindow", err)
	}
	_, err = control.OpenDeployment(context.Background(), OpenDeploymentRequest{
		RunID: "run-1", SourcePrefix: netip.MustParsePrefix("203.0.113.8/32"), Until: now.Add(21 * time.Minute),
	})
	if !errors.Is(err, ErrInvalidDeploymentWindow) {
		t.Fatalf("OpenDeployment(21m) error = %v, want ErrInvalidDeploymentWindow", err)
	}

	closed, err := control.CloseDeployment(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("CloseDeployment() error = %v", err)
	}
	if closed.DeploymentWindow != nil {
		t.Fatalf("deployment window = %#v, want nil", closed.DeploymentWindow)
	}
}

func TestControlPlaneSweepDestroysExpiredRuns(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	provider := &providerStub{inventory: []Run{
		{ID: "expired", State: StateRunning, ExpiresAt: now.Add(-time.Second)},
		{ID: "live", State: StateRunning, ExpiresAt: now.Add(time.Hour)},
		{ID: "released", State: StateReleased, ExpiresAt: now.Add(-time.Hour)},
	}}
	control := NewControlPlane(provider, func() time.Time { return now })

	result, err := control.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if len(result.Destroyed) != 1 || result.Destroyed[0] != "expired" {
		t.Fatalf("destroyed = %v, want [expired]", result.Destroyed)
	}
	if len(provider.closeDeploymentCalls) != 1 || provider.closeDeploymentCalls[0] != "live" {
		t.Fatalf("closed deployment ingress = %v, want [live]", provider.closeDeploymentCalls)
	}
	if len(provider.closeAnalysisCalls) != 1 || provider.closeAnalysisCalls[0] != "live" {
		t.Fatalf("closed analysis ingress = %v, want [live]", provider.closeAnalysisCalls)
	}
}

func TestControlPlanePreflightDistinguishesLiveAndReleased(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	request := validCreateRequest(now, 20_000_000)
	provider := &providerStub{status: Run{
		ID: request.RunID, Provider: "fake", Region: request.Region, AccountIDHash: request.AccountIDHash,
		Repository: request.Repository, CreatedAt: now, ExpiresAt: request.ExpiresAt,
		Tags: map[string]string{TagSourceSHA: request.SourceSHA, TagScenarioDigest: request.ScenarioDigest},
	}}
	control := NewControlPlane(provider, func() time.Time { return now })
	locator := RunLocator{
		Schema: RunLocatorSchemaV1, RunID: request.RunID, Provider: "fake", Region: request.Region,
		AccountIDHash: request.AccountIDHash, Repository: request.Repository, SourceSHA: request.SourceSHA,
		ScenarioDigest: request.ScenarioDigest, CreatedAt: now, ExpiresAt: request.ExpiresAt, ProvisionWorkflowRunID: 1,
	}
	result, err := control.Preflight(context.Background(), locator)
	if err != nil || result.State != PreflightLive || result.Run == nil {
		t.Fatalf("Preflight(live) = %#v, %v", result, err)
	}
	provider.statusErr = ErrRunNotFound
	result, err = control.Preflight(context.Background(), locator)
	if err != nil || result.State != PreflightReleased || result.Run != nil {
		t.Fatalf("Preflight(released) = %#v, %v", result, err)
	}
}

func TestControlPlanePreflightRejectsMismatchedProviderAuthorityBeforeEmptyInventory(t *testing.T) {
	now := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	provider := &providerStub{
		authority: ProviderAuthority{Provider: "fake", Region: "other", AccountIDHash: "sha256:account"},
		statusErr: ErrRunNotFound,
	}
	control := NewControlPlane(provider, func() time.Time { return now })
	locator := RunLocator{
		Schema: RunLocatorSchemaV1, RunID: "run-1", Provider: "fake", Region: "local",
		AccountIDHash: "sha256:account", Repository: "WuKongIM/WuKongIM",
		SourceSHA: "0123456789012345678901234567890123456789", ScenarioDigest: "sha256:scenario",
		CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), ProvisionWorkflowRunID: 42,
	}
	if _, err := control.Preflight(context.Background(), locator); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Preflight() error = %v, want ErrInvalidRequest", err)
	}
}

func TestControlPlaneTransitionAllowsOnlyBoundedForwardSteps(t *testing.T) {
	now := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	provider := &providerStub{status: Run{ID: "run-1", State: StateProvisioning, ExpiresAt: now.Add(3 * time.Hour)}}
	control := NewControlPlane(provider, func() time.Time { return now })
	run, err := control.Transition(context.Background(), TransitionRequest{RunID: "run-1", Next: StateReady})
	if err != nil || run.State != StateReady {
		t.Fatalf("ready transition = %#v, %v", run, err)
	}
	activeUntil := now.Add(2 * time.Hour)
	run, err = control.Transition(context.Background(), TransitionRequest{RunID: "run-1", Next: StateRunning, ActiveUntil: activeUntil})
	if err != nil || run.State != StateRunning || !run.ActiveUntil.Equal(activeUntil) {
		t.Fatalf("running transition = %#v, %v", run, err)
	}
	if _, err := control.Transition(context.Background(), TransitionRequest{RunID: "run-1", Next: StateReady}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("backward transition error = %v, want ErrInvalidRequest", err)
	}
}

func validCreateRequest(now time.Time, maxCost int64) CreateRequest {
	return CreateRequest{
		RunID:                     "run-20260714-001",
		Provider:                  "fake",
		Region:                    "local",
		AccountIDHash:             "sha256:account",
		Repository:                "WuKongIM/WuKongIM",
		SourceSHA:                 "0123456789012345678901234567890123456789",
		ScenarioDigest:            "sha256:scenario",
		DeploymentBundleDigest:    "sha256:bundle",
		MCPCertificateFingerprint: "sha256:certificate",
		Preset:                    PresetSmall,
		ExpiresAt:                 now.Add(2 * time.Hour),
		MaxTotalCostMicros:        maxCost,
		Currency:                  "CNY",
	}
}

type providerStub struct {
	authority            ProviderAuthority
	authorityErr         error
	quote                Quote
	inventory            []Run
	status               Run
	statusErr            error
	created              CreateRequest
	quoteCalls           int
	createCalls          int
	openCalls            int
	closeDeploymentCalls []string
	closeAnalysisCalls   []string
	destroyed            []string
}

func (p *providerStub) Name() string { return "fake" }

func (p *providerStub) Authority(context.Context) (ProviderAuthority, error) {
	if p.authority.Provider == "" {
		p.authority = ProviderAuthority{Provider: "fake", Region: "local", AccountIDHash: "sha256:account"}
	}
	return p.authority, p.authorityErr
}

func (p *providerStub) Inventory(context.Context) ([]Run, error) {
	return append([]Run(nil), p.inventory...), nil
}

func (p *providerStub) Quote(context.Context, CreateRequest) (Quote, error) {
	p.quoteCalls++
	return p.quote, nil
}

func (p *providerStub) Create(_ context.Context, req CreateRequest, quote Quote) (Run, error) {
	p.createCalls++
	p.created = req
	p.status = Run{
		ID: req.RunID, Provider: req.Provider, Region: req.Region, AccountIDHash: req.AccountIDHash,
		Repository: req.Repository, State: StateReady, ExpiresAt: req.ExpiresAt, Tags: maps.Clone(req.Tags), Quote: quote,
	}
	return p.status, nil
}

func (p *providerStub) Status(context.Context, string) (Run, error) { return p.status, p.statusErr }

func (p *providerStub) Transition(_ context.Context, req TransitionRequest) (Run, error) {
	p.status.State = req.Next
	p.status.ActiveUntil = req.ActiveUntil
	return p.status, nil
}

func (p *providerStub) OpenDeployment(_ context.Context, req OpenDeploymentRequest) (Run, error) {
	p.status.DeploymentWindow = &DeploymentWindow{SourcePrefix: req.SourcePrefix, Until: req.Until}
	return p.status, nil
}

func (p *providerStub) CloseDeployment(_ context.Context, runID string) (Run, error) {
	p.closeDeploymentCalls = append(p.closeDeploymentCalls, runID)
	p.status.DeploymentWindow = nil
	return p.status, nil
}

func (p *providerStub) OpenAnalysis(_ context.Context, req OpenAnalysisRequest) (Run, error) {
	p.openCalls++
	p.status.AnalysisWindow = &AnalysisWindow{SourcePrefix: req.SourcePrefix, Until: req.Until}
	return p.status, nil
}

func (p *providerStub) CloseAnalysis(_ context.Context, runID string) (Run, error) {
	p.closeAnalysisCalls = append(p.closeAnalysisCalls, runID)
	p.status.AnalysisWindow = nil
	return p.status, nil
}

func (p *providerStub) Destroy(_ context.Context, runID string) (Run, error) {
	p.destroyed = append(p.destroyed, runID)
	return Run{ID: runID, State: StateReleased}, nil
}
