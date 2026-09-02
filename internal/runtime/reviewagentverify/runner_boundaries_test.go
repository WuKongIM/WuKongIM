package reviewagentverify_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	verify "github.com/WuKongIM/WuKongIM/internal/runtime/reviewagentverify"
)

func TestRunnerRegistryIsSortedAndReturnsNewestTrustedResult(t *testing.T) {
	t.Parallel()

	ledger := &memoryEvidenceLedger{records: []verify.LedgerRecord{
		{Evidence: trustedCheckEvidence("go-unit", contract.CheckOutcomeFailed, 1, "old")},
		{Evidence: trustedCheckEvidence("go-vet", contract.CheckOutcomePassed, 0, "vet")},
		{Evidence: trustedCheckEvidence("go-unit", contract.CheckOutcomePassed, 0, "new")},
	}}
	runner := newMemoryRunner(t, &recordingExecutor{}, ledger, map[string]verify.CheckPlan{
		"go-vet":  validCheckPlan(),
		"go-unit": validCheckPlan(),
	})

	names := runner.Names()
	require.Equal(t, []string{"go-unit", "go-vet"}, names)
	names[0] = "attacker-controlled"
	require.Equal(t, []string{"go-unit", "go-vet"}, runner.Names())

	evidence, err := runner.Result(testGeneration(), "go-unit")
	require.NoError(t, err)
	require.Equal(t, "new", evidence.Stdout)
	_, err = runner.Result(testGeneration(), "unknown")
	require.EqualError(t, err, "unknown trusted check")

	ledger.records = nil
	_, err = runner.Result(testGeneration(), "go-unit")
	require.EqualError(t, err, "trusted check has no result")
	ledger.listErr = errors.New("ledger unavailable")
	_, err = runner.Result(testGeneration(), "go-unit")
	require.ErrorIs(t, err, ledger.listErr)
}

func TestRunnerConstructionFailsClosedOnWorkspaceAndPlanBoundaries(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	nonDirectory := filepath.Join(root, "file")
	require.NoError(t, os.WriteFile(nonDirectory, []byte("fixture"), 0o600))
	symlink := filepath.Join(root, "workspace-link")
	require.NoError(t, os.Symlink(root, symlink))
	executor := &recordingExecutor{}
	ledger := &memoryEvidenceLedger{}

	tests := []struct {
		name   string
		config verify.RunnerConfig
	}{
		{name: "nil executor", config: verify.RunnerConfig{WorkspaceRoot: root, Policy: oneCheckPolicy(), Ledger: ledger}},
		{name: "nil ledger", config: verify.RunnerConfig{WorkspaceRoot: root, Policy: oneCheckPolicy(), Executor: executor}},
		{name: "empty catalog", config: verify.RunnerConfig{WorkspaceRoot: root, Policy: verify.Policy{}, Executor: executor, Ledger: ledger}},
		{name: "non-directory workspace", config: verify.RunnerConfig{WorkspaceRoot: nonDirectory, Policy: oneCheckPolicy(), Executor: executor, Ledger: ledger}},
		{name: "symlink workspace", config: verify.RunnerConfig{WorkspaceRoot: symlink, Policy: oneCheckPolicy(), Executor: executor, Ledger: ledger}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := verify.NewRunner(test.config)
			require.Error(t, err)
		})
	}

	tooManyArguments := make([]string, 129)
	for index := range tooManyArguments {
		tooManyArguments[index] = "x"
	}
	planTests := []struct {
		name string
		plan verify.CheckPlan
	}{
		{name: "no arguments", plan: verify.CheckPlan{TimeoutSeconds: 1, MaxOutputBytes: 1}},
		{name: "too many arguments", plan: verify.CheckPlan{Arguments: tooManyArguments, TimeoutSeconds: 1, MaxOutputBytes: 1}},
		{name: "empty argument", plan: verify.CheckPlan{Arguments: []string{"go", ""}, TimeoutSeconds: 1, MaxOutputBytes: 1}},
		{name: "oversized argument", plan: verify.CheckPlan{Arguments: []string{strings.Repeat("x", 4097)}, TimeoutSeconds: 1, MaxOutputBytes: 1}},
		{name: "zero timeout", plan: verify.CheckPlan{Arguments: []string{"go"}, MaxOutputBytes: 1}},
		{name: "oversized timeout", plan: verify.CheckPlan{Arguments: []string{"go"}, TimeoutSeconds: 5401, MaxOutputBytes: 1}},
		{name: "zero output bound", plan: verify.CheckPlan{Arguments: []string{"go"}, TimeoutSeconds: 1}},
		{name: "oversized output bound", plan: verify.CheckPlan{Arguments: []string{"go"}, TimeoutSeconds: 1, MaxOutputBytes: (16 << 20) + 1}},
		{name: "working directory escape", plan: verify.CheckPlan{Arguments: []string{"go"}, WorkingDir: "../outside", TimeoutSeconds: 1, MaxOutputBytes: 1}},
	}
	for _, test := range planTests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := verify.NewRunner(verify.RunnerConfig{
				WorkspaceRoot: root,
				Policy: verify.Policy{TrustedChecks: map[string]verify.CheckPlan{
					"go-unit": test.plan,
				}},
				Executor: executor,
				Ledger:   ledger,
			})
			require.Error(t, err)
		})
	}
}

