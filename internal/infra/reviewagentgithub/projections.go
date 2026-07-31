package reviewagentgithub

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	usecase "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
)

// ReviewStateReader exposes only signed per-PR state reads.
type ReviewStateReader interface {
	Load(context.Context, int64) (LoadedReviewState, bool, error)
}

// ReviewFactsReader exposes the fresh, complete PR snapshot.
type ReviewFactsReader interface {
	ReadPullRequestMetadata(context.Context, int64) (PullRequestSnapshot, error)
}

// InlineReviewComment is one bounded line-level finding projection.
type InlineReviewComment struct {
	Path string
	Line int
	Body string
}

// ProjectionWriter is deliberately unable to merge, close, dismiss, resolve,
// edit branches, or create commits.
type ProjectionWriter interface {
	CreateIssueComment(context.Context, int64, string) (int64, error)
	UpdateIssueComment(context.Context, int64, string) error
	CreateReview(
		context.Context,
		int64,
		string,
		usecase.FormalReview,
		string,
		[]InlineReviewComment,
	) (int64, error)
	CreateCheckRun(
		context.Context,
		string,
		string,
		usecase.CheckConclusion,
		string,
		string,
	) (int64, error)
	UpdateCheckRun(
		context.Context,
		int64,
		usecase.CheckConclusion,
		string,
		string,
	) error
	CreateLifecycleCheckRun(
		context.Context,
		string,
		string,
		string,
		*usecase.CheckConclusion,
		string,
		string,
	) (int64, error)
	UpdateLifecycleCheckRun(
		context.Context,
		int64,
		string,
		*usecase.CheckConclusion,
		string,
		string,
	) error
}

// ReviewPublisher owns the sole Check, Review, and status-comment projection.
type ReviewPublisher struct {
	repository string
	appSlug    string
	appLogin   string
	state      ReviewStateReader
	facts      ReviewFactsReader
	writer     ProjectionWriter
}

// ReviewPublicationRequest is fenced to one exact signed state head.
type ReviewPublicationRequest struct {
	ExpectedStateHead string
	State             contract.ReviewState
	Result            *contract.ReviewResult
	Explanation       *contract.ExplanationResult
}

// ReviewPublication records repairable GitHub projection identities.
type ReviewPublication struct {
	StatusCommentID      int64
	ReviewID             int64
	CheckRunID           int64
	ExplanationCommentID int64
}

// NewReviewPublisher creates a write boundary with no code mutation method.
func NewReviewPublisher(
	repository string,
	appSlug string,
	appLogin string,
	state ReviewStateReader,
	facts ReviewFactsReader,
	writer ProjectionWriter,
) (*ReviewPublisher, error) {
	if !repositoryPattern.MatchString(repository) ||
		strings.TrimSpace(appSlug) == "" ||
		len(appSlug) > 100 ||
		!appBotLoginPattern.MatchString(appLogin) ||
		state == nil || facts == nil || writer == nil {
		return nil, errors.New("Review Publisher configuration is invalid")
	}
	return &ReviewPublisher{
		repository: repository,
		appSlug:    appSlug,
		appLogin:   appLogin,
		state:      state,
		facts:      facts,
		writer:     writer,
	}, nil
}

