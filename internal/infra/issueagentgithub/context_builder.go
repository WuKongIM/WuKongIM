package issueagentgithub

import (
	"context"
	"errors"
	"slices"
	"sort"
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
	Repository         string
	IssueNumber        int64
	PullRequestNumber  int64
	StatusCommentID    int64
	Sequence           uint64
	Task               contract.TaskIdentity
	Authorization      contract.AuthorizationRecord
	RequiredTests      []string
	RiskCeiling        []string
	InstructionDigests []contract.FileDigest
	KnowledgePaths     []string
	OutputSchemaDigest string
	Limits             contract.EngineerLimits
	CreatedAt          time.Time
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
	instructionDigests := slices.Clone(request.InstructionDigests)
	sort.Slice(instructionDigests, func(left, right int) bool {
		return instructionDigests[left].Path < instructionDigests[right].Path
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
			Authorization:      request.Authorization,
			Labels:             labels,
			RequiredTests:      requiredTests,
			RiskCeiling:        riskCeiling,
			InstructionDigests: instructionDigests,
			KnowledgePaths:     knowledgePaths,
			OutputSchemaDigest: request.OutputSchemaDigest,
			Limits:             request.Limits,
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
									AuthorAssociation string   `json:"authorAssociation"`
									Author            struct {
										Login string `json:"login"`
									} `json:"author"`
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
	query := `query($owner:String!,$name:String!,$number:Int!){repository(owner:$owner,name:$name){pullRequest(number:$number){reviewThreads(first:100){nodes{id isResolved path line comments(first:100){nodes{databaseId body updatedAt authorAssociation author{login}} pageInfo{hasNextPage}}} pageInfo{hasNextPage}}}}}`
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
