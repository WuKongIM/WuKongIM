package issueagentgithub

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func oneReadClient(t *testing.T, body string) *Client {
	t.Helper()
	return newBoundaryClient(t, 1<<20, func(*http.Request) (*http.Response, error) {
		return boundaryJSONResponse(http.StatusOK, body), nil
	})
}

func TestRepositoryReadInputsFailBeforeTransport(t *testing.T) {
	t.Parallel()

	client := newBoundaryClient(t, 1<<20, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid repository read must not reach transport")
		return nil, nil
	})
	ctx := context.Background()
	if _, err := client.Issue(ctx, 0); err == nil {
		t.Fatal("invalid Issue number was accepted")
	}
	if _, err := client.IssueComment(ctx, 0, 42); err == nil {
		t.Fatal("invalid comment identity was accepted")
	}
	if _, err := client.ActorPermission(ctx, "bad/login"); err == nil {
		t.Fatal("invalid actor login was accepted")
	}
	if _, err := client.PullRequest(ctx, 0); err == nil {
		t.Fatal("invalid pull request number was accepted")
	}
	if _, err := client.Ref(ctx, "main"); err == nil {
		t.Fatal("unmanaged ref was accepted")
	}
	if _, _, err := client.RefIfExists(ctx, "main"); err == nil {
		t.Fatal("unmanaged optional ref was accepted")
	}
	if _, _, err := client.ResolveTreePath(ctx, "bad-sha", "pkg/file.go"); err == nil {
		t.Fatal("invalid tree identity was accepted")
	}
	sha := strings.Repeat("a", 40)
	if _, err := client.CompareCandidate(ctx, sha, sha, 1); err == nil {
		t.Fatal("self comparison was accepted")
	}
	if _, err := client.Commit(ctx, "bad-sha"); err == nil {
		t.Fatal("invalid commit identity was accepted")
	}
	if _, err := client.CommitAttribution(ctx, "bad-sha"); err == nil {
		t.Fatal("invalid attribution identity was accepted")
	}
	if err := (*Client)(nil).getJSON(ctx, "/items", &struct{}{}); err == nil {
		t.Fatal("nil client was accepted")
	}
}

func TestRepositoryReadersRejectMalformedObjectEchoes(t *testing.T) {
	t.Parallel()

	sha := strings.Repeat("a", 40)
	other := strings.Repeat("b", 40)
	tests := []struct {
		name      string
		invoke    func(*Client) error
		body      string
		wantError string
	}{
		{
			name: "duplicate Issue labels",
			invoke: func(client *Client) error {
				_, err := client.Issue(context.Background(), 42)
				return err
			},
			body:      `{"node_id":"I_42","number":42,"state":"open","title":"title","body":"body","user":{"login":"reporter"},"labels":[{"name":"bug"},{"name":"bug"}]}`,
			wantError: "labels contain a duplicate",
		},
		{
			name: "unknown actor permission",
			invoke: func(client *Client) error {
				_, err := client.ActorPermission(context.Background(), "maintainer")
				return err
			},
			body:      `{"permission":"owner","user":{"login":"maintainer"}}`,
			wantError: "permission is unknown",
		},
		{
			name: "actor identity drift",
			invoke: func(client *Client) error {
				_, err := client.ActorPermission(context.Background(), "maintainer")
				return err
			},
			body:      `{"permission":"write","user":{"login":"other"}}`,
			wantError: "identity mismatch",
		},
		{
			name: "merged PR must be closed",
			invoke: func(client *Client) error {
				_, err := client.PullRequest(context.Background(), 9)
				return err
			},
			body:      `{"number":9,"state":"open","draft":false,"merged":true,"merge_commit_sha":"` + other + `","base":{"ref":"main","sha":"` + sha + `"},"head":{"ref":"agent/issue-42","sha":"` + other + `"}}`,
			wantError: "pull request response is invalid",
		},
		{
			name: "ref object must be a commit",
			invoke: func(client *Client) error {
				_, err := client.Ref(context.Background(), "agent/issue-42")
				return err
			},
			body:      `{"ref":"refs/heads/agent/issue-42","object":{"type":"tag","sha":"` + sha + `"}}`,
			wantError: "ref response is invalid",
		},
		{
			name: "commit parent must be a Git object",
			invoke: func(client *Client) error {
				_, err := client.Commit(context.Background(), sha)
				return err
			},
			body:      `{"sha":"` + sha + `","tree":{"sha":"` + other + `"},"parents":[{"sha":"invalid"}],"verification":{"verified":true,"reason":"valid"}}`,
			wantError: "commit parent is invalid",
		},
		{
			name: "commit attribution requires GitHub author",
			invoke: func(client *Client) error {
				_, err := client.CommitAttribution(context.Background(), sha)
				if !errors.Is(err, ErrUntrustedCommit) {
					return errors.New("attribution error was not classified")
				}
				return err
			},
			body:      `{"sha":"` + sha + `","author":null}`,
			wantError: "attribution is invalid",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.invoke(oneReadClient(t, test.body))
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}

func TestTreeReaderRejectsIncompleteOrCaseAmbiguousPaths(t *testing.T) {
	t.Parallel()

	root := strings.Repeat("a", 40)
	child := strings.Repeat("b", 40)
	tests := []struct {
		name      string
		body      string
		wantFound bool
		wantError string
	}{
		{
			name:      "truncated tree",
			body:      `{"sha":"` + root + `","truncated":true,"tree":[]}`,
			wantError: "response is incomplete",
		},
		{
			name:      "case collision",
			body:      `{"sha":"` + root + `","truncated":false,"tree":[{"path":"Docs","mode":"040000","type":"tree","sha":"` + child + `"}]}`,
			wantError: "case-colliding path",
		},
		{
			name:      "non-directory traversal",
			body:      `{"sha":"` + root + `","truncated":false,"tree":[{"path":"docs","mode":"100644","type":"blob","sha":"` + child + `"}]}`,
			wantError: "traverses a non-directory",
		},
		{
			name:      "missing path",
			body:      `{"sha":"` + root + `","truncated":false,"tree":[]}`,
			wantFound: false,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			entry, found, err := oneReadClient(t, test.body).ResolveTreePath(
				context.Background(), root, "docs/file.go",
			)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error = %v, want substring %q", err, test.wantError)
				}
				return
			}
			if err != nil || found != test.wantFound || entry != (TreeEntryFacts{}) {
				t.Fatalf("entry = %#v, found = %v, error = %v", entry, found, err)
			}
		})
	}
}

func TestSingletonReadRejectsUnexpectedPagination(t *testing.T) {
	t.Parallel()

	client := newBoundaryClient(t, 4096, func(*http.Request) (*http.Response, error) {
		response := boundaryJSONResponse(http.StatusOK, `{}`)
		response.Header.Set(
			"Link",
			`<https://api.example.test/v3/repos/WuKongIM/WuKongIM/issues/42?page=2>; rel="next"`,
		)
		return response, nil
	})
	var output map[string]any
	err := client.getJSON(
		context.Background(), "/repos/WuKongIM/WuKongIM/issues/42", &output,
	)
	if err == nil || !strings.Contains(err.Error(), "unexpectedly paginated") {
		t.Fatalf("getJSON() error = %v", err)
	}
}

func TestJSONTimeRejectsNilDestination(t *testing.T) {
	t.Parallel()

	var value *jsonTime
	if err := value.UnmarshalJSON([]byte(`"2026-08-01T00:00:00Z"`)); err == nil {
		t.Fatal("nil JSON time destination was accepted")
	}
}