// PublishDecision re-reads authority and idempotently repairs only the three
// supported GitHub projections.
func (publisher *ReviewPublisher) PublishDecision(
	ctx context.Context,
	request ReviewPublicationRequest,
) (ReviewPublication, error) {
	if publisher == nil || ctx == nil ||
		!gitSHAPattern.MatchString(request.ExpectedStateHead) {
		return ReviewPublication{}, errors.New(
			"Review publication request is invalid",
		)
	}
	if err := contract.ValidateReviewState(request.State); err != nil {
		return ReviewPublication{}, err
	}
	if request.State.Generation.Repository != publisher.repository {
		return ReviewPublication{}, errors.New(
			"Review publication generation is inconsistent",
		)
	}
	if request.Result != nil {
		if err := contract.ValidateReviewResult(*request.Result); err != nil {
			return ReviewPublication{}, err
		}
		if contract.MustGenerationDigest(request.State.Generation) !=
			contract.MustGenerationDigest(request.Result.Generation) {
			return ReviewPublication{}, errors.New(
				"Review publication generation is inconsistent",
			)
		}
		resultDigest, err := contract.ReviewResultDigest(*request.Result)
		if err != nil || resultDigest != request.State.ResultDigest {
			return ReviewPublication{}, errors.New(
				"Review publication result digest is inconsistent",
			)
		}
	}
	if request.Explanation != nil {
		if request.Result != nil {
			return ReviewPublication{}, errors.New(
				"Review publication contains multiple payloads",
			)
		}
		if err := contract.ValidateExplanationResult(
			*request.Explanation,
		); err != nil {
			return ReviewPublication{}, err
		}
		if contract.MustGenerationDigest(request.State.Generation) !=
			contract.MustGenerationDigest(
				request.Explanation.Generation,
			) {
			return ReviewPublication{}, errors.New(
				"Review explanation generation is inconsistent",
			)
		}
		explanationDigest, err := contract.ExplanationResultDigest(
			*request.Explanation,
		)
		if err != nil ||
			explanationDigest != request.State.ExplanationDigest {
			return ReviewPublication{}, errors.New(
				"Review explanation digest is inconsistent",
			)
		}
	}
	loaded, found, err := publisher.state.Load(
		ctx,
		request.State.Generation.PullRequest,
	)
	if err != nil || !found ||
		loaded.HeadSHA != request.ExpectedStateHead ||
		contract.MustGenerationDigest(loaded.State.Generation) !=
			contract.MustGenerationDigest(request.State.Generation) {
		return ReviewPublication{}, errors.New(
			"Review publication signed state is stale",
		)
	}
	loadedDigest, loadedErr := contract.ReviewStateDigest(loaded.State)
	requestDigest, requestErr := contract.ReviewStateDigest(request.State)
	if loadedErr != nil || requestErr != nil || loadedDigest != requestDigest {
		return ReviewPublication{}, errors.New(
			"Review publication signed state content changed",
		)
	}
	snapshot, err := publisher.facts.ReadPullRequestMetadata(
		ctx,
		request.State.Generation.PullRequest,
	)
	if err != nil {
		return ReviewPublication{}, err
	}
	if !snapshot.Facts.Open ||
		snapshot.Facts.Draft ||
		!samePublishedGeneration(snapshot.Facts, request.State.Generation) {
		return ReviewPublication{}, errors.New(
			"Review publication pull request is stale",
		)
	}
	if request.Explanation != nil {
		return publisher.publishExplanation(
			ctx,
			snapshot,
			request.State,
			*request.Explanation,
		)
	}
	if request.Result == nil {
		return publisher.publishLifecycle(ctx, snapshot, request.State)
	}
	plan, err := usecase.PlanPublication(
		request.State,
		usecase.GovernanceFacts{
			ControlPlaneChanged:   snapshot.Facts.ControlPlaneChanged,
			OwnerApproved:         snapshot.Facts.OwnerApproved,
			HumanChangesRequested: snapshot.Facts.HumanChangesRequested,
		},
	)
	if err != nil {
		return ReviewPublication{}, err
	}
	statusID, err := publisher.ensureStatus(
		ctx,
		snapshot,
		request.State,
		*request.Result,
		plan,
	)
	if err != nil {
		return ReviewPublication{}, err
	}
	reviewID, err := publisher.ensureReview(
		ctx,
		snapshot,
		request.State,
		*request.Result,
		plan,
	)
	if err != nil {
		return ReviewPublication{}, err
	}
	checkID, err := publisher.ensureCheck(
		ctx,
		snapshot,
		request.State,
		*request.Result,
		plan,
	)
	if err != nil {
		return ReviewPublication{}, err
	}
	publication := ReviewPublication{
		StatusCommentID: statusID,
		ReviewID:        reviewID,
		CheckRunID:      checkID,
	}
	if request.State.ExplanationReply != "" {
		explanation, explanationErr := publisher.publishExplanation(
			ctx,
			snapshot,
			request.State,
			contract.ExplanationResult{
				SchemaVersion: 1,
				Generation:    request.State.Generation,
				Reply:         request.State.ExplanationReply,
			},
		)
		if explanationErr != nil {
			return ReviewPublication{}, explanationErr
		}
		publication.ExplanationCommentID =
			explanation.ExplanationCommentID
	}
	return publication, nil
}

func (publisher *ReviewPublisher) publishExplanation(
	ctx context.Context,
	snapshot PullRequestSnapshot,
	state contract.ReviewState,
	explanation contract.ExplanationResult,
) (ReviewPublication, error) {
	marker := "<!-- review-agent-explanation:" +
		state.ExplanationDigest + " -->"
	for _, comment := range snapshot.IssueComments {
		if comment.Author == publisher.appLogin &&
			hasProjectionMarker(comment.Body, marker) {
			return ReviewPublication{
				ExplanationCommentID: comment.ID,
			}, nil
		}
	}
	commentID, err := publisher.writer.CreateIssueComment(
		ctx,
		state.Generation.PullRequest,
		marker+"\n\n"+explanation.Reply,
	)
	if err != nil {
		return ReviewPublication{}, err
	}
	return ReviewPublication{ExplanationCommentID: commentID}, nil
}

