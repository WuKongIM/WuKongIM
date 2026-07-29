package issueagent_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	issueagentusecase "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

const (
	assertionDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	binaryDigest    = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	commandDigest   = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	artifactDigest  = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
)

func TestEvaluateReproductionRequiresSameAssertionThreeTimesOnBothBaselines(t *testing.T) {
	t.Parallel()

	versions := issueagent.Versions{
		ReportedRef: "v2.1.0", AffectedSHA: affectedSHA,
		DiagnosisBaseSHA: baseSHA,
	}
	affected := observedRuns(1, affectedSHA, issueagentusecase.RunAssertionFailed)
	base := observedRuns(4, baseSHA, issueagentusecase.RunAssertionFailed)
	evaluation, err := issueagentusecase.EvaluateReproduction(
		versions, "three-node-cluster", affected, base, 900,
		"issue-agent-42-reproduction", artifactDigest,
		[]issueagent.TestFile{{
			Path:    "test/e2e/issue_agent/issue_42/reproduction_test.go",
			BlobSHA: "34567890abcdef1234567890abcdef1234567890",
		}},
	)
	require.NoError(t, err)
	require.Equal(t, issueagentusecase.ReproductionConfirmed, evaluation.Decision)
	require.NotNil(t, evaluation.Evidence)
	require.Equal(t, assertionDigest, evaluation.Evidence.AssertionSHA256)
	require.Len(t, evaluation.Evidence.AffectedRuns, 3)
	require.Len(t, evaluation.Evidence.DiagnosisBaseRuns, 3)
}

func TestEvaluateReproductionClassifiesAlreadyFixedHarnessAndInstability(t *testing.T) {
	t.Parallel()

	versions := issueagent.Versions{
		ReportedRef: affectedSHA, AffectedSHA: affectedSHA,
		DiagnosisBaseSHA: baseSHA,
	}
	tests := []struct {
		name     string
		affected []issueagentusecase.RunObservation
		base     []issueagentusecase.RunObservation
		want     issueagentusecase.ReproductionDecision
	}{
		{
			name:     "already fixed",
			affected: observedRuns(1, affectedSHA, issueagentusecase.RunAssertionFailed),
			base:     observedRuns(4, baseSHA, issueagentusecase.RunPassed),
			want:     issueagentusecase.ReproductionAlreadyFixed,
		},
		{
			name:     "build failure",
			affected: observedRuns(1, affectedSHA, issueagentusecase.RunBuildFailed),
			base:     observedRuns(4, baseSHA, issueagentusecase.RunPassed),
			want:     issueagentusecase.ReproductionBuildFailure,
		},
		{
			name:     "harness failure",
			affected: observedRuns(1, affectedSHA, issueagentusecase.RunSetupFailed),
			base:     observedRuns(4, baseSHA, issueagentusecase.RunPassed),
			want:     issueagentusecase.ReproductionHarnessError,
		},
		{
			name: "mixed results",
			affected: func() []issueagentusecase.RunObservation {
				runs := observedRuns(1, affectedSHA, issueagentusecase.RunAssertionFailed)
				runs[2].Outcome = issueagentusecase.RunPassed
				return runs
			}(),
			base: observedRuns(4, baseSHA, issueagentusecase.RunAssertionFailed),
			want: issueagentusecase.ReproductionInconclusive,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			evaluation, err := issueagentusecase.EvaluateReproduction(
				versions, "three-node-cluster", test.affected, test.base,
				900, "artifact", artifactDigest,
				[]issueagent.TestFile{{
					Path:    "test/e2e/issue_agent/issue_42/reproduction_test.go",
					BlobSHA: "34567890abcdef1234567890abcdef1234567890",
				}},
			)
			require.NoError(t, err)
			require.Equal(t, test.want, evaluation.Decision)
		})
	}
}

