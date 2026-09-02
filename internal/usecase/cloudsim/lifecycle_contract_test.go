package cloudsim

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestControlPlaneStatusAndCloseAnalysisPreserveExactProviderResult(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	provider := &providerStub{status: Run{
		ID: "run-1", State: StateRunning, ExpiresAt: now.Add(time.Hour),
		AnalysisWindow: &AnalysisWindow{
			SourcePrefix: netip.MustParsePrefix("203.0.113.8/32"), Until: now.Add(20 * time.Minute),
		},
	}}
	control := NewControlPlane(provider, func() time.Time { return now })

	status, err := control.Status(context.Background(), "run-1")
	if err != nil || status.ID != "run-1" {
		t.Fatalf("Status() = (%#v, %v), want exact provider run", status, err)
	}
	closed, err := control.CloseAnalysis(context.Background(), "run-1")
	if err != nil || closed.AnalysisWindow != nil {
		t.Fatalf("CloseAnalysis() = (%#v, %v), want closed window", closed, err)
	}
	if want := []string{"run-1"}; !slices.Equal(provider.statusCalls, want) {
		t.Fatalf("Status calls = %v, want %v", provider.statusCalls, want)
	}
	if want := []string{"run-1"}; !slices.Equal(provider.closeAnalysisCalls, want) {
		t.Fatalf("CloseAnalysis calls = %v, want %v", provider.closeAnalysisCalls, want)
	}

	if _, err := control.Status(context.Background(), " "); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Status(blank) error = %v, want ErrInvalidRequest", err)
	}
	if _, err := control.CloseAnalysis(context.Background(), " "); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("CloseAnalysis(blank) error = %v, want ErrInvalidRequest", err)
	}
	if len(provider.statusCalls) != 1 || len(provider.closeAnalysisCalls) != 1 {
		t.Fatalf("invalid identities reached provider: status=%v close=%v", provider.statusCalls, provider.closeAnalysisCalls)
	}

	statusFailure := errors.New("status unavailable")
	provider.statusErr = statusFailure
	if _, err := control.Status(context.Background(), "run-2"); !errors.Is(err, statusFailure) {
		t.Fatalf("Status(provider error) = %v, want provider error", err)
	}
	closeFailure := errors.New("security rule unavailable")
	provider.closeAnalysisErrors = map[string]error{"run-2": closeFailure}
	if _, err := control.CloseAnalysis(context.Background(), "run-2"); !errors.Is(err, closeFailure) {
		t.Fatalf("CloseAnalysis(provider error) = %v, want provider error", err)
	}
}

func TestControlPlaneCreateStopsAtFailedProviderBoundary(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	request := validCreateRequest(now, 20_000_000)
	quote := Quote{Currency: "CNY", WorstCaseCostMicros: 5_000_000, CapacityAvailable: true, QuotaAvailable: true}

	t.Run("inventory failure prevents quote and mutation", func(t *testing.T) {
		failure := errors.New("inventory unavailable")
		provider := &providerStub{inventoryErr: failure, quote: quote}
		_, err := NewControlPlane(provider, func() time.Time { return now }).Create(context.Background(), request)
		if err == nil || !strings.Contains(err.Error(), failure.Error()) {
			t.Fatalf("Create() error = %v, want inventory failure", err)
		}
		if provider.quoteCalls != 0 || provider.createCalls != 0 {
			t.Fatalf("provider calls after inventory failure: quote=%d create=%d", provider.quoteCalls, provider.createCalls)
		}
	})

	t.Run("quote failure prevents mutation", func(t *testing.T) {
		failure := errors.New("pricing unavailable")
		provider := &providerStub{quoteErr: failure}
		_, err := NewControlPlane(provider, func() time.Time { return now }).Create(context.Background(), request)
		if !errors.Is(err, ErrPricingUnavailable) || !strings.Contains(err.Error(), failure.Error()) {
			t.Fatalf("Create() error = %v, want pricing failure", err)
		}
		if provider.quoteCalls != 1 || provider.createCalls != 0 {
			t.Fatalf("provider calls after quote failure: quote=%d create=%d", provider.quoteCalls, provider.createCalls)
		}
	})

	t.Run("create failure is returned without a synthetic run", func(t *testing.T) {
		failure := errors.New("create request rejected")
		provider := &providerStub{quote: quote, createErr: failure}
		run, err := NewControlPlane(provider, func() time.Time { return now }).Create(context.Background(), request)
		if !errors.Is(err, failure) || run.ID != "" {
			t.Fatalf("Create() = (%#v, %v), want provider create failure", run, err)
		}
		if provider.createCalls != 1 || provider.created.RunID != "" {
			t.Fatalf("failed provider creation retained accepted request: calls=%d request=%#v", provider.createCalls, provider.created)
		}
	})
}

