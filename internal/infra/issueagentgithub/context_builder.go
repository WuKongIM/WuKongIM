package issueagentgithub

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

// ContextIssue is the bounded Issue input needed by the Context Builder.
type ContextIssue struct {
	ID                string
	Number            int64
	Title             string
	Body              string
	Author            string
	AuthorAssociation string
	UpdatedAt         time.Time
	Labels            []string
}

// ContextSource is the authenticated read-only GitHub boundary.
type ContextSource interface {
	ReadContextIssue(context.Context, int64) (ContextIssue, error)
	ReadContextComments(context.Context, int64) ([]contract.CommentSnapshot, error)
	ReadContextReviewThreads(
		context.Context,
		int64,
	) ([]contract.ReviewThreadSnapshot, error)
	ReadActorPermission(context.Context, string) (Permission, error)
}

// BuildContextRequest contains protected task inputs, never GitHub credentials.
type BuildContextRequest struct {
	Repository             string
	IssueNumber            int64
	PullRequestNumber      int64
	StatusCommentID        int64
	Sequence               uint64
	Task                   contract.TaskIdentity
	Authorization          contract.AuthorizationRecord
	RequiredTests          []string
	RiskCeiling            []string
	ContextDocumentDigests []contract.FileDigest
	KnowledgePaths         []string
	OutputSchemaDigest     string
	Limits                 contract.EngineerLimits
	CreatedAt              time.Time
}

// ContextBuilder reads current GitHub context and emits one credential-free bundle.
type ContextBuilder struct {
	source ContextSource
}

// NewContextBuilder constructs the bounded GitHub Context Builder.
func NewContextBuilder(source ContextSource) (*ContextBuilder, error) {
	if source == nil {
		return nil, errors.New("Context Builder source is required")
	}
	return &ContextBuilder{source: source}, nil
}

// Build creates one canonical Context Bundle from fresh authenticated reads.
func (builder *ContextBuilder) Build(
	ctx context.Context,
	request BuildContextRequest,
) (contract.ContextBundle, error) {
	if builder == nil || builder.source == nil || ctx == nil {
		return contract.ContextBundle{}, errors.New("Context Builder request is invalid")
	}
	issue, err := builder.source.ReadContextIssue(ctx, request.IssueNumber)
	if err != nil {
		return contract.ContextBundle{}, err
	}
	comments, err := builder.source.ReadContextComments(ctx, request.IssueNumber)
	if err != nil {
		return contract.ContextBundle{}, err
	}
	if request.StatusCommentID > 0 {
		comments = slices.DeleteFunc(comments, func(
			comment contract.CommentSnapshot,
		) bool {
			return comment.ID == request.StatusCommentID
		})
	}
	reviewThreads := []contract.ReviewThreadSnapshot{}
	if request.Task.Kind == contract.TaskKindReview {
		if request.PullRequestNumber <= 0 {
			return contract.ContextBundle{}, errors.New("Review context lacks pull request identity")
		}
		reviewThreads, err = builder.source.ReadContextReviewThreads(
			ctx, request.PullRequestNumber,
		)
		if err != nil {
			return contract.ContextBundle{}, err
		}
	}
	permission, err := builder.source.ReadActorPermission(
		ctx, request.Authorization.Actor,
	)
	if err != nil {
		return contract.ContextBundle{}, err
	}
	if string(permission) != request.Authorization.Permission {
		return contract.ContextBundle{}, errors.New("authorization permission changed")
	}

	labels := slices.Clone(issue.Labels)
	slices.Sort(labels)
	labels = slices.Compact(labels)
	requiredTests := sortedUniqueCopy(request.RequiredTests)
	riskCeiling := sortedUniqueCopy(request.RiskCeiling)
	knowledgePaths := sortedUniqueCopy(request.KnowledgePaths)
	contextDocumentDigests := slices.Clone(request.ContextDocumentDigests)
	sort.Slice(contextDocumentDigests, func(left, right int) bool {
		return contextDocumentDigests[left].Path < contextDocumentDigests[right].Path
	})
	comments = slices.Clone(comments)
	sort.Slice(comments, func(left, right int) bool {
		return comments[left].ID < comments[right].ID
	})
	reviewThreads = slices.Clone(reviewThreads)
	sort.Slice(reviewThreads, func(left, right int) bool {
		return reviewThreads[left].ID < reviewThreads[right].ID
	})

	bundle := contract.ContextBundle{
		SchemaVersion: 2,
		Repository:    request.Repository,
		IssueNumber:   request.IssueNumber,
		Sequence:      request.Sequence,
		Task:          request.Task,
		Trusted: contract.TrustedContext{
			Authorization:          request.Authorization,
			Labels:                 labels,
			RequiredTests:          requiredTests,
			RiskCeiling:            riskCeiling,
			ContextDocumentDigests: contextDocumentDigests,
			KnowledgePaths:         knowledgePaths,
			OutputSchemaDigest:     request.OutputSchemaDigest,
			Limits:                 request.Limits,
		},
		Untrusted: contract.UntrustedContext{
			Issue: contract.IssueSnapshot{
				ID: issue.ID, Number: issue.Number, Title: issue.Title,
				Body: issue.Body, Author: issue.Author,
				AuthorAssociation: issue.AuthorAssociation,
				UpdatedAt:         issue.UpdatedAt,
			},
			Comments:      comments,
			ReviewThreads: reviewThreads,
		},
		CreatedAt: request.CreatedAt,
	}
	if err := contract.ValidateContextBundle(bundle); err != nil {
		return contract.ContextBundle{}, err
	}
	return bundle, nil
}

