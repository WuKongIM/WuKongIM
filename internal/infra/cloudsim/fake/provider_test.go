package fake

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
)

func TestProviderCreateBuildsExpectedIsolatedInventory(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	provider := New(Options{Now: func() time.Time { return now }})
	req := fakeCreateRequest(now)
	quote, err := provider.Quote(context.Background(), req)
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}
	run, err := provider.Create(context.Background(), req, quote)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if run.State != cloudsim.StateReady {
		t.Fatalf("state = %q, want ready", run.State)
	}

	wantRoles := map[string]bool{
		"run-network": false,
		"node-1":      false,
		"node-2":      false,
		"node-3":      false,
		"sim":         false,
	}
	compute := 0
	disks := 0
	for _, resource := range run.Resources {
		if resource.Kind == "compute" {
			compute++
			wantRoles[resource.Role] = true
		}
		if resource.Kind == "disk" {
			disks++
		}
		if resource.Kind == "vpc" {
			wantRoles[resource.Role] = true
		}
		for _, key := range cloudsim.MandatoryTagKeys() {
			if resource.Tags[key] == "" {
				t.Errorf("resource %s mandatory tag %q is empty", resource.ID, key)
			}
		}
		if resource.Tags[cloudsim.TagResourceRole] != resource.Role {
			t.Errorf("resource %s role tag = %q, want %q", resource.ID, resource.Tags[cloudsim.TagResourceRole], resource.Role)
		}
	}
	if compute != 4 || disks != 4 {
		t.Fatalf("compute=%d disks=%d, want 4 and 4", compute, disks)
	}
	for role, seen := range wantRoles {
		if !seen {
			t.Errorf("missing resource role %q", role)
		}
	}
}

func TestProviderDestroyProvesReleasedWithEmptyInventory(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	provider := New(Options{Now: func() time.Time { return now }})
	req := fakeCreateRequest(now)
	quote, _ := provider.Quote(context.Background(), req)
	if _, err := provider.Create(context.Background(), req, quote); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	run, err := provider.Destroy(context.Background(), req.RunID)
	if err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
	if run.State != cloudsim.StateReleased || len(run.Resources) != 0 {
		t.Fatalf("destroyed run = %#v, want released with empty resources", run)
	}
	status, err := provider.Status(context.Background(), req.RunID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.State != cloudsim.StateReleased || len(status.Resources) != 0 {
		t.Fatalf("status = %#v, want released with empty resources", status)
	}
}

func TestProviderCreateFailureLeavesDiscoverablePartialInventory(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	provider := New(Options{
		Now:      func() time.Time { return now },
		Failures: FailurePlan{CreateAfterResources: 3},
	})
	req := fakeCreateRequest(now)
	quote, _ := provider.Quote(context.Background(), req)
	_, err := provider.Create(context.Background(), req, quote)
	if !errors.Is(err, ErrInjectedFailure) {
		t.Fatalf("Create() error = %v, want ErrInjectedFailure", err)
	}
	run, err := provider.Status(context.Background(), req.RunID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if run.State != cloudsim.StateReleasePending || len(run.Resources) != 3 {
		t.Fatalf("partial run state=%q resources=%d, want release_pending and 3", run.State, len(run.Resources))
	}
}

func TestProviderOpenAnalysisDoesNotMutateOtherRuns(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	provider := New(Options{Now: func() time.Time { return now }})
	first := fakeCreateRequest(now)
	second := fakeCreateRequest(now)
	second.RunID = "run-2"
	for _, req := range []cloudsim.CreateRequest{first, second} {
		quote, _ := provider.Quote(context.Background(), req)
		if _, err := provider.Create(context.Background(), req, quote); err != nil {
			t.Fatalf("Create(%s) error = %v", req.RunID, err)
		}
	}
	_, err := provider.OpenAnalysis(context.Background(), cloudsim.OpenAnalysisRequest{
		RunID: first.RunID,
		Until: now.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("OpenAnalysis() error = %v", err)
	}
	other, _ := provider.Status(context.Background(), second.RunID)
	if other.AnalysisWindow != nil {
		t.Fatalf("other run analysis window = %#v, want nil", other.AnalysisWindow)
	}
}

func TestOpenPersistsFakeProviderInventoryAcrossProcesses(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	statePath := t.TempDir() + "/inventory.json"
	provider, err := Open(Options{Now: func() time.Time { return now }, StatePath: statePath})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	req := fakeCreateRequest(now)
	quote, _ := provider.Quote(context.Background(), req)
	if _, err := provider.Create(context.Background(), req, quote); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	reopened, err := Open(Options{Now: func() time.Time { return now }, StatePath: statePath})
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	run, err := reopened.Status(context.Background(), req.RunID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if run.State != cloudsim.StateReady || len(run.Resources) != 12 {
		t.Fatalf("reopened run state=%q resources=%d", run.State, len(run.Resources))
	}
}

func fakeCreateRequest(now time.Time) cloudsim.CreateRequest {
	return cloudsim.CreateRequest{
		RunID: "run-1", Provider: ProviderName, Region: "local", AccountIDHash: "sha256:account",
		Repository: "WuKongIM/WuKongIM", SourceSHA: "0123456789012345678901234567890123456789",
		ScenarioDigest: "sha256:scenario", DeploymentBundleDigest: "sha256:bundle",
		MCPCertificateFingerprint: "sha256:certificate", Preset: cloudsim.PresetSmall,
		ExpiresAt: now.Add(2 * time.Hour), MaxTotalCostMicros: 20_000_000, Currency: "CNY",
		Tags: map[string]string{
			cloudsim.TagManagedBy: cloudsim.ManagedByValue, cloudsim.TagRunID: "run-1",
			cloudsim.TagRepository: "WuKongIM/WuKongIM", cloudsim.TagResourceRole: "run",
			cloudsim.TagSourceSHA:      "0123456789012345678901234567890123456789",
			cloudsim.TagScenarioDigest: "sha256:scenario", cloudsim.TagBundleDigest: "sha256:bundle",
			cloudsim.TagExpiresAt:                 now.Add(2 * time.Hour).UTC().Format(time.RFC3339),
			cloudsim.TagMCPCertificateFingerprint: "sha256:certificate",
		},
	}
}