func TestCollectEvidenceSealsMandatoryBaselineAndKeepsNewestOptionalResult(
	t *testing.T,
) {
	t.Parallel()

	mandatoryBaseline := trustedCheckEvidence(
		"go-unit",
		contract.CheckOutcomeFailed,
		1,
		"first mandatory result",
	)
	optionalOld := trustedCheckEvidence(
		"go-vet",
		contract.CheckOutcomeFailed,
		1,
		"old optional result",
	)
	optionalNew := trustedCheckEvidence(
		"go-vet",
		contract.CheckOutcomePassed,
		0,
		"new optional result",
	)
	ledger := &memoryEvidenceLedger{records: []verify.LedgerRecord{
		{Evidence: mandatoryBaseline},
		{Evidence: optionalOld},
		{Evidence: trustedCheckEvidence("go-unit", contract.CheckOutcomePassed, 0, "rerun")},
		{Evidence: optionalNew},
	}}
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.FixedZone("fixture", 3600))
	evidence, err := verify.CollectEvidence(
		ledger,
		verify.Policy{TrustedChecks: map[string]verify.CheckPlan{
			"go-unit": {},
			"go-vet":  {},
		}},
		func() time.Time { return now },
		testGeneration(),
		[]string{"go-unit", "go-unit"},
	)
	require.NoError(t, err)
	require.Equal(t, []contract.CheckEvidence{mandatoryBaseline, optionalNew}, evidence.Checks)
	require.True(t, evidence.Complete)
	require.Equal(t, time.UTC, evidence.CreatedAt.Location())

	evidence, err = verify.CollectEvidence(
		ledger,
		verify.Policy{TrustedChecks: map[string]verify.CheckPlan{
			"go-unit": {},
			"go-vet":  {},
		}},
		nil,
		testGeneration(),
		[]string{"go-unit"},
	)
	require.NoError(t, err)
	require.False(t, evidence.CreatedAt.IsZero())
}

