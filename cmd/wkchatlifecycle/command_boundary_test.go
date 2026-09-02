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
	repair "github.com/WuKongIM/WuKongIM/internal/usecase/chatlifecyclerepair"
	"github.com/WuKongIM/WuKongIM/internal/usecase/chatlifecyclerun"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

func TestWritePrivateAtomicCreatesOnceWithPrivatePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "capacity.yaml")
	if err := writePrivateAtomic(path, []byte("mode: capacity\n")); err != nil {
		t.Fatalf("write private output: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat private output: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("private output mode = %o", info.Mode().Perm())
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "mode: capacity\n" {
		t.Fatalf("private output = %q, %v", body, err)
	}
	if err := writePrivateAtomic(path, []byte("replacement")); !errors.Is(err, chatlifecyclerun.ErrInvalidInput) {
		t.Fatalf("overwrite error = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(body) {
		t.Fatalf("private output changed after refused overwrite: %q, %v", after, err)
	}

	for name, test := range map[string]struct {
		path string
		body []byte
	}{
		"blank path":  {path: " ", body: []byte("value")},
		"empty body":  {path: filepath.Join(dir, "empty"), body: nil},
		"missing dir": {path: filepath.Join(dir, "missing", "value"), body: []byte("value")},
	} {
		t.Run(name, func(t *testing.T) {
			if err := writePrivateAtomic(test.path, test.body); err == nil {
				t.Fatal("invalid private output unexpectedly succeeded")
			}
		})
	}
}

func TestReadStrictRejectsEmptyOversizeUnknownAndTrailingDocuments(t *testing.T) {
	type document struct {
		Schema string `json:"schema"`
	}
	dir := t.TempDir()
	write := func(name string, body []byte) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return path
	}

	var decoded document
	valid := write("valid.json", []byte(`{"schema":"v1"}`))
	if err := readStrict(valid, &decoded); err != nil || decoded.Schema != "v1" {
		t.Fatalf("valid strict document: decoded=%+v err=%v", decoded, err)
	}
	tests := []struct {
		name string
		path string
	}{
		{name: "blank path", path: " "},
		{name: "missing", path: filepath.Join(dir, "missing.json")},
		{name: "empty", path: write("empty.json", nil)},
		{name: "oversize", path: write("oversize.json", bytes.Repeat([]byte{'x'}, maxInputBytes+1))},
		{name: "unknown", path: write("unknown.json", []byte(`{"schema":"v1","secret":"no"}`))},
		{name: "trailing document", path: write("trailing.json", []byte("{}\n{}\n"))},
		{name: "trailing garbage", path: write("garbage.json", []byte("{}\ngarbage"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decoded = document{}
			if err := readStrict(tt.path, &decoded); err == nil {
				t.Fatal("invalid strict document unexpectedly succeeded")
			}
		})
	}
}

func TestRepairAbortCommandSealsExternalFailureAgainstExactGeneration(t *testing.T) {
	started := time.Date(2026, 9, 2, 8, 30, 0, 125_000_000, time.UTC)
	state, err := repair.Begin(repair.Config{
		TargetOnline: 10000, MinimumOnlinePercent: 95,
		WarmupTimeout: 5 * time.Minute, StallAfter: 15 * time.Second, QualifyAfter: 2 * time.Minute,
		MinimumSendRatePerSecond: 1, MaximumAckBacklog: 10000,
	}, repair.Candidate{
		RequestID: "repair-abort-request", LeaseID: "repair-abort-lease", Generation: 3,
		SourceSHA: strings.Repeat("a", 40), BundleDigest: "sha256:" + strings.Repeat("b", 64),
	}, started)
	if err != nil {
		t.Fatalf("begin repair state: %v", err)
	}
	statePath := writeJSONFile(t, t.TempDir(), "state.json", state)

	var output bytes.Buffer
	command := newRootCommand(&output)
	command.SetArgs([]string{
		"repair-abort", "--state", statePath,
		"--observed-at", started.Add(time.Second).Format(time.RFC3339Nano),
		"--reason", string(repair.ReasonOperatorStop),
	})
	if err := command.Execute(); err != nil {
		t.Fatalf("repair abort: %v", err)
	}
	var step repairStep
	if err := json.Unmarshal(output.Bytes(), &step); err != nil {
		t.Fatalf("decode repair step: %v", err)
	}
	if step.Schema != repairStepSchemaV1 || step.Decision.Action != repair.ActionStopAndDiagnose ||
		step.Decision.Reason != repair.ReasonOperatorStop || step.State.TerminalReason != repair.ReasonOperatorStop {
		t.Fatalf("repair abort step = %+v", step)
	}

	for name, args := range map[string][]string{
		"missing state": {
			"repair-abort", "--state", filepath.Join(filepath.Dir(statePath), "missing.json"),
			"--observed-at", started.Add(time.Second).Format(time.RFC3339Nano), "--reason", string(repair.ReasonOperatorStop),
		},
		"non UTC observation": {
			"repair-abort", "--state", statePath,
			"--observed-at", "2026-09-02T09:30:01+01:00", "--reason", string(repair.ReasonOperatorStop),
		},
		"invalid reason": {
			"repair-abort", "--state", statePath,
			"--observed-at", started.Add(time.Second).Format(time.RFC3339Nano), "--reason", "free_form_failure",
		},
	} {
		t.Run(name, func(t *testing.T) {
			command := newRootCommand(&bytes.Buffer{})
			command.SetArgs(args)
			if err := command.Execute(); err == nil {
				t.Fatal("invalid repair abort unexpectedly succeeded")
			}
		})
	}

	observe := newRootCommand(&bytes.Buffer{})
	observe.SetArgs([]string{
		"repair-observe", "--state", statePath,
		"--observation", filepath.Join(filepath.Dir(statePath), "missing-observation.json"),
	})
	if err := observe.Execute(); !errors.Is(err, repair.ErrInvalidObservation) {
		t.Fatalf("missing repair observation error = %v", err)
	}
}

func TestRepairTimeAndCounterNormalizationFailClosed(t *testing.T) {
	started := time.Date(2026, 9, 2, 8, 30, 0, 900_000_000, time.UTC)
	if got, err := normalizeRepairObservedAt(started, started.Add(time.Second)); err != nil || !got.Equal(started.Add(time.Second)) {
		t.Fatalf("later observation = %s, %v", got, err)
	}
	if got, err := normalizeRepairObservedAt(started, started.Truncate(time.Second)); err != nil || !got.Equal(started) {
		t.Fatalf("same-second observation = %s, %v", got, err)
	}
	if _, err := normalizeRepairObservedAt(started, started.Add(-time.Second)); !errors.Is(err, repair.ErrInvalidObservation) {
		t.Fatalf("older observation error = %v", err)
	}
	if got, err := addRepairCounter(10, 5); err != nil || got != 15 {
		t.Fatalf("counter sum = %d, %v", got, err)
	}
	if _, err := addRepairCounter(^uint64(0), 1); !errors.Is(err, repair.ErrInvalidObservation) {
		t.Fatalf("counter overflow error = %v", err)
	}
}

func TestReportAndCapacityCommandsRejectUnavailableInputsBeforeMutation(t *testing.T) {
	dir := t.TempDir()
	zeroPlan := filepath.Join(dir, "zero-plan.json")
	zeroQuote := filepath.Join(dir, "zero-quote.json")
	if err := os.WriteFile(zeroPlan, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write zero plan: %v", err)
	}
	if err := os.WriteFile(zeroQuote, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write zero quote: %v", err)
	}
	tests := []struct {
		name string
		args []string
	}{
		{name: "formal report", args: []string{"validate-formal-chain", "--formal-report", filepath.Join(dir, "missing-formal.json")}},
		{name: "rehearsal report", args: []string{"validate-rehearsal-report", "--report", filepath.Join(dir, "missing-report.json"), "--run-start", filepath.Join(dir, "missing-start.json")}},
		{name: "capacity config", args: []string{"prepare-capacity-config", "--config", filepath.Join(dir, "missing.yaml"), "--checkpoint", filepath.Join(dir, "missing.json"), "--output", filepath.Join(dir, "output.yaml")}},
		{name: "repair observe state", args: []string{"repair-observe", "--state", filepath.Join(dir, "missing-state.json"), "--observation", filepath.Join(dir, "missing-observation.json")}},
		{name: "plan selector missing plan", args: []string{"selector-from-plan", "--plan", filepath.Join(dir, "missing-plan.json"), "--quote", zeroQuote}},
		{name: "plan selector missing quote", args: []string{"selector-from-plan", "--plan", zeroPlan, "--quote", filepath.Join(dir, "missing-quote.json")}},
		{name: "plan selector untyped quote", args: []string{"selector-from-plan", "--plan", zeroPlan, "--quote", zeroQuote}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command := newRootCommand(&bytes.Buffer{})
			command.SetArgs(tt.args)
			if err := command.Execute(); err == nil {
				t.Fatal("unavailable input unexpectedly accepted")
			}
		})
	}
	if _, err := os.Stat(filepath.Join(dir, "output.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("capacity output exists after rejected input: %v", err)
	}
}

func TestSelectorCommandProjectsOnlyExactReleaseIdentity(t *testing.T) {
	now := time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)
	plan := cloudlease.Plan{
		Schema: cloudlease.PlanSchemaV1, LeaseID: "selector-command-lease", RequestID: "selector-command-request",
		Provider: cloudleasefake.ProviderName, Region: "fake-region", Repository: "WuKongIM/WuKongIM", Operator: "tester",
		ExpiresAt: now.Add(time.Hour), Budget: cloudlease.Budget{Currency: "CNY", LimitMicros: 10_000_000},
		Network: cloudlease.NetworkPlan{Isolated: true, SingleZone: true},
		HostGroups: []cloudlease.HostGroupPlan{{
			Role: "host", Count: 1,
			Compute:    cloudlease.ComputePlan{VCPUs: 4, MemoryBytes: 8 << 30, Architecture: "x86_64", BillingModel: "postpaid"},
			SystemDisk: cloudlease.DiskPlan{Role: "system", SizeBytes: 40 << 30, Class: "ssd"},
		}},
	}
	controller := cloudlease.NewController(cloudleasefake.New(cloudleasefake.Options{Now: func() time.Time { return now }}), func() time.Time { return now })
	quote, err := controller.Quote(context.Background(), plan)
	if err != nil {
		t.Fatalf("quote selector lease: %v", err)
	}
	receipt, err := controller.Acquire(context.Background(), plan, quote)
	if err != nil {
		t.Fatalf("acquire selector lease: %v", err)
	}
	path := writeJSONFile(t, t.TempDir(), "receipt.json", receiptDocument{
		Schema: "wukongim.cloud_lease.receipt/v1", Receipt: receipt,
	})
	var output bytes.Buffer
	command := newRootCommand(&output)
	command.SetArgs([]string{"selector", "--receipt", path})
	if err := command.Execute(); err != nil {
		t.Fatalf("project selector: %v", err)
	}
	var projected selectorDocument
	if err := json.Unmarshal(output.Bytes(), &projected); err != nil {
		t.Fatalf("decode selector: %v", err)
	}
	if projected.Schema != "wukongim.cloud_lease.selector/v1" || projected.Selector.LeaseID != receipt.LeaseID ||
		projected.Selector.RequestID != receipt.RequestID || projected.Selector.PlanDigest != receipt.PlanDigest ||
		projected.Selector.Provider != receipt.Provider || projected.Selector.Region != receipt.Region || projected.Selector.Repository != receipt.Repository {
		t.Fatalf("selector projection = %+v", projected)
	}
}
