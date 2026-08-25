package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
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

func TestValidateRehearsalReportRunStartRejectsStaleGeneration(t *testing.T) {
	startedAt := time.Date(2026, 8, 26, 1, 2, 3, 456_000_000, time.UTC)
	runHash := "sha256:" + strings.Repeat("a", 64)
	assignmentHash := "sha256:" + strings.Repeat("b", 64)
	report := chatlifecycle.Report{
		Stage: chatlifecycle.StageRehearsal,
		Fence: chatlifecycle.ReportFence{
			RunHash: runHash, AssignmentHash: assignmentHash, Generation: 7,
		},
		Window: chatlifecycle.ReportTimeWindow{Start: startedAt},
	}
	receipt := chatlifecycle.RunStartReceipt{
		Schema: chatlifecycle.RunStartReceiptSchemaV1, Stage: chatlifecycle.StageRehearsal,
		StartedAt: startedAt, ExpectedEndAt: startedAt.Add(4*time.Hour + 15*time.Minute),
		RunHash: runHash, AssignmentHash: assignmentHash, Generation: 7,
	}
	if err := validateRehearsalReportRunStart(report, receipt); err != nil {
		t.Fatalf("matching report/run-start rejected: %v", err)
	}

	for name, mutate := range map[string]func(*chatlifecycle.RunStartReceipt){
		"run hash":        func(value *chatlifecycle.RunStartReceipt) { value.RunHash = "sha256:" + strings.Repeat("c", 64) },
		"assignment hash": func(value *chatlifecycle.RunStartReceipt) { value.AssignmentHash = "sha256:" + strings.Repeat("d", 64) },
		"generation":      func(value *chatlifecycle.RunStartReceipt) { value.Generation++ },
		"start":           func(value *chatlifecycle.RunStartReceipt) { value.StartedAt = value.StartedAt.Add(time.Nanosecond) },
	} {
		t.Run(name, func(t *testing.T) {
			stale := receipt
			mutate(&stale)
			if err := validateRehearsalReportRunStart(report, stale); err == nil {
				t.Fatalf("report from stale %s was accepted", name)
			}
		})
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

func TestMaterializeCommandBindsExplicitRepairBudgetFromTrustedFlag(t *testing.T) {
	templatePath := filepath.Join("..", "..", "configs", "cloud", "chat-lifecycle", "repair-v1.json")
	templateFile, err := os.Open(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	template, err := chatlifecyclerun.DecodeTemplate(templateFile)
	templateFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	template.Budget.HardLimitMicros = 450_000_000
	template.Budget.OperationalStopMicros = 430_000_000
	customTemplate := filepath.Join(t.TempDir(), "repair.json")
	body, err := json.Marshal(template)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(customTemplate, body, 0o600); err != nil {
		t.Fatal(err)
	}

	args := []string{
		"materialize", "--template", customTemplate,
		"--source-sha", strings.Repeat("a", 40), "--operator", "tangtaoit",
		"--codex-diagnostic-pubkey", commandPublicKey(t), "--request-id", "command-repair-budget",
		"--repository", "WuKongIM/WuKongIM", "--bundle-digest", "sha256:" + strings.Repeat("b", 64),
		"--deployment-pubkey", commandPublicKey(t), "--now", "2026-08-25T01:00:00Z", "--attempt", "1",
	}
	command := newRootCommand(&bytes.Buffer{})
	command.SetArgs(args)
	if err := command.Execute(); err == nil {
		t.Fatal("explicit repair budget without trusted flag was accepted")
	}

	var output bytes.Buffer
	command = newRootCommand(&output)
	command.SetArgs(append(args, "--authorized-repair-budget-cny", "450"))
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var plan chatlifecyclerun.RunPlan
	if err := json.Unmarshal(output.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.LeasePlan.Budget.LimitMicros != 450_000_000 || plan.OperationalStopMicros != 430_000_000 {
		t.Fatalf("materialized repair budget = %+v", plan.LeasePlan.Budget)
	}
}

func TestRepairMonitorCommandsEmitFailFastDecisionFromDurableState(t *testing.T) {
	directory := t.TempDir()
	statePath := filepath.Join(directory, "state.json")
	started := time.Date(2026, 8, 22, 16, 30, 0, 0, time.UTC)
	var begin bytes.Buffer
	command := newRootCommand(&begin)
	command.SetArgs([]string{
		"repair-begin", "--request-id", "chat-repair-command", "--lease-id", "lease-command",
		"--generation", "4", "--source-sha", strings.Repeat("a", 40),
		"--bundle-digest", "sha256:" + strings.Repeat("b", 64), "--started-at", started.Format(time.RFC3339),
		"--target-online", "10000", "--minimum-online-percent", "95", "--minimum-send-rate", "1", "--maximum-ack-backlog", "10000", "--warmup-timeout", "5m", "--stall-after", "15s", "--qualify-after", "2m",
	})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, begin.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	for index, offset := range []time.Duration{5 * time.Second, 20 * time.Second} {
		observationPath := filepath.Join(directory, fmt.Sprintf("observation-%d.json", index))
		body := fmt.Sprintf(`{"schema":"wukongim.chat_lifecycle.repair_observation/v2","request_id":"chat-repair-command","lease_id":"lease-command","generation":4,"observed_at":%q,"phase":"active","online":10000,"sent":100,"send_acknowledged":90,"terminal_errors":0,"workers":[{"worker_id":0,"uptime":%d,"sent":34,"send_acknowledged":30},{"worker_id":1,"uptime":%d,"sent":33,"send_acknowledged":30},{"worker_id":2,"uptime":%d,"sent":33,"send_acknowledged":30}]}`,
			started.Add(offset).Format(time.RFC3339), offset.Nanoseconds(), offset.Nanoseconds(), offset.Nanoseconds())
		if err := os.WriteFile(observationPath, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		command = newRootCommand(&output)
		command.SetArgs([]string{"repair-observe", "--state", statePath, "--observation", observationPath})
		if err := command.Execute(); err != nil {
			t.Fatal(err)
		}
		var result struct {
			State    json.RawMessage `json:"state"`
			Decision struct {
				Action string `json:"action"`
				Reason string `json:"reason"`
			} `json:"decision"`
		}
		if err := json.Unmarshal(output.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		wantAction := "continue"
		if index == 1 {
			wantAction = "stop_and_diagnose"
		}
		if result.Decision.Action != wantAction {
			t.Fatalf("decision %d = %+v", index, result.Decision)
		}
		if index == 1 && result.Decision.Reason != "message_progress_stalled" {
			t.Fatalf("terminal reason = %+v", result.Decision)
		}
		if err := os.WriteFile(statePath, result.State, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRepairCaptureAggregatesThreeFencedWorkerSnapshots(t *testing.T) {
	directory := t.TempDir()
	statePath := filepath.Join(directory, "state.json")
	started := time.Date(2026, 8, 22, 18, 30, 0, 288_009_056, time.UTC)
	var begin bytes.Buffer
	command := newRootCommand(&begin)
	command.SetArgs([]string{
		"repair-begin", "--request-id", "chat-repair-capture", "--lease-id", "lease-capture",
		"--generation", "2", "--source-sha", strings.Repeat("a", 40),
		"--bundle-digest", "sha256:" + strings.Repeat("b", 64), "--started-at", started.Format(time.RFC3339Nano),
		"--target-online", "10000", "--minimum-online-percent", "95", "--minimum-send-rate", "1", "--maximum-ack-backlog", "10000", "--warmup-timeout", "5m", "--stall-after", "15s", "--qualify-after", "2m",
	})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, begin.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	args := []string{"repair-capture", "--state", statePath, "--observed-at", started.Truncate(time.Second).Format(time.RFC3339)}
	for workerID := uint64(0); workerID < 3; workerID++ {
		status := chatlifecycle.WorkerStatus{
			RunID: "repair-run", AssignmentID: "repair-assignment", Phase: chatlifecycle.WorkerPhaseRunning,
			Generation: 7, WorkerID: workerID, WorkerCount: 3, TrafficReady: true,
		}
		snapshot := chatlifecycle.WorkerSnapshot{
			RunID: "repair-run", AssignmentID: "repair-assignment", Phase: chatlifecycle.WorkerPhaseRunning,
			Generation: 7, WorkerID: workerID, WorkerCount: 3, Uptime: time.Duration(workerID+1) * time.Minute,
			Sessions: chatlifecycle.WorkerSessionSnapshot{Target: 3334, Online: 3333, TrafficReady: 3333},
			Messages: chatlifecycle.WorkerMessageSnapshot{
				Sent: 100 + workerID, SendAcknowledged: 90 + workerID,
				// A retryable first-attempt rejection is diagnostic evidence, not a
				// terminal message failure. The repair monitor must let the bounded
				// retry path determine whether the logical SEND eventually fails.
				SendRejected: workerID + 1,
			},
		}
		statusPath := writeJSONFile(t, directory, fmt.Sprintf("status-%d.json", workerID), status)
		snapshotPath := writeJSONFile(t, directory, fmt.Sprintf("snapshot-%d.json", workerID), snapshot)
		args = append(args, "--worker-status", statusPath, "--worker-snapshot", snapshotPath)
	}
	var output bytes.Buffer
	command = newRootCommand(&output)
	command.SetArgs(args)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var observation struct {
		ObservedAt       time.Time `json:"observed_at"`
		Phase            string    `json:"phase"`
		Online           uint64    `json:"online"`
		Sent             uint64    `json:"sent"`
		SendAcknowledged uint64    `json:"send_acknowledged"`
		TerminalErrors   uint64    `json:"terminal_errors"`
		Workers          [3]struct {
			WorkerID         uint64        `json:"worker_id"`
			Uptime           time.Duration `json:"uptime"`
			Sent             uint64        `json:"sent"`
			SendAcknowledged uint64        `json:"send_acknowledged"`
		} `json:"workers"`
	}
	if err := json.Unmarshal(output.Bytes(), &observation); err != nil {
		t.Fatal(err)
	}
	if !observation.ObservedAt.Equal(started) || observation.Phase != "active" || observation.Online != 9999 ||
		observation.Sent != 303 || observation.SendAcknowledged != 273 || observation.TerminalErrors != 0 {
		t.Fatalf("observation = %+v", observation)
	}
	for workerID, progress := range observation.Workers {
		if progress.WorkerID != uint64(workerID) || progress.Uptime != time.Duration(workerID+1)*time.Minute ||
			progress.Sent != 100+uint64(workerID) || progress.SendAcknowledged != 90+uint64(workerID) {
			t.Fatalf("worker progress %d = %+v", workerID, progress)
		}
	}
}

func writeJSONFile(t *testing.T, directory, name string, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMaterializeCommandRequiresTypedTransitionForFormalPlan(t *testing.T) {
	directory := t.TempDir()
	transitionPath := filepath.Join(directory, "formal-transition.json")
	codexPublicKey := commandPublicKey(t)
	transition := chatlifecyclerun.StageTransition{
		Schema: chatlifecyclerun.FormalTransitionSchemaV1, FromStage: chatlifecyclerun.StageRehearsal,
		Outcome: "rehearsal_pass", RequestID: "formal-command-run", SourceSHA: strings.Repeat("c", 40),
		BundleDigest: "sha256:" + strings.Repeat("d", 64), CodexDiagnosticPubKey: codexPublicKey,
		CommittedMicros: 80_000_000, ZeroInventory: true,
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
		"--codex-diagnostic-pubkey", codexPublicKey, "--request-id", transition.RequestID,
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
