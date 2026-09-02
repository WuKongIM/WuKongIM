package reviewagentgithub

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	usecase "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
)

func TestProjectionWritesRejectInvalidAuthorityBeforeTransport(t *testing.T) {
	t.Parallel()

	requests := 0
	client := newMemoryClient(t, 1<<20, func(*http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("transport must not receive invalid projection")
	})
	head := strings.Repeat("a", 40)
	conclusion := usecase.CheckSuccess
	tooManyComments := make([]InlineReviewComment, contract.MaxInlineComments+1)

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "comment identity",
			call: func() error {
				_, err := client.CreateIssueComment(context.Background(), 0, "status")
				return err
			},
		},
		{
			name: "comment update identity",
			call: func() error {
				return client.UpdateIssueComment(context.Background(), 0, "status")
			},
		},
		{
			name: "review head identity",
			call: func() error {
				_, err := client.CreateReview(
					context.Background(), 42, "not-a-sha", usecase.FormalReviewComment, "review", nil,
				)
				return err
			},
		},
		{
			name: "review event",
			call: func() error {
				_, err := client.CreateReview(
					context.Background(), 42, head, usecase.FormalReview("MERGE"), "review", nil,
				)
				return err
			},
		},
		{
			name: "inline review coordinate",
			call: func() error {
				_, err := client.CreateReview(
					context.Background(), 42, head, usecase.FormalReviewComment, "review",
					[]InlineReviewComment{{Path: "fix.go", Line: 0, Body: "finding"}},
				)
				return err
			},
		},
		{
			name: "inline review budget",
			call: func() error {
				_, err := client.CreateReview(
					context.Background(), 42, head, usecase.FormalReviewComment, "review", tooManyComments,
				)
				return err
			},
		},
		{
			name: "merge head identity",
			call: func() error {
				return client.MergePullRequest(context.Background(), 42, "not-a-sha")
			},
		},
		{
			name: "check conclusion",
			call: func() error {
				_, err := client.CreateCheckRun(
					context.Background(), head, "generation", usecase.CheckConclusion("neutral"), "title", "summary",
				)
				return err
			},
		},
		{
			name: "check output",
			call: func() error {
				_, err := client.CreateCheckRun(
					context.Background(), head, "generation", usecase.CheckSuccess, "", "summary",
				)
				return err
			},
		},
		{
			name: "check update identity",
			call: func() error {
				return client.UpdateCheckRun(
					context.Background(), 0, usecase.CheckSuccess, "title", "summary",
				)
			},
		},
		{
			name: "lifecycle status",
			call: func() error {
				_, err := client.CreateLifecycleCheckRun(
					context.Background(), head, "lifecycle", "queued", nil, "title", "summary",
				)
				return err
			},
		},
		{
			name: "lifecycle conclusion",
			call: func() error {
				_, err := client.CreateLifecycleCheckRun(
					context.Background(), head, "lifecycle", "in_progress", &conclusion, "title", "summary",
				)
				return err
			},
		},
		{
			name: "lifecycle identity",
			call: func() error {
				_, err := client.CreateLifecycleCheckRun(
					context.Background(), "not-a-sha", "lifecycle", "in_progress", nil, "title", "summary",
				)
				return err
			},
		},
		{
			name: "lifecycle update identity",
			call: func() error {
				return client.UpdateLifecycleCheckRun(
					context.Background(), 0, "completed", &conclusion, "title", "summary",
				)
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); err == nil {
				t.Fatal("projection call unexpectedly accepted invalid authority")
			}
		})
	}
	if requests != 0 {
		t.Fatalf("transport requests = %d, want 0", requests)
	}
}

