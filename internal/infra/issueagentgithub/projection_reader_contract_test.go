package issueagentgithub_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	"github.com/stretchr/testify/require"
)

func TestPullRequestProjectionLifecycleIsIdempotentAndHeadFenced(t *testing.T) {
	t.Parallel()

	const number int64 = 9
	headSHA := fortyHex("b")
	var mu sync.Mutex
	state := "open"
	draft := true
	readyWrites := 0
	draftWrites := 0
	closeWrites := 0
	updateWrites := 0

	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		writePull := func() {
			writeJSON(t, writer, map[string]any{
				"number": number, "state": state, "draft": draft,
				"mergeable": true, "merged": false,
				"base": map[string]any{"ref": "main", "sha": fortyHex("a")},
				"head": map[string]any{"ref": "agent/issue-42", "sha": headSHA},
			})
		}
		switch {
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/pulls/9":
			writePull()
		case request.Method == http.MethodPost &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/pulls/9/ready_for_review":
			readyWrites++
			draft = false
			writePull()
		case request.Method == http.MethodPatch &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/pulls/9":
			var input map[string]string
			require.NoError(t, json.NewDecoder(request.Body).Decode(&input))
			if _, updatingTitle := input["title"]; updatingTitle {
				updateWrites++
				require.Equal(t, map[string]string{
					"title": "fix(agent): issue #42",
					"body":  "verified summary",
					"state": "open",
				}, input)
			} else {
				closeWrites++
				require.Equal(t, map[string]string{"state": "closed"}, input)
				state = "closed"
			}
			writePull()
		case request.Method == http.MethodPost && request.URL.Path == "/graphql":
			var input struct {
				Query string `json:"query"`
			}
			require.NoError(t, json.NewDecoder(request.Body).Decode(&input))
			if strings.Contains(input.Query, "convertPullRequestToDraft") {
				draftWrites++
				draft = true
				writeJSON(t, writer, map[string]any{
					"data": map[string]any{
						"convertPullRequestToDraft": map[string]any{
							"pullRequest": map[string]any{
								"isDraft": true, "headRefOid": headSHA,
							},
						},
					},
				})
				return
			}
			writeJSON(t, writer, map[string]any{
				"data": map[string]any{
					"repository": map[string]any{
						"pullRequest": map[string]any{
							"id": "PR_node_9", "isDraft": false,
							"headRefOid": headSHA,
						},
					},
				},
			})
		default:
			http.NotFound(writer, request)
		}
	})
	client := newIssueMemoryClient(t, handler)

	_, err := client.EnsurePullRequestReady(context.Background(), number, fortyHex("c"))
	require.ErrorContains(t, err, "fence is stale")
	require.Equal(t, 0, readyWrites)

	ready, err := client.EnsurePullRequestReady(context.Background(), number, headSHA)
	require.NoError(t, err)
	require.False(t, ready.Draft)
	ready, err = client.EnsurePullRequestReady(context.Background(), number, headSHA)
	require.NoError(t, err)
	require.False(t, ready.Draft)
	require.Equal(t, 1, readyWrites, "an interrupted retry must reuse the Ready projection")

	updated, err := client.UpdatePullRequest(
		context.Background(), number,
		"fix(agent): issue #42", "verified summary", "open",
	)
	require.NoError(t, err)
	require.Equal(t, headSHA, updated.HeadSHA)
	require.Equal(t, 1, updateWrites)

	reverted, err := client.EnsurePullRequestDraft(context.Background(), number, headSHA)
	require.NoError(t, err)
	require.True(t, reverted.Draft)
	reverted, err = client.EnsurePullRequestDraft(context.Background(), number, headSHA)
	require.NoError(t, err)
	require.True(t, reverted.Draft)
	require.Equal(t, 1, draftWrites, "an interrupted retry must reuse the Draft projection")

	_, err = client.EnsurePullRequestClosed(context.Background(), number, fortyHex("c"))
	require.ErrorContains(t, err, "fence is stale")
	require.Equal(t, 0, closeWrites)
	closed, err := client.EnsurePullRequestClosed(context.Background(), number, headSHA)
	require.NoError(t, err)
	require.Equal(t, "closed", closed.State)
	closed, err = client.EnsurePullRequestClosed(context.Background(), number, headSHA)
	require.NoError(t, err)
	require.Equal(t, "closed", closed.State)
	require.Equal(t, 1, closeWrites, "an interrupted retry must not close an already-closed PR twice")
}

