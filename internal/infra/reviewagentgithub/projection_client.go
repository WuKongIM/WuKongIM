package reviewagentgithub

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	usecase "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
)

// InstallationAppSlug verifies the authenticated installation App.
func (client *Client) InstallationAppSlug(
	ctx context.Context,
) (string, error) {
	var payload struct {
		AppSlug string `json:"app_slug"`
	}
	if err := client.getJSON(ctx, "/installation", &payload); err != nil {
		return "", err
	}
	if payload.AppSlug == "" || len(payload.AppSlug) > 100 {
		return "", errors.New("GitHub installation App is invalid")
	}
	return payload.AppSlug, nil
}

// CreateIssueComment creates the sole mutable Review status comment.
func (client *Client) CreateIssueComment(
	ctx context.Context,
	pullRequest int64,
	body string,
) (int64, error) {
	if pullRequest <= 0 || body == "" || len(body) > 128<<10 {
		return 0, errors.New("Review status comment is invalid")
	}
	var response struct {
		ID int64 `json:"id"`
	}
	if err := client.requestJSON(
		ctx,
		http.MethodPost,
		fmt.Sprintf(
			"/repos/%s/issues/%d/comments",
			client.repository,
			pullRequest,
		),
		struct {
			Body string `json:"body"`
		}{Body: body},
		&response,
		http.StatusCreated,
	); err != nil {
		return 0, err
	}
	if response.ID <= 0 {
		return 0, errors.New("Review status comment response is invalid")
	}
	return response.ID, nil
}

// UpdateIssueComment updates one exact App-owned status comment.
func (client *Client) UpdateIssueComment(
	ctx context.Context,
	commentID int64,
	body string,
) error {
	if commentID <= 0 || body == "" || len(body) > 128<<10 {
		return errors.New("Review status comment update is invalid")
	}
	var response struct {
		ID int64 `json:"id"`
	}
	if err := client.requestJSON(
		ctx,
		http.MethodPatch,
		fmt.Sprintf(
			"/repos/%s/issues/comments/%d",
			client.repository,
			commentID,
		),
		struct {
			Body string `json:"body"`
		}{Body: body},
		&response,
		http.StatusOK,
	); err != nil {
		return err
	}
	if response.ID != commentID {
		return errors.New("Review status comment update response is invalid")
	}
	return nil
}

// CreateReview submits one formal Review with at most 20 inline comments.
func (client *Client) CreateReview(
	ctx context.Context,
	pullRequest int64,
	headSHA string,
	event usecase.FormalReview,
	body string,
	comments []InlineReviewComment,
) (int64, error) {
	if pullRequest <= 0 ||
		!gitSHAPattern.MatchString(headSHA) ||
		len(body) == 0 || len(body) > 128<<10 ||
		len(comments) > contract.MaxInlineComments {
		return 0, errors.New("formal Review request is invalid")
	}
	switch event {
	case usecase.FormalReviewApprove,
		usecase.FormalReviewRequestChanges,
		usecase.FormalReviewComment:
	default:
		return 0, errors.New("formal Review event is invalid")
	}
	type inline struct {
		Path string `json:"path"`
		Line int    `json:"line"`
		Side string `json:"side"`
		Body string `json:"body"`
	}
	payloadComments := make([]inline, 0, len(comments))
	for _, comment := range comments {
		if comment.Path == "" || comment.Line <= 0 ||
			comment.Body == "" || len(comment.Body) > 64<<10 {
			return 0, errors.New("inline Review comment is invalid")
		}
		payloadComments = append(payloadComments, inline{
			Path: comment.Path, Line: comment.Line,
			Side: "RIGHT", Body: comment.Body,
		})
	}
	var response struct {
		ID int64 `json:"id"`
	}
	if err := client.requestJSON(
		ctx,
		http.MethodPost,
		fmt.Sprintf(
			"/repos/%s/pulls/%d/reviews",
			client.repository,
			pullRequest,
		),
		struct {
			CommitID string   `json:"commit_id"`
			Event    string   `json:"event"`
			Body     string   `json:"body"`
			Comments []inline `json:"comments,omitempty"`
		}{
			CommitID: headSHA, Event: string(event),
			Body: body, Comments: payloadComments,
		},
		&response,
		http.StatusOK,
	); err != nil {
		return 0, err
	}
	if response.ID <= 0 {
		return 0, errors.New("formal Review response is invalid")
	}
	return response.ID, nil
}

// CreateCheckRun creates the sole Review Agent Verdict for one generation.
func (client *Client) CreateCheckRun(
	ctx context.Context,
	headSHA string,
	externalID string,
	conclusion usecase.CheckConclusion,
	title string,
	summary string,
) (int64, error) {
	if err := validateCheckWrite(
		conclusion,
		title,
		summary,
	); err != nil ||
		!gitSHAPattern.MatchString(headSHA) ||
		externalID == "" || len(externalID) > 256 {
		return 0, errors.New("Review Check Run request is invalid")
	}
	var response struct {
		ID int64 `json:"id"`
	}
	if err := client.requestJSON(
		ctx,
		http.MethodPost,
		"/repos/"+client.repository+"/check-runs",
		checkRunPayload(headSHA, externalID, conclusion, title, summary),
		&response,
		http.StatusCreated,
	); err != nil {
		return 0, err
	}
	if response.ID <= 0 {
		return 0, errors.New("Review Check Run response is invalid")
	}
	return response.ID, nil
}

