// Package reviewagentcli exposes the standalone Review Agent process boundary.
package reviewagentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	verify "github.com/WuKongIM/WuKongIM/internal/runtime/reviewagentverify"
	usecase "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
)

const maxCLIInputBytes = 128 << 20

// ReconcileGitHubRequest contains only an event hint; the operation must
// re-read all authoritative GitHub and signed-state facts.
type ReconcileGitHubRequest struct {
	PullRequest   int64               `json:"pull_request"`
	SignalKind    usecase.SignalKind  `json:"signal_kind"`
	RunID         int64               `json:"run_id"`
	WorkerAttempt uint32              `json:"worker_attempt"`
	CommentID     int64               `json:"comment_id"`
	Completion    *usecase.Completion `json:"completion"`
}

// ReconcileGitHubResponse is a pure, fenced execution plan.
type ReconcileGitHubResponse struct {
	Plan             usecase.ReconcilePlan `json:"plan"`
	NextState        *contract.ReviewState `json:"next_state"`
	StateChanged     bool                  `json:"state_changed"`
	StateFound       bool                  `json:"state_found"`
	StateHeadSHA     string                `json:"state_head_sha"`
	SchedulerChanged bool                  `json:"scheduler_changed"`
	SchedulerFound   bool                  `json:"scheduler_found"`
	SchedulerHeadSHA string                `json:"scheduler_head_sha"`
}

// BuildContextRequest binds context construction to an exact generation.
type BuildContextRequest struct {
	PullRequest   int64                       `json:"pull_request"`
	Generation    contract.GenerationIdentity `json:"generation"`
	ReviewReason  string                      `json:"review_reason"`
	PriorFindings []contract.Finding          `json:"prior_findings"`
	Risk          verify.RiskSelection        `json:"risk"`
}

// BuildContextResponse carries one complete bounded model input.
type BuildContextResponse struct {
	Context contract.ReviewContext `json:"context"`
	Digest  string                 `json:"digest"`
}

// VerifyBaselineRequest runs every mandatory protected check exactly once.
type VerifyBaselineRequest struct {
	Context     contract.ReviewContext `json:"context"`
	CollectOnly bool                   `json:"collect_only"`
}

// ValidateReviewResultRequest binds model output to trusted evidence and tree.
type ValidateReviewResultRequest struct {
	Context          contract.ReviewContext  `json:"context"`
	Evidence         contract.ReviewEvidence `json:"evidence"`
	Result           contract.ReviewResult   `json:"result"`
	BeforeTreeDigest string                  `json:"before_tree_digest"`
	AfterTreeDigest  string                  `json:"after_tree_digest"`
}

// ValidateExplanationRequest binds one advisory reply to the exact signed
// generation selected by the Controller.
type ValidateExplanationRequest struct {
	Generation contract.GenerationIdentity `json:"generation"`
	Result     contract.ExplanationResult  `json:"result"`
}

// ValidateExplanationResponse is the trusted bounded reply identity.
type ValidateExplanationResponse struct {
	Digest        string `json:"digest"`
	ResponseBytes uint64 `json:"response_bytes"`
}

// AppendStateRequest selects only one of the two fixed Review state targets.
type AppendStateRequest struct {
	Kind              string                  `json:"kind"`
	ReviewState       *contract.ReviewState   `json:"review_state"`
	SchedulerState    *usecase.SchedulerState `json:"scheduler_state"`
	ExpectedParentSHA string                  `json:"expected_parent_sha"`
	ExistingBranch    bool                    `json:"existing_branch"`
}

// AppendStateResponse identifies the accepted signed state commit.
type AppendStateResponse struct {
	HeadSHA string `json:"head_sha"`
}

// PublishReviewRequest projects one exact durable decision.
type PublishReviewRequest struct {
	ExpectedStateHead string                      `json:"expected_state_head"`
	State             contract.ReviewState        `json:"state"`
	Result            *contract.ReviewResult      `json:"result"`
	Explanation       *contract.ExplanationResult `json:"explanation"`
}

// PublishReviewResponse deliberately exposes no mutable projection identity.
type PublishReviewResponse struct{}