func TestTrackingIssueProjectionRecoversCreateWithoutDuplication(t *testing.T) {
	t.Parallel()

	const title = "Backport issue #42 to v3.1"
	const body = "Tracks the exact accepted candidate."
	var mu sync.Mutex
	created := false
	createCalls := 0

	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/search/issues":
			require.Equal(t, `repo:WuKongIM/WuKongIM is:issue in:title "`+title+`"`, request.URL.Query().Get("q"))
			items := []map[string]any{}
			if created {
				items = append(items, map[string]any{
					"number": 84, "title": title, "body": body,
				})
			}
			writeJSON(t, writer, map[string]any{
				"total_count": len(items), "items": items,
			})
		case request.Method == http.MethodPost &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/issues":
			createCalls++
			created = true
			var input struct {
				Title  string   `json:"title"`
				Body   string   `json:"body"`
				Labels []string `json:"labels"`
			}
			require.NoError(t, json.NewDecoder(request.Body).Decode(&input))
			require.Equal(t, title, input.Title)
			require.Equal(t, body, input.Body)
			require.Empty(t, input.Labels)
			writer.WriteHeader(http.StatusCreated)
			writeJSON(t, writer, map[string]any{
				"number": 84, "title": title, "body": body,
			})
		default:
			http.NotFound(writer, request)
		}
	})
	client := newIssueMemoryClient(t, handler)

	first, err := client.EnsureTrackingIssue(context.Background(), title, body)
	require.NoError(t, err)
	require.Equal(t, int64(84), first)
	second, err := client.EnsureTrackingIssue(context.Background(), title, body)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, 1, createCalls, "search recovery must prevent duplicate tracking Issues")
}

