package issueagentworker

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

// ModelOutput is the untrusted model proposal plus provider-metered usage.
type ModelOutput struct {
	Result issueagent.AgentResult
	Usage  issueagent.ModelUsage
}

// ModelRunner invokes exactly one policy-selected Adapter through the Broker.
type ModelRunner func(
	context.Context,
	issueagent.TaskEnvelope,
	[]byte,
	*Broker,
) (ModelOutput, error)

// WorkerConfig fixes one complete credential-free phase attempt.
type WorkerConfig struct {
	Task             issueagent.TaskEnvelope
	Prompt           []byte
	Policy           []byte
	Workspace        string
	Runner           ToolRunner
	Model            ModelRunner
	MaxArtifactBytes int64
	Binaries         BinaryEvidence
}

// BinaryEvidence records the exact trusted binaries mounted for a
// two-baseline reproduction.
type BinaryEvidence struct {
	AffectedSHA256      string `json:"affected_sha256,omitempty"`
	DiagnosisBaseSHA256 string `json:"diagnosis_base_sha256,omitempty"`
}

// Artifact is the sanitized publishable Worker output.
type Artifact struct {
	SchemaVersion int                     `json:"schema_version"`
	Task          issueagent.TaskEnvelope `json:"task"`
	Result        issueagent.AgentResult  `json:"result"`
	Tools         []ToolEvidence          `json:"tools"`
	Binaries      BinaryEvidence          `json:"binaries"`
	SHA256        string                  `json:"sha256"`
}

// Worker verifies inputs, runs one Adapter, and derives trusted local evidence.
type Worker struct {
	config WorkerConfig
	root   string
}

// NewWorker validates immutable task and workspace inputs.
func NewWorker(config WorkerConfig) (*Worker, error) {
	if err := issueagent.ValidateTaskEnvelope(config.Task); err != nil {
		return nil, err
	}
	if config.Runner == nil || config.Model == nil ||
		len(config.Prompt) == 0 || len(config.Prompt) > 128<<10 ||
		len(config.Policy) == 0 || len(config.Policy) > 1<<20 ||
		config.MaxArtifactBytes <= 0 || config.MaxArtifactBytes > 16<<20 ||
		digest(config.Prompt) != config.Task.PromptDigest ||
		digest(config.Policy) != config.Task.PolicyDigest {
		return nil, errors.New("Worker configuration is invalid")
	}
	if config.Task.Phase == issueagent.PhaseReproduce &&
		(!artifactDigestPattern.MatchString(config.Binaries.AffectedSHA256) ||
			!artifactDigestPattern.MatchString(config.Binaries.DiagnosisBaseSHA256)) {
		return nil, errors.New("reproduction binary evidence is invalid")
	}
	root, err := filepath.Abs(config.Workspace)
	if err != nil {
		return nil, errors.New("resolve Worker workspace")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, errors.New("Worker workspace is unavailable")
	}
	return &Worker{config: config, root: root}, nil
}