func TestProjectionWriteResponsesMustEchoCreatedOrUpdatedIdentity(t *testing.T) {
	t.Parallel()

	client := newMemoryClient(t, 1<<20, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/issues/42/comments"):
			return jsonResponse(http.StatusCreated, `{"id":0}`), nil
		case request.Method == http.MethodPatch && strings.Contains(request.URL.Path, "/issues/comments/"):
			return jsonResponse(http.StatusOK, `{"id":99}`), nil
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/pulls/42/reviews"):
			return jsonResponse(http.StatusOK, `{"id":0}`), nil
		case request.Method == http.MethodPut && strings.HasSuffix(request.URL.Path, "/pulls/42/merge"):
			return jsonResponse(http.StatusOK, `{"sha":"bad","merged":true,"message":"merged"}`), nil
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/check-runs"):
			return jsonResponse(http.StatusCreated, `{"id":0}`), nil
		case request.Method == http.MethodPatch && strings.Contains(request.URL.Path, "/check-runs/"):
			return jsonResponse(http.StatusOK, `{"id":99}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	})
	ctx := context.Background()
	head := strings.Repeat("a", 40)

	if _, err := client.CreateIssueComment(ctx, 42, "status"); err == nil {
		t.Fatal("CreateIssueComment() accepted missing response identity")
	}
	if err := client.UpdateIssueComment(ctx, 7, "status"); err == nil {
		t.Fatal("UpdateIssueComment() accepted mismatched response identity")
	}
	if _, err := client.CreateReview(
		ctx, 42, head, usecase.FormalReviewComment, "review", nil,
	); err == nil {
		t.Fatal("CreateReview() accepted missing response identity")
	}
	if err := client.MergePullRequest(ctx, 42, head); err == nil {
		t.Fatal("MergePullRequest() accepted malformed merge identity")
	}
	if _, err := client.CreateCheckRun(
		ctx, head, "generation", usecase.CheckSuccess, "title", "summary",
	); err == nil {
		t.Fatal("CreateCheckRun() accepted missing response identity")
	}
	if err := client.UpdateCheckRun(
		ctx, 7, usecase.CheckSuccess, "title", "summary",
	); err == nil {
		t.Fatal("UpdateCheckRun() accepted mismatched response identity")
	}
	if _, err := client.CreateLifecycleCheckRun(
		ctx, head, "lifecycle", "in_progress", nil, "title", "summary",
	); err == nil {
		t.Fatal("CreateLifecycleCheckRun() accepted missing response identity")
	}
	conclusion := usecase.CheckActionRequired
	if err := client.UpdateLifecycleCheckRun(
		ctx, 7, "completed", &conclusion, "title", "summary",
	); err == nil {
		t.Fatal("UpdateLifecycleCheckRun() accepted mismatched response identity")
	}
}

func TestProjectionTransportErrorsStopBeforeIdentityAcceptance(t *testing.T) {
	t.Parallel()

	client := newMemoryClient(t, 1<<20, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusServiceUnavailable, `{"message":"unavailable"}`), nil
	})
	head := strings.Repeat("a", 40)
	ctx := context.Background()

	if _, err := client.CreateIssueComment(ctx, 42, "status"); err == nil {
		t.Fatal("CreateIssueComment() accepted failed API response")
	}
	if err := client.UpdateIssueComment(ctx, 7, "status"); err == nil {
		t.Fatal("UpdateIssueComment() accepted failed API response")
	}
	if _, err := client.CreateReview(
		ctx, 42, head, usecase.FormalReviewComment, "review", nil,
	); err == nil {
		t.Fatal("CreateReview() accepted failed API response")
	}
	if err := client.MergePullRequest(ctx, 42, head); err == nil {
		t.Fatal("MergePullRequest() accepted failed API response")
	}
	if _, err := client.CreateCheckRun(
		ctx, head, "generation", usecase.CheckSuccess, "title", "summary",
	); err == nil {
		t.Fatal("CreateCheckRun() accepted failed API response")
	}
	if err := client.UpdateCheckRun(
		ctx, 7, usecase.CheckSuccess, "title", "summary",
	); err == nil {
		t.Fatal("UpdateCheckRun() accepted failed API response")
	}
	if _, err := client.CreateLifecycleCheckRun(
		ctx, head, "lifecycle", "in_progress", nil, "title", "summary",
	); err == nil {
		t.Fatal("CreateLifecycleCheckRun() accepted failed API response")
	}
	conclusion := usecase.CheckActionRequired
	if err := client.UpdateLifecycleCheckRun(
		ctx, 7, "completed", &conclusion, "title", "summary",
	); err == nil {
		t.Fatal("UpdateLifecycleCheckRun() accepted failed API response")
	}
}
