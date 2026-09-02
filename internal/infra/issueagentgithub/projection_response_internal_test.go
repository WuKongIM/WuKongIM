package issueagentgithub

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func projectionClient(
	t *testing.T,
	responses ...*http.Response,
) (*Client, *int) {
	t.Helper()
	index := 0
	client := newBoundaryClient(t, 1<<20, func(*http.Request) (*http.Response, error) {
		if index >= len(responses) {
			t.Fatalf("unexpected request %d", index+1)
		}
		response := responses[index]
		index++
		return response, nil
	})
	return client, &index
}

func validProjectionPull(number int64, state string, draft bool, headSHA string) string {
	return `{"number":` + strconv.FormatInt(number, 10) +
		`,"state":"` + state + `","draft":` + strconv.FormatBool(draft) +
		`,"mergeable":true,"merged":false,"base":{"ref":"main","sha":"` +
		strings.Repeat("a", 40) + `"},"head":{"ref":"agent/issue-42","sha":"` +
		headSHA + `"}}`
}

func TestProjectionInputsFailBeforeAnyGitHubWrite(t *testing.T) {
	t.Parallel()

	client := newBoundaryClient(t, 1<<20, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid projection input must not reach transport")
		return nil, nil
	})
	ctx := context.Background()
	if _, err := client.CreateIssueComment(ctx, 0, "body"); err == nil {
		t.Fatal("invalid comment input was accepted")
	}
	if _, err := client.UpdateIssueComment(ctx, 42, 0, "body"); err == nil {
		t.Fatal("invalid comment update was accepted")
	}
	if err := client.SetIssueLabelPresence(ctx, 42, "bad\nlabel", true); err == nil {
		t.Fatal("invalid label mutation was accepted")
	}
	if err := client.SetIssueLabels(ctx, 42, []string{"z", "a"}); err == nil {
		t.Fatal("unsorted label replacement was accepted")
	}
	if err := client.SetIssueLabels(ctx, 42, []string{"same", "same"}); err == nil {
		t.Fatal("duplicate label replacement was accepted")
	}
	if _, err := client.CreateDraftPullRequest(ctx, DraftPullRequest{
		Title: "title", Head: "human/branch", Base: "main",
	}); err == nil {
		t.Fatal("non-Agent pull request input was accepted")
	}
	if _, err := client.EnsureDraftPullRequest(ctx, DraftPullRequest{
		Title: "title", Head: "agent/issue-42", Base: "release",
	}); err == nil {
		t.Fatal("non-main pull request base was accepted")
	}
	if _, err := client.UpdatePullRequest(ctx, 0, "title", "body", "open"); err == nil {
		t.Fatal("invalid pull request update was accepted")
	}
	if _, err := client.MarkPullRequestReady(ctx, 0); err == nil {
		t.Fatal("invalid ready transition was accepted")
	}
	if _, err := client.CreateTrackingIssue(ctx, "", "body", nil); err == nil {
		t.Fatal("empty tracking Issue title was accepted")
	}
	if _, err := client.EnsureTrackingIssue(ctx, "bad\ntitle", "body"); err == nil {
		t.Fatal("unsafe tracking Issue identity was accepted")
	}
}

func TestCommentAndLabelWritesRejectInconsistentEchoes(t *testing.T) {
	t.Parallel()

	t.Run("created comment must be the exact Bot body", func(t *testing.T) {
		t.Parallel()
		client, calls := projectionClient(t, boundaryJSONResponse(
			http.StatusCreated,
			`{"id":51,"body":"different","user":{"login":"agent[bot]","type":"Bot"},"created_at":"2026-08-01T12:00:00Z","updated_at":"2026-08-01T12:00:00Z"}`,
		))
		_, err := client.CreateIssueComment(context.Background(), 42, "expected")
		if err == nil || !strings.Contains(err.Error(), "response is inconsistent") {
			t.Fatalf("CreateIssueComment() error = %v", err)
		}
		if *calls != 1 {
			t.Fatalf("calls = %d", *calls)
		}
	})

	t.Run("updated comment cannot move backwards in time", func(t *testing.T) {
		t.Parallel()
		client, _ := projectionClient(t, boundaryJSONResponse(
			http.StatusOK,
			`{"id":51,"body":"expected","user":{"login":"agent[bot]","type":"Bot"},"created_at":"2026-08-01T12:01:00Z","updated_at":"2026-08-01T12:00:00Z"}`,
		))
		_, err := client.UpdateIssueComment(
			context.Background(), 42, 51, "expected",
		)
		if err == nil || !strings.Contains(err.Error(), "response is inconsistent") {
			t.Fatalf("UpdateIssueComment() error = %v", err)
		}
	})

	t.Run("label replacement must echo exact set", func(t *testing.T) {
		t.Parallel()
		client, _ := projectionClient(t, boundaryJSONResponse(
			http.StatusOK,
			`[{"name":"bug"},{"name":"unexpected"}]`,
		))
		err := client.SetIssueLabels(
			context.Background(), 42, []string{"bug", "ready-for-agent"},
		)
		if err == nil || !strings.Contains(err.Error(), "does not match requested set") {
			t.Fatalf("SetIssueLabels() error = %v", err)
		}
	})
}