// Run executes one bounded phase without accepting model-authored evidence.
func (worker *Worker) Run(ctx context.Context) (Artifact, error) {
	if worker == nil {
		return Artifact{}, errors.New("Worker is nil")
	}
	if err := worker.verifyInstructions(); err != nil {
		return Artifact{}, err
	}
	if worker.config.Task.Phase == issueagent.PhaseFix ||
		worker.config.Task.Phase == issueagent.PhaseAddressReview {
		scratch := filepath.Join(worker.root, ".issue-agent-tmp")
		if err := os.Mkdir(scratch, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return Artifact{}, errors.New("create remediation scratch directory")
		}
		defer os.RemoveAll(scratch)
	}
	before, err := snapshotWorkspace(worker.root)
	if err != nil {
		return Artifact{}, err
	}
	allowedWrites := worker.config.Task.AllowedPaths
	if worker.config.Task.Phase == issueagent.PhaseDiagnose {
		allowedWrites = nil
	}
	broker, err := NewBroker(BrokerConfig{
		Workspace:         worker.root,
		AllowedWritePaths: allowedWrites,
		AllowedCommands:   worker.config.Task.AllowedCommands,
		MaxFileBytes:      worker.config.Task.Limits.MaxFileBytes,
		MaxFiles:          worker.config.Task.Limits.MaxFiles,
		MaxTotalBytes:     worker.config.Task.Limits.MaxTotalBytes,
		MaxOutputBytes:    worker.config.Task.Limits.MaxOutputBytes,
	}, worker.config.Runner)
	if err != nil {
		return Artifact{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, worker.config.Task.Limits.WallTime)
	defer cancel()
	modelOutput, err := worker.config.Model(
		runCtx, worker.config.Task, append([]byte(nil), worker.config.Prompt...), broker,
	)
	providerFailed := false
	if err != nil {
		providerFailed = true
		modelOutput = ModelOutput{
			Result: issueagent.AgentResult{
				SchemaVersion: 1, Repository: worker.config.Task.Repository,
				IssueNumber:     worker.config.Task.IssueNumber,
				Generation:      worker.config.Task.Generation,
				Sequence:        worker.config.Task.Sequence,
				OperationID:     worker.config.Task.OperationID,
				Phase:           worker.config.Task.Phase,
				Status:          issueagent.ResultStatusFailed,
				RequestedState:  issueagent.StateReadyForHuman,
				RequestedAction: issueagent.ActionWaitForHuman,
				Failure: &issueagent.Failure{
					Class:   issueagent.FailureProvider,
					Summary: providerFailureSummary(err),
				},
			},
			Usage: issueagent.ModelUsage{
				Provider: worker.config.Task.Provider,
				Model:    worker.config.Task.Model,
			},
		}
	}
	after, err := snapshotWorkspace(worker.root)
	if err != nil {
		return Artifact{}, err
	}
	changeSet, err := deriveChangeSet(before, after)
	if err != nil {
		return Artifact{}, err
	}
	if providerFailed {
		// A partial provider attempt can mutate only its disposable workspace.
		// No such mutation is eligible for trusted publication.
		changeSet = issueagent.ChangeSet{}
	}
	if err := issueagent.ValidateChangeSet(changeSet, issueagent.ChangeSetLimits{
		MaxFiles:      worker.config.Task.Limits.MaxFiles,
		MaxFileBytes:  int(worker.config.Task.Limits.MaxFileBytes),
		MaxTotalBytes: int(worker.config.Task.Limits.MaxTotalBytes),
		MaxDeletions:  worker.config.Task.Limits.MaxFiles,
	}); err != nil {
		return Artifact{}, err
	}
	if worker.config.Task.Phase == issueagent.PhaseDiagnose &&
		len(changeSet.Files) != 0 {
		return Artifact{}, errors.New("diagnosis phase changed repository files")
	}
	if err := validatePhaseProposal(
		worker.config.Task.Phase,
		modelOutput.Result.Status,
		modelOutput.Result.RequestedState,
		modelOutput.Result.RequestedAction,
	); err != nil {
		return Artifact{}, err
	}
	toolEvidence := broker.Evidence()
	if err := validateReproductionEvidence(
		worker.config.Task,
		modelOutput.Result,
		changeSet,
		toolEvidence,
	); err != nil {
		return Artifact{}, err
	}
	if err := validateFixedE2EEvidence(
		worker.config.Task, modelOutput.Result, toolEvidence,
	); err != nil {
		return Artifact{}, err
	}
	commands := make([]issueagent.CommandEvidence, 0)
	for _, evidence := range toolEvidence {
		if evidence.Tool != "command_run" {
			continue
		}
		if evidence.DurationMS <= 0 {
			return Artifact{}, errors.New("Worker command evidence has invalid duration")
		}
		commands = append(commands, issueagent.CommandEvidence{
			Executable: evidence.Executable,
			Arguments:  append([]string(nil), evidence.Arguments...),
			WorkingDir: evidence.Path, ExitCode: evidence.ExitCode,
			StdoutSHA256:    evidence.OutputSHA256,
			StderrSHA256:    evidence.ErrorSHA256,
			DurationMS:      evidence.DurationMS,
			AssertionSHA256: evidence.AssertionSHA256,
		})
	}
	evidenceJSON, err := json.Marshal(toolEvidence)
	if err != nil {
		return Artifact{}, errors.New("encode Worker evidence")
	}
	result := modelOutput.Result
	result.ChangeSet = changeSet
	result.Evidence = issueagent.EvidenceManifest{
		ArtifactSHA256: digest(evidenceJSON), Commands: commands,
	}
	if result.Diagnosis != nil {
		diagnosis := *result.Diagnosis
		diagnosis.EvidenceSHA256 = result.Evidence.ArtifactSHA256
		result.Diagnosis = &diagnosis
	}
	result.Usage = modelOutput.Usage
	if err := issueagent.ValidateAgentResult(result, worker.config.Task); err != nil {
		return Artifact{}, err
	}
	unsigned := struct {
		SchemaVersion int                     `json:"schema_version"`
		Task          issueagent.TaskEnvelope `json:"task"`
		Result        issueagent.AgentResult  `json:"result"`
		Tools         []ToolEvidence          `json:"tools"`
		Binaries      BinaryEvidence          `json:"binaries"`
	}{
		SchemaVersion: 1, Task: worker.config.Task,
		Result: result, Tools: toolEvidence, Binaries: worker.config.Binaries,
	}
	encoded, err := json.Marshal(unsigned)
	if err != nil || int64(len(encoded)) > worker.config.MaxArtifactBytes {
		return Artifact{}, errors.New("Worker Artifact exceeds byte limit")
	}
	return Artifact{
		SchemaVersion: 1, Task: worker.config.Task, Result: result,
		Tools: toolEvidence, Binaries: worker.config.Binaries,
		SHA256: digest(encoded),
	}, nil
}

func providerFailureSummary(err error) string {
	const summary = "The selected model provider did not complete this bounded attempt."
	var classified interface {
		SafeProviderFailureCode() string
	}
	if !errors.As(err, &classified) {
		return summary
	}
	switch code := classified.SafeProviderFailureCode(); code {
	case "authentication", "quota", "rate_limit", "invalid_request",
		"model_unavailable", "network", "provider_unavailable",
		"output_limit", "codex_process":
		return summary + " Safe diagnostic: " + code + "."
	default:
		return summary
	}
}

// ValidateArtifact replays every deterministic Worker-side validation before a
// Publisher trusts an uploaded JSON artifact.
func ValidateArtifact(artifact Artifact) error {
	if artifact.SchemaVersion != 1 ||
		!artifactDigestPattern.MatchString(artifact.SHA256) ||
		len(artifact.Tools) > 2048 {
		return errors.New("Worker Artifact identity is invalid")
	}
	if err := issueagent.ValidateAgentResult(artifact.Result, artifact.Task); err != nil {
		return err
	}
	toolJSON, err := json.Marshal(artifact.Tools)
	if err != nil || artifact.Result.Evidence.ArtifactSHA256 != digest(toolJSON) {
		return errors.New("Worker Artifact evidence digest is invalid")
	}
	if artifact.Task.Phase == issueagent.PhaseReproduce &&
		(!artifactDigestPattern.MatchString(artifact.Binaries.AffectedSHA256) ||
			!artifactDigestPattern.MatchString(artifact.Binaries.DiagnosisBaseSHA256)) {
		return errors.New("Worker Artifact binary evidence is invalid")
	}
	if err := validateReproductionEvidence(
		artifact.Task, artifact.Result, artifact.Result.ChangeSet, artifact.Tools,
	); err != nil {
		return err
	}
	if err := validateFixedE2EEvidence(
		artifact.Task, artifact.Result, artifact.Tools,
	); err != nil {
		return err
	}
	unsigned := struct {
		SchemaVersion int                     `json:"schema_version"`
		Task          issueagent.TaskEnvelope `json:"task"`
		Result        issueagent.AgentResult  `json:"result"`
		Tools         []ToolEvidence          `json:"tools"`
		Binaries      BinaryEvidence          `json:"binaries"`
	}{
		SchemaVersion: artifact.SchemaVersion, Task: artifact.Task,
		Result: artifact.Result, Tools: artifact.Tools, Binaries: artifact.Binaries,
	}
	encoded, err := json.Marshal(unsigned)
	if err != nil || digest(encoded) != artifact.SHA256 {
		return errors.New("Worker Artifact content digest is invalid")
	}
	return nil
}

func validateFixedE2EEvidence(
	task issueagent.TaskEnvelope,
	result issueagent.AgentResult,
	evidence []ToolEvidence,
) error {
	if task.Phase != issueagent.PhaseFix &&
		task.Phase != issueagent.PhaseAddressReview {
		return nil
	}
	if result.Status != issueagent.ResultStatusSuccess ||
		result.RequestedState != issueagent.StateValidating {
		return nil
	}
	if len(task.AllowedCommands) < 3 {
		return errors.New("remediation task lacks build, related-test, and E2E commands")
	}
	buildRule := task.AllowedCommands[0]
	e2eRule := task.AllowedCommands[len(task.AllowedCommands)-1]
	buildPasses := 0
	e2ePasses := 0
	relatedPasses := 0
	for _, item := range evidence {
		if item.Tool != "command_run" {
			continue
		}
		switch {
		case exactToolRule(item, buildRule):
			if item.ExitCode == 0 {
				buildPasses++
			}
		case exactToolRule(item, e2eRule):
			if item.ExitCode != 0 || item.AssertionSHA256 != "" {
				return errors.New("fixed E2E did not pass cleanly")
			}
			e2ePasses++
		default:
			for _, rule := range task.AllowedCommands[1 : len(task.AllowedCommands)-1] {
				if exactToolRule(item, rule) && item.ExitCode == 0 {
					relatedPasses++
					break
				}
			}
		}
	}
	if buildPasses != 1 || relatedPasses == 0 ||
		e2ePasses != task.RequiredRuns {
		return errors.New("remediation proof is missing exact successful commands")
	}
	return nil
}

func exactToolRule(item ToolEvidence, rule issueagent.CommandRule) bool {
	return item.Executable == rule.Executable &&
		len(item.Arguments) == len(rule.ArgvPrefix) &&
		slices.Equal(item.Arguments, rule.ArgvPrefix)
}

func validateReproductionEvidence(
	task issueagent.TaskEnvelope,
	result issueagent.AgentResult,
	changeSet issueagent.ChangeSet,
	evidence []ToolEvidence,
) error {
	if task.Phase != issueagent.PhaseReproduce {
		return nil
	}
	if result.Status == issueagent.ResultStatusFailed &&
		result.Failure != nil &&
		(result.Failure.Class == issueagent.FailureProvider ||
			result.Failure.Class == issueagent.FailureWorkerInfrastructure) {
		return nil
	}
	if len(changeSet.Files) == 0 {
		return errors.New("reproduction phase did not create a focused E2E")
	}
	for _, file := range changeSet.Files {
		if file.Operation != issueagent.FileOperationUpsert ||
			!strings.HasPrefix(file.Path, "test/e2e/") {
			return errors.New("reproduction phase changed a non-E2E file")
		}
	}
	if len(task.AllowedCommands) != 2 {
		return errors.New("reproduction task does not name two exact baseline commands")
	}
	runs := [2][]ToolEvidence{}
	for _, item := range evidence {
		if item.Tool != "command_run" {
			continue
		}
		matched := false
		for index, rule := range task.AllowedCommands {
			if item.Executable == rule.Executable &&
				len(item.Arguments) == len(rule.ArgvPrefix) &&
				slices.Equal(item.Arguments, rule.ArgvPrefix) {
				runs[index] = append(runs[index], item)
				matched = true
				break
			}
		}
		if !matched {
			return errors.New("reproduction executed an unclassified command")
		}
	}
	switch {
	case result.Status == issueagent.ResultStatusSuccess &&
		result.RequestedState == issueagent.StateReproduced:
		if result.Reproduction == nil {
			return errors.New("reproduction result omitted its assertion claim")
		}
		if err := requireRunCounts(runs, task.RequiredRuns); err != nil {
			return err
		}
		return requireAssertionFailures(
			result.Reproduction.AssertionSHA256, runs[0], runs[1],
		)
	case result.Status == issueagent.ResultStatusFailed &&
		result.RequestedState == issueagent.StateAlreadyFixed &&
		result.Failure != nil &&
		result.Failure.Class == issueagent.FailureAlreadyFixed:
		if result.Reproduction == nil {
			return errors.New("already-fixed result omitted its assertion claim")
		}
		if err := requireRunCounts(runs, task.RequiredRuns); err != nil {
			return err
		}
		if err := requireAssertionFailures(
			result.Reproduction.AssertionSHA256, runs[0],
		); err != nil {
			return err
		}
		for _, run := range runs[1] {
			if run.ExitCode != 0 || run.AssertionSHA256 != "" {
				return errors.New("diagnosis baseline did not pass cleanly")
			}
		}
		return nil
	default:
		// Classified harness/provider failures may legitimately have partial
		// evidence and are handled by the failed-result state machine.
		return nil
	}
}

func requireRunCounts(runs [2][]ToolEvidence, expected int) error {
	for _, baseline := range runs {
		if len(baseline) != expected {
			return errors.New("reproduction did not execute three runs per baseline")
		}
	}
	return nil
}

func requireAssertionFailures(expected string, groups ...[]ToolEvidence) error {
	assertion := expected
	for _, group := range groups {
		for _, run := range group {
			if run.ExitCode == 0 || run.AssertionSHA256 == "" {
				return errors.New("nonzero E2E result lacks a business assertion marker")
			}
			if assertion == "" {
				assertion = run.AssertionSHA256
			} else if assertion != run.AssertionSHA256 {
				return errors.New("E2E runs failed different business assertions")
			}
		}
	}
	return nil
}

type workspaceFile struct {
	mode          issueagent.FileMode
	content       []byte
	symlinkTarget string
}

func snapshotWorkspace(root string) (map[string]workspaceFile, error) {
	result := make(map[string]workspaceFile)
	var totalBytes int64
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("walk Worker workspace")
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return errors.New("relativize Worker workspace")
		}
		if entry.IsDir() {
			if relative == ".git" || relative == ".issue-agent-tmp" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(current)
			if err != nil || target == "" || filepath.IsAbs(target) ||
				strings.ContainsRune(target, 0) {
				return errors.New("Worker workspace contains an unsafe symlink")
			}
			directTarget := filepath.Clean(
				filepath.Join(filepath.Dir(current), target),
			)
			if !withinRoot(root, directTarget) {
				return errors.New("Worker workspace symlink escapes root")
			}
			targetRelative, err := filepath.Rel(root, directTarget)
			targetRelative = filepath.ToSlash(targetRelative)
			if err != nil || targetRelative == ".git" ||
				strings.HasPrefix(targetRelative, ".git/") ||
				targetRelative == ".issue-agent-tmp" ||
				strings.HasPrefix(targetRelative, ".issue-agent-tmp/") {
				return errors.New("Worker workspace symlink target is unsafe")
			}
			info, err := os.Lstat(directTarget)
			if err != nil || info.Mode()&os.ModeSymlink != 0 ||
				!info.Mode().IsRegular() ||
				len(result) >= 200000 ||
				totalBytes+int64(len(target)) > 2<<30 {
				return errors.New("Worker workspace symlink target is invalid")
			}
			result[filepath.ToSlash(relative)] = workspaceFile{
				symlinkTarget: target,
			}
			totalBytes += int64(len(target))
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("Worker workspace contains a special file")
		}
		if len(result) >= 200000 || info.Size() > 32<<20 ||
			totalBytes+info.Size() > 2<<30 {
			return errors.New("Worker workspace snapshot exceeds safety bounds")
		}
		content, err := os.ReadFile(current)
		if err != nil {
			return errors.New("read Worker workspace snapshot")
		}
		mode := issueagent.FileModeRegular
		if info.Mode().Perm()&0o111 != 0 {
			mode = issueagent.FileModeExecutable
		}
		result[filepath.ToSlash(relative)] = workspaceFile{
			mode: mode, content: content,
		}
		totalBytes += info.Size()
		return nil
	})
	return result, err
}