func (publisher *ReviewPublisher) publishLifecycle(
	ctx context.Context,
	snapshot PullRequestSnapshot,
	state contract.ReviewState,
) (ReviewPublication, error) {
	body, err := usecase.RenderStatus(state, time.Now().UTC())
	if err != nil {
		return ReviewPublication{}, err
	}
	marker := fmt.Sprintf(
		"<!-- review-agent-status:pr-%d -->",
		state.Generation.PullRequest,
	)
	var statusMatches []IssueComment
	for _, comment := range snapshot.IssueComments {
		if comment.Author == publisher.appLogin &&
			hasProjectionMarker(comment.Body, marker) {
			statusMatches = append(statusMatches, comment)
		}
	}
	if len(statusMatches) > 1 {
		return ReviewPublication{}, errors.New(
			"multiple Review Agent status comments",
		)
	}
	statusID := int64(0)
	if len(statusMatches) == 1 {
		statusID = statusMatches[0].ID
		if err := publisher.writer.UpdateIssueComment(
			ctx,
			statusID,
			body,
		); err != nil {
			return ReviewPublication{}, err
		}
	} else {
		statusID, err = publisher.writer.CreateIssueComment(
			ctx,
			state.Generation.PullRequest,
			body,
		)
		if err != nil {
			return ReviewPublication{}, err
		}
	}
	status := "completed"
	var conclusion *usecase.CheckConclusion
	switch state.Phase {
	case contract.PhaseQueued, contract.PhaseReviewing:
		status = "in_progress"
	case contract.PhaseApproved,
		contract.PhaseChangesRequired,
		contract.PhaseInconclusive:
		plan, planErr := usecase.PlanPublication(
			state,
			usecase.GovernanceFacts{
				ControlPlaneChanged:   snapshot.Facts.ControlPlaneChanged,
				OwnerApproved:         snapshot.Facts.OwnerApproved,
				HumanChangesRequested: snapshot.Facts.HumanChangesRequested,
			},
		)
		if planErr != nil {
			return ReviewPublication{}, planErr
		}
		value := plan.Conclusion
		conclusion = &value
	default:
		value := usecase.CheckActionRequired
		conclusion = &value
	}
	externalID := "review-agent/" +
		contract.MustGenerationDigest(state.Generation)
	var checks []CheckRun
	for _, check := range snapshot.Checks {
		if check.Name == "Review Agent Verdict" &&
			check.ExternalID == externalID &&
			check.AppSlug == publisher.appSlug {
			checks = append(checks, check)
		}
	}
	if len(checks) > 1 {
		return ReviewPublication{}, errors.New(
			"multiple Review Agent Verdict Check Runs",
		)
	}
	checkID := int64(0)
	if len(checks) == 1 {
		checkID = checks[0].ID
		err = publisher.writer.UpdateLifecycleCheckRun(
			ctx,
			checkID,
			status,
			conclusion,
			"Review Agent Verdict",
			state.Reason,
		)
	} else {
		checkID, err = publisher.writer.CreateLifecycleCheckRun(
			ctx,
			state.Generation.HeadSHA,
			externalID,
			status,
			conclusion,
			"Review Agent Verdict",
			state.Reason,
		)
	}
	if err != nil {
		return ReviewPublication{}, err
	}
	reviewID, err := publisher.ensureLifecycleReview(
		ctx,
		snapshot,
		state,
	)
	if err != nil {
		return ReviewPublication{}, err
	}
	publication := ReviewPublication{
		StatusCommentID: statusID,
		ReviewID:        reviewID,
		CheckRunID:      checkID,
	}
	if state.ExplanationReply != "" {
		explanation, explanationErr := publisher.publishExplanation(
			ctx,
			snapshot,
			state,
			contract.ExplanationResult{
				SchemaVersion: 1,
				Generation:    state.Generation,
				Reply:         state.ExplanationReply,
			},
		)
		if explanationErr != nil {
			return ReviewPublication{}, explanationErr
		}
		publication.ExplanationCommentID =
			explanation.ExplanationCommentID
	}
	return publication, nil
}

