package issueagent_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	issueagentusecase "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

func TestBuildDiagnosisTaskIsReadOnlyAndFixTaskFreezesThreePassE2E(t *testing.T) {
	t.Parallel()

	input := validPhaseTaskInput()
	diagnosisTask, err := issueagentusecase.BuildDiagnosisTask(
		input,
		[]issueagent.CommandRule{{
			Executable: "go", ArgvPrefix: []string{"test", "./internal/usecase/delivery"},
			MaxArgs: 2,
		}},
	)
	require.NoError(t, err)
	require.Equal(t, issueagent.PhaseDiagnose, diagnosisTask.Phase)
	require.False(t, diagnosisTask.ProductionChangesAllowed)

	diagnosis := validDiagnosis()
	reproduction := issueagent.Reproduction{
		Topology: "three-node-cluster",
		TestFiles: []issueagent.TestFile{{
			Path:    "test/e2e/issue_agent/issue_42/reproduction_test.go",
			BlobSHA: affectedSHA,
		}},
	}
	fixTask, err := issueagentusecase.BuildFixTask(
		input, diagnosis, reproduction,
		[]issueagent.CommandRule{{
			Executable: "go",
			ArgvPrefix: []string{"test", "./internal/usecase/delivery"},
			MaxArgs:    2,
		}},
	)
	require.NoError(t, err)
	require.Equal(t, issueagent.PhaseFix, fixTask.Phase)
	require.True(t, fixTask.ProductionChangesAllowed)
	require.Equal(t, 3, fixTask.RequiredRuns)
	require.Equal(t, "three-node-cluster", fixTask.RequiredTopology)
	require.Equal(t, diagnosis.IntendedPaths, fixTask.AllowedPaths)
	require.Equal(t, "go", fixTask.AllowedCommands[0].Executable)
	require.Equal(t, "env", fixTask.AllowedCommands[len(fixTask.AllowedCommands)-1].Executable)
}

func TestBuildFixTaskRequiresSecondAuthorizationAndRejectsAgentPaths(t *testing.T) {
	t.Parallel()

	input := validPhaseTaskInput()
	reproduction := issueagent.Reproduction{
		Topology: "single-node-cluster",
		TestFiles: []issueagent.TestFile{{
			Path:    "test/e2e/issue_agent/issue_42/reproduction_test.go",
			BlobSHA: affectedSHA,
		}},
	}
	diagnosis := validDiagnosis()
	diagnosis.RiskClasses = []string{issueagentusecase.RiskConsensus}
	_, err := issueagentusecase.BuildFixTask(
		input, diagnosis, reproduction,
		[]issueagent.CommandRule{{
			Executable: "go", ArgvPrefix: []string{"test"}, MaxArgs: 1,
		}},
	)
	require.Error(t, err)

	diagnosis.AuthorizationEvent = "direction-approved-42"
	diagnosis.RiskClasses = []string{issueagentusecase.RiskProtectedAgent}
	_, err = issueagentusecase.BuildFixTask(
		input, diagnosis, reproduction,
		[]issueagent.CommandRule{{
			Executable: "go", ArgvPrefix: []string{"test"}, MaxArgs: 1,
		}},
	)
	require.Error(t, err)
}

func validPhaseTaskInput() issueagentusecase.PhaseTaskInput {
	return issueagentusecase.PhaseTaskInput{
		Repository: "WuKongIM/WuKongIM", IssueNumber: 42,
		Generation: 1, Sequence: 7,
		OperationID:      assertionDigest,
		CheckpointDigest: binaryDigest,
		PolicyDigest:     commandDigest,
		PromptDigest:     artifactDigest,
		Versions: issueagent.Versions{
			ReportedRef: affectedSHA, AffectedSHA: affectedSHA,
			DiagnosisBaseSHA: baseSHA,
		},
		CandidateSHA: baseSHA,
		FrozenIssue:  "delivery fails",
		InstructionDigests: []issueagent.FileDigest{{
			Path: "AGENTS.md", SHA256: assertionDigest,
		}},
		Provider: issueagent.ProviderDeepSeek, Model: "deepseek-chat",
	}
}

func validDiagnosis() issueagent.Diagnosis {
	return issueagent.Diagnosis{
		Summary:            "stale recipient authority route",
		ExternalSymptom:    "recipient does not receive committed message",
		CausalPath:         "SEND -> delivery -> owner lookup",
		ViolatedInvariant:  "recipient owner must route committed delivery",
		EvidenceReferences: []string{"command:1"},
		EvidenceSHA256:     assertionDigest,
		IntendedPaths:      []string{"internal/usecase/delivery"},
		ClusterSemantics:   "same authority path applies to every cluster size",
		ValidationSuites:   []string{"go-e2e", "go-fast"},
		RiskClasses:        []string{},
	}
}