func deriveChangeSet(
	before map[string]workspaceFile,
	after map[string]workspaceFile,
) (issueagent.ChangeSet, error) {
	paths := make([]string, 0, len(before)+len(after))
	seen := make(map[string]struct{}, len(before)+len(after))
	for filePath := range before {
		seen[filePath] = struct{}{}
		paths = append(paths, filePath)
	}
	for filePath := range after {
		if _, ok := seen[filePath]; !ok {
			paths = append(paths, filePath)
		}
	}
	slices.Sort(paths)
	changes := make([]issueagent.FileChange, 0)
	for _, filePath := range paths {
		oldFile, existed := before[filePath]
		newFile, exists := after[filePath]
		if oldFile.symlinkTarget != "" || newFile.symlinkTarget != "" {
			if existed && exists &&
				oldFile.symlinkTarget != "" &&
				oldFile.symlinkTarget == newFile.symlinkTarget {
				continue
			}
			return issueagent.ChangeSet{},
				errors.New("Worker workspace symlink changed")
		}
		switch {
		case existed && !exists:
			changes = append(changes, issueagent.FileChange{
				Path: filePath, Operation: issueagent.FileOperationDelete,
			})
		case !existed && exists:
			changes = append(changes, issueagent.FileChange{
				Path: filePath, Operation: issueagent.FileOperationUpsert,
				Mode:          newFile.mode,
				ContentBase64: issueagent.EncodeFileContent(newFile.content),
			})
		case oldFile.mode != newFile.mode ||
			!slices.Equal(oldFile.content, newFile.content):
			changes = append(changes, issueagent.FileChange{
				Path: filePath, Operation: issueagent.FileOperationUpsert,
				Mode:          newFile.mode,
				ContentBase64: issueagent.EncodeFileContent(newFile.content),
			})
		}
	}
	return issueagent.ChangeSet{Files: changes}, nil
}