func (publisher *ReviewPublisher) ensureLifecycleReview(
	ctx context.Context,
	snapshot PullRequestSnapshot,
	state contract.ReviewState,
) (int64, error) {
	if !decisionPhaseForProjection(state.Phase) {
		return 0, nil
	}
	marker := formalReviewMarker(state)
	for _, review := range snapshot.Reviews {
		if review.Author == publisher.appLogin &&
			review.CommitID == state.Generation.HeadSHA &&
			hasProjectionMarker(review.Body, marker) {
			return review.ID, nil
		}
	}
	plan, err := usecase.PlanPublication(
		state,
		usecase.GovernanceFacts{
			ControlPlaneChanged:   snapshot.Facts.ControlPlaneChanged,
			OwnerApproved:         snapshot.Facts.OwnerApproved,
			HumanChangesRequested: snapshot.Facts.HumanChangesRequested,
		},
	)
	if err != nil {
		return 0, err
	}
	body := marker + "\n\nReview Agent recovered the signed `" +
		string(state.Phase) + "` decision."
	inline := make([]InlineReviewComment, 0, contract.MaxInlineComments)
	validLines := rightSideReviewLines(snapshot.CommentPatches)
	for _, finding := range state.PriorFindings {
		body += "\n\n---\n\n" + renderFinding(finding)
		if finding.Kind == contract.FindingBlocking &&
			finding.Path != "" &&
			validLines[finding.Path][int(finding.LineStart)] &&
			len(inline) < contract.MaxInlineComments {
			inline = append(inline, InlineReviewComment{
				Path: finding.Path,
				Line: int(finding.LineStart),
				Body: renderFinding(finding),
			})
		}
	}
	if plan.WaitingForOwner {
		body += "\n\nControl-plane changes still require approval from @WuKongIM/review-agent-owners."
	}
	if len(body) > 64<<10 {
		return 0, errors.New("recovered formal Review body exceeds context bound")
	}
	return publisher.writer.CreateReview(
		ctx,
		state.Generation.PullRequest,
		state.Generation.HeadSHA,
		plan.Review,
		body,
		inline,
	)
}

func decisionPhaseForProjection(phase contract.Phase) bool {
	return phase == contract.PhaseApproved ||
		phase == contract.PhaseChangesRequired ||
		phase == contract.PhaseInconclusive
}

func (publisher *ReviewPublisher) ensureStatus(
	ctx context.Context,
	snapshot PullRequestSnapshot,
	state contract.ReviewState,
	result contract.ReviewResult,
	plan usecase.PublicationPlan,
) (int64, error) {
	body, err := renderDecisionStatus(state, result, plan)
	if err != nil {
		return 0, err
	}
	var matches []IssueComment
	for _, comment := range snapshot.IssueComments {
		if comment.Author == publisher.appLogin &&
			hasProjectionMarker(comment.Body, plan.StatusMarker) {
			matches = append(matches, comment)
		}
	}
	if len(matches) > 1 {
		return 0, errors.New("multiple Review Agent status comments")
	}
	if len(matches) == 1 {
		if err := publisher.writer.UpdateIssueComment(
			ctx,
			matches[0].ID,
			body,
		); err != nil {
			return 0, err
		}
		return matches[0].ID, nil
	}
	return publisher.writer.CreateIssueComment(
		ctx,
		state.Generation.PullRequest,
		body,
	)
}

func (publisher *ReviewPublisher) ensureReview(
	ctx context.Context,
	snapshot PullRequestSnapshot,
	state contract.ReviewState,
	result contract.ReviewResult,
	plan usecase.PublicationPlan,
) (int64, error) {
	marker := formalReviewMarker(state)
	for _, review := range snapshot.Reviews {
		if review.Author == publisher.appLogin &&
			review.CommitID == state.Generation.HeadSHA &&
			hasProjectionMarker(review.Body, marker) {
			return review.ID, nil
		}
	}
	inline := make([]InlineReviewComment, 0, contract.MaxInlineComments)
	validLines := rightSideReviewLines(snapshot.CommentPatches)
	for _, finding := range result.Findings {
		if finding.Kind != contract.FindingBlocking ||
			finding.Path == "" ||
			!validLines[finding.Path][int(finding.LineStart)] ||
			len(inline) == contract.MaxInlineComments {
			continue
		}
		inline = append(inline, InlineReviewComment{
			Path: finding.Path,
			Line: int(finding.LineStart),
			Body: renderFinding(finding),
		})
	}
	body := marker + "\n\n" + result.Summary
	for _, finding := range result.Findings {
		if finding.Kind != contract.FindingBlocking {
			continue
		}
		body += "\n\n---\n\n" + renderFinding(finding)
	}
	if plan.WaitingForOwner {
		body += "\n\nControl-plane changes still require approval from @WuKongIM/review-agent-owners."
	}
	if len(body) > 64<<10 {
		return 0, errors.New(
			"formal Review body exceeds Issue Agent context bound",
		)
	}
	return publisher.writer.CreateReview(
		ctx,
		state.Generation.PullRequest,
		state.Generation.HeadSHA,
		plan.Review,
		body,
		inline,
	)
}

