package issueagent_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/stretchr/testify/require"
)

func TestCheckpointCanonicalBytesBindFirstAuthorizedSnapshot(t *testing.T) {
	t.Parallel()

	diagnosisBase := "0123456789abcdef0123456789abcdef01234567"
	checkpoint := issueagent.Checkpoint{
		SchemaVersion: 1,
		Repository:    "WuKongIM/WuKongIM",
		IssueNumber:   42,
		Generation:    1,
		Sequence:      1,
		State:         issueagent.StateAuthorized,
		FrozenInput: issueagent.FrozenInput{
			IssueBodySHA256:    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			AffectedVersion:    "v2.0.0",
			AcceptedCommentIDs: []int64{},
			AuthorizationEvent: "evt-42",
			AuthorizedBy:       "maintainer",
		},
		Versions: issueagent.Versions{
			ReportedRef:      "v2.0.0",
			DiagnosisBaseSHA: diagnosisBase,
		},
		Budget:     issueagent.Budget{},
		NextAction: issueagent.ActionPinVersions,
	}

	require.NoError(t, issueagent.ValidateCheckpoint(checkpoint))

	got, err := issueagent.CanonicalCheckpoint(checkpoint)
	require.NoError(t, err)
	require.Equal(t,
		`{"schema_version":1,"repository":"WuKongIM/WuKongIM","issue_number":42,"generation":1,"sequence":1,"expected_previous_checkpoint_id":null,"previous_checkpoint_sha256":null,"state":"authorized","frozen_input":{"issue_body_sha256":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","affected_version":"v2.0.0","accepted_comment_ids":[],"authorization_event":"evt-42","authorized_by":"maintainer"},"versions":{"reported_ref":"v2.0.0","affected_sha":"","diagnosis_base_sha":"0123456789abcdef0123456789abcdef01234567","integration_base_sha":null},"lease":null,"reproduction":null,"work":null,"diagnosis":null,"validation":null,"budget":{"reproduction_attempts":0,"remediation_attempts":0,"ci_repair_attempts":0,"infrastructure_attempts":0,"worker_seconds":0},"model":null,"control":null,"next_action":"pin_versions"}`,
		string(got),
	)
}

func TestCheckpointValidatesDurableLeaseReferences(t *testing.T) {
	t.Parallel()

	task := issueagent.TaskEnvelope{
		SchemaVersion: 1, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, Generation: 1, Sequence: 1,
		OperationID:      "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Phase:            issueagent.PhaseReproduce,
		CheckpointDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		PolicyDigest:     "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		PromptDigest:     "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		AffectedSHA:      "1234567890abcdef1234567890abcdef12345678",
		DiagnosisBaseSHA: "0123456789abcdef0123456789abcdef01234567",
		FrozenIssue:      "bug",
		InstructionDigests: []issueagent.FileDigest{{
			Path:   "AGENTS.md",
			SHA256: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		}},
		AllowedPaths: []string{"test/e2e/issue_agent/issue_42"},
		AllowedCommands: []issueagent.CommandRule{{
			Executable: "go", ArgvPrefix: []string{"test"}, MaxArgs: 1,
		}},
		Limits: issueagent.ResourceLimits{
			WallTime: time.Minute, MaxOutputBytes: 1024,
			MaxFiles: 1, MaxFileBytes: 1024, MaxTotalBytes: 1024,
		},
		RequiredTopology: "three-node-cluster", RequiredRuns: 3,
		Provider: issueagent.ProviderCodex, Model: "gpt-5",
	}
	taskDigest, err := issueagent.TaskDigest(task)
	require.NoError(t, err)
	checkpoint := issueagent.Checkpoint{
		SchemaVersion: 1, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, Generation: 1, Sequence: 1,
		State: issueagent.StateReproducing,
		FrozenInput: issueagent.FrozenInput{
			IssueBodySHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			AffectedVersion: "v2.0.0", AcceptedCommentIDs: []int64{},
			AuthorizationEvent: "evt-42", AuthorizedBy: "maintainer",
		},
		Versions: issueagent.Versions{
			ReportedRef:      "v2.0.0",
			AffectedSHA:      "1234567890abcdef1234567890abcdef12345678",
			DiagnosisBaseSHA: "0123456789abcdef0123456789abcdef01234567",
		},
		Lease: &issueagent.Lease{
			OperationID: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Workflow:    "issue-agent-run.yml", DispatchRequestID: "dispatch-42",
			Phase:      issueagent.PhaseReproduce,
			IssuedAt:   time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC),
			ExpiresAt:  time.Date(2026, 7, 28, 12, 35, 0, 0, time.UTC),
			TaskSHA256: taskDigest, Task: task,
			ReservedSeconds: 2100, Heavy: true,
		},
		NextAction: issueagent.ActionReproduce,
	}
	require.NoError(t, issueagent.ValidateCheckpoint(checkpoint))

	checkpoint.Lease.ExpiresAt = checkpoint.Lease.IssuedAt
	require.Error(t, issueagent.ValidateCheckpoint(checkpoint))
}

