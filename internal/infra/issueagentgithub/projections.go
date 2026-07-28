package issueagentgithub

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// DraftPullRequest is a deterministic Draft PR projection.
type DraftPullRequest struct {
	Title string
	Body  string
	Head  string
	Base  string
}

type pullResponse struct {
	Number      int64  `json:"number"`
	State       string `json:"state"`
	Draft       bool   `json:"draft"`
	Mergeable   *bool  `json:"mergeable"`
	MergeCommit string `json:"merge_commit_sha"`
	Base        struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"base"`
	Head struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
}

// CreateIssueComment appends one bounded comment and verifies the echoed body.
func (client *Client) CreateIssueComment(
	ctx context.Context,
	issueNumber int64,
	body string,
) (IssueComment, error) {
	if issueNumber <= 0 || strings.TrimSpace(body) == "" ||
		len(body) > maxCheckpointComment {
		return IssueComment{}, errors.New("Issue comment is invalid")
	}
	var response struct {
		ID   int64 `json:"id"`
		User struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"user"`
		Body      string   `json:"body"`
		CreatedAt jsonTime `json:"created_at"`
		UpdatedAt jsonTime `json:"updated_at"`
	}
	if err := client.requestJSON(
		ctx,
		http.MethodPost,
		"/repos/"+client.repository+"/issues/"+strconv.FormatInt(issueNumber, 10)+"/comments",
		struct {
			Body string `json:"body"`
		}{Body: body},
		&response,
		http.StatusCreated,
		http.StatusOK,
	); err != nil {
		return IssueComment{}, err
	}
	if response.ID <= 0 || response.User.Login == "" ||
		response.User.Type != "Bot" || response.Body != body ||
		response.CreatedAt.Time.IsZero() ||
		!response.CreatedAt.Time.Equal(response.UpdatedAt.Time) {
		return IssueComment{}, errors.New("GitHub Issue comment response is inconsistent")
	}
	return IssueComment{
		ID: response.ID, Author: response.User.Login, AuthorType: response.User.Type,
		Body: response.Body, CreatedAt: response.CreatedAt.Time,
		UpdatedAt: response.UpdatedAt.Time,
	}, nil
}

// SetIssueLabels replaces labels with an exact sorted, unique set.
func (client *Client) SetIssueLabels(
	ctx context.Context,
	issueNumber int64,
	labels []string,
) error {
	if issueNumber <= 0 || len(labels) > 100 || !slices.IsSorted(labels) {
		return errors.New("Issue labels are invalid")
	}
	for index, label := range labels {
		if strings.TrimSpace(label) == "" || len(label) > 100 ||
			index > 0 && labels[index-1] == label {
			return errors.New("Issue labels must be bounded, sorted, and unique")
		}
	}
	var response []struct {
		Name string `json:"name"`
	}
	if err := client.requestJSON(
		ctx,
		http.MethodPut,
		"/repos/"+client.repository+"/issues/"+strconv.FormatInt(issueNumber, 10)+"/labels",
		struct {
			Labels []string `json:"labels"`
		}{Labels: labels},
		&response,
		http.StatusOK,
	); err != nil {
		return err
	}
	actual := make([]string, 0, len(response))
	for _, label := range response {
		actual = append(actual, label.Name)
	}
	slices.Sort(actual)
	if !slices.Equal(actual, labels) {
		return errors.New("GitHub label response does not match requested set")
	}
	return nil
}

// CreateDraftPullRequest opens an exact Agent-branch-to-main Draft PR.
func (client *Client) CreateDraftPullRequest(
	ctx context.Context,
	input DraftPullRequest,
) (PullRequestFacts, error) {
	if strings.TrimSpace(input.Title) == "" || len(input.Title) > 256 ||
		len(input.Body) > 64<<10 || !agentRefPattern.MatchString(input.Head) ||
		input.Base != "main" {
		return PullRequestFacts{}, errors.New("Draft pull request input is invalid")
	}
	var response pullResponse
	if err := client.requestJSON(
		ctx,
		http.MethodPost,
		"/repos/"+client.repository+"/pulls",
		struct {
			Title string `json:"title"`
			Body  string `json:"body"`
			Head  string `json:"head"`
			Base  string `json:"base"`
			Draft bool   `json:"draft"`
		}{
			Title: input.Title, Body: input.Body, Head: input.Head,
			Base: input.Base, Draft: true,
		},
		&response,
		http.StatusCreated,
	); err != nil {
		return PullRequestFacts{}, err
	}
	pull, err := validatePullResponse(response)
	if err != nil || !pull.Draft || pull.HeadRef != input.Head ||
		pull.BaseRef != input.Base {
		return PullRequestFacts{}, errors.New("created Draft pull request is inconsistent")
	}
	return pull, nil
}

