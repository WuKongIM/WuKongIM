package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
	"github.com/WuKongIM/WuKongIM/internal/bench/localbaseline"
)

func TestBoundedDirectoryObservationCountsRegularFilesAndCachesOneMinute(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	first := filepath.Join(root, "first.bin")
	second := filepath.Join(nested, "second.bin")
	if err := os.WriteFile(first, []byte("1234"), 0o600); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if err := os.WriteFile(second, []byte("567"), 0o600); err != nil {
		t.Fatalf("write second: %v", err)
	}
	if err := os.Symlink(first, filepath.Join(root, "alias.bin")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if got, err := boundedDirectoryBytes(root); err != nil || got != 7 {
		t.Fatalf("bounded bytes = %d, %v; want 7", got, err)
	}
	if _, err := boundedDirectoryBytes(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing observation root unexpectedly succeeded")
	}

	start := time.Unix(100, 0)
	handler := &hostMetricsHandler{watchPath: root}
	if got, ok := handler.watchedBytes(start); !ok || got != 7 {
		t.Fatalf("initial watched bytes = %d, %t", got, ok)
	}
	if err := os.WriteFile(first, []byte("123456789"), 0o600); err != nil {
		t.Fatalf("grow first: %v", err)
	}
	if got, ok := handler.watchedBytes(start.Add(59 * time.Second)); !ok || got != 7 {
		t.Fatalf("cached watched bytes = %d, %t", got, ok)
	}
	if got, ok := handler.watchedBytes(start.Add(time.Minute)); !ok || got != 12 {
		t.Fatalf("refreshed watched bytes = %d, %t", got, ok)
	}
	if got, ok := (&hostMetricsHandler{}).watchedBytes(start); ok || got != 0 {
		t.Fatalf("disabled watch = %d, %t", got, ok)
	}
}

func TestFormalChainCapacityConfigIsCreatedOnceWithPrivatePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "capacity.yaml")
	cfg := chatlifecycle.Config{Mode: chatlifecycle.ModeCapacity, Stage: chatlifecycle.StageFormal}
	if err := writeFormalChainConfig(path, cfg); err != nil {
		t.Fatalf("write formal capacity config: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat capacity config: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("capacity config mode = %o", info.Mode().Perm())
	}
	original, err := os.ReadFile(path)
	if err != nil || len(original) == 0 {
		t.Fatalf("read capacity config: bytes=%d err=%v", len(original), err)
	}
	if err := writeFormalChainConfig(path, chatlifecycle.Config{}); err == nil {
		t.Fatal("existing capacity config was overwritten")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(original) {
		t.Fatalf("capacity config changed after refused overwrite: err=%v", err)
	}
	if err := writeFormalChainConfig(filepath.Join(dir, "missing", "capacity.yaml"), cfg); err == nil {
		t.Fatal("write beneath missing directory unexpectedly succeeded")
	}
}

func TestFormalChainResultProjectionIsBoundedAndFailsClosed(t *testing.T) {
	report := chatlifecycle.Report{Verdict: chatlifecycle.ReportVerdictEvidence{
		Outcome:           chatlifecycle.VerdictProductFailure,
		Cause:             chatlifecycle.VerdictCauseWorkerProduct,
		Terminal:          true,
		CleanupErrorCount: 1,
		CleanupErrors:     []chatlifecycle.VerdictCleanupErrorCode{chatlifecycle.VerdictCleanupWorkerStop},
	}}
	projected := reportRunResult(report, "formal summary\n")
	report.Verdict.CleanupErrors[0] = chatlifecycle.VerdictCleanupObserver
	if projected.Verdict.Outcome != chatlifecycle.VerdictProductFailure || projected.Verdict.Cause != chatlifecycle.VerdictCauseWorkerProduct || !projected.Verdict.Terminal {
		t.Fatalf("projected verdict = %+v", projected.Verdict)
	}
	if len(projected.Verdict.CleanupErrors) != 1 || projected.Verdict.CleanupErrors[0] != chatlifecycle.VerdictCleanupWorkerStop {
		t.Fatalf("cleanup projection aliases report: %+v", projected.Verdict.CleanupErrors)
	}

	coordinator := coordinatorRunResult(chatlifecycle.CoordinatorResult{Outcome: chatlifecycle.CoordinatorStopped}, "final.json")
	if coordinator.Verdict.Outcome != chatlifecycle.VerdictOperatorStop || coordinator.Verdict.Cause != chatlifecycle.VerdictCauseOperatorRequested || !strings.Contains(coordinator.Summary, "report=final.json") {
		t.Fatalf("coordinator projection = %+v", coordinator)
	}
	internal := internalFormalChainFailure("formal summary\n")
	if internal.Verdict.Outcome != chatlifecycle.VerdictHarnessInvalid || !internal.Verdict.Terminal || !strings.Contains(internal.Summary, "invalid_continuation") {
		t.Fatalf("internal failure projection = %+v", internal)
	}

	if _, err := (*productionFormalChainRunner)(nil).Run(context.Background()); err == nil {
		t.Fatal("nil formal runner did not fail closed")
	}
	if _, err := (&productionFormalChainRunner{}).Run(context.Background()); err == nil {
		t.Fatal("incomplete formal runner did not fail closed")
	}
	if _, err := (*productionChatLifecycleRunner)(nil).Run(context.Background()); err == nil {
		t.Fatal("nil lifecycle runner did not fail closed")
	}
	if _, err := (&productionChatLifecycleRunner{}).Run(context.Background()); err == nil {
		t.Fatal("incomplete lifecycle runner did not fail closed")
	}
}

func TestProductionRunnerStopRequestsAreNilSafeAndIdempotent(t *testing.T) {
	(*productionFormalChainRunner)(nil).RequestStop()
	(*productionChatLifecycleRunner)(nil).RequestStop()

	formal := &productionFormalChainRunner{stop: make(chan struct{})}
	formal.RequestStop()
	formal.RequestStop()
	select {
	case <-formal.stop:
	default:
		t.Fatal("formal stop channel remains open")
	}

	lifecycle := &productionChatLifecycleRunner{stop: make(chan struct{})}
	lifecycle.RequestStop()
	lifecycle.RequestStop()
	select {
	case <-lifecycle.stop:
	default:
		t.Fatal("lifecycle stop channel remains open")
	}
}

func TestLocalResultWritersPreserveTypedOutcomeAndPrivateMode(t *testing.T) {
	dir := t.TempDir()
	stepPath := filepath.Join(dir, "step.json")
	step := localChatLifecycleStepResult{Schema: localChatLifecycleStepSchemaV1, Outcome: localChatLifecycleStepClean, Reason: "closed"}
	if err := writeLocalChatLifecycleStepResult(stepPath, step); err != nil {
		t.Fatalf("write lifecycle step: %v", err)
	}
	var decoded localChatLifecycleStepResult
	body, err := os.ReadFile(stepPath)
	if err != nil || len(body) == 0 || body[len(body)-1] != '\n' || json.Unmarshal(body, &decoded) != nil {
		t.Fatalf("read lifecycle step: bytes=%d err=%v", len(body), err)
	}
	if decoded.Outcome != localChatLifecycleStepClean || decoded.Schema != localChatLifecycleStepSchemaV1 {
		t.Fatalf("decoded lifecycle step = %+v", decoded)
	}
	if info, err := os.Stat(stepPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("lifecycle step mode: info=%v err=%v", info, err)
	}
	if err := writeLocalChatLifecycleStepResult("", step); err == nil {
		t.Fatal("empty lifecycle step path unexpectedly accepted")
	}

	authorizationPath := filepath.Join(dir, "authorization.json")
	authorization := localbaseline.AuthorizationResult{Schema: "authorization/v1", Outcome: localbaseline.OutcomeClean, Authorizes: true}
	if err := writeLocalSingleNodeAuthorization(authorizationPath, authorization); err != nil {
		t.Fatalf("write authorization: %v", err)
	}
	var decodedAuthorization localbaseline.AuthorizationResult
	if err := readLocalSingleNodeJSON(authorizationPath, &decodedAuthorization); err != nil {
		t.Fatalf("read authorization: %v", err)
	}
	if decodedAuthorization.Schema != authorization.Schema || !decodedAuthorization.Authorizes {
		t.Fatalf("decoded authorization = %+v", decodedAuthorization)
	}
	if err := writeLocalSingleNodeAuthorization(" ", authorization); err == nil {
		t.Fatal("blank authorization path unexpectedly accepted")
	}
}

func TestLocalResultExitCodesKeepConfoundedAndProductFailuresSeparate(t *testing.T) {
	stepCases := map[localChatLifecycleStepOutcome]int{
		localChatLifecycleStepClean:                0,
		localChatLifecycleStepRateFailed:           exitHardLimit,
		localChatLifecycleStepProductFailure:       exitHardLimit,
		localChatLifecycleStepStorageConfounded:    exitPreflight,
		localChatLifecycleStepHostConfounded:       exitPreflight,
		localChatLifecycleStepInsufficientEvidence: exitInternal,
	}
	for outcome, want := range stepCases {
		if got := localChatLifecycleStepExitCode(outcome); got != want {
			t.Fatalf("lifecycle outcome %q exit = %d, want %d", outcome, got, want)
		}
	}
	closedCases := []struct {
		result localbaseline.ClosedStepResult
		want   int
	}{
		{result: localbaseline.ClosedStepResult{Clean: true}, want: 0},
		{result: localbaseline.ClosedStepResult{Outcome: localbaseline.OutcomeRateFailed}, want: exitHardLimit},
		{result: localbaseline.ClosedStepResult{Outcome: localbaseline.OutcomeProductFailure}, want: exitHardLimit},
		{result: localbaseline.ClosedStepResult{Outcome: localbaseline.OutcomeInsufficientEvidence}, want: exitInternal},
	}
	for _, tt := range closedCases {
		if got := localSingleNodeStepExitCode(tt.result); got != tt.want {
			t.Fatalf("closed step %+v exit = %d, want %d", tt.result, got, tt.want)
		}
	}
}

func TestLocalSingleNodeJSONRejectsTrailingDocumentsAndUnsafePaths(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.json")
	if err := writeLocalSingleNodeJSON(valid, map[string]int{"value": 7}); err != nil {
		t.Fatalf("write typed JSON: %v", err)
	}
	var decoded map[string]int
	if err := readLocalSingleNodeJSON(valid, &decoded); err != nil || decoded["value"] != 7 {
		t.Fatalf("read typed JSON: decoded=%v err=%v", decoded, err)
	}
	if err := writeLocalSingleNodeJSON(".", decoded); err == nil {
		t.Fatal("directory output path unexpectedly accepted")
	}
	if err := writeLocalSingleNodeJSON(filepath.Join(dir, "unsupported.json"), make(chan int)); err == nil {
		t.Fatal("unsupported JSON value unexpectedly encoded")
	}

	trailingDocument := filepath.Join(dir, "trailing-document.json")
	if err := os.WriteFile(trailingDocument, []byte("{}\n{}\n"), 0o600); err != nil {
		t.Fatalf("write trailing document: %v", err)
	}
	if err := readLocalSingleNodeJSON(trailingDocument, &decoded); err == nil || !strings.Contains(err.Error(), "trailing JSON document") {
		t.Fatalf("trailing document error = %v", err)
	}
	trailingGarbage := filepath.Join(dir, "trailing-garbage.json")
	if err := os.WriteFile(trailingGarbage, []byte("{}\ngarbage"), 0o600); err != nil {
		t.Fatalf("write trailing garbage: %v", err)
	}
	if err := readLocalSingleNodeJSON(trailingGarbage, &decoded); err == nil || !strings.Contains(err.Error(), "trailing data") {
		t.Fatalf("trailing garbage error = %v", err)
	}
}

func TestLocalSingleNodeRegularPathAndBoundedReadRejectLinksAndOversizeFiles(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	regular := filepath.Join(nested, "evidence.json")
	if err := os.WriteFile(regular, []byte("12345"), 0o600); err != nil {
		t.Fatalf("write evidence: %v", err)
	}
	if err := requireLocalSingleNodeRegularPath(root, "nested/evidence.json"); err != nil {
		t.Fatalf("regular path rejected: %v", err)
	}
	if body, err := readLocalSingleNodeBoundedFile(regular, 5); err != nil || string(body) != "12345" {
		t.Fatalf("bounded read = %q, %v", body, err)
	}
	if _, err := readLocalSingleNodeBoundedFile(regular, 4); err == nil {
		t.Fatal("oversize file unexpectedly accepted")
	}

	link := filepath.Join(root, "link.json")
	if err := os.Symlink(regular, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := requireLocalSingleNodeRegularPath(root, "link.json"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink error = %v", err)
	}
	if err := requireLocalSingleNodeRegularPath(root, "nested"); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory terminal error = %v", err)
	}
	if err := requireLocalSingleNodeRegularPath(root, "missing/file.json"); err == nil {
		t.Fatal("missing path unexpectedly accepted")
	}
}

func TestCeilingCostFractionRejectsOverflowAndRoundsUp(t *testing.T) {
	if got, ok := ceilingCostFraction(10, 3, 4); !ok || got != 8 {
		t.Fatalf("rounded cost = %d, %t; want 8", got, ok)
	}
	for _, input := range [][3]int64{{0, 1, 1}, {1, -1, 1}, {1, 1, 0}, {1 << 62, 4, 4}} {
		if got, ok := ceilingCostFraction(input[0], input[1], input[2]); ok || got != 0 {
			t.Fatalf("invalid fraction %v = %d, %t", input, got, ok)
		}
	}
	if got, ok := ceilingCostFraction(math.MaxInt64, 1, 2); ok || got != 0 {
		t.Fatalf("rounding overflow = %d, %t", got, ok)
	}
}

func TestLocalSingleNodeJSONMissingInputPreservesFilesystemError(t *testing.T) {
	var decoded map[string]any
	err := readLocalSingleNodeJSON(filepath.Join(t.TempDir(), "missing.json"), &decoded)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing JSON error = %v", err)
	}
}