func TestCollectEvidenceRejectsUntrustedOrIncompleteLedgerState(t *testing.T) {
	t.Parallel()

	policy := verify.Policy{TrustedChecks: map[string]verify.CheckPlan{
		"go-unit": {},
	}}
	generation := testGeneration()
	validRecord := verify.LedgerRecord{
		Evidence: trustedCheckEvidence("go-unit", contract.CheckOutcomePassed, 0, "ok"),
	}
	tests := []struct {
		name       string
		ledger     verify.EvidenceLedger
		policy     verify.Policy
		generation contract.GenerationIdentity
		mandatory  []string
	}{
		{name: "nil ledger", policy: policy, generation: generation},
		{name: "empty catalog", ledger: &memoryEvidenceLedger{}, generation: generation},
		{name: "invalid generation", ledger: &memoryEvidenceLedger{}, policy: policy, generation: contract.GenerationIdentity{}},
		{name: "ledger read error", ledger: &memoryEvidenceLedger{listErr: errors.New("read failed")}, policy: policy, generation: generation},
		{name: "unknown mandatory check", ledger: &memoryEvidenceLedger{records: []verify.LedgerRecord{validRecord}}, policy: policy, generation: generation, mandatory: []string{"unknown"}},
		{name: "missing mandatory result", ledger: &memoryEvidenceLedger{}, policy: policy, generation: generation, mandatory: []string{"go-unit"}},
		{name: "unknown ledger result", ledger: &memoryEvidenceLedger{records: []verify.LedgerRecord{{Evidence: trustedCheckEvidence("unknown", contract.CheckOutcomePassed, 0, "bad")}}}, policy: policy, generation: generation},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := verify.CollectEvidence(
				test.ledger,
				test.policy,
				func() time.Time { return time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC) },
				test.generation,
				test.mandatory,
			)
			require.Error(t, err)
		})
	}

	var unavailable *verify.Runner
	_, err := unavailable.CollectEvidence(generation, []string{"go-unit"})
	require.EqualError(t, err, "evidence collector is unavailable")
}

func TestRunnerClassifiesProcessResultsAndBoundsPublishedOutput(t *testing.T) {
	t.Parallel()

	longOutput := []byte(strings.Repeat("x", contract.MaxCheckOutputExcerptBytes+100))
	invalidUTF8 := []byte{0xff, 'o', 'k'}
	tests := []struct {
		name        string
		result      verify.ProcessResult
		executeErr  error
		wantOutcome contract.CheckOutcome
		wantExit    int
		wantText    string
	}{
		{name: "passed", result: verify.ProcessResult{Stdout: []byte("ok"), Duration: time.Second}, wantOutcome: contract.CheckOutcomePassed},
		{name: "failed", result: verify.ProcessResult{ExitCode: 2, Stderr: []byte("failure"), Duration: time.Second}, wantOutcome: contract.CheckOutcomeFailed, wantExit: 2},
		{name: "executor error", result: verify.ProcessResult{ExitCode: -1}, executeErr: context.Canceled, wantOutcome: contract.CheckOutcomeError, wantExit: -1},
		{name: "zero duration is normalized", result: verify.ProcessResult{Stdout: []byte("quick")}, wantOutcome: contract.CheckOutcomePassed},
		{name: "long output is excerpted", result: verify.ProcessResult{Stdout: longOutput, Duration: time.Second}, wantOutcome: contract.CheckOutcomePassed, wantText: "truncated"},
		{name: "invalid UTF-8 is normalized", result: verify.ProcessResult{Stdout: invalidUTF8, Duration: time.Second}, wantOutcome: contract.CheckOutcomePassed, wantText: "\uFFFDok"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			executor := &recordingExecutor{result: test.result, err: test.executeErr}
			ledger := &memoryEvidenceLedger{}
			runner := newMemoryRunner(t, executor, ledger, map[string]verify.CheckPlan{
				"go-unit": {
					Arguments:      []string{"go", "test", "./internal/..."},
					WorkingDir:     ".",
					TimeoutSeconds: 30,
					MaxOutputBytes: contract.MaxCheckOutputExcerptBytes + 100,
				},
			})
			evidence, err := runner.Run(context.Background(), testGeneration(), "go-unit")
			require.NoError(t, err)
			require.Equal(t, test.wantOutcome, evidence.Outcome)
			require.Equal(t, test.wantExit, evidence.ExitCode)
			require.NotZero(t, evidence.DurationMS)
			require.Equal(t, contentDigest(string(test.result.Stdout)), evidence.StdoutDigest)
			require.LessOrEqual(t, len(evidence.Stdout), contract.MaxCheckOutputExcerptBytes)
			if test.wantText != "" {
				require.Contains(t, evidence.Stdout, test.wantText)
			}
			require.Len(t, ledger.records, 1)
			require.Equal(t, 1, executor.calls)
		})
	}
}