func TestEvaluateReproductionRejectsWrongTopologyAssertionAndRunCount(t *testing.T) {
	t.Parallel()

	versions := issueagent.Versions{
		ReportedRef: affectedSHA, AffectedSHA: affectedSHA,
		DiagnosisBaseSHA: baseSHA,
	}
	affected := observedRuns(1, affectedSHA, issueagentusecase.RunAssertionFailed)
	base := observedRuns(4, baseSHA, issueagentusecase.RunAssertionFailed)
	affected[0].Topology = "single-node-cluster"
	_, err := issueagentusecase.EvaluateReproduction(
		versions, "three-node-cluster", affected, base, 900, "artifact",
		artifactDigest, []issueagent.TestFile{{Path: "x", BlobSHA: affectedSHA}},
	)
	require.Error(t, err)

	affected = observedRuns(1, affectedSHA, issueagentusecase.RunAssertionFailed)
	affected[1].AssertionSHA256 = artifactDigest
	_, err = issueagentusecase.EvaluateReproduction(
		versions, "three-node-cluster", affected, base, 900, "artifact",
		artifactDigest, []issueagent.TestFile{{Path: "x", BlobSHA: affectedSHA}},
	)
	require.Error(t, err)

	_, err = issueagentusecase.EvaluateReproduction(
		versions, "three-node-cluster", affected[:2], base, 900, "artifact",
		artifactDigest, []issueagent.TestFile{{Path: "x", BlobSHA: affectedSHA}},
	)
	require.Error(t, err)
}

func TestBuildReproductionTaskOnlyAllowsFocusedE2EChanges(t *testing.T) {
	t.Parallel()

	task, err := issueagentusecase.BuildReproductionTask(
		issueagentusecase.ReproductionTaskInput{
			Repository: "WuKongIM/WuKongIM", IssueNumber: 42,
			Generation: 1, Sequence: 4,
			OperationID:      assertionDigest,
			CheckpointDigest: binaryDigest,
			PolicyDigest:     commandDigest,
			PromptDigest:     artifactDigest,
			Versions: issueagent.Versions{
				ReportedRef: affectedSHA, AffectedSHA: affectedSHA,
				DiagnosisBaseSHA: baseSHA,
			},
			FrozenIssue: "reproduce this behavior",
			InstructionDigests: []issueagent.FileDigest{{
				Path: "AGENTS.md", SHA256: assertionDigest,
			}},
			Topology: "three-node-cluster",
			Provider: issueagent.ProviderDeepSeek, Model: "deepseek-chat",
		},
	)
	require.NoError(t, err)
	require.Equal(t, issueagent.PhaseReproduce, task.Phase)
	require.False(t, task.ProductionChangesAllowed)
	require.Equal(t, 3, task.RequiredRuns)
	require.Equal(t,
		[]string{"test/e2e/issue_agent/issue_42"},
		task.AllowedPaths,
	)

	_, err = issueagentusecase.BuildReproductionTask(
		issueagentusecase.ReproductionTaskInput{
			Repository: "WuKongIM/WuKongIM", IssueNumber: 42,
			HarnessPaths: []string{"internal/runtime"},
		},
	)
	require.Error(t, err)
}

func TestReproductionTopologyDefaultsToSingleNodeAndHonorsExplicitMultiNode(
	t *testing.T,
) {
	t.Parallel()

	for _, environment := range []string{
		"",
		"Linux; HTTP API",
		"Linux; single-node cluster; Go SDK",
		"Linux; 单节点集群; HTTP API",
	} {
		topology, err := issueagentusecase.ReproductionTopology(environment)
		require.NoError(t, err)
		require.Equal(t, "single-node-cluster", topology)
	}
	for _, environment := range []string{
		"Linux; three-node cluster; Go SDK",
		"Linux; multi-node cluster; HTTP API",
		"Linux; 2-node cluster; HTTP API",
		"Linux; cluster has 5 nodes; HTTP API",
		"Linux；三节点集群；HTTP API",
		"Linux；2节点集群；HTTP API",
		"Linux；多节点；HTTP API",
	} {
		topology, err := issueagentusecase.ReproductionTopology(environment)
		require.NoError(t, err)
		require.Equal(t, "three-node-cluster", topology)
	}

	_, err := issueagentusecase.ReproductionTopology(
		"single-node cluster upgraded to three-node cluster",
	)
	require.Error(t, err)

	_, err = issueagentusecase.ReproductionTopology("Linux; 0-node cluster")
	require.Error(t, err)
}

func observedRuns(
	firstID int64,
	sha string,
	outcome issueagentusecase.RunOutcome,
) []issueagentusecase.RunObservation {
	result := make([]issueagentusecase.RunObservation, 3)
	for index := range result {
		result[index] = issueagentusecase.RunObservation{
			RunID: firstID + int64(index), SourceSHA: sha,
			BinarySHA256: binaryDigest, CommandSHA256: commandDigest,
			Assertion:       "message is delivered exactly once",
			AssertionSHA256: assertionDigest,
			Topology:        "three-node-cluster", Outcome: outcome,
		}
	}
	return result
}
