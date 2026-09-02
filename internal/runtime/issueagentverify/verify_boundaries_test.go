package issueagentverify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/stretchr/testify/require"
)

var testIssueAgentNow = time.Date(2026, 9, 2, 3, 4, 5, 0, time.UTC)

func TestVerifyCandidateClassifiesPreExecutionRisk(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		files      []contract.FileChange
		policy     VerificationPolicy
		wantRisk   contract.CandidateRisk
		wantReason string
	}{
		{
			name: "dependency manifest",
			files: []contract.FileChange{
				testIssueAgentUpsert("tools/go.mod", 0o644, []byte("module example\n")),
			},
			wantRisk: contract.CandidateRiskHigh, wantReason: "high-risk",
		},
		{
			name: "configured high-risk subtree",
			files: []contract.FileChange{
				testIssueAgentUpsert("internal/runtime/cluster/state.go", 0o644, []byte("package cluster\n")),
			},
			policy:   VerificationPolicy{HighRiskPaths: []string{"internal/runtime/cluster/"}},
			wantRisk: contract.CandidateRiskHigh, wantReason: "high-risk",
		},
		{
			name: "new executable",
			files: []contract.FileChange{
				testIssueAgentUpsert("scripts/tool.sh", 0o755, []byte("#!/bin/sh\n")),
			},
			wantRisk: contract.CandidateRiskHigh, wantReason: "high-risk",
		},
		{
			name:     "empty candidate",
			wantRisk: contract.CandidateRiskInvestigation, wantReason: "no repository change",
		},
		{
			name: "changed-file policy",
			files: []contract.FileChange{
				testIssueAgentUpsert("a.go", 0o644, []byte("package a\n")),
				testIssueAgentUpsert("b.go", 0o644, []byte("package b\n")),
			},
			policy:   VerificationPolicy{MaxChangedFiles: 1},
			wantRisk: contract.CandidateRiskHigh, wantReason: "changed-file policy",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &issueAgentRunnerStub{}
			policy := testIssueAgentPolicy()
			policy.HighRiskPaths = test.policy.HighRiskPaths
			policy.MaxChangedFiles = test.policy.MaxChangedFiles
			evidence, err := VerifyCandidate(
				context.Background(), t.TempDir(),
				testIssueAgentSnapshot(test.files...), policy, runner,
				testIssueAgentNow,
			)
			require.NoError(t, err)
			require.False(t, evidence.PublicationEligible)
			require.Equal(t, test.wantRisk, evidence.Risk)
			require.Contains(t, evidence.FailureReason, test.wantReason)
			require.Equal(t, []string{"focused", "unit"}, evidence.RequiredSuites)
			require.Empty(t, evidence.Commands)
			require.Zero(t, runner.calls)
			require.NoError(t, contract.ValidateCandidateEvidence(evidence))
		})
	}
}

func TestVerifyCandidateSealsCommandEvidenceAndStopsOnFailure(t *testing.T) {
	t.Parallel()

	checkout := t.TempDir()
	writeIssueAgentFile(t, checkout, "internal/fix.go", []byte("old\n"), 0o644)
	stdout := []byte("focused suite passed\n")
	stderr := []byte("assertion failed\n")
	runner := &issueAgentRunnerStub{results: []VerificationCommandResult{
		{ExitCode: 0, Stdout: stdout, Duration: 11 * time.Millisecond},
		{ExitCode: 2, Stderr: stderr, Duration: 13 * time.Millisecond},
		{ExitCode: 0, Duration: time.Millisecond},
	}}
	policy := testIssueAgentPolicy()
	policy.Commands = []VerificationCommandPlan{
		{Arguments: []string{"go", "test", "./internal/fix"}, WorkingDir: ".", TimeoutSeconds: 60},
		{Arguments: []string{"go", "vet", "./internal/fix"}, WorkingDir: "."},
		{Arguments: []string{"unused"}, WorkingDir: "."},
	}
	evidence, err := VerifyCandidate(
		context.Background(), checkout,
		testIssueAgentSnapshot(
			testIssueAgentUpsert("internal/fix.go", 0o644, []byte("new\n")),
		),
		policy, runner, testIssueAgentNow,
	)
	require.NoError(t, err)
	require.False(t, evidence.PublicationEligible)
	require.Equal(t, "verification command failed", evidence.FailureReason)
	require.Equal(t, 2, runner.calls)
	require.Len(t, evidence.Commands, 2)
	require.Equal(t, 11*time.Millisecond.Milliseconds(), int64(evidence.Commands[0].DurationMS))
	require.Equal(t, testIssueAgentSHA256(stdout), evidence.Commands[0].StdoutDigest)
	require.Equal(t, testIssueAgentSHA256(stderr), evidence.Commands[1].StderrDigest)
	require.NoError(t, contract.ValidateCandidateEvidence(evidence))

	policy.Commands[0].Arguments[0] = "mutated-after-verification"
	stdout[0] = 'X'
	require.Equal(t, "go", evidence.Commands[0].Arguments[0])
	require.Equal(t, testIssueAgentSHA256([]byte("focused suite passed\n")), evidence.Commands[0].StdoutDigest)
}

