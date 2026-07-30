package reviewagent_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

func TestDecodeReviewResultIsStrictAndBounded(t *testing.T) {
	t.Parallel()

	body := validResultJSON()
	result, err := reviewagent.DecodeReviewResult(strings.NewReader(body), 32<<10)
	require.NoError(t, err)
	require.Equal(t, reviewagent.DecisionChangesRequired, result.Decision)

	tests := map[string]string{
		"unknown authority field": strings.Replace(
			body,
			`"decision":"changes_required"`,
			`"decision":"changes_required","check_conclusion":"success"`,
			1,
		),
		"trailing JSON": body + `{}`,
		"invalid decision": strings.Replace(
			body, `"changes_required"`, `"merge"`, 1,
		),
		"oversized summary": strings.Replace(
			body, `"summary":"race remains"`,
			`"summary":"`+strings.Repeat("x", reviewagent.MaxSummaryBytes+1)+`"`,
			1,
		),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, decodeErr := reviewagent.DecodeReviewResult(
				strings.NewReader(input),
				int64(len(input)+1),
			)
			require.Error(t, decodeErr)
		})
	}

	_, err = reviewagent.DecodeReviewResult(
		strings.NewReader(body),
		int64(len(body)-1),
	)
	require.EqualError(t, err, "JSON input exceeds byte limit")
}

func TestReviewResultLimitsFindingsAndRequiresCompleteInventory(t *testing.T) {
	t.Parallel()

	result, err := reviewagent.DecodeReviewResult(
		strings.NewReader(validResultJSON()),
		32<<10,
	)
	require.NoError(t, err)
	require.NoError(t, reviewagent.ValidateReviewResult(result))

	result.InventoryComplete = false
	require.EqualError(
		t,
		reviewagent.ValidateReviewResult(result),
		"Review result does not cover the complete inventory",
	)

	result.InventoryComplete = true
	result.Findings = make(
		[]reviewagent.Finding,
		reviewagent.MaxFindings+1,
	)
	require.EqualError(
		t,
		reviewagent.ValidateReviewResult(result),
		"Review result contains too many findings",
	)
}

func validResultJSON() string {
	identity := validGeneration()
	return fmt.Sprintf(`{
		"schema_version":1,
		"generation":{
			"repository":%q,
			"pull_request":%d,
			"head_sha":%q,
			"base_sha":%q,
			"test_merge_sha":%q,
			"intent_digest":%q,
			"generation":%d,
			"state_parent_sha":%q
		},
		"decision":"changes_required",
		"summary":"race remains",
		"inventory_complete":true,
		"file_assessments":[{
			"path":"internal/runtime/delivery/queue.go",
			"risk":"high",
			"summary":"queue close races with enqueue"
		}],
		"findings":[{
			"kind":"blocking",
			"dimension":"security_runtime",
			"title":"enqueue can race with close",
			"path":"internal/runtime/delivery/queue.go",
			"line_start":81,
			"line_end":84,
			"scenario":"one goroutine closes while another enqueues",
			"impact":"send on closed channel panics",
			"evidence":["check:go-race"],
			"resolution":"serialize close and enqueue"
		}],
		"sources":["check:go-race"],
		"unresolved_uncertainty":""
	}`,
		identity.Repository,
		identity.PullRequest,
		identity.HeadSHA,
		identity.BaseSHA,
		identity.TestMergeSHA,
		identity.IntentDigest,
		identity.Generation,
		identity.StateParentSHA,
	)
}
