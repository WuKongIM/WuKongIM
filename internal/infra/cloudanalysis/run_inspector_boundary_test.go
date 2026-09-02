package cloudanalysis

import (
	"context"
	"errors"
	"testing"
	"time"

	analysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
)

func TestProviderRunInspectorFailsClosedWithoutExactInventoryProof(t *testing.T) {
	inspector, source := validProviderRunInspectorFixture()

	t.Run("missing inventory source", func(t *testing.T) {
		missing := inspector
		missing.Source = nil
		if _, err := missing.InspectRun(context.Background(), "run-1"); !errors.Is(err, ErrInvalidHTTPConfig) {
			t.Fatalf("InspectRun() error = %v, want ErrInvalidHTTPConfig", err)
		}
	})

	t.Run("invalid locator", func(t *testing.T) {
		invalid := inspector
		invalid.Locator.Schema = "unsupported"
		if _, err := invalid.InspectRun(context.Background(), "run-1"); !errors.Is(err, analysis.ErrRunIdentityMismatch) {
			t.Fatalf("InspectRun() error = %v, want ErrRunIdentityMismatch", err)
		}
		if source.calls != 0 {
			t.Fatalf("inventory source calls = %d before locator validation", source.calls)
		}
	})

	t.Run("requested identity mismatch", func(t *testing.T) {
		if _, err := inspector.InspectRun(context.Background(), "run-other"); !errors.Is(err, analysis.ErrRunIdentityMismatch) {
			t.Fatalf("InspectRun() error = %v, want ErrRunIdentityMismatch", err)
		}
	})

	t.Run("inventory error", func(t *testing.T) {
		failed := inspector
		wantErr := errors.New("provider inventory unavailable")
		failed.Source = &recordingRunStatusSource{err: wantErr}
		if _, err := failed.InspectRun(context.Background(), "run-1"); !errors.Is(err, wantErr) {
			t.Fatalf("InspectRun() error = %v, want wrapped inventory error", err)
		}
	})
}

func TestProviderRunInspectorRejectsEveryMismatchedIdentityDimension(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ProviderRunInspector, *cloudsim.Run)
	}{
		{name: "run id", mutate: func(_ *ProviderRunInspector, run *cloudsim.Run) { run.ID = "run-other" }},
		{name: "provider", mutate: func(_ *ProviderRunInspector, run *cloudsim.Run) { run.Provider = "other" }},
		{name: "region", mutate: func(_ *ProviderRunInspector, run *cloudsim.Run) { run.Region = "other" }},
		{name: "account", mutate: func(_ *ProviderRunInspector, run *cloudsim.Run) { run.AccountIDHash = "other" }},
		{name: "repository", mutate: func(_ *ProviderRunInspector, run *cloudsim.Run) { run.Repository = "other/repo" }},
		{name: "source SHA", mutate: func(_ *ProviderRunInspector, run *cloudsim.Run) { run.Tags[cloudsim.TagSourceSHA] = "other" }},
		{name: "scenario tag", mutate: func(_ *ProviderRunInspector, run *cloudsim.Run) { run.Tags[cloudsim.TagScenarioDigest] = "other" }},
		{name: "created at", mutate: func(_ *ProviderRunInspector, run *cloudsim.Run) { run.CreatedAt = run.CreatedAt.Add(time.Second) }},
		{name: "expires at", mutate: func(_ *ProviderRunInspector, run *cloudsim.Run) { run.ExpiresAt = run.ExpiresAt.Add(time.Second) }},
		{name: "effective scenario", mutate: func(inspector *ProviderRunInspector, _ *cloudsim.Run) { inspector.Scenario.Digest = "other" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inspector, source := validProviderRunInspectorFixture()
			test.mutate(&inspector, &source.run)
			if _, err := inspector.InspectRun(context.Background(), "run-1"); !errors.Is(err, analysis.ErrRunIdentityMismatch) {
				t.Fatalf("InspectRun() error = %v, want ErrRunIdentityMismatch", err)
			}
		})
	}
}

func TestProviderRunInspectorMapsExactProviderInventory(t *testing.T) {
	inspector, source := validProviderRunInspectorFixture()
	inspection, err := inspector.InspectRun(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("InspectRun() error = %v", err)
	}
	if source.requestedRunID != "run-1" || inspection.RunID != "run-1" || inspection.Provider != "fake" ||
		inspection.Region != "cn-test" || inspection.SourceSHA != "abc123" || inspection.InventoryCount != 1 ||
		inspection.Scenario.Digest != "sha256:scenario" {
		t.Fatalf("InspectRun() = %#v, requested=%q", inspection, source.requestedRunID)
	}
}

func TestStaticRunInspectorRejectsMissingOrDifferentIdentity(t *testing.T) {
	inspector := StaticRunInspector{Inspection: analysis.RunInspection{RunID: "run-1", State: "running", InventoryCount: 1}}
	for _, runID := range []string{"", "run-other"} {
		if _, err := inspector.InspectRun(context.Background(), runID); !errors.Is(err, analysis.ErrRunIdentityMismatch) {
			t.Fatalf("InspectRun(%q) error = %v, want ErrRunIdentityMismatch", runID, err)
		}
	}
}

type recordingRunStatusSource struct {
	run            cloudsim.Run
	err            error
	calls          int
	requestedRunID string
}

func (s *recordingRunStatusSource) Status(_ context.Context, runID string) (cloudsim.Run, error) {
	s.calls++
	s.requestedRunID = runID
	return s.run, s.err
}

func validProviderRunInspectorFixture() (ProviderRunInspector, *recordingRunStatusSource) {
	createdAt := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	expiresAt := createdAt.Add(2 * time.Hour)
	locator := cloudsim.RunLocator{
		Schema: cloudsim.RunLocatorSchemaV1, RunID: "run-1", Provider: "fake", Region: "cn-test",
		AccountIDHash: "sha256:account", Repository: "WuKongIM/WuKongIM", SourceSHA: "abc123",
		ScenarioDigest: "sha256:scenario", CreatedAt: createdAt, ExpiresAt: expiresAt, ProvisionWorkflowRunID: 42,
	}
	source := &recordingRunStatusSource{run: cloudsim.Run{
		ID: "run-1", Provider: "fake", Region: "cn-test", AccountIDHash: "sha256:account", Repository: "WuKongIM/WuKongIM",
		State: cloudsim.StateRunning, CreatedAt: createdAt, ExpiresAt: expiresAt,
		Tags:      map[string]string{cloudsim.TagSourceSHA: "abc123", cloudsim.TagScenarioDigest: "sha256:scenario"},
		Resources: []cloudsim.Resource{{ID: "node-1", Kind: "compute", Role: "node-1"}},
	}}
	return ProviderRunInspector{
		Source: source, Locator: locator, Scenario: analysis.ScenarioInspection{Digest: "sha256:scenario", HashSlotCount: 256},
	}, source
}
