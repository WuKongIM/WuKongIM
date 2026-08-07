package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	cloudleasefake "github.com/WuKongIM/WuKongIM/internal/infra/cloudlease/fake"
	"github.com/WuKongIM/WuKongIM/internal/usecase/chatlifecyclerun"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

func TestMaterializeCommandBindsOnlyReviewedRehearsalInputs(t *testing.T) {
	var output bytes.Buffer
	command := newRootCommand(&output)
	command.SetArgs([]string{
		"materialize",
		"--template", filepath.Join("..", "..", "configs", "cloud", "chat-lifecycle", "rehearsal-v1.json"),
		"--source-sha", strings.Repeat("a", 40),
		"--operator", "tangtaoit",
		"--codex-diagnostic-pubkey", commandPublicKey(t),
		"--request-id", "command-run",
		"--repository", "WuKongIM/WuKongIM",
		"--bundle-digest", "sha256:" + strings.Repeat("b", 64),
		"--deployment-pubkey", commandPublicKey(t),
		"--now", "2026-08-07T12:00:00Z",
		"--attempt", "1",
	})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var plan chatlifecyclerun.RunPlan
	if err := json.Unmarshal(output.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Schema != chatlifecyclerun.RunPlanSchemaV1 || plan.Stage != chatlifecyclerun.StageRehearsal ||
		plan.Attempt != 1 || plan.LeasePlan.HostGroups[0].Count != 3 || plan.LeasePlan.HostGroups[1].Count != 1 {
		t.Fatalf("materialized plan = %+v", plan)
	}
}

func TestSelectorCommandRejectsUntypedOrExtendedReceipts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := os.WriteFile(path, []byte(`{"schema":"wukongim.cloud_lease.receipt/v1","receipt":{},"override":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	command := newRootCommand(&bytes.Buffer{})
	command.SetArgs([]string{"selector", "--receipt", path})
	if err := command.Execute(); err == nil {
		t.Fatal("selector accepted an extended untyped receipt")
	}
}

func TestSelectorFromPlanCommandCreatesPreAcquireCleanupIdentity(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	plan := cloudlease.Plan{
		Schema: cloudlease.PlanSchemaV1, LeaseID: "selector-lease", RequestID: "selector-request",
		Provider: cloudleasefake.ProviderName, Region: "fake-region", Repository: "WuKongIM/WuKongIM",
		Operator: "tester", ExpiresAt: now.Add(time.Hour),
		Budget:  cloudlease.Budget{Currency: "CNY", LimitMicros: 10_000_000},
		Network: cloudlease.NetworkPlan{Isolated: true, SingleZone: true},
		HostGroups: []cloudlease.HostGroupPlan{{
			Role: "host", Count: 1,
			Compute:    cloudlease.ComputePlan{VCPUs: 4, MemoryBytes: 8 << 30, Architecture: "x86_64", BillingModel: "postpaid"},
			SystemDisk: cloudlease.DiskPlan{Role: "system", SizeBytes: 40 << 30, Class: "ssd"},
		}},
	}
	provider := cloudleasefake.New(cloudleasefake.Options{Now: func() time.Time { return now }})
	quote, err := cloudlease.NewController(provider, func() time.Time { return now }).Quote(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	planPath := filepath.Join(directory, "plan.json")
	quotePath := filepath.Join(directory, "quote.json")
	planBody, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	quoteBody, err := json.Marshal(quoteDocument{Schema: "wukongim.cloud_lease.quote/v1", Quote: quote})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, planBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(quotePath, quoteBody, 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := newRootCommand(&output)
	command.SetArgs([]string{"selector-from-plan", "--plan", planPath, "--quote", quotePath})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var document selectorDocument
	if err := json.Unmarshal(output.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Selector.LeaseID != plan.LeaseID || document.Selector.PlanDigest != quote.PlanDigest {
		t.Fatalf("selector = %+v", document.Selector)
	}
}

func commandPublicKey(t *testing.T) string {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
}
