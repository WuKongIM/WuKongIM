package reviewagentgithub

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestStateRefHeadFailsClosedOnProtocolErrors(t *testing.T) {
	t.Parallel()

	branch := "review-state/pr-42"
	validBody := `{"ref":"refs/heads/` + branch +
		`","object":{"type":"commit","sha":"` + strings.Repeat("a", 40) + `"}}`

	t.Run("invalid ref name", func(t *testing.T) {
		client := newMemoryClient(t, 1024, func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid ref must not reach the transport")
			return nil, nil
		})

		_, _, err := client.StateRefHead(context.Background(), "review-state/pr-042")
		if err == nil || err.Error() != "Review state ref name is invalid" {
			t.Fatalf("StateRefHead() error = %v", err)
		}
	})

	t.Run("canceled transport", func(t *testing.T) {
		client := newMemoryClient(t, 1024, func(request *http.Request) (*http.Response, error) {
			return nil, request.Context().Err()
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, _, err := client.StateRefHead(ctx, branch)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("StateRefHead() error = %v, want context.Canceled", err)
		}
	})

	tests := []struct {
		name     string
		maxBytes int64
		response func() *http.Response
		want     string
	}{
		{
			name:     "non-success status",
			maxBytes: 1024,
			response: func() *http.Response {
				return jsonResponse(http.StatusForbidden, `{"message":"forbidden"}`)
			},
			want: "GitHub API returned status 403",
		},
		{
			name:     "unexpected content type",
			maxBytes: 1024,
			response: func() *http.Response {
				response := jsonResponse(http.StatusOK, validBody)
				response.Header.Set("Content-Type", "text/plain")
				return response
			},
			want: "GitHub API returned unexpected content type",
		},
		{
			name:     "oversized body",
			maxBytes: 32,
			response: func() *http.Response {
				return jsonResponse(http.StatusOK, validBody)
			},
			want: "GitHub API response exceeds byte limit",
		},
		{
			name:     "malformed JSON",
			maxBytes: 1024,
			response: func() *http.Response {
				return jsonResponse(http.StatusOK, `{"ref":`)
			},
			want: "decode GitHub API response",
		},
		{
			name:     "trailing JSON",
			maxBytes: 1024,
			response: func() *http.Response {
				return jsonResponse(http.StatusOK, validBody+` {}`)
			},
			want: "GitHub API response contains trailing JSON",
		},
		{
			name:     "mismatched response identity",
			maxBytes: 1024,
			response: func() *http.Response {
				return jsonResponse(
					http.StatusOK,
					strings.Replace(validBody, branch, "review-state/pr-43", 1),
				)
			},
			want: "Review state ref response is invalid",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newMemoryClient(t, test.maxBytes, func(*http.Request) (*http.Response, error) {
				return test.response(), nil
			})

			_, _, err := client.StateRefHead(context.Background(), branch)
			if err == nil || err.Error() != test.want {
				t.Fatalf("StateRefHead() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestStateRefVisibilityCancellationDoesNotWaitForTheRetryDelay(t *testing.T) {
	t.Parallel()

	branch := "review-state/pr-42"
	parentSHA := strings.Repeat("a", 40)
	newSHA := strings.Repeat("b", 40)
	ctx, cancel := context.WithCancel(context.Background())
	body := `{"ref":"refs/heads/` + branch +
		`","object":{"type":"commit","sha":"` + parentSHA + `"}}`
	client := newMemoryClient(t, 1024, func(*http.Request) (*http.Response, error) {
		response := jsonResponse(http.StatusOK, body)
		response.Body = &cancelOnCloseBody{
			Reader: response.Body,
			cancel: cancel,
		}
		return response, nil
	})

	err := client.waitForStateRefHead(
		ctx,
		branch,
		parentSHA,
		newSHA,
		2,
		time.Hour,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForStateRefHead() error = %v, want context.Canceled", err)
	}
}

func TestValidStatePathAcceptsOnlyCanonicalStateDocuments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want bool
	}{
		{path: ".review-agent-state/scheduler.json", want: true},
		{path: ".review-agent-state/pr-42.json", want: true},
		{path: ".review-agent-state/pr-042.json", want: false},
		{path: ".review-agent-state/pr-0.json", want: false},
		{path: ".review-agent-state/pr-9223372036854775808.json", want: false},
		{path: ".review-agent-state/pr-42.json/extra", want: false},
		{path: "review-agent-state/pr-42.json", want: false},
	}
	for _, test := range tests {
		if got := validStatePath(test.path); got != test.want {
			t.Errorf("validStatePath(%q) = %t, want %t", test.path, got, test.want)
		}
	}
}

type cancelOnCloseBody struct {
	io.Reader
	cancel context.CancelFunc
}

func (body *cancelOnCloseBody) Close() error {
	body.cancel()
	return nil
}