func TestPullRequestWritesRejectStaleOrMalformedEchoes(t *testing.T) {
	t.Parallel()

	head := strings.Repeat("b", 40)
	t.Run("create must remain Draft", func(t *testing.T) {
		t.Parallel()
		client, _ := projectionClient(t, boundaryJSONResponse(
			http.StatusCreated, validProjectionPull(9, "open", false, head),
		))
		_, err := client.CreateDraftPullRequest(
			context.Background(),
			DraftPullRequest{
				Title: "fix(agent): issue #42", Body: "summary",
				Head: "agent/issue-42", Base: "main",
			},
		)
		if err == nil || !strings.Contains(err.Error(), "created Draft pull request is inconsistent") {
			t.Fatalf("CreateDraftPullRequest() error = %v", err)
		}
	})

	t.Run("draft lookup rejects more than one identity", func(t *testing.T) {
		t.Parallel()
		pull := validProjectionPull(9, "open", true, head)
		client, _ := projectionClient(t, boundaryJSONResponse(
			http.StatusOK, `[`+pull+`,`+pull+`]`,
		))
		_, err := client.EnsureDraftPullRequest(
			context.Background(),
			DraftPullRequest{
				Title: "fix(agent): issue #42", Body: "summary",
				Head: "agent/issue-42", Base: "main",
			},
		)
		if err == nil || !strings.Contains(err.Error(), "ambiguous pull requests") {
			t.Fatalf("EnsureDraftPullRequest() error = %v", err)
		}
	})

	t.Run("existing draft identity must match requested projection", func(t *testing.T) {
		t.Parallel()
		body := strings.Replace(
			validProjectionPull(9, "open", true, head),
			`"number":9`,
			`"number":9,"title":"different","body":"summary"`,
			1,
		)
		client, _ := projectionClient(t, boundaryJSONResponse(http.StatusOK, `[`+body+`]`))
		_, err := client.EnsureDraftPullRequest(
			context.Background(),
			DraftPullRequest{
				Title: "fix(agent): issue #42", Body: "summary",
				Head: "agent/issue-42", Base: "main",
			},
		)
		if err == nil || !strings.Contains(err.Error(), "existing Agent pull request is inconsistent") {
			t.Fatalf("EnsureDraftPullRequest() error = %v", err)
		}
	})

	t.Run("updated projection must still be a valid pull request", func(t *testing.T) {
		t.Parallel()
		client, _ := projectionClient(t, boundaryJSONResponse(
			http.StatusOK, validProjectionPull(0, "open", false, head),
		))
		_, err := client.UpdatePullRequest(
			context.Background(), 9, "title", "body", "open",
		)
		if err == nil || !strings.Contains(err.Error(), "response is invalid") {
			t.Fatalf("UpdatePullRequest() error = %v", err)
		}
	})

	t.Run("ready transition rejects unchanged Draft", func(t *testing.T) {
		t.Parallel()
		client, _ := projectionClient(t, boundaryJSONResponse(
			http.StatusOK, validProjectionPull(9, "open", true, head),
		))
		_, err := client.MarkPullRequestReady(context.Background(), 9)
		if err == nil || !strings.Contains(err.Error(), "did not become ready") {
			t.Fatalf("MarkPullRequestReady() error = %v", err)
		}
	})

	t.Run("close transition rejects changed head", func(t *testing.T) {
		t.Parallel()
		current := validProjectionPull(9, "open", false, head)
		changed := validProjectionPull(9, "closed", false, strings.Repeat("c", 40))
		client, calls := projectionClient(
			t,
			boundaryJSONResponse(http.StatusOK, current),
			boundaryJSONResponse(http.StatusOK, changed),
		)
		_, err := client.EnsurePullRequestClosed(context.Background(), 9, head)
		if err == nil || !strings.Contains(err.Error(), "did not close exactly") {
			t.Fatalf("EnsurePullRequestClosed() error = %v", err)
		}
		if *calls != 2 {
			t.Fatalf("calls = %d, want 2", *calls)
		}
	})

	t.Run("ready transition rejects changed head", func(t *testing.T) {
		t.Parallel()
		current := validProjectionPull(9, "open", true, head)
		changed := validProjectionPull(9, "open", false, strings.Repeat("c", 40))
		client, _ := projectionClient(
			t,
			boundaryJSONResponse(http.StatusOK, current),
			boundaryJSONResponse(http.StatusOK, changed),
		)
		_, err := client.EnsurePullRequestReady(context.Background(), 9, head)
		if err == nil || !strings.Contains(err.Error(), "did not become exactly Ready") {
			t.Fatalf("EnsurePullRequestReady() error = %v", err)
		}
	})

	t.Run("draft lookup rejects GraphQL identity drift", func(t *testing.T) {
		t.Parallel()
		current := validProjectionPull(9, "open", false, head)
		client, _ := projectionClient(
			t,
			boundaryJSONResponse(http.StatusOK, current),
			boundaryJSONResponse(http.StatusOK, `{"data":{"repository":{"pullRequest":{"id":"PR_9","isDraft":false,"headRefOid":"`+strings.Repeat("c", 40)+`"}}}}`),
		)
		_, err := client.EnsurePullRequestDraft(context.Background(), 9, head)
		if err == nil || !strings.Contains(err.Error(), "draft lookup is inconsistent") {
			t.Fatalf("EnsurePullRequestDraft() error = %v", err)
		}
	})
}

