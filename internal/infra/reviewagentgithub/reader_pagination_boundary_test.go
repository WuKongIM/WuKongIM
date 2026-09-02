package reviewagentgithub

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type reviewThreadNodeFixture struct {
	ID         string `json:"id"`
	IsResolved bool   `json:"isResolved"`
	Path       string `json:"path"`
	Line       int    `json:"line"`
}

func reviewThreadPageResponse(
	t *testing.T,
	total int,
	nodes []reviewThreadNodeFixture,
	hasNext bool,
	cursor string,
) *http.Response {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"data": map[string]any{
			"repository": map[string]any{
				"nameWithOwner": "WuKongIM/WuKongIM",
				"pullRequest": map[string]any{
					"number": 42,
					"reviewThreads": map[string]any{
						"totalCount": total,
						"nodes":      nodes,
						"pageInfo": map[string]any{
							"hasNextPage": hasNext,
							"endCursor":   cursor,
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal review thread page: %v", err)
	}
	return jsonResponse(http.StatusOK, string(body))
}

func TestReviewThreadPaginationCarriesOpaqueCursorAndStableCount(t *testing.T) {
	t.Parallel()

	page := 0
	client := newMemoryClient(t, 1<<20, func(request *http.Request) (*http.Response, error) {
		page++
		if request.Method != http.MethodPost || request.URL.Path != "/graphql" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		var input struct {
			Variables struct {
				Owner  string  `json:"owner"`
				Name   string  `json:"name"`
				Number int64   `json:"number"`
				Cursor *string `json:"cursor"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Fatalf("decode GraphQL request: %v", err)
		}
		if input.Variables.Owner != "WuKongIM" ||
			input.Variables.Name != "WuKongIM" ||
			input.Variables.Number != 42 {
			t.Fatalf("GraphQL variables = %+v", input.Variables)
		}
		if page == 1 {
			if input.Variables.Cursor != nil {
				t.Fatalf("first cursor = %v, want nil", *input.Variables.Cursor)
			}
			return reviewThreadPageResponse(t, 2, []reviewThreadNodeFixture{{
				ID: "thread-1", Path: "a.go", Line: 3,
			}}, true, "opaque-cursor-1"), nil
		}
		if input.Variables.Cursor == nil || *input.Variables.Cursor != "opaque-cursor-1" {
			t.Fatalf("second cursor = %v", input.Variables.Cursor)
		}
		return reviewThreadPageResponse(t, 2, []reviewThreadNodeFixture{{
			ID: "thread-2", IsResolved: true, Path: "b.go", Line: 8,
		}}, false, ""), nil
	})

	threads, err := client.readReviewThreads(context.Background(), 42)
	if err != nil {
		t.Fatalf("readReviewThreads() error = %v", err)
	}
	if page != 2 || len(threads) != 2 ||
		threads[0].ID != "thread-1" || threads[1].ID != "thread-2" ||
		!threads[1].IsResolved {
		t.Fatalf("readReviewThreads() pages = %d, threads = %+v", page, threads)
	}
}

func TestReviewThreadPaginationFailsClosedOnChangingOrIncompleteConnection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		maxPages int
		pages    func(*testing.T, int) *http.Response
		expected string
	}{
		{
			name: "count changes",
			pages: func(t *testing.T, page int) *http.Response {
				if page == 1 {
					return reviewThreadPageResponse(t, 2, []reviewThreadNodeFixture{{ID: "one", Path: "a.go", Line: 1}}, true, "next")
				}
				return reviewThreadPageResponse(t, 3, []reviewThreadNodeFixture{{ID: "two", Path: "b.go", Line: 2}}, false, "")
			},
			expected: "GitHub Review thread count changed during pagination",
		},
		{
			name: "missing cursor",
			pages: func(t *testing.T, _ int) *http.Response {
				return reviewThreadPageResponse(t, 1, []reviewThreadNodeFixture{{ID: "one", Path: "a.go", Line: 1}}, true, "")
			},
			expected: "GitHub Review thread cursor is invalid",
		},
		{
			name: "incomplete final page",
			pages: func(t *testing.T, _ int) *http.Response {
				return reviewThreadPageResponse(t, 2, []reviewThreadNodeFixture{{ID: "one", Path: "a.go", Line: 1}}, false, "")
			},
			expected: "GitHub Review thread pagination is incomplete",
		},
		{
			name: "invalid node",
			pages: func(t *testing.T, _ int) *http.Response {
				return reviewThreadPageResponse(t, 1, []reviewThreadNodeFixture{{ID: "", Path: "a.go", Line: 1}}, false, "")
			},
			expected: "GitHub Review thread response is invalid",
		},
		{
			name:     "page budget",
			maxPages: 1,
			pages: func(t *testing.T, _ int) *http.Response {
				return reviewThreadPageResponse(t, 2, []reviewThreadNodeFixture{{ID: "one", Path: "a.go", Line: 1}}, true, "next")
			},
			expected: "GitHub Review thread pagination exceeds page budget",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			page := 0
			client := newMemoryClient(t, 1<<20, func(*http.Request) (*http.Response, error) {
				page++
				return test.pages(t, page), nil
			})
			if test.maxPages > 0 {
				client.maxPages = test.maxPages
			}
			_, err := client.readReviewThreads(context.Background(), 42)
			if err == nil || err.Error() != test.expected {
				t.Fatalf("readReviewThreads() error = %v, want %q", err, test.expected)
			}
		})
	}
}

func TestLinkedIssueProjectionSkipsPullRequestsButRejectsIdentityDrift(t *testing.T) {
	t.Parallel()

	t.Run("skip pull request", func(t *testing.T) {
		t.Parallel()
		client := newMemoryClient(t, 1<<20, func(request *http.Request) (*http.Response, error) {
			if !strings.HasSuffix(request.URL.Path, "/issues/17") {
				t.Fatalf("path = %q", request.URL.Path)
			}
			return jsonResponse(http.StatusOK, `{"number":17,"state":"open","title":"linked PR","body":"body","pull_request":{}}`), nil
		})
		issues, err := client.readLinkedIssues(context.Background(), []int64{17})
		if err != nil || len(issues) != 0 {
			t.Fatalf("readLinkedIssues() = %+v, %v", issues, err)
		}
	})

	t.Run("reject identity drift", func(t *testing.T) {
		t.Parallel()
		client := newMemoryClient(t, 1<<20, func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"number":18,"state":"open","title":"wrong issue","body":"body","pull_request":null}`), nil
		})
		_, err := client.readLinkedIssues(context.Background(), []int64{17})
		if err == nil || err.Error() != "linked GitHub Issue response is invalid" {
			t.Fatalf("readLinkedIssues() error = %v", err)
		}
	})
}
