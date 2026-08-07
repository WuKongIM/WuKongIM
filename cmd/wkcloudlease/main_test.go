package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/infra/cloudlease/fake"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
	"golang.org/x/crypto/ssh"
)

func TestDryRunExercisesCompleteFakeLifecycleWithoutBackgroundWork(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(&stdout)
	command.SetArgs([]string{"dry-run"})

	if err := command.Execute(); err != nil {
		t.Fatalf("dry-run error = %v", err)
	}
	var result dryRunResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode dry-run output: %v\n%s", err, stdout.String())
	}
	if result.Schema != dryRunSchemaV1 || result.Provider != "fake" {
		t.Fatalf("dry-run identity = %#v", result)
	}
	if result.FinalState != "released" || result.ResidualResources != 0 {
		t.Fatalf("dry-run final result = %#v, want released zero inventory", result)
	}
	wantOperations := []string{"quote", "acquire", "inspect", "grant_access", "revoke_access", "release", "sweep"}
	if len(result.Operations) != len(wantOperations) {
		t.Fatalf("dry-run operations = %v, want %v", result.Operations, wantOperations)
	}
	for index, want := range wantOperations {
		if result.Operations[index] != want {
			t.Fatalf("dry-run operation[%d] = %q, want %q", index, result.Operations[index], want)
		}
	}
	if result.SweepExamined != 0 {
		t.Fatalf("dry-run sweep examined = %d, want 0 after zero-inventory proof", result.SweepExamined)
	}
}

func TestQuoteCommandReadsStrictPlanAndEmitsProviderEvidence(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	plan := dryRunPlan(now)
	plan.Network.ConservativePublicEgressBytes = 10 << 30
	path := filepath.Join(t.TempDir(), "plan.json")
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	provider := &quoteOnlyProvider{now: now}
	var stdout bytes.Buffer
	command := newRootCommandWithDependencies(&stdout, commandDependencies{
		now: func() time.Time { return now },
		quoteProvider: func(got cloudlease.Plan) (cloudlease.Provider, error) {
			if got.LeaseID != plan.LeaseID {
				t.Fatalf("provider factory Plan = %#v, want lease %q", got, plan.LeaseID)
			}
			return provider, nil
		},
	})
	command.SetArgs([]string{"quote", "--plan", path})

	if err := command.Execute(); err != nil {
		t.Fatalf("quote error = %v", err)
	}
	var result quoteResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode quote output: %v\n%s", err, stdout.String())
	}
	if result.Schema != quoteSchemaV1 || result.Quote.LeaseID != plan.LeaseID || result.Quote.PlanDigest == "" {
		t.Fatalf("quote output = %#v", result)
	}
	if provider.mutationCalls != 0 {
		t.Fatalf("provider mutation calls = %d, want 0", provider.mutationCalls)
	}
}

func TestQuoteCommandRejectsUnknownPlanFieldsBeforeProviderConstruction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, []byte(`{"schema":"wukongim.cloud_lease/v1","unknown":true}`), 0o600); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	providerCalls := 0
	command := newRootCommandWithDependencies(&bytes.Buffer{}, commandDependencies{
		quoteProvider: func(cloudlease.Plan) (cloudlease.Provider, error) {
			providerCalls++
			return nil, nil
		},
	})
	command.SetArgs([]string{"quote", "--plan", path})

	if err := command.Execute(); err == nil {
		t.Fatal("quote error = nil, want strict JSON rejection")
	}
	if providerCalls != 0 {
		t.Fatalf("provider factory calls = %d, want 0", providerCalls)
	}
}