func TestRepositoryContextAdaptersPreserveExactGitHubIdentity(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/git/ref/heads/main":
			writeJSON(t, writer, map[string]any{
				"ref":    "refs/heads/main",
				"object": map[string]any{"type": "commit", "sha": fortyHex("a")},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/issues/comments/51":
			writeJSON(t, writer, map[string]any{
				"id":                 51,
				"issue_url":          serverURL(request) + "/repos/WuKongIM/WuKongIM/issues/42",
				"user":               map[string]any{"login": "maintainer", "type": "User"},
				"author_association": "MEMBER", "body": "approved context",
				"created_at": "2026-07-28T12:00:00Z",
				"updated_at": "2026-07-28T12:01:00Z",
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/issues/42":
			writeJSON(t, writer, map[string]any{
				"node_id": "I_node_42", "number": 42, "state": "open",
				"title": "message loss", "body": "reproduction",
				"updated_at":         "2026-07-28T12:02:00Z",
				"user":               map[string]any{"login": "reporter"},
				"author_association": "CONTRIBUTOR",
				"labels":             []map[string]any{{"name": "ready-for-agent"}, {"name": "bug"}},
			})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/issues/42/comments":
			require.Equal(t, "1", request.URL.Query().Get("page"))
			writeJSON(t, writer, []map[string]any{{
				"id":                 51,
				"user":               map[string]any{"login": "maintainer", "type": "User"},
				"author_association": "MEMBER", "body": "approved context",
				"created_at": "2026-07-28T12:00:00Z",
				"updated_at": "2026-07-28T12:01:00Z",
			}})
		case request.Method == http.MethodGet &&
			request.URL.Path == "/repos/WuKongIM/WuKongIM/collaborators/maintainer/permission":
			writeJSON(t, writer, map[string]any{
				"permission": "maintain", "user": map[string]any{"login": "maintainer"},
			})
		case request.Method == http.MethodPost && request.URL.Path == "/graphql":
			writeJSON(t, writer, map[string]any{
				"data": map[string]any{
					"repository": map[string]any{
						"pullRequest": map[string]any{
							"reviewThreads": map[string]any{
								"nodes": []map[string]any{{
									"id": "thread-2", "isResolved": false,
									"path": "pkg/db/message.go", "line": 17,
									"comments": map[string]any{
										"nodes": []map[string]any{{
											"databaseId": 61, "body": "retain the fence",
											"updatedAt": "2026-07-28T12:03:00Z", "outdated": false,
											"authorAssociation": "MEMBER",
											"author":            map[string]any{"login": "reviewer"},
											"pullRequestReview": map[string]any{
												"databaseId": 70,
												"commit":     map[string]any{"oid": fortyHex("b")},
											},
										}},
										"pageInfo": map[string]any{"hasNextPage": false},
									},
								}},
								"pageInfo": map[string]any{"hasNextPage": false},
							},
						},
					},
				},
			})
		default:
			http.NotFound(writer, request)
		}
	})
	client := newIssueMemoryClient(t, handler)

	main, err := client.DefaultBranchHead(context.Background(), "main")
	require.NoError(t, err)
	require.Equal(t, fortyHex("a"), main.SHA)
	comment, err := client.IssueComment(context.Background(), 51, 42)
	require.NoError(t, err)
	require.Equal(t, "MEMBER", comment.AuthorAssociation)

	issue, err := client.ReadContextIssue(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, "I_node_42", issue.ID)
	require.Equal(t, []string{"bug", "ready-for-agent"}, issue.Labels)
	comments, err := client.ReadContextComments(context.Background(), 42)
	require.NoError(t, err)
	require.Len(t, comments, 1)
	require.Equal(t, int64(51), comments[0].ID)
	permission, err := client.ReadActorPermission(context.Background(), "maintainer")
	require.NoError(t, err)
	require.Equal(t, issueagentgithub.PermissionMaintain, permission)
	threads, err := client.ReadContextReviewThreads(context.Background(), 9)
	require.NoError(t, err)
	require.Len(t, threads, 1)
	require.Equal(t, "thread-2", threads[0].ID)
	require.Equal(t, "retain the fence", threads[0].Comments[0].Body)
}

func TestRepositoryReadersRejectCrossObjectIdentityEchoes(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/WuKongIM/WuKongIM/git/ref/heads/main":
			writeJSON(t, writer, map[string]any{
				"ref":    "refs/heads/release",
				"object": map[string]any{"type": "commit", "sha": fortyHex("a")},
			})
		case "/repos/WuKongIM/WuKongIM/issues/comments/51":
			writeJSON(t, writer, map[string]any{
				"id":                 51,
				"issue_url":          serverURL(request) + "/repos/WuKongIM/WuKongIM/issues/43",
				"user":               map[string]any{"login": "maintainer", "type": "User"},
				"author_association": "MEMBER", "body": "wrong Issue",
				"created_at": "2026-07-28T12:00:00Z",
				"updated_at": "2026-07-28T12:01:00Z",
			})
		default:
			http.NotFound(writer, request)
		}
	})
	client := newIssueMemoryClient(t, handler)

	_, err := client.DefaultBranchHead(context.Background(), "main")
	require.ErrorContains(t, err, "main ref response is invalid")
	_, err = client.IssueComment(context.Background(), 51, 42)
	require.ErrorContains(t, err, "comment response is invalid")
	_, err = client.DefaultBranchHead(context.Background(), "release")
	require.ErrorContains(t, err, "baseline must be main")
}

func serverURL(request *http.Request) string {
	return "http://" + request.Host
}