func TestVerifyCandidateRejectsInvalidRunnerAndPlanResults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		plan    VerificationCommandPlan
		result  VerificationCommandResult
		runErr  error
		wantErr string
	}{
		{
			name: "empty plan", plan: VerificationCommandPlan{},
			result:  VerificationCommandResult{Duration: time.Millisecond},
			wantErr: "Verifier command plan is invalid",
		},
		{
			name:    "runner error",
			plan:    VerificationCommandPlan{Arguments: []string{"go", "test"}, WorkingDir: "."},
			runErr:  errors.New("unavailable"),
			wantErr: "Verifier command runner failed",
		},
		{
			name:    "zero duration",
			plan:    VerificationCommandPlan{Arguments: []string{"go", "test"}, WorkingDir: "."},
			result:  VerificationCommandResult{ExitCode: 0},
			wantErr: "Verifier command result is invalid",
		},
		{
			name: "oversized stdout",
			plan: VerificationCommandPlan{Arguments: []string{"go", "test"}, WorkingDir: "."},
			result: VerificationCommandResult{
				Duration: time.Millisecond, Stdout: make([]byte, (1<<20)+1),
			},
			wantErr: "Verifier command result is invalid",
		},
		{
			name: "oversized stderr",
			plan: VerificationCommandPlan{Arguments: []string{"go", "test"}, WorkingDir: "."},
			result: VerificationCommandResult{
				Duration: time.Millisecond, Stderr: make([]byte, (1<<20)+1),
			},
			wantErr: "Verifier command result is invalid",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			checkout := t.TempDir()
			policy := testIssueAgentPolicy()
			policy.Commands = []VerificationCommandPlan{test.plan}
			runner := &issueAgentRunnerStub{
				results: []VerificationCommandResult{test.result},
				err:     test.runErr,
			}
			_, err := VerifyCandidate(
				context.Background(), checkout,
				testIssueAgentSnapshot(
					testIssueAgentUpsert("internal/new.go", 0o644, []byte("package internal\n")),
				),
				policy, runner, testIssueAgentNow,
			)
			require.EqualError(t, err, test.wantErr)
		})
	}
}

func TestVerifyCandidateRejectsInvalidAuthorityBeforeExecution(t *testing.T) {
	t.Parallel()

	runner := &issueAgentRunnerStub{}
	_, err := VerifyCandidate(
		nil, t.TempDir(), testIssueAgentSnapshot(),
		testIssueAgentPolicy(), runner, testIssueAgentNow,
	)
	require.EqualError(t, err, "Verifier input is invalid")

	invalidSnapshot := testIssueAgentSnapshot()
	invalidSnapshot.SchemaVersion = 1
	_, err = VerifyCandidate(
		context.Background(), t.TempDir(), invalidSnapshot,
		testIssueAgentPolicy(), runner, testIssueAgentNow,
	)
	require.ErrorContains(t, err, "invalid Candidate Snapshot identity")

	policy := testIssueAgentPolicy()
	policy.Commands = nil
	_, err = VerifyCandidate(
		context.Background(), t.TempDir(),
		testIssueAgentSnapshot(
			testIssueAgentUpsert("internal/new.go", 0o644, []byte("package internal\n")),
		),
		policy, runner, testIssueAgentNow,
	)
	require.EqualError(t, err, "Verifier test plan is empty")
	require.Zero(t, runner.calls)
}

