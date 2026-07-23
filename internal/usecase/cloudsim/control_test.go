package cloudsim

import (
	"context"
	"errors"
	"maps"
	"net/netip"
	"slices"
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

func TestControlPlaneInventorySnapshotBindsAuthorityBeforeInventory(t *testing.T) {
	authority := ProviderAuthority{Provider: "fake", Region: "local", AccountIDHash: "sha256:account"}
	provider := &providerStub{
		authority: authority,
		inventory: []Run{{
			ID: "run-1", Provider: authority.Provider, Region: authority.Region, AccountIDHash: authority.AccountIDHash,
		}},
	}
	control := NewControlPlane(provider, time.Now)

	snapshot, err := control.InventorySnapshot(context.Background())
	if err != nil {
		t.Fatalf("InventorySnapshot() error = %v", err)
	}
	if snapshot.Authority != authority || len(snapshot.Runs) != 1 || snapshot.Runs[0].ID != "run-1" {
		t.Fatalf("InventorySnapshot() = %#v", snapshot)
	}
	if want := []string{"authority", "inventory"}; !slices.Equal(provider.discoveryCalls, want) {
		t.Fatalf("provider discovery calls = %v, want %v", provider.discoveryCalls, want)
	}
}

func TestControlPlaneInventorySnapshotRejectsDuplicateAndMismatchedRuns(t *testing.T) {
	authority := ProviderAuthority{Provider: "fake", Region: "local", AccountIDHash: "sha256:account"}
	valid := Run{ID: "run-1", Provider: authority.Provider, Region: authority.Region, AccountIDHash: authority.AccountIDHash}
	tests := []struct {
		name string
		runs []Run
	}{
		{name: "empty run id", runs: []Run{{Provider: authority.Provider, Region: authority.Region, AccountIDHash: authority.AccountIDHash}}},
		{name: "duplicate run id", runs: []Run{valid, valid}},
		{name: "provider mismatch", runs: []Run{{ID: "run-1", Provider: "other", Region: authority.Region, AccountIDHash: authority.AccountIDHash}}},
		{name: "region mismatch", runs: []Run{{ID: "run-1", Provider: authority.Provider, Region: "other", AccountIDHash: authority.AccountIDHash}}},
		{name: "account mismatch", runs: []Run{{ID: "run-1", Provider: authority.Provider, Region: authority.Region, AccountIDHash: "sha256:other"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &providerStub{authority: authority, inventory: test.runs}
			control := NewControlPlane(provider, time.Now)

			if _, err := control.InventorySnapshot(context.Background()); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("InventorySnapshot() error = %v, want ErrInvalidRequest", err)
			}
			if want := []string{"authority", "inventory"}; !slices.Equal(provider.discoveryCalls, want) {
				t.Fatalf("provider discovery calls = %v, want %v", provider.discoveryCalls, want)
			}
		})
	}
}

func TestControlPlaneInventorySnapshotStopsAfterAuthorityFailure(t *testing.T) {
	authorityErr := errors.New("authority unavailable")
	provider := &providerStub{authorityErr: authorityErr}
	control := NewControlPlane(provider, time.Now)

	if _, err := control.InventorySnapshot(context.Background()); !errors.Is(err, authorityErr) {
		t.Fatalf("InventorySnapshot() error = %v, want authority error", err)
	}
	if want := []string{"authority"}; !slices.Equal(provider.discoveryCalls, want) {
		t.Fatalf("provider discovery calls = %v, want %v", provider.discoveryCalls, want)
	}
}

func TestControlPlaneCleanupRejectsMismatchedProviderAuthority(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	authorityErr := errors.New("provider account does not match retained config")

	tests := []struct {
		name string
		run  func(*ControlPlane) error
	}{
		{
			name: "destroy",
			run: func(control *ControlPlane) error {
				_, err := control.Destroy(context.Background(), "run-1")
				return err
			},
		},
		{
			name: "sweep",
			run: func(control *ControlPlane) error {
				_, err := control.Sweep(context.Background())
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &providerStub{
				authorityErr: authorityErr,
				inventory:    []Run{{ID: "run-1", State: StateRunning, ExpiresAt: now.Add(-time.Second)}},
			}
			control := NewControlPlane(provider, func() time.Time { return now })

			if err := test.run(control); !errors.Is(err, authorityErr) {
				t.Fatalf("operation error = %v, want authority error", err)
			}
			if len(provider.destroyed) != 0 {
				t.Fatalf("provider mutated after authority failure: destroy=%v", provider.destroyed)
			}
		})
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
	provider.status.State = StateRunning
	provider.status.AnalysisWindow = &AnalysisWindow{
		SourcePrefix: netip.MustParsePrefix("203.0.113.9/32"), Until: now.Add(20 * time.Minute),
	}
	_, err = control.OpenAnalysis(context.Background(), OpenAnalysisRequest{
		RunID: "run-1", SourcePrefix: netip.MustParsePrefix("203.0.113.8/32"), Until: now.Add(30 * time.Minute),
	})
	if !errors.Is(err, ErrInvalidAnalysisWindow) {
		t.Fatalf("OpenAnalysis(concurrent window) error = %v, want ErrInvalidAnalysisWindow", err)
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

func TestControlPlaneObservationAccessRequiresPublicIPv4AndLiveRunLease(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	provider := &providerStub{status: Run{ID: "run-1", State: StateReady, ExpiresAt: now.Add(2 * time.Hour)}}
	control := NewControlPlane(provider, func() time.Time { return now })

	opened, err := control.OpenPublicView(context.Background(), OpenPublicViewRequest{
		RunID: "run-1", SourcePrefix: netip.MustParsePrefix("0.0.0.0/0"), Until: now.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("OpenPublicView() error = %v", err)
	}
	if opened.PublicViewWindow == nil || opened.PublicViewWindow.SourcePrefix.String() != "0.0.0.0/0" {
		t.Fatalf("public view window = %#v, want public IPv4 /0", opened.PublicViewWindow)
	}

	for _, source := range []string{"203.0.113.8/32", "::/0"} {
		_, err = control.OpenPublicView(context.Background(), OpenPublicViewRequest{
			RunID: "run-1", SourcePrefix: netip.MustParsePrefix(source), Until: now.Add(time.Hour),
		})
		if !errors.Is(err, ErrInvalidPublicViewWindow) {
			t.Fatalf("OpenPublicView(%s) error = %v, want ErrInvalidPublicViewWindow", source, err)
		}
	}
	_, err = control.OpenPublicView(context.Background(), OpenPublicViewRequest{
		RunID: "run-1", SourcePrefix: netip.MustParsePrefix("0.0.0.0/0"), Until: now.Add(2*time.Hour + time.Second),
	})
	if !errors.Is(err, ErrInvalidPublicViewWindow) {
		t.Fatalf("OpenPublicView(after lease) error = %v, want ErrInvalidPublicViewWindow", err)
	}
	provider.status.State = StateProvisioning
	_, err = control.OpenPublicView(context.Background(), OpenPublicViewRequest{
		RunID: "run-1", SourcePrefix: netip.MustParsePrefix("0.0.0.0/0"), Until: now.Add(time.Hour),
	})
	if !errors.Is(err, ErrInvalidPublicViewWindow) {
		t.Fatalf("OpenPublicView(provisioning) error = %v, want ErrInvalidPublicViewWindow", err)
	}

	closed, err := control.ClosePublicView(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("ClosePublicView() error = %v", err)
	}
	if closed.PublicViewWindow != nil {
		t.Fatalf("public view window = %#v, want nil", closed.PublicViewWindow)
	}
}

func TestControlPlaneSweepDestroysExpiredRuns(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	provider := &providerStub{inventory: []Run{
		{ID: "expired", State: StateRunning, ExpiresAt: now.Add(-time.Second)},
		{ID: "stale-window", State: StateRunning, ExpiresAt: now.Add(time.Hour),
			DeploymentWindow: &DeploymentWindow{Until: now.Add(-time.Second)},
			AnalysisWindow:   &AnalysisWindow{Until: now.Add(-time.Second)},
			PublicViewWindow: &PublicViewWindow{SourcePrefix: netip.MustParsePrefix("0.0.0.0/0"), Until: now.Add(-time.Second)}},
		{ID: "active-window", State: StateRunning, ExpiresAt: now.Add(time.Hour),
			DeploymentWindow: &DeploymentWindow{SourcePrefix: netip.MustParsePrefix("203.0.113.8/32"), Until: now.Add(time.Minute)},
			AnalysisWindow:   &AnalysisWindow{SourcePrefix: netip.MustParsePrefix("198.51.100.8/32"), Until: now.Add(30 * time.Minute)}},
		{ID: "malformed-window", State: StateRunning, ExpiresAt: now.Add(2 * time.Hour),
			DeploymentWindow: &DeploymentWindow{SourcePrefix: netip.MustParsePrefix("203.0.113.0/24"), Until: now.Add(time.Minute)},
			AnalysisWindow:   &AnalysisWindow{SourcePrefix: netip.MustParsePrefix("198.51.100.8/32"), Until: now.Add(MaxAnalysisWindow + time.Minute)}},
		{ID: "window-beyond-lease", State: StateRunning, ExpiresAt: now.Add(10 * time.Minute),
			DeploymentWindow: &DeploymentWindow{SourcePrefix: netip.MustParsePrefix("203.0.113.8/32"), Until: now.Add(15 * time.Minute)}},
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
	if want := []string{"stale-window", "malformed-window", "window-beyond-lease"}; !slices.Equal(provider.closeDeploymentCalls, want) {
		t.Fatalf("closed deployment ingress = %v, want %v", provider.closeDeploymentCalls, want)
	}
	if want := []string{"stale-window", "malformed-window"}; !slices.Equal(provider.closeAnalysisCalls, want) {
		t.Fatalf("closed analysis ingress = %v, want %v", provider.closeAnalysisCalls, want)
	}
	if want := []string{"stale-window"}; !slices.Equal(provider.closeObservationCalls, want) {
		t.Fatalf("closed observation ingress = %v, want %v", provider.closeObservationCalls, want)
	}
}

func TestControlPlanePreflightDistinguishesLiveAndReleased(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	request := validCreateRequest(now, 20_000_000)
	provider := &providerStub{status: Run{
		ID: request.RunID, Provider: "fake", Region: request.Region, AccountIDHash: request.AccountIDHash,
		Repository: request.Repository, CreatedAt: now, ExpiresAt: request.ExpiresAt,
		Tags:      map[string]string{TagSourceSHA: request.SourceSHA, TagScenarioDigest: request.ScenarioDigest},
		Resources: []Resource{{ID: "i-sim", Kind: "instance", Role: "sim", Tags: map[string]string{}}},
	}}
	control := NewControlPlane(provider, func() time.Time { return now })
	locator := RunLocator{
		Schema: RunLocatorSchemaV1, RunID: request.RunID, Provider: "fake", Region: request.Region,
		AccountIDHash: request.AccountIDHash, Repository: request.Repository, SourceSHA: request.SourceSHA,
		ScenarioDigest: request.ScenarioDigest, CreatedAt: now, ExpiresAt: request.ExpiresAt, ProvisionWorkflowRunID: 1,
	}
	result, err := control.Preflight(context.Background(), locator)
	if err != nil || result.State != PreflightLive || result.Run == nil || len(result.Resources) != 1 {
		t.Fatalf("Preflight(live) = %#v, %v", result, err)
	}
	provider.statusErr = ErrRunNotFound
	result, err = control.Preflight(context.Background(), locator)
	if err != nil || result.State != PreflightReleased || result.Run != nil || result.Resources == nil || len(result.Resources) != 0 {
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
	authority             ProviderAuthority
	authorityErr          error
	quote                 Quote
	inventory             []Run
	status                Run
	statusErr             error
	created               CreateRequest
	quoteCalls            int
	createCalls           int
	openCalls             int
	closeDeploymentCalls  []string
	closeAnalysisCalls    []string
	closeObservationCalls []string
	destroyed             []string
	discoveryCalls        []string
}

func (p *providerStub) Name() string { return "fake" }

func (p *providerStub) Authority(context.Context) (ProviderAuthority, error) {
	p.discoveryCalls = append(p.discoveryCalls, "authority")
	if p.authority.Provider == "" {
		p.authority = ProviderAuthority{Provider: "fake", Region: "local", AccountIDHash: "sha256:account"}
	}
	return p.authority, p.authorityErr
}

func (p *providerStub) Inventory(context.Context) ([]Run, error) {
	p.discoveryCalls = append(p.discoveryCalls, "inventory")
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

func (p *providerStub) OpenPublicView(_ context.Context, req OpenPublicViewRequest) (Run, error) {
	p.status.PublicViewWindow = &PublicViewWindow{SourcePrefix: req.SourcePrefix, Until: req.Until}
	return p.status, nil
}

func (p *providerStub) ClosePublicView(_ context.Context, runID string) (Run, error) {
	p.closeObservationCalls = append(p.closeObservationCalls, runID)
	p.status.PublicViewWindow = nil
	return p.status, nil
}

func (p *providerStub) Destroy(_ context.Context, runID string) (Run, error) {
	p.destroyed = append(p.destroyed, runID)
	return Run{ID: runID, State: StateReleased}, nil
}
