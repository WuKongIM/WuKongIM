package reviewagent_test

import (
	"encoding/json"
	"strings"
	"testing"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	"github.com/stretchr/testify/require"
)

func TestExplanationResultIsGenerationBoundAndBounded(t *testing.T) {
	t.Parallel()

	result := contract.ExplanationResult{
		SchemaVersion: 1,
		Generation:    validGeneration(),
		Reply:         "The stale read permits two owners for the same slot.",
	}
	require.NoError(t, contract.ValidateExplanationResult(result))
	digest, err := contract.ExplanationResultDigest(result)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(digest, "sha256:"))

	result.Reply = strings.Repeat("x", (64<<10)+1)
	require.EqualError(
		t,
		contract.ValidateExplanationResult(result),
		"invalid Review explanation reply",
	)

	result.Reply = "Do not repeat <!-- review-agent-status:pr-42 -->"
	require.EqualError(
		t,
		contract.ValidateExplanationResult(result),
		"invalid Review explanation reply",
	)
}

func TestExplanationResultStrictDecodeRoundTrip(t *testing.T) {
	t.Parallel()

	want := contract.ExplanationResult{
		SchemaVersion: 1,
		Generation:    validGeneration(),
		Reply:         "The check failed before publication, so no review was posted.",
	}
	body, err := json.Marshal(want)
	require.NoError(t, err)
	got, err := contract.DecodeExplanationResult(
		strings.NewReader(string(body)),
		int64(len(body)),
	)
	require.NoError(t, err)
	require.Equal(t, want, got)

	digestBefore, err := contract.ExplanationResultDigest(want)
	require.NoError(t, err)
	digestAfter, err := contract.ExplanationResultDigest(got)
	require.NoError(t, err)
	require.Equal(t, digestBefore, digestAfter)

	unknown := strings.Replace(
		string(body),
		`"reply":`,
		`"publish":true,"reply":`,
		1,
	)
	_, err = contract.DecodeExplanationResult(
		strings.NewReader(unknown),
		int64(len(unknown)),
	)
	require.Error(t, err)

	_, err = contract.DecodeExplanationResult(
		strings.NewReader(string(body)),
		int64(len(body)-1),
	)
	require.EqualError(t, err, "JSON input exceeds byte limit")
}

func TestExplanationResultRejectsSchemaAndGenerationMismatch(t *testing.T) {
	t.Parallel()

	result := contract.ExplanationResult{
		SchemaVersion: 2,
		Generation:    validGeneration(),
		Reply:         "bounded reply",
	}
	require.EqualError(
		t,
		contract.ValidateExplanationResult(result),
		"unsupported Review explanation schema version",
	)

	result.SchemaVersion = 1
	result.Generation.HeadSHA = ""
	require.Error(t, contract.ValidateExplanationResult(result))
	_, err := contract.ExplanationResultDigest(result)
	require.Error(t, err)
}