// UpdateCheckRun repairs one existing App-owned Review Agent Verdict.
func (client *Client) UpdateCheckRun(
	ctx context.Context,
	checkRunID int64,
	conclusion usecase.CheckConclusion,
	title string,
	summary string,
) error {
	if checkRunID <= 0 ||
		validateCheckWrite(conclusion, title, summary) != nil {
		return errors.New("Review Check Run update is invalid")
	}
	var response struct {
		ID int64 `json:"id"`
	}
	if err := client.requestJSON(
		ctx,
		http.MethodPatch,
		fmt.Sprintf(
			"/repos/%s/check-runs/%d",
			client.repository,
			checkRunID,
		),
		checkRunPayload("", "", conclusion, title, summary),
		&response,
		http.StatusOK,
	); err != nil {
		return err
	}
	if response.ID != checkRunID {
		return errors.New("Review Check Run update response is invalid")
	}
	return nil
}

// CreateLifecycleCheckRun creates a queued/in-progress/fail-closed projection.
func (client *Client) CreateLifecycleCheckRun(
	ctx context.Context,
	headSHA string,
	externalID string,
	status string,
	conclusion *usecase.CheckConclusion,
	title string,
	summary string,
) (int64, error) {
	payload, err := lifecycleCheckPayload(
		headSHA,
		externalID,
		status,
		conclusion,
		title,
		summary,
	)
	if err != nil {
		return 0, err
	}
	var response struct {
		ID int64 `json:"id"`
	}
	if err := client.requestJSON(
		ctx,
		http.MethodPost,
		"/repos/"+client.repository+"/check-runs",
		payload,
		&response,
		http.StatusCreated,
	); err != nil {
		return 0, err
	}
	if response.ID <= 0 {
		return 0, errors.New("Review lifecycle Check response is invalid")
	}
	return response.ID, nil
}

// UpdateLifecycleCheckRun repairs a queued/in-progress/fail-closed projection.
func (client *Client) UpdateLifecycleCheckRun(
	ctx context.Context,
	checkRunID int64,
	status string,
	conclusion *usecase.CheckConclusion,
	title string,
	summary string,
) error {
	if checkRunID <= 0 {
		return errors.New("Review lifecycle Check update is invalid")
	}
	payload, err := lifecycleCheckPayload(
		"",
		"",
		status,
		conclusion,
		title,
		summary,
	)
	if err != nil {
		return err
	}
	var response struct {
		ID int64 `json:"id"`
	}
	if err := client.requestJSON(
		ctx,
		http.MethodPatch,
		fmt.Sprintf(
			"/repos/%s/check-runs/%d",
			client.repository,
			checkRunID,
		),
		payload,
		&response,
		http.StatusOK,
	); err != nil {
		return err
	}
	if response.ID != checkRunID {
		return errors.New("Review lifecycle Check update response is invalid")
	}
	return nil
}

func validateCheckWrite(
	conclusion usecase.CheckConclusion,
	title string,
	summary string,
) error {
	switch conclusion {
	case usecase.CheckSuccess,
		usecase.CheckFailure,
		usecase.CheckActionRequired:
	default:
		return errors.New("Review Check conclusion is invalid")
	}
	if title == "" || len(title) > 255 ||
		summary == "" || len(summary) > 64<<10 {
		return errors.New("Review Check output is invalid")
	}
	return nil
}

func checkRunPayload(
	headSHA string,
	externalID string,
	conclusion usecase.CheckConclusion,
	title string,
	summary string,
) map[string]any {
	payload := map[string]any{
		"name":       "Review Agent Verdict",
		"status":     "completed",
		"conclusion": string(conclusion),
		"output": map[string]string{
			"title": title, "summary": summary,
		},
	}
	if headSHA != "" {
		payload["head_sha"] = headSHA
		payload["external_id"] = externalID
	}
	return payload
}

func lifecycleCheckPayload(
	headSHA string,
	externalID string,
	status string,
	conclusion *usecase.CheckConclusion,
	title string,
	summary string,
) (map[string]any, error) {
	if (status != "in_progress" && status != "completed") ||
		title == "" || len(title) > 255 ||
		summary == "" || len(summary) > 64<<10 {
		return nil, errors.New("Review lifecycle Check request is invalid")
	}
	if (status == "in_progress" && conclusion != nil) ||
		(status == "completed" &&
			(conclusion == nil ||
				(*conclusion != usecase.CheckActionRequired &&
					*conclusion != usecase.CheckSuccess &&
					*conclusion != usecase.CheckFailure))) {
		return nil, errors.New("Review lifecycle Check conclusion is invalid")
	}
	if headSHA != "" &&
		(!gitSHAPattern.MatchString(headSHA) ||
			externalID == "" || len(externalID) > 256) {
		return nil, errors.New("Review lifecycle Check identity is invalid")
	}
	payload := map[string]any{
		"name":   "Review Agent Verdict",
		"status": status,
		"output": map[string]string{
			"title": title, "summary": summary,
		},
	}
	if conclusion != nil {
		payload["conclusion"] = string(*conclusion)
	}
	if headSHA != "" {
		payload["head_sha"] = headSHA
		payload["external_id"] = externalID
	}
	return payload, nil
}
