package reviewagent_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

func TestReviewEvidenceRoundTripPreservesNamedCheckOutcomes(t *testing.T) {
	t.Parallel()

	evidence := validEvidence()
	require.NoError(t, reviewagent.ValidateReviewEvidence(evidence))
	body, err := json.Marshal(evidence)
	require.NoError(t, err)

	decoded, err := reviewagent.DecodeReviewEvidence(
		strings.NewReader(string(body)),
		int64(len(body)),
	)
	require.NoError(t, err)
	require.Equal(t, evidence, decoded)

	digestBefore, err := reviewagent.ReviewEvidenceDigest(evidence)
	require.NoError(t, err)
	digestAfter, err := reviewagent.ReviewEvidenceDigest(decoded)
	require.NoError(t, err)
	require.Equal(t, digestBefore, digestAfter)

	decoded.Checks[0].DurationMS++
	changedDigest, err := reviewagent.ReviewEvidenceDigest(decoded)
	require.NoError(t, err)
	require.NotEqual(t, digestBefore, changedDigest)
}

func TestReviewEvidenceRejectsIncompleteOrForgedEvidence(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*reviewagent.ReviewEvidence){
		"schema version": func(evidence *reviewagent.ReviewEvidence) {
			evidence.SchemaVersion = 2
		},
		"invalid generation": func(evidence *reviewagent.ReviewEvidence) {
			evidence.Generation.Generation = 0
		},
		"missing checks": func(evidence *reviewagent.ReviewEvidence) {
			evidence.Checks = nil
		},
		"too many checks": func(evidence *reviewagent.ReviewEvidence) {
			evidence.Checks = make(
				[]reviewagent.CheckEvidence,
				reviewagent.MaxChecks+1,
			)
		},
		"duplicate check": func(evidence *reviewagent.ReviewEvidence) {
			evidence.Checks = append(evidence.Checks, evidence.Checks[0])
		},
		"complete with failure": func(evidence *reviewagent.ReviewEvidence) {
			evidence.FailureReason = "runner unavailable"
		},
		"incomplete without reason": func(evidence *reviewagent.ReviewEvidence) {
			evidence.Complete = false
		},
		"local timestamp": func(evidence *reviewagent.ReviewEvidence) {
			evidence.CreatedAt = time.Date(
				2026, 8, 1, 8, 0, 0, 0,
				time.FixedZone("local", 3600),
			)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			evidence := validEvidence()
			mutate(&evidence)
			require.Error(t, reviewagent.ValidateReviewEvidence(evidence))
		})
	}

	t.Run("bounded incomplete reason", func(t *testing.T) {
		t.Parallel()
		evidence := validEvidence()
		evidence.Complete = false
		evidence.FailureReason = "trusted runner timed out"
		require.NoError(t, reviewagent.ValidateReviewEvidence(evidence))
	})
}

func TestReviewEvidenceRejectsInvalidNamedCheckRecords(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*reviewagent.CheckEvidence){
		"name": func(check *reviewagent.CheckEvidence) {
			check.Name = "Go Unit"
		},
		"command digest": func(check *reviewagent.CheckEvidence) {
			check.CommandDigest = "sha256:short"
		},
		"stdout digest": func(check *reviewagent.CheckEvidence) {
			check.StdoutDigest = ""
		},
		"stderr digest": func(check *reviewagent.CheckEvidence) {
			check.StderrDigest = ""
		},
		"stdout bound": func(check *reviewagent.CheckEvidence) {
			check.Stdout = strings.Repeat(
				"x",
				reviewagent.MaxCheckOutputExcerptBytes+1,
			)
		},
		"stderr NUL": func(check *reviewagent.CheckEvidence) {
			check.Stderr = "bad\x00output"
		},
		"duration": func(check *reviewagent.CheckEvidence) {
			check.DurationMS = 0
		},
		"passing exit code": func(check *reviewagent.CheckEvidence) {
			check.ExitCode = 1
		},
		"failed zero exit code": func(check *reviewagent.CheckEvidence) {
			check.Outcome = reviewagent.CheckOutcomeFailed
			check.ExitCode = 0
		},
		"outcome": func(check *reviewagent.CheckEvidence) {
			check.Outcome = "skipped"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			evidence := validEvidence()
			evidence.Checks = evidence.Checks[:1]
			mutate(&evidence.Checks[0])
			require.Error(t, reviewagent.ValidateReviewEvidence(evidence))
		})
	}
}

func TestDecodeReviewEvidenceIsStrictAndBounded(t *testing.T) {
	t.Parallel()

	body, err := json.Marshal(validEvidence())
	require.NoError(t, err)
	unknown := strings.Replace(
		string(body),
		`"complete":true`,
		`"complete":true,"trusted":true`,
		1,
	)
	_, err = reviewagent.DecodeReviewEvidence(
		strings.NewReader(unknown),
		int64(len(unknown)),
	)
	require.Error(t, err)

	_, err = reviewagent.DecodeReviewEvidence(
		strings.NewReader(string(body)),
		int64(len(body)-1),
	)
	require.EqualError(t, err, "JSON input exceeds byte limit")
}
