package issueagentverify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

// VerificationCommandPlan is one trusted argv command, never model-authored.
type VerificationCommandPlan struct {
	Arguments      []string `json:"arguments"`
	WorkingDir     string   `json:"working_dir"`
	TimeoutSeconds uint64   `json:"timeout_seconds"`
}

// VerificationCommandResult is bounded process output from a trusted runner.
type VerificationCommandResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
	Duration time.Duration
}

// VerificationRunner executes trusted plans without Publisher credentials.
type VerificationRunner interface {
	Run(context.Context, VerificationCommandPlan) (VerificationCommandResult, error)
}

// VerificationPolicy is protected diff and test policy.
type VerificationPolicy struct {
	Repository      string                    `json:"repository"`
	IssueNumber     int64                     `json:"issue_number"`
	ProtectedPaths  []string                  `json:"protected_paths"`
	HighRiskPaths   []string                  `json:"high_risk_paths"`
	RequiredSuites  []string                  `json:"required_suites"`
	Commands        []VerificationCommandPlan `json:"commands"`
	MaxChangedFiles int                       `json:"max_changed_files"`
}

// VerifyCandidate classifies a captured candidate before executing trusted tests.
func VerifyCandidate(
	ctx context.Context,
	cleanCheckoutRoot string,
	snapshot CandidateSnapshot,
	policy VerificationPolicy,
	runner VerificationRunner,
	now time.Time,
) (contract.CandidateEvidence, error) {
	if ctx == nil || runner == nil ||
		cleanCheckoutRoot == "" ||
		!path.IsAbs(strings.ReplaceAll(cleanCheckoutRoot, "\\", "/")) ||
		policy.Repository == "" || policy.IssueNumber <= 0 ||
		len(policy.RequiredSuites) == 0 ||
		now.IsZero() || now.Location() != time.UTC {
		return contract.CandidateEvidence{}, errors.New("Verifier input is invalid")
	}
	if err := ValidateCandidateSnapshot(snapshot); err != nil {
		return contract.CandidateEvidence{}, err
	}
	candidateDigest, err := CandidateSnapshotDigest(snapshot)
	if err != nil {
		return contract.CandidateEvidence{}, err
	}
	changeSetDigest, err := contract.ChangeSetDigest(snapshot.ChangeSet)
	if err != nil {
		return contract.CandidateEvidence{}, err
	}
	requiredSuites := slices.Clone(policy.RequiredSuites)
	slices.Sort(requiredSuites)
	requiredSuites = slices.Compact(requiredSuites)

	for _, file := range snapshot.ChangeSet.Files {
		if matchesProtectedPath(file.Path, policy.ProtectedPaths) ||
			isRepositoryInstruction(file.Path) {
			return rejectedCandidateEvidence(
				policy, snapshot, candidateDigest, changeSetDigest,
				requiredSuites, contract.CandidateRiskHigh,
				fmt.Sprintf("candidate changes protected path %q", file.Path),
				now,
			)
		}
		if matchesProtectedPath(file.Path, policy.HighRiskPaths) ||
			isDependencyManifest(file.Path) ||
			file.Operation == contract.FileOperationUpsert &&
				file.Mode == contract.FileModeExecutable {
			return rejectedCandidateEvidence(
				policy, snapshot, candidateDigest, changeSetDigest,
				requiredSuites, contract.CandidateRiskHigh,
				fmt.Sprintf("candidate changes high-risk path %q", file.Path),
				now,
			)
		}
	}
	if len(snapshot.ChangeSet.Files) == 0 {
		return rejectedCandidateEvidence(
			policy, snapshot, candidateDigest, changeSetDigest,
			requiredSuites, contract.CandidateRiskInvestigation,
			"candidate contains no repository change", now,
		)
	}
	if policy.MaxChangedFiles > 0 &&
		len(snapshot.ChangeSet.Files) > policy.MaxChangedFiles {
		return rejectedCandidateEvidence(
			policy, snapshot, candidateDigest, changeSetDigest,
			requiredSuites, contract.CandidateRiskHigh,
			"candidate exceeds changed-file policy", now,
		)
	}
	if len(policy.Commands) == 0 {
		return contract.CandidateEvidence{}, errors.New("Verifier test plan is empty")
	}
	baselineEntries, err := scanCandidateTree(cleanCheckoutRoot)
	if err != nil {
		return contract.CandidateEvidence{}, err
	}
	if err := applyCandidateChangeSet(cleanCheckoutRoot, snapshot.ChangeSet); err != nil {
		return contract.CandidateEvidence{}, err
	}
	if err := verifyAppliedCandidate(
		cleanCheckoutRoot,
		baselineEntries,
		snapshot.ChangeSet,
	); err != nil {
		return contract.CandidateEvidence{}, err
	}

	commands := make([]contract.VerificationCommand, 0, len(policy.Commands))
	failureReason := ""
	for _, command := range policy.Commands {
		if err := validateVerificationPlan(command); err != nil {
			return contract.CandidateEvidence{}, err
		}
		result, err := runner.Run(ctx, command)
		if err != nil {
			return contract.CandidateEvidence{}, errors.New("Verifier command runner failed")
		}
		if result.Duration <= 0 ||
			len(result.Stdout) > 1<<20 ||
			len(result.Stderr) > 1<<20 {
			return contract.CandidateEvidence{}, errors.New("Verifier command result is invalid")
		}
		stdoutSum := sha256.Sum256(result.Stdout)
		stderrSum := sha256.Sum256(result.Stderr)
		commands = append(commands, contract.VerificationCommand{
			Arguments:  slices.Clone(command.Arguments),
			WorkingDir: command.WorkingDir, ExitCode: result.ExitCode,
			StdoutDigest: "sha256:" + hex.EncodeToString(stdoutSum[:]),
			StderrDigest: "sha256:" + hex.EncodeToString(stderrSum[:]),
			DurationMS:   uint64(result.Duration.Milliseconds()),
		})
		if result.ExitCode != 0 {
			failureReason = "verification command failed"
			break
		}
	}
	if err := verifyAppliedCandidate(
		cleanCheckoutRoot,
		baselineEntries,
		snapshot.ChangeSet,
	); err != nil {
		return contract.CandidateEvidence{}, errors.New(
			"verification command changed the candidate tree",
		)
	}
	evidence := contract.CandidateEvidence{
		SchemaVersion: 2, Repository: policy.Repository,
		IssueNumber: policy.IssueNumber, TaskID: snapshot.TaskID,
		BaseSHA: snapshot.BaseSHA, CandidateDigest: candidateDigest,
		ChangeSetDigest: changeSetDigest, Risk: contract.CandidateRiskLow,
		PublicationEligible: failureReason == "",
		RequiredSuites:      requiredSuites, Commands: commands,
		FailureReason: failureReason, CreatedAt: now,
	}
	if err := contract.ValidateCandidateEvidence(evidence); err != nil {
		return contract.CandidateEvidence{}, err
	}
	return evidence, nil
}

