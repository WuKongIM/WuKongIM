package issueagent_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/stretchr/testify/require"
)

func TestCandidateEvidenceStrictRoundTripAndDigestBindVerifierDecision(t *testing.T) {
	t.Parallel()

	want := validCandidateEvidence()
	body, err := json.Marshal(want)
	require.NoError(t, err)
	got, err := issueagent.DecodeCandidateEvidence(bytes.NewReader(body), int64(len(body)))
	require.NoError(t, err)
	require.Equal(t, want, got)

	digest, err := issueagent.CandidateEvidenceDigest(got)
	require.NoError(t, err)
	got.Commands[0].DurationMS++
	changed, err := issueagent.CandidateEvidenceDigest(got)
	require.NoError(t, err)
	require.NotEqual(t, digest, changed, "trusted command evidence must be digest-bound")
}

func TestCandidateEvidenceAcceptsExplicitNonPublishableRiskDecision(t *testing.T) {
	t.Parallel()

	for _, risk := range []issueagent.CandidateRisk{
		issueagent.CandidateRiskLow,
		issueagent.CandidateRiskInvestigation,
		issueagent.CandidateRiskHigh,
	} {
		risk := risk
		t.Run(string(risk), func(t *testing.T) {
			t.Parallel()
			evidence := validCandidateEvidence()
			evidence.Risk = risk
			evidence.PublicationEligible = false
			evidence.FailureReason = "verification did not authorize publication"
			require.NoError(t, issueagent.ValidateCandidateEvidence(evidence))
		})
	}
}

func TestCandidateEvidenceRejectsAuthorityAndCommandContradictions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*issueagent.CandidateEvidence)
	}{
		{
			name: "unknown risk",
			mutate: func(evidence *issueagent.CandidateEvidence) {
				evidence.Risk = issueagent.CandidateRisk("medium")
			},
		},
		{
			name: "publishable result lacks commands",
			mutate: func(evidence *issueagent.CandidateEvidence) {
				evidence.Commands = nil
			},
		},
		{
			name: "publishable result contains failed command",
			mutate: func(evidence *issueagent.CandidateEvidence) {
				evidence.Commands[0].ExitCode = 1
			},
		},
		{
			name: "high risk cannot publish",
			mutate: func(evidence *issueagent.CandidateEvidence) {
				evidence.Risk = issueagent.CandidateRiskHigh
			},
		},
		{
			name: "publishable result cannot claim a failure",
			mutate: func(evidence *issueagent.CandidateEvidence) {
				evidence.FailureReason = "contradiction"
			},
		},
		{
			name: "rejected result requires reason",
			mutate: func(evidence *issueagent.CandidateEvidence) {
				evidence.PublicationEligible = false
			},
		},
		{
			name: "command arguments cannot be empty",
			mutate: func(evidence *issueagent.CandidateEvidence) {
				evidence.Commands[0].Arguments = nil
			},
		},
		{
			name: "working directory cannot escape repository",
			mutate: func(evidence *issueagent.CandidateEvidence) {
				evidence.Commands[0].WorkingDir = "../outside"
			},
		},
		{
			name: "duration must be recorded",
			mutate: func(evidence *issueagent.CandidateEvidence) {
				evidence.Commands[0].DurationMS = 0
			},
		},
		{
			name: "output digest must be canonical",
			mutate: func(evidence *issueagent.CandidateEvidence) {
				evidence.Commands[0].StdoutDigest = "sha256:short"
			},
		},
		{
			name: "timestamp must be UTC",
			mutate: func(evidence *issueagent.CandidateEvidence) {
				evidence.CreatedAt = evidence.CreatedAt.In(time.FixedZone("offset", 3600))
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			evidence := validCandidateEvidence()
			test.mutate(&evidence)
			require.Error(t, issueagent.ValidateCandidateEvidence(evidence))
		})
	}
}
