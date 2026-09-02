package issueagentgithub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestIssueCommentPaginationRejectsEveryIncompleteInventoryShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		linkForPage   func(page int) string
		wantError     string
		responseBody  string
		maxBodyBytes  int64
		validateCalls func(*testing.T, int)
	}{
		{
			name: "cross host next page",
			linkForPage: func(int) string {
				return `<https://attacker.example/repos/WuKongIM/WuKongIM/issues/42/comments?per_page=100&page=2>; rel="next"`
			},
			wantError:    "outside request scope",
			responseBody: `[]`,
		},
		{
			name: "skipped next page",
			linkForPage: func(int) string {
				return `<https://api.example.test/v3/repos/WuKongIM/WuKongIM/issues/42/comments?per_page=100&page=3>; rel="next"`
			},
			wantError:    "outside request scope",
			responseBody: `[]`,
		},
		{
			name: "extra query widens scope",
			linkForPage: func(int) string {
				return `<https://api.example.test/v3/repos/WuKongIM/WuKongIM/issues/42/comments?per_page=100&page=2&since=now>; rel="next"`
			},
			wantError:    "outside request scope",
			responseBody: `[]`,
		},
		{
			name: "page budget exhausted",
			linkForPage: func(page int) string {
				return `<https://api.example.test/v3/repos/WuKongIM/WuKongIM/issues/42/comments?per_page=100&page=` +
					strconv.Itoa(page+1) + `>; rel="next"`
			},
			wantError:    "pagination exceeds page budget",
			responseBody: `[]`,
			validateCalls: func(t *testing.T, calls int) {
				t.Helper()
				if calls != 2 {
					t.Fatalf("calls = %d, want 2", calls)
				}
			},
		},
		{
			name:         "invalid comment identity",
			linkForPage:  func(int) string { return "" },
			wantError:    "comment response is invalid",
			responseBody: `[{"id":0,"user":{"login":"author","type":"User"},"body":"body","created_at":"2026-08-01T00:00:00Z","updated_at":"2026-08-01T00:00:00Z"}]`,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			client := newBoundaryClient(t, 1<<20, func(request *http.Request) (*http.Response, error) {
				calls++
				if request.Method != http.MethodGet ||
					request.URL.Path != "/v3/repos/WuKongIM/WuKongIM/issues/42/comments" ||
					request.URL.Query().Get("per_page") != "100" ||
					request.URL.Query().Get("page") != strconv.Itoa(calls) {
					t.Fatalf("unexpected pagination request: %s %s", request.Method, request.URL)
				}
				response := boundaryJSONResponse(http.StatusOK, test.responseBody)
				response.Header.Set("Link", test.linkForPage(calls))
				return response, nil
			})
			_, err := client.ListIssueComments(context.Background(), 42)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want substring %q", err, test.wantError)
			}
			if test.validateCalls != nil {
				test.validateCalls(t, calls)
			}
		})
	}

	if _, err := (*Client)(nil).ListIssueComments(context.Background(), 42); err == nil {
		t.Fatal("nil client was accepted")
	}
	client := newBoundaryClient(t, 4096, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid issue number must not reach transport")
		return nil, nil
	})
	if _, err := client.ListIssueComments(context.Background(), 0); err == nil {
		t.Fatal("invalid issue number was accepted")
	}
}

func TestIssueInventoryRejectsAmbiguousIdentityAndPagination(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		body      string
		link      string
		wantError string
	}{
		{
			name:      "duplicate issue",
			body:      `[{"number":2},{"number":2}]`,
			wantError: "contains a duplicate",
		},
		{
			name:      "invalid issue identity",
			body:      `[{"number":0}]`,
			wantError: "identity is invalid",
		},
		{
			name:      "unexpected next page",
			body:      `[{"number":2}]`,
			link:      `<https://api.example.test/v3/repos/WuKongIM/WuKongIM/issues?labels=ready-for-agent&page=2&per_page=41&state=open>; rel="next"`,
			wantError: "exceeds the global accounting bound",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := newBoundaryClient(t, 4096, func(request *http.Request) (*http.Response, error) {
				if request.URL.Query().Get("state") != "open" ||
					request.URL.Query().Get("labels") != "ready-for-agent" ||
					request.URL.Query().Get("page") != "1" ||
					request.URL.Query().Get("per_page") != "41" {
					t.Fatalf("unexpected inventory query: %s", request.URL.RawQuery)
				}
				response := boundaryJSONResponse(http.StatusOK, test.body)
				response.Header.Set("Link", test.link)
				return response, nil
			})
			_, err := client.ListOpenIssueNumbersByLabel(
				context.Background(), "ready-for-agent",
			)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want substring %q", err, test.wantError)
			}
		})
	}

	client := newBoundaryClient(t, 4096, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid label must not reach transport")
		return nil, nil
	})
	for _, label := range []string{"", "ready,other", "ready\nother", strings.Repeat("x", 101)} {
		if _, err := client.ListOpenIssueNumbersByLabel(context.Background(), label); err == nil {
			t.Fatalf("invalid label %q was accepted", label)
		}
	}
}