func matchesProtectedPath(repositoryPath string, protected []string) bool {
	for _, prefix := range protected {
		prefix = strings.TrimSuffix(prefix, "/")
		if repositoryPath == prefix ||
			strings.HasPrefix(repositoryPath, prefix+"/") {
			return true
		}
	}
	return false
}

func rejectedCandidateEvidence(
	policy VerificationPolicy,
	snapshot CandidateSnapshot,
	candidateDigest string,
	changeSetDigest string,
	requiredSuites []string,
	risk contract.CandidateRisk,
	reason string,
	now time.Time,
) (contract.CandidateEvidence, error) {
	evidence := contract.CandidateEvidence{
		SchemaVersion: 2, Repository: policy.Repository,
		IssueNumber: policy.IssueNumber, TaskID: snapshot.TaskID,
		BaseSHA: snapshot.BaseSHA, CandidateDigest: candidateDigest,
		ChangeSetDigest: changeSetDigest, Risk: risk,
		PublicationEligible: false, RequiredSuites: requiredSuites,
		Commands: []contract.VerificationCommand{}, FailureReason: reason,
		CreatedAt: now,
	}
	if err := contract.ValidateCandidateEvidence(evidence); err != nil {
		return contract.CandidateEvidence{}, err
	}
	return evidence, nil
}

func isDependencyManifest(repositoryPath string) bool {
	switch path.Base(repositoryPath) {
	case "go.mod", "go.sum", "package.json", "package-lock.json",
		"pnpm-lock.yaml", "yarn.lock":
		return true
	default:
		return false
	}
}

func isRepositoryInstruction(repositoryPath string) bool {
	switch path.Base(repositoryPath) {
	case "AGENTS.md", "FLOW.md":
		return true
	default:
		return false
	}
}

func validateVerificationPlan(plan VerificationCommandPlan) error {
	if len(plan.Arguments) == 0 || len(plan.Arguments) > 128 ||
		plan.WorkingDir == "" ||
		plan.WorkingDir != "." &&
			(path.IsAbs(plan.WorkingDir) ||
				path.Clean(plan.WorkingDir) != plan.WorkingDir ||
				plan.WorkingDir == ".." ||
				strings.HasPrefix(plan.WorkingDir, "../")) ||
		plan.TimeoutSeconds > uint64((90*time.Minute).Seconds()) {
		return errors.New("Verifier command plan is invalid")
	}
	for _, argument := range plan.Arguments {
		if argument == "" || len(argument) > 4096 ||
			strings.ContainsRune(argument, '\x00') {
			return errors.New("Verifier command argument is invalid")
		}
	}
	return nil
}