func TestTrackingIssueWritesRejectAmbiguousOrMismatchedIdentity(t *testing.T) {
	t.Parallel()

	t.Run("create echo mismatch", func(t *testing.T) {
		t.Parallel()
		client, _ := projectionClient(t, boundaryJSONResponse(
			http.StatusCreated,
			`{"number":84,"title":"different","body":"body"}`,
		))
		_, err := client.CreateTrackingIssue(
			context.Background(), "Backport issue #42", "body", []string{},
		)
		if err == nil || !strings.Contains(err.Error(), "response is inconsistent") {
			t.Fatalf("CreateTrackingIssue() error = %v", err)
		}
	})

	t.Run("search rejects duplicate exact matches", func(t *testing.T) {
		t.Parallel()
		client, _ := projectionClient(t, boundaryJSONResponse(
			http.StatusOK,
			`{"total_count":2,"items":[{"number":84,"title":"Backport issue #42","body":"body"},{"number":85,"title":"Backport issue #42","body":"body"}]}`,
		))
		_, err := client.EnsureTrackingIssue(
			context.Background(), "Backport issue #42", "body",
		)
		if err == nil || !strings.Contains(err.Error(), "identity is ambiguous") {
			t.Fatalf("EnsureTrackingIssue() error = %v", err)
		}
	})

	t.Run("search must be one bounded page", func(t *testing.T) {
		t.Parallel()
		response := boundaryJSONResponse(http.StatusOK, `{"total_count":0,"items":[]}`)
		response.Header.Set(
			"Link",
			`<https://api.example.test/v3/search/issues?page=2&per_page=100>; rel="next"`,
		)
		client, _ := projectionClient(t, response)
		_, err := client.EnsureTrackingIssue(
			context.Background(), "Backport issue #42", "body",
		)
		if err == nil || !strings.Contains(err.Error(), "search exceeds bound") {
			t.Fatalf("EnsureTrackingIssue() error = %v", err)
		}
	})
}