// UpdatePullRequest updates only bounded title/body/state projections.
func (client *Client) UpdatePullRequest(
	ctx context.Context,
	number int64,
	title string,
	body string,
	state string,
) (PullRequestFacts, error) {
	if number <= 0 || strings.TrimSpace(title) == "" || len(title) > 256 ||
		len(body) > 64<<10 || state != "open" && state != "closed" {
		return PullRequestFacts{}, errors.New("pull request update is invalid")
	}
	var response pullResponse
	if err := client.requestJSON(
		ctx,
		http.MethodPatch,
		"/repos/"+client.repository+"/pulls/"+strconv.FormatInt(number, 10),
		struct {
			Title string `json:"title"`
			Body  string `json:"body"`
			State string `json:"state"`
		}{Title: title, Body: body, State: state},
		&response,
		http.StatusOK,
	); err != nil {
		return PullRequestFacts{}, err
	}
	return validatePullResponse(response)
}

// MarkPullRequestReady converts one Draft PR without merging it.
func (client *Client) MarkPullRequestReady(
	ctx context.Context,
	number int64,
) (PullRequestFacts, error) {
	if number <= 0 {
		return PullRequestFacts{}, errors.New("pull request number is invalid")
	}
	var response pullResponse
	if err := client.requestJSON(
		ctx,
		http.MethodPost,
		"/repos/"+client.repository+"/pulls/"+strconv.FormatInt(number, 10)+"/ready_for_review",
		struct{}{},
		&response,
		http.StatusOK,
	); err != nil {
		return PullRequestFacts{}, err
	}
	pull, err := validatePullResponse(response)
	if err != nil || pull.Draft {
		return PullRequestFacts{}, errors.New("pull request did not become ready")
	}
	return pull, nil
}

// CreateTrackingIssue creates a bounded child/backport tracking Issue.
func (client *Client) CreateTrackingIssue(
	ctx context.Context,
	title string,
	body string,
	labels []string,
) (int64, error) {
	if strings.TrimSpace(title) == "" || len(title) > 256 ||
		len(body) > 64<<10 || len(labels) > 20 || !slices.IsSorted(labels) {
		return 0, errors.New("tracking Issue input is invalid")
	}
	var response struct {
		Number int64  `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
	}
	if err := client.requestJSON(
		ctx,
		http.MethodPost,
		"/repos/"+client.repository+"/issues",
		struct {
			Title  string   `json:"title"`
			Body   string   `json:"body"`
			Labels []string `json:"labels"`
		}{Title: title, Body: body, Labels: labels},
		&response,
		http.StatusCreated,
	); err != nil {
		return 0, err
	}
	if response.Number <= 0 || response.Title != title || response.Body != body {
		return 0, errors.New("tracking Issue response is inconsistent")
	}
	return response.Number, nil
}

func validatePullResponse(response pullResponse) (PullRequestFacts, error) {
	if response.Number <= 0 ||
		(response.State != "open" && response.State != "closed") ||
		response.Base.Ref != "main" ||
		!agentRefPattern.MatchString(response.Head.Ref) ||
		!gitObjectPattern.MatchString(response.Base.SHA) ||
		!gitObjectPattern.MatchString(response.Head.SHA) ||
		response.MergeCommit != "" && !gitObjectPattern.MatchString(response.MergeCommit) {
		return PullRequestFacts{}, errors.New("GitHub pull request response is invalid")
	}
	return PullRequestFacts{
		Number: response.Number, State: response.State, Draft: response.Draft,
		Mergeable: response.Mergeable, BaseRef: response.Base.Ref,
		BaseSHA: response.Base.SHA, HeadRef: response.Head.Ref,
		HeadSHA: response.Head.SHA, MergeCommit: response.MergeCommit,
	}, nil
}