func sortedUniqueCopy(values []string) []string {
	result := slices.Clone(values)
	slices.Sort(result)
	return slices.Compact(result)
}

// ReadContextIssue adapts the repository Client to the ContextSource boundary.
func (client *Client) ReadContextIssue(
	ctx context.Context,
	issueNumber int64,
) (ContextIssue, error) {
	issue, err := client.Issue(ctx, issueNumber)
	if err != nil {
		return ContextIssue{}, err
	}
	if issue.ID == "" || issue.UpdatedAt.IsZero() {
		return ContextIssue{}, errors.New("GitHub Issue context identity is incomplete")
	}
	return ContextIssue{
		ID: issue.ID, Number: issue.Number, Title: issue.Title, Body: issue.Body,
		Author: issue.Author, AuthorAssociation: issue.AuthorAssociation,
		UpdatedAt: issue.UpdatedAt, Labels: slices.Clone(issue.Labels),
	}, nil
}

// ReadContextComments adapts complete Issue comment pages to untrusted context.
func (client *Client) ReadContextComments(
	ctx context.Context,
	issueNumber int64,
) ([]contract.CommentSnapshot, error) {
	comments, err := client.ListIssueComments(ctx, issueNumber)
	if err != nil {
		return nil, err
	}
	result := make([]contract.CommentSnapshot, 0, len(comments))
	for _, comment := range comments {
		if comment.AuthorAssociation == "" {
			return nil, errors.New("GitHub comment context lacks author association")
		}
		result = append(result, contract.CommentSnapshot{
			ID: comment.ID, Author: comment.Author,
			AuthorAssociation: comment.AuthorAssociation,
			Body:              comment.Body, UpdatedAt: comment.UpdatedAt,
		})
	}
	return result, nil
}

// ReadActorPermission re-reads current repository authority for ContextSource.
func (client *Client) ReadActorPermission(
	ctx context.Context,
	actor string,
) (Permission, error) {
	return client.ActorPermission(ctx, actor)
}

// ReadContextReviewThreads reads the complete bounded unresolved thread set.
func (client *Client) ReadContextReviewThreads(
	ctx context.Context,
	pullRequestNumber int64,
) ([]contract.ReviewThreadSnapshot, error) {
	return client.readContextReviewThreads(ctx, pullRequestNumber, 0, "")
}

