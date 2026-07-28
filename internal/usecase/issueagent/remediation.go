package issueagent

import (
	"errors"
	"slices"
	"strings"
	"time"

	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

// PhaseTaskInput contains checkpoint-frozen facts shared by diagnosis and fix tasks.
type PhaseTaskInput struct {
	Repository         string
	IssueNumber        int64
	Generation         uint64
	Sequence           uint64
	OperationID        string
	CheckpointDigest   string
	PolicyDigest       string
	PromptDigest       string
	Versions           issueagentcontract.Versions
	CandidateSHA       string
	FrozenIssue        string
	AcceptedCommentIDs []int64
	InstructionDigests []issueagentcontract.FileDigest
	Provider           issueagentcontract.Provider
	Model              string
}

// BuildDiagnosisTask constructs a read-only causal-investigation task.
func BuildDiagnosisTask(
	input PhaseTaskInput,
	commands []issueagentcontract.CommandRule,
) (issueagentcontract.TaskEnvelope, error) {
	task := phaseTaskBase(input)
	task.Phase = issueagentcontract.PhaseDiagnose
	task.AllowedPaths = []string{"internal"}
	task.AllowedCommands = append([]issueagentcontract.CommandRule(nil), commands...)
	task.ProductionChangesAllowed = false
	if err := issueagentcontract.ValidateTaskEnvelope(task); err != nil {
		return issueagentcontract.TaskEnvelope{}, err
	}
	return task, nil
}

// BuildFixTask constructs a diagnosis-scoped remediation task with one exact
// candidate build, related tests, and three fixed E2E passes.
func BuildFixTask(
	input PhaseTaskInput,
	diagnosis issueagentcontract.Diagnosis,
	reproduction issueagentcontract.Reproduction,
	relatedCommands []issueagentcontract.CommandRule,
) (issueagentcontract.TaskEnvelope, error) {
	if len(diagnosis.RiskClasses) != 0 && diagnosis.AuthorizationEvent == "" {
		return issueagentcontract.TaskEnvelope{},
			errors.New("high-risk diagnosis lacks second authorization")
	}
	if slices.Contains(diagnosis.RiskClasses, RiskProtectedAgent) {
		return issueagentcontract.TaskEnvelope{},
			errors.New("protected Agent paths are human-only")
	}
	if len(relatedCommands) == 0 || len(relatedCommands) > 8 {
		return issueagentcontract.TaskEnvelope{},
			errors.New("related-test command set is invalid")
	}
	task := phaseTaskBase(input)
	task.Phase = issueagentcontract.PhaseFix
	task.AllowedPaths = append([]string(nil), diagnosis.IntendedPaths...)
	task.ProductionChangesAllowed = true
	task.RequiredTopology = reproduction.Topology
	task.RequiredRuns = 3
	scenarioPackage := ""
	for _, testFile := range reproduction.TestFiles {
		if index := strings.LastIndex(testFile.Path, "/"); index > 0 {
			candidate := "./" + testFile.Path[:index]
			if scenarioPackage == "" {
				scenarioPackage = candidate
			} else if scenarioPackage != candidate {
				return issueagentcontract.TaskEnvelope{},
					errors.New("frozen E2E files span multiple packages")
			}
		}
	}
	if scenarioPackage == "" {
		return issueagentcontract.TaskEnvelope{}, errors.New("frozen E2E package is missing")
	}
	buildArguments := []string{
		"build", "-trimpath", "-o", ".issue-agent-tmp/wukongim", "./cmd/wukongim",
	}
	e2eArguments := []string{
		"WK_E2E_BINARY=.issue-agent-tmp/wukongim", "go", "test",
		"-tags=e2e", scenarioPackage, "-count=1",
	}
	task.AllowedCommands = []issueagentcontract.CommandRule{{
		Executable: "go", ArgvPrefix: buildArguments, MaxArgs: len(buildArguments),
	}}
	task.AllowedCommands = append(task.AllowedCommands, relatedCommands...)
	task.AllowedCommands = append(task.AllowedCommands, issueagentcontract.CommandRule{
		Executable: "env", ArgvPrefix: e2eArguments, MaxArgs: len(e2eArguments),
	})
	if err := issueagentcontract.ValidateTaskEnvelope(task); err != nil {
		return issueagentcontract.TaskEnvelope{}, err
	}
	return task, nil
}

func phaseTaskBase(input PhaseTaskInput) issueagentcontract.TaskEnvelope {
	return issueagentcontract.TaskEnvelope{
		SchemaVersion: 1, Repository: input.Repository,
		IssueNumber: input.IssueNumber, Generation: input.Generation,
		Sequence: input.Sequence, OperationID: input.OperationID,
		CheckpointDigest: input.CheckpointDigest,
		PolicyDigest:     input.PolicyDigest, PromptDigest: input.PromptDigest,
		AffectedSHA:        input.Versions.AffectedSHA,
		DiagnosisBaseSHA:   input.Versions.DiagnosisBaseSHA,
		CandidateSHA:       input.CandidateSHA,
		FrozenIssue:        input.FrozenIssue,
		AcceptedCommentIDs: append([]int64(nil), input.AcceptedCommentIDs...),
		InstructionDigests: append(
			[]issueagentcontract.FileDigest(nil), input.InstructionDigests...,
		),
		Limits: issueagentcontract.ResourceLimits{
			WallTime: 90 * time.Minute, MaxOutputBytes: 4 << 20,
			MaxFiles: 64, MaxFileBytes: 4 << 20, MaxTotalBytes: 16 << 20,
		},
		Provider: input.Provider, Model: input.Model,
	}
}