func applyCandidateChangeSet(
	root string,
	changeSet contract.ChangeSet,
) error {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Verifier checkout root is unsafe")
	}
	for _, change := range changeSet.Files {
		target, err := prepareCandidateTarget(root, change.Path)
		if err != nil {
			return err
		}
		switch change.Operation {
		case contract.FileOperationDelete:
			targetInfo, err := os.Lstat(target)
			if err != nil || !targetInfo.Mode().IsRegular() {
				return fmt.Errorf("candidate deletion %q is not a regular file", change.Path)
			}
			if err := os.Remove(target); err != nil {
				return errors.New("delete candidate file")
			}
		case contract.FileOperationUpsert:
			content, err := contract.DecodeFileContent(change)
			if err != nil {
				return err
			}
			if targetInfo, err := os.Lstat(target); err == nil {
				if !targetInfo.Mode().IsRegular() {
					return fmt.Errorf("candidate target %q is not regular", change.Path)
				}
			} else if !os.IsNotExist(err) {
				return errors.New("inspect candidate target")
			}
			mode := os.FileMode(0o644)
			if change.Mode == contract.FileModeExecutable {
				mode = 0o755
			}
			parent := filepath.Dir(target)
			temporary, err := os.CreateTemp(parent, ".issue-agent-verify-*")
			if err != nil {
				return errors.New("create candidate temporary file")
			}
			temporaryName := temporary.Name()
			cleanup := func() {
				_ = temporary.Close()
				_ = os.Remove(temporaryName)
			}
			if _, err := temporary.Write(content); err != nil {
				cleanup()
				return errors.New("write candidate temporary file")
			}
			if err := temporary.Chmod(mode); err != nil {
				cleanup()
				return errors.New("set candidate file mode")
			}
			if err := temporary.Close(); err != nil {
				_ = os.Remove(temporaryName)
				return errors.New("close candidate temporary file")
			}
			if err := os.Rename(temporaryName, target); err != nil {
				_ = os.Remove(temporaryName)
				return errors.New("replace candidate file")
			}
			written, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(written, content) {
				return errors.New("candidate file verification failed")
			}
		default:
			return errors.New("candidate operation is invalid")
		}
	}
	return nil
}

func prepareCandidateTarget(root, repositoryPath string) (string, error) {
	if repositoryPath == "" || path.IsAbs(repositoryPath) ||
		path.Clean(repositoryPath) != repositoryPath ||
		repositoryPath == ".git" ||
		strings.HasPrefix(repositoryPath, ".git/") {
		return "", errors.New("candidate target path is unsafe")
	}
	parts := strings.Split(repositoryPath, "/")
	current := root
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, filepath.FromSlash(part))
		info, err := os.Lstat(current)
		switch {
		case err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0:
		case os.IsNotExist(err):
			if err := os.Mkdir(current, 0o755); err != nil {
				return "", errors.New("create candidate directory")
			}
		default:
			return "", errors.New("candidate path traverses unsafe directory")
		}
	}
	return filepath.Join(root, filepath.FromSlash(repositoryPath)), nil
}

func verifyAppliedCandidate(
	root string,
	before map[string]treeEntry,
	expected contract.ChangeSet,
) error {
	after, err := scanCandidateTree(root)
	if err != nil {
		return err
	}
	expectedByPath := make(map[string]contract.FileChange, len(expected.Files))
	for _, change := range expected.Files {
		expectedByPath[change.Path] = change
	}
	paths := make([]string, 0, len(before)+len(after))
	for repositoryPath := range before {
		paths = append(paths, repositoryPath)
	}
	for repositoryPath := range after {
		if _, exists := before[repositoryPath]; !exists {
			paths = append(paths, repositoryPath)
		}
	}
	slices.Sort(paths)
	observed := make(map[string]struct{})
	for _, repositoryPath := range paths {
		oldEntry, hadOld := before[repositoryPath]
		newEntry, hasNew := after[repositoryPath]
		if hadOld == hasNew && oldEntry == newEntry {
			continue
		}
		change, expectedChange := expectedByPath[repositoryPath]
		if !expectedChange {
			return fmt.Errorf(
				"candidate application changed unexpected path %q",
				repositoryPath,
			)
		}
		switch change.Operation {
		case contract.FileOperationDelete:
			if hasNew {
				return fmt.Errorf(
					"candidate deletion %q remains present",
					repositoryPath,
				)
			}
		case contract.FileOperationUpsert:
			if !hasNew || newEntry.kind != "regular" ||
				newEntry.mode != change.Mode {
				return fmt.Errorf(
					"candidate upsert %q has unexpected type or mode",
					repositoryPath,
				)
			}
			content, err := contract.DecodeFileContent(change)
			if err != nil {
				return err
			}
			if newEntry.size != int64(len(content)) ||
				newEntry.digest != sha256.Sum256(content) {
				return fmt.Errorf(
					"candidate upsert %q has unexpected content",
					repositoryPath,
				)
			}
		default:
			return errors.New("candidate verification operation is invalid")
		}
		observed[repositoryPath] = struct{}{}
	}
	if len(observed) != len(expected.Files) {
		return errors.New("candidate application did not realize the complete ChangeSet")
	}
	return nil
}