func (client *Client) readContextReviewThreads(
	ctx context.Context,
	pullRequestNumber int64,
	reviewID int64,
	headSHA string,
) ([]contract.ReviewThreadSnapshot, error) {
	if client == nil || pullRequestNumber <= 0 {
		return nil, errors.New("Review context request is invalid")
	}
	parts := strings.Split(client.repository, "/")
	var response struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						Nodes []struct {
							ID         string `json:"id"`
							IsResolved bool   `json:"isResolved"`
							Path       string `json:"path"`
							Line       int64  `json:"line"`
							Comments   struct {
								Nodes []struct {
									DatabaseID        int64    `json:"databaseId"`
									Body              string   `json:"body"`
									UpdatedAt         jsonTime `json:"updatedAt"`
									Outdated          bool     `json:"outdated"`
									AuthorAssociation string   `json:"authorAssociation"`
									Author            struct {
										Login string `json:"login"`
									} `json:"author"`
									PullRequestReview struct {
										DatabaseID int64 `json:"databaseId"`
										Commit     struct {
											OID string `json:"oid"`
										} `json:"commit"`
									} `json:"pullRequestReview"`
								} `json:"nodes"`
								PageInfo struct {
									HasNextPage bool `json:"hasNextPage"`
								} `json:"pageInfo"`
							} `json:"comments"`
						} `json:"nodes"`
						PageInfo struct {
							HasNextPage bool `json:"hasNextPage"`
						} `json:"pageInfo"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	query := `query($owner:String!,$name:String!,$number:Int!){repository(owner:$owner,name:$name){pullRequest(number:$number){reviewThreads(first:100){nodes{id isResolved path line comments(first:100){nodes{databaseId body updatedAt outdated authorAssociation author{login} pullRequestReview{databaseId commit{oid}}} pageInfo{hasNextPage}}} pageInfo{hasNextPage}}}}}`
	if err := client.requestJSON(
		ctx, "POST", "/graphql",
		struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}{
			Query: query,
			Variables: map[string]any{
				"owner": parts[0], "name": parts[1],
				"number": pullRequestNumber,
			},
		},
		&response, 200,
	); err != nil {
		return nil, err
	}
	threads := response.Data.Repository.PullRequest.ReviewThreads
	if len(response.Errors) != 0 || threads.PageInfo.HasNextPage ||
		len(threads.Nodes) > 100 {
		return nil, errors.New("Review context response is incomplete")
	}
	result := make([]contract.ReviewThreadSnapshot, 0, len(threads.Nodes))
	for _, thread := range threads.Nodes {
		if thread.IsResolved {
			continue
		}
		if thread.Comments.PageInfo.HasNextPage ||
			len(thread.Comments.Nodes) == 0 ||
			len(thread.Comments.Nodes) > 100 {
			return nil, errors.New("Review comment context is incomplete")
		}
		first := thread.Comments.Nodes[0]
		if reviewID != 0 &&
			(first.Outdated ||
				first.PullRequestReview.DatabaseID != reviewID ||
				first.PullRequestReview.Commit.OID != headSHA) {
			continue
		}
		comments := make([]contract.CommentSnapshot, 0, len(thread.Comments.Nodes))
		for _, comment := range thread.Comments.Nodes {
			comments = append(comments, contract.CommentSnapshot{
				ID: comment.DatabaseID, Author: comment.Author.Login,
				AuthorAssociation: comment.AuthorAssociation,
				Body:              comment.Body, UpdatedAt: comment.UpdatedAt.Time,
			})
		}
		sort.Slice(comments, func(left, right int) bool {
			return comments[left].ID < comments[right].ID
		})
		result = append(result, contract.ReviewThreadSnapshot{
			ID: thread.ID, Path: thread.Path, Line: thread.Line,
			Comments: comments,
		})
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].ID < result[right].ID
	})
	return result, nil
}

// ReadReviewAgentFindings returns only a fresh current-head REQUEST_CHANGES
// Review and its unresolved threads from the configured Review Agent App.
func (client *Client) ReadReviewAgentFindings(
	ctx context.Context,
	pullRequestNumber int64,
	headSHA string,
	appLogin string,
) ([]contract.ReviewThreadSnapshot, bool, error) {
	if client == nil || pullRequestNumber <= 0 ||
		!gitObjectPattern.MatchString(headSHA) ||
		len(headSHA) != 40 ||
		!appBotLoginPattern.MatchString(appLogin) {
		return nil, false, errors.New(
			"Review Agent finding request is invalid",
		)
	}
	review, found, err := client.latestReviewAgentReview(
		ctx,
		pullRequestNumber,
		headSHA,
		appLogin,
	)
	if err != nil || !found || review.State != "CHANGES_REQUESTED" {
		return nil, false, err
	}
	threads, err := client.readContextReviewThreads(
		ctx,
		pullRequestNumber,
		review.ID,
		headSHA,
	)
	if err != nil {
		return nil, false, err
	}
	filtered := make([]contract.ReviewThreadSnapshot, 0, len(threads)+1)
	if review.Body != "" {
		filtered = append(filtered, contract.ReviewThreadSnapshot{
			ID: "review-agent-formal-" + strconv.FormatInt(review.ID, 10),
			Comments: []contract.CommentSnapshot{{
				ID: review.ID, Author: appLogin,
				AuthorAssociation: "NONE",
				Body:              review.Body,
				UpdatedAt:         review.SubmittedAt,
			}},
		})
	}
	for _, thread := range threads {
		if len(thread.Comments) == 0 ||
			thread.Comments[0].Author != appLogin {
			continue
		}
		filtered = append(filtered, thread)
	}
	sort.Slice(filtered, func(left, right int) bool {
		return filtered[left].ID < filtered[right].ID
	})
	if len(filtered) == 0 {
		return nil, false, errors.New(
			"Review Agent change request has no bounded findings",
		)
	}
	return filtered, true, nil
}

type reviewAgentFormalReview struct {
	ID          int64
	State       string
	Body        string
	SubmittedAt time.Time
}

func (client *Client) latestReviewAgentReview(
	ctx context.Context,
	pullRequestNumber int64,
	headSHA string,
	appLogin string,
) (reviewAgentFormalReview, bool, error) {
	var latest reviewAgentFormalReview
	found := false
	for page := 1; page <= client.maxPages; page++ {
		endpoint := client.endpoint(
			"/repos/" + client.repository + "/pulls/" +
				strconv.FormatInt(pullRequestNumber, 10) + "/reviews",
		)
		query := endpoint.Query()
		query.Set("per_page", "100")
		query.Set("page", strconv.Itoa(page))
		endpoint.RawQuery = query.Encode()
		var payload []struct {
			ID       int64  `json:"id"`
			State    string `json:"state"`
			Body     string `json:"body"`
			CommitID string `json:"commit_id"`
			User     struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			} `json:"user"`
			SubmittedAt *jsonTime `json:"submitted_at"`
		}
		next, err := client.getJSONPage(ctx, endpoint, &payload)
		if err != nil {
			return reviewAgentFormalReview{}, false, err
		}
		if len(payload) > 100 {
			return reviewAgentFormalReview{}, false, errors.New(
				"Review Agent Review page exceeds item limit",
			)
		}
		for _, item := range payload {
			if item.User.Login != appLogin ||
				item.User.Type != "Bot" ||
				item.CommitID != headSHA {
				continue
			}
			if item.ID <= 0 || item.SubmittedAt == nil ||
				item.SubmittedAt.Time.IsZero() ||
				len(item.Body) > 64<<10 {
				return reviewAgentFormalReview{}, false, errors.New(
					"Review Agent formal Review is invalid",
				)
			}
			if !found ||
				item.SubmittedAt.Time.After(latest.SubmittedAt) ||
				item.SubmittedAt.Time.Equal(latest.SubmittedAt) &&
					item.ID > latest.ID {
				latest = reviewAgentFormalReview{
					ID: item.ID, State: item.State, Body: item.Body,
					SubmittedAt: item.SubmittedAt.Time,
				}
				found = true
			}
		}
		if next == nil {
			return latest, found, nil
		}
		if page == client.maxPages ||
			next.Scheme != client.baseURL.Scheme ||
			next.Host != client.baseURL.Host ||
			next.Path != endpoint.Path ||
			next.Query().Get("per_page") != "100" ||
			next.Query().Get("page") != strconv.Itoa(page+1) ||
			len(next.Query()) != 2 {
			return reviewAgentFormalReview{}, false, errors.New(
				"Review Agent Review pagination is incomplete",
			)
		}
	}
	return reviewAgentFormalReview{}, false, errors.New(
		"Review Agent Review pagination did not terminate",
	)
}
