package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cloudleasefake "github.com/WuKongIM/WuKongIM/internal/infra/cloudlease/fake"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

func TestReadStrictDocumentRejectsUnsafeInputBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		path    func(*testing.T) string
		content []byte
		want    string
	}{
		{name: "path required", path: func(*testing.T) string { return "" }, want: "document path is required"},
		{name: "missing file", path: func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing.json") }, want: "no such file"},
		{name: "empty file", content: nil, want: "non-empty"},
		{name: "oversized file", content: bytes.Repeat([]byte(" "), maxPlanBytes+1), want: "at most 1 MiB"},
		{
			name:    "unknown field",
			content: []byte(`{"schema":"wukongim.cloud_lease.selector/v1","selector":{},"unknown":true}`),
			want:    "unknown field",
		},
		{name: "multiple JSON values", content: []byte("{}\n{}\n"), want: "multiple JSON values"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := ""
			if test.path != nil {
				path = test.path(t)
			} else {
				path = writeRawLeaseDocument(t, "document.json", test.content)
			}
			var document selectorDocument
			err := readStrictDocument(path, &document)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("readStrictDocument() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestReadPlanRejectsUnsafeInputBoundaries(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	validPlan, err := json.Marshal(dryRunPlan(now))
	if err != nil {
		t.Fatalf("Marshal(plan): %v", err)
	}
	tests := []struct {
		name    string
		path    func(*testing.T) string
		content []byte
		want    string
	}{
		{name: "path required", path: func(*testing.T) string { return "" }, want: "plan path is required"},
		{name: "missing file", path: func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing.json") }, want: "open plan"},
		{name: "empty file", content: nil, want: "non-empty"},
		{name: "oversized file", content: bytes.Repeat([]byte(" "), maxPlanBytes+1), want: "at most 1 MiB"},
		{name: "multiple JSON values", content: append(append(validPlan, '\n'), []byte("{}\n")...), want: "multiple JSON values"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := ""
			if test.path != nil {
				path = test.path(t)
			} else {
				path = writeRawLeaseDocument(t, "plan.json", test.content)
			}
			_, err := readPlan(path)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("readPlan() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestVersionedDocumentsRejectUnsupportedSchemasBeforeProviderConstruction(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	directory := t.TempDir()
	plan := dryRunPlan(now)
	planPath := writeJSONDocument(t, directory, "plan.json", plan)
	validQuotePath := writeJSONDocument(t, directory, "quote.json", quoteResult{Schema: quoteSchemaV1})
	validBootstrapPath := writeJSONDocument(t, directory, "bootstrap.json", bootstrapAccessDocument{
		Schema: bootstrapAccessSchemaV1,
	})
	selector := cloudlease.Selector{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Repository: plan.Repository, PlanDigest: "digest",
	}
	validSelectorPath := writeJSONDocument(t, directory, "selector.json", selectorDocument{
		Schema: selectorSchemaV1, Selector: selector,
	})

	tests := []struct {
		name string
		run  func(commandDependencies) error
		want string
	}{
		{
			name: "quote",
			run: func(dependencies commandDependencies) error {
				badQuotePath := writeJSONDocument(t, directory, "bad-quote.json", quoteResult{Schema: "unsupported"})
				return runAcquire(context.Background(), &bytes.Buffer{}, planPath, badQuotePath, validBootstrapPath, dependencies)
			},
			want: "quote schema is not supported",
		},
		{
			name: "bootstrap access",
			run: func(dependencies commandDependencies) error {
				badBootstrapPath := writeJSONDocument(t, directory, "bad-bootstrap.json", bootstrapAccessDocument{Schema: "unsupported"})
				return runAcquire(context.Background(), &bytes.Buffer{}, planPath, validQuotePath, badBootstrapPath, dependencies)
			},
			want: "bootstrap access schema is not supported",
		},
		{
			name: "selector",
			run: func(dependencies commandDependencies) error {
				badSelectorPath := writeJSONDocument(t, directory, "bad-selector.json", selectorDocument{Schema: "unsupported"})
				return runInspect(context.Background(), &bytes.Buffer{}, badSelectorPath, dependencies)
			},
			want: "selector schema is not supported",
		},
		{
			name: "access grant",
			run: func(dependencies commandDependencies) error {
				badGrantPath := writeJSONDocument(t, directory, "bad-grant.json", accessGrantDocument{Schema: "unsupported"})
				return runGrantAccess(context.Background(), &bytes.Buffer{}, validSelectorPath, badGrantPath, dependencies)
			},
			want: "access grant schema is not supported",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			providerCalls := 0
			dependencies := commandDependencies{
				now: func() time.Time { return now },
				inventoryProvider: func(string, string) (cloudlease.Provider, error) {
					providerCalls++
					return nil, nil
				},
				lifecycleProvider: func(string, string) (cloudlease.Provider, error) {
					providerCalls++
					return nil, nil
				},
			}
			err := test.run(dependencies)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("command error = %v, want %q", err, test.want)
			}
			if providerCalls != 0 {
				t.Fatalf("provider factory calls = %d, want zero", providerCalls)
			}
		})
	}
}

func TestProviderConstructionFailsClosedAndPreservesErrors(t *testing.T) {
	if _, err := constructLifecycleProvider(commandDependencies{}, "fake", "region"); err == nil || !strings.Contains(err.Error(), "factory is unavailable") {
		t.Fatalf("constructLifecycleProvider(nil) error = %v", err)
	}
	if _, err := constructInventoryProvider(commandDependencies{}, "fake", "region"); err == nil || !strings.Contains(err.Error(), "factory is unavailable") {
		t.Fatalf("constructInventoryProvider(nil) error = %v", err)
	}

	sentinel := errors.New("credential scope denied")
	dependencies := commandDependencies{
		lifecycleProvider: func(string, string) (cloudlease.Provider, error) { return nil, sentinel },
		inventoryProvider: func(string, string) (cloudlease.Provider, error) { return nil, sentinel },
	}
	if _, err := constructLifecycleProvider(dependencies, "fake", "region"); !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "construct lifecycle provider") {
		t.Fatalf("constructLifecycleProvider(error) = %v", err)
	}
	if _, err := constructInventoryProvider(dependencies, "fake", "region"); !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "construct inventory provider") {
		t.Fatalf("constructInventoryProvider(error) = %v", err)
	}
}

func TestRunQuoteFailsClosedAndPreservesProviderErrors(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	planPath := writeJSONDocument(t, t.TempDir(), "plan.json", dryRunPlan(now))
	constructionErr := errors.New("credential scope denied")
	tests := []struct {
		name         string
		dependencies commandDependencies
		want         string
		wantIs       error
	}{
		{
			name:         "missing factory",
			dependencies: commandDependencies{now: func() time.Time { return now }},
			want:         "quote provider factory is unavailable",
		},
		{
			name: "factory error",
			dependencies: commandDependencies{
				now: func() time.Time { return now },
				quoteProvider: func(cloudlease.Plan) (cloudlease.Provider, error) {
					return nil, constructionErr
				},
			},
			want:   "construct quote provider",
			wantIs: constructionErr,
		},
		{
			name: "provider quote error",
			dependencies: commandDependencies{
				now: func() time.Time { return now },
				quoteProvider: func(cloudlease.Plan) (cloudlease.Provider, error) {
					return cloudleasefake.New(cloudleasefake.Options{
						Now:      func() time.Time { return now },
						Failures: cloudleasefake.FailurePlan{Quote: true},
					}), nil
				},
			},
			want:   "quote:",
			wantIs: cloudleasefake.ErrInjectedFailure,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			err := runQuote(context.Background(), &stdout, planPath, test.dependencies)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("runQuote() error = %v, want %q", err, test.want)
			}
			if test.wantIs != nil && !errors.Is(err, test.wantIs) {
				t.Fatalf("runQuote() error = %v, want errors.Is(%v)", err, test.wantIs)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestRunSweepRejectsNonExactScopeBeforeProviderConstruction(t *testing.T) {
	tests := []struct {
		name       string
		provider   string
		region     string
		repository string
	}{
		{name: "blank provider", region: "region", repository: "owner/repo"},
		{name: "provider whitespace", provider: " fake", region: "region", repository: "owner/repo"},
		{name: "region whitespace", provider: "fake", region: "region ", repository: "owner/repo"},
		{name: "repository whitespace", provider: "fake", region: "region", repository: " owner/repo"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			providerCalls := 0
			err := runSweep(context.Background(), &bytes.Buffer{}, test.provider, test.region, test.repository, commandDependencies{
				lifecycleProvider: func(string, string) (cloudlease.Provider, error) {
					providerCalls++
					return nil, nil
				},
			})
			if err == nil || !strings.Contains(err.Error(), "exact non-empty values") {
				t.Fatalf("runSweep() error = %v, want exact-value rejection", err)
			}
			if providerCalls != 0 {
				t.Fatalf("provider factory calls = %d, want zero", providerCalls)
			}
		})
	}
}

func TestRunReleaseEmitsResidualProjectionBeforeReturningError(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	plan := dryRunPlan(now)
	provider := cloudleasefake.New(cloudleasefake.Options{
		Now: func() time.Time { return now },
		Failures: cloudleasefake.FailurePlan{
			ReleaseResidualAttempts: map[string]int{plan.LeaseID: 1},
		},
	})
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	quote, err := controller.Quote(context.Background(), plan)
	if err != nil {
		t.Fatalf("Quote(): %v", err)
	}
	if _, err := controller.Acquire(context.Background(), plan, quote); err != nil {
		t.Fatalf("Acquire(): %v", err)
	}
	selector, err := cloudlease.ReleaseSelectorFromPlanQuote(plan, quote, now)
	if err != nil {
		t.Fatalf("ReleaseSelectorFromPlanQuote(): %v", err)
	}
	selectorPath := writeJSONDocument(t, t.TempDir(), "selector.json", selectorDocument{
		Schema: selectorSchemaV1, Selector: selector,
	})

	var stdout bytes.Buffer
	err = runRelease(context.Background(), &stdout, selectorPath, commandDependencies{
		now: func() time.Time { return now },
		lifecycleProvider: func(string, string) (cloudlease.Provider, error) {
			return provider, nil
		},
	})
	if !errors.Is(err, cloudlease.ErrResidualResources) {
		t.Fatalf("runRelease() error = %v, want ErrResidualResources", err)
	}
	var output releaseResult
	if decodeErr := json.Unmarshal(stdout.Bytes(), &output); decodeErr != nil {
		t.Fatalf("decode release projection: %v; output=%q", decodeErr, stdout.String())
	}
	if output.Schema != releaseSchemaV1 || output.Result.Receipt == nil || output.Result.ZeroInventory != nil ||
		output.Result.Receipt.State != cloudlease.StateReleasePending || len(output.Result.Receipt.Resources) == 0 {
		t.Fatalf("release projection = %#v, want residual receipt", output)
	}
}

func TestRunSweepEmitsEmptyProjectionBeforeProviderError(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	provider := cloudleasefake.New(cloudleasefake.Options{
		Now:      func() time.Time { return now },
		Failures: cloudleasefake.FailurePlan{List: true},
	})

	var stdout bytes.Buffer
	err := runSweep(context.Background(), &stdout, cloudleasefake.ProviderName, "fake-region", "owner/repo", commandDependencies{
		now: func() time.Time { return now },
		lifecycleProvider: func(string, string) (cloudlease.Provider, error) {
			return provider, nil
		},
	})
	if !errors.Is(err, cloudleasefake.ErrInjectedFailure) {
		t.Fatalf("runSweep() error = %v, want injected provider error", err)
	}
	var output sweepResult
	if decodeErr := json.Unmarshal(stdout.Bytes(), &output); decodeErr != nil {
		t.Fatalf("decode sweep projection: %v; output=%q", decodeErr, stdout.String())
	}
	if output.Schema != sweepSchemaV1 || output.Result.Examined != 0 || output.Result.RevokedAccess == nil ||
		output.Result.Released == nil || output.Result.Pending == nil || output.Result.Failed == nil {
		t.Fatalf("sweep projection = %#v, want explicit empty evidence", output)
	}
}

func writeRawLeaseDocument(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
	return path
}
