package issueagentworker

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

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
}

// Artifact is the sanitized publishable Worker output.
type Artifact struct {
	SchemaVersion int                     `json:"schema_version"`
	Task          issueagent.TaskEnvelope `json:"task"`
	Result        issueagent.AgentResult  `json:"result"`
	Tools         []ToolEvidence          `json:"tools"`
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
	before, err := snapshotWorkspace(worker.root)
	if err != nil {
		return Artifact{}, err
	}
	broker, err := NewBroker(BrokerConfig{
		Workspace:         worker.root,
		AllowedWritePaths: worker.config.Task.AllowedPaths,
		AllowedCommands:   worker.config.Task.AllowedCommands,
		MaxFileBytes:      worker.config.Task.Limits.MaxFileBytes,
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
	if err != nil {
		if runCtx.Err() != nil {
			return Artifact{}, runCtx.Err()
		}
		return Artifact{}, errors.New("selected model Adapter failed")
	}
	after, err := snapshotWorkspace(worker.root)
	if err != nil {
		return Artifact{}, err
	}
	changeSet, err := deriveChangeSet(before, after)
	if err != nil {
		return Artifact{}, err
	}
	if err := issueagent.ValidateChangeSet(changeSet, issueagent.ChangeSetLimits{
		MaxFiles:      worker.config.Task.Limits.MaxFiles,
		MaxFileBytes:  int(worker.config.Task.Limits.MaxFileBytes),
		MaxTotalBytes: int(worker.config.Task.Limits.MaxTotalBytes),
		MaxDeletions:  worker.config.Task.Limits.MaxFiles,
	}); err != nil {
		return Artifact{}, err
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
			StdoutSHA256: evidence.OutputSHA256,
			StderrSHA256: evidence.ErrorSHA256,
			DurationMS:   evidence.DurationMS,
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
	result.Usage = modelOutput.Usage
	if err := issueagent.ValidateAgentResult(result, worker.config.Task); err != nil {
		return Artifact{}, err
	}
	unsigned := struct {
		SchemaVersion int                     `json:"schema_version"`
		Task          issueagent.TaskEnvelope `json:"task"`
		Result        issueagent.AgentResult  `json:"result"`
		Tools         []ToolEvidence          `json:"tools"`
	}{
		SchemaVersion: 1, Task: worker.config.Task,
		Result: result, Tools: toolEvidence,
	}
	encoded, err := json.Marshal(unsigned)
	if err != nil || int64(len(encoded)) > worker.config.MaxArtifactBytes {
		return Artifact{}, errors.New("Worker Artifact exceeds byte limit")
	}
	return Artifact{
		SchemaVersion: 1, Task: worker.config.Task, Result: result,
		Tools: toolEvidence, SHA256: digest(encoded),
	}, nil
}

type workspaceFile struct {
	mode    issueagent.FileMode
	content []byte
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
			if relative == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("Worker workspace contains a symlink")
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
