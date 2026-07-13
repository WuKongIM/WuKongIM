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
	quote       Quote
	inventory   []Run
	status      Run
	created     CreateRequest
	quoteCalls  int
	createCalls int
	openCalls   int
	destroyed   []string
}

func (p *providerStub) Name() string { return "fake" }

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

func (p *providerStub) Status(context.Context, string) (Run, error) { return p.status, nil }

func (p *providerStub) OpenAnalysis(_ context.Context, req OpenAnalysisRequest) (Run, error) {
	p.openCalls++
	p.status.AnalysisWindow = &AnalysisWindow{SourcePrefix: req.SourcePrefix, Until: req.Until}
	return p.status, nil
}

func (p *providerStub) CloseAnalysis(context.Context, string) (Run, error) {
	p.status.AnalysisWindow = nil
	return p.status, nil
}

func (p *providerStub) Destroy(_ context.Context, runID string) (Run, error) {
	p.destroyed = append(p.destroyed, runID)
	return Run{ID: runID, State: StateReleased}, nil
}
