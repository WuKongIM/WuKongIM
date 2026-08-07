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

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
	cloudleasefake "github.com/WuKongIM/WuKongIM/internal/infra/cloudlease/fake"
	"github.com/WuKongIM/WuKongIM/internal/usecase/chatlifecyclerun"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

func TestFormalChainRequiresCapacityOnlyAfterPassingContinuousSoak(t *testing.T) {
	end := time.Unix(1_900_000_000, 0)
	digest := "sha256:" + strings.Repeat("a", 64)
	fence := chatlifecycle.ReportFence{
		RunHash: "sha256:" + strings.Repeat("b", 64), AssignmentHash: "sha256:" + strings.Repeat("c", 64), Generation: 1,
	}
	formal := chatlifecycle.Report{
		Profile: chatlifecycle.ProfileFormal, Mode: chatlifecycle.ModeSoak, Stage: chatlifecycle.StageFormal,
		Kind: chatlifecycle.CheckpointFinal, Final: true, Continuous: true, DatasetDigest: digest, Fence: fence,
		Window:  chatlifecycle.ReportTimeWindow{End: end},
		Verdict: chatlifecycle.ReportVerdictEvidence{Terminal: true, Outcome: chatlifecycle.VerdictPass, Cause: chatlifecycle.VerdictCauseCompleted},
	}
	if _, err := formalChainResultFromReports(formal, nil); err == nil {
		t.Fatal("passing formal Soak without capacity recovery was accepted")
	}
	capacityEnd := end.Add(4 * time.Hour)
	capacity := chatlifecycle.Report{
		Profile: chatlifecycle.ProfileFormal, Mode: chatlifecycle.ModeCapacity, Stage: chatlifecycle.StageFormal,
		Kind: chatlifecycle.CheckpointFinal, Final: true, Continuous: true, DatasetDigest: digest, Fence: fence,
		Window: chatlifecycle.ReportTimeWindow{Start: end.Add(time.Second), End: capacityEnd},
		Verdict: chatlifecycle.ReportVerdictEvidence{
			Terminal: true, Outcome: chatlifecycle.VerdictPassedWithCapacityWarning,
			Cause: chatlifecycle.VerdictCauseInfrastructureCapacity,
		},
		Capacity: chatlifecycle.ReportCapacityEvidence{
			Attempted: true, Completed: true, Attribution: chatlifecycle.CapacityAttributionInfrastructure,
			MaximumPassingRate: 2_750, FirstFailingRate: 3_025, RecoveryPassed: true,
		},
	}
	result, err := formalChainResultFromReports(formal, &capacity)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != chatlifecycle.VerdictPassedWithCapacityWarning ||
		result.Cause != chatlifecycle.VerdictCauseInfrastructureCapacity || result.End != capacityEnd {
		t.Fatalf("formal chain result = %+v", result)
	}

	for name, mutate := range map[string]func(*chatlifecycle.Report){
		"dataset": func(report *chatlifecycle.Report) { report.DatasetDigest = "sha256:" + strings.Repeat("d", 64) },
		"fence":   func(report *chatlifecycle.Report) { report.Fence.Generation++ },
		"window":  func(report *chatlifecycle.Report) { report.Window.Start = formal.Window.End },
	} {
		t.Run("rejects spliced "+name, func(t *testing.T) {
			candidate := capacity
			mutate(&candidate)
			if _, err := formalChainResultFromReports(formal, &candidate); err == nil {
				t.Fatalf("spliced %s report was accepted", name)
			}
		})
	}

	formal.Verdict.Outcome, formal.Verdict.Cause = chatlifecycle.VerdictProductFailure, chatlifecycle.VerdictCauseMessageLoss
	if _, err := formalChainResultFromReports(formal, &capacity); err == nil {
		t.Fatal("failed formal Soak accepted a spliced capacity report")
	}
	if result, err := formalChainResultFromReports(formal, nil); err != nil || result.Outcome != chatlifecycle.VerdictProductFailure {
		t.Fatalf("early formal failure result = %+v, %v", result, err)
	}
}

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

func TestMaterializeCommandRequiresTypedTransitionForFormalPlan(t *testing.T) {
	directory := t.TempDir()
	transitionPath := filepath.Join(directory, "formal-transition.json")
	transition := chatlifecyclerun.StageTransition{
		Schema: chatlifecyclerun.FormalTransitionSchemaV1, FromStage: chatlifecyclerun.StageRehearsal,
		Outcome: "rehearsal_pass", RequestID: "formal-command-run", SourceSHA: strings.Repeat("c", 40),
		BundleDigest: "sha256:" + strings.Repeat("d", 64), CommittedMicros: 80_000_000, ZeroInventory: true,
	}
	body, err := json.Marshal(transition)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transitionPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"materialize",
		"--template", filepath.Join("..", "..", "configs", "cloud", "chat-lifecycle", "formal-v1.json"),
		"--source-sha", transition.SourceSHA, "--operator", "tangtaoit",
		"--codex-diagnostic-pubkey", commandPublicKey(t), "--request-id", transition.RequestID,
		"--repository", "WuKongIM/WuKongIM", "--bundle-digest", transition.BundleDigest,
		"--deployment-pubkey", commandPublicKey(t), "--now", "2026-08-08T12:00:00Z",
		"--attempt", "1", "--committed-micros", "80000000",
	}
	command := newRootCommand(&bytes.Buffer{})
	command.SetArgs(args)
	if err := command.Execute(); err == nil {
		t.Fatal("formal materialization without --transition was accepted")
	}
	var output bytes.Buffer
	command = newRootCommand(&output)
	command.SetArgs(append(args, "--transition", transitionPath))
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var plan chatlifecyclerun.RunPlan
	if err := json.Unmarshal(output.Bytes(), &plan); err != nil || plan.Stage != chatlifecyclerun.StageFormal {
		t.Fatalf("formal plan = %+v, %v", plan, err)
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