func TestControlPlaneDestroyChecksAuthorityBeforeExactMutation(t *testing.T) {
	provider := &providerStub{}
	control := NewControlPlane(provider, time.Now)
	run, err := control.Destroy(context.Background(), "run-1")
	if err != nil || run.State != StateReleased {
		t.Fatalf("Destroy() = (%#v, %v), want provider-released run", run, err)
	}
	if want := []string{"authority"}; !slices.Equal(provider.discoveryCalls, want) {
		t.Fatalf("Destroy() discovery calls = %v, want %v", provider.discoveryCalls, want)
	}
	if want := []string{"run-1"}; !slices.Equal(provider.destroyed, want) {
		t.Fatalf("Destroy() mutations = %v, want %v", provider.destroyed, want)
	}
}

func TestControlPlaneSweepRetainsFailuresAndContinuesReconciliation(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	publicFailure := errors.New("close public ingress")
	deploymentFailure := errors.New("close deployment ingress")
	analysisFailure := errors.New("close analysis ingress")
	destroyFailure := errors.New("destroy resources")
	provider := &providerStub{
		inventory: []Run{
			{ID: "released-residual", State: StateReleased, ExpiresAt: now.Add(-time.Hour), Resources: []Resource{{ID: "i-left"}}},
			{ID: "unknown-expiry", State: StateRunning},
			{ID: "bad-public", State: StateRunning, ExpiresAt: now.Add(time.Hour),
				PublicViewWindow: &PublicViewWindow{SourcePrefix: netip.MustParsePrefix("0.0.0.0/0"), Until: now.Add(-time.Second)},
				DeploymentWindow: &DeploymentWindow{Until: now.Add(-time.Second)}},
			{ID: "bad-deployment", State: StateRunning, ExpiresAt: now.Add(time.Hour),
				DeploymentWindow: &DeploymentWindow{Until: now.Add(-time.Second)},
				AnalysisWindow:   &AnalysisWindow{Until: now.Add(-time.Second)}},
			{ID: "bad-analysis", State: StateRunning, ExpiresAt: now.Add(time.Hour),
				AnalysisWindow: &AnalysisWindow{Until: now.Add(-time.Second)}},
			{ID: "expired-failed", State: StateRunning, ExpiresAt: now.Add(-time.Second)},
			{ID: "expired-destroyed", State: StateRunning, ExpiresAt: now.Add(-time.Second)},
		},
		closeObservationErrors: map[string]error{"bad-public": publicFailure},
		closeDeploymentErrors:  map[string]error{"bad-deployment": deploymentFailure},
		closeAnalysisErrors:    map[string]error{"bad-analysis": analysisFailure},
		destroyErrors:          map[string]error{"expired-failed": destroyFailure},
	}
	control := NewControlPlane(provider, func() time.Time { return now })

	result, err := control.Sweep(context.Background())
	for _, failure := range []error{publicFailure, deploymentFailure, analysisFailure, destroyFailure} {
		if !errors.Is(err, failure) {
			t.Errorf("Sweep() error = %v, want joined %v", err, failure)
		}
	}
	wantRetained := []string{"released-residual", "unknown-expiry", "bad-public", "bad-deployment", "bad-analysis", "expired-failed"}
	if !slices.Equal(result.Retained, wantRetained) {
		t.Fatalf("Sweep().Retained = %v, want %v", result.Retained, wantRetained)
	}
	wantFailed := []string{"bad-public", "bad-deployment", "bad-analysis", "expired-failed"}
	if !slices.Equal(result.Failed, wantFailed) {
		t.Fatalf("Sweep().Failed = %v, want %v", result.Failed, wantFailed)
	}
	if want := []string{"expired-destroyed"}; !slices.Equal(result.Destroyed, want) {
		t.Fatalf("Sweep().Destroyed = %v, want %v", result.Destroyed, want)
	}
	if slices.Contains(provider.closeDeploymentCalls, "bad-public") || slices.Contains(provider.closeAnalysisCalls, "bad-deployment") {
		t.Fatalf("Sweep() continued mutating a run after an earlier close failure: deployment=%v analysis=%v",
			provider.closeDeploymentCalls, provider.closeAnalysisCalls)
	}
}