func rightSideReviewLines(
	patches map[string]string,
) map[string]map[int]bool {
	result := make(map[string]map[int]bool, len(patches))
	for path, patch := range patches {
		lines := make(map[int]bool)
		next := 0
		inHunk := false
		for _, line := range strings.Split(patch, "\n") {
			if strings.HasPrefix(line, "@@ ") {
				plus := strings.Index(line, " +")
				if plus < 0 {
					inHunk = false
					continue
				}
				end := strings.Index(line[plus+2:], " ")
				if end < 0 {
					inHunk = false
					continue
				}
				rangeText := line[plus+2 : plus+2+end]
				startText, _, _ := strings.Cut(rangeText, ",")
				start, err := strconv.Atoi(startText)
				if err != nil || start <= 0 {
					inHunk = false
					continue
				}
				next = start
				inHunk = true
				continue
			}
			if !inHunk || line == "" {
				continue
			}
			switch line[0] {
			case '+', ' ':
				lines[next] = true
				next++
			case '-':
			case '\\':
			default:
				inHunk = false
			}
		}
		result[path] = lines
	}
	return result
}

func (publisher *ReviewPublisher) ensureCheck(
	ctx context.Context,
	snapshot PullRequestSnapshot,
	state contract.ReviewState,
	result contract.ReviewResult,
	plan usecase.PublicationPlan,
) (int64, error) {
	var matches []CheckRun
	for _, check := range snapshot.Checks {
		if check.Name == plan.CheckName &&
			check.ExternalID == plan.ExternalID &&
			check.AppSlug == publisher.appSlug {
			matches = append(matches, check)
		}
	}
	if len(matches) > 1 {
		return 0, errors.New("multiple Review Agent Verdict Check Runs")
	}
	summary := result.Summary
	if plan.WaitingForOwner {
		summary += "\n\nWaiting for @WuKongIM/review-agent-owners approval."
	}
	if plan.HumanReviewStillBlocks {
		summary += "\n\nA human REQUEST_CHANGES Review still blocks merging."
	}
	if len(matches) == 1 {
		if err := publisher.writer.UpdateCheckRun(
			ctx,
			matches[0].ID,
			plan.Conclusion,
			"Review Agent Verdict",
			summary,
		); err != nil {
			return 0, err
		}
		return matches[0].ID, nil
	}
	return publisher.writer.CreateCheckRun(
		ctx,
		state.Generation.HeadSHA,
		plan.ExternalID,
		plan.Conclusion,
		"Review Agent Verdict",
		summary,
	)
}

func samePublishedGeneration(
	facts usecase.PullRequestFacts,
	generation contract.GenerationIdentity,
) bool {
	return facts.Repository == generation.Repository &&
		facts.PullRequest == generation.PullRequest &&
		facts.HeadSHA == generation.HeadSHA &&
		facts.BaseSHA == generation.BaseSHA &&
		normalizedTestMergeSHA(facts.TestMergeSHA) == generation.TestMergeSHA &&
		facts.IntentDigest == generation.IntentDigest
}

func normalizedTestMergeSHA(value string) string {
	if value == "" {
		return strings.Repeat("0", 40)
	}
	return value
}

func formalReviewMarker(state contract.ReviewState) string {
	return fmt.Sprintf(
		"<!-- review-agent-review:%s -->",
		contract.MustGenerationDigest(state.Generation),
	)
}

func hasProjectionMarker(body string, marker string) bool {
	return body == marker || strings.HasPrefix(body, marker+"\n")
}

func renderDecisionStatus(
	state contract.ReviewState,
	result contract.ReviewResult,
	plan usecase.PublicationPlan,
) (string, error) {
	body, err := usecase.RenderStatus(state, time.Now().UTC())
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"%s\n- decision: `%s`\n- check: `%s`\n\n%s",
		body,
		result.Decision,
		plan.Conclusion,
		result.Summary,
	), nil
}

func renderFinding(finding contract.Finding) string {
	return fmt.Sprintf(
		"**%s**\n\n%s\n\nImpact: %s\n\nRequired resolution: %s",
		finding.Title,
		finding.Scenario,
		finding.Impact,
		finding.Resolution,
	)
}