func TestVerificationPlanRejectsAmbiguousExecutionInputs(t *testing.T) {
	t.Parallel()

	tooManyArguments := make([]string, 129)
	for index := range tooManyArguments {
		tooManyArguments[index] = "x"
	}
	tests := []struct {
		name string
		plan VerificationCommandPlan
	}{
		{name: "no arguments", plan: VerificationCommandPlan{WorkingDir: "."}},
		{name: "too many arguments", plan: VerificationCommandPlan{Arguments: tooManyArguments, WorkingDir: "."}},
		{name: "no working directory", plan: VerificationCommandPlan{Arguments: []string{"go"}}},
		{name: "absolute working directory", plan: VerificationCommandPlan{Arguments: []string{"go"}, WorkingDir: "/tmp"}},
		{name: "unclean working directory", plan: VerificationCommandPlan{Arguments: []string{"go"}, WorkingDir: "internal/../pkg"}},
		{name: "parent working directory", plan: VerificationCommandPlan{Arguments: []string{"go"}, WorkingDir: ".."}},
		{name: "escaping working directory", plan: VerificationCommandPlan{Arguments: []string{"go"}, WorkingDir: "../pkg"}},
		{name: "timeout", plan: VerificationCommandPlan{Arguments: []string{"go"}, WorkingDir: ".", TimeoutSeconds: uint64((90 * time.Minute).Seconds()) + 1}},
		{name: "empty argument", plan: VerificationCommandPlan{Arguments: []string{"go", ""}, WorkingDir: "."}},
		{name: "long argument", plan: VerificationCommandPlan{Arguments: []string{"go", strings.Repeat("x", 4097)}, WorkingDir: "."}},
		{name: "nul argument", plan: VerificationCommandPlan{Arguments: []string{"go", "bad\x00arg"}, WorkingDir: "."}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Error(t, validateVerificationPlan(test.plan))
		})
	}
	require.NoError(t, validateVerificationPlan(VerificationCommandPlan{
		Arguments:  []string{"go", "test", "./internal/..."},
		WorkingDir: "internal", TimeoutSeconds: 90 * 60,
	}))
}