func TestLifecycleCommandsAcquireInspectReleaseAndSweepTypedDocuments(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	provider := fake.New(fake.Options{Now: func() time.Time { return now }, EstimatedCostMicros: 4_000_000})
	plan := dryRunPlan(now)
	quote, err := cloudlease.NewController(provider, func() time.Time { return now }).Quote(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	planPath := writeJSONDocument(t, directory, "plan.json", plan)
	quotePath := writeJSONDocument(t, directory, "quote.json", quoteResult{Schema: quoteSchemaV1, Quote: quote})
	bootstrapPath := writeJSONDocument(t, directory, "bootstrap.json", bootstrapAccessDocument{
		Schema: bootstrapAccessSchemaV1, Access: testBootstrapAccess(t),
	})
	dependencies := commandDependencies{
		now: func() time.Time { return now },
		lifecycleProvider: func(gotProvider, gotRegion string) (cloudlease.Provider, error) {
			if gotProvider != plan.Provider || gotRegion != plan.Region {
				t.Fatalf("provider factory identity = %s/%s", gotProvider, gotRegion)
			}
			return provider, nil
		},
	}

	var acquireOutput bytes.Buffer
	acquire := newRootCommandWithDependencies(&acquireOutput, dependencies)
	acquire.SetArgs([]string{"acquire", "--plan", planPath, "--quote", quotePath, "--bootstrap-access", bootstrapPath})
	if err := acquire.Execute(); err != nil {
		t.Fatalf("acquire error = %v", err)
	}
	var acquired receiptResult
	if err := json.Unmarshal(acquireOutput.Bytes(), &acquired); err != nil || acquired.Schema != receiptSchemaV1 || acquired.Receipt.State != cloudlease.StateActive {
		t.Fatalf("acquire output = %s, %v", acquireOutput.String(), err)
	}
	if _, exists := acquired.Receipt.Tags[cloudlease.TagBootstrapAccessDigest]; !exists {
		t.Fatalf("receipt tags = %#v, want bootstrap access digest", acquired.Receipt.Tags)
	}

	selector := cloudlease.Selector{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Repository: plan.Repository, PlanDigest: quote.PlanDigest,
	}
	selectorPath := writeJSONDocument(t, directory, "selector.json", selectorDocument{Schema: selectorSchemaV1, Selector: selector})
	var inspectOutput bytes.Buffer
	inspect := newRootCommandWithDependencies(&inspectOutput, dependencies)
	inspect.SetArgs([]string{"inspect", "--selector", selectorPath})
	if err := inspect.Execute(); err != nil {
		t.Fatalf("inspect error = %v", err)
	}
	var inspected receiptResult
	if err := json.Unmarshal(inspectOutput.Bytes(), &inspected); err != nil || inspected.Receipt.LeaseID != plan.LeaseID {
		t.Fatalf("inspect output = %s, %v", inspectOutput.String(), err)
	}

	var releaseOutput bytes.Buffer
	release := newRootCommandWithDependencies(&releaseOutput, dependencies)
	release.SetArgs([]string{"release", "--selector", selectorPath})
	if err := release.Execute(); err != nil {
		t.Fatalf("release error = %v", err)
	}
	var released releaseResult
	if err := json.Unmarshal(releaseOutput.Bytes(), &released); err != nil || released.Result.ZeroInventory == nil {
		t.Fatalf("release output = %s, %v", releaseOutput.String(), err)
	}

	var sweepOutput bytes.Buffer
	sweep := newRootCommandWithDependencies(&sweepOutput, dependencies)
	sweep.SetArgs([]string{"sweep", "--provider", plan.Provider, "--region", plan.Region, "--repository", plan.Repository})
	if err := sweep.Execute(); err != nil {
		t.Fatalf("sweep error = %v", err)
	}
	var swept sweepResult
	if err := json.Unmarshal(sweepOutput.Bytes(), &swept); err != nil || swept.Schema != sweepSchemaV1 || swept.Result.Examined != 0 {
		t.Fatalf("sweep output = %s, %v", sweepOutput.String(), err)
	}
}

func TestAcquireRejectsUnknownBootstrapFieldsBeforeProviderConstruction(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	plan := dryRunPlan(now)
	planPath := writeJSONDocument(t, directory, "plan.json", plan)
	quotePath := writeJSONDocument(t, directory, "quote.json", quoteResult{Schema: quoteSchemaV1})
	bootstrapPath := filepath.Join(directory, "bootstrap.json")
	if err := os.WriteFile(bootstrapPath, []byte(`{"schema":"wukongim.cloud_lease.bootstrap_access/v1","access":{"authorized_keys":[]},"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	providerCalls := 0
	command := newRootCommandWithDependencies(&bytes.Buffer{}, commandDependencies{
		now: func() time.Time { return now },
		lifecycleProvider: func(string, string) (cloudlease.Provider, error) {
			providerCalls++
			return nil, nil
		},
	})
	command.SetArgs([]string{"acquire", "--plan", planPath, "--quote", quotePath, "--bootstrap-access", bootstrapPath})
	if err := command.Execute(); err == nil {
		t.Fatal("acquire error = nil, want strict JSON rejection")
	}
	if providerCalls != 0 {
		t.Fatalf("provider calls = %d, want zero", providerCalls)
	}
}

func testBootstrapAccess(t *testing.T) cloudlease.BootstrapAccess {
	t.Helper()
	keys := make([]string, 0, 2)
	for value := byte(11); value <= 12; value++ {
		private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{value}, ed25519.SeedSize))
		publicKey, err := ssh.NewPublicKey(private.Public())
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, string(ssh.MarshalAuthorizedKey(publicKey)))
	}
	return cloudlease.BootstrapAccess{AuthorizedKeys: keys}
}

func writeJSONDocument(t *testing.T, directory, name string, value any) string {
	t.Helper()
	path := filepath.Join(directory, name)
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type quoteOnlyProvider struct {
	now           time.Time
	mutationCalls int
}

func (*quoteOnlyProvider) Name() string { return "fake" }

func (p *quoteOnlyProvider) Quote(_ context.Context, request cloudlease.QuoteRequest) (cloudlease.Quote, error) {
	return cloudlease.Quote{
		LeaseID: request.Plan.LeaseID, RequestID: request.Plan.RequestID,
		Provider: request.Plan.Provider, Region: request.Plan.Region, Zone: "fake-zone-a",
		PlanDigest: request.PlanDigest, Currency: request.Plan.Budget.Currency, EstimatedCostMicros: 5_000_000,
		CapacityAvailable: true, QuotaAvailable: true, QuotedAt: p.now, ValidUntil: p.now.Add(10 * time.Minute),
	}, nil
}

func (p *quoteOnlyProvider) Acquire(context.Context, cloudlease.AcquireRequest) (cloudlease.Receipt, error) {
	p.mutationCalls++
	return cloudlease.Receipt{}, nil
}

func (*quoteOnlyProvider) Inspect(context.Context, cloudlease.Selector) (cloudlease.Receipt, error) {
	return cloudlease.Receipt{}, cloudlease.ErrLeaseNotFound
}

func (*quoteOnlyProvider) List(context.Context, cloudlease.InventoryFilter) ([]cloudlease.Receipt, error) {
	return nil, nil
}

func (p *quoteOnlyProvider) GrantAccess(context.Context, cloudlease.Selector, cloudlease.AccessGrant) (cloudlease.Receipt, error) {
	p.mutationCalls++
	return cloudlease.Receipt{}, nil
}

func (p *quoteOnlyProvider) RevokeAccess(context.Context, cloudlease.Selector, string) (cloudlease.Receipt, error) {
	p.mutationCalls++
	return cloudlease.Receipt{}, nil
}

func (p *quoteOnlyProvider) Release(context.Context, cloudlease.Selector) (cloudlease.ReleaseResult, error) {
	p.mutationCalls++
	return cloudlease.ReleaseResult{}, nil
}
