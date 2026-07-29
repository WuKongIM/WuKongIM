package issueagent

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

const requiredReproductionRuns = 3

var (
	singleNodeTopologyMarkers = []string{
		"single-node", "single node", "1-node", "1 node", "单节点",
	}
	multiNodeTopologyMarkers = []string{
		"three-node", "three node", "3-node", "3 node",
		"multi-node", "multi node", "two-node", "two node",
		"三节点", "多节点",
	}
	nodeCountPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?:^|[^0-9])([0-9]+)\s*(?:-\s*)?nodes?(?:[^a-z]|$)`),
		regexp.MustCompile(`(?:^|[^0-9])([0-9]+)\s*节点`),
	}
)

// RunOutcome separates business failures from build and harness failures.
type RunOutcome string

const (
	RunAssertionFailed RunOutcome = "assertion_failed"
	RunPassed          RunOutcome = "passed"
	RunBuildFailed     RunOutcome = "build_failed"
	RunSetupFailed     RunOutcome = "setup_failed"
	RunInfrastructure  RunOutcome = "infrastructure_failed"
)

// RunObservation is one trusted process-level E2E observation.
type RunObservation struct {
	RunID           int64
	SourceSHA       string
	BinarySHA256    string
	CommandSHA256   string
	Assertion       string
	AssertionSHA256 string
	Topology        string
	Outcome         RunOutcome
}

// ReproductionDecision is the deterministic classification of two baselines.
type ReproductionDecision string

const (
	ReproductionConfirmed    ReproductionDecision = "reproduced"
	ReproductionAlreadyFixed ReproductionDecision = "already_fixed"
	ReproductionBuildFailure ReproductionDecision = "build_failure"
	ReproductionHarnessError ReproductionDecision = "harness_failure"
	ReproductionInconclusive ReproductionDecision = "inconclusive"
)

// ReproductionEvaluation contains only validated, same-assertion evidence.
type ReproductionEvaluation struct {
	Decision ReproductionDecision
	Evidence *issueagentcontract.Reproduction
}

// EvaluateReproduction enforces the two-baseline, same-test, three-run rule.
func EvaluateReproduction(
	versions issueagentcontract.Versions,
	topology string,
	affected []RunObservation,
	diagnosisBase []RunObservation,
	artifactRunID int64,
	artifactName string,
	artifactSHA256 string,
	testFiles []issueagentcontract.TestFile,
) (ReproductionEvaluation, error) {
	if len(affected) != requiredReproductionRuns ||
		len(diagnosisBase) != requiredReproductionRuns {
		return ReproductionEvaluation{Decision: ReproductionInconclusive},
			errors.New("reproduction requires exactly three runs per baseline")
	}
	if topology != "single-node-cluster" &&
		topology != "three-node-cluster" &&
		topology != "multi-node-cluster" {
		return ReproductionEvaluation{Decision: ReproductionHarnessError},
			errors.New("reproduction topology is invalid")
	}
	all := append(append([]RunObservation(nil), affected...), diagnosisBase...)
	assertion := all[0].Assertion
	assertionDigest := all[0].AssertionSHA256
	for _, run := range all {
		if run.Topology != topology {
			return ReproductionEvaluation{Decision: ReproductionHarnessError},
				errors.New("reproduction topology does not match the task")
		}
		if run.Assertion != assertion || run.AssertionSHA256 != assertionDigest ||
			strings.TrimSpace(assertion) == "" ||
			!scheduleDigestPattern.MatchString(assertionDigest) {
			return ReproductionEvaluation{Decision: ReproductionHarnessError},
				errors.New("reproduction runs do not name one business assertion")
		}
		switch run.Outcome {
		case RunBuildFailed:
			return ReproductionEvaluation{Decision: ReproductionBuildFailure}, nil
		case RunSetupFailed, RunInfrastructure:
			return ReproductionEvaluation{Decision: ReproductionHarnessError}, nil
		case RunAssertionFailed, RunPassed:
		default:
			return ReproductionEvaluation{Decision: ReproductionHarnessError},
				errors.New("reproduction run outcome is invalid")
		}
	}
	if !allFromSHA(affected, versions.AffectedSHA) ||
		!allFromSHA(diagnosisBase, versions.DiagnosisBaseSHA) {
		return ReproductionEvaluation{Decision: ReproductionHarnessError},
			errors.New("reproduction source SHA does not match a frozen baseline")
	}
	affectedOutcome, affectedStable := stableOutcome(affected)
	baseOutcome, baseStable := stableOutcome(diagnosisBase)
	if !affectedStable || !baseStable {
		return ReproductionEvaluation{Decision: ReproductionInconclusive}, nil
	}
	decision := ReproductionInconclusive
	switch {
	case affectedOutcome == RunAssertionFailed && baseOutcome == RunAssertionFailed:
		decision = ReproductionConfirmed
	case affectedOutcome == RunAssertionFailed && baseOutcome == RunPassed:
		decision = ReproductionAlreadyFixed
	default:
		return ReproductionEvaluation{Decision: decision}, nil
	}
	if artifactRunID <= 0 || artifactName == "" ||
		!scheduleDigestPattern.MatchString(artifactSHA256) ||
		len(testFiles) == 0 || !slices.IsSortedFunc(testFiles, func(a, b issueagentcontract.TestFile) int {
		return strings.Compare(a.Path, b.Path)
	}) {
		return ReproductionEvaluation{Decision: ReproductionHarnessError},
			errors.New("accepted reproduction evidence metadata is invalid")
	}
	evidence := &issueagentcontract.Reproduction{
		TestFiles:         append([]issueagentcontract.TestFile(nil), testFiles...),
		Assertion:         assertion,
		AssertionSHA256:   assertionDigest,
		Topology:          topology,
		AffectedRuns:      contractRuns(affected),
		DiagnosisBaseRuns: contractRuns(diagnosisBase),
		ArtifactRunID:     artifactRunID,
		ArtifactName:      artifactName,
		ArtifactSHA256:    artifactSHA256,
	}
	return ReproductionEvaluation{Decision: decision, Evidence: evidence}, nil
}

// ReproductionTaskInput contains trusted facts used to construct one no-fix task.
type ReproductionTaskInput struct {
	Repository         string
	IssueNumber        int64
	Generation         uint64
	Sequence           uint64
	OperationID        string
	CheckpointDigest   string
	PolicyDigest       string
	PromptDigest       string
	Versions           issueagentcontract.Versions
	FrozenIssue        string
	AcceptedCommentIDs []int64
	InstructionDigests []issueagentcontract.FileDigest
	Topology           string
	HarnessPaths       []string
	Provider           issueagentcontract.Provider
	Model              string
}

// ReproductionTopology derives the supported process topology from frozen Bug
// facts. An omitted topology means a single-node cluster.
func ReproductionTopology(environment string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(environment))
	single := containsAny(normalized, singleNodeTopologyMarkers)
	multi := containsAny(normalized, multiNodeTopologyMarkers)
	countSingle, countMulti, err := topologyFromNodeCounts(normalized)
	if err != nil {
		return "", err
	}
	single = single || countSingle
	multi = multi || countMulti
	if single && multi {
		return "", errors.New("Bug environment names conflicting cluster topologies")
	}
	if multi {
		return "three-node-cluster", nil
	}
	return "single-node-cluster", nil
}

// BuildReproductionTask creates the closed no-production-change Worker scope.
func BuildReproductionTask(input ReproductionTaskInput) (issueagentcontract.TaskEnvelope, error) {
	scenario := fmt.Sprintf("test/e2e/issue_agent/issue_%d", input.IssueNumber)
	allowedPaths := append([]string{scenario}, input.HarnessPaths...)
	slices.Sort(allowedPaths)
	if len(input.HarnessPaths) > 4 {
		return issueagentcontract.TaskEnvelope{}, errors.New("reproduction harness scope is too broad")
	}
	for _, harness := range input.HarnessPaths {
		if !strings.HasPrefix(harness, "test/e2e/") ||
			strings.HasPrefix(harness, "test/e2e/issue_agent/") {
			return issueagentcontract.TaskEnvelope{},
				errors.New("reproduction harness path is outside test/e2e")
		}
	}
	testArguments := []string{
		"test", "-tags=e2e", "./" + scenario, "-count=1",
	}
	affectedArguments := append(
		[]string{"WK_E2E_BINARY=/issue-agent/bin/affected", "go"},
		testArguments...,
	)
	baseArguments := append(
		[]string{"WK_E2E_BINARY=/issue-agent/bin/diagnosis-base", "go"},
		testArguments...,
	)
	task := issueagentcontract.TaskEnvelope{
		SchemaVersion: 1, Repository: input.Repository,
		IssueNumber: input.IssueNumber, Generation: input.Generation,
		Sequence: input.Sequence, OperationID: input.OperationID,
		Phase:            issueagentcontract.PhaseReproduce,
		CheckpointDigest: input.CheckpointDigest,
		PolicyDigest:     input.PolicyDigest, PromptDigest: input.PromptDigest,
		AffectedSHA:        input.Versions.AffectedSHA,
		DiagnosisBaseSHA:   input.Versions.DiagnosisBaseSHA,
		FrozenIssue:        input.FrozenIssue,
		AcceptedCommentIDs: append([]int64(nil), input.AcceptedCommentIDs...),
		InstructionDigests: append(
			[]issueagentcontract.FileDigest(nil), input.InstructionDigests...,
		),
		AllowedPaths: allowedPaths,
		AllowedCommands: []issueagentcontract.CommandRule{
			{
				Executable: "env", ArgvPrefix: affectedArguments,
				MaxArgs: len(affectedArguments),
			},
			{
				Executable: "env", ArgvPrefix: baseArguments,
				MaxArgs: len(baseArguments),
			},
		},
		Limits: issueagentcontract.ResourceLimits{
			WallTime: 90 * time.Minute, MaxOutputBytes: 4 << 20,
			// Reserve the sixteenth file for the trusted Publisher-injected
			// scenario AGENTS.md.
			MaxFiles: 15, MaxFileBytes: 2 << 20, MaxTotalBytes: 8 << 20,
		},
		RequiredTopology:         input.Topology,
		RequiredRuns:             requiredReproductionRuns,
		ProductionChangesAllowed: false,
		Provider:                 input.Provider, Model: input.Model,
	}
	if err := issueagentcontract.ValidateTaskEnvelope(task); err != nil {
		return issueagentcontract.TaskEnvelope{}, err
	}
	return task, nil
}

func stableOutcome(runs []RunObservation) (RunOutcome, bool) {
	first := runs[0].Outcome
	for _, run := range runs[1:] {
		if run.Outcome != first {
			return "", false
		}
	}
	return first, true
}

func containsAny(value string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func topologyFromNodeCounts(value string) (bool, bool, error) {
	var single, multi bool
	for _, pattern := range nodeCountPatterns {
		for _, match := range pattern.FindAllStringSubmatch(value, -1) {
			count, err := strconv.Atoi(match[1])
			if err != nil || count == 0 {
				return false, false, errors.New("Bug environment has an invalid cluster size")
			}
			if count == 1 {
				single = true
			} else {
				multi = true
			}
		}
	}
	return single, multi, nil
}

func allFromSHA(runs []RunObservation, expected string) bool {
	for _, run := range runs {
		if run.SourceSHA != expected ||
			!scheduleDigestPattern.MatchString(run.BinarySHA256) ||
			!scheduleDigestPattern.MatchString(run.CommandSHA256) ||
			run.RunID <= 0 {
			return false
		}
	}
	return true
}

func contractRuns(observations []RunObservation) []issueagentcontract.ReproductionRun {
	result := make([]issueagentcontract.ReproductionRun, 0, len(observations))
	for _, run := range observations {
		result = append(result, issueagentcontract.ReproductionRun{
			RunID: run.RunID, SourceSHA: run.SourceSHA,
			BinarySHA256: run.BinarySHA256, CommandSHA256: run.CommandSHA256,
			AssertionSHA256: run.AssertionSHA256, Outcome: string(run.Outcome),
		})
	}
	return result
}