func TestApplyCandidateChangeSetRealizesCompleteRegularFileDiff(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeIssueAgentFile(t, root, "delete.txt", []byte("old\n"), 0o644)
	writeIssueAgentFile(t, root, "scripts/tool.sh", []byte("old\n"), 0o644)
	before, err := scanCandidateTree(root)
	require.NoError(t, err)
	changeSet := contract.ChangeSet{Files: []contract.FileChange{
		{Path: "delete.txt", Operation: contract.FileOperationDelete},
		testIssueAgentUpsert("scripts/tool.sh", 0o755, []byte("#!/bin/sh\n")),
		testIssueAgentUpsert("src/new.go", 0o644, []byte("package src\n")),
	}}
	require.NoError(t, applyCandidateChangeSet(root, changeSet))
	require.NoFileExists(t, filepath.Join(root, "delete.txt"))
	info, err := os.Stat(filepath.Join(root, "scripts", "tool.sh"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
	require.NoError(t, verifyAppliedCandidate(root, before, changeSet))

	writeIssueAgentFile(t, root, "src/new.go", []byte("tampered\n"), 0o644)
	err = verifyAppliedCandidate(root, before, changeSet)
	require.ErrorContains(t, err, "unexpected content")
}

func TestApplyCandidateChangeSetRejectsUnsafeFilesystemTargets(t *testing.T) {
	t.Parallel()

	t.Run("unsafe root", func(t *testing.T) {
		actual := t.TempDir()
		rootLink := filepath.Join(t.TempDir(), "checkout")
		require.NoError(t, os.Symlink(actual, rootLink))
		err := applyCandidateChangeSet(rootLink, contract.ChangeSet{})
		require.EqualError(t, err, "Verifier checkout root is unsafe")
	})
	t.Run("missing deletion", func(t *testing.T) {
		err := applyCandidateChangeSet(t.TempDir(), contract.ChangeSet{Files: []contract.FileChange{{
			Path: "missing", Operation: contract.FileOperationDelete,
		}}})
		require.ErrorContains(t, err, "is not a regular file")
	})
	t.Run("symlink target", func(t *testing.T) {
		root := t.TempDir()
		writeIssueAgentFile(t, root, "real", []byte("data"), 0o644)
		require.NoError(t, os.Symlink("real", filepath.Join(root, "link")))
		err := applyCandidateChangeSet(root, contract.ChangeSet{Files: []contract.FileChange{
			testIssueAgentUpsert("link", 0o644, []byte("replacement")),
		}})
		require.ErrorContains(t, err, "is not regular")
	})
	t.Run("invalid encoded content", func(t *testing.T) {
		err := applyCandidateChangeSet(t.TempDir(), contract.ChangeSet{Files: []contract.FileChange{{
			Path: "bad", Operation: contract.FileOperationUpsert,
			Mode: contract.FileModeRegular, ContentBase64: "not-base64",
		}}})
		require.ErrorContains(t, err, "canonical base64")
	})
	t.Run("unknown operation", func(t *testing.T) {
		err := applyCandidateChangeSet(t.TempDir(), contract.ChangeSet{Files: []contract.FileChange{{
			Path: "bad", Operation: contract.FileOperation("rename"),
		}}})
		require.EqualError(t, err, "candidate operation is invalid")
	})
	t.Run("symlink directory traversal", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(root, "linked")))
		_, err := prepareCandidateTarget(root, "linked/file")
		require.EqualError(t, err, "candidate path traverses unsafe directory")
	})
	for _, unsafePath := range []string{"", "/absolute", "a/../b", ".git", ".git/config"} {
		t.Run("unsafe path "+strings.ReplaceAll(unsafePath, "/", "_"), func(t *testing.T) {
			_, err := prepareCandidateTarget(t.TempDir(), unsafePath)
			require.EqualError(t, err, "candidate target path is unsafe")
		})
	}
}

func testIssueAgentSnapshot(files ...contract.FileChange) CandidateSnapshot {
	files = slices.Clone(files)
	slices.SortFunc(files, func(left, right contract.FileChange) int {
		return strings.Compare(left.Path, right.Path)
	})
	return CandidateSnapshot{
		SchemaVersion: 2,
		TaskID:        testIssueAgentTaskID,
		BaseSHA:       testIssueAgentBaseSHA,
		ChangeSet:     contract.ChangeSet{Files: files},
	}
}

func testIssueAgentUpsert(
	repositoryPath string,
	mode os.FileMode,
	content []byte,
) contract.FileChange {
	fileMode := contract.FileModeRegular
	if mode == 0o755 {
		fileMode = contract.FileModeExecutable
	}
	return contract.FileChange{
		Path: repositoryPath, Operation: contract.FileOperationUpsert,
		Mode: fileMode, ContentBase64: contract.EncodeFileContent(content),
	}
}

func testIssueAgentPolicy() VerificationPolicy {
	return VerificationPolicy{
		Repository: "WuKongIM/WuKongIM", IssueNumber: 42,
		RequiredSuites: []string{"unit", "focused", "unit"},
		Commands: []VerificationCommandPlan{{
			Arguments:  []string{"go", "test", "./internal/..."},
			WorkingDir: ".", TimeoutSeconds: 60,
		}},
		MaxChangedFiles: 8,
	}
}

func testIssueAgentSHA256(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type issueAgentRunnerStub struct {
	results []VerificationCommandResult
	err     error
	calls   int
}

func (runner *issueAgentRunnerStub) Run(
	context.Context,
	VerificationCommandPlan,
) (VerificationCommandResult, error) {
	index := runner.calls
	runner.calls++
	if runner.err != nil {
		return VerificationCommandResult{}, runner.err
	}
	if index >= len(runner.results) {
		return VerificationCommandResult{
			ExitCode: 0, Duration: time.Millisecond,
		}, nil
	}
	return runner.results[index], nil
}