func TestRunnerRejectsOversizedOutputInvalidGenerationAndLedgerFailure(
	t *testing.T,
) {
	t.Parallel()

	executor := &recordingExecutor{result: verify.ProcessResult{
		Stdout:   []byte(strings.Repeat("x", 11)),
		Duration: time.Second,
	}}
	ledger := &memoryEvidenceLedger{}
	runner := newMemoryRunner(t, executor, ledger, map[string]verify.CheckPlan{
		"go-unit": {
			Arguments:      []string{"go", "test"},
			TimeoutSeconds: 1,
			MaxOutputBytes: 10,
		},
	})
	_, err := runner.Run(context.Background(), testGeneration(), "go-unit")
	require.EqualError(t, err, "named-check output exceeds byte limit")
	require.Empty(t, ledger.records)

	_, err = runner.Run(context.Background(), contract.GenerationIdentity{}, "go-unit")
	require.Error(t, err)
	require.Equal(t, 1, executor.calls)

	executor.result = verify.ProcessResult{Duration: time.Second}
	ledger.appendErr = errors.New("append failed")
	_, err = runner.Run(context.Background(), testGeneration(), "go-unit")
	require.ErrorIs(t, err, ledger.appendErr)
}

type memoryEvidenceLedger struct {
	records   []verify.LedgerRecord
	appendErr error
	listErr   error
}

func (ledger *memoryEvidenceLedger) Append(
	generation contract.GenerationIdentity,
	evidence contract.CheckEvidence,
) error {
	if ledger.appendErr != nil {
		return ledger.appendErr
	}
	ledger.records = append(ledger.records, verify.LedgerRecord{
		Sequence:   uint64(len(ledger.records) + 1),
		Generation: generation,
		Evidence:   evidence,
		CreatedAt:  time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
	})
	return nil
}

func (ledger *memoryEvidenceLedger) List(
	_ contract.GenerationIdentity,
) ([]verify.LedgerRecord, error) {
	if ledger.listErr != nil {
		return nil, ledger.listErr
	}
	return append([]verify.LedgerRecord(nil), ledger.records...), nil
}

func newMemoryRunner(
	t *testing.T,
	executor verify.ProcessExecutor,
	ledger verify.EvidenceLedger,
	plans map[string]verify.CheckPlan,
) *verify.Runner {
	t.Helper()
	runner, err := verify.NewRunner(verify.RunnerConfig{
		WorkspaceRoot: t.TempDir(),
		Policy:        verify.Policy{TrustedChecks: plans},
		Executor:      executor,
		Ledger:        ledger,
		Now: func() time.Time {
			return time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
		},
	})
	require.NoError(t, err)
	return runner
}

func validCheckPlan() verify.CheckPlan {
	return verify.CheckPlan{
		Arguments:      []string{"go", "test", "./internal/..."},
		WorkingDir:     ".",
		TimeoutSeconds: 60,
		MaxOutputBytes: 1 << 20,
	}
}

func oneCheckPolicy() verify.Policy {
	return verify.Policy{TrustedChecks: map[string]verify.CheckPlan{
		"go-unit": validCheckPlan(),
	}}
}

func trustedCheckEvidence(
	name string,
	outcome contract.CheckOutcome,
	exitCode int,
	stdout string,
) contract.CheckEvidence {
	return contract.CheckEvidence{
		Name:          name,
		CommandDigest: digest("1"),
		Outcome:       outcome,
		ExitCode:      exitCode,
		DurationMS:    1,
		StdoutDigest:  contentDigest(stdout),
		StderrDigest:  contentDigest(""),
		Stdout:        stdout,
	}
}
