package issueagent_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	issueagentusecase "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

func TestRiskClassificationRequiresSecondAuthorizationAndFencesAgentPaths(t *testing.T) {
	t.Parallel()

	decision, err := issueagentusecase.ClassifyRisk(issueagentusecase.RiskInput{
		Paths:            []string{"internal/runtime/session/router.go"},
		ConsensusChanged: true,
	})
	require.NoError(t, err)
	require.Equal(t, []string{issueagentusecase.RiskConsensus}, decision.Classes)
	require.True(t, decision.RequiresSecondAuthorization)
	require.False(t, decision.HumanOnly)

	protected, err := issueagentusecase.ClassifyRisk(issueagentusecase.RiskInput{
		Paths: []string{".github/workflows/issue-agent-run.yml"},
	})
	require.NoError(t, err)
	require.True(t, protected.HumanOnly)
	require.Contains(t, protected.Classes, issueagentusecase.RiskProtectedAgent)

	for _, filePath := range []string{
		".github/ISSUE_TEMPLATE/bug_report.yml",
		"internal/app/issue_agent.go",
		"scripts/issue_agent_schema_test.go",
	} {
		decision, err := issueagentusecase.ClassifyRisk(issueagentusecase.RiskInput{
			Paths: []string{filePath},
		})
		require.NoError(t, err)
		require.True(t, decision.HumanOnly, filePath)
	}
}

func TestRiskFactsComeFromTrustedChangeSetPaths(t *testing.T) {
	t.Parallel()

	input := issueagentusecase.RiskInputFromChangeSet(issueagent.ChangeSet{
		Files: []issueagent.FileChange{
			{Path: "go.mod"},
			{Path: "internal/infra/cluster/raft/node.go"},
		},
	})
	decision, err := issueagentusecase.ClassifyRisk(input)
	require.NoError(t, err)
	require.Contains(t, decision.Classes, issueagentusecase.RiskConsensus)
	require.Contains(t, decision.Classes, issueagentusecase.RiskDependency)
	require.False(t, issueagentusecase.RiskClassesAuthorized(
		decision.Classes, []string{issueagentusecase.RiskConsensus}, "event-1",
	))
	require.True(t, issueagentusecase.RiskClassesAuthorized(
		decision.Classes, decision.Classes, "event-1",
	))
}

func TestValidationRequestAlwaysIncludesFastAndE2EWithRiskSuites(t *testing.T) {
	t.Parallel()

	request, err := issueagentusecase.BuildValidationRequest(
		"0123456789abcdef0123456789abcdef01234567",
		[]string{
			issueagentusecase.RiskSecurity,
			issueagentusecase.RiskConsensus,
		},
	)
	require.NoError(t, err)
	require.Contains(t, request.Suites, "go-fast")
	require.Contains(t, request.Suites, "go-e2e")
	require.Contains(t, request.Suites, "go-race")
	require.Contains(t, request.Suites, "go-integration")
	require.Contains(t, request.Suites, "three-node-smoke")
	require.Contains(t, request.Labels, "agent-ci/run")
	require.Contains(t, request.Body, "<!-- agent-validation-plan:v1")
	require.Equal(t, "high", request.Risk)
}

func TestDiagnosisEvidenceIsMandatoryForSuccessfulDiagnoseResult(t *testing.T) {
	t.Parallel()

	task := diagnosisResultTask()
	result := diagnosisResult(task)
	result.RequestedState = issueagent.StateDiagnosed
	result.RequestedAction = issueagent.ActionImplementFix
	require.Error(t, issueagent.ValidateAgentResult(result, task))

	result.Diagnosis = &issueagent.Diagnosis{
		Summary:            "delivery lookup selects stale owner",
		ExternalSymptom:    "recipient receives no committed message",
		CausalPath:         "SEND -> delivery usecase -> stale owner route",
		ViolatedInvariant:  "committed recipient authority owns delivery",
		EvidenceReferences: []string{"command:1", "command:2"},
		EvidenceSHA256:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		IntendedPaths:      []string{"internal/usecase/delivery"},
		ClusterSemantics:   "same routing path applies to every cluster size",
		ValidationSuites:   []string{"go-e2e", "go-fast"},
		RiskClasses:        []string{},
	}
	require.NoError(t, issueagent.ValidateAgentResult(result, task))
}

func diagnosisResultTask() issueagent.TaskEnvelope {
	return issueagent.TaskEnvelope{
		SchemaVersion: 1, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, Generation: 1, Sequence: 6,
		OperationID:      "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		Phase:            issueagent.PhaseDiagnose,
		CheckpointDigest: "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		PolicyDigest:     "sha256:3333333333333333333333333333333333333333333333333333333333333333",
		PromptDigest:     "sha256:4444444444444444444444444444444444444444444444444444444444444444",
		AffectedSHA:      "0123456789abcdef0123456789abcdef01234567",
		DiagnosisBaseSHA: "89abcdef0123456789abcdef0123456789abcdef",
		CandidateSHA:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		FrozenIssue:      "delivery fails",
		InstructionDigests: []issueagent.FileDigest{{
			Path:   "AGENTS.md",
			SHA256: "sha256:5555555555555555555555555555555555555555555555555555555555555555",
		}},
		AllowedPaths: []string{"internal/usecase/delivery"},
		AllowedCommands: []issueagent.CommandRule{{
			Executable: "go", ArgvPrefix: []string{"test"}, MaxArgs: 2,
		}},
		Limits: issueagent.ResourceLimits{
			WallTime: time.Minute, MaxOutputBytes: 1024,
			MaxFiles: 2, MaxFileBytes: 1024, MaxTotalBytes: 2048,
		},
		ProductionChangesAllowed: false,
		Provider:                 issueagent.ProviderDeepSeek, Model: "deepseek-chat",
	}
}

func diagnosisResult(task issueagent.TaskEnvelope) issueagent.AgentResult {
	return issueagent.AgentResult{
		SchemaVersion: 1, Repository: task.Repository,
		IssueNumber: task.IssueNumber, Generation: task.Generation,
		Sequence: task.Sequence, OperationID: task.OperationID,
		Phase: task.Phase, Status: issueagent.ResultStatusSuccess,
		RequestedState:  issueagent.StateDiagnosed,
		RequestedAction: issueagent.ActionImplementFix,
		Evidence: issueagent.EvidenceManifest{
			ArtifactSHA256: "sha256:6666666666666666666666666666666666666666666666666666666666666666",
			Commands: []issueagent.CommandEvidence{{
				Executable: "go", Arguments: []string{"test"},
				WorkingDir: ".", ExitCode: 0,
				StdoutSHA256: "sha256:7777777777777777777777777777777777777777777777777777777777777777",
				StderrSHA256: "sha256:8888888888888888888888888888888888888888888888888888888888888888",
				DurationMS:   1,
			}},
		},
		Usage: issueagent.ModelUsage{
			Provider: task.Provider, Model: task.Model,
		},
	}
}