func TestCheckpointRejectsMalformedIdentityBeforeSigning(t *testing.T) {
	t.Parallel()

	checkpoint := issueagent.Checkpoint{
		SchemaVersion: 1,
		Repository:    "../other",
		IssueNumber:   42,
		Generation:    1,
		Sequence:      1,
		State:         issueagent.StateAuthorized,
		FrozenInput: issueagent.FrozenInput{
			IssueBodySHA256:    "sha256:not-a-digest",
			AffectedVersion:    "latest",
			AcceptedCommentIDs: []int64{3, 2},
			AuthorizationEvent: "evt-42",
			AuthorizedBy:       "maintainer",
		},
		Versions: issueagent.Versions{
			ReportedRef:      "latest",
			DiagnosisBaseSHA: "main",
		},
		NextAction: issueagent.ActionPinVersions,
	}

	_, err := issueagent.CanonicalCheckpoint(checkpoint)
	require.Error(t, err)
}

func TestFailedValidationEvidenceIsDurableOutsideReadyState(t *testing.T) {
	t.Parallel()

	previousID := int64(9)
	previousDigest := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	checkpoint := issueagent.Checkpoint{
		SchemaVersion: 1, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, Generation: 1, Sequence: 8,
		ExpectedPreviousCheckpointID: &previousID,
		PreviousCheckpointSHA256:     &previousDigest,
		State:                        issueagent.StateReadyForHuman,
		FrozenInput: issueagent.FrozenInput{
			IssueBodySHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			AffectedVersion: "v2.0.0", AcceptedCommentIDs: []int64{},
			AuthorizationEvent: "event", AuthorizedBy: "maintainer",
		},
		Versions: issueagent.Versions{
			ReportedRef:      "v2.0.0",
			AffectedSHA:      "0123456789abcdef0123456789abcdef01234567",
			DiagnosisBaseSHA: "89abcdef0123456789abcdef0123456789abcdef",
		},
		NextAction: issueagent.ActionWaitForHuman,
	}
	checkpoint.Validation = &issueagent.Validation{
		HeadSHA:        "0123456789abcdef0123456789abcdef01234567",
		TestMergeSHA:   "89abcdef0123456789abcdef0123456789abcdef",
		GateGeneration: 10, RequestRunID: 11, EvidenceRunID: 12,
		RequiredSuites: []string{"go-e2e", "go-fast"},
		LocalPasses:    0, Conclusion: "failure",
	}
	require.NoError(t, issueagent.ValidateCheckpoint(checkpoint))
}

func TestCheckpointRejectsLifecycleStateWithoutRequiredEvidence(t *testing.T) {
	t.Parallel()

	checkpoint := issueagent.Checkpoint{
		SchemaVersion: 1, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, Generation: 1, Sequence: 1,
		State: issueagent.StateReproduced,
		FrozenInput: issueagent.FrozenInput{
			IssueBodySHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			AffectedVersion: "v2.0.0", AcceptedCommentIDs: []int64{},
			AuthorizationEvent: "evt-42", AuthorizedBy: "maintainer",
		},
		Versions: issueagent.Versions{
			ReportedRef:      "v2.0.0",
			AffectedSHA:      "1234567890abcdef1234567890abcdef12345678",
			DiagnosisBaseSHA: "0123456789abcdef0123456789abcdef01234567",
		},
		NextAction: issueagent.ActionOpenDraftPR,
	}
	require.Error(t, issueagent.ValidateCheckpoint(checkpoint))
}

func TestCheckpointBoundsMechanicalIntegrationAttempts(t *testing.T) {
	t.Parallel()

	checkpoint := checkpointWithReproduction()
	require.NoError(t, issueagent.ValidateCheckpoint(checkpoint))

	checkpoint.Work.MechanicalRebaseAttempts = 2
	require.Error(t, issueagent.ValidateCheckpoint(checkpoint))
}