// Operations are the role-separated composition hooks.
type Operations struct {
	ReconcileGitHub func(
		context.Context,
		ReconcileGitHubRequest,
	) (ReconcileGitHubResponse, error)
	RecoverReview func(
		context.Context,
		ReconcileGitHubRequest,
	) (ReconcileGitHubResponse, error)
	BuildContext func(
		context.Context,
		BuildContextRequest,
	) (BuildContextResponse, error)
	VerifyBaseline func(
		context.Context,
		VerifyBaselineRequest,
	) (contract.ReviewEvidence, error)
	ValidateReviewResult func(
		context.Context,
		ValidateReviewResultRequest,
	) (verify.ValidatedDecision, error)
	ValidateExplanation func(
		context.Context,
		ValidateExplanationRequest,
	) (ValidateExplanationResponse, error)
	AppendState func(
		context.Context,
		AppendStateRequest,
	) (AppendStateResponse, error)
	PublishReview func(
		context.Context,
		PublishReviewRequest,
	) (PublishReviewResponse, error)
}

// Run accepts exactly one command and one bounded strict JSON input.
func Run(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	operations Operations,
) int {
	if ctx == nil || len(args) != 1 || stdin == nil ||
		stdout == nil || stderr == nil {
		return writeFailure(stderr)
	}
	var output any
	var err error
	switch args[0] {
	case "normalize-review-result":
		output, err = contract.DecodeReviewResult(stdin, maxCLIInputBytes)
	case "reconcile-github":
		var request ReconcileGitHubRequest
		if err = decodeStrict(stdin, &request); err == nil &&
			operations.ReconcileGitHub != nil {
			output, err = operations.ReconcileGitHub(ctx, request)
		} else if err == nil {
			err = errors.New("operation unavailable")
		}
	case "recover-review":
		var request ReconcileGitHubRequest
		if err = decodeStrict(stdin, &request); err == nil &&
			operations.RecoverReview != nil {
			output, err = operations.RecoverReview(ctx, request)
		} else if err == nil {
			err = errors.New("operation unavailable")
		}
	case "build-context":
		var request BuildContextRequest
		if err = decodeStrict(stdin, &request); err == nil &&
			operations.BuildContext != nil {
			output, err = operations.BuildContext(ctx, request)
		} else if err == nil {
			err = errors.New("operation unavailable")
		}
	case "verify-baseline":
		var request VerifyBaselineRequest
		if err = decodeStrict(stdin, &request); err == nil &&
			operations.VerifyBaseline != nil {
			output, err = operations.VerifyBaseline(ctx, request)
		} else if err == nil {
			err = errors.New("operation unavailable")
		}
	case "validate-review-result":
		var request ValidateReviewResultRequest
		if err = decodeStrict(stdin, &request); err == nil &&
			operations.ValidateReviewResult != nil {
			output, err = operations.ValidateReviewResult(ctx, request)
		} else if err == nil {
			err = errors.New("operation unavailable")
		}
	case "validate-explanation":
		var request ValidateExplanationRequest
		if err = decodeStrict(stdin, &request); err == nil &&
			operations.ValidateExplanation != nil {
			output, err = operations.ValidateExplanation(ctx, request)
		} else if err == nil {
			err = errors.New("operation unavailable")
		}
	case "append-state":
		var request AppendStateRequest
		if err = decodeStrict(stdin, &request); err == nil &&
			operations.AppendState != nil {
			output, err = operations.AppendState(ctx, request)
		} else if err == nil {
			err = errors.New("operation unavailable")
		}
	case "publish-review":
		var request PublishReviewRequest
		if err = decodeStrict(stdin, &request); err == nil &&
			operations.PublishReview != nil {
			output, err = operations.PublishReview(ctx, request)
		} else if err == nil {
			err = errors.New("operation unavailable")
		}
	default:
		err = errors.New("unknown Review Agent command")
	}
	if err != nil {
		return writeFailure(stderr)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(output); err != nil {
		return writeFailure(stderr)
	}
	return 0
}

func decodeStrict(reader io.Reader, output any) error {
	body, err := io.ReadAll(io.LimitReader(reader, maxCLIInputBytes+1))
	if err != nil || len(body) > maxCLIInputBytes {
		return errors.New("Review Agent command input is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return errors.New("Review Agent command input is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("Review Agent command input is invalid")
	}
	return nil
}

func writeFailure(stderr io.Writer) int {
	if stderr != nil {
		_, _ = io.WriteString(stderr, "review agent command failed\n")
	}
	return 1
}