func (worker *Worker) verifyInstructions() error {
	for _, instruction := range worker.config.Task.InstructionDigests {
		if !safeRelativePath(instruction.Path) {
			return errors.New("Worker instruction path is unsafe")
		}
		resolved, err := filepath.EvalSymlinks(
			filepath.Join(worker.root, filepath.FromSlash(instruction.Path)),
		)
		if err != nil || !withinRoot(worker.root, resolved) {
			return errors.New("Worker instruction file is unavailable")
		}
		content, err := os.ReadFile(resolved)
		if err != nil || digest(content) != instruction.SHA256 {
			return errors.New("Worker instruction digest mismatch")
		}
	}
	return nil
}

func validatePhaseProposal(
	phase issueagent.Phase,
	status issueagent.ResultStatus,
	state issueagent.State,
	action issueagent.Action,
) error {
	if status == issueagent.ResultStatusFailed {
		if state != issueagent.StateNeedsInfo &&
			state != issueagent.StateReadyForHuman &&
			state != issueagent.StateAlreadyFixed {
			return errors.New("failed Worker proposal selects an invalid state")
		}
		return nil
	}
	var valid bool
	switch phase {
	case issueagent.PhaseReproduce:
		valid = state == issueagent.StateReproduced &&
			action == issueagent.ActionOpenDraftPR ||
			state == issueagent.StateAlreadyFixed && action == issueagent.ActionNone
	case issueagent.PhaseDiagnose:
		valid = state == issueagent.StateDiagnosed &&
			action == issueagent.ActionImplementFix
	case issueagent.PhaseFix, issueagent.PhaseAddressReview:
		valid = state == issueagent.StateValidating &&
			action == issueagent.ActionValidate
	}
	if !valid {
		return errors.New("successful Worker proposal is invalid for phase")
	}
	return nil
}