func TestUpdateRefsCASCarriesExactExpectedOldOIDs(t *testing.T) {
	t.Parallel()

	oldHead := strings.Repeat("a", 40)
	newHead := strings.Repeat("b", 40)
	mainHead := strings.Repeat("c", 40)
	stageHead := strings.Repeat("d", 40)
	stageBranch := "agent/issue-42-rebase-" + strings.Repeat("e", 64)
	updates := []refUpdate{
		{
			Name: "refs/heads/agent/issue-42", BeforeOID: oldHead,
			AfterOID: newHead, Force: true,
		},
		{
			Name: "refs/heads/" + stageBranch, BeforeOID: stageHead,
			AfterOID: zeroGitOID, Force: true,
		},
		{
			Name: "refs/heads/main", BeforeOID: mainHead,
			AfterOID: mainHead, Force: false,
		},
	}
	client := newBoundaryClient(t, 1<<20, func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != "/v3/graphql" {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL)
		}
		var envelope struct {
			Query     string `json:"query"`
			Variables struct {
				Input struct {
					RepositoryID string      `json:"repositoryId"`
					RefUpdates   []refUpdate `json:"refUpdates"`
				} `json:"input"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(request.Body).Decode(&envelope); err != nil {
			t.Fatalf("decode CAS request: %v", err)
		}
		if !strings.Contains(envelope.Query, "updateRefs") {
			t.Fatalf("query = %q", envelope.Query)
		}
		if envelope.Variables.Input.RepositoryID != "R_repository" {
			t.Fatalf("repository ID = %q", envelope.Variables.Input.RepositoryID)
		}
		if len(envelope.Variables.Input.RefUpdates) != len(updates) {
			t.Fatalf("updates = %#v", envelope.Variables.Input.RefUpdates)
		}
		for index := range updates {
			if envelope.Variables.Input.RefUpdates[index] != updates[index] {
				t.Fatalf("update[%d] = %#v, want %#v", index,
					envelope.Variables.Input.RefUpdates[index], updates[index])
			}
		}
		return boundaryJSONResponse(
			http.StatusOK,
			`{"data":{"updateRefs":{}},"errors":[]}`,
		), nil
	})
	if err := client.updateRefsCAS(context.Background(), "R_repository", updates); err != nil {
		t.Fatalf("updateRefsCAS() error = %v", err)
	}
}

func TestUpdateRefsCASRejectsInvalidOrAmbiguousMutation(t *testing.T) {
	t.Parallel()

	sha := strings.Repeat("a", 40)
	valid := refUpdate{
		Name: "refs/heads/agent/issue-42", BeforeOID: sha,
		AfterOID: strings.Repeat("b", 40), Force: true,
	}
	client := newBoundaryClient(t, 4096, func(*http.Request) (*http.Response, error) {
		return boundaryJSONResponse(
			http.StatusOK,
			`{"data":{"updateRefs":null},"errors":[{"message":"expected head changed: installation-secret-token"}]}`,
		), nil
	})
	if err := client.updateRefsCAS(context.Background(), "R_repository", []refUpdate{valid}); err == nil || err.Error() != "GitHub atomic ref update failed" {
		t.Fatalf("GraphQL rejection error = %v", err)
	}

	noTransport := newBoundaryClient(t, 4096, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid CAS input must not reach transport")
		return nil, nil
	})
	tests := []struct {
		name       string
		repository string
		updates    []refUpdate
	}{
		{name: "missing repository", updates: []refUpdate{valid}},
		{name: "missing updates", repository: "R_repository"},
		{
			name: "too many updates", repository: "R_repository",
			updates: []refUpdate{valid, valid, valid, valid},
		},
		{
			name: "malformed ref", repository: "R_repository",
			updates: []refUpdate{{Name: "refs/tags/v1", BeforeOID: sha, AfterOID: sha}},
		},
		{
			name: "three updates without main fence", repository: "R_repository",
			updates: []refUpdate{valid, valid, valid},
		},
		{
			name: "main fence can only accompany swap", repository: "R_repository",
			updates: []refUpdate{{
				Name: "refs/heads/main", BeforeOID: sha, AfterOID: sha,
			}},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := noTransport.updateRefsCAS(
				context.Background(), test.repository, test.updates,
			); err == nil {
				t.Fatal("invalid CAS input was accepted")
			}
		})
	}
}

func TestGetJSONPageOversizeAndTransportErrorsAreBounded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		roundTrip boundaryRoundTripper
		wantError string
	}{
		{
			name: "oversized response",
			roundTrip: func(*http.Request) (*http.Response, error) {
				return boundaryJSONResponse(http.StatusOK, strings.Repeat("x", 17)), nil
			},
			wantError: "response exceeds byte limit",
		},
		{
			name: "transport error redacted",
			roundTrip: func(*http.Request) (*http.Response, error) {
				return nil, io.ErrUnexpectedEOF
			},
			wantError: "GitHub API request failed",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := newBoundaryClient(t, 16, test.roundTrip)
			endpoint := client.endpoint("/items")
			var output []any
			_, err := client.getJSONPage(context.Background(), endpoint, &output)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}
