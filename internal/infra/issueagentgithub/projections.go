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
	Title       string `json:"title"`
	Body        string `json:"body"`
	State       string `json:"state"`
	Draft       bool   `json:"draft"`
	Mergeable   *bool  `json:"mergeable"`
	Merged      bool   `json:"merged"`
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
		pull.BaseRef != input.Base || response.Title != input.Title ||
		response.Body != input.Body {
		return PullRequestFacts{}, errors.New("created Draft pull request is inconsistent")
	}
	return pull, nil
}

// EnsureDraftPullRequest creates the deterministic Draft PR or reuses the one
// exact open Draft left by an interrupted Publisher attempt.
func (client *Client) EnsureDraftPullRequest(
	ctx context.Context,
	input DraftPullRequest,
) (PullRequestFacts, error) {
	if strings.TrimSpace(input.Title) == "" || len(input.Title) > 256 ||
		len(input.Body) > 64<<10 || !agentRefPattern.MatchString(input.Head) ||
		input.Base != "main" {
		return PullRequestFacts{}, errors.New("Draft pull request input is invalid")
	}
	owner := strings.SplitN(client.repository, "/", 2)[0]
	endpoint := client.endpoint("/repos/" + client.repository + "/pulls")
	query := endpoint.Query()
	query.Set("state", "all")
	query.Set("head", owner+":"+input.Head)
	query.Set("base", input.Base)
	query.Set("per_page", "100")
	query.Set("page", "1")
	endpoint.RawQuery = query.Encode()
	var responses []pullResponse
	next, err := client.getJSONPage(ctx, endpoint, &responses)
	if err != nil {
		return PullRequestFacts{}, err
	}
	if next != nil || len(responses) > 1 {
		return PullRequestFacts{}, errors.New("Agent branch has ambiguous pull requests")
	}
	if len(responses) == 0 {
		return client.CreateDraftPullRequest(ctx, input)
	}
	response := responses[0]
	pull, err := validatePullResponse(response)
	if err != nil || pull.State != "open" || !pull.Draft ||
		pull.HeadRef != input.Head || pull.BaseRef != input.Base ||
		response.Title != input.Title || response.Body != input.Body {
		return PullRequestFacts{}, errors.New("existing Agent pull request is inconsistent")
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

// EnsurePullRequestReady converts a Draft or reuses an exact already-Ready PR
// left by an interrupted Publisher.
func (client *Client) EnsurePullRequestReady(
	ctx context.Context,
	number int64,
	expectedHeadSHA string,
) (PullRequestFacts, error) {
	current, err := client.PullRequest(ctx, number)
	if err != nil {
		return PullRequestFacts{}, err
	}
	if current.State != "open" || current.HeadSHA != expectedHeadSHA {
		return PullRequestFacts{}, errors.New("pull request Ready fence is stale")
	}
	if !current.Draft {
		return current, nil
	}
	ready, err := client.MarkPullRequestReady(ctx, number)
	if err != nil || ready.HeadSHA != expectedHeadSHA || ready.State != "open" {
		return PullRequestFacts{}, errors.New("pull request did not become exactly Ready")
	}
	return ready, nil
}

// EnsurePullRequestDraft converts exact adopted human work back to Draft so
// the Agent cannot expose it as review-ready before full revalidation.
func (client *Client) EnsurePullRequestDraft(
	ctx context.Context,
	number int64,
	expectedHeadSHA string,
) (PullRequestFacts, error) {
	current, err := client.PullRequest(ctx, number)
	if err != nil || current.State != "open" ||
		current.HeadSHA != expectedHeadSHA {
		return PullRequestFacts{}, errors.New("pull request draft fence is stale")
	}
	if current.Draft {
		return current, nil
	}
	parts := strings.Split(client.repository, "/")
	var lookup struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ID         string `json:"id"`
					IsDraft    bool   `json:"isDraft"`
					HeadRefOID string `json:"headRefOid"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := client.requestJSON(
		ctx, http.MethodPost, "/graphql",
		struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}{
			Query: `query($owner:String!,$name:String!,$number:Int!){repository(owner:$owner,name:$name){pullRequest(number:$number){id isDraft headRefOid}}}`,
			Variables: map[string]any{
				"owner": parts[0], "name": parts[1], "number": number,
			},
		},
		&lookup, http.StatusOK,
	); err != nil {
		return PullRequestFacts{}, err
	}
	pull := lookup.Data.Repository.PullRequest
	if len(lookup.Errors) != 0 || pull.ID == "" ||
		pull.HeadRefOID != expectedHeadSHA || pull.IsDraft {
		return PullRequestFacts{}, errors.New("pull request draft lookup is inconsistent")
	}
	var converted struct {
		Data struct {
			Convert struct {
				PullRequest struct {
					IsDraft    bool   `json:"isDraft"`
					HeadRefOID string `json:"headRefOid"`
				} `json:"pullRequest"`
			} `json:"convertPullRequestToDraft"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := client.requestJSON(
		ctx, http.MethodPost, "/graphql",
		struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}{
			Query:     `mutation($id:ID!){convertPullRequestToDraft(input:{pullRequestId:$id}){pullRequest{isDraft headRefOid}}}`,
			Variables: map[string]any{"id": pull.ID},
		},
		&converted, http.StatusOK,
	); err != nil {
		return PullRequestFacts{}, err
	}
	echo := converted.Data.Convert.PullRequest
	if len(converted.Errors) != 0 || !echo.IsDraft ||
		echo.HeadRefOID != expectedHeadSHA {
		return PullRequestFacts{}, errors.New("pull request did not become exact Draft")
	}
	result, err := client.PullRequest(ctx, number)
	if err != nil || !result.Draft || result.HeadSHA != expectedHeadSHA {
		return PullRequestFacts{}, errors.New("pull request Draft re-read is inconsistent")
	}
	return result, nil
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

// EnsureTrackingIssue makes a deterministic backport projection idempotent.
func (client *Client) EnsureTrackingIssue(
	ctx context.Context,
	title string,
	body string,
) (int64, error) {
	if strings.TrimSpace(title) == "" || len(title) > 256 ||
		strings.ContainsAny(title, "\"\r\n") || len(body) > 64<<10 {
		return 0, errors.New("tracking Issue identity is invalid")
	}
	endpoint := client.endpoint("/search/issues")
	query := endpoint.Query()
	query.Set(
		"q",
		"repo:"+client.repository+" is:issue in:title \""+title+"\"",
	)
	query.Set("per_page", "100")
	query.Set("page", "1")
	endpoint.RawQuery = query.Encode()
	var response struct {
		TotalCount int `json:"total_count"`
		Items      []struct {
			Number int64  `json:"number"`
			Title  string `json:"title"`
			Body   string `json:"body"`
		} `json:"items"`
	}
	next, err := client.getJSONPage(ctx, endpoint, &response)
	if err != nil {
		return 0, err
	}
	if next != nil || response.TotalCount > 100 || len(response.Items) > 100 {
		return 0, errors.New("tracking Issue search exceeds bound")
	}
	var found int64
	for _, item := range response.Items {
		if item.Title != title || item.Body != body {
			continue
		}
		if item.Number <= 0 || found != 0 {
			return 0, errors.New("tracking Issue identity is ambiguous")
		}
		found = item.Number
	}
	if found != 0 {
		return found, nil
	}
	return client.CreateTrackingIssue(ctx, title, body, []string{})
}

func validatePullResponse(response pullResponse) (PullRequestFacts, error) {
	if response.Number <= 0 ||
		(response.State != "open" && response.State != "closed") ||
		response.Base.Ref != "main" ||
		!agentRefPattern.MatchString(response.Head.Ref) ||
		!gitObjectPattern.MatchString(response.Base.SHA) ||
		!gitObjectPattern.MatchString(response.Head.SHA) ||
		response.MergeCommit != "" && !gitObjectPattern.MatchString(response.MergeCommit) ||
		response.Merged &&
			(response.State != "closed" || response.MergeCommit == "") {
		return PullRequestFacts{}, errors.New("GitHub pull request response is invalid")
	}
	return PullRequestFacts{
		Number: response.Number, State: response.State, Draft: response.Draft,
		Mergeable: response.Mergeable, Merged: response.Merged,
		BaseRef: response.Base.Ref,
		BaseSHA: response.Base.SHA, HeadRef: response.Head.Ref,
		HeadSHA: response.Head.SHA, MergeCommit: response.MergeCommit,
	}, nil
}
